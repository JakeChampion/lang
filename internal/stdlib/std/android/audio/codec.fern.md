# Android platform audio decoder

This module decodes audio packets through MediaCodec on Android API 21 and later.
It supports device codecs for MIME types such as `audio/mpeg`, `audio/mp4a-latm`, `audio/flac`, and `audio/opus`.
The caller supplies complete access units, timestamps in microseconds, and codec-specific data.
Container bytes are not access units. Use a demuxer before this API.

Opus can require all three CSD buffers. AAC usually requires AudioSpecificConfig in `csd0`.

`open_preferred` tries supported hardware codecs, software codecs, then codecs of unknown implementation.
It records failed attempts. If no candidate opens, the caller can use its Fern software decoder.
Hardware classification comes from Android API 29 attributes. It does not measure speed or power.

The API shares buffer types and platform errors with `std/android/video`.
It does not use video dimensions or video output formats.
PCM output is interleaved signed 16-bit or float32 in native byte order.
`FormatChanged` gives the actual sample rate, channel count, encoding, and channel mask.
The caller must check this format before playback. Other PCM encodings return `Unsupported`.

`Config.pcm_encoding` accepts zero for the platform default, `PCM_I16`, or `PCM_F32`.
An explicit request sets `pcm-encoding` and rejects a different output encoding.
Float PCM can preserve 24-bit integer precision. It cannot preserve every 32-bit integer sample.
Use the Fern decoder when exact source precision exceeds the platform output format.

Limits bound each packet, each PCM buffer, codec-specific data, sample rate, and channel count.
They do not bound allocations inside Android. Input and output each use platform backpressure.

Output views borrow platform memory. Release each output once after use.
Views and input slots expire on release, flush, or close. Do not copy decoder handles or use them concurrently.
Close each opened decoder once. After input EOS, drain output through an EOS buffer.

Flush invalidates buffers and resets the stream. After seek, submit the required configuration and preroll again.
This module does not trim encoder delay, apply replay gain, resample, or perform audio offload.

```fern
<<*>>=
import "std/android/audio/capabilities";
import "std/android/video/capabilities" as policy;
import "std/android/video" as shared;
import "std/video/frame";

pub struct Limits { max_packet_bytes: i32, max_output_bytes: i32, max_sample_rate: i32, max_channels: i32 }

pub struct Config { request: capabilities.Request, csd0: u8[], csd1: u8[], csd2: u8[], pcm_encoding: i32 }

pub struct Decoder { handle: usize, scratch: usize, limits: Limits, pcm_encoding: i32 }

pub struct Format { sample_rate: i32, channels: i32, encoding: i32, channel_mask: i32 }

pub enum Output { Retry, BuffersChanged, FormatChanged(Format), Buffer(shared.OutputBuffer) }

pub struct Attempt { name: string, error: frame.VideoError }

pub struct Selected { decoder: Decoder, info: capabilities.CodecInfo, attempts: Attempt[] }

pub struct OpenFailure { error: frame.VideoError, attempts: Attempt[] }

pub const PCM_I16: i32 = 2;

pub const PCM_F32: i32 = 4;

pub const FLAG_CODEC_CONFIG: u32 = 2u32;

pub const FLAG_END_OF_STREAM: u32 = 4u32;

pub function default_limits(): Limits {
  return Limits { max_packet_bytes: 1048576, max_output_bytes: 1048576, max_sample_rate: 192000, max_channels: 8 };
}

function valid_limits(request: capabilities.Request, limits: Limits): boolean {
  return limits.max_packet_bytes > 0 && limits.max_output_bytes > 0 && limits.max_sample_rate >= request.sample_rate && limits.max_channels >= request.channels && limits.max_channels <= 8 && limits.max_sample_rate <= 768000;
}

function valid_encoding(value: i32): boolean {
  return value == PCM_I16 || value == PCM_F32;
}

function validate(config: Config, name: string, limits: Limits): Result[void, frame.VideoError] {
  if (config.pcm_encoding != 0 && !valid_encoding(config.pcm_encoding)) {
    return Err(frame.Unsupported);
  }
  if (!capabilities.valid(config.request) || name.len() < 1 || name.len() > 512) {
    return Err(frame.Header);
  }
  for b in name.as_bytes() {
    if (b == 0u8) {
      return Err(frame.Header);
    }
  }
  if (!valid_limits(config.request, limits)) {
    return Err(frame.Limit);
  }
  let remaining = limits.max_packet_bytes;
  for bytes in [config.csd0, config.csd1, config.csd2] {
    if (bytes.len() > remaining) {
      return Err(frame.Limit);
    }
    remaining -= bytes.len();
  }
  return Ok(());
}

pub function open_named(config: Config, name: string, limits: Limits): Result[Decoder, frame.VideoError] {
  validate(config, name, limits)?;
  let format = shared.c_format_new();
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  let key = shared.c_string("mime");
  let mime = shared.c_string(config.request.mime);
  shared.c_set_string(format, key, mime);
  shared.free_string(key, "mime");
  shared.free_string(mime, config.request.mime);
  shared.set_i32(format, "sample-rate", config.request.sample_rate);
  shared.set_i32(format, "channel-count", config.request.channels);
  shared.set_i32(format, "max-input-size", limits.max_packet_bytes);
  if (config.pcm_encoding != 0) {
    shared.set_i32(format, "pcm-encoding", config.pcm_encoding);
  }
  if (config.request.profile > 0) {
    shared.set_i32(format, if (config.request.mime == "audio/mp4a-latm") { "aac-profile" } else { "profile" }, config.request.profile);
  }
  shared.set_buffer(format, "csd-0", config.csd0);
  shared.set_buffer(format, "csd-1", config.csd1);
  shared.set_buffer(format, "csd-2", config.csd2);
  let named = shared.c_string(name);
  let handle = shared.c_create_name(named);
  shared.free_string(named, name);
  if (handle == 0 as usize) {
    let _ = shared.c_format_delete(format);
    return Err(frame.Unsupported);
  }
  let configured = shared.c_configure(handle, format, 0 as usize, 0 as usize, 0u32);
  let _ = shared.c_format_delete(format);
  if (configured != 0 || shared.c_start(handle) != 0) {
    let _ = shared.c_delete(handle);
    return Err(frame.Platform);
  }
  unsafe {
    return Ok(Decoder { handle: handle, scratch: __alloc(32), limits: limits, pcm_encoding: config.pcm_encoding });
  }
}

pub function open_selected(config: Config, infos: [capabilities.CodecInfo], selection: policy.Policy, limits: Limits): Result[Selected, OpenFailure] {
  if let Err(error) = validate(config, "selected", limits) {
    return Err(OpenFailure { error: error, attempts: [] });
  }
  let attempts: Attempt[] = [];
  for info in capabilities.ordered(infos, config.request, selection) {
    match (open_named(config, info.name, limits)) {
      Ok(decoder) => {
        return Ok(Selected { decoder: decoder, info: info, attempts: attempts });
      },
      Err(error) => {
        attempts = attempts.append(Attempt { name: info.name, error: error });
      }
    }
  }
  return Err(OpenFailure { error: frame.Unsupported, attempts: attempts });
}

pub function open_preferred(env: usize, api_level: i32, config: Config, limits: Limits): Result[Selected, OpenFailure] {
  let infos = match (capabilities.discover(env, api_level, config.request, 256)) {
    Ok(value) => value,
    Err(error) => { return Err(OpenFailure { error: error, attempts: [] }); },
  };
  return open_selected(config, infos, policy.PreferHardware, limits);
}

// Close the failed codec. The caller restores configuration, packet position, and preroll.
pub function fallback_to_software(failed: Selected, config: Config, infos: [capabilities.CodecInfo], limits: Limits): Result[Selected, OpenFailure] {
  let _ = failed.decoder.close();
  let candidates: capabilities.CodecInfo[] = [];
  for info in infos {
    if (info.name != failed.info.name) {
      candidates = candidates.append(info);
    }
  }
  return open_selected(config, candidates, policy.SoftwareOnly, limits);
}

pub function (decoder: Decoder) close(): Result[void, frame.VideoError] {
  return shared.close_handle(decoder.handle, decoder.scratch);
}

pub function (decoder: Decoder) flush(): Result[void, frame.VideoError] {
  if (shared.c_flush(decoder.handle) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (decoder: Decoder) input(timeout_us: i64): Result[Option[shared.InputBuffer], frame.VideoError] {
  return shared.input_handle(decoder.handle, decoder.scratch, decoder.limits.max_packet_bytes, timeout_us);
}

pub function (decoder: Decoder) submit(slot: shared.InputBuffer, bytes: [u8], time_us: i64, flags: u32): Result[void, frame.VideoError] {
  return shared.submit_handle(decoder.handle, decoder.limits.max_packet_bytes, slot, bytes, time_us, flags);
}

function output_format(decoder: Decoder): Result[Format, frame.VideoError] {
  let format = shared.c_output_format(decoder.handle);
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  let result = Format {
    sample_rate: shared.get_i32(format, "sample-rate", 0, decoder.scratch),
    channels: shared.get_i32(format, "channel-count", 0, decoder.scratch),
    encoding: shared.get_i32(format, "pcm-encoding", PCM_I16, decoder.scratch),
    channel_mask: shared.get_i32(format, "channel-mask", 0, decoder.scratch),
  };
  let _ = shared.c_format_delete(format);
  if (result.sample_rate < 1 || result.channels < 1) {
    return Err(frame.Platform);
  }
  if (result.sample_rate > decoder.limits.max_sample_rate || result.channels > decoder.limits.max_channels) {
    return Err(frame.Limit);
  }
  if (!valid_encoding(result.encoding) || decoder.pcm_encoding != 0 && result.encoding != decoder.pcm_encoding) {
    return Err(frame.Unsupported);
  }
  return Ok(result);
}

pub function (decoder: Decoder) output(timeout_us: i64): Result[Output, frame.VideoError] {
  if (timeout_us < 0i64) {
    return Err(frame.Header);
  }
  let index = shared.c_dequeue_output(decoder.handle, decoder.scratch, timeout_us);
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
    let info = shared.output_buffer(index as i32, decoder.scratch);
    if (info.offset < 0 || info.size < 0 || info.size > decoder.limits.max_output_bytes) {
      let _ = shared.c_release(decoder.handle, index as usize, 0u8);
      return Err(if (info.size > decoder.limits.max_output_bytes) { frame.Limit } else { frame.Platform });
    }
    return Ok(Buffer(shared.OutputBuffer { index: index as i32, offset: info.offset, size: info.size, presentation_time_us: info.presentation_time_us, flags: info.flags }));
  }
}

pub function (decoder: Decoder) output_view(buffer: shared.OutputBuffer): Result[shared.BufferView, frame.VideoError] {
  return shared.view_output(decoder.handle, decoder.scratch, decoder.limits.max_output_bytes, buffer);
}

pub function (decoder: Decoder) copy_output(buffer: shared.OutputBuffer, own bytes: u8[]): Result[shared.Packet, frame.VideoError] {
  return shared.copy_view(decoder.output_view(buffer)?, buffer, bytes);
}

pub function (decoder: Decoder) release(buffer: shared.OutputBuffer): Result[void, frame.VideoError] {
  if (buffer.index < 0) {
    return Err(frame.Header);
  }
  if (shared.c_release(decoder.handle, buffer.index as usize, 0u8) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}
```
