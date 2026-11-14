# AV1 HDR metadata

This module reads one complete AV1 metadata OBU with content-light or mastering-display values.
It reuses the bounded OBU and LEB128 readers. The packet budget applies before header access.
The result retains scalar values without input bytes or a payload copy.

Content-light values preserve the unsigned 16-bit `max_cll` and `max_fall` fields from CEA-861.3.
Chromaticity values retain unsigned 0.16 fixed-point form, in red, green, blue, and white-point order.
Maximum and minimum luminance retain unsigned 24.8 and 18.14 fixed-point form, respectively.
Their units are candelas per square meter.

The reader checks fixed field extents and trailing bits before it returns metadata.
Additional trailing zero bytes remain within the packet budget.
Both supported metadata types are not layer-specific and prohibit an extension header, including zero layer identifiers.
Other metadata types return `Unsupported`.

The caller supplies one immutable OBU, including its header and size field.
Use the existing OBU cursor to select it from a packet.
The reader does not validate other metadata payloads, physical display properties, or cross-field color constraints.
It does not configure Android, convert colors, apply tone mapping, or decode pixels.

The syntax and units follow [AV1 sections 5.8 and 6.7](https://aomediacodec.github.io/av1-spec/).

```fern
<<*>>=
// Read native AV1 content-light and mastering-display metadata.
import "std/video/av1";
import "std/video/frame";
import "std/audio/wire";

pub struct Light { max_cll: u32, max_fall: u32 }

pub struct Chromaticity { x: u32, y: u32 }

pub struct Display { red: Chromaticity, green: Chromaticity, blue: Chromaticity, white: Chromaticity, max_luminance: u32, min_luminance: u32 }

pub enum Metadata { ContentLight(Light), MasteringDisplay(Display) }

function trailing(input: [u8], at: i32): Result[void, frame.VideoError] {
  if (at >= input.len()) {
    return Err(frame.Truncated);
  }
  if (input[at] != 128u8) {
    return Err(frame.Header);
  }
  for byte in input[at + 1:] {
    if (byte != 0u8) {
      return Err(frame.Header);
    }
  }
  return Ok(());
}

function chromaticity(input: [u8], at: i32): Chromaticity {
  return Chromaticity { x: wire.be(input, at, 2) as u32, y: wire.be(input, at + 2, 2) as u32 };
}

function light(input: [u8], at: i32): Result[Metadata, frame.VideoError] {
  if (input.len() - at < 4) {
    return Err(frame.Truncated);
  }
  trailing(input, at + 4)?;
  return Ok(ContentLight(Light { max_cll: wire.be(input, at, 2) as u32, max_fall: wire.be(input, at + 2, 2) as u32 }));
}

function display(input: [u8], at: i32): Result[Metadata, frame.VideoError] {
  if (input.len() - at < 24) {
    return Err(frame.Truncated);
  }
  trailing(input, at + 24)?;
  return Ok(MasteringDisplay(Display { red: chromaticity(input, at), green: chromaticity(input, at + 4), blue: chromaticity(input, at + 8), white: chromaticity(input, at + 12), max_luminance: wire.be(input, at + 16, 4) as u32, min_luminance: wire.be(input, at + 20, 4) as u32 }));
}

pub function inspect(input: [u8], limits: frame.Limits): Result[Metadata, frame.VideoError] {
  let Some(unit) = av1.next_obu(input, 0, limits)? else {
    return Err(frame.Truncated);
  };
  if (unit.kind != 5) {
    return Err(frame.Unsupported);
  }
  if (unit.next_offset != input.len()) {
    return Err(frame.Header);
  }
  let payload = input[unit.offset:unit.next_offset];
  let (at, kind) = av1.leb128(payload, 0)?;
  if (kind != 1u64 && kind != 2u64) {
    return Err(frame.Unsupported);
  }
  if (unit.extension) {
    return Err(frame.Header);
  }
  return if (kind == 1u64) { light(payload, at) } else { display(payload, at) };
}
```
