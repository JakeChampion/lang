# AV1 decoder configuration records

This module inspects one AV1CodecConfigurationRecord from an MP4 `av1C` box.
It checks the four-byte header and each self-delimited configuration OBU.
An optional sequence header must come first and appear only once.
Its profile, first operating-point level and tier, precision, and chroma fields must match the record.

The packet budget applies before access. Sequence headers retain the existing 512-byte and maximum-area limits.
The result retains no input bytes. It reports sequence dimensions only when the record contains a sequence header.
Other OBU payloads, coded samples, decoder-model delays, and complete profile/level conformance remain outside this API.
This module does not decode pixels.

```fern
<<*>>=
// Inspect bounded AV1 configuration metadata without payload copies.
import "std/video/av1";
import "std/video/av1/sequence";
import "std/video/frame";

pub struct Header { profile: i32, level: i32, tier: i32, bit_depth: i32, monochrome: boolean, subsampling_x: i32, subsampling_y: i32, chroma_sample_position: i32, presentation_delay: i32, maximum: Option[frame.Format] }

function precision(profile: i32, flags: u8): Result[i32, frame.VideoError] {
  let high = (flags & 64u8) != 0u8;
  let twelve = (flags & 32u8) != 0u8;
  if (twelve && (!high || profile != 2)) {
    return Err(frame.Header);
  }
  return Ok(if (twelve) { 12 } else if (high) { 10 } else { 8 });
}

function professional_chroma(h: Header): boolean {
  if (h.bit_depth != 12) {
    return h.subsampling_x == 1 && h.subsampling_y == 0 && h.chroma_sample_position == 0;
  }
  return (h.subsampling_x != 0 || h.subsampling_y == 0) && (h.subsampling_x == 1 && h.subsampling_y == 1 || h.chroma_sample_position == 0);
}

function chroma_matches(h: Header): boolean {
  if (h.monochrome) {
    return h.profile != 1 && h.subsampling_x == 1 && h.subsampling_y == 1 && h.chroma_sample_position == 0;
  }
  if (h.profile == 0) {
    return h.subsampling_x == 1 && h.subsampling_y == 1;
  }
  if (h.profile == 1) {
    return h.subsampling_x == 0 && h.subsampling_y == 0 && h.chroma_sample_position == 0;
  }
  return professional_chroma(h);
}

function reserved_ok(value: u8): boolean {
  return (value & 224u8) == 0u8 && ((value & 16u8) != 0u8 || (value & 15u8) == 0u8);
}

function fields_ok(value: Header): boolean {
  return value.chroma_sample_position != 3 && (value.level > 7 || value.tier == 0) && chroma_matches(value);
}

function header(input: [u8]): Result[Header, frame.VideoError] {
  if (input.len() < 4) {
    return Err(frame.Truncated);
  }
  if ((input[0] & 128u8) == 0u8) {
    return Err(frame.Header);
  }
  if ((input[0] & 127u8) != 1u8) {
    return Err(frame.Unsupported);
  }
  let profile = (input[1] >> 5) as i32;
  if (profile > 2) {
    return Err(frame.Unsupported);
  }
  if (!reserved_ok(input[3])) {
    return Err(frame.Header);
  }
  let value = Header { profile: profile, level: (input[1] & 31u8) as i32, tier: (input[2] >> 7) as i32, bit_depth: precision(profile, input[2])?, monochrome: (input[2] & 16u8) != 0u8, subsampling_x: (input[2] >> 3 & 1u8) as i32, subsampling_y: (input[2] >> 2 & 1u8) as i32, chroma_sample_position: (input[2] & 3u8) as i32, presentation_delay: if ((input[3] & 16u8) != 0u8) { (input[3] & 15u8) as i32 + 1 } else { 0 }, maximum: None };
  if (!fields_ok(value)) {
    return Err(frame.Header);
  }
  return Ok(value);
}

function with_sequence(input: [u8], h: Header, limits: frame.Limits): Result[Header, frame.VideoError] {
  let s = sequence.inspect_record(input, limits, h.level, h.tier)?;
  if (s.profile != h.profile || s.bit_depth != h.bit_depth || s.monochrome != h.monochrome || s.subsampling_x != h.subsampling_x || s.subsampling_y != h.subsampling_y || s.chroma_sample_position != h.chroma_sample_position) {
    return Err(frame.Header);
  }
  return Ok(Header { ...h, maximum: Some(s.maximum) });
}

pub function inspect(input: [u8], limits: frame.Limits): Result[Header, frame.VideoError] {
  frame.check_packet(input, limits)?;
  let value = header(input)?;
  let at = 4;
  while (at < input.len()) {
    let Some(unit) = av1.next_obu(input, at, limits)? else {
      return Err(frame.Truncated);
    };
    if (unit.kind == 1) {
      if (at != 4) {
        return Err(frame.Header);
      }
      value = with_sequence(input[at:unit.next_offset], value, limits)?;
    }
    at = unit.next_offset;
  }
  return Ok(value);
}
```
