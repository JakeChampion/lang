# SILK fixed-point arithmetic

This module supplies the integer arithmetic for the SILK decoder.
It preserves signed narrowing, saturation, and the reference rounding order.
Callers supply valid shift counts and nonzero divisors.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and the SILK reference in RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK fixed-point arithmetic with reference rounding and saturation.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

pub const MIN32: i32 = -2147483647 - 1;

pub const MAX32: i32 = 2147483647;

pub function s16(x: i32): i32 {
  return x << 16 >> 16;
}

pub function clamp(lo: i32, x: i32, hi: i32): i32 {
  return if (x < lo) { lo } else if (x > hi) { hi } else { x };
}

pub function sat16(x: i32): i32 {
  return clamp(-32768, x, 32767);
}

pub function abs32(x: i32): i32 {
  return if (x < 0) { -x } else { x };
}

pub function clz32(x: i32): i32 {
  return __clz32(x as u32);
}

pub function ilog(x: i32): i32 {
  return if (x <= 0) { 0 } else { 32 - clz32(x) };
}

pub function rshift_round32(x: i32, shift: i32): i32 {
  return if (shift == 1) { (x >> 1) + (x & 1) } else { (x >> shift - 1) + 1 >> 1 };
}

pub function rshift_round64(x: i64, shift: i32): i64 {
  return if (shift == 1) { (x >> 1) + (x & 1i64) } else { (x >> (shift - 1) as i64) + 1i64 >> 1 };
}

pub function smmul(a: i32, b: i32): i32 {
  return (a as i64 * (b as i64) >> 32) as i32;
}

pub function smulbb(a: i32, b: i32): i32 {
  return s16(a) * s16(b);
}

pub function smulwb(a: i32, b: i32): i32 {
  return (a as i64 * (s16(b) as i64) >> 16) as i32;
}

pub function smulww(a: i32, b: i32): i32 {
  return (a as i64 * (b as i64) >> 16) as i32;
}

pub function smulwt(a: i32, b: i32): i32 {
  return (a as i64 * ((b >> 16) as i64) >> 16) as i32;
}

pub function smlabb(a: i32, b: i32, c: i32): i32 {
  return a + smulbb(b, c);
}

pub function smlawb(a: i32, b: i32, c: i32): i32 {
  return a + smulwb(b, c);
}

pub function smlawt(a: i32, b: i32, c: i32): i32 {
  return a + smulwt(b, c);
}

pub function smla_ww(a: i32, b: i32, c: i32): i32 {
  return a + smulwb(b, c) + b * rshift_round32(c, 16);
}

pub function add_sat32(a: i32, b: i32): i32 {
  let sum = a as i64 + b as i64;
  return if (sum > MAX32 as i64) { MAX32 } else if (sum < MIN32 as i64) { MIN32 } else { sum as i32 };
}

pub function sub_sat32(a: i32, b: i32): i32 {
  let difference = a as i64 - b as i64;
  return if (difference > MAX32 as i64) { MAX32 } else if (difference < MIN32 as i64) { MIN32 } else { difference as i32 };
}

pub function lshift_sat32(a: i32, shift: i32): i32 {
  return clamp(MIN32 >> shift, a, MAX32 >> shift) << shift;
}

pub function add_pos_sat32(a: i32, b: i32): i32 {
  let sum = a as u32 + b as u32;
  return if ((sum & 2147483648u32) != 0u32) { MAX32 } else { sum as i32 };
}

pub function silk_rand(seed: i32): i32 {
  return (907633515u32 + seed as u32 * 196314165u32) as i32;
}

pub function inverse32_varq(b: i32, qres: i32): i32 {
  let headroom = clz32(abs32(b)) - 1;
  let normalized = b << headroom;
  let inverse = (MAX32 >> 2) / (normalized >> 16);
  let result = inverse << 16;
  let error_q32 = (1 << 29) - smulwb(normalized, inverse) << 3;
  result = smla_ww(result, error_q32, inverse);
  let shift = 61 - headroom - qres;
  if (shift <= 0) {
    let value = result as i64 << (-shift) as i64;
    return if (value > MAX32 as i64) { MAX32 } else if (value < MIN32 as i64) { MIN32 } else { value as i32 };
  }
  return if (shift < 32) { result >> shift } else { 0 };
}

pub function div32_varq(a: i32, b: i32, qres: i32): i32 {
  let a_headroom = clz32(abs32(a)) - 1;
  let a_normalized = a << a_headroom;
  let b_headroom = clz32(abs32(b)) - 1;
  let b_normalized = b << b_headroom;
  let inverse = (MAX32 >> 2) / (b_normalized >> 16);
  let result = smulwb(a_normalized, inverse);
  a_normalized -= smmul(b_normalized, result) << 3;
  result = smlawb(result, a_normalized, inverse);
  let shift = 29 + a_headroom - b_headroom - qres;
  if (shift < 0) {
    return lshift_sat32(result, -shift);
  }
  return if (shift < 32) { result >> shift } else { 0 };
}

pub function clz_frac(x: i32): (i32, i32) {
  let leading = clz32(x);
  let rotation = 24 - leading & 31;
  let value = x as u32;
  let rotated = if (rotation == 0) { value } else { value >> rotation as u32 | value << (32 - rotation) as u32 };
  return (leading, (rotated & 127u32) as i32);
}

pub function sqrt_approx(x: i32): i32 {
  if (x <= 0) {
    return 0;
  }
  let (leading, fraction) = clz_frac(x);
  let y = if ((leading & 1) != 0) { 32768 } else { 46214 };
  y >>= leading >> 1;
  return smlawb(y, y, smulbb(213, fraction));
}

pub function lin2log(x: i32): i32 {
  let (leading, fraction) = clz_frac(x);
  return smlawb(fraction, fraction * (128 - fraction), 179) + (31 - leading << 7);
}

const SIGMOID_SLOPE: i32[] = [237, 153, 73, 30, 12, 7];

const SIGMOID_POSITIVE: i32[] = [16384, 23955, 28861, 31213, 32178, 32548];

const SIGMOID_NEGATIVE: i32[] = [16384, 8812, 3906, 1554, 589, 219];

pub function sigm_q15(x: i32): i32 {
  if (x < 0) {
    if (x <= -192) {
      return 0;
    }
    let value = -x;
    return SIGMOID_NEGATIVE[value >> 5] - smulbb(SIGMOID_SLOPE[value >> 5], value & 31);
  }
  return if (x >= 192) { 32767 } else { SIGMOID_POSITIVE[x >> 5] + smulbb(SIGMOID_SLOPE[x >> 5], x & 31) };
}

pub function expand_lpc(own coefficients: i32[], chirp_q16: i32): i32[] {
  let chirp = chirp_q16;
  for i in 0..coefficients.len() {
    coefficients = coefficients.with(i, (coefficients[i] as i64 * (chirp as i64) >> 16) as i32);
    if (i + 1 < coefficients.len()) {
      chirp = (chirp_q16 as i64 * (chirp as i64) + 32768i64 >> 16) as i32;
    }
  }
  return coefficients;
}

pub function expand_lpc16(own coefficients: i32[], own chirp_q16: i32): i32[] {
  let delta = chirp_q16 - 65536;
  for i in 0..coefficients.len() {
    coefficients = coefficients.with(i, s16(rshift_round32(chirp_q16 * coefficients[i], 16)));
    chirp_q16 += rshift_round32(chirp_q16 * delta, 16);
  }
  return coefficients;
}

function squared_energy(samples: [i32], shift: i32, initial: u32): u32 {
  let energy = initial;
  let i = 0;
  while (i + 1 < samples.len()) {
    let pair = smulbb(samples[i], samples[i]) as u32 + smulbb(samples[i + 1], samples[i + 1]) as u32;
    energy += pair >> shift as u32;
    i += 2;
  }
  if (i < samples.len()) {
    energy += smulbb(samples[i], samples[i]) as u32 >> shift as u32;
  }
  return energy;
}

pub function sum_squares(samples: [i32]): (i32, i32) {
  if (samples.len() == 0) {
    return (0, 0);
  }
  let shift = 31 - clz32(samples.len());
  let energy = squared_energy(samples, shift, samples.len() as u32);
  shift += 3 - clz32(energy as i32);
  if (shift < 0) {
    shift = 0;
  }
  return (squared_energy(samples, shift, 0u32) as i32, shift);
}
```
