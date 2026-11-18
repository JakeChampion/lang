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
// This module finds the audio decoders of an Android device.
import "std/android/video/capabilities" as shared;
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

function request_format(env: usize, request: Request): Result[usize, frame.VideoError] {
  let format = shared.media_format(env, "createAudioFormat", request.mime, request.sample_rate, request.channels)?;
  shared.set_positive(env, format, if (request.mime == "audio/mp4a-latm") { "aac-profile" } else { "profile" }, request.profile)?;
  return Ok(format);
}

function inspect(env: usize, info: usize, format: usize, request: Request, api_level: i32): Result[Option[CodecInfo], frame.VideoError] {
  shared.push(env)?;
  defer shared.pop(env);
  if (shared.read_boolean(env, info, "isEncoder")? || !shared.supports_type(env, info, request.mime)?) {
    return Ok(None);
  }
  let caps = shared.codec_caps(env, info, request.mime)?;
  let Some(supported) = shared.format_supported(env, caps, format)? else {
    return Ok(None);
  };
  let attr = shared.attributes(env, info, api_level)?;
  return Ok(Some(CodecInfo { name: shared.codec_name(env, info)?, request: request, implementation: attr.implementation, vendor: attr.vendor, alias: attr.alias, supported: supported }));
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
  let infos = shared.codec_list(env)?;
  let count = jni.get_array_length(env, infos);
  if (!shared.healthy(env)) {
    return Err(frame.Platform);
  }
  if (count < 0 || count > max_codecs) {
    return Err(frame.Limit);
  }
  return collect(env, infos, count, request, api_level);
}

function collect(env: usize, infos: usize, count: i32, request: Request, api_level: i32): Result[CodecInfo[], frame.VideoError] {
  let format = request_format(env, request)?;
  let out: CodecInfo[] = [];
  for i in 0..count {
    let info = jni.get_object_array_element(env, infos, i);
    if (!shared.healthy(env) || info == 0 as usize) {
      return Err(frame.Platform);
    }
    let result = inspect(env, info, format, request, api_level);
    let _ = jni.delete_local_ref(env, info);
    if let Some(value) = result? {
      out = out.append(value);
    }
  }
  return Ok(out);
}
```
