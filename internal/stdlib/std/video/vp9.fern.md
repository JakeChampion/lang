# VP9 keyframe headers

This module inspects the uncompressed header of one VP9 keyframe.
It reuses the bounded MSB bit reader and retains no input bytes.
Packet and pixel limits apply before header results return.
The pixel limit covers both coded and render dimensions.

The reader validates the complete uncompressed syntax, including loop-filter updates, quantizer deltas, segmentation features, tile bounds, and zero alignment bits.
Fixed syntax loops and a 128-byte header window bound the work.
The declared compressed-header range must fit the input.

The caller supplies one frame, without an IVF header or superframe index.
Inter frames and show-existing-frame commands return `Unsupported`.
The result does not validate entropy-coded headers, tile payloads, complete frame boundaries, or level conformance.
It does not decode pixels, discover a playback clock, or establish decoder support.

`split_packet` returns byte ranges for one VP9 packet, including an optional superframe index.
The packet limit applies before the reader examines the index.
An index contains at most eight sizes and occupies at most 34 bytes.
The reader validates every size before it returns any range, without a payload copy.

A packet without an index returns one range.
A superframe marker requires a complete index with matching markers and positive sizes within the payload.
The result reports unindexed bytes before the index separately, without a padding-validity claim.
It does not identify unindexed concatenated frames, validate coded payloads, or assign separate timestamps.

```fern
<<*>>=
// Inspect bounded VP9 keyframe metadata without pixel reconstruction.
import "std/audio/alac/bits";
import "std/audio/pcm";
import "std/audio/wire";
import "std/video/frame";

pub struct FrameRange { offset: i32, size: i32 }

pub struct Packet { frames: FrameRange[], index_offset: i32, trailing_bytes: i32 }

function indexed_packet(input: [u8], count: i32, width: i32, start: i32): Result[Packet, frame.VideoError] {
  let frames: FrameRange[] = [];
  let offset = 0;
  for i in 0..count {
    let size = wire.le(input, start + 1 + i * width, width);
    if (size == 0u64 || size > (start - offset) as u64) {
      return Err(frame.Header);
    }
    frames = frames.append(FrameRange { offset: offset, size: size as i32 });
    offset += size as i32;
  }
  return Ok(Packet { frames: frames, index_offset: start, trailing_bytes: start - offset });
}

pub function split_packet(input: [u8], limits: frame.Limits): Result[Packet, frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() == 0) {
    return Err(frame.Truncated);
  }
  let marker = input[input.len() - 1];
  if ((marker & 224u8) != 192u8) {
    return Ok(Packet { frames: [FrameRange { offset: 0, size: input.len() }], index_offset: input.len(), trailing_bytes: 0 });
  }
  let count = (marker & 7u8) as i32 + 1;
  let width = (marker >> 3 & 3u8) as i32 + 1;
  let bytes = 2 + count * width;
  if (input.len() < bytes) {
    return Err(frame.Truncated);
  }
  let start = input.len() - bytes;
  if (input[start] != marker) {
    return Err(frame.Header);
  }
  return indexed_packet(input, count, width, start);
}

pub struct Keyframe { profile: i32, coded: frame.Format, render: frame.Format, bit_depth: i32, color_space: i32, full_range: boolean, subsampling_x: i32, subsampling_y: i32, show_frame: boolean, error_resilient: boolean, filter_level: i32, sharpness: i32, base_quantizer: i32, segmentation: boolean, tile_columns_log2: i32, tile_rows_log2: i32, uncompressed_bytes: i32, compressed_bytes: i32, tile_offset: i32 }

struct Prefix { at: i64, profile: i32, show: boolean, resilient: boolean }

struct Color { at: i64, depth: i32, space: i32, full: boolean, x: i32, y: i32 }

struct Extent { at: i64, coded: frame.Format, render: frame.Format }

struct Filter { at: i64, level: i32, sharpness: i32 }

struct Quantizer { at: i64, base: i32 }

struct Tiles { at: i64, columns: i32, rows: i32 }

struct Syntax { filter: Filter, quantizer: Quantizer, segmented: boolean, tiles: Tiles }

function read(input: [u8], at: i64, width: i32): Result[(i64, u32), frame.VideoError] {
  return match (bits.read(input, at, width)) { Ok(value) => Ok(value), Err(pcm.Truncated) => Err(frame.Truncated), _ => Err(frame.Header) };
}

function fixed(input: [u8], at: i64, width: i32, expected: u32): Result[i64, frame.VideoError] {
  let (next, value) = read(input, at, width)?;
  if (value != expected) {
    return Err(frame.Header);
  }
  return Ok(next);
}

function profile(input: [u8]): Result[(i64, i32), frame.VideoError] {
  let at = fixed(input, 0i64, 2, 2u32)?;
  let (next1, low) = read(input, at, 1)?;
  let (next2, high) = read(input, next1, 1)?;
  at = next2;
  let profile = (low + 2u32 * high) as i32;
  if (profile == 3) {
    at = fixed(input, at, 1, 0u32)?;
  }
  return Ok((at, profile));
}

function prefix(input: [u8]): Result[Prefix, frame.VideoError] {
  let (at, profile) = profile(input)?;
  let (next3, existing) = read(input, at, 1)?;
  if (existing != 0u32) {
    return Err(frame.Unsupported);
  }
  let (next4, kind) = read(input, next3, 1)?;
  if (kind != 0u32) {
    return Err(frame.Unsupported);
  }
  let (next5, show) = read(input, next4, 1)?;
  let (next6, resilient) = read(input, next5, 1)?;
  at = fixed(input, next6, 24, 4817730u32)?;
  return Ok(Prefix { at: at, profile: profile, show: show != 0u32, resilient: resilient != 0u32 });
}

function chroma_samples(input: [u8], at: i64, space: i32, depth: i32, full: boolean): Result[Color, frame.VideoError] {
  let (next1, x) = read(input, at, 1)?;
  let (next2, y) = read(input, next1, 1)?;
  if (x == 1u32 && y == 1u32) {
    return Err(frame.Header);
  }
  return Ok(Color { at: fixed(input, next2, 1, 0u32)?, depth: depth, space: space, full: full, x: x as i32, y: y as i32 });
}

function rgb(input: [u8], at: i64, profile: i32, depth: i32): Result[Color, frame.VideoError] {
  if (profile == 0 || profile == 2) {
    return Err(frame.Header);
  }
  return Ok(Color { at: fixed(input, at, 1, 0u32)?, depth: depth, space: 7, full: true, x: 0, y: 0 });
}

function subsampling(input: [u8], at: i64, profile: i32, space: i32, depth: i32): Result[Color, frame.VideoError] {
  if (space == 7) {
    return rgb(input, at, profile, depth);
  }
  let (next, full) = read(input, at, 1)?;
  if (profile == 0 || profile == 2) {
    return Ok(Color { at: next, depth: depth, space: space, full: full != 0u32, x: 1, y: 1 });
  }
  return chroma_samples(input, next, space, depth, full != 0u32);
}

function color(input: [u8], p: Prefix): Result[Color, frame.VideoError] {
  let at = p.at;
  let depth = 8;
  if (p.profile >= 2) {
    let (next, twelve) = read(input, at, 1)?;
    at = next;
    depth = if (twelve != 0u32) { 12 } else { 10 };
  }
  let (next, space) = read(input, at, 3)?;
  return subsampling(input, next, p.profile, space as i32, depth);
}

function dimensions(input: [u8], at: i64, limits: frame.Limits): Result[(i64, frame.Format), frame.VideoError] {
  let (next1, width) = read(input, at, 16)?;
  let (next2, height) = read(input, next1, 16)?;
  let format = frame.Format { width: width as i32 + 1, height: height as i32 + 1 };
  let _ = frame.pixel_count(format, limits)?;
  return Ok((next2, format));
}

function extent(input: [u8], at: i64, limits: frame.Limits): Result[Extent, frame.VideoError] {
  let (next1, coded) = dimensions(input, at, limits)?;
  let (next2, different) = read(input, next1, 1)?;
  let (next3, render) = if (different == 0u32) { (next2, coded) } else { dimensions(input, next2, limits)? };
  return Ok(Extent { at: next3, coded: coded, render: render });
}

function optional_value(input: [u8], at: i64, width: i32): Result[i64, frame.VideoError] {
  let (next, present) = read(input, at, 1)?;
  if (present == 0u32) {
    return Ok(next);
  }
  let (end, _) = read(input, next, width)?;
  return Ok(end);
}

function filter_deltas(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (next1, enabled) = read(input, at, 1)?;
  if (enabled == 0u32) {
    return Ok(next1);
  }
  let (next2, update) = read(input, next1, 1)?;
  at = next2;
  if (update != 0u32) {
    for _ in 0..6 {
      at = optional_value(input, at, 7)?;
    }
  }
  return Ok(at);
}

function filter(input: [u8], at: i64, resilient: boolean): Result[Filter, frame.VideoError] {
  let (next1, _) = read(input, at, if (resilient) { 2 } else { 4 })?;
  let (next2, level) = read(input, next1, 6)?;
  let (next3, sharpness) = read(input, next2, 3)?;
  return Ok(Filter { at: filter_deltas(input, next3)?, level: level as i32, sharpness: sharpness as i32 });
}

function quantizer(input: [u8], at: i64): Result[Quantizer, frame.VideoError] {
  let (next, base) = read(input, at, 8)?;
  at = next;
  for _ in 0..3 {
    at = optional_value(input, at, 5)?;
  }
  return Ok(Quantizer { at: at, base: base as i32 });
}

function probabilities(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (next1, update) = read(input, at, 1)?;
  at = next1;
  if (update != 0u32) {
    for _ in 0..7 {
      at = optional_value(input, at, 8)?;
    }
    let (next2, temporal) = read(input, at, 1)?;
    at = next2;
    if (temporal != 0u32) {
      for _ in 0..3 {
        at = optional_value(input, at, 8)?;
      }
    }
  }
  return Ok(at);
}

function features(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (next1, update) = read(input, at, 1)?;
  at = next1;
  if (update != 0u32) {
    let (next2, _) = read(input, at, 1)?;
    at = next2;
    for _ in 0..8 {
      for width in [9, 7, 2, 0] {
        at = optional_value(input, at, width)?;
      }
    }
  }
  return Ok(at);
}

function segmentation(input: [u8], at: i64): Result[(i64, boolean), frame.VideoError] {
  let (next, enabled) = read(input, at, 1)?;
  if (enabled == 0u32) {
    return Ok((next, false));
  }
  return Ok((features(input, probabilities(input, next)?)?, true));
}

function increment(input: [u8], at: i64, minimum: i32, maximum: i32): Result[(i64, i32), frame.VideoError] {
  let value = minimum;
  while (value < maximum) {
    let (next, more) = read(input, at, 1)?;
    at = next;
    if (more == 0u32) {
      break;
    }
    value += 1;
  }
  return Ok((at, value));
}

function tiles(input: [u8], at: i64, width: i32): Result[Tiles, frame.VideoError] {
  let blocks = (width + 63) / 64;
  let minimum = 0;
  while (64 << minimum < blocks) {
    minimum += 1;
  }
  let maximum = 0;
  while (blocks >> maximum + 1 >= 4) {
    maximum += 1;
  }
  let (next1, columns) = increment(input, at, minimum, maximum)?;
  let (next2, rows) = increment(input, next1, 0, 2)?;
  return Ok(Tiles { at: next2, columns: columns, rows: rows });
}

function aligned(input: [u8], at: i64): Result[i32, frame.VideoError] {
  while (at % 8i64 != 0i64) {
    at = fixed(input, at, 1, 0u32)?;
  }
  return Ok((at / 8i64) as i32);
}

function syntax(input: [u8], extent: Extent, resilient: boolean): Result[Syntax, frame.VideoError] {
  let f = filter(input, extent.at, resilient)?;
  let q = quantizer(input, f.at)?;
  let (at, segmented) = segmentation(input, q.at)?;
  let t = tiles(input, at, extent.coded.width)?;
  return Ok(Syntax { filter: f, quantizer: q, segmented: segmented, tiles: t });
}

pub function inspect_keyframe(input: [u8], limits: frame.Limits): Result[Keyframe, frame.VideoError] {
  frame.check_packet(input, limits)?;
  let window = input[:if (input.len() < 128) { input.len() } else { 128 }];
  let p = prefix(window)?;
  let c = color(window, p)?;
  let e = extent(window, c.at, limits)?;
  let s = syntax(window, e, p.resilient)?;
  let (end, size) = read(window, s.tiles.at, 16)?;
  let bytes = aligned(window, end)?;
  if (size > (input.len() - bytes) as u32) {
    return Err(frame.Truncated);
  }
  return Ok(Keyframe { profile: p.profile, coded: e.coded, render: e.render, bit_depth: c.depth, color_space: c.space, full_range: c.full, subsampling_x: c.x, subsampling_y: c.y, show_frame: p.show, error_resilient: p.resilient, filter_level: s.filter.level, sharpness: s.filter.sharpness, base_quantizer: s.quantizer.base, segmentation: s.segmented, tile_columns_log2: s.tiles.columns, tile_rows_log2: s.tiles.rows, uncompressed_bytes: bytes, compressed_bytes: size as i32, tile_offset: bytes + size as i32 });
}
```
