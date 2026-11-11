# HEVC picture parameter metadata

This reader checks one base-layer PPS NAL against one complete SPS NAL.
Each input includes its two-byte NAL header, without start codes, length prefixes, or surrounding zero bytes.
The reader reuses the existing SPS parser and checks the matching sequence identifier.
It retains no input bytes, tile map, or scaling coefficients.

The packet budget applies to each NAL before an unescaped copy.
A separate 64 KiB ceiling bounds the PPS. The existing SPS ceiling remains 8 KiB.
The coded-picture budget applies to the SPS dimensions before crop.
Tile syntax requires positive dimensions and complete coverage in coded-tree-block units.
This is parameter syntax, without picture reconstruction or complete profile and level conformance.

```fern
<<*>>=
// Inspect bounded HEVC PPS syntax with its matching SPS.
import "std/video/hevc";
import "std/video/avc" as bits;
import "std/video/frame";

pub const MAX_PPS_BYTES: i32 = 65536;

pub struct PictureParameters { picture_id: i32, sequence_id: i32, dependent_slice_segments: boolean, output_flag_present: boolean, extra_slice_header_bits: i32, sign_data_hiding: boolean, cabac_init_present: boolean, default_l0_references: i32, default_l1_references: i32, initial_qp: i32, constrained_intra: boolean, transform_skip: boolean, cu_qp_delta: boolean, qp_delta_depth: i32, cb_qp_offset: i32, cr_qp_offset: i32, slice_chroma_qp_offsets: boolean, weighted_prediction: boolean, weighted_biprediction: boolean, transquant_bypass: boolean, tiles: boolean, entropy_coding_sync: boolean, tile_columns: i32, tile_rows: i32, uniform_spacing: boolean, loop_filter_across_tiles: boolean, loop_filter_across_slices: boolean, deblock_control: boolean, deblock_override: boolean, deblock_disabled: boolean, beta_offset_div2: i32, tc_offset_div2: i32, scaling_lists: boolean, lists_modification: boolean, parallel_merge_log2: i32, slice_header_extension: boolean, range_extension: boolean, transform_skip_log2: i32, cross_component_prediction: boolean, chroma_qp_offset_list: boolean, chroma_qp_depth: i32, chroma_qp_entries: i32, sao_luma_scale: i32, sao_chroma_scale: i32 }

function empty(): PictureParameters {
  return PictureParameters { picture_id: 0, sequence_id: 0, dependent_slice_segments: false, output_flag_present: false, extra_slice_header_bits: 0, sign_data_hiding: false, cabac_init_present: false, default_l0_references: 1, default_l1_references: 1, initial_qp: 26, constrained_intra: false, transform_skip: false, cu_qp_delta: false, qp_delta_depth: 0, cb_qp_offset: 0, cr_qp_offset: 0, slice_chroma_qp_offsets: false, weighted_prediction: false, weighted_biprediction: false, transquant_bypass: false, tiles: false, entropy_coding_sync: false, tile_columns: 1, tile_rows: 1, uniform_spacing: true, loop_filter_across_tiles: true, loop_filter_across_slices: false, deblock_control: false, deblock_override: false, deblock_disabled: false, beta_offset_div2: 0, tc_offset_div2: 0, scaling_lists: false, lists_modification: false, parallel_merge_log2: 2, slice_header_extension: false, range_extension: false, transform_skip_log2: 2, cross_component_prediction: false, chroma_qp_offset_list: false, chroma_qp_depth: 0, chroma_qp_entries: 0, sao_luma_scale: 0, sao_chroma_scale: 0 };
}

function parameter_bytes(input: [u8], limits: frame.Limits): Result[u8[], frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() > MAX_PPS_BYTES) {
    return Err(frame.Limit);
  }
  if (input.len() < 2) {
    return Err(frame.Truncated);
  }
  if ((input[0] & 128u8) != 0u8 || (input[1] & 7u8) == 0u8) {
    return Err(frame.Header);
  }
  if ((input[0] >> 1u8 & 63u8) != 34u8 || (input[0] & 1u8) != 0u8 || input[1] >> 3u8 != 0u8) {
    return Err(frame.Unsupported);
  }
  if ((input[1] & 7u8) != 1u8) {
    return Err(frame.Header);
  }
  return bits.rbsp_bytes(input[2:]);
}

function signed(input: [u8], at: i64, minimum: i32, maximum: i32): Result[(i64, i32), frame.VideoError] {
  let (next, value) = bits.se(input, at)?;
  if (value < minimum || value > maximum) {
    return Err(frame.Header);
  }
  return Ok((next, value));
}

function prefix(input: [u8], context: hevc.SequenceContext): Result[(i64, PictureParameters), frame.VideoError] {
  let (a, id) = bits.bounded_ue(input, 0i64, 63u32)?;
  let (b, sequence) = bits.bounded_ue(input, a, 15u32)?;
  if (sequence as i32 != context.sequence.sequence_id) {
    return Err(frame.Header);
  }
  let (c, flags) = bits.read(input, b, 7)?;
  let (d, l0) = bits.bounded_ue(input, c, 14u32)?;
  let (e, l1) = bits.bounded_ue(input, d, 14u32)?;
  let (f, qp) = signed(input, e, -(26 + 6 * (context.sequence.bit_depth_luma - 8)), 25)?;
  return Ok((f, PictureParameters { ...empty(), picture_id: id as i32, sequence_id: sequence as i32, dependent_slice_segments: (flags & 64u32) != 0u32, output_flag_present: (flags & 32u32) != 0u32, extra_slice_header_bits: (flags >> 2u32 & 7u32) as i32, sign_data_hiding: (flags & 2u32) != 0u32, cabac_init_present: (flags & 1u32) != 0u32, default_l0_references: l0 as i32 + 1, default_l1_references: l1 as i32 + 1, initial_qp: qp + 26 }));
}

function prediction(input: [u8], at: i64, context: hevc.SequenceContext, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  let (a, flags) = bits.read(input, at, 3)?;
  let depth = 0u32;
  at = a;
  if ((flags & 1u32) != 0u32) {
    let (next, value) = bits.bounded_ue(input, at, (context.ctb - context.minimum) as u32)?;
    at = next;
    depth = value;
  }
  let (b, cb) = signed(input, at, -12, 12)?;
  let (c, cr) = signed(input, b, -12, 12)?;
  let (d, other) = bits.read(input, c, 6)?;
  return Ok((d, PictureParameters { ...p, constrained_intra: (flags & 4u32) != 0u32, transform_skip: (flags & 2u32) != 0u32, cu_qp_delta: (flags & 1u32) != 0u32, qp_delta_depth: depth as i32, cb_qp_offset: cb, cr_qp_offset: cr, slice_chroma_qp_offsets: (other & 32u32) != 0u32, weighted_prediction: (other & 16u32) != 0u32, weighted_biprediction: (other & 8u32) != 0u32, transquant_bypass: (other & 4u32) != 0u32, tiles: (other & 2u32) != 0u32, entropy_coding_sync: (other & 1u32) != 0u32 }));
}

function units(value: i32, log2: i32): u32 {
  let size = 1i64 << log2 as i64;
  return ((value as i64 + size - 1i64) / size) as u32;
}

// Reserve one positive extent for each column or row after the current entry.
function extents(input: [u8], at: i64, count: i32, available: u32): Result[i64, frame.VideoError] {
  for i in 0..count {
    let (next, width) = bits.bounded_ue(input, at, available - (count - i) as u32 - 1u32)?;
    at = next;
    available -= width + 1u32;
  }
  return Ok(at);
}

function tile_grid(input: [u8], at: i64, width: u32, height: u32): Result[(i64, i32, i32, boolean), frame.VideoError] {
  let (a, columns) = bits.bounded_ue(input, at, width - 1u32)?;
  let (b, rows) = bits.bounded_ue(input, a, height - 1u32)?;
  if (columns == 0u32 && rows == 0u32) {
    return Err(frame.Header);
  }
  let (c, uniform) = bits.read(input, b, 1)?;
  return Ok((c, columns as i32, rows as i32, uniform != 0u32));
}

function tiles(input: [u8], at: i64, context: hevc.SequenceContext, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  if (!p.tiles) {
    return Ok((at, p));
  }
  let width = units(context.sequence.coded.width, context.ctb);
  let height = units(context.sequence.coded.height, context.ctb);
  let (c, columns, rows, uniform) = tile_grid(input, at, width, height)?;
  at = c;
  if (!uniform) {
    at = extents(input, at, columns as i32, width)?;
    at = extents(input, at, rows as i32, height)?;
  }
  let (d, across) = bits.read(input, at, 1)?;
  return Ok((d, PictureParameters { ...p, tile_columns: columns + 1, tile_rows: rows + 1, uniform_spacing: uniform, loop_filter_across_tiles: across != 0u32 }));
}

function deblocking(input: [u8], at: i64, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  let (a, flags) = bits.read(input, at, 2)?;
  p = PictureParameters { ...p, loop_filter_across_slices: (flags & 2u32) != 0u32, deblock_control: (flags & 1u32) != 0u32 };
  if (!p.deblock_control) {
    return Ok((a, p));
  }
  let (b, control) = bits.read(input, a, 2)?;
  p = PictureParameters { ...p, deblock_override: (control & 2u32) != 0u32, deblock_disabled: (control & 1u32) != 0u32 };
  if (p.deblock_disabled) {
    return Ok((b, p));
  }
  let (c, beta) = signed(input, b, -6, 6)?;
  let (d, tc) = signed(input, c, -6, 6)?;
  return Ok((d, PictureParameters { ...p, beta_offset_div2: beta, tc_offset_div2: tc }));
}

function suffix(input: [u8], at: i64, context: hevc.SequenceContext, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  let (a, scaling) = bits.read(input, at, 1)?;
  at = a;
  if (scaling != 0u32) {
    at = hevc.scaling_lists(input, at)?;
  }
  let (b, lists) = bits.read(input, at, 1)?;
  let (c, merge) = bits.bounded_ue(input, b, (context.ctb - 2) as u32)?;
  let (d, extension) = bits.read(input, c, 1)?;
  return Ok((d, PictureParameters { ...p, scaling_lists: scaling != 0u32, lists_modification: lists != 0u32, parallel_merge_log2: merge as i32 + 2, slice_header_extension: extension != 0u32 }));
}

function chroma_array_type(sequence: hevc.Sequence): i32 {
  return if (sequence.separate_colour_plane) { 0 } else { sequence.chroma_format_idc };
}

function chroma_list(input: [u8], at: i64, context: hevc.SequenceContext, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  if (!p.chroma_qp_offset_list) {
    return Ok((at, p));
  }
  if (chroma_array_type(context.sequence) == 0) {
    return Err(frame.Header);
  }
  let (a, depth) = bits.bounded_ue(input, at, (context.ctb - context.minimum) as u32)?;
  let (b, count) = bits.bounded_ue(input, a, 5u32)?;
  at = b;
  for _ in 0..count as i32 + 1 {
    let (c, _) = signed(input, at, -12, 12)?;
    let (d, _) = signed(input, c, -12, 12)?;
    at = d;
  }
  return Ok((at, PictureParameters { ...p, chroma_qp_depth: depth as i32, chroma_qp_entries: count as i32 + 1 }));
}

function sao_limit(depth: i32): u32 {
  return if (depth > 10) { (depth - 10) as u32 } else { 0u32 };
}

function range_extension(input: [u8], at: i64, context: hevc.SequenceContext, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  let transform = 0u32;
  if (p.transform_skip) {
    let (next, value) = bits.bounded_ue(input, at, (context.transform - 2) as u32)?;
    at = next;
    transform = value;
  }
  let (a, flags) = bits.read(input, at, 2)?;
  if ((flags & 2u32) != 0u32 && chroma_array_type(context.sequence) != 3) {
    return Err(frame.Header);
  }
  let (b, value) = chroma_list(input, a, context, PictureParameters { ...p, range_extension: true, transform_skip_log2: transform as i32 + 2, cross_component_prediction: (flags & 2u32) != 0u32, chroma_qp_offset_list: (flags & 1u32) != 0u32 })?;
  let (c, luma_scale) = bits.bounded_ue(input, b, sao_limit(context.sequence.bit_depth_luma))?;
  let (d, chroma_scale) = bits.bounded_ue(input, c, sao_limit(context.sequence.bit_depth_chroma))?;
  return Ok((d, PictureParameters { ...value, sao_luma_scale: luma_scale as i32, sao_chroma_scale: chroma_scale as i32 }));
}

function extensions(input: [u8], at: i64, context: hevc.SequenceContext, p: PictureParameters): Result[(i64, PictureParameters), frame.VideoError] {
  let (a, present) = bits.read(input, at, 1)?;
  if (present == 0u32) {
    return Ok((a, p));
  }
  let (b, flags) = bits.read(input, a, 8)?;
  if ((flags & 127u32) != 0u32) {
    return Err(frame.Unsupported);
  }
  return if ((flags & 128u32) != 0u32) { range_extension(input, b, context, p) } else { Ok((b, p)) };
}

pub function inspect(input: [u8], sps: [u8], limits: frame.Limits): Result[PictureParameters, frame.VideoError] {
  let bytes = parameter_bytes(input, limits)?;
  let context = hevc.sequence_context(sps, limits)?;
  let (a, initial) = prefix(bytes, context)?;
  let (b, predicted) = prediction(bytes, a, context, initial)?;
  let (c, tiled) = tiles(bytes, b, context, predicted)?;
  let (d, filtered) = deblocking(bytes, c, tiled)?;
  let (e, scaled) = suffix(bytes, d, context, filtered)?;
  let (f, extended) = extensions(bytes, e, context, scaled)?;
  bits.trailing(bytes, f)?;
  return Ok(extended);
}
```
