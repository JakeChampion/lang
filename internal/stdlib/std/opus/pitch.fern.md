# The CELT pitch analysis

This module finds the pitch period of a signal, and holds the linear prediction that the analysis and the loss concealment use. It is a port of the float build of `pitch.c` and `celt_lpc.c` from libopus 1.4.

## Model

The pitch search works on a copy of the signal at half the sample rate. A filter of five taps makes the spectrum of that copy flat, so the strong harmonics do not hide the period. Then a coarse search at a quarter of the rate finds the two best candidates. A fine search at half the rate examines the periods near them, and an interpolation can move the result by one sample.

The encoder searches for the period of the prefilter at half the rate. A strong correlation at a multiple of the true period can win that search. `remove_doubling` examines each period `T / k` for `k` from 2 to 15. It takes the shortest period whose gain passes a threshold, then corrects the result by one sample and gives the gain of the prefilter.

The linear prediction finds the filter of order `p` that predicts each sample from the samples before it. `lpc` gets the filter from the autocorrelation, `fir` gives the error of the prediction, and `iir` makes a signal again from an error signal.

## Contracts

- An offset `at` in an argument is the index of the first value. `fir` and `iir` also read the `ord` values before it.
- `pitch_search` takes the signals at half the rate that `pitch_downsample` gives. `len` and `max_pitch` are in samples at the full rate, and `len` is a multiple of 4. It reads `len / 2` values of `x` and `(len + max_pitch) / 2` values of `y`.
- `remove_doubling` takes the signal at half the rate, with `maxperiod / 2` values of history before the `n / 2` values of the frame. `maxperiod`, `minperiod`, `n`, the period `t0` and the period of the last frame are at the full rate.
- `iir` takes an `n` that is a multiple of 4, and an `ord` that is a multiple of 4.
- The arithmetic is in f32, in the same order as libopus, so the results are the same bits.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<correlation>>

<<prediction>>

<<pitch>>

<<doubling>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT pitch analysis and linear prediction, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2010 Xiph.Org Foundation
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
import "std/opus/modes";
```

### `correlation`

Each sum adds its products in order, as the unrolled kernels of libopus do.

```fern
<<correlation>>=
// The sum of `x[xa + i] * y[ya + i]` for `i` below `n`.
pub function inner_prod(x: [f32], xa: i32, y: [f32], ya: i32, n: i32): f32 {
  let s: f32 = 0.0f32;
  for i in 0..n {
    s = s + x[xa + i] * y[ya + i];
  }
  return s;
}

pub struct Xcorr { xcorr: f32[], max: f32 }

// The correlation of `x` with `y` at each lag below `max_pitch`, and the
// largest of them, which is at least 1.
pub function pitch_xcorr(x: [f32], xa: i32, y: [f32], ya: i32, len: i32, max_pitch: i32): Xcorr {
  let xcorr: f32[] = [];
  let max = 1.0f32;
  for i in 0..max_pitch {
    let s = inner_prod(x, xa, y, ya + i, len);
    xcorr = xcorr.append(s);
    if (s > max) {
      max = s;
    }
  }
  return Xcorr { xcorr: xcorr, max: max };
}

// The autocorrelation of the `n` values of `x` from `at`, at the lags 0 to
// `lag`. With `windowed`, the mode window fades both ends over the overlap.
pub function autocorr(x: [f32], at: i32, windowed: boolean, lag: i32, n: i32): f32[] {
  let xx: f32[] = [];
  for i in 0..n {
    xx = xx.append(x[at + i]);
  }
  if (windowed) {
    for i in 0..modes.OVERLAP {
      xx = xx.with(i, x[at + i] * modes.WINDOW[i]);
      xx = xx.with(n - i - 1, x[at + n - i - 1] * modes.WINDOW[i]);
    }
  }
  let fast = n - lag;
  let ac = pitch_xcorr(xx, 0, xx, 0, fast, lag + 1).xcorr;
  for k in 0..lag + 1 {
    let d = 0.0f32;
    for i in k + fast..n {
      d += xx[i] * xx[i - k];
    }
    ac = ac.with(k, ac[k] + d);
  }
  return ac;
}
```

### `prediction`

`lpc` is the Levinson-Durbin recursion. It stops when the prediction removes 30 dB of the energy.

```fern
<<prediction>>=
const TINY: f32 = 1.000000013351432e-10f32;

const THIRTY_DB: f32 = 0.0010000000474974513f32;

function zeros(n: i32): f32[] {
  let x: f32[] = [];
  for i in 0..n {
    x = x.append(0.0f32);
  }
  return x;
}

// The `p` coefficients of the prediction from the autocorrelation `ac`.
pub function lpc(ac: [f32], p: i32): f32[] {
  let a = zeros(p);
  if (!(ac[0] > TINY)) {
    return a;
  }
  let error = ac[0];
  for i in 0..p {
    let rr = 0.0f32;
    for j in 0..i {
      rr += a[j] * ac[i - j];
    }
    rr += ac[i + 1];
    let r = -(rr / error);
    a = a.with(i, r);
    let half = i + 1 >> 1;
    for j in 0..half {
      let t1 = a[j];
      let t2 = a[i - 1 - j];
      a = a.with(j, t1 + r * t2);
      a = a.with(i - 1 - j, t2 + r * t1);
    }
    error -= r * r * error;
    if (error <= THIRTY_DB * ac[0]) {
      break;
    }
  }
  return a;
}

// The error of the prediction `num` of order `ord` for the `n` values of `x`
// from `at`.
pub function fir(x: [f32], at: i32, num: [f32], n: i32, ord: i32): f32[] {
  let y: f32[] = [];
  for i in 0..n {
    let s = x[at + i];
    for j in 0..ord {
      s += num[ord - 1 - j] * x[at + i + j - ord];
    }
    y = y.append(s);
  }
  return y;
}

pub struct Iir { y: f32[], mem: f32[] }

struct Block { y: f32[], out: f32[] }

// One block of four outputs of `iir`, as an FIR of the outputs before it,
// with the outputs of the block added after.
function iir_block(x: [f32], at: i32, den: [f32], own y: f32[], own out: f32[], i: i32, ord: i32): Block {
  let s0 = x[at + i];
  let s1 = x[at + i + 1];
  let s2 = x[at + i + 2];
  let s3 = x[at + i + 3];
  for j in 0..ord {
    let d = den[ord - 1 - j];
    s0 += d * y[i + j];
    s1 += d * y[i + j + 1];
    s2 += d * y[i + j + 2];
    s3 += d * y[i + j + 3];
  }
  y = y.with(i + ord, -s0);
  s1 += y[i + ord] * den[0];
  y = y.with(i + ord + 1, -s1);
  s2 += y[i + ord + 1] * den[0];
  s2 += y[i + ord] * den[1];
  y = y.with(i + ord + 2, -s2);
  s3 += y[i + ord + 2] * den[0];
  s3 += y[i + ord + 1] * den[1];
  s3 += y[i + ord] * den[2];
  y = y.with(i + ord + 3, -s3);
  out = out.append(s0).append(s1).append(s2).append(s3);
  return Block { y: y, out: out };
}

// The signal from the error `x` of the prediction `den` of order `ord`, for
// `n` values from `at`. `mem` holds the last `ord` outputs before them, the
// newest first, and the result holds the new ones.
pub function iir(x: [f32], at: i32, den: [f32], n: i32, ord: i32, mem: [f32]): Iir {
  let y = zeros(n + ord);
  for i in 0..ord {
    y = y.with(i, -mem[ord - 1 - i]);
  }
  let out: f32[] = [];
  let i = 0;
  while (i < n - 3) {
    let Block { y: y1, out: o1 } = iir_block(x, at, den, y, out, i, ord);
    y = y1;
    out = o1;
    i += 4;
  }
  let next: f32[] = [];
  for k in 0..ord {
    next = next.append(out[n - k - 1]);
  }
  return Iir { y: out, mem: next };
}
```

### `pitch`

`pitch_downsample` gives the copy at half the rate, after the flattening filter. `pitch_search` gives the lag of `y` that matches `x` best, in samples at the full rate. The search steps two samples, and an interpolation can move the result by one sample.

```fern
<<pitch>>=
const NOISE_FLOOR: f32 = 1.000100016593933f32;

const LAG_STEP: f32 = 0.00800000037997961f32;

const LAG_DECAY: f32 = 0.8999999761581421f32;

const ZERO_AT: f32 = 0.800000011920929f32;

const XCORR_SCALE: f32 = 9.999999960041972e-13f32;

const INTERPOLATE: f32 = 0.699999988079071f32;

function half_rate(x: [f32], at: i32, n: i32): f32[] {
  let y = [0.25f32 * x[at + 1] + 0.5f32 * x[at]];
  for i in 1..n {
    y = y.append(0.25f32 * x[at + 2 * i - 1] + 0.25f32 * x[at + 2 * i + 1] + 0.5f32 * x[at + 2 * i]);
  }
  return y;
}

// The five-tap filter, in place.
function fir5(own x: f32[], num: [f32], n: i32): f32[] {
  let m0: f32 = 0.0f32;
  let m1: f32 = 0.0f32;
  let m2: f32 = 0.0f32;
  let m3: f32 = 0.0f32;
  let m4: f32 = 0.0f32;
  for i in 0..n {
    let s = x[i] + num[0] * m0 + num[1] * m1 + num[2] * m2 + num[3] * m3 + num[4] * m4;
    m4 = m3;
    m3 = m2;
    m2 = m1;
    m1 = m0;
    m0 = x[i];
    x = x.with(i, s);
  }
  return x;
}

// The signal of `channels` channels, from `x0[a0]` and `x1[a1]`, at half
// the rate and flattened. `len` is the length at the full rate.
pub function pitch_downsample(x0: [f32], a0: i32, x1: [f32], a1: i32, len: i32, channels: i32): f32[] {
  let n = len >> 1;
  let lp = half_rate(x0, a0, n);
  if (channels == 2) {
    let lp1 = half_rate(x1, a1, n);
    for i in 0..n {
      lp = lp.with(i, lp[i] + lp1[i]);
    }
  }
  let ac = autocorr(lp, 0, false, 4, n);
  ac = ac.with(0, ac[0] * NOISE_FLOOR);
  for i in 1..5 {
    let w = LAG_STEP * (i as f32);
    ac = ac.with(i, ac[i] - ac[i] * w * w);
  }
  let a = lpc(ac, 4);
  let tmp = 1.0f32;
  for i in 0..4 {
    tmp = LAG_DECAY * tmp;
    a = a.with(i, a[i] * tmp);
  }
  let num = [a[0] + ZERO_AT, a[1] + ZERO_AT * a[0], a[2] + ZERO_AT * a[1], a[3] + ZERO_AT * a[2], ZERO_AT * a[3]];
  return fir5(lp, num, n);
}

struct Best { num0: f32, den0: f32, p0: i32, num1: f32, den1: f32, p1: i32 }

// The two lags of `xcorr` with the best normalised correlation.
function find_best_pitch(xcorr: [f32], y: [f32], len: i32, max_pitch: i32): Best {
  let syy = 1.0f32;
  for j in 0..len {
    syy += y[j] * y[j];
  }
  let b = Best { num0: -1.0f32, den0: 0.0f32, p0: 0, num1: -1.0f32, den1: 0.0f32, p1: 1 };
  for i in 0..max_pitch {
    if (xcorr[i] > 0.0f32) {
      let x16 = xcorr[i] * XCORR_SCALE;
      let num = x16 * x16;
      if (num * b.den1 > b.num1 * syy) {
        if (num * b.den0 > b.num0 * syy) {
          b = Best { num0: num, den0: syy, p0: i, num1: b.num0, den1: b.den0, p1: b.p0 };
        } else {
          b = Best { ...b, num1: num, den1: syy, p1: i };
        }
      }
    }
    syy += y[i + len] * y[i + len] - y[i] * y[i];
    if (1.0f32 > syy) {
      syy = 1.0f32;
    }
  }
  return b;
}

function abs(v: i32): i32 {
  if (v < 0) {
    return -v;
  }
  return v;
}

// The period of `x` in `y`, from `len` values of `x[xa]` and
// `len + max_pitch` values of `y`.
pub function pitch_search(x: [f32], xa: i32, y: [f32], len: i32, max_pitch: i32): i32 {
  let lag = len + max_pitch;
  let x4: f32[] = [];
  for j in 0..len >> 2 {
    x4 = x4.append(x[xa + 2 * j]);
  }
  let y4: f32[] = [];
  for j in 0..lag >> 2 {
    y4 = y4.append(y[2 * j]);
  }
  let coarse = pitch_xcorr(x4, 0, y4, 0, len >> 2, max_pitch >> 2);
  let b = find_best_pitch(coarse.xcorr, y4, len >> 2, max_pitch >> 2);
  let half = max_pitch >> 1;
  let xcorr = zeros(half);
  for i in 0..half {
    if (abs(i - 2 * b.p0) <= 2 || abs(i - 2 * b.p1) <= 2) {
      let s = inner_prod(x, xa, y, i, len >> 1);
      if (-1.0f32 > s) {
        s = -1.0f32;
      }
      xcorr = xcorr.with(i, s);
    }
  }
  let fine = find_best_pitch(xcorr, y, len >> 1, half);
  let offset = 0;
  let p = fine.p0;
  if (p > 0 && p < half - 1) {
    let a = xcorr[p - 1];
    let m = xcorr[p];
    let c = xcorr[p + 1];
    if (c - a > INTERPOLATE * (m - a)) {
      offset = 1;
    } else if (a - c > INTERPOLATE * (m - c)) {
      offset = -1;
    }
  }
  return 2 * p - offset;
}
```

### `doubling`

The gain of a period is its correlation, normalised by the energy of the frame and of the signal one period earlier. The square root is in f64, as `celt_sqrt` is in the float build of libopus. The search compares each shorter period `T1` with a threshold that falls when `T1` is near the period of the last frame. For `k` above 2, the search also averages the correlation at a second multiple of `T1`. Thus a period of `T / k` must explain more of the signal than `T` does.

libopus also tests `T1 < 2 * minperiod` after the test of `T1 < 3 * minperiod`. The first test is true whenever the second is true, so the port does not have the second test.

```fern
<<doubling>>=
pub struct Doubling { period: i32, gain: f32 }

struct Candidate { t: i32, xy: f32, yy: f32, g: f32 }

const SECOND_CHECK: i32[] = [0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2];

const LOW_FLOOR: f32 = 0.30000001192092896f32;

const HIGH_FLOOR: f32 = 0.4000000059604645f32;

const HIGH_SCALE: f32 = 0.8500000238418579f32;

function pitch_gain(xy: f32, xx: f32, yy: f32): f32 {
  return xy / (((1.0f32 + xx * yy) as f64).sqrt() as f32);
}

// The energy of the `n` values before `b - i`, for each `i` up to `max`.
function yy_lookup(x: [f32], b: i32, n: i32, max: i32, xx: f32): f32[] {
  let out = [xx];
  let yy = xx;
  for i in 1..max + 1 {
    yy = yy + x[b - i] * x[b - i] - x[b + n - i] * x[b + n - i];
    if (0.0f32 > yy) {
      out = out.append(0.0f32);
    } else {
      out = out.append(yy);
    }
  }
  return out;
}

// The second period that the search averages with `t1`.
function second_period(t0: i32, t1: i32, k: i32, max: i32): i32 {
  if (k > 2) {
    return (2 * SECOND_CHECK[k] * t0 + k) / (2 * k);
  }
  if (t1 + t0 > max) {
    return t0;
  }
  return t0 + t1;
}

// The part of the gain of the last frame that `t1` keeps.
function continuity(t1: i32, k: i32, t0: i32, prev_period: i32, prev_gain: f32): f32 {
  let d = abs(t1 - prev_period);
  if (d <= 1) {
    return prev_gain;
  }
  if (d <= 2 && 5 * k * k < t0) {
    return 0.5f32 * prev_gain;
  }
  return 0.0f32;
}

function threshold(t1: i32, minperiod: i32, g0: f32, cont: f32): f32 {
  let floor = LOW_FLOOR;
  let t = INTERPOLATE * g0 - cont;
  if (t1 < 3 * minperiod) {
    floor = HIGH_FLOOR;
    t = HIGH_SCALE * g0 - cont;
  }
  if (floor > t) {
    return floor;
  }
  return t;
}

// The gain of the chosen period, which is at most its normalised gain.
function doubling_gain(c: Candidate): f32 {
  let xy = c.xy;
  if (0.0f32 > xy) {
    xy = 0.0f32;
  }
  let pg: f32 = 1.0f32;
  if (!(c.yy <= xy)) {
    pg = xy / (c.yy + 1.0f32);
  }
  if (pg > c.g) {
    return c.g;
  }
  return pg;
}

// The period at the full rate: `2 * t`, moved by one sample toward the
// stronger neighbour, and at least `minperiod`.
function doubling_period(x: [f32], b: i32, n: i32, t: i32, minperiod: i32): i32 {
  let c0 = inner_prod(x, b, x, b - (t - 1), n);
  let c1 = inner_prod(x, b, x, b - t, n);
  let c2 = inner_prod(x, b, x, b - (t + 1), n);
  let offset = 0;
  if (c2 - c0 > INTERPOLATE * (c1 - c0)) {
    offset = 1;
  } else if (c0 - c2 > INTERPOLATE * (c1 - c2)) {
    offset = -1;
  }
  let p = 2 * t + offset;
  if (p < minperiod) {
    return minperiod;
  }
  return p;
}

// The period and the gain of the prefilter for `x`, from the period `t0`
// that the search found.
pub function remove_doubling(x: [f32], maxperiod: i32, minperiod: i32, n: i32, t0: i32, prev_period: i32, prev_gain: f32): Doubling {
  let max = maxperiod / 2;
  let min = minperiod / 2;
  let half = n / 2;
  let prev = prev_period / 2;
  let t = t0 / 2;
  if (t >= max) {
    t = max - 1;
  }
  let xx = inner_prod(x, max, x, max, half);
  let yy = yy_lookup(x, max, half, max, xx);
  let xy0 = inner_prod(x, max, x, max - t, half);
  let g0 = pitch_gain(xy0, xx, yy[t]);
  let best = Candidate { t: t, xy: xy0, yy: yy[t], g: g0 };
  for k in 2..16 {
    let t1 = (2 * t + k) / (2 * k);
    if (t1 < min) {
      break;
    }
    let t1b = second_period(t, t1, k, max);
    let xy = 0.5f32 * (inner_prod(x, max, x, max - t1, half) + inner_prod(x, max, x, max - t1b, half));
    let yy1 = 0.5f32 * (yy[t1] + yy[t1b]);
    let g1 = pitch_gain(xy, xx, yy1);
    if (g1 > threshold(t1, min, g0, continuity(t1, k, t, prev, prev_gain))) {
      best = Candidate { t: t1, xy: xy, yy: yy1, g: g1 };
    }
  }
  return Doubling { period: doubling_period(x, max, half, best.t, minperiod), gain: doubling_gain(best) };
}
```
