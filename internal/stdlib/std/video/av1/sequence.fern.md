# AV1 sequence headers

This module inspects one complete self-delimited AV1 sequence-header OBU.
It reuses the OBU iterator and bounded MSB reader, without a payload copy or retained input bytes.
The result reports maximum frame dimensions, precision, color metadata, and sequence tool flags.
Maximum dimensions are sequence bounds, rather than the dimensions of every later frame.

The packet budget applies before OBU access. The sequence payload has an additional 512-byte ceiling.
The pixel budget applies to the maximum frame area before further syntax checks.
Fixed loops cover up to 32 operating points and bounded timing fields.
The reader checks the complete sequence syntax and trailing bits.

It does not validate coded frames, reference state, operating-point relationships, profile/level conformance, or decoder support.
Per-frame render dimensions and entropy-coded data remain outside this API.
This module does not decode pixels or assign a presentation timeline.

```fern
<<*>>=
// Inspect bounded AV1 sequence metadata without picture reconstruction.
import "std/video/av1";
import "std/video/frame";
import "std/audio/alac/bits";
import "std/audio/pcm";

pub const MAX_SEQUENCE_BYTES: i32 = 512;

pub struct Header { profile: i32, maximum: frame.Format, bit_depth: i32, monochrome: boolean, subsampling_x: i32, subsampling_y: i32, chroma_sample_position: i32, color_primaries: i32, transfer_characteristics: i32, matrix_coefficients: i32, full_range: boolean, still_picture: boolean, reduced_still_picture: boolean, operating_points: i32, frame_id_bits: i32, order_hint_bits: i32, use_128x128_superblock: boolean, enable_superres: boolean, enable_cdef: boolean, enable_restoration: boolean, film_grain: boolean }

struct Prefix { at: i64, profile: i32, still: boolean, reduced: boolean, points: i32 }

struct Tools { at: i64, frame_ids: i32, order: i32, large: boolean, superres: boolean, cdef: boolean, restoration: boolean }

struct Color { at: i64, depth: i32, mono: boolean, primaries: i32, transfer: i32, matrix: i32, full: boolean, x: i32, y: i32, position: i32 }

function read(input: [u8], at: i64, width: i32): Result[(i64, u32), frame.VideoError] {
  return match (bits.read(input, at, width)) { Ok(value) => Ok(value), Err(pcm.Truncated) => Err(frame.Truncated), _ => Err(frame.Header) };
}

function skip(input: [u8], at: i64, count: i64): Result[i64, frame.VideoError] {
  return match (bits.skip(input, at, count)) { Ok(value) => Ok(value), Err(pcm.Truncated) => Err(frame.Truncated), _ => Err(frame.Header) };
}

// The timing uvlc value must be below UINT32_MAX, so 32 leading zeros are invalid.
function picture_ticks(input: [u8], at: i64): Result[i64, frame.VideoError] {
  for zeros in 0..32 {
    let (next, value) = read(input, at, 1)?;
    at = next;
    if (value != 0u32) {
      return skip(input, at, zeros as i64);
    }
  }
  return Err(frame.Header);
}

function timing(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (next0, ticks) = read(input, at, 32)?;
  let (next1, scale) = read(input, next0, 32)?;
  if (ticks == 0u32 || scale == 0u32) {
    return Err(frame.Header);
  }
  let (next2, equal) = read(input, next1, 1)?;
  return if (equal != 0u32) { picture_ticks(input, next2) } else { Ok(next2) };
}

function model(input: [u8], at: i64): Result[(i64, i32), frame.VideoError] {
  let (next0, present) = read(input, at, 1)?;
  at = next0;
  if (present != 0u32) {
    at = timing(input, at)?;
    let (next1, decoder) = read(input, at, 1)?;
    at = next1;
    if (decoder != 0u32) {
      let (next2, width) = read(input, at, 5)?;
      let (next3, ticks) = read(input, next2, 32)?;
      if (ticks == 0u32) {
        return Err(frame.Header);
      }
      return Ok((skip(input, next3, 10i64)?, width as i32 + 1));
    }
  }
  return Ok((at, 0));
}

function optional_skip(input: [u8], at: i64, count: i64): Result[i64, frame.VideoError] {
  let (next, present) = read(input, at, 1)?;
  return if (present == 0u32) { Ok(next) } else { skip(input, next, count) };
}

function level_matches(level: u32, expected: Option[(i32, i32)]): boolean {
  return match (expected) {
    Some((wanted, tier)) => level as i32 == wanted && (level > 7u32 || tier == 0),
    None => true,
  };
}

function tier(input: [u8], at: i64, expected: Option[(i32, i32)]): Result[i64, frame.VideoError] {
  let Some((_, wanted)) = expected else {
    return skip(input, at, 1i64);
  };
  let (next, value) = read(input, at, 1)?;
  return if (value as i32 == wanted) { Ok(next) } else { Err(frame.Header) };
}

function point(input: [u8], at: i64, width: i32, display: boolean, expected: Option[(i32, i32)]): Result[i64, frame.VideoError] {
  at = skip(input, at, 12i64)?;
  let (next0, level) = read(input, at, 5)?;
  at = next0;
  if (!level_matches(level, expected)) {
    return Err(frame.Header);
  }
  if (level > 7u32) {
    at = tier(input, at, expected)?;
  }
  if (width != 0) {
    at = optional_skip(input, at, (2 * width + 1) as i64)?;
  }
  if (display) {
    at = optional_skip(input, at, 4i64)?;
  }
  return Ok(at);
}

function reduced_point(input: [u8], at: i64, expected: Option[(i32, i32)]): Result[(i64, i32), frame.VideoError] {
  let Some((wanted, tier)) = expected else {
    return Ok((skip(input, at, 5i64)?, 1));
  };
  let (next, level) = read(input, at, 5)?;
  if (level as i32 != wanted || tier != 0) {
    return Err(frame.Header);
  }
  return Ok((next, 1));
}

function operating(input: [u8], at: i64, reduced: boolean, expected: Option[(i32, i32)]): Result[(i64, i32), frame.VideoError] {
  if (reduced) {
    return reduced_point(input, at, expected);
  }
  let (next0, width) = model(input, at)?;
  let (next1, display) = read(input, next0, 1)?;
  let (next2, count) = read(input, next1, 5)?;
  at = next2;
  for index in 0..count as i32 + 1 {
    at = point(input, at, width, display != 0u32, if (index == 0) { expected } else { None })?;
  }
  return Ok((at, count as i32 + 1));
}

function prefix(input: [u8], expected: Option[(i32, i32)]): Result[Prefix, frame.VideoError] {
  let (next0, profile) = read(input, 0i64, 3)?;
  if (profile > 2u32) {
    return Err(frame.Unsupported);
  }
  let (next1, still) = read(input, next0, 1)?;
  let (next2, reduced) = read(input, next1, 1)?;
  if (reduced != 0u32 && still == 0u32) {
    return Err(frame.Header);
  }
  let (at, points) = operating(input, next2, reduced != 0u32, expected)?;
  return Ok(Prefix { at: at, profile: profile as i32, still: still != 0u32, reduced: reduced != 0u32, points: points });
}

function dimensions(input: [u8], at: i64): Result[(i64, frame.Format), frame.VideoError] {
  let (next0, width_bits) = read(input, at, 4)?;
  let (next1, height_bits) = read(input, next0, 4)?;
  let (next2, width) = read(input, next1, width_bits as i32 + 1)?;
  let (next3, height) = read(input, next2, height_bits as i32 + 1)?;
  return Ok((next3, frame.Format { width: width as i32 + 1, height: height as i32 + 1 }));
}

function frame_ids(input: [u8], at: i64, reduced: boolean): Result[(i64, i32), frame.VideoError] {
  if (reduced) {
    return Ok((at, 0));
  }
  let (next0, present) = read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((next0, 0));
  }
  let (next1, delta) = read(input, next0, 4)?;
  let (next2, extra) = read(input, next1, 3)?;
  let width = delta as i32 + extra as i32 + 3;
  if (width > 16) {
    return Err(frame.Header);
  }
  return Ok((next2, width));
}

function screen_tools(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (next0, choose) = read(input, at, 1)?;
  at = next0;
  let force = 2u32;
  if (choose == 0u32) {
    let (next1, value) = read(input, at, 1)?;
    at = next1;
    force = value;
  }
  if (force > 0u32) {
    let (next2, integer) = read(input, at, 1)?;
    at = next2;
    if (integer == 0u32) {
      at = skip(input, at, 1i64)?;
    }
  }
  return Ok(at);
}

function inter_tools(input: [u8], at: i64, reduced: boolean): Result[(i64, i32), frame.VideoError] {
  if (reduced) {
    return Ok((at, 0));
  }
  at = skip(input, at, 4i64)?;
  let (next0, order) = read(input, at, 1)?;
  at = next0;
  if (order != 0u32) {
    at = skip(input, at, 2i64)?;
  }
  at = screen_tools(input, at)?;
  if (order == 0u32) {
    return Ok((at, 0));
  }
  let (next1, width) = read(input, at, 3)?;
  return Ok((next1, width as i32 + 1));
}

function tools(input: [u8], at: i64, reduced: boolean): Result[Tools, frame.VideoError] {
  let (next0, ids) = frame_ids(input, at, reduced)?;
  let (next1, large) = read(input, next0, 1)?;
  let next2 = skip(input, next1, 2i64)?;
  let (next3, order) = inter_tools(input, next2, reduced)?;
  let (next4, flags) = read(input, next3, 3)?;
  return Ok(Tools { at: next4, frame_ids: ids, order: order, large: large != 0u32, superres: (flags & 4u32) != 0u32, cdef: (flags & 2u32) != 0u32, restoration: (flags & 1u32) != 0u32 });
}

function depth(input: [u8], at: i64, profile: i32): Result[(i64, i32), frame.VideoError] {
  let (next0, high) = read(input, at, 1)?;
  if (profile != 2 || high == 0u32) {
    return Ok((next0, if (high == 0u32) { 8 } else { 10 }));
  }
  let (next1, twelve) = read(input, next0, 1)?;
  return Ok((next1, if (twelve == 0u32) { 10 } else { 12 }));
}

function description(input: [u8], at: i64): Result[(i64, i32, i32, i32), frame.VideoError] {
  let (next0, present) = read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((next0, 2, 2, 2));
  }
  let (next1, primaries) = read(input, next0, 8)?;
  let (next2, transfer) = read(input, next1, 8)?;
  let (next3, matrix) = read(input, next2, 8)?;
  return Ok((next3, primaries as i32, transfer as i32, matrix as i32));
}

function subsample(input: [u8], at: i64, profile: i32, precision: i32): Result[(i64, i32, i32), frame.VideoError] {
  if (profile == 0) {
    return Ok((at, 1, 1));
  }
  if (profile == 1) {
    return Ok((at, 0, 0));
  }
  if (precision != 12) {
    return Ok((at, 1, 0));
  }
  let (next0, x) = read(input, at, 1)?;
  if (x == 0u32) {
    return Ok((next0, 0, 0));
  }
  let (next1, y) = read(input, next0, 1)?;
  return Ok((next1, 1, y as i32));
}

function sample_position(input: [u8], at: i64, x: i32, y: i32): Result[(i64, i32), frame.VideoError] {
  if (x == 1 && y == 1) {
    let (next, sample) = read(input, at, 2)?;
    if (sample == 3u32) {
      return Err(frame.Header);
    }
    return Ok((next, sample as i32));
  }
  return Ok((at, 0));
}

function regular_color(input: [u8], value: Color, profile: i32): Result[Color, frame.VideoError] {
  let (next0, full) = read(input, value.at, 1)?;
  let (next1, x, y) = subsample(input, next0, profile, value.depth)?;
  let (at, position) = sample_position(input, next1, x, y)?;
  if (value.matrix == 0 && (x != 0 || y != 0)) {
    return Err(frame.Header);
  }
  return Ok(Color { ...value, at: skip(input, at, 1i64)?, full: full != 0u32, x: x, y: y, position: position });
}

function sampling(input: [u8], value: Color, profile: i32): Result[Color, frame.VideoError] {
  if (value.mono) {
    let (at, full) = read(input, value.at, 1)?;
    return Ok(Color { ...value, at: at, full: full != 0u32, x: 1, y: 1 });
  }
  if (value.primaries == 1 && value.transfer == 13 && value.matrix == 0) {
    if (profile == 0 || profile == 2 && value.depth != 12) {
      return Err(frame.Header);
    }
    return Ok(Color { ...value, at: skip(input, value.at, 1i64)?, full: true });
  }
  return regular_color(input, value, profile);
}

function color(input: [u8], at: i64, profile: i32): Result[Color, frame.VideoError] {
  let (next0, precision) = depth(input, at, profile)?;
  at = next0;
  let mono = false;
  if (profile != 1) {
    let (next1, present) = read(input, at, 1)?;
    at = next1;
    mono = present != 0u32;
  }
  let (next2, primaries, transfer, matrix) = description(input, at)?;
  return sampling(input, Color { at: next2, depth: precision, mono: mono, primaries: primaries, transfer: transfer, matrix: matrix, full: false, x: 0, y: 0, position: 0 }, profile);
}

function trailing(input: [u8], at: i64): Result[void, frame.VideoError] {
  let (next0, one) = read(input, at, 1)?;
  if (one != 1u32) {
    return Err(frame.Header);
  }
  at = next0;
  while (at % 8i64 != 0i64) {
    let (next1, zero) = read(input, at, 1)?;
    if (zero != 0u32) {
      return Err(frame.Header);
    }
    at = next1;
  }
  for byte in input[(at / 8i64) as i32:] {
    if (byte != 0u8) {
      return Err(frame.Header);
    }
  }
  return Ok(());
}

function payload(input: [u8], limits: frame.Limits, expected: Option[(i32, i32)]): Result[Header, frame.VideoError] {
  let p = prefix(input, expected)?;
  let (at, maximum) = dimensions(input, p.at)?;
  let _ = frame.pixel_count(maximum, limits)?;
  let t = tools(input, at, p.reduced)?;
  let c = color(input, t.at, p.profile)?;
  let (end, grain) = read(input, c.at, 1)?;
  trailing(input, end)?;
  return Ok(Header { profile: p.profile, maximum: maximum, bit_depth: c.depth, monochrome: c.mono, subsampling_x: c.x, subsampling_y: c.y, chroma_sample_position: c.position, color_primaries: c.primaries, transfer_characteristics: c.transfer, matrix_coefficients: c.matrix, full_range: c.full, still_picture: p.still, reduced_still_picture: p.reduced, operating_points: p.points, frame_id_bits: t.frame_ids, order_hint_bits: t.order, use_128x128_superblock: t.large, enable_superres: t.superres, enable_cdef: t.cdef, enable_restoration: t.restoration, film_grain: grain != 0u32 });
}

function inspect_impl(input: [u8], limits: frame.Limits, expected: Option[(i32, i32)]): Result[Header, frame.VideoError] {
  let Some(unit) = av1.next_obu(input, 0, limits)? else {
    return Err(frame.Truncated);
  };
  if (unit.kind != 1) {
    return Err(frame.Unsupported);
  }
  if (unit.extension || unit.next_offset != input.len()) {
    return Err(frame.Header);
  }
  if (unit.size > MAX_SEQUENCE_BYTES) {
    return Err(frame.Limit);
  }
  return payload(input[unit.offset:unit.next_offset], limits, expected);
}

pub function inspect(input: [u8], limits: frame.Limits): Result[Header, frame.VideoError] {
  return inspect_impl(input, limits, None);
}

pub(package) function inspect_record(input: [u8], limits: frame.Limits, level: i32, tier: i32): Result[Header, frame.VideoError] {
  return inspect_impl(input, limits, Some((level, tier)));
}
```
