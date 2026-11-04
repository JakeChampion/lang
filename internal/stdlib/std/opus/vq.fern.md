# The CELT band shapes

This module codes the shape of a CELT band as a PVQ codeword (RFC 6716, section 4.3.4). It is a port of the float build of `vq.c` from libopus 1.4.

## Model

The shape of a band is a vector of unit length. The encoder finds the codeword of `k` pulses that is nearest to the shape, and codes it with `std/opus/pvq`. The decoder scales the codeword back to the length `gain`.

Before the search, a rotation spreads the energy of the shape over more of its values. Then a codeword of few pulses, which has many zeros, can come nearer to a flat shape. After the decode, the opposite rotation gives the shape back. `spread` sets the strength of the rotation, from `SPREAD_NONE` to `SPREAD_AGGRESSIVE`.

A band of `b` short blocks has a bit in the collapse mask for each block. The bit is set when the codeword has a pulse in that block.

## Contracts

- `n` is 2 or more, and `k` is 1 or more.
- A shape array has `n` values.
- The arithmetic is in f32, in the same order as libopus, so the results are the same bits.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<rotation>>

<<search>>

<<coding>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT band shapes, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2009 Xiph.Org Foundation
// Written by Jean-Marc Valin
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions
// are met:
//
// - Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//
// - Redistributions in binary form must reproduce the above copyright
// notice, this list of conditions and the following disclaimer in the
// documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// ``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER
// OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
import "std/f64";
import "std/opus/pvq";
import "std/opus/range";
```

### `rotation`

`PI` is an f32 in libopus, so the angle of the rotation is an f32 before `cos` takes it as an f64.

```fern
<<rotation>>=
pub const SPREAD_NONE: i32 = 0;

pub const SPREAD_LIGHT: i32 = 1;

pub const SPREAD_NORMAL: i32 = 2;

pub const SPREAD_AGGRESSIVE: i32 = 3;

const PI: f32 = 3.141592653f32;

const SPREAD_FACTOR: i32[] = [15, 10, 5];

// cos(pi / 2 * x), as libopus calculates it.
pub function cos_norm(x: f32): f32 {
  return ((0.5f32 * PI * x) as f64).cos() as f32;
}

// One pass of plane rotations by the angle of `c` and `s`, between each value
// and the value `stride` after it: first forward, and then back.
function rotation1(own x: f32[], at: i32, len: i32, stride: i32, c: f32, s: f32): f32[] {
  let ms = -s;
  for i in 0..len - stride {
    let x1 = x[at + i];
    let x2 = x[at + i + stride];
    x = x.with(at + i + stride, c * x2 + s * x1);
    x = x.with(at + i, c * x1 + ms * x2);
  }
  let i = len - 2 * stride - 1;
  while (i >= 0) {
    let x1 = x[at + i];
    let x2 = x[at + i + stride];
    x = x.with(at + i + stride, c * x2 + s * x1);
    x = x.with(at + i, c * x1 + ms * x2);
    i -= 1;
  }
  return x;
}

// Rotates the `len` values of `x`, in `stride` blocks, for a codeword of `k`
// pulses. `dir` is 1 before the search and -1 after the decode.
pub function exp_rotation(own x: f32[], len: i32, dir: i32, stride: i32, k: i32, spread: i32): f32[] {
  if (2 * k >= len || spread == SPREAD_NONE) {
    return x;
  }
  let factor = SPREAD_FACTOR[spread - 1];
  let gain: f32 = 1.0f32 * (len as f32) / ((len + factor * k) as f32);
  let theta: f32 = 0.5f32 * (gain * gain);
  let c = cos_norm(theta);
  let s = cos_norm(1.0f32 - theta);
  let stride2 = 0;
  if (len >= 8 * stride) {
    stride2 = 1;
    while ((stride2 * stride2 + stride2) * stride + (stride >> 2) < len) {
      stride2 += 1;
    }
  }
  let block = len / stride;
  for i in 0..stride {
    let at = i * block;
    if (dir < 0) {
      if (stride2 > 0) {
        x = rotation1(x, at, block, stride2, s, c);
      }
      x = rotation1(x, at, block, 1, c, s);
    } else {
      x = rotation1(x, at, block, 1, c, -s);
      if (stride2 > 0) {
        x = rotation1(x, at, block, stride2, s, -c);
      }
    }
  }
  return x;
}
```

### `search`

The search starts from a projection of the shape onto the pyramid of `k` pulses, when `k` is more than half of `n`. Then it adds one pulse at a time where the pulse brings the codeword nearest to the shape. It works on the magnitudes and puts the signs back at the end.

```fern
<<search>>=
const EPSILON: f32 = 1.0e-15f32;

pub function inner_prod(x: [f32], y: [f32], n: i32): f32 {
  let xy: f32 = 0.0f32;
  for i in 0..n {
    xy = xy + x[i] * y[i];
  }
  return xy;
}

pub struct Search { x: f32[], iy: i32[], yy: f32 }

struct Start { x: f32[], iy: i32[], y: f32[], xy: f32, yy: f32, left: i32 }

// The projection onto the pyramid, rounded down, so that it has `k` pulses or
// fewer.
function project(own x: f32[], k: i32, n: i32): Start {
  let iy: i32[] = [];
  let y: f32[] = [];
  for j in 0..n {
    iy = iy.append(0);
    y = y.append(0.0f32);
  }
  let xy: f32 = 0.0f32;
  let yy: f32 = 0.0f32;
  let left = k;
  if (k > n >> 1) {
    let sum: f32 = 0.0f32;
    for j in 0..n {
      sum += x[j];
    }
    if (!(sum > EPSILON && sum < 64.0f32)) {
      x = x.with(0, 1.0f32);
      for j in 1..n {
        x = x.with(j, 0.0f32);
      }
      sum = 1.0f32;
    }
    let rcp: f32 = (k as f32 + 0.8f32) * (1.0f32 / sum);
    for j in 0..n {
      let p = (rcp * x[j]).floor() as i32;
      let v = p as f32;
      iy = iy.with(j, p);
      yy = yy + v * v;
      xy = xy + x[j] * v;
      y = y.with(j, v * 2.0f32);
      left -= p;
    }
  }
  return Start { x: x, iy: iy, y: y, xy: xy, yy: yy, left: left };
}

// The index of the value where one more pulse brings the codeword nearest to
// the shape.
function best_pulse(x: [f32], y: [f32], xy: f32, yy: f32, n: i32): i32 {
  let best = 0;
  let rxy0 = xy + x[0];
  let best_num = rxy0 * rxy0;
  let best_den = yy + y[0];
  for j in 1..n {
    let rxy = xy + x[j];
    let ryy = yy + y[j];
    let num = rxy * rxy;
    if (best_den * num > ryy * best_num) {
      best_den = ryy;
      best_num = num;
      best = j;
    }
  }
  return best;
}

// The codeword of `k` pulses nearest to the shape `x`, and the sum of its
// squares. The search changes `x`.
pub function pvq_search(own x: f32[], k: i32, n: i32): Search {
  let neg: boolean[] = [];
  for j in 0..n {
    neg = neg.append(x[j] < 0.0f32);
    x = x.with(j, x[j].abs());
  }
  let st = project(x, k, n);
  let Start { x: xs, iy, y, xy, yy, left } = st;
  if (left > n + 3) {
    let tmp = left as f32;
    yy = yy + tmp * tmp;
    yy = yy + tmp * y[0];
    iy = iy.with(0, iy[0] + left);
    left = 0;
  }
  for i in 0..left {
    yy += 1.0f32;
    let best = best_pulse(xs, y, xy, yy, n);
    xy += xs[best];
    yy += y[best];
    y = y.with(best, y[best] + 2.0f32);
    iy = iy.with(best, iy[best] + 1);
  }
  for j in 0..n {
    if (neg[j]) {
      iy = iy.with(j, 0 - iy[j]);
    }
  }
  return Search { x: xs, iy: iy, yy: yy };
}
```

### `coding`

```fern
<<coding>>=
// The shape of the codeword `iy`, scaled to the length `gain`.
function normalise_residual(iy: [i32], n: i32, ryy: f32, gain: f32): f32[] {
  let g = 1.0f32 / ((ryy as f64).sqrt() as f32) * gain;
  let x: f32[] = [];
  for i in 0..n {
    x = x.append(g * (iy[i] as f32));
  }
  return x;
}

// One bit for each of the `b` blocks of the codeword that has a pulse.
pub function collapse_mask(iy: [i32], n: i32, b: i32): i32 {
  if (b <= 1) {
    return 1;
  }
  let n0 = n / b;
  let mask = 0;
  for i in 0..b {
    let any = 0;
    for j in 0..n0 {
      any = any | iy[i * n0 + j];
    }
    if (any != 0) {
      mask = mask | 1 << i;
    }
  }
  return mask;
}

pub struct Quant { e: range.Encoder, x: f32[], mask: i32 }

// Codes the shape `x` with `k` pulses. With `resynth`, the result holds the
// shape that the decoder gets.
pub function alg_quant(own e: range.Encoder, own x: f32[], n: i32, k: i32, spread: i32, b: i32, gain: f32, resynth: boolean): Quant {
  x = exp_rotation(x, n, 1, b, k, spread);
  let s = pvq_search(x, k, n);
  e = pvq.encode_pulses(e, s.iy, k);
  let out = s.x;
  if (resynth) {
    out = exp_rotation(normalise_residual(s.iy, n, s.yy, gain), n, 0 - 1, b, k, spread);
  }
  return Quant { e: e, x: out, mask: collapse_mask(s.iy, n, b) };
}

pub struct Unquant { d: range.Decoder, x: f32[], mask: i32 }

// Decodes a shape of `n` values and `k` pulses, scaled to the length `gain`.
pub function alg_unquant(own d: range.Decoder, n: i32, k: i32, spread: i32, b: i32, gain: f32): Unquant {
  let (d1, iy, ryy) = pvq.decode_pulses(d, n, k);
  let x: f32[] = exp_rotation(normalise_residual(iy, n, ryy as f32, gain), n, 0 - 1, b, k, spread);
  return Unquant { d: d1, x: x, mask: collapse_mask(iy, n, b) };
}

// Scales `x` to the length `gain`.
pub function renormalise_vector(own x: f32[], n: i32, gain: f32): f32[] {
  let e = EPSILON + inner_prod(x, x, n);
  let g = 1.0f32 / ((e as f64).sqrt() as f32) * gain;
  for i in 0..n {
    x = x.with(i, g * x[i]);
  }
  return x;
}

// atan2(y, x) with an error of at most about 1e-4, as libopus calculates it.
pub function fast_atan2f(y: f32, x: f32): f32 {
  let x2 = x * x;
  let y2 = y * y;
  if (x2 + y2 < 1.0e-18f32) {
    return 0.0f32;
  }
  let ce = PI / 2.0f32;
  let half = ce;
  if (y < 0.0f32) {
    half = -ce;
  }
  if (x2 < y2) {
    let den: f32 = (y2 + 0.67848403f32 * x2) * (y2 + 0.08595542f32 * x2);
    return -x * y * (y2 + 0.43157974f32 * x2) / den + half;
  }
  let den: f32 = (x2 + 0.67848403f32 * y2) * (x2 + 0.08595542f32 * y2);
  let quad = ce;
  if (x * y < 0.0f32) {
    quad = -ce;
  }
  return x * y * (x2 + 0.43157974f32 * y2) / den + half - quad;
}

// The angle between the two channels of a band, from 0 to 16384. With
// `stereo`, the angle is that of the mid and side of `x` and `y`.
pub function stereo_itheta(x: [f32], y: [f32], stereo: boolean, n: i32): i32 {
  let emid = EPSILON;
  let eside = EPSILON;
  if (stereo) {
    for i in 0..n {
      let m = x[i] + y[i];
      let s = x[i] - y[i];
      emid = emid + m * m;
      eside = eside + s * s;
    }
  } else {
    emid += inner_prod(x, x, n);
    eside += inner_prod(y, y, n);
  }
  let mid = (emid as f64).sqrt() as f32;
  let side = (eside as f64).sqrt() as f32;
  return (0.5f32 + 16384.0f32 * 0.63662f32 * fast_atan2f(side, mid)).floor() as i32;
}
```
