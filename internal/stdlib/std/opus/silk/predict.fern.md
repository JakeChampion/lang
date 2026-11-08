# SILK prediction filters

This module stabilizes normalized line spectral frequencies and reconstructs the SILK prediction filters.
It retains the reference fixed-point arithmetic and bandwidth expansion order.
Narrowband and mediumband use ten coefficients. Wideband uses sixteen coefficients.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK spectral frequency stabilization and prediction filter reconstruction.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/silk/math";
import "std/opus/silk/tables";

function zeros(count: i32): i32[] {
  let values: i32[] = [];
  for _ in 0..count {
    values = values.append(0);
  }
  return values;
}

// The bounded adjustment loop follows the reference minimum-gap repair order.
// fern-lint: allow cyclomatic-complexity
pub function stabilize(own values: i32[], wideband: boolean): Result[i32[], pcm.AudioError] {
  let order = if (wideband) { 16 } else { 10 };
  if (values.len() != order) {
    return Err(pcm.Header);
  }
  for value in values {
    if (value < -32768 || value > 32767) {
      return Err(pcm.Header);
    }
  }
  let gaps = if (wideband) { tables.CODEBOOK_MINIMUM_SPACING_FOR_NORMALIZED_LS_COEFFICIENTS_WIDEBAND } else { tables.CODEBOOK_MINIMUM_SPACING_FOR_NORMALIZED_LS_COEFFICIENTS_NARROWBAND_AND_MEDIUMBAND };
  for _ in 0..20 {
    let position = 0;
    let difference = math.MAX32;
    for i in 0..order + 1 {
      let previous = if (i == 0) { 0 } else { values[i - 1] };
      let current = if (i == order) { 32768 } else { values[i] };
      let gap = current - previous - gaps[i];
      if (gap < difference) {
        difference = gap;
        position = i;
      }
    }
    if (difference >= 0) {
      return Ok(values);
    }
    if (position == 0) {
      values = values.with(0, gaps[0]);
      continue;
    }
    if (position == order) {
      values = values.with(order - 1, 32768 - gaps[order]);
      continue;
    }
    let minimum = gaps[position] >> 1;
    for i in 0..position {
      minimum += gaps[i];
    }
    let maximum = 32768 - (gaps[position] >> 1);
    for i in position + 1..order + 1 {
      maximum -= gaps[i];
    }
    let center = math.clamp(minimum, values[position - 1] + values[position] + 1 >> 1, maximum);
    values = values.with(position - 1, center - (gaps[position] >> 1));
    values = values.with(position, values[position - 1] + gaps[position]);
  }
  for i in 1..order {
    let value = values[i];
    let at = i;
    while (at > 0 && value < values[at - 1]) {
      values = values.with(at, values[at - 1]);
      at -= 1;
    }
    values = values.with(at, value);
  }
  for i in 0..order {
    let previous = if (i == 0) { 0 } else { values[i - 1] };
    let minimum = math.sat16(previous + gaps[i]);
    if (values[i] < minimum) {
      values = values.with(i, minimum);
    }
  }
  let i = order - 1;
  while (i >= 0) {
    let next = if (i == order - 1) { 32768 } else { values[i + 1] };
    let maximum = next - gaps[i + 1];
    if (values[i] > maximum) {
      values = values.with(i, maximum);
    }
    i -= 1;
  }
  return Ok(values);
}

function initial_coefficients(coefficients: [i32]): i32[] {
  let order = coefficients.len();
  if (order < 1 || order > 16) {
    return [];
  }
  let current = zeros(16);
  let dc = 0;
  for i in 0..order {
    if (coefficients[i] < -32768 || coefficients[i] > 32767) {
      return [];
    }
    dc += coefficients[i];
    current = current.with(i, coefficients[i] << 12);
  }
  if (dc >= 4096) {
    return [];
  }
  return current;
}

function inverse_step(previous: [i32], own current: i32[], reflection: i32, reciprocal: i32, scale_bits: i32, count: i32): i32[] {
  for n in 0..count {
    let correction = math.rshift_round64(previous[count - n - 1] as i64 * (reflection as i64), 31) as i32;
    let numerator = math.sub_sat32(previous[n], correction);
    let value = math.rshift_round64(numerator as i64 * (reciprocal as i64), scale_bits);
    if (value > math.MAX32 as i64 || value < math.MIN32 as i64) {
      return [];
    }
    current = current.with(n, value as i32);
  }
  return current;
}

pub function inverse_gain(coefficients: [i32]): i32 {
  let current = initial_coefficients(coefficients);
  if (current.len() == 0) {
    return 0;
  }
  let previous = zeros(16);
  let inverse_q30 = 1 << 30;
  let index = coefficients.len() - 1;
  while (index > 0) {
    if (current[index] > 16773022 || current[index] < -16773022) {
      return 0;
    }
    let reflection_q31 = -(current[index] << 7);
    let scale_q30 = (1 << 30) - math.smmul(reflection_q31, reflection_q31);
    let scale_bits = 32 - math.clz32(math.abs32(scale_q30));
    let reciprocal = math.inverse32_varq(scale_q30, scale_bits + 30);
    inverse_q30 = math.smmul(inverse_q30, scale_q30) << 2;
    let temporary = previous;
    previous = current;
    current = inverse_step(previous, temporary, reflection_q31, reciprocal, scale_bits, index);
    if (current.len() == 0) {
      return 0;
    }
    index -= 1;
  }
  if (current[0] > 16773022 || current[0] < -16773022) {
    return 0;
  }
  let reflection_q31 = -(current[0] << 7);
  return math.smmul(inverse_q30, (1 << 30) - math.smmul(reflection_q31, reflection_q31)) << 2;
}

function limit_range(own coefficients: i32[]): i32[] {
  for _ in 0..10 {
    let maximum = 0;
    let position = 0;
    for i in 0..coefficients.len() {
      let value = math.abs32(coefficients[i]);
      if (value > maximum) {
        maximum = value;
        position = i;
      }
    }
    let q12 = maximum + 16 >> 5;
    if (q12 > 163838) {
      q12 = 163838;
    }
    if (q12 <= 32767) {
      return coefficients;
    }
    let chirp = 65470 - (q12 - 32767 << 14) / (q12 * (position + 1) >> 2);
    coefficients = math.expand_lpc(coefficients, chirp);
  }
  for i in 0..coefficients.len() {
    coefficients = coefficients.with(i, math.clamp(-32768, coefficients[i] + 16 >> 5, 32767) << 5);
  }
  return coefficients;
}

function quantize(coefficients: [i32]): i32[] {
  let output: i32[] = [];
  for value in coefficients {
    output = output.append(math.s16(value + 16 >> 5));
  }
  return output;
}

function cosines(normalized: [i32], wideband: boolean): Result[i32[], pcm.AudioError] {
  let order = if (wideband) { 16 } else { 10 };
  if (normalized.len() != order) {
    return Err(pcm.Header);
  }
  let ordering = if (wideband) { tables.LSF_ORDERING_FOR_POLYNOMIAL_EVALUATION_WIDEBAND } else { tables.LSF_ORDERING_FOR_POLYNOMIAL_EVALUATION_NARROWBAND_AND_MEDIUMBAND };
  let cosine = zeros(order);
  for k in 0..order {
    let value = normalized[k];
    if (value < 0 || value > 32767) {
      return Err(pcm.Header);
    }
    let index = value >> 8;
    let fraction = value & 255;
    let a = tables.Q12_COSINE_TABLE_FOR_LSF_CONVERION[index];
    let b = tables.Q12_COSINE_TABLE_FOR_LSF_CONVERION[index + 1];
    cosine = cosine.with(ordering[k] as i32, a * 256 + (b - a) * fraction + 4 >> 3);
  }
  return Ok(cosine);
}

pub function to_lpc(normalized: [i32], wideband: boolean): Result[i32[], pcm.AudioError] {
  let cosine = match (cosines(normalized, wideband)) {
    Ok(values) => values,
    Err(error) => { return Err(error); },
  };
  let order = cosine.len();
  let half = order / 2;
  let p = zeros(half + 1).with(0, 65536).with(1, -cosine[0]);
  let q = zeros(half + 1).with(0, 65536).with(1, -cosine[1]);
  for k in 1..half {
    p = p.with(k + 1, p[k - 1] * 2 - (cosine[2 * k] as i64 * (p[k] as i64) + 32768i64 >> 16) as i32);
    q = q.with(k + 1, q[k - 1] * 2 - (cosine[2 * k + 1] as i64 * (q[k] as i64) + 32768i64 >> 16) as i32);
    let j = k;
    while (j > 1) {
      p = p.with(j, p[j] + p[j - 2] - (cosine[2 * k] as i64 * (p[j - 1] as i64) + 32768i64 >> 16) as i32);
      q = q.with(j, q[j] + q[j - 2] - (cosine[2 * k + 1] as i64 * (q[j - 1] as i64) + 32768i64 >> 16) as i32);
      j -= 1;
    }
    p = p.with(1, p[1] - cosine[2 * k]);
    q = q.with(1, q[1] - cosine[2 * k + 1]);
  }
  let coefficients = zeros(order);
  for k in 0..half {
    coefficients = coefficients.with(k, -(q[k + 1] - q[k]) - (p[k + 1] + p[k]));
    coefficients = coefficients.with(order - k - 1, q[k + 1] - q[k] - (p[k + 1] + p[k]));
  }
  coefficients = limit_range(coefficients);
  let result = quantize(coefficients);
  for i in 0..16 {
    if (inverse_gain(result) >= 107374) {
      break;
    }
    coefficients = math.expand_lpc(coefficients, 65536 - (2 << i));
    result = quantize(coefficients);
  }
  return Ok(result);
}
```
