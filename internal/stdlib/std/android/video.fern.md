# Android video decoder

This module uses the synchronous NDK MediaCodec API from `libmediandk.so`.
The minimum API level is 21. Codec availability depends on the device.
The platform can choose a hardware or software decoder.
A MIME name does not establish profile, level, bit depth, or HDR support.

`open` configures a decoder for an existing `ANativeWindow`.
Output stays in the platform surface. Fern does not copy decoded pixel planes.
The caller supplies complete codec packets and codec configuration data.
H.264 and HEVC configuration requires Annex B start codes.
MP4 length-prefixed NAL units require a container conversion before submission.

Call `close` exactly once for each successful `open`.
Do not copy decoder handles or use one decoder concurrently.
Each input slot belongs to its decoder until one successful `submit`.

Validation errors leave the input slot available for another submission.
After a platform error, flush or recreate the decoder.
Release each output buffer exactly once, even when the caller discards its frame.
A flush invalidates all input slots and output buffers.
A close invalidates the decoder and all its buffers.

A nonnegative timeout uses microseconds. Negative timeouts return `Header`.
`None` from `input` and `Retry` from `output` indicate platform backpressure.
After input EOS, drain output through a buffer with the EOS flag.

A flush starts a new stream segment. Supply a keyframe and required configuration.
The module does not provide a demuxer, audio synchronization, or a playback clock.

```fern
<<*>>=
// This module submits video packets to Android MediaCodec surfaces.
import "std/video/frame";
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

pub struct InputBuffer { index: i32, data: usize, capacity: i32 }

pub struct OutputBuffer { index: i32, offset: i32, size: i32, presentation_time_us: i64, flags: u32 }

pub struct OutputFormat { width: i32, height: i32, crop_left: i32, crop_top: i32, crop_right: i32, crop_bottom: i32, color_standard: i32, color_range: i32, color_transfer: i32 }

pub enum Output { Retry, BuffersChanged, FormatChanged(OutputFormat), Buffer(OutputBuffer) }

pub const FLAG_KEY_FRAME: u32 = 1u32;

pub const FLAG_CODEC_CONFIG: u32 = 2u32;

pub const FLAG_END_OF_STREAM: u32 = 4u32;

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

pub function open(config: Config, window: usize, limits: frame.Limits): Result[Decoder, frame.VideoError] {
  let _ = frame.pixel_count(config.format, limits)?;
  frame.check_packet(config.csd0, limits)?;
  frame.check_packet(config.csd1, limits)?;
  if (window == 0 as usize || limits.max_packet_bytes == 0) {
    return Err(frame.Header);
  }
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
  let handle = c_create(kind);
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

pub function (decoder: Decoder) close(): Result[void, frame.VideoError] {
  let stopped = c_stop(decoder.handle);
  let deleted = c_delete(decoder.handle);
  unsafe {
    __free(decoder.scratch, 32);
  }
  if (stopped != 0 || deleted != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (decoder: Decoder) flush(): Result[void, frame.VideoError] {
  if (c_flush(decoder.handle) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (decoder: Decoder) input(timeout_us: i64): Result[Option[InputBuffer], frame.VideoError] {
  if (timeout_us < 0i64) {
    return Err(frame.Header);
  }
  let index = c_dequeue_input(decoder.handle, timeout_us);
  if (index == -1i64) {
    return Ok(None);
  }
  if (index < 0i64 || index > 2147483647i64) {
    return Err(frame.Platform);
  }
  unsafe {
    __store_ptr(decoder.scratch, 0 as usize);
    let data = c_input_buffer(decoder.handle, index as usize, decoder.scratch);
    let capacity = __load_ptr(decoder.scratch);
    if (data == 0 as usize) {
      return Err(frame.Platform);
    }
    let size = decoder.max_packet_bytes;
    if (capacity < size as usize) {
      size = capacity as i32;
    }
    return Ok(Some(InputBuffer { index: index as i32, data: data, capacity: size }));
  }
}

pub function (decoder: Decoder) submit(slot: InputBuffer, bytes: [u8], presentation_time_us: i64, flags: u32): Result[void, frame.VideoError] {
  if (slot.index < 0 || slot.data == 0 as usize || slot.capacity < 0 || presentation_time_us < 0i64 || (flags & 4294967288u32) != 0u32) {
    return Err(frame.Header);
  }
  if (bytes.len() > decoder.max_packet_bytes) {
    return Err(frame.Limit);
  }
  if (bytes.len() > slot.capacity) {
    return Err(frame.Buffer);
  }
  if (bytes.len() == 0 && (flags & FLAG_END_OF_STREAM) == 0u32) {
    return Err(frame.Header);
  }
  unsafe {
    for i in 0..bytes.len() {
      __store_u8(slot.data + i as usize, bytes[i] as i32);
    }
  }
  if (c_queue(decoder.handle, slot.index as usize, 0 as usize, bytes.len() as usize, presentation_time_us as u64, flags) != 0) {
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
    return Ok(Buffer(OutputBuffer {
      index: index as i32, offset: __load_i32(decoder.scratch), size: __load_i32(decoder.scratch + 4 as usize),
      presentation_time_us: __load_i64(decoder.scratch + 8 as usize), flags: __load_i32(decoder.scratch + 16 as usize) as u32,
    }));
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
```
