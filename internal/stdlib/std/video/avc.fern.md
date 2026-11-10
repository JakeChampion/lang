# Native AVC sequence headers

`inspect_sps` reads one H.264 sequence parameter set through its trailing bits.
The input includes its NAL header, without a start code or length prefix.
The reader validates emulation-prevention bytes and reuses the bounded MSB bit reader.
It supports the ordinary SPS syntax for Baseline, Main, Extended, and the extended profile family.
Subset SPS units and other NAL types return `Unsupported`.

The result reports coded and visible dimensions, pixel crop margins, chroma format, bit depth, and VUI metadata.
The pixel limit applies to the complete coded picture before crop.
The packet limit applies before the reader copies the unescaped bytes.
An additional 8 KiB SPS input ceiling bounds header storage when the caller permits larger packets.
Fixed syntax limits bound scaling lists, picture-order cycles, and HRD entries.
These limits do not define an exact process-memory bound.

This reader does not decode pixels or establish complete stream conformance.
It does not validate pictures, parameter-set references, level constraints, or decoder support.
The existing NAL and MP4 APIs retain their framing contracts.

An absent aspect ratio has numerator and denominator zero.
An absent timing record has zero tick counts. Color identifiers default to the H.264 unspecified value, 2.
The presence flags identify explicit video-signal and color-description syntax.
Crop fields are pixel margins removed from the corresponding coded-picture edges.

VUI tick counts retain their coded units. They do not establish frame durations or an A/V playback clock.

Malformed supported syntax returns `Header`. Incomplete fields return `Truncated`.
Input budgets, coded-picture budgets, and overlong integer codes return `Limit`.

```fern
<<*>>=
// Read bounded H.264 sequence parameter sets without a platform codec.
import "std/video/frame";
import "std/audio/alac/bits" as bit_input;
import "std/audio/pcm";

pub const MAX_SPS_BYTES: i32 = 8192;

pub struct Crop { left: i32, top: i32, right: i32, bottom: i32 }

pub struct AspectRatio { numerator: u32, denominator: u32 }

pub struct Vui { aspect_ratio: AspectRatio, video_signal_present: boolean, colour_description_present: boolean, video_format: i32, full_range: boolean, colour_primaries: i32, transfer_characteristics: i32, matrix_coefficients: i32, num_units_in_tick: u32, time_scale: u32, fixed_frame_rate: boolean }

pub struct Sequence { profile_idc: i32, level_idc: i32, sequence_id: i32, chroma_format_idc: i32, separate_colour_plane: boolean, bit_depth_luma: i32, bit_depth_chroma: i32, coded: frame.Format, visible: frame.Format, crop: Crop, frame_mbs_only: boolean, mb_adaptive_frame_field: boolean, max_reference_frames: i32, vui: Vui }

struct Prefix { at: i64, profile: i32, level: i32, id: i32 }

struct Chroma { at: i64, idc: i32, separate: boolean, luma: i32, chroma: i32 }

struct Picture { at: i64, coded: frame.Format, crop: Crop, frame_only: boolean, adaptive: boolean, references: i32 }

struct Signal { present: boolean, description: boolean, format: i32, full: boolean, primaries: i32, transfer: i32, matrix: i32 }

struct Timing { tick: u32, scale: u32, fixed: boolean }

function read(input: [u8], at: i64, count: i32): Result[(i64, u32), frame.VideoError] {
  return match (bit_input.read(input, at, count)) {
    Ok(value) => Ok(value),
    Err(pcm.Truncated) => Err(frame.Truncated),
    _ => Err(frame.Header),
  };
}

function ue(input: [u8], at: i64): Result[(i64, u32), frame.VideoError] {
  let zeros = 0;
  loop {
    let (next, value) = read(input, at, 1)?;
    at = next;
    if (value != 0u32) {
      break;
    }
    zeros += 1;
    if (zeros > 31) {
      return Err(frame.Limit);
    }
  }
  let (next, suffix) = read(input, at, zeros)?;
  return Ok((next, (1u32 << zeros as u32) - 1u32 + suffix));
}

function bounded_ue(input: [u8], at: i64, maximum: u32): Result[(i64, u32), frame.VideoError] {
  let (next, value) = ue(input, at)?;
  if (value > maximum) {
    return Err(frame.Header);
  }
  return Ok((next, value));
}

function se(input: [u8], at: i64): Result[(i64, i32), frame.VideoError] {
  let (next, value) = ue(input, at)?;
  let magnitude = (value / 2u32 + value % 2u32) as i32;
  return Ok((next, if ((value & 1u32) == 0u32) { -magnitude } else { magnitude }));
}

function unescape(input: [u8], limits: frame.Limits): Result[u8[], frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() > MAX_SPS_BYTES) {
    return Err(frame.Limit);
  }
  if (input.len() == 0) {
    return Err(frame.Truncated);
  }
  if ((input[0] & 128u8) != 0u8) {
    return Err(frame.Header);
  }
  if ((input[0] & 31u8) != 7u8) {
    return Err(frame.Unsupported);
  }
  if ((input[0] & 96u8) == 0u8) {
    return Err(frame.Header);
  }
  return rbsp_bytes(input[1:]);
}

function prevention(input: [u8], at: i32): Result[void, frame.VideoError] {
  if (at + 1 == input.len()) {
    return Err(frame.Truncated);
  }
  if (input[at + 1] > 3u8) {
    return Err(frame.Header);
  }
  return Ok(());
}

function rbsp_bytes(input: [u8]): Result[u8[], frame.VideoError] {
  let output: u8[] = [];
  let zeros = 0;
  for at in 0..input.len() {
    let byte = input[at];
    if (zeros == 2 && byte <= 3u8) {
      if (byte != 3u8) {
        return Err(frame.Header);
      }
      prevention(input, at)?;
      zeros = 0;
    } else {
      output = output.append(byte);
      zeros = if (byte == 0u8) { zeros + 1 } else { 0 };
    }
  }
  return Ok(output);
}

function extended(profile: i32): boolean {
  for value in [100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135] {
    if (profile == value) {
      return true;
    }
  }
  return false;
}

function prefix(input: [u8]): Result[Prefix, frame.VideoError] {
  let (at, profile) = read(input, 0i64, 8)?;
  let (position1, constraints) = read(input, at, 8)?;
  at = position1;
  let (position2, level) = read(input, at, 8)?;
  at = position2;
  let (position3, id) = bounded_ue(input, at, 31u32)?;
  at = position3;
  if ((constraints & 3u32) != 0u32) {
    return Err(frame.Header);
  }
  if (profile != 66u32 && profile != 77u32 && profile != 88u32 && !extended(profile as i32)) {
    return Err(frame.Unsupported);
  }
  return Ok(Prefix { at: at, profile: profile as i32, level: level as i32, id: id as i32 });
}

function scaling_list(input: [u8], at: i64, count: i32): Result[i64, frame.VideoError] {
  let last = 8;
  let next = 8;
  for _ in 0..count {
    if (next != 0) {
      let (end, delta) = se(input, at)?;
      at = end;
      if (delta < -128 || delta > 127) {
        return Err(frame.Header);
      }
      next = (last + delta + 256) % 256;
    }
    last = if (next == 0) { last } else { next };
  }
  return Ok(at);
}

function scaling_matrix(input: [u8], at: i64, chroma: i32): Result[i64, frame.VideoError] {
  for index in 0..if (chroma == 3) { 12 } else { 8 } {
    let (end, present) = read(input, at, 1)?;
    at = end;
    if (present != 0u32) {
      at = scaling_list(input, at, if (index < 6) { 16 } else { 64 })?;
    }
  }
  return Ok(at);
}

function chroma_header(input: [u8], p: Prefix): Result[Chroma, frame.VideoError] {
  if (!extended(p.profile)) {
    return Ok(Chroma { at: p.at, idc: 1, separate: false, luma: 8, chroma: 8 });
  }
  let (at, idc) = bounded_ue(input, p.at, 3u32)?;
  let separate = false;
  if (idc == 3u32) {
    let (end, flag) = read(input, at, 1)?;
    at = end;
    separate = flag != 0u32;
  }
  return chroma_depth(input, at, idc as i32, separate);
}

function chroma_depth(input: [u8], at: i64, idc: i32, separate: boolean): Result[Chroma, frame.VideoError] {
  let (position1, luma) = bounded_ue(input, at, 6u32)?;
  at = position1;
  let (position2, chroma) = bounded_ue(input, at, 6u32)?;
  at = position2;
  let (position3, _) = read(input, at, 1)?;
  at = position3;
  let (position4, scaling) = read(input, at, 1)?;
  at = position4;
  if (scaling != 0u32) {
    at = scaling_matrix(input, at, idc)?;
  }
  return Ok(Chroma { at: at, idc: idc, separate: separate, luma: luma as i32 + 8, chroma: chroma as i32 + 8 });
}

function poc_cycle(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, _) = read(input, at, 1)?;
  at = position1;
  let (position2, _) = se(input, at)?;
  at = position2;
  let (position3, _) = se(input, at)?;
  at = position3;
  let (position4, count) = bounded_ue(input, at, 255u32)?;
  at = position4;
  for _ in 0..count as i32 {
    let (end, _) = se(input, at)?;
    at = end;
  }
  return Ok(at);
}

function picture_order(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, _) = bounded_ue(input, at, 12u32)?;
  at = position1;
  let (position2, kind) = bounded_ue(input, at, 2u32)?;
  at = position2;
  if (kind == 0u32) {
    let (end, _) = bounded_ue(input, at, 12u32)?;
    return Ok(end);
  }
  if (kind == 1u32) {
    return poc_cycle(input, at);
  }
  return Ok(at);
}

function frame_mode(input: [u8], at: i64): Result[(i64, boolean, boolean), frame.VideoError] {
  let (position1, only) = read(input, at, 1)?;
  at = position1;
  let adaptive = false;
  if (only == 0u32) {
    let (end, flag) = read(input, at, 1)?;
    at = end;
    adaptive = flag != 0u32;
  }
  let (position2, _) = read(input, at, 1)?;
  at = position2;
  return Ok((at, only != 0u32, adaptive));
}

function extent(width: u32, height: u32, only: boolean, limits: frame.Limits): Result[frame.Format, frame.VideoError] {
  let w = (width as i64 + 1i64) * 16i64;
  let h = (height as i64 + 1i64) * 16i64 * (if (only) { 1i64 } else { 2i64 });
  if (w > 2147483647i64 || h > 2147483647i64) {
    return Err(frame.Limit);
  }
  let format = frame.Format { width: w as i32, height: h as i32 };
  let _ = frame.pixel_count(format, limits)?;
  return Ok(format);
}

function crop_codes(input: [u8], at: i64): Result[(i64, u32, u32, u32, u32), frame.VideoError] {
  let (position1, left) = ue(input, at)?;
  at = position1;
  let (position2, right) = ue(input, at)?;
  at = position2;
  let (position3, top) = ue(input, at)?;
  at = position3;
  let (position4, bottom) = ue(input, at)?;
  at = position4;
  return Ok((at, left, right, top, bottom));
}

function crop_margins(codes: (i64, u32, u32, u32, u32), chroma: Chroma, only: boolean, format: frame.Format): Result[Crop, frame.VideoError] {
  let (_, left, right, top, bottom) = codes;
  let array_type = if (chroma.separate) { 0 } else { chroma.idc };
  let x = if (array_type == 1 || array_type == 2) { 2i64 } else { 1i64 };
  let y = (if (array_type == 1) { 2i64 } else { 1i64 }) * (if (only) { 1i64 } else { 2i64 });
  let horizontal = (left as i64 + right as i64) * x;
  let vertical = (top as i64 + bottom as i64) * y;
  if (horizontal >= format.width as i64 || vertical >= format.height as i64) {
    return Err(frame.Header);
  }
  return Ok(Crop { left: (left as i64 * x) as i32, right: (right as i64 * x) as i32, top: (top as i64 * y) as i32, bottom: (bottom as i64 * y) as i32 });
}

function crop_header(input: [u8], at: i64, chroma: Chroma, only: boolean, format: frame.Format): Result[(i64, Crop), frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present == 0u32) {
    return Ok((at, Crop { left: 0, right: 0, top: 0, bottom: 0 }));
  }
  let codes = crop_codes(input, at)?;
  let (end, _, _, _, _) = codes;
  return Ok((end, crop_margins(codes, chroma, only, format)?));
}

function picture(input: [u8], chroma: Chroma, limits: frame.Limits): Result[Picture, frame.VideoError] {
  let at = picture_order(input, chroma.at)?;
  let (position1, references) = bounded_ue(input, at, 16u32)?;
  at = position1;
  let (position2, _) = read(input, at, 1)?;
  at = position2;
  let (position3, width) = ue(input, at)?;
  at = position3;
  let (position4, height) = ue(input, at)?;
  at = position4;
  let (position5, only, adaptive) = frame_mode(input, at)?;
  at = position5;
  let coded = extent(width, height, only, limits)?;
  let (position6, crop) = crop_header(input, at, chroma, only, coded)?;
  at = position6;
  return Ok(Picture { at: at, coded: coded, crop: crop, frame_only: only, adaptive: adaptive, references: references as i32 });
}

function unspecified_signal(): Signal {
  return Signal { present: false, description: false, format: 5, full: false, primaries: 2, transfer: 2, matrix: 2 };
}

function empty_vui(): Vui {
  return Vui { aspect_ratio: AspectRatio { numerator: 0u32, denominator: 0u32 }, video_signal_present: false, colour_description_present: false, video_format: 5, full_range: false, colour_primaries: 2, transfer_characteristics: 2, matrix_coefficients: 2, num_units_in_tick: 0u32, time_scale: 0u32, fixed_frame_rate: false };
}

function extended_aspect(input: [u8], at: i64): Result[(i64, AspectRatio), frame.VideoError] {
  let (position1, width) = read(input, at, 16)?;
  at = position1;
  let (position2, height) = read(input, at, 16)?;
  at = position2;
  if (width == 0u32 || height == 0u32) {
    return Err(frame.Header);
  }
  return Ok((at, AspectRatio { numerator: width, denominator: height }));
}

function aspect(input: [u8], at: i64): Result[(i64, AspectRatio), frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present == 0u32) {
    return Ok((at, AspectRatio { numerator: 0u32, denominator: 0u32 }));
  }
  let (position2, id) = read(input, at, 8)?;
  at = position2;
  if (id == 255u32) {
    return extended_aspect(input, at);
  }
  return Ok((at, table_aspect(id)?));
}

function table_aspect(id: u32): Result[AspectRatio, frame.VideoError] {
  if (id > 16u32) {
    return Err(frame.Header);
  }
  let widths: u32[] = [0, 1, 12, 10, 16, 40, 24, 20, 32, 80, 18, 15, 64, 160, 4, 3, 2];
  let heights: u32[] = [0, 1, 11, 11, 11, 33, 11, 11, 11, 33, 11, 11, 33, 99, 3, 2, 1];
  return Ok(AspectRatio { numerator: widths[id as i32], denominator: heights[id as i32] });
}

function overscan(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present != 0u32) {
    let (end, _) = read(input, at, 1)?;
    return Ok(end);
  }
  return Ok(at);
}

function signal_fields(input: [u8], at: i64): Result[(i64, Signal), frame.VideoError] {
  let (position1, fields) = read(input, at, 5)?;
  at = position1;
  let format = (fields >> 2u32) as i32;
  if (format > 5) {
    return Err(frame.Header);
  }
  let signal = Signal { ...unspecified_signal(), present: true, format: format, full: (fields & 2u32) != 0u32 };
  if ((fields & 1u32) == 0u32) {
    return Ok((at, signal));
  }
  let (position2, colour) = read(input, at, 24)?;
  at = position2;
  return Ok((at, Signal { ...signal, description: true, primaries: (colour >> 16u32) as i32, transfer: (colour >> 8u32 & 255u32) as i32, matrix: (colour & 255u32) as i32 }));
}

function video_signal(input: [u8], at: i64): Result[(i64, Signal), frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present == 0u32) {
    return Ok((at, unspecified_signal()));
  }
  return signal_fields(input, at);
}

function chroma_location(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present != 0u32) {
    let (end, _) = bounded_ue(input, at, 5u32)?;
    let (last, _) = bounded_ue(input, end, 5u32)?;
    return Ok(last);
  }
  return Ok(at);
}

function timing(input: [u8], at: i64): Result[(i64, Timing), frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present == 0u32) {
    return Ok((at, Timing { tick: 0u32, scale: 0u32, fixed: false }));
  }
  let (position2, tick) = read(input, at, 32)?;
  at = position2;
  let (position3, scale) = read(input, at, 32)?;
  at = position3;
  let (position4, fixed) = read(input, at, 1)?;
  at = position4;
  if (tick == 0u32 || scale == 0u32) {
    return Err(frame.Header);
  }
  return Ok((at, Timing { tick: tick, scale: scale, fixed: fixed != 0u32 }));
}

function cpb(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, _) = ue(input, at)?;
  at = position1;
  let (position2, _) = ue(input, at)?;
  at = position2;
  let (position3, _) = read(input, at, 1)?;
  at = position3;
  return Ok(at);
}

function hrd(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, count) = bounded_ue(input, at, 31u32)?;
  at = position1;
  let (position2, _) = read(input, at, 8)?;
  at = position2;
  for _ in 0..count as i32 + 1 {
    at = cpb(input, at)?;
  }
  let (position3, _) = read(input, at, 20)?;
  at = position3;
  return Ok(at);
}

function optional_hrd(input: [u8], at: i64): Result[(i64, boolean), frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present != 0u32) {
    return Ok((hrd(input, at)?, true));
  }
  return Ok((at, false));
}

function hrd_pair(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (position1, nal) = optional_hrd(input, at)?;
  at = position1;
  let (position2, vcl) = optional_hrd(input, at)?;
  at = position2;
  if (nal || vcl) {
    let (end, _) = read(input, at, 1)?;
    at = end;
  }
  let (position3, _) = read(input, at, 1)?;
  at = position3;
  return Ok(at);
}

function motion_limits(input: [u8], at: i64): Result[i64, frame.VideoError] {
  for _ in 0..4 {
    let (end, _) = bounded_ue(input, at, 16u32)?;
    at = end;
  }
  return Ok(at);
}

function restriction(input: [u8], at: i64, references: i32): Result[i64, frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present == 0u32) {
    return Ok(at);
  }
  let (position2, _) = read(input, at, 1)?;
  at = position2;
  at = motion_limits(input, at)?;
  let (position3, reorder) = bounded_ue(input, at, 16u32)?;
  at = position3;
  let (position4, buffering) = bounded_ue(input, at, 16u32)?;
  at = position4;
  if (reorder > buffering || buffering < references as u32) {
    return Err(frame.Header);
  }
  return Ok(at);
}

function vui(input: [u8], at: i64, references: i32): Result[(i64, Vui), frame.VideoError] {
  let (position1, present) = read(input, at, 1)?;
  at = position1;
  if (present == 0u32) {
    return Ok((at, empty_vui()));
  }
  let (position2, ratio) = aspect(input, at)?;
  at = position2;
  at = overscan(input, at)?;
  let (position3, signal) = video_signal(input, at)?;
  at = position3;
  at = chroma_location(input, at)?;
  let (position4, time) = timing(input, at)?;
  at = position4;
  at = hrd_pair(input, at)?;
  at = restriction(input, at, references)?;
  return Ok((at, Vui { aspect_ratio: ratio, video_signal_present: signal.present, colour_description_present: signal.description, video_format: signal.format, full_range: signal.full, colour_primaries: signal.primaries, transfer_characteristics: signal.transfer, matrix_coefficients: signal.matrix, num_units_in_tick: time.tick, time_scale: time.scale, fixed_frame_rate: time.fixed }));
}

function trailing(input: [u8], at: i64): Result[void, frame.VideoError] {
  let (position1, stop) = read(input, at, 1)?;
  at = position1;
  if (stop != 1u32) {
    return Err(frame.Header);
  }
  while (at % 8i64 != 0i64) {
    let (end, bit) = read(input, at, 1)?;
    at = end;
    if (bit != 0u32) {
      return Err(frame.Header);
    }
  }
  if (at != input.len() as i64 * 8i64) {
    return Err(frame.Header);
  }
  return Ok(());
}

pub function inspect_sps(input: [u8], limits: frame.Limits): Result[Sequence, frame.VideoError] {
  let bytes = unescape(input, limits)?;
  let p = prefix(bytes)?;
  let chroma = chroma_header(bytes, p)?;
  let pic = picture(bytes, chroma, limits)?;
  let (at, metadata) = vui(bytes, pic.at, pic.references)?;
  trailing(bytes, at)?;
  return Ok(Sequence { profile_idc: p.profile, level_idc: p.level, sequence_id: p.id, chroma_format_idc: chroma.idc, separate_colour_plane: chroma.separate, bit_depth_luma: chroma.luma, bit_depth_chroma: chroma.chroma, coded: pic.coded, visible: frame.Format { width: pic.coded.width - pic.crop.left - pic.crop.right, height: pic.coded.height - pic.crop.top - pic.crop.bottom }, crop: pic.crop, frame_mbs_only: pic.frame_only, mb_adaptive_frame_field: pic.adaptive, max_reference_frames: pic.references, vui: metadata });
}
```
