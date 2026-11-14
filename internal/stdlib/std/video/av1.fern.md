# AV1 low-overhead OBU ranges

This module reads self-delimited OBUs from one AV1 low-overhead packet.
The iterator returns payload ranges without a copy or retained input bytes.
Start at offset zero. Continue at `next_offset` until the result is `None`.
The caller supplies an immutable packet and preserves its boundaries.

The packet budget applies before any header access.
Each call examines at most two header bytes and eight size bytes.
Header and extension reserved bits must be zero.
Sequence headers, temporal delimiters, and tile lists must not have extension headers.
These OBU types are not layer-specific under AV1 section 6.2.2.

An absent size field or a reserved OBU type returns `Unsupported`.
Annex B temporal-unit and frame-unit lengths are outside this API.

Zero-length payloads and nonminimal size encodings are permitted.
The payload must fit the packet, and the decoded size cannot exceed 32 bits.
The result does not validate payload syntax, OBU order, temporal units, or complete coded frames.
It does not decode pixels, assign timestamps, or establish decoder support.

The [AV1 specification](https://aomediacodec.github.io/av1-spec/) defines the OBU header and `leb128` conformance rules.
Normal tests include every prefix of the independently declared OBU ranges.
FFmpeg 7 accepts an eighth size byte with its continuation bit set.
This reader rejects that form under the specification's termination rule.

```fern
<<*>>=
// Read bounded AV1 low-overhead OBU ranges without payload copies.
import "std/video/frame";

pub struct Unit { kind: i32, extension: boolean, temporal_id: i32, spatial_id: i32, offset: i32, size: i32, next_offset: i32 }

struct Header { kind: i32, extension: boolean, temporal_id: i32, spatial_id: i32, next: i32 }

function valid_extension(kind: i32, extension: boolean): boolean {
  return !extension || kind != 1 && kind != 2 && kind != 8;
}

function supported_header(kind: i32, sized: boolean, unsized: boolean): boolean {
  return (kind >= 1 && kind <= 8 || kind == 15) && (sized || unsized);
}

function header(input: [u8], at: i32, unsized: boolean): Result[Header, frame.VideoError] {
  let byte = input[at];
  if ((byte & 129u8) != 0u8) {
    return Err(frame.Header);
  }
  let kind = (byte >> 3 & 15u8) as i32;
  if (!supported_header(kind, (byte & 2u8) != 0u8, unsized)) {
    return Err(frame.Unsupported);
  }
  let extension = (byte & 4u8) != 0u8;
  if (!valid_extension(kind, extension)) {
    return Err(frame.Header);
  }
  if (!extension) {
    return Ok(Header { kind: kind, extension: false, temporal_id: 0, spatial_id: 0, next: at + 1 });
  }
  if (input.len() - at < 2) {
    return Err(frame.Truncated);
  }
  let extra = input[at + 1];
  if ((extra & 7u8) != 0u8) {
    return Err(frame.Header);
  }
  return Ok(Header { kind: kind, extension: true, temporal_id: (extra >> 5) as i32, spatial_id: (extra >> 3 & 3u8) as i32, next: at + 2 });
}

pub(package) function leb128(input: [u8], at: i32): Result[(i32, u64), frame.VideoError] {
  let size = 0u64;
  for i in 0..8 {
    if (i >= input.len() - at) {
      return Err(frame.Truncated);
    }
    let byte = input[at + i];
    size |= (byte & 127u8) as u64 << (i * 7) as u64;
    if ((byte & 128u8) == 0u8) {
      if (size > 4294967295u64) {
        return Err(frame.Header);
      }
      return Ok((at + i + 1, size));
    }
  }
  return Err(frame.Header);
}

pub function next_obu(input: [u8], offset: i32, limits: frame.Limits): Result[Option[Unit], frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (offset < 0 || offset > input.len()) {
    return Err(frame.Header);
  }
  if (offset == input.len()) {
    return Ok(None);
  }
  let value = header(input, offset, false)?;
  let (payload, size) = leb128(input, value.next)?;
  if (size > (input.len() - payload) as u64) {
    return Err(frame.Truncated);
  }
  let end = payload + size as i32;
  return Ok(Some(Unit { kind: value.kind, extension: value.extension, temporal_id: value.temporal_id, spatial_id: value.spatial_id, offset: payload, size: size as i32, next_offset: end }));
}

// The enclosing Annex B length supplies an absent OBU size.
pub(package) function delimited(input: [u8]): Result[Unit, frame.VideoError] {
  if (input.len() == 0) {
    return Err(frame.Truncated);
  }
  let value = header(input, 0, true)?;
  let payload = value.next;
  if ((input[0] & 2u8) != 0u8) {
    let (at, size) = leb128(input, payload)?;
    if (size != (input.len() - at) as u64) {
      return Err(frame.Header);
    }
    payload = at;
  }
  return Ok(Unit { kind: value.kind, extension: value.extension, temporal_id: value.temporal_id, spatial_id: value.spatial_id, offset: payload, size: input.len() - payload, next_offset: input.len() });
}
```
