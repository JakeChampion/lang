# VP9 configuration records

This module inspects the complete payload of one MP4 `vpcC` box, including its version and flags.
It accepts version one, zero flags, and the eight-byte VP9 configuration record.
The record must contain no initialization data.

The packet budget applies before access. The result contains eight scalar fields and retains no input bytes.
Profile, level identifier, precision, chroma, and RGB constraints follow the published VP Codec ISO Media File Format Binding version 1.0.
Color identifiers remain scalar values. This API does not establish color, profile/level, coded-sample, or complete-stream conformance.
It does not decode pixels.

```fern
<<*>>=
// Inspect a bounded version-one VP9 configuration without payload copies.
import "std/video/frame";

pub struct Header { profile: i32, level: i32, bit_depth: i32, chroma_subsampling: i32, full_range: boolean, color_primaries: i32, transfer_characteristics: i32, matrix_coefficients: i32 }

function level_ok(value: i32): boolean {
  for level in [0, 10, 11, 20, 21, 30, 31, 40, 41, 50, 51, 52, 60, 61, 62] {
    if (value == level) {
      return true;
    }
  }
  return false;
}

function precision_ok(h: Header): boolean {
  if (h.profile < 2) {
    return h.bit_depth == 8;
  }
  return h.bit_depth == 10 || h.bit_depth == 12;
}

function chroma_ok(h: Header): boolean {
  if (h.chroma_subsampling > 3 || h.matrix_coefficients == 0 && h.chroma_subsampling != 3) {
    return false;
  }
  return (h.profile & 1) == (if (h.chroma_subsampling < 2) { 0 } else { 1 });
}

function shape_ok(input: [u8]): boolean {
  return input.len() == 12 && input[1] == 0u8 && input[2] == 0u8 && input[3] == 0u8 && input[10] == 0u8 && input[11] == 0u8;
}

pub function inspect(input: [u8], limits: frame.Limits): Result[Header, frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() < 12) {
    return Err(frame.Truncated);
  }
  if (input[0] != 1u8) {
    return Err(frame.Unsupported);
  }
  if (!shape_ok(input)) {
    return Err(frame.Header);
  }
  let h = Header { profile: input[4] as i32, level: input[5] as i32, bit_depth: (input[6] >> 4) as i32, chroma_subsampling: (input[6] >> 1 & 7u8) as i32, full_range: (input[6] & 1u8) != 0u8, color_primaries: input[7] as i32, transfer_characteristics: input[8] as i32, matrix_coefficients: input[9] as i32 };
  if (h.profile > 3 || !level_ok(h.level) || !precision_ok(h) || !chroma_ok(h)) {
    return Err(frame.Header);
  }
  return Ok(h);
}
```
