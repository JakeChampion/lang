# Android audio codec capabilities

This module discovers regular audio decoders through MediaCodecList on API 21 and later.
The request includes the MIME type, sample rate, channel count, and optional profile.
Discovery checks `isFormatSupported`. It does not guarantee that configuration will succeed.

The module shares JNI helpers, implementation classes, policies, and errors with the video API.
Import `std/android/video/capabilities` for `Policy` and `Implementation`.
API 29 supplies hardware and software attributes. Earlier versions report `Unknown`.
Codec names do not prove hardware acceleration. Audio offload is a separate platform feature.

Call discovery with an attached JNI environment and no pending Java exception.
Limits bound the codec list. They do not bound memory inside Android codecs.

```fern
<<*>>=
// This module discovers Android audio decoders and orders supported candidates.
import "std/android/video/capabilities" as shared;
import "std/android/video/capabilities/bindings";
import "std/video/frame";
import "std/jni";
import "core/cmp";

@derive(cmp.Eq, cmp.Debug)
pub struct Request { mime: string, sample_rate: i32, channels: i32, profile: i32 }

pub struct CodecInfo { name: string, request: Request, implementation: shared.Implementation, vendor: boolean, alias: boolean, supported: boolean }

pub function valid(request: Request): boolean {
  if (!request.mime.starts_with("audio/") || request.mime.len() < 7 || request.mime.len() > 128 || request.sample_rate < 1 || request.sample_rate > 768000 || request.channels < 1 || request.channels > 8 || request.profile < 0) {
    return false;
  }
  for b in request.mime.as_bytes() {
    if (b == 0u8) {
      return false;
    }
  }
  return true;
}

pub function ordered(infos: [CodecInfo], request: Request, policy: shared.Policy): CodecInfo[] {
  let out: CodecInfo[] = [];
  for rank in 0..3 {
    for info in infos {
      if (info.supported && !info.alias && info.name.len() > 0 && info.request == request && shared.policy_rank(info.implementation, policy) == rank) {
        let duplicate = false;
        for previous in out {
          if (previous.name == info.name) {
            duplicate = true;
          }
        }
        if (!duplicate) {
          out = out.append(info);
        }
      }
    }
  }
  return out;
}

function request_format(env: usize, c: bindings.Classes, request: Request): Result[bindings.MediaFormat, frame.VideoError] {
  let format = shared.platform(c.media_format.create_audio_format(env, request.mime, request.sample_rate, request.channels))?;
  if (format.is_null()) {
    return Err(frame.Platform);
  }
  shared.set_positive(env, c, format, if (request.mime == "audio/mp4a-latm") { "aac-profile" } else { "profile" }, request.profile)?;
  return Ok(format);
}

function inspect(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo, format: bindings.MediaFormat, request: Request, api_level: i32): Result[Option[CodecInfo], frame.VideoError] {
  shared.push(env)?;
  defer shared.pop(env);
  if (shared.is_encoder(env, c, info)? || !shared.supports_type(env, c, info, request.mime)?) {
    return Ok(None);
  }
  let caps = shared.codec_caps(env, c, info, request.mime)?;
  let Some(supported) = shared.format_supported(env, c, caps, format) else {
    return Ok(None);
  };
  let attr = shared.attributes(env, c, info, api_level)?;
  return Ok(Some(CodecInfo { name: shared.codec_name(env, c, info)?, request: request, implementation: attr.implementation, vendor: attr.vendor, alias: attr.alias, supported: supported }));
}

function validate_discovery(env: usize, api_level: i32, request: Request, max_codecs: i32): Result[void, frame.VideoError] {
  if (env == 0 as usize || api_level < 21 || !valid(request)) {
    return Err(frame.Header);
  }
  if (max_codecs < 0 || max_codecs > 4096) {
    return Err(frame.Limit);
  }
  let pending = jni.exception_occurred(env);
  if (pending != 0 as usize) {
    let _ = jni.delete_local_ref(env, pending);
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function discover(env: usize, api_level: i32, request: Request, max_codecs: i32): Result[CodecInfo[], frame.VideoError] {
  validate_discovery(env, api_level, request, max_codecs)?;
  shared.push(env)?;
  defer shared.pop(env);
  let c = shared.classes(env)?;
  defer c.release(env);
  let infos = shared.codec_infos(env, c)?;
  let count = shared.length(env, infos, max_codecs)?;
  return collect(env, c, infos, count, request, api_level);
}

function collect(env: usize, c: bindings.Classes, infos: jni.Object, count: i32, request: Request, api_level: i32): Result[CodecInfo[], frame.VideoError] {
  let format = request_format(env, c, request)?;
  let out: CodecInfo[] = [];
  for i in 0..count {
    let info = shared.info_at(env, infos, i)?;
    let result = inspect(env, c, info, format, request, api_level);
    let _ = jni.delete_local_ref(env, info.ref);
    if let Some(value) = result? {
      out = out.append(value);
    }
  }
  return Ok(out);
}
```
