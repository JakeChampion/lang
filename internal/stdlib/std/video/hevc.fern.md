# Native HEVC sequence headers

`inspect_sps` reads one base-layer HEVC sequence parameter set, including its two-byte NAL header.
The input excludes a start code, a length prefix, and surrounding zero bytes.
The reader reuses the native NAL unescape and bounded bit readers.
It supports ordinary SPS syntax and the nine range-extension flags.
Multilayer, 3D, screen-content, nonzero profile spaces, and unknown extensions return `Unsupported`.

The result reports profile identifiers, coded and visible dimensions, crop margins, precision, ordering bounds, and VUI metadata.
The packet limit and a separate 8 KiB SPS ceiling apply before the unescaped copy.
The pixel budget applies to the complete coded picture before crop.
Fixed bounds constrain scaling lists, reference-set counts, sublayers, and HRD entries.
These limits do not define an exact process-memory bound.

The reader checks supported syntax through its trailing bits. It retains no source bytes or coefficient lists.
It does not decode pictures, resolve VPS references, or validate profile, level, reference-picture, or operating-point conformance.
Profile constraint bits remain outside this metadata check.
The native pixel-decode table remains unchanged.

Crop fields use pixel margins. Visible dimensions apply the conformance window only.
A zero aspect numerator or denominator denotes an unspecified ratio. Unspecified color identifiers default to 2.

Absent timing fields are zero. Present clock fields must be nonzero.
VUI ticks retain their coded units, without a frame-duration or A/V clock claim.

Malformed supported syntax returns `Header`. Incomplete fields return `Truncated`.
Packet, coded-picture, and integer-code budgets return `Limit`.

```fern
<<*>>=
// Inspect bounded HEVC SPS metadata without a platform codec.
import "std/video/frame";
import "std/video/avc" as bits;
import "core/cmp";

pub const MAX_SPS_BYTES: i32 = 8192;

pub struct Crop { left: i32, top: i32, right: i32, bottom: i32 }

pub struct AspectRatio { numerator: u32, denominator: u32 }

pub struct Vui { aspect_ratio: AspectRatio, video_signal_present: boolean, colour_description_present: boolean, video_format: i32, full_range: boolean, colour_primaries: i32, transfer_characteristics: i32, matrix_coefficients: i32, field_sequence: boolean, num_units_in_tick: u32, time_scale: u32 }

pub struct Sequence { profile_space: i32, tier: boolean, profile_idc: i32, level_idc: i32, compatibility_flags: u32, vps_id: i32, sequence_id: i32, max_sub_layers: i32, temporal_nesting: boolean, chroma_format_idc: i32, separate_colour_plane: boolean, bit_depth_luma: i32, bit_depth_chroma: i32, coded: frame.Format, visible: frame.Format, crop: Crop, log2_max_poc_lsb: i32, max_dec_pic_buffering: i32, max_num_reorder_pics: i32, vui: Vui }

struct Prefix { at: i64, space: i32, tier: boolean, profile: i32, level: i32, compatibility: u32, vps: i32, sub: i32, nesting: boolean }

struct Picture { at: i64, id: i32, chroma: i32, separate: boolean, luma: i32, chroma_depth: i32, coded: frame.Format, visible: frame.Format, crop: Crop, poc: i32 }

struct Ordering { at: i64, buffering: i32, reorder: i32 }

struct Blocks { at: i64, minimum: i32, ctb: i32, transform: i32 }

pub(package) struct SequenceContext { sequence: Sequence, minimum: i32, ctb: i32, transform: i32 }

struct Hrd { at: i64, nal: boolean, vcl: boolean, sub_picture: boolean }

function skip(input: [u8], at: i64, count: i32): Result[i64, frame.VideoError] {
  while (count > 0) {
    let take = if (count > 32) { 32 } else { count };
    let (next, _) = bits.read(input, at, take)?;
    at = next;
    count -= take;
  }
  return Ok(at);
}

function parameter_bytes(input: [u8], limits: frame.Limits): Result[u8[], frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() > MAX_SPS_BYTES) {
    return Err(frame.Limit);
  }
  if (input.len() < 2) {
    return Err(frame.Truncated);
  }
  if ((input[0] & 128u8) != 0u8 || (input[1] & 7u8) == 0u8) {
    return Err(frame.Header);
  }
  if ((input[0] >> 1u8 & 63u8) != 33u8 || (input[0] & 1u8) != 0u8 || input[1] >> 3u8 != 0u8) {
    return Err(frame.Unsupported);
  }
  if ((input[1] & 7u8) != 1u8) {
    return Err(frame.Header);
  }
  return bits.rbsp_bytes(input[2:]);
}

function sublayer_profiles(input: [u8], at: i64, sub: i32): Result[i64, frame.VideoError] {
  let flags = 0u32;
  for i in 0..sub {
    let (next, value) = bits.read(input, at, 2)?;
    at = next;
    flags |= value << (2 * i) as u32;
  }
  if (sub > 0) {
    let (next, reserved) = bits.read(input, at, 2 * (8 - sub))?;
    at = next;
    if (reserved != 0u32) {
      return Err(frame.Header);
    }
  }
  return sublayer_entries(input, at, flags, sub);
}

function sublayer_entries(input: [u8], at: i64, flags: u32, sub: i32): Result[i64, frame.VideoError] {
  for i in 0..sub {
    if ((flags & 2u32 << (2 * i) as u32) != 0u32) {
      let (next, space) = bits.read(input, at, 2)?;
      if (space != 0u32) {
        return Err(frame.Unsupported);
      }
      at = skip(input, next, 86)?;
    }
    if ((flags & 1u32 << (2 * i) as u32) != 0u32) {
      at = skip(input, at, 8)?;
    }
  }
  return Ok(at);
}

function temporal_layers(head: u32): Result[i32, frame.VideoError] {
  let sub = (head >> 1u32 & 7u32) as i32;
  if (sub > 6 || sub == 0 && (head & 1u32) == 0u32) {
    return Err(frame.Header);
  }
  return Ok(sub);
}

function prefix(input: [u8]): Result[Prefix, frame.VideoError] {
  let (at, head) = bits.read(input, 0i64, 8)?;
  let sub = temporal_layers(head)?;
  let (p1, profile) = bits.read(input, at, 8)?;
  if (profile >> 6u32 != 0u32) {
    return Err(frame.Unsupported);
  }
  let (p2, compatibility) = bits.read(input, p1, 32)?;
  let constraints = skip(input, p2, 48)?;
  let (p3, level) = bits.read(input, constraints, 8)?;
  let end = sublayer_profiles(input, p3, sub)?;
  return Ok(Prefix { at: end, space: (profile >> 6u32) as i32, tier: (profile & 32u32) != 0u32, profile: (profile & 31u32) as i32, level: level as i32, compatibility: compatibility, vps: (head >> 4u32) as i32, sub: sub, nesting: (head & 1u32) != 0u32 });
}

function chroma_units(chroma: i32, separate: boolean): (i32, i32) {
  if (separate || chroma == 0) {
    return (1, 1);
  }
  return (if (chroma == 3) { 1 } else { 2 }, if (chroma == 1) { 2 } else { 1 });
}

function crop(input: [u8], at: i64, chroma: i32, separate: boolean, format: frame.Format): Result[(i64, Crop), frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((at, Crop { left: 0, top: 0, right: 0, bottom: 0 }));
  }
  let (a, left) = bits.ue(input, at)?;
  let (b, right) = bits.ue(input, a)?;
  let (c, top) = bits.ue(input, b)?;
  let (d, bottom) = bits.ue(input, c)?;
  let (x, y) = chroma_units(chroma, separate);
  if ((left as u64 + right as u64) * (x as u64) >= format.width as u64 || (top as u64 + bottom as u64) * (y as u64) >= format.height as u64) {
    return Err(frame.Header);
  }
  return Ok((d, Crop { left: (left * (x as u32)) as i32, right: (right * (x as u32)) as i32, top: (top * (y as u32)) as i32, bottom: (bottom * (y as u32)) as i32 }));
}

function dimensions(input: [u8], at: i64, limits: frame.Limits): Result[(i64, frame.Format), frame.VideoError] {
  let (a, width) = bits.ue(input, at)?;
  let (b, height) = bits.ue(input, a)?;
  if (width > 2147483647u32 || height > 2147483647u32) {
    return Err(frame.Limit);
  }
  let format = frame.Format { width: width as i32, height: height as i32 };
  frame.pixel_count(format, limits)?;
  return Ok((b, format));
}

function picture(input: [u8], at: i64, limits: frame.Limits): Result[Picture, frame.VideoError] {
  let (a, id) = bits.bounded_ue(input, at, 15u32)?;
  let (b, chroma) = bits.bounded_ue(input, a, 3u32)?;
  at = b;
  let separate = 0u32;
  if (chroma == 3u32) {
    let (next, value) = bits.read(input, at, 1)?;
    at = next;
    separate = value;
  }
  let (c, coded) = dimensions(input, at, limits)?;
  let (d, crop) = crop(input, c, chroma as i32, separate != 0u32, coded)?;
  let (e, luma) = bits.bounded_ue(input, d, 8u32)?;
  let (f, depth) = bits.bounded_ue(input, e, 8u32)?;
  let (g, poc) = bits.bounded_ue(input, f, 12u32)?;
  return Ok(Picture { at: g, id: id as i32, chroma: chroma as i32, separate: separate != 0u32, luma: luma as i32 + 8, chroma_depth: depth as i32 + 8, coded: coded, visible: frame.Format { width: coded.width - crop.left - crop.right, height: coded.height - crop.top - crop.bottom }, crop: crop, poc: poc as i32 + 4 });
}

function ordering(input: [u8], at: i64, sub: i32): Result[Ordering, frame.VideoError] {
  let (at, all) = bits.read(input, at, 1)?;
  let start = if (all != 0u32) { 0 } else { sub };
  let buffering = 0u32;
  let reorder = 0u32;
  for _ in start..sub + 1 {
    let (a, buffer) = bits.bounded_ue(input, at, 15u32)?;
    let (b, order) = bits.bounded_ue(input, a, buffer)?;
    let (c, _) = bits.ue(input, b)?;
    at = c;
    if (buffer < buffering || order < reorder) {
      return Err(frame.Header);
    }
    buffering = buffer;
    reorder = order;
  }
  return Ok(Ordering { at: at, buffering: buffering as i32 + 1, reorder: reorder as i32 });
}

function block_dimensions(format: frame.Format, minimum: i32, ctb: i32): Result[void, frame.VideoError] {
  if (ctb < 4 || format.width % (1 << minimum) != 0 || format.height % (1 << minimum) != 0) {
    return Err(frame.Header);
  }
  return Ok(());
}

function blocks(input: [u8], at: i64, format: frame.Format): Result[Blocks, frame.VideoError] {
  let (a, min_cb) = bits.bounded_ue(input, at, 3u32)?;
  let (b, diff_cb) = bits.bounded_ue(input, a, 3u32 - min_cb)?;
  let minimum = min_cb as i32 + 3;
  let ctb = minimum + diff_cb as i32;
  block_dimensions(format, minimum, ctb)?;
  let (c, min_tb) = bits.bounded_ue(input, b, min_cb)?;
  let transform = min_tb as i32 + 2;
  let max_tb = if (ctb < 5) { ctb } else { 5 };
  let (d, diff_tb) = bits.bounded_ue(input, c, (max_tb - transform) as u32)?;
  let (e, _) = bits.bounded_ue(input, d, (ctb - transform) as u32)?;
  let (f, _) = bits.bounded_ue(input, e, (ctb - transform) as u32)?;
  return Ok(Blocks { at: f, minimum: minimum, ctb: ctb, transform: transform + diff_tb as i32 });
}

function coefficients(input: [u8], at: i64, size: i32): Result[i64, frame.VideoError] {
  if (size > 1) {
    let (next, dc) = bits.se(input, at)?;
    if (dc < -7 || dc > 247) {
      return Err(frame.Header);
    }
    at = next;
  }
  let count = if (size == 0) { 16 } else { 64 };
  for _ in 0..count {
    let (next, delta) = bits.se(input, at)?;
    if (delta < -128 || delta > 127) {
      return Err(frame.Header);
    }
    at = next;
  }
  return Ok(at);
}

pub(package) function scaling_lists(input: [u8], at: i64): Result[i64, frame.VideoError] {
  for size in 0..4 {
    let matrix = 0;
    while (matrix < 6) {
      let (next, mode) = bits.read(input, at, 1)?;
      at = next;
      if (mode == 0u32) {
        let maximum = if (size == 3) { matrix / 3 } else { matrix };
        let (end, _) = bits.bounded_ue(input, at, maximum as u32)?;
        at = end;
      } else {
        at = coefficients(input, at, size)?;
      }
      matrix += if (size == 3) { 3 } else { 1 };
    }
  }
  return Ok(at);
}

function scaling(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (at, enabled) = bits.read(input, at, 1)?;
  if (enabled != 0u32) {
    let (next, present) = bits.read(input, at, 1)?;
    at = next;
    if (present != 0u32) {
      return scaling_lists(input, at);
    }
  }
  return Ok(at);
}

function pcm(input: [u8], at: i64, picture: Picture, blocks: Blocks): Result[i64, frame.VideoError] {
  let (at, flags) = bits.read(input, at, 3)?;
  if ((flags & 1u32) == 0u32) {
    return Ok(at);
  }
  let (a, depths) = bits.read(input, at, 8)?;
  if ((depths >> 4u32) as i32 >= picture.luma || (depths & 15u32) as i32 >= picture.chroma_depth) {
    return Err(frame.Header);
  }
  return pcm_sizes(input, a, blocks);
}

function pcm_sizes(input: [u8], at: i64, blocks: Blocks): Result[i64, frame.VideoError] {
  let lower = if (blocks.minimum < 5) { blocks.minimum } else { 5 };
  let upper = if (blocks.ctb < 5) { blocks.ctb } else { 5 };
  let (b, minimum) = bits.bounded_ue(input, at, (upper - 3) as u32)?;
  if (minimum as i32 + 3 < lower) {
    return Err(frame.Header);
  }
  let (c, _) = bits.bounded_ue(input, b, (upper - 3 - minimum as i32) as u32)?;
  return skip(input, c, 1);
}

function poc_order(a: i32, b: i32): i32 {
  if (a < 0 && b > 0) {
    return -1;
  }
  if (a > 0 && b < 0) {
    return 1;
  }
  return if (a < 0) { b - a } else { a - b };
}

function predicted_values(input: [u8], at: i64, previous: [i32], delta: i32): Result[(i64, i32[]), frame.VideoError] {
  let values: i32[] = [];
  for i in 0..previous.len() + 1 {
    let (next, used) = bits.read(input, at, 1)?;
    at = next;
    let selected = used;
    if (used == 0u32) {
      let (end, delta) = bits.read(input, at, 1)?;
      at = end;
      selected = delta;
    }
    if (selected != 0u32) {
      let poc = delta + (if (i == previous.len()) { 0 } else { previous[i] });
      if (poc != 0) {
        values = values.append(poc);
      }
    }
  }
  return Ok((at, cmp.sort_by(values, poc_order)));
}

function predicted_set(input: [u8], at: i64, previous: [i32], maximum: i32): Result[(i64, i32[]), frame.VideoError] {
  let (a, sign) = bits.read(input, at, 1)?;
  let (b, magnitude) = bits.bounded_ue(input, a, 32767u32)?;
  let delta = (magnitude as i32 + 1) * (if (sign != 0u32) { -1 } else { 1 });
  let (end, values) = predicted_values(input, b, previous, delta)?;
  if (values.len() > maximum) {
    return Err(frame.Header);
  }
  return Ok((end, values));
}

function delta_values(input: [u8], at: i64, count: i32, direction: i32, own values: i32[]): Result[(i64, i32[]), frame.VideoError] {
  let poc = 0;
  for _ in 0..count {
    let (next, delta) = bits.bounded_ue(input, at, 32767u32)?;
    at = skip(input, next, 1)?;
    poc += (delta as i32 + 1) * direction;
    values = values.append(poc);
  }
  return Ok((at, values));
}

function explicit_set(input: [u8], at: i64, maximum: i32): Result[(i64, i32[]), frame.VideoError] {
  let (a, negative) = bits.bounded_ue(input, at, maximum as u32)?;
  let (b, positive) = bits.bounded_ue(input, a, maximum as u32 - negative)?;
  let (c, values) = delta_values(input, b, negative as i32, -1, [])?;
  return delta_values(input, c, positive as i32, 1, values);
}

function reference_sets(input: [u8], at: i64, maximum: i32): Result[i64, frame.VideoError] {
  let (at, count) = bits.bounded_ue(input, at, 64u32)?;
  let previous: i32[] = [];
  for i in 0..count as i32 {
    let predicted = 0u32;
    if (i > 0) {
      let (next, value) = bits.read(input, at, 1)?;
      at = next;
      predicted = value;
    }
    let (next, total) = if (predicted != 0u32) { predicted_set(input, at, previous, maximum)? } else { explicit_set(input, at, maximum)? };
    at = next;
    previous = total;
  }
  return Ok(at);
}

function long_term(input: [u8], at: i64, poc: i32): Result[i64, frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present != 0u32) {
    let (next, count) = bits.bounded_ue(input, at, 32u32)?;
    at = next;
    for _ in 0..count as i32 {
      at = skip(input, at, poc + 1)?;
    }
  }
  return Ok(at);
}

function empty_vui(): Vui {
  return Vui { aspect_ratio: AspectRatio { numerator: 0u32, denominator: 0u32 }, video_signal_present: false, colour_description_present: false, video_format: 5, full_range: false, colour_primaries: 2, transfer_characteristics: 2, matrix_coefficients: 2, field_sequence: false, num_units_in_tick: 0u32, time_scale: 0u32 };
}

function aspect(input: [u8], at: i64): Result[(i64, AspectRatio), frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((at, AspectRatio { numerator: 0u32, denominator: 0u32 }));
  }
  let (a, id) = bits.read(input, at, 8)?;
  if (id == 255u32) {
    let (b, width) = bits.read(input, a, 16)?;
    let (c, height) = bits.read(input, b, 16)?;
    return Ok((c, AspectRatio { numerator: width, denominator: height }));
  }
  if (id > 16u32) {
    return Err(frame.Header);
  }
  let widths: u32[] = [0, 1, 12, 10, 16, 40, 24, 20, 32, 80, 18, 15, 64, 160, 4, 3, 2];
  let heights: u32[] = [0, 1, 11, 11, 11, 33, 11, 11, 11, 33, 11, 11, 33, 99, 3, 2, 1];
  return Ok((a, AspectRatio { numerator: widths[id as i32], denominator: heights[id as i32] }));
}

function signal(input: [u8], at: i64, value: Vui): Result[(i64, Vui), frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((at, value));
  }
  let (a, flags) = bits.read(input, at, 5)?;
  if (flags >> 2u32 > 5u32) {
    return Err(frame.Header);
  }
  value = Vui { ...value, video_signal_present: true, video_format: (flags >> 2u32) as i32, full_range: (flags & 2u32) != 0u32 };
  if ((flags & 1u32) == 0u32) {
    return Ok((a, value));
  }
  let (b, colour) = bits.read(input, a, 24)?;
  return Ok((b, Vui { ...value, colour_description_present: true, colour_primaries: (colour >> 16u32) as i32, transfer_characteristics: (colour >> 8u32 & 255u32) as i32, matrix_coefficients: (colour & 255u32) as i32 }));
}

function display_window(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present != 0u32) {
    let (a, _) = bits.bounded_ue(input, at, 16384u32)?;
    let (b, _) = bits.bounded_ue(input, a, 16384u32)?;
    let (c, _) = bits.bounded_ue(input, b, 16384u32)?;
    let (d, _) = bits.bounded_ue(input, c, 16384u32)?;
    return Ok(d);
  }
  return Ok(at);
}

function hrd_common(input: [u8], at: i64): Result[Hrd, frame.VideoError] {
  let (at, flags) = bits.read(input, at, 2)?;
  let sub = 0u32;
  if (flags != 0u32) {
    let (next, value) = bits.read(input, at, 1)?;
    at = next;
    sub = value;
    if (sub != 0u32) {
      at = skip(input, at, 19)?;
    }
    at = skip(input, at, if (sub != 0u32) { 12 } else { 8 })?;
    at = skip(input, at, 15)?;
  }
  return Ok(Hrd { at: at, nal: (flags & 2u32) != 0u32, vcl: (flags & 1u32) != 0u32, sub_picture: sub != 0u32 });
}

function cpb_entries(input: [u8], at: i64, count: i32, sub_picture: boolean): Result[i64, frame.VideoError] {
  for _ in 0..count {
    let (a, _) = bits.ue(input, at)?;
    let (b, _) = bits.ue(input, a)?;
    at = b;
    if (sub_picture) {
      let (c, _) = bits.ue(input, at)?;
      let (d, _) = bits.ue(input, c)?;
      at = d;
    }
    at = skip(input, at, 1)?;
  }
  return Ok(at);
}

function cpb_count(input: [u8], at: i64): Result[(i64, i32), frame.VideoError] {
  let (at, fixed_general) = bits.read(input, at, 1)?;
  let fixed = fixed_general;
  if (fixed == 0u32) {
    let (next, value) = bits.read(input, at, 1)?;
    at = next;
    fixed = value;
  }
  let low_delay = 0u32;
  if (fixed != 0u32) {
    let (next, _) = bits.bounded_ue(input, at, 2047u32)?;
    at = next;
  } else {
    let (next, value) = bits.read(input, at, 1)?;
    at = next;
    low_delay = value;
  }
  if (low_delay != 0u32) {
    return Ok((at, 1));
  }
  let (next, count) = bits.bounded_ue(input, at, 31u32)?;
  return Ok((next, count as i32 + 1));
}

function hrd(input: [u8], at: i64, sub: i32): Result[i64, frame.VideoError] {
  let common = hrd_common(input, at)?;
  at = common.at;
  for _ in 0..sub + 1 {
    let (next, count) = cpb_count(input, at)?;
    at = next;
    if (common.nal) {
      at = cpb_entries(input, at, count, common.sub_picture)?;
    }
    if (common.vcl) {
      at = cpb_entries(input, at, count, common.sub_picture)?;
    }
  }
  return Ok(at);
}

function timing(input: [u8], at: i64, value: Vui, sub: i32): Result[(i64, Vui), frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((at, value));
  }
  let (a, tick) = bits.read(input, at, 32)?;
  let (b, scale) = bits.read(input, a, 32)?;
  if (tick == 0u32 || scale == 0u32) {
    return Err(frame.Header);
  }
  at = timing_tail(input, b, sub)?;
  return Ok((at, Vui { ...value, num_units_in_tick: tick, time_scale: scale }));
}

function timing_tail(input: [u8], at: i64, sub: i32): Result[i64, frame.VideoError] {
  let (at, proportional) = bits.read(input, at, 1)?;
  if (proportional != 0u32) {
    let (next, _) = bits.ue(input, at)?;
    at = next;
  }
  let (d, present_hrd) = bits.read(input, at, 1)?;
  at = d;
  if (present_hrd != 0u32) {
    at = hrd(input, at, sub)?;
  }
  return Ok(at);
}

function restriction(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok(at);
  }
  at = skip(input, at, 3)?;
  let (a, _) = bits.bounded_ue(input, at, 4095u32)?;
  let (b, _) = bits.bounded_ue(input, a, 16u32)?;
  let (c, _) = bits.bounded_ue(input, b, 16u32)?;
  let (d, _) = bits.bounded_ue(input, c, 15u32)?;
  let (e, _) = bits.bounded_ue(input, d, 15u32)?;
  return Ok(e);
}

function vui(input: [u8], at: i64, sub: i32): Result[(i64, Vui), frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((at, empty_vui()));
  }
  let (a, value) = vui_signal(input, at)?;
  let (b, timed) = timing(input, a, value, sub)?;
  at = restriction(input, b)?;
  return Ok((at, timed));
}

function vui_signal(input: [u8], at: i64): Result[(i64, Vui), frame.VideoError] {
  let (a, ratio) = aspect(input, at)?;
  at = bits.overscan(input, a)?;
  let (b, value) = signal(input, at, Vui { ...empty_vui(), aspect_ratio: AspectRatio { numerator: ratio.numerator, denominator: ratio.denominator } })?;
  at = bits.chroma_location(input, b)?;
  let (c, flags) = bits.read(input, at, 3)?;
  at = display_window(input, c)?;
  return Ok((at, Vui { ...value, field_sequence: (flags & 2u32) != 0u32 }));
}

function extensions(input: [u8], at: i64): Result[i64, frame.VideoError] {
  let (at, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok(at);
  }
  let (next, flags) = bits.read(input, at, 8)?;
  if ((flags & 127u32) != 0u32) {
    return Err(frame.Unsupported);
  }
  return if ((flags & 128u32) != 0u32) { skip(input, next, 9) } else { Ok(next) };
}

function body(bytes: [u8], p: Prefix, picture: Picture): Result[(i64, Ordering, Blocks), frame.VideoError] {
  let order = ordering(bytes, picture.at, p.sub)?;
  let block = blocks(bytes, order.at, picture.coded)?;
  let at = scaling(bytes, block.at)?;
  at = pcm(bytes, at, picture, block)?;
  at = reference_sets(bytes, at, order.buffering - 1)?;
  at = long_term(bytes, at, picture.poc)?;
  at = skip(bytes, at, 2)?;
  return Ok((at, order, block));
}

pub(package) function sequence_context(input: [u8], limits: frame.Limits): Result[SequenceContext, frame.VideoError] {
  let bytes = parameter_bytes(input, limits)?;
  let p = prefix(bytes)?;
  let picture = picture(bytes, p.at, limits)?;
  let (at, order, block) = body(bytes, p, picture)?;
  let (next, vui) = vui(bytes, at, p.sub)?;
  let end = extensions(bytes, next)?;
  bits.trailing(bytes, end)?;
  let sequence = Sequence { profile_space: p.space, tier: p.tier, profile_idc: p.profile, level_idc: p.level, compatibility_flags: p.compatibility, vps_id: p.vps, sequence_id: picture.id, max_sub_layers: p.sub + 1, temporal_nesting: p.nesting, chroma_format_idc: picture.chroma, separate_colour_plane: picture.separate, bit_depth_luma: picture.luma, bit_depth_chroma: picture.chroma_depth, coded: picture.coded, visible: picture.visible, crop: picture.crop, log2_max_poc_lsb: picture.poc, max_dec_pic_buffering: order.buffering, max_num_reorder_pics: order.reorder, vui: vui };
  return Ok(SequenceContext { sequence: sequence, minimum: block.minimum, ctb: block.ctb, transform: block.transform });
}

pub function inspect_sps(input: [u8], limits: frame.Limits): Result[Sequence, frame.VideoError] {
  let context = sequence_context(input, limits)?;
  return Ok(context.sequence);
}
```
