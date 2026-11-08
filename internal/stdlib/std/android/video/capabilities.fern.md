# Android video codec capabilities

Discovery uses MediaCodecList through the caller's attached JNI environment.
The regular codec list excludes codecs that require secure or tunneled operation.
Android 29 or later supplies hardware, software, vendor, and alias attributes.
Earlier versions report `Unknown` and never infer hardware from a codec name.

Each result describes support for one exact request through `isFormatSupported`.
The caller must provide the stream profile and level when those are known.
A support report does not replace a successful configuration or a device test.
Discovery clears exceptions that it handles and bounds local JNI references.

```fern
<<*>>=
// This module discovers Android video codecs and orders supported candidates.
import "std/jni";
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

function policy_rank(implementation: Implementation, policy: Policy): i32 {
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

function pop(env: usize): void {
  let _ = jni.call1(env, 20, 0 as usize);
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

function method(env: usize, obj: usize, name: string, signature: string): Result[usize, frame.VideoError] {
  let cls = jni.get_object_class(env, obj);
  if (!healthy(env) || cls == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_method_id(env, cls, name, signature);
  let _ = jni.delete_local_ref(env, cls);
  if (!healthy(env) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(id);
}

function object(env: usize, obj: usize, name: string, signature: string, values: usize[]): Result[usize, frame.VideoError] {
  let id = method(env, obj, name, signature)?;
  let args = jni.jvalues(values);
  let value = jni.call_object_method_a(env, obj, id, args);
  jni.free_buffer(args);
  if (!healthy(env) || value == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(value);
}

function read_boolean(env: usize, obj: usize, name: string): Result[boolean, frame.VideoError] {
  let id = method(env, obj, name, "()Z")?;
  let value = jni.call_boolean_method_a(env, obj, id, 0 as usize);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  return Ok(value != 0);
}

function integer_field(env: usize, obj: usize, name: string): Result[i32, frame.VideoError] {
  let cls = jni.get_object_class(env, obj);
  if (!healthy(env) || cls == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_field_id(env, cls, name, "I");
  let _ = jni.delete_local_ref(env, cls);
  if (!healthy(env) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  let value = jni.get_int_field(env, obj, id);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  return Ok(value);
}

function object_field(env: usize, obj: usize, name: string, signature: string): Result[usize, frame.VideoError] {
  let cls = jni.get_object_class(env, obj);
  if (!healthy(env) || cls == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_field_id(env, cls, name, signature);
  let _ = jni.delete_local_ref(env, cls);
  if (!healthy(env) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  let value = jni.get_object_field(env, obj, id);
  if (!healthy(env) || value == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(value);
}

function set_integer(env: usize, format: usize, name: string, value: i32): Result[void, frame.VideoError] {
  let id = method(env, format, "setInteger", "(Ljava/lang/String;I)V")?;
  let key = jni.new_string(env, name);
  if (!healthy(env) || key == 0 as usize) {
    return Err(frame.Platform);
  }
  let args = jni.jvalues([key, value as usize]);
  let _ = jni.call_void_method_a(env, format, id, args);
  jni.free_buffer(args);
  let _ = jni.delete_local_ref(env, key);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  return Ok(());
}

function format_for(env: usize, request: Request): Result[usize, frame.VideoError] {
  let cls = jni.find_class(env, "android/media/MediaFormat");
  if (!healthy(env) || cls == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_static_method_id(env, cls, "createVideoFormat", "(Ljava/lang/String;II)Landroid/media/MediaFormat;");
  if (!healthy(env) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  let mime = jni.new_string(env, request.mime);
  if (!healthy(env) || mime == 0 as usize) {
    return Err(frame.Platform);
  }
  let args = jni.jvalues([mime, request.width as usize, request.height as usize]);
  let format = jni.call_static_object_method_a(env, cls, id, args);
  jni.free_buffer(args);
  if (!healthy(env) || format == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(format);
}

function set_positive(env: usize, format: usize, key: string, value: i32): Result[void, frame.VideoError] {
  if (value > 0) {
    set_integer(env, format, key, value)?;
  }
  return Ok(());
}

function request_format(env: usize, request: Request): Result[usize, frame.VideoError] {
  let format = format_for(env, request)?;
  set_positive(env, format, "frame-rate", request.frame_rate)?;
  set_positive(env, format, "bitrate", request.bitrate)?;
  set_positive(env, format, "profile", request.profile)?;
  set_positive(env, format, "level", request.level)?;
  set_positive(env, format, "color-format", request.color_format)?;
  return Ok(format);
}

function profiles(env: usize, caps: usize, limits: Limits): Result[ProfileLevel[], frame.VideoError] {
  let array = object_field(env, caps, "profileLevels", "[Landroid/media/MediaCodecInfo$CodecProfileLevel;")?;
  let count = jni.get_array_length(env, array);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  if (count < 0 || count > limits.max_profiles) {
    return Err(frame.Limit);
  }
  let out: ProfileLevel[] = [];
  for i in 0..count {
    let item = jni.get_object_array_element(env, array, i);
    if (!healthy(env) || item == 0 as usize) {
      return Err(frame.Platform);
    }
    out = out.append(ProfileLevel { profile: integer_field(env, item, "profile")?, level: integer_field(env, item, "level")? });
    let _ = jni.delete_local_ref(env, item);
  }
  return Ok(out);
}

function colors(env: usize, caps: usize, limits: Limits): Result[i32[], frame.VideoError] {
  let array = object_field(env, caps, "colorFormats", "[I")?;
  let count = jni.get_array_length(env, array);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  if (count < 0 || count > limits.max_color_formats) {
    return Err(frame.Limit);
  }
  let out: i32[] = [];
  unsafe {
    let scratch = __alloc(4);
    for i in 0..count {
      // JNIEnv.GetIntArrayRegion is table entry 203.
      let _ = __c_call5(jni.method_ptr(env, 203), env, array, i as usize, 1 as usize, scratch);
      if (!healthy(env)) {
        __free(scratch, 4);
        return Err(frame.Platform);
      }
      out = out.append(__load_i32(scratch));
    }
    __free(scratch, 4);
  }
  return Ok(out);
}

function push(env: usize): Result[void, frame.VideoError] {
  if (jni.call1(env, 19, 64 as usize) as i32 != 0) {
    let _ = healthy(env);
    return Err(frame.Platform);
  }
  return Ok(());
}

function supports_type(env: usize, info: usize, mime: string): Result[boolean, frame.VideoError] {
  let types = object(env, info, "getSupportedTypes", "()[Ljava/lang/String;", [])?;
  let count = jni.get_array_length(env, types);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  if (count < 0 || count > 256) {
    return Err(frame.Limit);
  }
  let matches = false;
  for i in 0..count {
    let item = jni.get_object_array_element(env, types, i);
    if (!healthy(env) || item == 0 as usize) {
      return Err(frame.Platform);
    }
    matches = matches || jni.string_of(env, item) == mime;
    let _ = jni.delete_local_ref(env, item);
    if (!healthy(env)) {
      return Err(frame.Platform);
    }
  }
  return Ok(matches);
}

function codec_name(env: usize, info: usize): Result[string, frame.VideoError] {
  let name_obj = object(env, info, "getName", "()Ljava/lang/String;", [])?;
  if (jni.get_string_utf_length(env, name_obj) > 512) {
    return Err(frame.Limit);
  }
  let name = jni.string_of(env, name_obj);
  if (!healthy(env) || name.len() == 0) {
    return Err(frame.Platform);
  }
  return Ok(name);
}

function codec_caps(env: usize, info: usize, mime: string): Result[usize, frame.VideoError] {
  let name = jni.new_string(env, mime);
  if (!healthy(env) || name == 0 as usize) {
    return Err(frame.Platform);
  }
  return object(env, info, "getCapabilitiesForType", "(Ljava/lang/String;)Landroid/media/MediaCodecInfo$CodecCapabilities;", [name]);
}

function format_supported(env: usize, caps: usize, format: usize): Result[Option[boolean], frame.VideoError] {
  let id = method(env, caps, "isFormatSupported", "(Landroid/media/MediaFormat;)Z")?;
  let args = jni.jvalues([format]);
  let supported = jni.call_boolean_method_a(env, caps, id, args);
  jni.free_buffer(args);
  if (!healthy(env)) {
    return Ok(None);
  }
  return Ok(Some(supported != 0));
}

struct Attributes { implementation: Implementation, vendor: boolean, alias: boolean }

function attributes(env: usize, info: usize, api_level: i32): Result[Attributes, frame.VideoError] {
  if (api_level < 29) {
    return Ok(Attributes { implementation: Unknown, vendor: false, alias: false });
  }
  let hardware = read_boolean(env, info, "isHardwareAccelerated")?;
  let software = read_boolean(env, info, "isSoftwareOnly")?;
  let implementation = Unknown;
  if (hardware && !software) {
    implementation = Hardware;
  }
  if (software && !hardware) {
    implementation = Software;
  }
  return Ok(Attributes { implementation: implementation, vendor: read_boolean(env, info, "isVendor")?, alias: read_boolean(env, info, "isAlias")? });
}

function report(env: usize, info: usize, caps: usize, request: Request, supported: boolean, api_level: i32, limits: Limits): Result[CodecInfo, frame.VideoError] {
  let attr = attributes(env, info, api_level)?;
  return Ok(CodecInfo { name: codec_name(env, info)?, request: request, implementation: attr.implementation, vendor: attr.vendor, alias: attr.alias, supported: supported, profiles: profiles(env, caps, limits)?, color_formats: colors(env, caps, limits)? });
}

function inspect_codec(env: usize, info: usize, format: usize, request: Request, api_level: i32, limits: Limits): Result[Option[CodecInfo], frame.VideoError] {
  push(env)?;
  defer pop(env);
  if (read_boolean(env, info, "isEncoder")? != request.encoder) {
    return Ok(None);
  }
  if (!supports_type(env, info, request.mime)?) {
    return Ok(None);
  }
  let caps = codec_caps(env, info, request.mime)?;
  let Some(supported) = format_supported(env, caps, format)? else {
    return Ok(None);
  };
  return Ok(Some(report(env, info, caps, request, supported, api_level, limits)?));
}

function codec_list(env: usize): Result[usize, frame.VideoError] {
  let cls = jni.find_class(env, "android/media/MediaCodecList");
  if (!healthy(env) || cls == 0 as usize) {
    return Err(frame.Platform);
  }
  let ctor = jni.get_method_id(env, cls, "<init>", "(I)V");
  if (!healthy(env) || ctor == 0 as usize) {
    return Err(frame.Platform);
  }
  let args = jni.jvalues([0 as usize]);
  let list = jni.new_object_a(env, cls, ctor, args);
  jni.free_buffer(args);
  if (!healthy(env) || list == 0 as usize) {
    return Err(frame.Platform);
  }
  return object(env, list, "getCodecInfos", "()[Landroid/media/MediaCodecInfo;", []);
}

function collect(env: usize, infos: usize, request: Request, api_level: i32, limits: Limits): Result[CodecInfo[], frame.VideoError] {
  let count = jni.get_array_length(env, infos);
  if (!healthy(env)) {
    return Err(frame.Platform);
  }
  if (count < 0 || count > limits.max_codecs) {
    return Err(frame.Limit);
  }
  let format = request_format(env, request)?;
  let out: CodecInfo[] = [];
  for i in 0..count {
    let info = jni.get_object_array_element(env, infos, i);
    if (!healthy(env) || info == 0 as usize) {
      return Err(frame.Platform);
    }
    let inspected = inspect_codec(env, info, format, request, api_level, limits);
    let _ = jni.delete_local_ref(env, info);
    match (inspected?) {
      Some(value) => {
        out = out.append(value);
      },
      None => {}
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
  let infos = codec_list(env)?;
  return collect(env, infos, request, api_level, limits);
}
```
