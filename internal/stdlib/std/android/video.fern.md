# Android video codecs

This module uses the synchronous NDK MediaCodec API from `libmediandk.so`.
Decode and byte-buffer encode require API 21 or later.
Surface encode and runtime encoder parameters require API 26 or later.
Codec, profile, level, bit depth, and HDR support depend on the device.

`open` retains the platform decoder selection for existing callers.
`open_preferred` discovers codecs and attempts hardware before software.
The capabilities module reports actual hardware attributes on API 29 or later.
Strict policies never select an unknown implementation.
Named APIs also support byte-buffer decode when the window is zero.

Surface decode keeps pixels in the platform surface without a Fern pixel copy.
`open_format_named` preserves the complete extractor format, including configuration and color metadata.
Packet APIs require complete packets and codec configuration data.
H.264 and HEVC configuration uses Annex B start codes.
MP4 packet APIs require length-prefix conversion before submission.
The extractor path passes Android packets directly, as its platform contract requires.

Encoders accept tightly packed, eight-bit YUV420 planar or semiplanar frames.
The encoder requires even dimensions. Input timestamps must increase strictly.

Surface encoders expose an owned `ANativeWindow` for an EGL or camera producer.
The producer supplies timestamps before `signal_end_of_input`.
`set_bitrate` and `request_keyframe` apply runtime encoder parameters.
These requests do not guarantee a particular compression quality or keyframe deadline.

Encoder format changes return an owned `CodecFormat`.
This format supplies codec configuration to the muxer before the first sample.
`output_view` borrows codec storage until buffer release, flush, or close.
`copy_output` consumes reusable storage and reports its valid byte count separately.
The muxer can write a view before release without a Fern packet copy.

Call `close` exactly once for each codec and returned format.
Keep handles and buffer slots with one owner on one thread.
Each input slot permits one successful submission.
Validation errors retain that slot. Platform errors require a flush or a new codec.
Release each output buffer exactly once, including discarded frames and EOS buffers.

A flush invalidates all outstanding buffers. A close also invalidates the codec.

Timeouts use microseconds. Negative timeouts return `Header`.
`None`, `Retry`, and `EncodingRetry` indicate platform backpressure.
After input EOS, drain output through a buffer with the EOS flag.
Timed output release uses an absolute Android monotonic timestamp in nanoseconds.
A packet timestamp uses media microseconds and needs a playback clock conversion.

A decoder fallback consumes and closes the failed decoder.
The replacement requires configuration and a sync sample from the caller.
The player module provides extractor, clock, pause, seek, and recovery control.
Audio synchronization and HDR display configuration remain with the app.
Resource limits bound Fern buffers, not internal codec memory or process RSS.

```fern
<<*>>=
// This module decodes and encodes video through Android MediaCodec.
import "std/ffi";
import "std/video/frame";
import "std/android/video/capabilities";
import "std/utf8";
import "core/cmp";

@derive(cmp.Eq, cmp.Debug)
pub enum Codec { H264, HEVC, VP8, VP9, AV1, MPEG2, MPEG4, H263 }

pub function mime(codec: Codec): string {
  return match (codec) {
    H264 => "video/avc", HEVC => "video/hevc", VP8 => "video/x-vnd.on2.vp8",
    VP9 => "video/x-vnd.on2.vp9", AV1 => "video/av01", MPEG2 => "video/mpeg2",
    MPEG4 => "video/mp4v-es", H263 => "video/3gpp",
  };
}

pub struct Config { codec: Codec, format: frame.Format, csd0: u8[], csd1: u8[] }

pub struct Decoder { handle: usize, scratch: usize, max_packet_bytes: i32 }

pub struct Encoder { handle: usize, scratch: usize, max_packet_bytes: i32, input_surface: usize, input_bytes: i32, state: usize }

pub struct EncoderConfig { codec: Codec, format: frame.Format, bitrate: i32, frame_rate: i32, keyframe_interval: i32, color_format: i32, profile: i32, level: i32, bitrate_mode: i32, color_standard: i32, color_range: i32, color_transfer: i32 }

pub struct CodecFormat { handle: usize }

pub struct BufferView { data: usize, size: i32 }

pub struct Packet { bytes: u8[], size: i32, presentation_time_us: i64, flags: u32 }

pub enum EncoderOutput { EncodingRetry, EncodingBuffersChanged, EncodingFormat(CodecFormat), EncodedBuffer(OutputBuffer) }

pub struct Attempt { name: string, error: frame.VideoError }

pub struct OpenFailure { error: frame.VideoError, attempts: Attempt[] }

pub struct SelectedDecoder { decoder: Decoder, info: capabilities.CodecInfo, attempts: Attempt[] }

pub struct SelectedEncoder { encoder: Encoder, info: capabilities.CodecInfo, attempts: Attempt[] }

pub struct InputBuffer { index: i32, data: usize, capacity: i32 }

pub struct OutputBuffer { index: i32, offset: i32, size: i32, presentation_time_us: i64, flags: u32 }

// AMediaCodecBufferInfo of media/NdkMediaCodec.h.
@derive(ffi.CLayout)
pub(package) struct BufferInfo { offset: i32, size: i32, presentation_time_us: i64, flags: u32 }

// The two results of AMediaFormat_getBuffer.
@derive(ffi.CLayout)
struct Bytes { data: usize, size: usize }

// The block of an encoder that its methods change: the time of the last
// sample, and 1 after the end of the stream.
@derive(ffi.CLayout)
struct EncoderState { last_time_us: i64, ended: i64 }

pub struct OutputFormat { width: i32, height: i32, crop_left: i32, crop_top: i32, crop_right: i32, crop_bottom: i32, color_standard: i32, color_range: i32, color_transfer: i32, stride: i32, slice_height: i32, color_format: i32 }

pub enum Output { Retry, BuffersChanged, FormatChanged(OutputFormat), Buffer(OutputBuffer) }

pub const FLAG_KEY_FRAME: u32 = 1u32;

pub const FLAG_CODEC_CONFIG: u32 = 2u32;

pub const FLAG_END_OF_STREAM: u32 = 4u32;

pub const COLOR_YUV420_PLANAR: i32 = 19;

pub const COLOR_YUV420_SEMIPLANAR: i32 = 21;

pub const COLOR_SURFACE: i32 = 2130708361;

@import("libmediandk.so", "AMediaCodec_createCodecByName")
function c_create_name(name: usize): usize;

@import("libmediandk.so", "AMediaFormat_getBuffer")
function c_get_buffer(format: usize, name: usize, data: usize, size: usize): u8;

@import("libmediandk.so", "AMediaFormat_getString")
function c_get_string(format: usize, name: usize, data: usize): u8;

@import("libmediandk.so", "AMediaCodec_getOutputBuffer")
function c_output_buffer(codec: usize, index: usize, capacity: usize): usize;

@import("libmediandk.so", "AMediaCodec_createInputSurface")
function c_input_surface(codec: usize, window: usize): i32;

@import("libmediandk.so", "AMediaCodec_signalEndOfInputStream")
function c_surface_eos(codec: usize): i32;

@import("libandroid.so", "ANativeWindow_release")
function c_window_release(window: usize): void;

@import("libmediandk.so", "AMediaCodec_setParameters")
function c_parameters(codec: usize, format: usize): i32;

@import("libmediandk.so", "AMediaFormat_new")
function c_format_new(): usize;

@import("libmediandk.so", "AMediaFormat_delete")
function c_format_delete(format: usize): i32;

@import("libmediandk.so", "AMediaFormat_setString")
function c_set_string(format: usize, name: usize, value: usize): void;

@import("libmediandk.so", "AMediaFormat_setInt32")
function c_set_i32(format: usize, name: usize, value: i32): void;

@import("libmediandk.so", "AMediaFormat_setBuffer")
function c_set_buffer(format: usize, name: usize, data: usize, size: usize): void;

@import("libmediandk.so", "AMediaFormat_getInt32")
function c_get_i32(format: usize, name: usize, value: usize): u8;

@import("libmediandk.so", "AMediaCodec_createDecoderByType")
function c_create(mime: usize): usize;

@import("libmediandk.so", "AMediaCodec_configure")
function c_configure(codec: usize, format: usize, window: usize, crypto: usize, flags: u32): i32;

@import("libmediandk.so", "AMediaCodec_start")
function c_start(codec: usize): i32;

@import("libmediandk.so", "AMediaCodec_stop")
function c_stop(codec: usize): i32;

@import("libmediandk.so", "AMediaCodec_flush")
function c_flush(codec: usize): i32;

@import("libmediandk.so", "AMediaCodec_delete")
function c_delete(codec: usize): i32;

@import("libmediandk.so", "AMediaCodec_dequeueInputBuffer")
function c_dequeue_input(codec: usize, timeout_us: i64): i64;

@import("libmediandk.so", "AMediaCodec_getInputBuffer")
function c_input_buffer(codec: usize, index: usize, capacity: usize): usize;

@import("libmediandk.so", "AMediaCodec_queueInputBuffer")
function c_queue(codec: usize, index: usize, offset: usize, size: usize, time_us: u64, flags: u32): i32;

@import("libmediandk.so", "AMediaCodec_dequeueOutputBuffer")
function c_dequeue_output(codec: usize, info: usize, timeout_us: i64): i64;

@import("libmediandk.so", "AMediaCodec_getOutputFormat")
function c_output_format(codec: usize): usize;

@import("libmediandk.so", "AMediaCodec_releaseOutputBuffer")
function c_release(codec: usize, index: usize, render: u8): i32;

@import("libmediandk.so", "AMediaCodec_releaseOutputBufferAtTime")
function c_release_at(codec: usize, index: usize, time_ns: i64): i32;

function c_string(value: string): usize {
  let bytes = value.as_bytes();
  unsafe {
    let data = __alloc(bytes.len() + 1);
    for i in 0..bytes.len() {
      __store_u8(data + i as usize, bytes[i] as i32);
    }
    __store_u8(data + bytes.len() as usize, 0);
    return data;
  }
}

function free_string(data: usize, value: string): void {
  unsafe {
    __free(data, value.len() + 1);
  }
}

function set_i32(format: usize, name: string, value: i32): void {
  let key = c_string(name);
  c_set_i32(format, key, value);
  free_string(key, name);
}

function get_i32(format: usize, name: string, fallback: i32, scratch: usize): i32 {
  let key = c_string(name);
  let found = c_get_i32(format, key, scratch);
  free_string(key, name);
  if (found != 0u8) {
    unsafe {
      return __load_i32(scratch);
    }
  }
  return fallback;
}

function set_buffer(format: usize, name: string, bytes: [u8]): void {
  if (bytes.len() == 0) {
    return;
  }
  let key = c_string(name);
  unsafe {
    let data = __alloc(bytes.len());
    for i in 0..bytes.len() {
      __store_u8(data + i as usize, bytes[i] as i32);
    }
    c_set_buffer(format, key, data, bytes.len() as usize);
    __free(data, bytes.len());
  }
  free_string(key, name);
}

function validate_decoder(config: Config, window: usize, name: string, require_window: boolean, limits: frame.Limits): Result[void, frame.VideoError] {
  let _ = frame.pixel_count(config.format, limits)?;
  frame.check_packet(config.csd0, limits)?;
  frame.check_packet(config.csd1, limits)?;
  if (require_window && window == 0 as usize || limits.max_packet_bytes == 0 || name.len() > 512) {
    return Err(frame.Header);
  }
  for b in name.as_bytes() {
    if (b == 0) {
      return Err(frame.Header);
    }
  }
  return Ok(());
}

function open_impl(config: Config, window: usize, name: string, require_window: boolean, limits: frame.Limits): Result[Decoder, frame.VideoError] {
  validate_decoder(config, window, name, require_window, limits)?;
  let format = c_format_new();
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  let text = mime(config.codec);
  let kind = c_string(text);
  let key = c_string("mime");
  c_set_string(format, key, kind);
  free_string(key, "mime");
  set_i32(format, "width", config.format.width);
  set_i32(format, "height", config.format.height);
  set_i32(format, "max-input-size", limits.max_packet_bytes);
  set_buffer(format, "csd-0", config.csd0);
  set_buffer(format, "csd-1", config.csd1);
  let handle = 0 as usize;
  if (name.len() == 0) {
    handle = c_create(kind);
  } else {
    let named = c_string(name);
    handle = c_create_name(named);
    free_string(named, name);
  }
  free_string(kind, text);
  if (handle == 0 as usize) {
    let _ = c_format_delete(format);
    return Err(frame.Unsupported);
  }
  let configured = c_configure(handle, format, window, 0 as usize, 0u32);
  let _ = c_format_delete(format);
  if (configured != 0) {
    let _ = c_delete(handle);
    return Err(frame.Platform);
  }
  if (c_start(handle) != 0) {
    let _ = c_delete(handle);
    return Err(frame.Platform);
  }
  unsafe {
    return Ok(Decoder { handle: handle, scratch: __alloc(32), max_packet_bytes: limits.max_packet_bytes });
  }
}

pub function open(config: Config, window: usize, limits: frame.Limits): Result[Decoder, frame.VideoError] {
  return open_impl(config, window, "", true, limits);
}

// A zero window selects byte-buffer output. A nonzero window selects a surface.
pub function open_named(config: Config, window: usize, name: string, limits: frame.Limits): Result[Decoder, frame.VideoError] {
  if (name.len() == 0) {
    return Err(frame.Header);
  }
  return open_impl(config, window, name, false, limits);
}

// The format remains with the caller. Configuration copies its codec metadata.
pub function open_format_named(format: CodecFormat, window: usize, name: string, limits: frame.Limits): Result[Decoder, frame.VideoError] {
  let _ = frame.pixel_count(frame.Format { width: format.integer("width", 0), height: format.integer("height", 0) }, limits)?;
  if (name.len() == 0 || name.len() > 512 || limits.max_packet_bytes <= 0) {
    return Err(frame.Header);
  }
  for b in name.as_bytes() {
    if (b == 0) {
      return Err(frame.Header);
    }
  }
  let named = c_string(name);
  let handle = c_create_name(named);
  free_string(named, name);
  if (handle == 0 as usize) {
    return Err(frame.Unsupported);
  }
  if (c_configure(handle, format.handle, window, 0 as usize, 0u32) != 0 || c_start(handle) != 0) {
    let _ = c_delete(handle);
    return Err(frame.Platform);
  }
  unsafe {
    return Ok(Decoder { handle: handle, scratch: __alloc(32), max_packet_bytes: limits.max_packet_bytes });
  }
}

// The output buffer `index`, from the AMediaCodecBufferInfo at `scratch`.
function output_buffer(index: i32, scratch: usize): OutputBuffer {
  let info = unsafe { BufferInfo.c_load(scratch) };
  return OutputBuffer { index: index, offset: info.offset, size: info.size, presentation_time_us: info.presentation_time_us, flags: info.flags };
}

function close_handle(handle: usize, scratch: usize): Result[void, frame.VideoError] {
  let stopped = c_stop(handle);
  let deleted = c_delete(handle);
  unsafe {
    __free(scratch, 32);
  }
  if (stopped != 0 || deleted != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (decoder: Decoder) close(): Result[void, frame.VideoError] {
  return close_handle(decoder.handle, decoder.scratch);
}

pub function (decoder: Decoder) flush(): Result[void, frame.VideoError] {
  if (c_flush(decoder.handle) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

function input_handle(handle: usize, scratch: usize, max_packet_bytes: i32, timeout_us: i64): Result[Option[InputBuffer], frame.VideoError] {
  if (timeout_us < 0i64) {
    return Err(frame.Header);
  }
  let index = c_dequeue_input(handle, timeout_us);
  if (index == -1i64) {
    return Ok(None);
  }
  if (index < 0i64 || index > 2147483647i64) {
    return Err(frame.Platform);
  }
  unsafe {
    __store_ptr(scratch, 0 as usize);
    let data = c_input_buffer(handle, index as usize, scratch);
    let capacity = __load_ptr(scratch);
    if (data == 0 as usize) {
      return Err(frame.Platform);
    }
    let size = max_packet_bytes;
    if (capacity < size as usize) {
      size = capacity as i32;
    }
    return Ok(Some(InputBuffer { index: index as i32, data: data, capacity: size }));
  }
}

pub function (decoder: Decoder) input(timeout_us: i64): Result[Option[InputBuffer], frame.VideoError] {
  return input_handle(decoder.handle, decoder.scratch, decoder.max_packet_bytes, timeout_us);
}

function validate_input(slot: InputBuffer, size: i32, presentation_time_us: i64, flags: u32, maximum: i32): Result[void, frame.VideoError] {
  if (slot.index < 0 || slot.data == 0 as usize || slot.capacity < 0 || size < 0 || presentation_time_us < 0i64 || (flags & 4294967288u32) != 0u32) {
    return Err(frame.Header);
  }
  if (size > maximum) {
    return Err(frame.Limit);
  }
  if (size > slot.capacity) {
    return Err(frame.Buffer);
  }
  return Ok(());
}

function empty_input_valid(size: i32, flags: u32): boolean {
  return size > 0 || (flags & FLAG_END_OF_STREAM) != 0u32;
}

function submit_handle(handle: usize, max_packet_bytes: i32, slot: InputBuffer, bytes: [u8], presentation_time_us: i64, flags: u32): Result[void, frame.VideoError] {
  validate_input(slot, bytes.len(), presentation_time_us, flags, max_packet_bytes)?;
  if (!empty_input_valid(bytes.len(), flags)) {
    return Err(frame.Header);
  }
  unsafe {
    for i in 0..bytes.len() {
      __store_u8(slot.data + i as usize, bytes[i] as i32);
    }
  }
  if (c_queue(handle, slot.index as usize, 0 as usize, bytes.len() as usize, presentation_time_us as u64, flags) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (decoder: Decoder) submit(slot: InputBuffer, bytes: [u8], presentation_time_us: i64, flags: u32): Result[void, frame.VideoError] {
  return submit_handle(decoder.handle, decoder.max_packet_bytes, slot, bytes, presentation_time_us, flags);
}

// Queue bytes already written into the acquired slot, such as extractor output.
pub function (decoder: Decoder) submit_input(slot: InputBuffer, size: i32, presentation_time_us: i64, flags: u32): Result[void, frame.VideoError] {
  validate_input(slot, size, presentation_time_us, flags, decoder.max_packet_bytes)?;
  if (!empty_input_valid(size, flags)) {
    return Err(frame.Header);
  }
  if (c_queue(decoder.handle, slot.index as usize, 0 as usize, size as usize, presentation_time_us as u64, flags) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

function output_format(decoder: Decoder): Result[OutputFormat, frame.VideoError] {
  let format = c_output_format(decoder.handle);
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  let scratch = decoder.scratch;
  let width = get_i32(format, "width", 0, scratch);
  let height = get_i32(format, "height", 0, scratch);
  let result = OutputFormat {
    width: width, height: height,
    crop_left: get_i32(format, "crop-left", 0, scratch),
    crop_top: get_i32(format, "crop-top", 0, scratch),
    crop_right: get_i32(format, "crop-right", width - 1, scratch),
    crop_bottom: get_i32(format, "crop-bottom", height - 1, scratch),
    color_standard: get_i32(format, "color-standard", 0, scratch),
    color_range: get_i32(format, "color-range", 0, scratch),
    color_transfer: get_i32(format, "color-transfer", 0, scratch),
    stride: get_i32(format, "stride", width, scratch),
    slice_height: get_i32(format, "slice-height", height, scratch),
    color_format: get_i32(format, "color-format", 0, scratch),
  };
  let _ = c_format_delete(format);
  if (width <= 0 || height <= 0 || result.crop_left < 0 || result.crop_top < 0 || result.crop_right < result.crop_left || result.crop_right >= width || result.crop_bottom < result.crop_top || result.crop_bottom >= height) {
    return Err(frame.Platform);
  }
  return Ok(result);
}

pub function (decoder: Decoder) output(timeout_us: i64): Result[Output, frame.VideoError] {
  if (timeout_us < 0i64) {
    return Err(frame.Header);
  }
  let index = c_dequeue_output(decoder.handle, decoder.scratch, timeout_us);
  if (index == -1i64) {
    return Ok(Retry);
  }
  if (index == -2i64) {
    return Ok(FormatChanged(output_format(decoder)?));
  }
  if (index == -3i64) {
    return Ok(BuffersChanged);
  }
  if (index < 0i64 || index > 2147483647i64) {
    return Err(frame.Platform);
  }
  unsafe {
    return Ok(Buffer(output_buffer(index as i32, decoder.scratch)));
  }
}

pub function (decoder: Decoder) release(buffer: OutputBuffer, render: boolean): Result[void, frame.VideoError] {
  if (buffer.index < 0) {
    return Err(frame.Header);
  }
  let flag = if (render) { 1u8 } else { 0u8 };
  if (c_release(decoder.handle, buffer.index as usize, flag) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

// The timestamp uses the Android monotonic clock in nanoseconds.
pub function (decoder: Decoder) release_at(buffer: OutputBuffer, time_ns: i64): Result[void, frame.VideoError] {
  if (buffer.index < 0 || time_ns < 0i64) {
    return Err(frame.Header);
  }
  if (c_release_at(decoder.handle, buffer.index as usize, time_ns) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function decoder_request(config: Config, profile: i32, level: i32, frame_rate: i32): capabilities.Request {
  return capabilities.Request { mime: mime(config.codec), width: config.format.width, height: config.format.height, encoder: false, frame_rate: frame_rate, bitrate: 0, profile: profile, level: level, color_format: 0 };
}

pub function open_selected(config: Config, window: usize, request: capabilities.Request, infos: [capabilities.CodecInfo], policy: capabilities.Policy, limits: frame.Limits): Result[SelectedDecoder, OpenFailure] {
  if (!capabilities.valid(request) || request != decoder_request(config, request.profile, request.level, request.frame_rate)) {
    return Err(OpenFailure { error: frame.Header, attempts: [] });
  }
  let attempts: Attempt[] = [];
  for info in capabilities.ordered(infos, request, policy) {
    match (open_named(config, window, info.name, limits)) {
      Ok(decoder) => {
        return Ok(SelectedDecoder { decoder: decoder, info: info, attempts: attempts });
      },
      Err(e) => {
        attempts = attempts.append(Attempt { name: info.name, error: e });
        if (e == frame.Header || e == frame.Limit || e == frame.Buffer) {
          return Err(OpenFailure { error: e, attempts: attempts });
        }
      }
    }
  }
  return Err(OpenFailure { error: frame.Unsupported, attempts: attempts });
}

pub function open_preferred(env: usize, api_level: i32, config: Config, window: usize, limits: frame.Limits): Result[SelectedDecoder, OpenFailure] {
  let request = decoder_request(config, 0, 0, 0);
  let infos = match (capabilities.discover(env, api_level, request, capabilities.default_limits())) {
    Ok(value) => value, Err(e) => { return Err(OpenFailure { error: e, attempts: [] }); },
  };
  return open_selected(config, window, request, infos, capabilities.PreferHardware, limits);
}

// The replacement needs codec configuration and a keyframe from the caller.
pub function fallback_to_software(config: Config, window: usize, own failed: SelectedDecoder, infos: [capabilities.CodecInfo], limits: frame.Limits): Result[SelectedDecoder, OpenFailure] {
  let _ = failed.decoder.close();
  let candidates: capabilities.CodecInfo[] = [];
  for info in infos {
    if (info.name != failed.info.name) {
      candidates = candidates.append(info);
    }
  }
  return open_selected(config, window, failed.info.request, candidates, capabilities.SoftwareOnly, limits);
}

function view_output(handle: usize, scratch: usize, maximum: i32, buffer: OutputBuffer): Result[BufferView, frame.VideoError] {
  if (buffer.index < 0 || buffer.offset < 0 || buffer.size < 0) {
    return Err(frame.Header);
  }
  if (buffer.size > maximum) {
    return Err(frame.Limit);
  }
  if (buffer.size == 0) {
    return Ok(BufferView { data: 0 as usize, size: 0 });
  }
  unsafe {
    __store_ptr(scratch, 0 as usize);
    let data = c_output_buffer(handle, buffer.index as usize, scratch);
    let capacity = __load_ptr(scratch);
    if (data == 0 as usize || buffer.offset as usize > capacity || buffer.size as usize > capacity - buffer.offset as usize) {
      return Err(frame.Platform);
    }
    return Ok(BufferView { data: data + buffer.offset as usize, size: buffer.size });
  }
}

pub function (decoder: Decoder) output_view(buffer: OutputBuffer): Result[BufferView, frame.VideoError] {
  return view_output(decoder.handle, decoder.scratch, decoder.max_packet_bytes, buffer);
}

function copy_view(view: BufferView, buffer: OutputBuffer, own bytes: u8[]): Result[Packet, frame.VideoError] {
  if (bytes.len() < view.size) {
    return Err(frame.Buffer);
  }
  unsafe {
    for i in 0..view.size {
      bytes = bytes.with(i, __load_u8(view.data + i as usize) as u8);
    }
  }
  return Ok(Packet { bytes: bytes, size: view.size, presentation_time_us: buffer.presentation_time_us, flags: buffer.flags });
}

pub function (decoder: Decoder) copy_output(buffer: OutputBuffer, own bytes: u8[]): Result[Packet, frame.VideoError] {
  let view = decoder.output_view(buffer)?;
  return copy_view(view, buffer, bytes);
}

pub function (format: CodecFormat) close(): Result[void, frame.VideoError] {
  if (c_format_delete(format.handle) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (format: CodecFormat) integer(name: string, fallback: i32): i32 {
  unsafe {
    let scratch = __alloc(4);
    let value = get_i32(format.handle, name, fallback, scratch);
    __free(scratch, 4);
    return value;
  }
}

pub function (format: CodecFormat) text(name: string, maximum: i32): Result[string, frame.VideoError] {
  if (maximum < 0 || maximum > 1048576 || name.len() > 128) {
    return Err(frame.Limit);
  }
  for b in name.as_bytes() {
    if (b == 0) {
      return Err(frame.Header);
    }
  }
  let key = c_string(name);
  unsafe {
    let slot = __alloc(8);
    __store_ptr(slot, 0 as usize);
    let found = c_get_string(format.handle, key, slot);
    let data = __load_ptr(slot);
    __free(slot, 8);
    free_string(key, name);
    if (found == 0u8) {
      return Ok("");
    }
    if (data == 0 as usize) {
      return Err(frame.Platform);
    }
    let bytes: u8[] = [];
    for i in 0..maximum {
      let b = __load_u8(data + i as usize) as u8;
      if (b == 0) {
        return Ok(utf8.from_bytes_lossy(bytes));
      }
      bytes = bytes.append(b);
    }
  }
  return Err(frame.Limit);
}

function validate_format_request(format: CodecFormat, request: capabilities.Request): Result[void, frame.VideoError] {
  let mime = match (format.text("mime", 128)) { Ok(value) => value, Err(e) => { return Err(e); } };
  if (request.encoder || !capabilities.valid(request) || request.mime != mime || request.width != format.integer("width", 0) || request.height != format.integer("height", 0)) {
    return Err(frame.Header);
  }
  return Ok(());
}

pub function select_format(format: CodecFormat, window: usize, request: capabilities.Request, infos: [capabilities.CodecInfo], policy: capabilities.Policy, limits: frame.Limits): Result[SelectedDecoder, OpenFailure] {
  match (validate_format_request(format, request)) {
    Err(e) => {
      return Err(OpenFailure { error: e, attempts: [] });
    },
    Ok(_) => {}
  }
  let attempts: Attempt[] = [];
  for info in capabilities.ordered(infos, request, policy) {
    match (open_format_named(format, window, info.name, limits)) {
      Ok(decoder) => {
        return Ok(SelectedDecoder { decoder: decoder, info: info, attempts: attempts });
      },
      Err(e) => {
        attempts = attempts.append(Attempt { name: info.name, error: e });
        if (e == frame.Header || e == frame.Limit) {
          return Err(OpenFailure { error: e, attempts: attempts });
        }
      }
    }
  }
  return Err(OpenFailure { error: frame.Unsupported, attempts: attempts });
}

pub function (format: CodecFormat) buffer(name: string, maximum: i32): Result[u8[], frame.VideoError] {
  if (maximum < 0 || name.len() > 128) {
    return Err(frame.Limit);
  }
  for b in name.as_bytes() {
    if (b == 0) {
      return Err(frame.Header);
    }
  }
  let key = c_string(name);
  unsafe {
    let scratch = __alloc(Bytes.c_size() as i32);
    Bytes { data: 0 as usize, size: 0 as usize }.c_store(scratch);
    let found = c_get_buffer(format.handle, key, scratch + Bytes.c_offset_data(), scratch + Bytes.c_offset_size());
    free_string(key, name);
    let data = Bytes.c_load_data(scratch);
    let size = Bytes.c_load_size(scratch);
    __free(scratch, Bytes.c_size() as i32);
    if (found == 0u8) {
      return Ok([]);
    }
    if (size > maximum as usize) {
      return Err(frame.Limit);
    }
    if (size > 0 as usize && data == 0 as usize) {
      return Err(frame.Platform);
    }
    let bytes: u8[] = [];
    for i in 0..size as i32 {
      bytes = bytes.append(__load_u8(data + i as usize) as u8);
    }
    return Ok(bytes);
  }
}

pub function default_encoder_config(codec: Codec, format: frame.Format, bitrate: i32, frame_rate: i32): EncoderConfig {
  return EncoderConfig { codec: codec, format: format, bitrate: bitrate, frame_rate: frame_rate, keyframe_interval: 2, color_format: COLOR_YUV420_PLANAR, profile: 0, level: 0, bitrate_mode: 1, color_standard: 0, color_range: 0, color_transfer: 0 };
}

pub function encoder_request(config: EncoderConfig): capabilities.Request {
  return capabilities.Request { mime: mime(config.codec), width: config.format.width, height: config.format.height, encoder: true, frame_rate: config.frame_rate, bitrate: config.bitrate, profile: config.profile, level: config.level, color_format: config.color_format };
}

function encoder_tuning_valid(config: EncoderConfig): boolean {
  return config.bitrate > 0 && config.frame_rate > 0 && config.keyframe_interval >= 0 && config.profile >= 0 && config.level >= 0 && config.bitrate_mode >= 0 && config.bitrate_mode <= 2;
}

function encoder_color_valid(config: EncoderConfig): boolean {
  return config.color_standard >= 0 && config.color_range >= 0 && config.color_transfer >= 0 && config.format.width % 2 == 0 && config.format.height % 2 == 0;
}

function valid_codec_name(name: string): boolean {
  if (name.len() == 0 || name.len() > 512) {
    return false;
  }
  for b in name.as_bytes() {
    if (b == 0) {
      return false;
    }
  }
  return true;
}

function encoder_color_format_valid(color_format: i32): boolean {
  return color_format == COLOR_SURFACE || color_format == COLOR_YUV420_PLANAR || color_format == COLOR_YUV420_SEMIPLANAR;
}

function validate_encoder(config: EncoderConfig, name: string, limits: frame.Limits): Result[void, frame.VideoError] {
  let count = frame.pixel_count(config.format, limits)?;
  if (!encoder_tuning_valid(config) || !encoder_color_valid(config) || !valid_codec_name(name)) {
    return Err(frame.Header);
  }
  if (!encoder_color_format_valid(config.color_format)) {
    return Err(frame.Unsupported);
  }
  if (limits.max_packet_bytes <= 0 || config.color_format != COLOR_SURFACE && count + count / 2 > limits.max_packet_bytes) {
    return Err(frame.Limit);
  }
  return Ok(());
}

function encoder_optional_format(format: usize, config: EncoderConfig): void {
  if (config.profile > 0) {
    set_i32(format, "profile", config.profile);
  }
  if (config.level > 0) {
    set_i32(format, "level", config.level);
  }
  if (config.color_standard > 0) {
    set_i32(format, "color-standard", config.color_standard);
  }
  if (config.color_range > 0) {
    set_i32(format, "color-range", config.color_range);
  }
  if (config.color_transfer > 0) {
    set_i32(format, "color-transfer", config.color_transfer);
  }
}

function configure_encoder(config: EncoderConfig, name: string, limits: frame.Limits): Result[usize, frame.VideoError] {
  validate_encoder(config, name, limits)?;
  let format = c_format_new();
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  let text = mime(config.codec);
  let kind = c_string(text);
  let key = c_string("mime");
  c_set_string(format, key, kind);
  free_string(key, "mime");
  free_string(kind, text);
  set_i32(format, "width", config.format.width);
  set_i32(format, "height", config.format.height);
  set_i32(format, "bitrate", config.bitrate);
  set_i32(format, "frame-rate", config.frame_rate);
  set_i32(format, "i-frame-interval", config.keyframe_interval);
  set_i32(format, "color-format", config.color_format);
  set_i32(format, "bitrate-mode", config.bitrate_mode);
  encoder_optional_format(format, config);
  let named = c_string(name);
  let handle = c_create_name(named);
  free_string(named, name);
  if (handle == 0 as usize) {
    let _ = c_format_delete(format);
    return Err(frame.Unsupported);
  }
  let status = c_configure(handle, format, 0 as usize, 0 as usize, 1u32);
  let _ = c_format_delete(format);
  if (status != 0) {
    let _ = c_delete(handle);
    return Err(frame.Platform);
  }
  return Ok(handle);
}

function started_encoder(config: EncoderConfig, handle: usize, surface: usize, limits: frame.Limits): Result[Encoder, frame.VideoError] {
  if (c_start(handle) != 0) {
    let _ = c_delete(handle);
    if (surface != 0 as usize) {
      c_window_release(surface);
    }
    return Err(frame.Platform);
  }
  unsafe {
    let state = __alloc(EncoderState.c_size() as i32);
    EncoderState { last_time_us: -1i64, ended: 0i64 }.c_store(state);
    let input_bytes = if (surface == 0 as usize) { config.format.width * config.format.height * 3 / 2 } else { 0 };
    return Ok(Encoder { handle: handle, scratch: __alloc(32), max_packet_bytes: limits.max_packet_bytes, input_surface: surface, input_bytes: input_bytes, state: state });
  }
}

pub function open_encoder(config: EncoderConfig, name: string, limits: frame.Limits): Result[Encoder, frame.VideoError] {
  if (config.color_format == COLOR_SURFACE) {
    return Err(frame.Unsupported);
  }
  let handle = configure_encoder(config, name, limits)?;
  return started_encoder(config, handle, 0 as usize, limits);
}

// This path requires Android API 26. The caller borrows input_surface until close.
pub function open_surface_encoder(config: EncoderConfig, name: string, limits: frame.Limits): Result[Encoder, frame.VideoError] {
  if (config.color_format != COLOR_SURFACE) {
    return Err(frame.Header);
  }
  let handle = configure_encoder(config, name, limits)?;
  unsafe {
    let slot = __alloc(8);
    __store_ptr(slot, 0 as usize);
    let status = c_input_surface(handle, slot);
    let surface = __load_ptr(slot);
    __free(slot, 8);
    if (status != 0 || surface == 0 as usize) {
      let _ = c_delete(handle);
      if (surface != 0 as usize) {
        c_window_release(surface);
      }
      return Err(frame.Platform);
    }
    return started_encoder(config, handle, surface, limits);
  }
}

pub function (encoder: Encoder) close(): Result[void, frame.VideoError] {
  let result = close_handle(encoder.handle, encoder.scratch);
  if (encoder.input_surface != 0 as usize) {
    c_window_release(encoder.input_surface);
  }
  unsafe {
    __free(encoder.state, EncoderState.c_size() as i32);
  }
  return result;
}

pub function (encoder: Encoder) input(timeout_us: i64): Result[Option[InputBuffer], frame.VideoError] {
  unsafe {
    if (encoder.input_surface != 0 as usize || EncoderState.c_load_ended(encoder.state) != 0i64) {
      return Err(frame.State);
    }
  }
  return input_handle(encoder.handle, encoder.scratch, encoder.max_packet_bytes, timeout_us);
}

function encoder_sample_valid(encoder: Encoder, size: i32, flags: u32): boolean {
  if (flags == FLAG_END_OF_STREAM) {
    return size == 0;
  }
  return flags == 0u32 && size == encoder.input_bytes;
}

function encoder_timestamp_valid(last: i64, next: i64, eos: boolean): boolean {
  return next >= 0i64 && next >= last && (eos || next != last);
}

pub function (encoder: Encoder) submit(slot: InputBuffer, bytes: [u8], presentation_time_us: i64, flags: u32): Result[void, frame.VideoError] {
  unsafe {
    if (encoder.input_surface != 0 as usize || EncoderState.c_load_ended(encoder.state) != 0i64) {
      return Err(frame.State);
    }
    let eos = (flags & FLAG_END_OF_STREAM) != 0u32;
    if (!encoder_sample_valid(encoder, bytes.len(), flags)) {
      return Err(frame.Header);
    }
    if (!encoder_timestamp_valid(EncoderState.c_load_last_time_us(encoder.state), presentation_time_us, eos)) {
      return Err(frame.Header);
    }
    submit_handle(encoder.handle, encoder.max_packet_bytes, slot, bytes, presentation_time_us, flags)?;
    EncoderState.c_store_last_time_us(encoder.state, presentation_time_us);
    if (eos) {
      EncoderState.c_store_ended(encoder.state, 1i64);
    }
  }
  return Ok(());
}

// Surface producers supply presentation timestamps through EGL or the camera.
pub function (encoder: Encoder) signal_end_of_input(): Result[void, frame.VideoError] {
  unsafe {
    if (encoder.input_surface == 0 as usize || EncoderState.c_load_ended(encoder.state) != 0i64) {
      return Err(frame.State);
    }
    if (c_surface_eos(encoder.handle) != 0) {
      return Err(frame.Platform);
    }
    EncoderState.c_store_ended(encoder.state, 1i64);
  }
  return Ok(());
}

pub function (encoder: Encoder) output(timeout_us: i64): Result[EncoderOutput, frame.VideoError] {
  if (timeout_us < 0i64) {
    return Err(frame.Header);
  }
  let index = c_dequeue_output(encoder.handle, encoder.scratch, timeout_us);
  if (index == -1i64) {
    return Ok(EncodingRetry);
  }
  if (index == -3i64) {
    return Ok(EncodingBuffersChanged);
  }
  if (index == -2i64) {
    let format = c_output_format(encoder.handle);
    if (format == 0 as usize) {
      return Err(frame.Platform);
    }
    return Ok(EncodingFormat(CodecFormat { handle: format }));
  }
  if (index < 0i64 || index > 2147483647i64) {
    return Err(frame.Platform);
  }
  unsafe {
    return Ok(EncodedBuffer(output_buffer(index as i32, encoder.scratch)));
  }
}

pub function (encoder: Encoder) output_view(buffer: OutputBuffer): Result[BufferView, frame.VideoError] {
  return view_output(encoder.handle, encoder.scratch, encoder.max_packet_bytes, buffer);
}

pub function (encoder: Encoder) copy_output(buffer: OutputBuffer, own bytes: u8[]): Result[Packet, frame.VideoError] {
  let view = encoder.output_view(buffer)?;
  return copy_view(view, buffer, bytes);
}

pub function (encoder: Encoder) release(buffer: OutputBuffer): Result[void, frame.VideoError] {
  if (buffer.index < 0) {
    return Err(frame.Header);
  }
  if (c_release(encoder.handle, buffer.index as usize, 0u8) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

function parameter(handle: usize, name: string, value: i32): Result[void, frame.VideoError] {
  let format = c_format_new();
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  set_i32(format, name, value);
  let status = c_parameters(handle, format);
  let _ = c_format_delete(format);
  if (status != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (encoder: Encoder) set_bitrate(bitrate: i32): Result[void, frame.VideoError] {
  if (bitrate <= 0) {
    return Err(frame.Header);
  }
  unsafe {
    if (EncoderState.c_load_ended(encoder.state) != 0i64) {
      return Err(frame.State);
    }
  }
  return parameter(encoder.handle, "video-bitrate", bitrate);
}

pub function (encoder: Encoder) request_keyframe(): Result[void, frame.VideoError] {
  unsafe {
    if (EncoderState.c_load_ended(encoder.state) != 0i64) {
      return Err(frame.State);
    }
  }
  return parameter(encoder.handle, "request-sync", 0);
}

pub function select_encoder(config: EncoderConfig, infos: [capabilities.CodecInfo], policy: capabilities.Policy, limits: frame.Limits): Result[SelectedEncoder, OpenFailure] {
  let attempts: Attempt[] = [];
  for info in capabilities.ordered(infos, encoder_request(config), policy) {
    match (open_encoder(config, info.name, limits)) {
      Ok(encoder) => {
        return Ok(SelectedEncoder { encoder: encoder, info: info, attempts: attempts });
      },
      Err(e) => {
        attempts = attempts.append(Attempt { name: info.name, error: e });
        if (e == frame.Header || e == frame.Limit || e == frame.Buffer) {
          return Err(OpenFailure { error: e, attempts: attempts });
        }
      }
    }
  }
  return Err(OpenFailure { error: frame.Unsupported, attempts: attempts });
}

pub function select_surface_encoder(config: EncoderConfig, infos: [capabilities.CodecInfo], policy: capabilities.Policy, limits: frame.Limits): Result[SelectedEncoder, OpenFailure] {
  let attempts: Attempt[] = [];
  for info in capabilities.ordered(infos, encoder_request(config), policy) {
    match (open_surface_encoder(config, info.name, limits)) {
      Ok(encoder) => {
        return Ok(SelectedEncoder { encoder: encoder, info: info, attempts: attempts });
      },
      Err(e) => {
        attempts = attempts.append(Attempt { name: info.name, error: e });
        if (e == frame.Header || e == frame.Limit || e == frame.Buffer) {
          return Err(OpenFailure { error: e, attempts: attempts });
        }
      }
    }
  }
  return Err(OpenFailure { error: frame.Unsupported, attempts: attempts });
}
```
