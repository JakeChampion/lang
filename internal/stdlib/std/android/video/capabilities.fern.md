# Android video codec capabilities

Discovery uses MediaCodecList through the caller's attached JNI environment.
The module calls Java only through `std/android/video/capabilities/bindings`, which `jnibind` writes from `bindings.spec`.
The regular codec list excludes codecs that require secure or tunneled operation.
Android 29 or later supplies hardware, software, vendor, and alias attributes.
Earlier versions report `Unknown` and never infer hardware from a codec name.

`decoder_types` lists each MIME type that a regular decoder takes, for audio and for video.
A player can compare a stream with this list before it plays the stream as it is.

Each result describes support for one exact request through `isFormatSupported`.
The caller must provide the stream profile and level when those are known.
A support report does not replace a successful configuration or a device test.
Discovery clears exceptions that it handles and bounds local JNI references.

```fern
<<*>>=
// This module discovers Android video codecs and orders supported candidates.
import "std/jni";
import "std/android/video/capabilities/bindings";
import "std/video/frame";
import "core/cmp";

@derive(cmp.Eq, cmp.Debug)
pub enum Implementation { Hardware, Software, Unknown }

@derive(cmp.Eq, cmp.Debug)
pub enum Policy { PreferHardware, HardwareOnly, SoftwareOnly, PlatformOrder }

@derive(cmp.Eq, cmp.Debug)
pub struct Request { mime: string, width: i32, height: i32, encoder: boolean, frame_rate: i32, bitrate: i32, profile: i32, level: i32, color_format: i32 }

@derive(cmp.Eq, cmp.Debug)
pub struct ProfileLevel { profile: i32, level: i32 }

pub struct CodecInfo { name: string, request: Request, implementation: Implementation, vendor: boolean, alias: boolean, supported: boolean, profiles: ProfileLevel[], color_formats: i32[] }

pub struct Limits { max_codecs: i32, max_profiles: i32, max_color_formats: i32 }

pub function default_limits(): Limits {
  return Limits { max_codecs: 256, max_profiles: 256, max_color_formats: 256 };
}

function valid_mime(mime: string): boolean {
  if (mime.len() < 7 || mime.len() > 128) {
    return false;
  }
  for b in mime.as_bytes() {
    if (b == 0) {
      return false;
    }
  }
  let prefix = "video/".as_bytes();
  let bytes = mime.as_bytes();
  for i in 0..6 {
    if (bytes[i] != prefix[i]) {
      return false;
    }
  }
  return true;
}

pub function valid(request: Request): boolean {
  return valid_mime(request.mime) && request.width > 0 && request.height > 0 && request.frame_rate >= 0 && request.bitrate >= 0 && request.profile >= 0 && request.level >= 0 && request.color_format >= 0;
}

function admitted(info: CodecInfo, request: Request): boolean {
  return info.supported && !info.alias && info.name.len() > 0 && info.request == request;
}

pub(package) function policy_rank(implementation: Implementation, policy: Policy): i32 {
  return match (policy) {
    HardwareOnly => if (implementation == Hardware) { 0 } else { -1 },
    SoftwareOnly => if (implementation == Software) { 0 } else { -1 },
    PlatformOrder => 0,
    PreferHardware => match (implementation) { Hardware => 0, Software => 1, Unknown => 2 },
  };
}

function contains(infos: [CodecInfo], name: string): boolean {
  for info in infos {
    if (info.name == name) {
      return true;
    }
  }
  return false;
}

pub function ordered(infos: [CodecInfo], request: Request, policy: Policy): CodecInfo[] {
  let out: CodecInfo[] = [];
  for rank in 0..3 {
    for info in infos {
      if (admitted(info, request) && policy_rank(info.implementation, policy) == rank && !contains(out, info.name)) {
        out = out.append(info);
      }
    }
  }
  return out;
}

pub(package) function pop(env: usize): void {
  let _ = jni.pop_local_frame(env, 0 as usize);
}

function healthy(env: usize): boolean {
  let exception = jni.exception_occurred(env);
  if (exception == 0 as usize) {
    return true;
  }
  let _ = jni.exception_clear(env);
  let _ = jni.delete_local_ref(env, exception);
  return false;
}

// The result of a wrapper. An error of Java is `Platform`.
pub(package) function platform[T](got: Result[T, jni.JavaError]): Result[T, frame.VideoError] {
  return match (got) {
    Ok(value) => Ok(value),
    Err(_) => Err(frame.Platform),
  };
}

// The classes that the discovery calls. Release them before the local frame ends.
pub(package) function classes(env: usize): Result[bindings.Classes, frame.VideoError] {
  return platform(bindings.classes(env));
}

// The length of `array`, which is at most `maximum`.
pub(package) function length(env: usize, array: jni.Object, maximum: i32): Result[i32, frame.VideoError] {
  let count = jni.get_array_length(env, array.ref);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  if (count < 0 || count > maximum) {
    return Err(frame.Limit);
  }
  return Ok(count);
}

// A local reference to element `i` of `array`.
function element(env: usize, array: jni.Object, i: i32): Result[usize, frame.VideoError] {
  let item = jni.get_object_array_element(env, array.ref, i);
  if (!healthy(env) || item == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(item);
}

// Codec `i` of the array `infos` that `codec_infos` gives.
pub(package) function info_at(env: usize, infos: jni.Object, i: i32): Result[bindings.MediaCodecInfo, frame.VideoError] {
  return Ok(bindings.MediaCodecInfo { ref: element(env, infos, i)? });
}

pub(package) function set_positive(env: usize, c: bindings.Classes, format: bindings.MediaFormat, key: string, value: i32): Result[void, frame.VideoError] {
  if (value > 0) {
    platform(c.media_format.set_integer(env, format, key, value))?;
  }
  return Ok(());
}

function request_format(env: usize, c: bindings.Classes, request: Request): Result[bindings.MediaFormat, frame.VideoError] {
  let format = platform(c.media_format.create_video_format(env, request.mime, request.width, request.height))?;
  if (format.is_null()) {
    return Err(frame.Platform);
  }
  set_positive(env, c, format, "frame-rate", request.frame_rate)?;
  set_positive(env, c, format, "bitrate", request.bitrate)?;
  set_positive(env, c, format, "profile", request.profile)?;
  set_positive(env, c, format, "level", request.level)?;
  set_positive(env, c, format, "color-format", request.color_format)?;
  return Ok(format);
}

function profiles(env: usize, c: bindings.Classes, caps: bindings.MediaCodecInfo_CodecCapabilities, limits: Limits): Result[ProfileLevel[], frame.VideoError] {
  let array = platform(c.media_codec_info_codec_capabilities.profile_levels(env, caps))?;
  if (array.is_null()) {
    return Err(frame.Platform);
  }
  let count = length(env, array, limits.max_profiles)?;
  let out: ProfileLevel[] = [];
  for i in 0..count {
    let item = bindings.MediaCodecInfo_CodecProfileLevel { ref: element(env, array, i)? };
    let profile = platform(c.media_codec_info_codec_profile_level.profile(env, item))?;
    let level = platform(c.media_codec_info_codec_profile_level.level(env, item))?;
    out = out.append(ProfileLevel { profile: profile, level: level });
    let _ = jni.delete_local_ref(env, item.ref);
  }
  return Ok(out);
}

function colors(env: usize, c: bindings.Classes, caps: bindings.MediaCodecInfo_CodecCapabilities, limits: Limits): Result[i32[], frame.VideoError] {
  let array = platform(c.media_codec_info_codec_capabilities.color_formats(env, caps))?;
  if (array.is_null()) {
    return Err(frame.Platform);
  }
  let count = length(env, array, limits.max_color_formats)?;
  let out = jni.int_array_region(env, array.ref, 0, count);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  return Ok(out);
}

pub(package) function push(env: usize): Result[void, frame.VideoError] {
  if (!jni.push_local_frame(env, 64)) {
    return Err(frame.Platform);
  }
  return Ok(());
}

function supported_types(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo): Result[string[], frame.VideoError] {
  let types = platform(c.media_codec_info.get_supported_types(env, info))?;
  if (types.is_null()) {
    return Err(frame.Platform);
  }
  defer jni.delete_local_ref(env, types.ref);
  let count = length(env, types, 256)?;
  let out: string[] = [];
  for i in 0..count {
    let item = element(env, types, i)?;
    out = out.append(jni.string_of(env, item));
    let _ = jni.delete_local_ref(env, item);
    if (!healthy(env)) {
      return Err(frame.Platform);
    }
  }
  return Ok(out);
}

pub(package) function is_encoder(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo): Result[boolean, frame.VideoError] {
  return platform(c.media_codec_info.is_encoder(env, info));
}

pub(package) function supports_type(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo, mime: string): Result[boolean, frame.VideoError] {
  let types = supported_types(env, c, info)?;
  return Ok(types.contains(mime));
}

pub(package) function codec_name(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo): Result[string, frame.VideoError] {
  let name = platform(c.media_codec_info.get_name(env, info))?;
  if (name.len() > 512) {
    return Err(frame.Limit);
  }
  if (name.len() == 0) {
    return Err(frame.Platform);
  }
  return Ok(name);
}

pub(package) function codec_caps(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo, mime: string): Result[bindings.MediaCodecInfo_CodecCapabilities, frame.VideoError] {
  let caps = platform(c.media_codec_info.get_capabilities_for_type(env, info, mime))?;
  if (caps.is_null()) {
    return Err(frame.Platform);
  }
  return Ok(caps);
}

// None when the codec throws for `format`.
pub(package) function format_supported(env: usize, c: bindings.Classes, caps: bindings.MediaCodecInfo_CodecCapabilities, format: bindings.MediaFormat): Option[boolean] {
  return match (c.media_codec_info_codec_capabilities.is_format_supported(env, caps, format)) {
    Ok(supported) => Some(supported),
    Err(_) => None,
  };
}

pub(package) struct Attributes { implementation: Implementation, vendor: boolean, alias: boolean }

pub(package) function attributes(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo, api_level: i32): Result[Attributes, frame.VideoError] {
  if (api_level < 29) {
    return Ok(Attributes { implementation: Unknown, vendor: false, alias: false });
  }
  let hardware = platform(c.media_codec_info.is_hardware_accelerated(env, info))?;
  let software = platform(c.media_codec_info.is_software_only(env, info))?;
  let implementation = Unknown;
  if (hardware && !software) {
    implementation = Hardware;
  }
  if (software && !hardware) {
    implementation = Software;
  }
  let vendor = platform(c.media_codec_info.is_vendor(env, info))?;
  let alias = platform(c.media_codec_info.is_alias(env, info))?;
  return Ok(Attributes { implementation: implementation, vendor: vendor, alias: alias });
}

function report(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo, caps: bindings.MediaCodecInfo_CodecCapabilities, request: Request, supported: boolean, api_level: i32, limits: Limits): Result[CodecInfo, frame.VideoError] {
  let attr = attributes(env, c, info, api_level)?;
  return Ok(CodecInfo { name: codec_name(env, c, info)?, request: request, implementation: attr.implementation, vendor: attr.vendor, alias: attr.alias, supported: supported, profiles: profiles(env, c, caps, limits)?, color_formats: colors(env, c, caps, limits)? });
}

function inspect_codec(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo, format: bindings.MediaFormat, request: Request, api_level: i32, limits: Limits): Result[Option[CodecInfo], frame.VideoError] {
  push(env)?;
  defer pop(env);
  if (is_encoder(env, c, info)? != request.encoder) {
    return Ok(None);
  }
  if (!supports_type(env, c, info, request.mime)?) {
    return Ok(None);
  }
  let caps = codec_caps(env, c, info, request.mime)?;
  let Some(supported) = format_supported(env, c, caps, format) else {
    return Ok(None);
  };
  return Ok(Some(report(env, c, info, caps, request, supported, api_level, limits)?));
}

// The array of each regular codec of the device.
pub(package) function codec_infos(env: usize, c: bindings.Classes): Result[jni.Object, frame.VideoError] {
  let list = platform(c.media_codec_list.new(env, bindings.MEDIA_CODEC_LIST_REGULAR_CODECS))?;
  if (list.is_null()) {
    return Err(frame.Platform);
  }
  let infos = platform(c.media_codec_list.get_codec_infos(env, list))?;
  if (infos.is_null()) {
    return Err(frame.Platform);
  }
  return Ok(infos);
}

function collect(env: usize, c: bindings.Classes, infos: jni.Object, request: Request, api_level: i32, limits: Limits): Result[CodecInfo[], frame.VideoError] {
  let count = length(env, infos, limits.max_codecs)?;
  let format = request_format(env, c, request)?;
  let out: CodecInfo[] = [];
  for i in 0..count {
    let info = info_at(env, infos, i)?;
    let inspected = inspect_codec(env, c, info, format, request, api_level, limits);
    let _ = jni.delete_local_ref(env, info.ref);
    match (inspected?) {
      Some(value) => {
        out = out.append(value);
      },
      None => {}
    }
  }
  return Ok(out);
}

// The types of codec `info` when it is a decoder, else none.
function decoder_types_of(env: usize, c: bindings.Classes, info: bindings.MediaCodecInfo): Result[string[], frame.VideoError] {
  if (is_encoder(env, c, info)?) {
    return Ok([]);
  }
  return supported_types(env, c, info);
}

// Each MIME type that a regular decoder of the device takes, once.
pub function decoder_types(env: usize, limits: Limits): Result[string[], frame.VideoError] {
  if (env == 0 as usize) {
    return Err(frame.Header);
  }
  if (limits.max_codecs < 0) {
    return Err(frame.Limit);
  }
  let pending = jni.exception_occurred(env);
  if (pending != 0 as usize) {
    let _ = jni.delete_local_ref(env, pending);
    return Err(frame.Platform);
  }
  push(env)?;
  defer pop(env);
  let c = classes(env)?;
  defer c.release(env);
  let infos = codec_infos(env, c)?;
  let count = length(env, infos, limits.max_codecs)?;
  let out: string[] = [];
  for i in 0..count {
    let info = info_at(env, infos, i)?;
    let got = decoder_types_of(env, c, info);
    let _ = jni.delete_local_ref(env, info.ref);
    let types = got?;
    for t in types {
      if (!out.contains(t)) {
        out = out.append(t);
      }
    }
  }
  return Ok(out);
}

pub function discover(env: usize, api_level: i32, request: Request, limits: Limits): Result[CodecInfo[], frame.VideoError] {
  if (env == 0 as usize || api_level < 21 || !valid(request)) {
    return Err(frame.Header);
  }
  if (limits.max_codecs < 0 || limits.max_profiles < 0 || limits.max_color_formats < 0) {
    return Err(frame.Limit);
  }
  let pending = jni.exception_occurred(env);
  if (pending != 0 as usize) {
    let _ = jni.delete_local_ref(env, pending);
    return Err(frame.Platform);
  }
  push(env)?;
  defer pop(env);
  let c = classes(env)?;
  defer c.release(env);
  let infos = codec_infos(env, c)?;
  return collect(env, c, infos, request, api_level, limits);
}
```
