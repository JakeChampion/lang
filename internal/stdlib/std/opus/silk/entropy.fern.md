# SILK frame parameters

This module decodes SILK frame parameters with the shared Opus range decoder.
The sample rate is 8, 12, or 16 kHz. An internal frame lasts 10 or 20 milliseconds.
The caller reads packet activity flags and redundant frames before the primary frames.
The caller retains separate prediction histories for the two stereo channels.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK entropy decoding for one internal frame and one coded channel.
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

function gains(own decoder: range.Decoder, signal: i32, count: i32, independent: boolean, decoded: boolean, own previous: i32): (range.Decoder, i32, i32[]) {
  let output: i32[] = [];
  for i in 0..count {
    if (i == 0 && (independent || !decoded)) {
      let (next, msb) = decoder.icdf(gain_msb(signal), 8);
      let (last, lsb) = next.icdf(tables.ICDF_INDEPENDENT_QUANTIZATION_GAIN_LSB, 8);
      decoder = last;
      let gain = msb << 3 | lsb;
      previous = if (decoded) { math.clamp(gain, previous - 16, math.MAX32) } else { gain };
    } else {
      let (next, delta) = decoder.icdf(tables.ICDF_DELTA_QUANTIZATION_GAIN, 8);
      decoder = next;
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
  return (decoder, previous, output);
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

function lsf_residuals(own decoder: range.Decoder, wide: boolean, first: i32): (range.Decoder, i32[]) {
  let (next, indices) = lsf_indices(decoder, wide, first);
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
  return (next, residual);
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

function lsf_interpolate(own decoder: range.Decoder, previous: [i32], current: [i32], decoded: boolean, duration: i32): (range.Decoder, i32, i32[]) {
  if (duration != 20) {
    return (decoder, 4, []);
  }
  let (next, factor) = decoder.icdf(tables.ICDF_NORMALIZED_LSF_INTERPOLATION_INDEX, 8);
  if (factor == 4 || !decoded || previous.len() != current.len()) {
    return (next, factor, []);
  }
  let output: i32[] = [];
  for i in 0..current.len() {
    output = output.append(math.s16(previous[i] + (factor * (current[i] - previous[i]) >> 2)));
  }
  return (next, factor, output);
}

function contour(own decoder: range.Decoder, rate: i32, duration: i32): (range.Decoder, i32) {
  if (rate == 8) {
    return decoder.icdf(if (duration == 10) { tables.ICDF_SUBFRAME_PITCH_CONTOUR_NARROWBAND10_MS } else { tables.ICDF_SUBFRAME_PITCH_CONTOUR_NARROWBAND20_MS }, 8);
  }
  return decoder.icdf(if (duration == 10) { tables.ICDF_SUBFRAME_PITCH_CONTOUR_MEDIUMBAND_OR_WIDEBAND10_MS } else { tables.ICDF_SUBFRAME_PITCH_CONTOUR_MEDIUMBAND_OR_WIDEBAND20_MS }, 8);
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

function pitch(own decoder: range.Decoder, rate: i32, duration: i32, independent: boolean, previous: History): (range.Decoder, i32, i32[]) {
  let absolute = independent || !previous.voiced;
  let lag = previous.lag;
  if (!absolute) {
    let (next, delta) = decoder.icdf(tables.ICDF_PRIMARY_PITCH_LAG_CHANGE, 8);
    decoder = next;
    absolute = delta == 0;
    lag += delta - 9;
  }
  if (absolute) {
    let low_table = if (rate == 8) { tables.ICDF_PRIMARY_PITCH_LAG_LOW_PART_NARROWBAND } else if (rate == 12) { tables.ICDF_PRIMARY_PITCH_LAG_LOW_PART_MEDIUMBAND } else { tables.ICDF_PRIMARY_PITCH_LAG_LOW_PART_WIDEBAND };
    let (next, high) = decoder.icdf(tables.ICDF_PRIMARY_PITCH_LAG_HIGH_PART, 8);
    let (last, low) = next.icdf(low_table, 8);
    decoder = last;
    lag = high * (rate / 2) + low + rate * 2;
  }
  let (next, row) = contour(decoder, rate, duration);
  let output: i32[] = [];
  let count = duration / 5;
  for i in 0..count {
    output = output.append(math.clamp(rate * 2, lag + contour_value(rate, duration, row * count + i), rate * 18));
  }
  return (next, lag, output);
}

function ltp(own decoder: range.Decoder, count: i32): (range.Decoder, i32[]) {
  let (next, periodicity) = decoder.icdf(tables.ICDF_PERIODICITY_INDEX, 8);
  decoder = next;
  let indices = if (periodicity == 0) { tables.ICDF_LTP_FILTER_INDEX0 } else if (periodicity == 1) { tables.ICDF_LTP_FILTER_INDEX1 } else { tables.ICDF_LTP_FILTER_INDEX2 };
  let codebook = if (periodicity == 0) { tables.CODEBOOK_LTP_FILTER_PERIODICITY_INDEX0 } else if (periodicity == 1) { tables.CODEBOOK_LTP_FILTER_PERIODICITY_INDEX1 } else { tables.CODEBOOK_LTP_FILTER_PERIODICITY_INDEX2 };
  let output: i32[] = [];
  for _ in 0..count {
    let (last, index) = decoder.icdf(indices, 8);
    decoder = last;
    for i in 0..5 {
      output = output.append(codebook[index * 5 + i]);
    }
  }
  return (decoder, output);
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
    while (count == 17 && bits < 10) {
      let (more, escaped) = symbol(decoder, tables.ICDF_PULSE_COUNT, tables.ICDF_PULSE_COUNT_OFFSETS, 9);
      decoder = more;
      count = escaped;
      bits += 1;
    }
    if (bits == 10) {
      let (more, escaped) = symbol(decoder, tables.ICDF_PULSE_COUNT, tables.ICDF_PULSE_COUNT_OFFSETS, 10);
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
  let table = if (size == 16) { tables.ICDF_PULSE_COUNT_SPLIT16_SAMPLE_PARTITIONS } else if (size == 8) { tables.ICDF_PULSE_COUNT_SPLIT8_SAMPLE_PARTITIONS } else if (size == 4) { tables.ICDF_PULSE_COUNT_SPLIT4_SAMPLE_PARTITIONS } else { tables.ICDF_PULSE_COUNT_SPLIT2_SAMPLE_PARTITIONS };
  let offsets = if (size == 16) { tables.ICDF_PULSE_COUNT_SPLIT16_SAMPLE_PARTITIONS_OFFSETS } else if (size == 8) { tables.ICDF_PULSE_COUNT_SPLIT8_SAMPLE_PARTITIONS_OFFSETS } else if (size == 4) { tables.ICDF_PULSE_COUNT_SPLIT4_SAMPLE_PARTITIONS_OFFSETS } else { tables.ICDF_PULSE_COUNT_SPLIT2_SAMPLE_PARTITIONS_OFFSETS };
  let (next, left) = symbol(decoder, table, offsets, count - 1);
  return (next, left, count - left);
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
  let group = ((signal - 1) * 2 + (if (high) { 1 } else { 0 })) * 7;
  for i in 0..pulses.len() {
    if (pulses[i] == 0) {
      continue;
    }
    let count = counts[i / 16];
    let at = (group + (if (count < 6) { count } else { 6 })) * 2;
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

function voiced_parameters(own decoder: range.Decoder, signal: i32, previous: History, rate: i32, duration: i32, independent: boolean, skip_scale: boolean): (range.Decoder, i32, i32[], i32[], i32) {
  let lag = 0;
  let pitch_values: i32[] = [];
  let filters: i32[] = [];
  let scale = 0;
  if (signal == 3) {
    let (f, base, values) = pitch(decoder, rate, duration, independent, previous);
    let (g, coefficients) = ltp(f, duration / 5);
    decoder = g;
    lag = base;
    pitch_values = values;
    filters = coefficients;
    scale = 15565;
    if (independent && !skip_scale) {
      let (h, index) = decoder.icdf(tables.ICDF_LTP_SCALING_PARAMETER, 8);
      decoder = h;
      scale = if (index == 0) { 15565 } else if (index == 1) { 12288 } else { 8192 };
    }
  }
  return (decoder, lag, pitch_values, filters, scale);
}

pub function valid_frame(rate: i32, duration: i32): boolean {
  return (rate == 8 || rate == 12 || rate == 16) && (duration == 10 || duration == 20);
}

function valid_history(previous: History): boolean {
  return previous.gain >= 0 && previous.gain <= 63;
}

pub function decode(own decoder: range.Decoder, previous: History, rate: i32, duration: i32, active: boolean, independent: boolean, skip_scale: boolean): Result[(range.Decoder, History, Control), pcm.AudioError] {
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
  let (b, gain, frame_gains) = gains(a, signal, duration / 5, independent, previous.decoded, previous.gain);
  let (c, first) = lsf_first(b, wide, signal == 3);
  let (d, residual) = lsf_residuals(c, wide, first);
  let lsf = lsf_coefficients(residual, wide, first)?;
  let (e, interpolation, interpolated) = lsf_interpolate(d, previous.lsf, lsf, previous.decoded, duration);
  let first_lpc = if (interpolated.len() == 0) { [] } else { predict.to_lpc(interpolated, wide)? };
  let lpc = predict.to_lpc(lsf, wide)?;
  let (g, lag, pitch_values, filters, scale) = voiced_parameters(e, signal, previous, rate, duration, independent, skip_scale);
  let (h, seed) = g.icdf(tables.ICDF_LINEAR_CONGRUENTIAL_GENERATOR_SEED, 8);
  let (i, counts, shifts) = pulse_counts(h, (rate * duration + 15) / 16, signal == 3);
  let (j, raw) = shell(i, counts);
  let (k, pulses) = pulse_bits(j, raw, counts, shifts, signal, high);
  if (k.error) {
    return Err(pcm.Header);
  }
  let control = Control {
    signal: signal, high_offset: high, gains: frame_gains, lsf: lsf,
    first_lpc: first_lpc, lpc: lpc, interpolation: interpolation, pitch: pitch_values,
    ltp: filters, ltp_scale: scale, seed: seed, pulses: pulses, excitation: excitation(pulses, signal, high, seed),
  };
  let next_history = History { gain: gain, lag: lag, voiced: signal == 3, decoded: true, lsf: lsf };
  return Ok((k, next_history, control));
}
```
