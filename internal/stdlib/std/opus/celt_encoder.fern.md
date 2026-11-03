# The CELT encoder

This module contains the first analysis functions for the CELT encoder, the transform layer of Opus (RFC 6716, section 4.3). It ports the corresponding functions from the float build of `celt_encoder.c` in libopus 1.4. The tests compare their results with libopus. Complete frame encoding remains part of #14083.

## Model

The CELT encoder works on frames of 2.5 to 20 ms at 48 kHz. These functions analyse the signal before the range coder, energy quantiser, band coder and bit allocation code the frame.

The analysis functions come first. Each one makes one decision for the frame:

- `preemphasis` scales the samples to the CELT range and applies the pre-emphasis filter.
- `transient_analysis` decides if the frame has a transient, so that it takes short blocks.
- `patch_transient_decision` finds a sudden energy increase that the transient analysis missed.
- `tf_analysis` chooses the time-frequency resolution of each band, and `tf_encode` codes the choice.
- `alloc_trim_analysis` chooses the allocation trim from the spectral tilt and the stereo correlation.
- `stereo_analysis` decides if mid and side coding costs less than left and right coding.

An `Analysis` holds the results of the tonality analysis of the Opus layer. When `valid` is false, the encoder does not use them.

## Contracts

- The arithmetic is in f32, in the same order as libopus. Where libopus does a step in f64, this module does the same step in f64.
- The samples of `preemphasis` are interleaved, with `channels` samples for each instant, from -1.0 to 1.0.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<preemphasis>>

<<transients>>

<<tf>>

<<trim>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT encoder, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2010 Xiph.Org Foundation
// Copyright (c) 2008 Gregory Maxwell
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
import "std/opus/celt";
import "std/opus/modes";
import "std/opus/pitch";
import "std/opus/range";
```

### `preemphasis`

`preemphasis` takes `n` samples of channel `c` from interleaved samples. With `upsample` more than 1, zeros come after each sample. With `clip`, a sample is held inside two times the full scale. It gives the filtered samples and the filter memory for the next frame.

```fern
<<preemphasis>>=
const SIG_SCALE: f32 = 32768.0f32;

pub struct Preemphasis { samples: f32[], mem: f32 }

pub function preemphasis(pcm: [f32], c: i32, n: i32, channels: i32, upsample: i32, mem: f32, clip: boolean): Preemphasis {
  let inp: f32[] = [];
  for i in 0..n {
    inp = inp.append(0.0f32);
  }
  let nu: i32 = n / upsample;
  for i in 0..nu {
    let x: f32 = pcm[channels * i + c] * SIG_SCALE;
    if (clip) {
      x = max32(-65536.0f32, min32(65536.0f32, x));
    }
    inp = inp.with(i * upsample, x);
  }
  let m: f32 = mem;
  let out: f32[] = [];
  for x in inp {
    out = out.append(x - m);
    m = modes.PREEMPH * x;
  }
  return Preemphasis { samples: out, mem: m };
}

function min32(a: f32, b: f32): f32 {
  if (a < b) {
    return a;
  }
  return b;
}

function max32(a: f32, b: f32): f32 {
  if (a > b) {
    return a;
  }
  return b;
}
```

### `transients`

`transient_analysis` filters each channel with a high-pass filter, then follows its energy forwards and backwards with two decays. The ratio of the frame energy to the harmonic mean of that envelope is the mask metric. A metric over 200 is a transient. With `allow_weak`, a metric under 600 is only a weak transient, which the encoder handles in another way at low bitrates.

```fern
<<transients>>=
const INV_TABLE: i32[] = [
  255, 255, 156, 110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
  23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
  12, 12, 11, 11, 11, 10, 10, 10, 9, 9, 9, 9, 9, 9, 8, 8,
  8, 8, 8, 7, 7, 7, 7, 7, 7, 6, 6, 6, 6, 6, 6, 6,
  6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 5, 5, 5,
  5, 5, 5, 5, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
  4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3,
  3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2,
];

const EPSILON: f32 = 0.000000000000001f32;

pub struct Transient { transient: boolean, weak: boolean, tf_estimate: f32, tf_chan: i32 }

// The mask metric of channel `c`: `len` samples from `c * len`.
function mask_metric(input: [f32], c: i32, len: i32, decay: f32): i32 {
  let tmp: f32[] = [];
  let mem0: f32 = 0.0f32;
  let mem1: f32 = 0.0f32;
  for i in 0..len {
    let x: f32 = input[i + c * len];
    let y: f32 = mem0 + x;
    mem0 = mem1 + y - 2.0f32 * x;
    mem1 = x - 0.5f32 * y;
    if (i < 12) {
      tmp = tmp.append(0.0f32);
    } else {
      tmp = tmp.append(y);
    }
  }
  let len2: i32 = len / 2;
  let mean: f32 = 0.0f32;
  mem0 = 0.0f32;
  // A forward pass gives the post-echo threshold.
  for i in 0..len2 {
    let x2: f32 = tmp[2 * i] * tmp[2 * i] + tmp[2 * i + 1] * tmp[2 * i + 1];
    mean = mean + x2;
    let v: f32 = mem0 + decay * (x2 - mem0);
    tmp = tmp.with(i, v);
    mem0 = v;
  }
  mem0 = 0.0f32;
  let max_e: f32 = 0.0f32;
  // A backward pass gives the pre-echo threshold.
  let i: i32 = len2 - 1;
  while (i >= 0) {
    let v: f32 = mem0 + 0.125f32 * (tmp[i] - mem0);
    tmp = tmp.with(i, v);
    mem0 = v;
    max_e = max32(max_e, mem0);
    i = i - 1;
  }
  let frame: f32 = ((mean * max_e) as f64 * 0.5 * (len2 as f64)).sqrt() as f32;
  let norm: f32 = len2 as f32 / (EPSILON + frame);
  let unmask: i32 = 0;
  let k: i32 = 12;
  while (k < len2 - 5) {
    let v: f64 = ((64.0f32 * norm * (tmp[k] + EPSILON)) as f64).floor();
    let id: i32 = 0;
    if (v > 0.0) {
      id = 127;
      if (v < 127.0) {
        id = v as i32;
      }
    }
    unmask = unmask + INV_TABLE[id];
    k = k + 4;
  }
  return 64 * unmask * 4 / (6 * (len2 - 17));
}

pub function transient_analysis(input: [f32], len: i32, channels: i32, allow_weak: boolean): Transient {
  let decay: f32 = 0.0625f32;
  if (allow_weak) {
    decay = 0.03125f32;
  }
  let metric: i32 = 0;
  let tf_chan: i32 = 0;
  for c in 0..channels {
    let unmask: i32 = mask_metric(input, c, len, decay);
    if (unmask > metric) {
      tf_chan = c;
      metric = unmask;
    }
  }
  let transient: boolean = metric > 200;
  let weak: boolean = false;
  if (allow_weak && transient && metric < 600) {
    transient = false;
    weak = true;
  }
  let tf_max: f32 = max32(0.0f32, ((27 * metric) as f64).sqrt() as f32 - 42.0f32);
  let scaled: f64 = (0.0069f32 * min32(163.0f32, tf_max)) as f64 - 0.139;
  let tf_estimate: f32 = 0.0f32;
  if (scaled > 0.0) {
    tf_estimate = scaled.sqrt() as f32;
  }
  return Transient { transient: transient, weak: weak, tf_estimate: tf_estimate, tf_chan: tf_chan };
}

// A mean increase of more than 1 dB over a spread copy of the old energies
// is a transient.
pub function patch_transient_decision(new_e: [f32], old_e: [f32], bands: i32, start: i32, end: i32, channels: i32): boolean {
  let spread: f32[] = [];
  for i in 0..end {
    spread = spread.append(0.0f32);
  }
  if (channels == 1) {
    spread = spread.with(start, old_e[start]);
    for i in start + 1..end {
      spread = spread.with(i, max32(spread[i - 1] - 1.0f32, old_e[i]));
    }
  } else {
    spread = spread.with(start, max32(old_e[start], old_e[start + bands]));
    for i in start + 1..end {
      spread = spread.with(i, max32(spread[i - 1] - 1.0f32, max32(old_e[i], old_e[i + bands])));
    }
  }
  let i: i32 = end - 2;
  while (i >= start) {
    spread = spread.with(i, max32(spread[i], spread[i + 1] - 1.0f32));
    i = i - 1;
  }
  let from: i32 = start;
  if (from < 2) {
    from = 2;
  }
  let diff: f32 = 0.0f32;
  for c in 0..channels {
    for k in from..end - 1 {
      let x1: f32 = max32(0.0f32, new_e[k + c * bands]);
      let x2: f32 = max32(0.0f32, spread[k]);
      diff = diff + max32(0.0f32, x1 - x2);
    }
  }
  diff = diff / ((channels * (end - 1 - from)) as f32);
  return diff > 1.0f32;
}
```

### `tf`

The TF analysis tries each resolution of each band with Haar steps, and takes the one with the smallest L1 norm. A bias prefers good frequency resolution. A Viterbi search then chooses the flag of each band, where a change of flag between bands costs `lambda`. `importance` weighs the cost of each band.

```fern
<<tf>>=
const SQRT_HALF: f32 = 0.70710678f32;

function abs16(x: f32): f32 {
  if (x <= 0.0f32) {
    return 0.0f32 - x;
  }
  return x;
}

function max16(a: f32, b: f32): f32 {
  if (a > b) {
    return a;
  }
  return b;
}

function min16(a: f32, b: f32): f32 {
  if (a < b) {
    return a;
  }
  return b;
}

// One Haar step on pairs of values `stride` apart.
function haar1(own x: f32[], n0: i32, stride: i32): f32[] {
  for i in 0..stride {
    for j in 0..n0 >> 1 {
      let a: i32 = stride * 2 * j + i;
      let c: i32 = stride * (2 * j + 1) + i;
      let tmp1: f32 = SQRT_HALF * x[a];
      let tmp2: f32 = SQRT_HALF * x[c];
      x = x.with(a, tmp1 + tmp2);
      x = x.with(c, tmp1 - tmp2);
    }
  }
  return x;
}

function l1_metric(tmp: [f32], n: i32, lm: i32, bias: f32): f32 {
  let l1: f32 = 0.0f32;
  for i in 0..n {
    l1 = l1 + abs16(tmp[i]);
  }
  return l1 + lm as f32 * bias * l1;
}

function part(x: [f32], at: i32, n: i32): f32[] {
  let out: f32[] = [];
  for i in at..at + n {
    out = out.append(x[i]);
  }
  return out;
}

// The metric of a band in half steps from its best number of Haar steps.
// A band of one bin cannot split to -1, so its metric goes to the half-way
// point when it is at an end.
function metric_of(best_level: i32, transient: boolean, narrow: boolean, lm: i32): i32 {
  let metric: i32 = 0 - 2 * best_level;
  if (transient) {
    metric = 2 * best_level;
  }
  if (narrow && (metric == 0 || metric == 0 - 2 * lm)) {
    metric = metric - 1;
  }
  return metric;
}

// The L1 norm of the band after one more Haar step than the frame size has:
// the -1 level of a transient.
function split_l1(tmp: [f32], n: i32, lm: i32, bias: f32): f32 {
  return l1_metric(haar1(part(tmp, 0, n), n >> lm, 1 << lm), n, lm + 1, bias);
}

// The metric of band `i` in half steps: the best number of Haar steps, with
// the sign of the direction.
function band_metric(i: i32, transient: boolean, x: [f32], n0: i32, lm: i32, tf_chan: i32, bias: f32): i32 {
  let width: i32 = modes.EBANDS[i + 1] - modes.EBANDS[i];
  let n: i32 = width << lm;
  let narrow: boolean = width == 1;
  let tmp: f32[] = part(x, tf_chan * n0 + (modes.EBANDS[i] << lm), n);
  let first: i32 = 0;
  let steps: i32 = lm + 1;
  if (transient) {
    first = lm;
  }
  if (transient || narrow) {
    steps = lm;
  }
  let best_l1: f32 = l1_metric(tmp, n, first, bias);
  let best_level: i32 = 0;
  if (transient && !narrow) {
    let l1: f32 = split_l1(tmp, n, lm, bias);
    if (l1 < best_l1) {
      best_l1 = l1;
      best_level = 0 - 1;
    }
  }
  for k in 0..steps {
    let b: i32 = k + 1;
    if (transient) {
      b = lm - k - 1;
    }
    tmp = haar1(tmp, n >> k, 1 << k);
    let l1: f32 = l1_metric(tmp, n, b, bias);
    if (l1 < best_l1) {
      best_l1 = l1;
      best_level = k + 1;
    }
  }
  return metric_of(best_level, transient, narrow, lm);
}

function abs_i(v: i32): i32 {
  if (v < 0) {
    return 0 - v;
  }
  return v;
}

function min_i(a: i32, b: i32): i32 {
  if (a < b) {
    return a;
  }
  return b;
}

// The cost of band `i` for flag `k` of the row of `select`.
function band_cost(metric: [i32], importance: [i32], i: i32, row: i32, k: i32): i32 {
  return importance[i] * abs_i(metric[i] - 2 * celt.TF_SELECT[row + k]);
}

function first_costs(metric: [i32], importance: [i32], row: i32, lambda: i32, transient: boolean): (i32, i32) {
  let extra: i32 = lambda;
  if (transient) {
    extra = 0;
  }
  return (band_cost(metric, importance, 0, row, 0), band_cost(metric, importance, 0, row, 1) + extra);
}

// The lowest cost of the flags of all bands with `select`.
function select_cost(metric: [i32], importance: [i32], row: i32, lambda: i32, transient: boolean): i32 {
  let (cost0, cost1) = first_costs(metric, importance, row, lambda, transient);
  for i in 1..metric.len() {
    let curr0: i32 = min_i(cost0, cost1 + lambda);
    let curr1: i32 = min_i(cost0 + lambda, cost1);
    cost0 = curr0 + band_cost(metric, importance, i, row, 0);
    cost1 = curr1 + band_cost(metric, importance, i, row, 1);
  }
  return min_i(cost0, cost1);
}

// The flag of each band on the path of lowest cost.
function viterbi(metric: [i32], importance: [i32], row: i32, lambda: i32, transient: boolean): i32[] {
  let len: i32 = metric.len();
  let path0: i32[] = [0];
  let path1: i32[] = [0];
  let (cost0, cost1) = first_costs(metric, importance, row, lambda, transient);
  for i in 1..len {
    let curr0: i32 = cost1 + lambda;
    let p0: i32 = 1;
    if (cost0 < cost1 + lambda) {
      curr0 = cost0;
      p0 = 0;
    }
    let curr1: i32 = cost1;
    let p1: i32 = 1;
    if (cost0 + lambda < cost1) {
      curr1 = cost0 + lambda;
      p1 = 0;
    }
    path0 = path0.append(p0);
    path1 = path1.append(p1);
    cost0 = curr0 + band_cost(metric, importance, i, row, 0);
    cost1 = curr1 + band_cost(metric, importance, i, row, 1);
  }
  let res: i32[] = [];
  for i in 0..len {
    res = res.append(0);
  }
  if (cost0 >= cost1) {
    res = res.with(len - 1, 1);
  }
  let i: i32 = len - 2;
  while (i >= 0) {
    if (res[i + 1] == 1) {
      res = res.with(i, path1[i + 1]);
    } else {
      res = res.with(i, path0[i + 1]);
    }
    i = i - 1;
  }
  return res;
}

pub struct Tf { res: i32[], select: i32 }

pub function tf_analysis(len: i32, transient: boolean, lambda: i32, x: [f32], n0: i32, lm: i32, tf_estimate: f32, tf_chan: i32, importance: [i32]): Tf {
  let bias: f32 = 0.04f32 * max16(-0.25f32, 0.5f32 - tf_estimate);
  let metric: i32[] = [];
  for i in 0..len {
    metric = metric.append(band_metric(i, transient, x, n0, lm, tf_chan, bias));
  }
  let row: i32 = lm * 8;
  if (transient) {
    row = row + 4;
  }
  // tf_select = 1 is only for transients.
  let select: i32 = 0;
  if (transient && select_cost(metric, importance, row + 2, lambda, transient) < select_cost(metric, importance, row, lambda, transient)) {
    select = 1;
  }
  return Tf { res: viterbi(metric, importance, row + 2 * select, lambda, transient), select: select };
}

// Codes the flags of bands `start` to `end` while the budget lasts, then
// `select` when it changes a band. It gives the TF change of each band.
pub function tf_encode(own e: range.Encoder, start: i32, end: i32, transient: boolean, own res: i32[], lm: i32, select: i32): (range.Encoder, i32[]) {
  let budget: i32 = e.storage * 8;
  let tell: i32 = e.tell();
  let logp: i32 = 4;
  let next_logp: i32 = 5;
  let row: i32 = lm * 8;
  if (transient) {
    logp = 2;
    next_logp = 4;
    row = row + 4;
  }
  let rsv: boolean = lm > 0 && tell + logp + 1 <= budget;
  if (rsv) {
    budget = budget - 1;
  }
  let curr: i32 = 0;
  let changed: i32 = 0;
  for i in start..end {
    if (tell + logp <= budget) {
      e = e.bit_logp(res[i] != curr, logp);
      tell = e.tell();
      curr = res[i];
      changed = changed | curr;
    } else {
      res = res.with(i, curr);
    }
    logp = next_logp;
  }
  let sel: i32 = 0;
  if (rsv && celt.TF_SELECT[row + changed] != celt.TF_SELECT[row + 2 + changed]) {
    e = e.bit_logp(select != 0, 1);
    sel = select;
  }
  for i in start..end {
    res = res.with(i, celt.TF_SELECT[row + 2 * sel + res[i]]);
  }
  return (e, res);
}
```

### `trim`

The allocation trim moves bits between the low and the high bands. It starts from 5, or less at low rates. A strong correlation between the channels at low frequencies, a falling spectrum, and a high TF estimate each lower it. The result is an index from 0 to 10.

```fern
<<trim>>=
pub struct Analysis { valid: boolean, tonality: f32, tonality_slope: f32, activity: f32, max_pitch_ratio: f32, leak_boost: i32[] }

pub function no_analysis(): Analysis {
  return Analysis { valid: false, tonality: 0.0f32, tonality_slope: 0.0f32, activity: 0.0f32, max_pitch_ratio: 0.0f32, leak_boost: [] };
}

pub struct Trim { index: i32, stereo_saving: f32 }

function celt_log2(x: f32): f32 {
  return (1.442695040888963387 * (x as f64).log()) as f32;
}

// The correlation of the channels in band `i`.
function band_corr(x: [f32], n0: i32, lm: i32, i: i32): f32 {
  let at: i32 = modes.EBANDS[i] << lm;
  return pitch.inner_prod(x, at, x, n0 + at, modes.EBANDS[i + 1] - modes.EBANDS[i] << lm);
}

// The trim change and the stereo saving from the correlation of the
// channels, in that order.
function stereo_trim(x: [f32], n0: i32, lm: i32, intensity: i32, stereo_saving: f32): (f32, f32) {
  let sum: f32 = 0.0f32;
  for i in 0..8 {
    sum = sum + band_corr(x, n0, lm, i);
  }
  sum = 0.125f32 * sum;
  sum = min16(1.0f32, abs16(sum));
  let min_xc: f32 = sum;
  for i in 8..intensity {
    min_xc = min16(min_xc, abs16(band_corr(x, n0, lm, i)));
  }
  min_xc = min16(1.0f32, abs16(min_xc));
  let log_xc: f32 = celt_log2(1.001f32 - sum * sum);
  let log_xc2: f32 = max16(0.5f32 * log_xc, celt_log2(1.001f32 - min_xc * min_xc));
  return (max16(-4.0f32, 0.75f32 * log_xc), min16(stereo_saving + 0.25f32, -(0.5f32 * log_xc2)));
}

pub function alloc_trim_analysis(x: [f32], band_log_e: [f32], end: i32, lm: i32, channels: i32, n0: i32, analysis: Analysis, stereo_saving: f32, tf_estimate: f32, intensity: i32, surround_trim: f32, equiv_rate: i32): Trim {
  let trim: f32 = 5.0f32;
  if (equiv_rate < 64000) {
    trim = 4.0f32;
  } else if (equiv_rate < 80000) {
    trim = 4.0f32 + 0.0625f32 * ((equiv_rate - 64000 >> 10) as f32);
  }
  let saving: f32 = stereo_saving;
  if (channels == 2) {
    let (add, s) = stereo_trim(x, n0, lm, intensity, stereo_saving);
    trim = trim + add;
    saving = s;
  }
  // The spectral tilt.
  let diff: f32 = 0.0f32;
  for c in 0..channels {
    for i in 0..end - 1 {
      diff = diff + band_log_e[i + c * modes.BANDS] * ((2 + 2 * i - end) as f32);
    }
  }
  diff = diff / ((channels * (end - 1)) as f32);
  trim = trim - max32(-2.0f32, min32(2.0f32, (diff + 1.0f32) / 6.0f32));
  trim = trim - surround_trim;
  trim = trim - 2.0f32 * tf_estimate;
  if (analysis.valid) {
    trim = trim - max16(-2.0f32, min16(2.0f32, 2.0f32 * (analysis.tonality_slope + 0.05f32)));
  }
  let index: i32 = ((0.5f32 + trim) as f64).floor() as i32;
  if (index < 0) {
    index = 0;
  }
  if (index > 10) {
    index = 10;
  }
  return Trim { index: index, stereo_saving: saving };
}

// Whether mid and side coding of the first 13 bands costs less than left
// and right coding, by the L1 norm of each.
pub function stereo_analysis(x: [f32], lm: i32, n0: i32): boolean {
  let sum_lr: f32 = EPSILON;
  let sum_ms: f32 = EPSILON;
  for j in 0..modes.EBANDS[13] << lm {
    let l: f32 = x[j];
    let r: f32 = x[n0 + j];
    let m: f32 = l + r;
    let s: f32 = l - r;
    sum_lr = sum_lr + (abs16(l) + abs16(r));
    sum_ms = sum_ms + (abs16(m) + abs16(s));
  }
  sum_ms = 0.707107f32 * sum_ms;
  let thetas: i32 = 13;
  if (lm <= 1) {
    thetas = 5;
  }
  return ((modes.EBANDS[13] << lm + 1) + thetas) as f32 * sum_ms > (modes.EBANDS[13] << lm + 1) as f32 * sum_lr;
}
```

