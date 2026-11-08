# IVF packet reader

IVF carries VP8, VP9, or AV1 packets and integer timestamps.
`inspect` reads the 32-byte version-zero header.
`next_packet` returns offsets into the caller's byte view without a packet copy.
It accepts one packet prefix and does not require the complete file.
An incomplete packet returns `Truncated`. An empty suffix returns `None`.

The frame count in the header is advisory.
A timestamp uses `scale / rate` seconds per tick.
The module does not decode compressed pixels or split AV1 temporal units.

```fern
<<*>>=
// This module reads IVF packet offsets and timestamps.
import "std/video/frame";
import "std/audio/wire";
import "core/cmp";

@derive(cmp.Eq, cmp.Debug)
pub enum Codec { VP8, VP9, AV1 }

pub struct Header { codec: Codec, format: frame.Format, rate: u32, scale: u32, frame_count: u32 }

pub struct Packet { offset: i32, size: i32, timestamp: u64, next_offset: i32 }

// The header parser checks each field of the IVF header in turn, and names
// the codec from its tag.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: frame.Limits): Result[Header, frame.VideoError] {
  if (input.len() < 32) {
    return Err(frame.Truncated);
  }
  if (!wire.tag(input, 0, "DKIF")) {
    return Err(frame.Header);
  }
  if (wire.le(input, 4, 2) != 0u64 || wire.le(input, 6, 2) != 32u64) {
    return Err(frame.Unsupported);
  }
  let codec: Codec = VP8;
  if (wire.tag(input, 8, "VP80")) {
    codec = VP8;
  } else if (wire.tag(input, 8, "VP90")) {
    codec = VP9;
  } else if (wire.tag(input, 8, "AV01")) {
    codec = AV1;
  } else {
    return Err(frame.Unsupported);
  }
  let format = frame.Format { width: wire.le(input, 12, 2) as i32, height: wire.le(input, 14, 2) as i32 };
  let _ = frame.pixel_count(format, limits)?;
  let rate = wire.le(input, 16, 4) as u32;
  let scale = wire.le(input, 20, 4) as u32;
  if (rate == 0u32 || scale == 0u32) {
    return Err(frame.Header);
  }
  return Ok(Header { codec: codec, format: format, rate: rate, scale: scale, frame_count: wire.le(input, 24, 4) as u32 });
}

pub function next_packet(input: [u8], at: i32, limits: frame.Limits): Result[Option[Packet], frame.VideoError] {
  if (limits.max_pixels < 0 || limits.max_packet_bytes < 0) {
    return Err(frame.Limit);
  }
  if (at < 0 || at > input.len()) {
    return Err(frame.Header);
  }
  if (at == input.len()) {
    return Ok(None);
  }
  if (input.len() - at < 12) {
    return Err(frame.Truncated);
  }
  let size = wire.le(input, at, 4);
  if (size == 0u64) {
    return Err(frame.Header);
  }
  if (size > limits.max_packet_bytes as u64) {
    return Err(frame.Limit);
  }
  if (size > (input.len() - at - 12) as u64) {
    return Err(frame.Truncated);
  }
  let start = at + 12;
  return Ok(Some(Packet { offset: start, size: size as i32, timestamp: wire.le(input, at + 4, 8), next_offset: start + size as i32 }));
}

pub function timestamp_us(timestamp: u64, rate: u32, scale: u32): Result[i64, frame.VideoError] {
  if (rate == 0u32 || scale == 0u32) {
    return Err(frame.Header);
  }
  // Split the division to avoid an unnecessary intermediate overflow.
  let factor = scale as u64 * 1000000u64;
  let whole = timestamp / (rate as u64);
  let remainder = timestamp % (rate as u64);
  let Some(a) = whole *? factor else {
    return Err(frame.Limit);
  };
  let b = remainder * (factor / (rate as u64)) + remainder * (factor % (rate as u64)) / (rate as u64);
  let Some(value) = a +? b else {
    return Err(frame.Limit);
  };
  if (value > 9223372036854775807u64) {
    return Err(frame.Limit);
  }
  return Ok(value as i64);
}
```
