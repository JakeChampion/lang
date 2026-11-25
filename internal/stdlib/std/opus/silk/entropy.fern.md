# SILK frame parameters

This module codes SILK frame parameters with the shared Opus range coder.
The sample rate is 8, 12, or 16 kHz. An internal frame lasts 10 or 20 milliseconds.
The caller codes packet activity flags and redundant frames before the primary frames.
The caller retains separate prediction histories for the two stereo channels.

A frame has two forms. `Indices` holds the coded symbols of the frame.
`Control` holds the dequantized parameters that the SILK core uses.
`decode_indices` reads the symbols, and `dequantize` gives the parameters.

`encode` writes the symbols. It makes the same choices as libopus for the rate level, the pulse shifts and the pitch lag.
An inactive frame has the signal type 1. A voiced frame has the type 3, and each other frame has the type 2.

The decoder derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
The encoder derives from `encode_indices.c` and `encode_pulses.c` of libopus `22244de5`.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK entropy coding for one internal frame and one coded channel.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/range";
import "std/opus/silk/math";
import "std/opus/silk/predict";
import "std/opus/silk/tables";

pub struct History { gain: i32, lag: i32, voiced: boolean, decoded: boolean, lsf: i32[] }

pub struct Control {
  signal: i32,
  high_offset: boolean,
  gains: i32[],
  lsf: i32[],
  first_lpc: i32[],
  lpc: i32[],
  interpolation: i32,
  pitch: i32[],
  ltp: i32[],
  ltp_scale: i32,
  seed: i32,
  pulses: i32[],
  excitation: i32[],
}

// The first of `gains` is an absolute index from 0 to 63 when the frame
// codes it independently, and otherwise a change from 0 to 40, as is each
// other gain. `interpolation` is 4 when a 10 ms frame does not code it. An
// unvoiced frame has 0 for `lag`, `contour`, `periodicity` and `ltp_scale`,
// and no `ltp`. `pulses` holds 16 signed values for each shell block.
pub struct Indices {
  signal: i32,
  high_offset: boolean,
  gains: i32[],
  lsf_first: i32,
  lsf: i32[],
  interpolation: i32,
  lag: i32,
  contour: i32,
  periodicity: i32,
  ltp: i32[],
  ltp_scale: i32,
  seed: i32,
  pulses: i32[],
}

// The bits of a pulse count for each rate level, and of each rate level for
// unvoiced and voiced frames, in 1/32 bits, from libopus.
const PULSES_PER_BLOCK_BITS_Q5: i32[] = [
  31, 57, 107, 160, 205, 205, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
  69, 47, 67, 111, 166, 205, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
  82, 74, 79, 95, 109, 128, 145, 160, 173, 205, 205, 205, 224, 255, 255, 224, 255, 224,
  125, 74, 59, 69, 97, 141, 182, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
  173, 115, 85, 73, 76, 92, 115, 145, 173, 205, 224, 224, 255, 255, 255, 255, 255, 255,
  166, 134, 113, 102, 101, 102, 107, 118, 125, 138, 145, 155, 166, 182, 192, 192, 205, 150,
  224, 182, 134, 101, 83, 79, 85, 97, 120, 145, 173, 205, 224, 255, 255, 255, 255, 255,
  255, 224, 192, 150, 120, 101, 92, 89, 93, 102, 118, 134, 160, 182, 192, 224, 224, 224,
  255, 224, 224, 182, 155, 134, 118, 109, 104, 102, 106, 111, 118, 131, 145, 160, 173, 131,
];

const RATE_LEVELS_BITS_Q5: i32[] = [
  131, 74, 141, 79, 80, 138, 95, 104, 134,
  95, 99, 91, 125, 93, 76, 123, 115, 123,
];

// The largest pulse sum of a partition of 2, 4, 8 and 16 samples that the
// libopus encoder codes without a shift.
const MAX_PULSES: i32[] = [8, 10, 12, 16];

const RATE_LEVELS: i32 = 9;

const PULSE_ESCAPE: i32 = 17;

const MAX_SHIFTS: i32 = 10;

pub function history(): History {
  return History { gain: 0, lag: 0, voiced: false, decoded: false, lsf: [] };
}

function symbol(own decoder: range.Decoder, table: [u8], offsets: [i32], row: i32): (range.Decoder, i32) {
  return decoder.icdf(table[offsets[row]:offsets[row + 1]], 8);
}

function frame_type(own decoder: range.Decoder, active: boolean): (range.Decoder, i32, boolean) {
  let table = if (active) { tables.ICDF_FRAME_TYPE_VAD_ACTIVE } else { tables.ICDF_FRAME_TYPE_VAD_INACTIVE };
  let (next, value) = decoder.icdf(table, 8);
  return (next, if (active) { 2 + value / 2 } else { 1 }, value % 2 == 1);
}

function gain_msb(signal: i32): u8[] {
  if (signal == 1) {
    return tables.ICDF_INDEPENDENT_QUANTIZATION_GAIN_MSB_INACTIVE;
  }
  if (signal == 3) {
    return tables.ICDF_INDEPENDENT_QUANTIZATION_GAIN_MSB_VOICED;
  }
  return tables.ICDF_INDEPENDENT_QUANTIZATION_GAIN_MSB_UNVOICED;
}

function gain_indices(own decoder: range.Decoder, signal: i32, count: i32, absolute: boolean): (range.Decoder, i32[]) {
  let output: i32[] = [];
  for i in 0..count {
    if (i == 0 && absolute) {
      let (next, msb) = decoder.icdf(gain_msb(signal), 8);
      let (last, lsb) = next.icdf(tables.ICDF_INDEPENDENT_QUANTIZATION_GAIN_LSB, 8);
      decoder = last;
      output = output.append(msb << 3 | lsb);
    } else {
      let (next, delta) = decoder.icdf(tables.ICDF_DELTA_QUANTIZATION_GAIN, 8);
      decoder = next;
      output = output.append(delta);
    }
  }
  return (decoder, output);
}

function gains(indices: [i32], absolute: boolean, decoded: boolean, own previous: i32): (i32, i32[]) {
  let output: i32[] = [];
  for i in 0..indices.len() {
    if (i == 0 && absolute) {
      let gain = indices[0];
      previous = if (decoded) { math.clamp(gain, previous - 16, math.MAX32) } else { gain };
    } else {
      let delta = indices[i];
      let a = 2 * delta - 16;
      let b = previous + delta - 4;
      previous = math.clamp(0, if (a > b) { a } else { b }, 63);
    }
    let log = (0x1D1C71 * previous >> 16) + 2090;
    let exponent = 1 << (log >> 7);
    let fraction = log & 127;
    let value = exponent + ((-174 * fraction * (128 - fraction) >> 16) + fraction) * (exponent >> 7);
    output = output.append(value);
  }
  return (previous, output);
}

function lsf_first(own decoder: range.Decoder, wide: boolean, voiced: boolean): (range.Decoder, i32) {
  if (wide) {
    return decoder.icdf(if (voiced) { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_WIDEBAND_VOICED } else { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_WIDEBAND_UNVOICED }, 8);
  }
  return decoder.icdf(if (voiced) { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_NARROWBAND_OR_MEDIUMBAND_VOICED } else { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_NARROWBAND_OR_MEDIUMBAND_UNVOICED }, 8);
}

function lsf_indices(own decoder: range.Decoder, wide: boolean, first: i32): (range.Decoder, i32[]) {
  let order = if (wide) { 16 } else { 10 };
  let codebook = if (wide) { tables.CODEBOOK_NORMALIZED_LSF_STAGE_TWO_INDEX_WIDEBAND } else { tables.CODEBOOK_NORMALIZED_LSF_STAGE_TWO_INDEX_NARROWBAND_OR_MEDIUMBAND };
  let indices: i32[] = [];
  for i in 0..order {
    let (next, value) = symbol(decoder, tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX, tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX_OFFSETS, codebook[first * order + i] as i32);
    decoder = next;
    let index = value - 4;
    if (index == -4 || index == 4) {
      let (last, extension) = decoder.icdf(tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX_EXTENSION, 8);
      decoder = last;
      index += if (index < 0) { -extension } else { extension };
    }
    indices = indices.append(index);
  }
  return (decoder, indices);
}

function lsf_residuals(indices: [i32], wide: boolean, first: i32): i32[] {
  let order = indices.len();
  let weights = if (wide) { tables.PREDICTION_WEIGHT_FOR_WIDEBAND_NORMALIZED_LSF } else { tables.PREDICTION_WEIGHT_FOR_NARROWBAND_AND_MEDIUMBAND_NORMALIZED_LSF };
  let selection = if (wide) { tables.PREDICTION_WEIGHT_SELECTION_FOR_WIDEBAND_NORMALIZED_LSF } else { tables.PREDICTION_WEIGHT_SELECTION_FOR_NARROWBAND_AND_MEDIUMBAND_NORMALIZED_LSF };
  let step = if (wide) { 9830 } else { 11796 };
  let residual = pcm.zeros(order);
  let k = order - 1;
  while (k >= 0) {
    let prediction = 0;
    if (k + 1 < order) {
      let weight = weights[selection[first * (order - 1) + k] as i32 * (order - 1) + k] as i32;
      prediction = residual[k + 1] * weight >> 8;
    }
    let index = indices[k];
    let sign = if (index < 0) { -1 } else if (index > 0) { 1 } else { 0 };
    residual = residual.with(k, math.s16(prediction + (((index << 10) - sign * 102) * step >> 16)));
    k -= 1;
  }
  return residual;
}

function lsf_coefficients(residual: [i32], wide: boolean, first: i32): Result[i32[], pcm.AudioError] {
  let order = residual.len();
  let codebook = if (wide) { tables.CODEBOOK_NORMALIZED_LSF_STAGE_ONE_WIDEBAND } else { tables.CODEBOOK_NORMALIZED_LSF_STAGE_ONE_NARROWBAND_OR_MEDIUMBAND };
  let output: i32[] = [];
  for k in 0..order {
    let at = first * order + k;
    let current = codebook[at] as i32;
    let previous = if (k == 0) { 0 } else { codebook[at - 1] as i32 };
    let next = if (k + 1 == order) { 256 } else { codebook[at + 1] as i32 };
    let squared = 1024 / (current - previous) + 1024 / (next - current) << 16;
    let bits = math.ilog(squared);
    let fraction = squared >> bits - 8 & 127;
    let y = (if ((bits & 1) == 0) { 46214 } else { 32768 }) >> (32 - bits >> 1);
    let weight = math.s16(y + (213 * fraction * y >> 16));
    output = output.append(math.clamp(0, (current << 7) + (residual[k] << 14) / weight, 32767));
  }
  return predict.stabilize(output, wide);
}

function lsf_interpolate(factor: i32, previous: [i32], current: [i32], decoded: boolean): (i32, i32[]) {
  if (factor == 4 || !decoded || previous.len() != current.len()) {
    return (4, []);
  }
  let output: i32[] = [];
  for i in 0..current.len() {
    output = output.append(math.s16(previous[i] + (factor * (current[i] - previous[i]) >> 2)));
  }
  return (factor, output);
}

function contour_table(rate: i32, duration: i32): u8[] {
  if (rate == 8) {
    return if (duration == 10) { tables.ICDF_SUBFRAME_PITCH_CONTOUR_NARROWBAND10_MS } else { tables.ICDF_SUBFRAME_PITCH_CONTOUR_NARROWBAND20_MS };
  }
  return if (duration == 10) { tables.ICDF_SUBFRAME_PITCH_CONTOUR_MEDIUMBAND_OR_WIDEBAND10_MS } else { tables.ICDF_SUBFRAME_PITCH_CONTOUR_MEDIUMBAND_OR_WIDEBAND20_MS };
}

function lag_low_table(rate: i32): u8[] {
  return if (rate == 8) { tables.ICDF_PRIMARY_PITCH_LAG_LOW_PART_NARROWBAND } else if (rate == 12) { tables.ICDF_PRIMARY_PITCH_LAG_LOW_PART_MEDIUMBAND } else { tables.ICDF_PRIMARY_PITCH_LAG_LOW_PART_WIDEBAND };
}

function ltp_table(periodicity: i32): u8[] {
  return if (periodicity == 0) { tables.ICDF_LTP_FILTER_INDEX0 } else if (periodicity == 1) { tables.ICDF_LTP_FILTER_INDEX1 } else { tables.ICDF_LTP_FILTER_INDEX2 };
}

function contour_value(rate: i32, duration: i32, at: i32): i32 {
  if (rate == 8) {
    if (duration == 10) {
      return tables.CODEBOOK_SUBFRAME_PITCH_COUNTER_NARROWBAND10_MS[at] as i32;
    }
    return tables.CODEBOOK_SUBFRAME_PITCH_COUNTER_NARROWBAND20_MS[at];
  }
  if (duration == 10) {
    return tables.CODEBOOK_SUBFRAME_PITCH_COUNTER_MEDIUMBAND_OR_WIDEBAND10_MS[at];
  }
  return tables.CODEBOOK_SUBFRAME_PITCH_COUNTER_MEDIUMBAND_OR_WIDEBAND20_MS[at];
}

// A voiced frame codes its lag as a change from the lag of the previous
// frame when it can, and otherwise as an absolute value.
function delta_lag(independent: boolean, previous: History): boolean {
  return !independent && previous.voiced;
}

function pitch_lag(own decoder: range.Decoder, rate: i32, independent: boolean, previous: History): (range.Decoder, i32) {
  let absolute = !delta_lag(independent, previous);
  let lag = previous.lag;
  if (!absolute) {
    let (next, delta) = decoder.icdf(tables.ICDF_PRIMARY_PITCH_LAG_CHANGE, 8);
    decoder = next;
    absolute = delta == 0;
    lag += delta - 9;
  }
  if (absolute) {
    let (next, high) = decoder.icdf(tables.ICDF_PRIMARY_PITCH_LAG_HIGH_PART, 8);
    let (last, low) = next.icdf(lag_low_table(rate), 8);
    decoder = last;
    lag = high * (rate / 2) + low + rate * 2;
  }
  return (decoder, lag);
}

function pitch(lag: i32, row: i32, rate: i32, duration: i32): i32[] {
  let output: i32[] = [];
  let count = duration / 5;
  for i in 0..count {
    output = output.append(math.clamp(rate * 2, lag + contour_value(rate, duration, row * count + i), rate * 18));
  }
  return output;
}

function ltp(periodicity: i32, rows: [i32]): i32[] {
  let codebook = if (periodicity == 0) { tables.CODEBOOK_LTP_FILTER_PERIODICITY_INDEX0 } else if (periodicity == 1) { tables.CODEBOOK_LTP_FILTER_PERIODICITY_INDEX1 } else { tables.CODEBOOK_LTP_FILTER_PERIODICITY_INDEX2 };
  let output: i32[] = [];
  for index in rows {
    for i in 0..5 {
      output = output.append(codebook[index * 5 + i]);
    }
  }
  return output;
}

// The pulse count row after `shifts` escapes. The row of the last shift
// cannot code a further escape.
function escape_row(shifts: i32): i32 {
  return if (shifts == MAX_SHIFTS) { RATE_LEVELS + 1 } else { RATE_LEVELS };
}

function pulse_counts(own decoder: range.Decoder, blocks: i32, voiced: boolean): (range.Decoder, i32[], i32[]) {
  let (next, level) = decoder.icdf(if (voiced) { tables.ICDF_RATE_LEVEL_VOICED } else { tables.ICDF_RATE_LEVEL_UNVOICED }, 8);
  decoder = next;
  let pulses: i32[] = [];
  let shifts: i32[] = [];
  for _ in 0..blocks {
    let (last, value) = symbol(decoder, tables.ICDF_PULSE_COUNT, tables.ICDF_PULSE_COUNT_OFFSETS, level);
    decoder = last;
    let count = value;
    let bits = 0;
    while (count == PULSE_ESCAPE) {
      bits += 1;
      let (more, escaped) = symbol(decoder, tables.ICDF_PULSE_COUNT, tables.ICDF_PULSE_COUNT_OFFSETS, escape_row(bits));
      decoder = more;
      count = escaped;
    }
    pulses = pulses.append(count);
    shifts = shifts.append(bits);
  }
  return (decoder, pulses, shifts);
}

function split(own decoder: range.Decoder, count: i32, size: i32): (range.Decoder, i32, i32) {
  if (count == 0) {
    return (decoder, 0, 0);
  }
  let (next, left) = symbol(decoder, split_table(size), split_offsets(size), count - 1);
  return (next, left, count - left);
}

function split_table(size: i32): u8[] {
  return if (size == 16) { tables.ICDF_PULSE_COUNT_SPLIT16_SAMPLE_PARTITIONS } else if (size == 8) { tables.ICDF_PULSE_COUNT_SPLIT8_SAMPLE_PARTITIONS } else if (size == 4) { tables.ICDF_PULSE_COUNT_SPLIT4_SAMPLE_PARTITIONS } else { tables.ICDF_PULSE_COUNT_SPLIT2_SAMPLE_PARTITIONS };
}

function split_offsets(size: i32): i32[] {
  return if (size == 16) { tables.ICDF_PULSE_COUNT_SPLIT16_SAMPLE_PARTITIONS_OFFSETS } else if (size == 8) { tables.ICDF_PULSE_COUNT_SPLIT8_SAMPLE_PARTITIONS_OFFSETS } else if (size == 4) { tables.ICDF_PULSE_COUNT_SPLIT4_SAMPLE_PARTITIONS_OFFSETS } else { tables.ICDF_PULSE_COUNT_SPLIT2_SAMPLE_PARTITIONS_OFFSETS };
}

// The offset of the two-entry sign table in EXCITATION_SIGN_ICDFS.
function sign_at(signal: i32, high: boolean, count: i32): i32 {
  return (((signal - 1) * 2 + (if (high) { 1 } else { 0 })) * 7 + (if (count < 6) { count } else { 6 })) * 2;
}

function shell(own decoder: range.Decoder, counts: [i32]): (range.Decoder, i32[]) {
  let output = pcm.zeros(counts.len() * 16);
  for block in 0..counts.len() {
    let (a, left, right) = split(decoder, counts[block], 16);
    decoder = a;
    let halves = [left, right];
    for j in 0..2 {
      let (b, x, y) = split(decoder, halves[j], 8);
      decoder = b;
      let quarters = [x, y];
      for k in 0..2 {
        let (c, u, v) = split(decoder, quarters[k], 4);
        decoder = c;
        let pairs = [u, v];
        for m in 0..2 {
          let (d, p, q) = split(decoder, pairs[m], 2);
          decoder = d;
          let at = block * 16 + j * 8 + k * 4 + m * 2;
          output = output.with(at, p).with(at + 1, q);
        }
      }
    }
  }
  return (decoder, output);
}

function pulse_bits(own decoder: range.Decoder, own pulses: i32[], counts: [i32], shifts: [i32], signal: i32, high: boolean): (range.Decoder, i32[]) {
  for i in 0..pulses.len() {
    for _ in 0..shifts[i / 16] {
      let (next, bit) = decoder.icdf(tables.ICDF_EXCITATION_LSB, 8);
      decoder = next;
      pulses = pulses.with(i, pulses[i] << 1 | bit);
    }
  }
  for i in 0..pulses.len() {
    if (pulses[i] == 0) {
      continue;
    }
    let at = sign_at(signal, high, counts[i / 16]);
    let (next, sign) = decoder.icdf(tables.EXCITATION_SIGN_ICDFS[at:at + 2], 8);
    decoder = next;
    if (sign == 0) {
      pulses = pulses.with(i, -pulses[i]);
    }
  }
  return (decoder, pulses);
}

function excitation(pulses: [i32], signal: i32, high: boolean, own seed: i32): i32[] {
  let offset = if (signal == 3) { if (high) { 25 } else { 8 } } else { if (high) { 60 } else { 25 } };
  let output: i32[] = [];
  for pulse in pulses {
    let sign = if (pulse < 0) { -1 } else if (pulse > 0) { 1 } else { 0 };
    let value = (pulse << 8) - sign * 20 + offset;
    seed = math.silk_rand(seed);
    let mask = seed >> 31;
    output = output.append((value ^ mask) - mask);
    seed += pulse;
  }
  return output;
}

function voiced_indices(own decoder: range.Decoder, previous: History, rate: i32, duration: i32, independent: boolean, skip_scale: boolean): (range.Decoder, i32, i32, i32, i32[], i32) {
  let (a, lag) = pitch_lag(decoder, rate, independent, previous);
  let (b, contour) = a.icdf(contour_table(rate, duration), 8);
  let (c, periodicity) = b.icdf(tables.ICDF_PERIODICITY_INDEX, 8);
  decoder = c;
  let rows: i32[] = [];
  for _ in 0..duration / 5 {
    let (next, row) = decoder.icdf(ltp_table(periodicity), 8);
    decoder = next;
    rows = rows.append(row);
  }
  let scale = 0;
  if (independent && !skip_scale) {
    let (next, index) = decoder.icdf(tables.ICDF_LTP_SCALING_PARAMETER, 8);
    decoder = next;
    scale = index;
  }
  return (decoder, lag, contour, periodicity, rows, scale);
}

pub function valid_frame(rate: i32, duration: i32): boolean {
  return (rate == 8 || rate == 12 || rate == 16) && (duration == 10 || duration == 20);
}

function valid_history(previous: History): boolean {
  return previous.gain >= 0 && previous.gain <= 63;
}

function blocks(rate: i32, duration: i32): i32 {
  return (rate * duration + 15) / 16;
}

function absolute_gain(independent: boolean, previous: History): boolean {
  return independent || !previous.decoded;
}

function in_range(v: i32, lo: i32, hi: i32): boolean {
  return v >= lo && v <= hi;
}

// A lag that the frame can code: as a change from the previous lag, or as
// an absolute value.
function codable_lag(lag: i32, rate: i32, independent: boolean, previous: History): boolean {
  if (delta_lag(independent, previous) && in_range(lag - previous.lag, -8, 11)) {
    return true;
  }
  return in_range(lag - rate * 2, 0, tables.ICDF_PRIMARY_PITCH_LAG_HIGH_PART.len() * (rate / 2) - 1);
}

function unvoiced_indices(x: Indices): boolean {
  return x.lag == 0 && x.contour == 0 && x.periodicity == 0 && x.ltp.len() == 0 && x.ltp_scale == 0;
}

function valid_voicing(x: Indices, previous: History, rate: i32, duration: i32, independent: boolean): boolean {
  if (x.signal != 3) {
    return unvoiced_indices(x);
  }
  if (!codable_lag(x.lag, rate, independent, previous) || !in_range(x.contour, 0, contour_table(rate, duration).len() - 1)) {
    return false;
  }
  return in_range(x.periodicity, 0, 2) && x.ltp.len() == duration / 5 && in_range(x.ltp_scale, 0, 2) && valid_ltp(x.ltp, x.periodicity);
}

function valid_ltp(rows: [i32], periodicity: i32): boolean {
  for row in rows {
    if (!in_range(row, 0, ltp_table(periodicity).len() - 1)) {
      return false;
    }
  }
  return true;
}

function valid_gains(gains: [i32], absolute: boolean): boolean {
  for i in 0..gains.len() {
    let top = if (i == 0 && absolute) { 63 } else { tables.ICDF_DELTA_QUANTIZATION_GAIN.len() - 1 };
    if (!in_range(gains[i], 0, top)) {
      return false;
    }
  }
  return true;
}

function valid_lsf(x: Indices): boolean {
  if (!in_range(x.lsf_first, 0, tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_WIDEBAND_VOICED.len() - 1)) {
    return false;
  }
  let limit = 3 + tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX_EXTENSION.len();
  for v in x.lsf {
    if (!in_range(v, -limit, limit)) {
      return false;
    }
  }
  return true;
}

function valid_lengths(x: Indices, rate: i32, duration: i32): boolean {
  let order = if (rate == 16) { 16 } else { 10 };
  return in_range(x.signal, 1, 3) && x.gains.len() == duration / 5 && x.lsf.len() == order && x.pulses.len() == blocks(rate, duration) * 16;
}

function valid_indices(x: Indices, previous: History, rate: i32, duration: i32, independent: boolean): boolean {
  if (!valid_lengths(x, rate, duration)) {
    return false;
  }
  if (!valid_gains(x.gains, absolute_gain(independent, previous)) || !valid_lsf(x)) {
    return false;
  }
  let interpolation = if (duration == 20) { in_range(x.interpolation, 0, 4) } else { x.interpolation == 4 };
  return interpolation && in_range(x.seed, 0, 3) && valid_voicing(x, previous, rate, duration, independent);
}

// Reads the coded symbols of one frame.
pub function decode_indices(own decoder: range.Decoder, previous: History, rate: i32, duration: i32, active: boolean, independent: boolean, skip_scale: boolean): Result[(range.Decoder, Indices), pcm.AudioError] {
  if (!valid_frame(rate, duration)) {
    return Err(pcm.Header);
  }
  if (decoder.storage < 1) {
    return Err(pcm.Truncated);
  }
  if (!valid_history(previous)) {
    return Err(pcm.Header);
  }
  let wide = rate == 16;
  let (a, signal, high) = frame_type(decoder, active);
  let (b, gain_values) = gain_indices(a, signal, duration / 5, absolute_gain(independent, previous));
  let (c, first) = lsf_first(b, wide, signal == 3);
  let (d, lsf) = lsf_indices(c, wide, first);
  decoder = d;
  let interpolation = 4;
  if (duration == 20) {
    let (next, factor) = decoder.icdf(tables.ICDF_NORMALIZED_LSF_INTERPOLATION_INDEX, 8);
    decoder = next;
    interpolation = factor;
  }
  let lag = 0;
  let contour = 0;
  let periodicity = 0;
  let rows: i32[] = [];
  let scale = 0;
  if (signal == 3) {
    let (next, voiced_lag, row, period, filters, index) = voiced_indices(decoder, previous, rate, duration, independent, skip_scale);
    decoder = next;
    lag = voiced_lag;
    contour = row;
    periodicity = period;
    rows = filters;
    scale = index;
  }
  let (h, seed) = decoder.icdf(tables.ICDF_LINEAR_CONGRUENTIAL_GENERATOR_SEED, 8);
  let (i, counts, shifts) = pulse_counts(h, blocks(rate, duration), signal == 3);
  let (j, raw) = shell(i, counts);
  let (k, pulses) = pulse_bits(j, raw, counts, shifts, signal, high);
  if (k.error) {
    return Err(pcm.Header);
  }
  let indices = Indices {
    signal: signal, high_offset: high, gains: gain_values, lsf_first: first, lsf: lsf,
    interpolation: interpolation, lag: lag, contour: contour, periodicity: periodicity,
    ltp: rows, ltp_scale: scale, seed: seed, pulses: pulses,
  };
  return Ok((k, indices));
}

const LTP_SCALES: i32[] = [15565, 12288, 8192];

// The pitch lags, the LTP filters and the LTP scale of a frame.
function voiced_parameters(x: Indices, rate: i32, duration: i32): (i32[], i32[], i32) {
  if (x.signal != 3) {
    return ([], [], 0);
  }
  return (pitch(x.lag, x.contour, rate, duration), ltp(x.periodicity, x.ltp), LTP_SCALES[x.ltp_scale]);
}

// Gives the parameters of the frame that `indices` codes, and the history
// that the next frame of the channel codes against.
pub function dequantize(x: Indices, previous: History, rate: i32, duration: i32, independent: boolean): Result[(History, Control), pcm.AudioError] {
  if (!valid_frame(rate, duration) || !valid_history(previous) || !valid_indices(x, previous, rate, duration, independent)) {
    return Err(pcm.Header);
  }
  let wide = rate == 16;
  let (gain, frame_gains) = gains(x.gains, absolute_gain(independent, previous), previous.decoded, previous.gain);
  let lsf = lsf_coefficients(lsf_residuals(x.lsf, wide, x.lsf_first), wide, x.lsf_first)?;
  let (interpolation, interpolated) = lsf_interpolate(x.interpolation, previous.lsf, lsf, previous.decoded);
  let first_lpc = if (interpolated.len() == 0) { [] } else { predict.to_lpc(interpolated, wide)? };
  let lpc = predict.to_lpc(lsf, wide)?;
  let (pitch_values, filters, scale) = voiced_parameters(x, rate, duration);
  let control = Control {
    signal: x.signal, high_offset: x.high_offset, gains: frame_gains, lsf: lsf,
    first_lpc: first_lpc, lpc: lpc, interpolation: interpolation,
    pitch: pitch_values, ltp: filters, ltp_scale: scale,
    seed: x.seed, pulses: x.pulses, excitation: excitation(x.pulses, x.signal, x.high_offset, x.seed),
  };
  let next_history = History { gain: gain, lag: x.lag, voiced: x.signal == 3, decoded: true, lsf: lsf };
  return Ok((next_history, control));
}

pub function decode(own decoder: range.Decoder, previous: History, rate: i32, duration: i32, active: boolean, independent: boolean, skip_scale: boolean): Result[(range.Decoder, History, Control), pcm.AudioError] {
  let (next, indices) = decode_indices(decoder, previous, rate, duration, active, independent, skip_scale)?;
  let (history, control) = dequantize(indices, previous, rate, duration, independent)?;
  return Ok((next, history, control));
}

// The partitions of the 16 magnitudes at `at` hold at most the sums of
// MAX_PULSES.
function fits(magnitudes: [i32], at: i32): boolean {
  for level in 0..4 {
    let size = 2 << level;
    let start = 0;
    while (start < 16) {
      let sum = 0;
      for k in 0..size {
        sum += magnitudes[at + start + k];
      }
      if (sum > MAX_PULSES[level]) {
        return false;
      }
      start += size;
    }
  }
  return true;
}

// The magnitudes after the shifts of each block, the shifts, and the sums.
// The shifts are -1 when a block needs more than MAX_SHIFTS.
function shift_blocks(pulses: [i32]): (i32[], i32[], i32[]) {
  let magnitudes: i32[] = [];
  for p in pulses {
    magnitudes = magnitudes.append(if (p < 0) { -p } else { p });
  }
  let shifts: i32[] = [];
  let sums: i32[] = [];
  for block in 0..pulses.len() / 16 {
    let at = block * 16;
    let shift = 0;
    while (!fits(magnitudes, at) && shift <= MAX_SHIFTS) {
      for k in 0..16 {
        magnitudes = magnitudes.with(at + k, magnitudes[at + k] >> 1);
      }
      shift += 1;
    }
    let sum = 0;
    for k in 0..16 {
      sum += magnitudes[at + k];
    }
    shifts = shifts.append(if (shift > MAX_SHIFTS) { -1 } else { shift });
    sums = sums.append(sum);
  }
  return (magnitudes, shifts, sums);
}

// The rate level with the fewest bits for the pulse counts, as libopus
// chooses it.
function rate_level(shifts: [i32], sums: [i32], voiced: boolean): i32 {
  let best = 0;
  let fewest = math.MAX32;
  for level in 0..RATE_LEVELS {
    let bits = RATE_LEVELS_BITS_Q5[(if (voiced) { RATE_LEVELS } else { 0 }) + level];
    for i in 0..sums.len() {
      bits += PULSES_PER_BLOCK_BITS_Q5[level * (PULSE_ESCAPE + 1) + (if (shifts[i] > 0) { PULSE_ESCAPE } else { sums[i] })];
    }
    if (bits < fewest) {
      fewest = bits;
      best = level;
    }
  }
  return best;
}

function count_symbol(own e: range.Encoder, s: i32, row: i32): range.Encoder {
  let offsets = tables.ICDF_PULSE_COUNT_OFFSETS;
  return e.icdf(s, tables.ICDF_PULSE_COUNT[offsets[row]:offsets[row + 1]], 8);
}

function encode_split(own e: range.Encoder, left: i32, count: i32, size: i32): range.Encoder {
  if (count == 0) {
    return e;
  }
  let offsets = split_offsets(size);
  return e.icdf(left, split_table(size)[offsets[count - 1]:offsets[count]], 8);
}

function sum_of(values: [i32], at: i32, size: i32): i32 {
  let sum = 0;
  for k in 0..size {
    sum += values[at + k];
  }
  return sum;
}

// Codes the splits of one block in the order that `shell` reads them.
function encode_shell(own e: range.Encoder, magnitudes: [i32], at: i32, size: i32): range.Encoder {
  if (size == 1) {
    return e;
  }
  let half = size / 2;
  e = encode_split(e, sum_of(magnitudes, at, half), sum_of(magnitudes, at, size), size);
  e = encode_shell(e, magnitudes, at, half);
  return encode_shell(e, magnitudes, at + half, half);
}

function encode_pulses(own e: range.Encoder, pulses: [i32], signal: i32, high: boolean): Result[range.Encoder, pcm.AudioError] {
  let (magnitudes, shifts, sums) = shift_blocks(pulses);
  for shift in shifts {
    if (shift < 0) {
      return Err(pcm.Header);
    }
  }
  e = encode_counts(e, shifts, sums, signal == 3);
  for i in 0..sums.len() {
    e = encode_shell(e, magnitudes, i * 16, 16);
  }
  return Ok(encode_bits(e, pulses, shifts, sums, signal, high));
}

function encode_counts(own e: range.Encoder, shifts: [i32], sums: [i32], voiced: boolean): range.Encoder {
  let level = rate_level(shifts, sums, voiced);
  e = e.icdf(level, if (voiced) { tables.ICDF_RATE_LEVEL_VOICED } else { tables.ICDF_RATE_LEVEL_UNVOICED }, 8);
  for i in 0..sums.len() {
    if (shifts[i] == 0) {
      e = count_symbol(e, sums[i], level);
      continue;
    }
    e = count_symbol(e, PULSE_ESCAPE, level);
    for k in 1..shifts[i] {
      e = count_symbol(e, PULSE_ESCAPE, escape_row(k));
    }
    e = count_symbol(e, sums[i], escape_row(shifts[i]));
  }
  return e;
}

// Codes the bits that the shifts removed, and then the signs.
function encode_bits(own e: range.Encoder, pulses: [i32], shifts: [i32], sums: [i32], signal: i32, high: boolean): range.Encoder {
  for i in 0..pulses.len() {
    let magnitude = if (pulses[i] < 0) { -pulses[i] } else { pulses[i] };
    let bit = shifts[i / 16] - 1;
    while (bit >= 0) {
      e = e.icdf(magnitude >> bit & 1, tables.ICDF_EXCITATION_LSB, 8);
      bit -= 1;
    }
  }
  for i in 0..pulses.len() {
    if (pulses[i] != 0) {
      let at = sign_at(signal, high, sums[i / 16]);
      e = e.icdf(if (pulses[i] > 0) { 1 } else { 0 }, tables.EXCITATION_SIGN_ICDFS[at:at + 2], 8);
    }
  }
  return e;
}

function encode_lag(own e: range.Encoder, lag: i32, rate: i32, independent: boolean, previous: History): range.Encoder {
  if (delta_lag(independent, previous)) {
    let delta = lag - previous.lag;
    if (in_range(delta, -8, 11)) {
      return e.icdf(delta + 9, tables.ICDF_PRIMARY_PITCH_LAG_CHANGE, 8);
    }
    e = e.icdf(0, tables.ICDF_PRIMARY_PITCH_LAG_CHANGE, 8);
  }
  let index = lag - rate * 2;
  let high = index / (rate / 2);
  e = e.icdf(high, tables.ICDF_PRIMARY_PITCH_LAG_HIGH_PART, 8);
  return e.icdf(index - high * (rate / 2), lag_low_table(rate), 8);
}

function encode_lsf(own e: range.Encoder, x: Indices, wide: boolean): range.Encoder {
  let voiced = x.signal == 3;
  let first_table = if (wide) {
    if (voiced) { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_WIDEBAND_VOICED } else { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_WIDEBAND_UNVOICED }
  } else {
    if (voiced) { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_NARROWBAND_OR_MEDIUMBAND_VOICED } else { tables.ICDF_NORMALIZED_LSF_STAGE_ONE_INDEX_NARROWBAND_OR_MEDIUMBAND_UNVOICED }
  };
  e = e.icdf(x.lsf_first, first_table, 8);
  let order = x.lsf.len();
  let codebook = if (wide) { tables.CODEBOOK_NORMALIZED_LSF_STAGE_TWO_INDEX_WIDEBAND } else { tables.CODEBOOK_NORMALIZED_LSF_STAGE_TWO_INDEX_NARROWBAND_OR_MEDIUMBAND };
  let offsets = tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX_OFFSETS;
  for i in 0..order {
    let row = codebook[x.lsf_first * order + i] as i32;
    let index = x.lsf[i];
    e = e.icdf(math.clamp(-4, index, 4) + 4, tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX[offsets[row]:offsets[row + 1]], 8);
    if (index >= 4) {
      e = e.icdf(index - 4, tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX_EXTENSION, 8);
    } else if (index <= -4) {
      e = e.icdf(-index - 4, tables.ICDF_NORMALIZED_LSF_STAGE_TWO_INDEX_EXTENSION, 8);
    }
  }
  return e;
}

function encode_type(own e: range.Encoder, x: Indices): range.Encoder {
  let offset = if (x.high_offset) { 1 } else { 0 };
  if (x.signal == 1) {
    return e.icdf(offset, tables.ICDF_FRAME_TYPE_VAD_INACTIVE, 8);
  }
  return e.icdf((x.signal - 2) * 2 + offset, tables.ICDF_FRAME_TYPE_VAD_ACTIVE, 8);
}

function encode_gains(own e: range.Encoder, x: Indices, absolute: boolean): range.Encoder {
  for i in 0..x.gains.len() {
    if (i == 0 && absolute) {
      e = e.icdf(x.gains[0] >> 3, gain_msb(x.signal), 8);
      e = e.icdf(x.gains[0] & 7, tables.ICDF_INDEPENDENT_QUANTIZATION_GAIN_LSB, 8);
    } else {
      e = e.icdf(x.gains[i], tables.ICDF_DELTA_QUANTIZATION_GAIN, 8);
    }
  }
  return e;
}

function encode_voiced(own e: range.Encoder, x: Indices, previous: History, rate: i32, duration: i32, independent: boolean, scaled: boolean): range.Encoder {
  e = encode_lag(e, x.lag, rate, independent, previous);
  e = e.icdf(x.contour, contour_table(rate, duration), 8);
  e = e.icdf(x.periodicity, tables.ICDF_PERIODICITY_INDEX, 8);
  for row in x.ltp {
    e = e.icdf(row, ltp_table(x.periodicity), 8);
  }
  if (scaled) {
    e = e.icdf(x.ltp_scale, tables.ICDF_LTP_SCALING_PARAMETER, 8);
  }
  return e;
}

// Writes the coded symbols of one frame. An inactive frame codes its type
// with the table of the inactive voice activity flag, and each other frame
// with the table of the active flag.
pub function encode(own e: range.Encoder, x: Indices, previous: History, rate: i32, duration: i32, independent: boolean, skip_scale: boolean): Result[range.Encoder, pcm.AudioError] {
  if (!valid_frame(rate, duration) || !valid_history(previous) || !valid_indices(x, previous, rate, duration, independent)) {
    return Err(pcm.Header);
  }
  let voiced = x.signal == 3;
  let scaled = independent && !skip_scale;
  if (voiced && !scaled && x.ltp_scale != 0) {
    return Err(pcm.Header);
  }
  e = encode_gains(encode_type(e, x), x, absolute_gain(independent, previous));
  e = encode_lsf(e, x, rate == 16);
  if (duration == 20) {
    e = e.icdf(x.interpolation, tables.ICDF_NORMALIZED_LSF_INTERPOLATION_INDEX, 8);
  }
  if (voiced) {
    e = encode_voiced(e, x, previous, rate, duration, independent, scaled);
  }
  e = e.icdf(x.seed, tables.ICDF_LINEAR_CONGRUENTIAL_GENERATOR_SEED, 8);
  return encode_pulses(e, x.pulses, x.signal, x.high_offset);
}
```
