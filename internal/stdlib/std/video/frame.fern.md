# Video frames

Pixels are packed `u32` values in numeric `0xAARRGGBB` order.
The byte order of a pixel in memory depends on the target.
Alpha is straight, not premultiplied.
The modules do not infer a transfer function or a color profile.

Limits bound logical pixel storage and individual compressed packets.
They do not bound process memory or allocations inside a platform decoder.

```fern
<<*>>=
// This module provides video frame values and resource limits.
import "core/cmp";
import "core/error";

@derive(cmp.Eq, cmp.Debug)
pub enum VideoError { Header, Truncated, Unsupported, Limit, Buffer, Platform }

impl error.Error for VideoError {}

impl cmp.Display for VideoError {}

pub function (e: VideoError) message(): string {
  return match (e) {
    Header => "invalid video header or packet",
    Truncated => "incomplete video packet",
    Unsupported => "unsupported video format",
    Limit => "video exceeds the configured limit",
    Buffer => "video buffer is too small",
    Platform => "platform video operation failed",
  };
}

pub function (e: VideoError) to_string(): string {
  return e.message();
}

@derive(cmp.Eq, cmp.Debug)
pub struct Format { width: i32, height: i32 }

pub struct Frame { format: Format, pixels: u32[] }

pub struct Limits { max_pixels: i32, max_packet_bytes: i32 }

pub function default_limits(): Limits {
  return Limits { max_pixels: 8388608, max_packet_bytes: 16777216 };
}

pub function pixel_count(format: Format, limits: Limits): Result[i32, VideoError] {
  if (format.width <= 0 || format.height <= 0) {
    return Err(Header);
  }
  if (limits.max_pixels < 0 || limits.max_packet_bytes < 0) {
    return Err(Limit);
  }
  let Some(count) = format.width *? format.height else {
    return Err(Limit);
  };
  // Native scalar array slots use eight bytes, including u32 values.
  if (count > limits.max_pixels || count > 268435455) {
    return Err(Limit);
  }
  return Ok(count);
}

pub function check_packet(input: [u8], limits: Limits): Result[void, VideoError] {
  if (limits.max_pixels < 0 || limits.max_packet_bytes < 0 || input.len() > limits.max_packet_bytes) {
    return Err(Limit);
  }
  return Ok(());
}

pub function create(format: Format, limits: Limits): Result[Frame, VideoError] {
  let count = pixel_count(format, limits)?;
  let pixels: u32[] = [];
  for _ in 0..count {
    pixels = pixels.append(0u32);
  }
  return Ok(Frame { format: format, pixels: pixels });
}
```
