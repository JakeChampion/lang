# The CELT encoder

This module contains the first analysis functions for the CELT encoder, the transform layer of Opus (RFC 6716, section 4.3). It ports the corresponding functions from the float build of `celt_encoder.c` in libopus 1.4. The tests compare their results with libopus. Complete frame encoding remains part of #14083.

## Model

The CELT encoder works on frames of 2.5 to 20 ms at 48 kHz. These functions analyse the signal before the range coder, energy quantiser, band coder and bit allocation code the frame.

The analysis functions come first. Each one makes one decision for the frame:

- `preemphasis` scales the samples to the CELT range and applies the pre-emphasis filter.
- `compute_mdcts` gives the spectrum of each channel, in one long block or in interleaved short blocks.
- `transient_analysis` decides if the frame has a transient, so that it takes short blocks.
- `patch_transient_decision` finds a sudden energy increase that the transient analysis missed.
- `tf_analysis` chooses the time-frequency resolution of each band, and `tf_encode` codes the choice.
- `alloc_trim_analysis` chooses the allocation trim from the spectral tilt and the stereo correlation.
- `stereo_analysis` decides if mid and side coding costs less than left and right coding.
- `dynalloc_analysis` gives the extra bits of each band from a follower of the band energies. It also gives the importance of each band for the TF analysis and the spreading weight of each band.
- `compute_vbr` gives the bit target of a VBR frame.
- `run_prefilter` finds the pitch period and the gain of the frame, and applies the pitch prefilter.

An `Analysis` holds the results of the tonality analysis of the Opus layer. When `valid` is false, the encoder does not use them.

## Contracts

- The arithmetic is in f32, in the same order as libopus. Where libopus does a step in f64, this module does the same step in f64.
- The samples of `preemphasis` are interleaved, with `channels` samples for each instant, from -1.0 to 1.0.
- The input of `run_prefilter` holds the overlap and then the frame of each channel. Its `mem` holds `celt.COMBFILTER_MAXPERIOD` samples of each channel, and its `in_mem` holds one overlap of each channel.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<preemphasis>>

<<mdcts>>

<<transients>>

<<tf>>

<<trim>>

<<dynalloc>>

<<vbr>>

<<prefilter>>
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
import "std/opus/energy";
import "std/opus/mdct";
import "std/opus/modes";
import "std/opus/pitch";
import "std/opus/range";
import "std/opus/rate";
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

### `mdcts`

A frame of short blocks has `short_blocks` MDCTs of `modes.SHORT_MDCT_SIZE` bins for each channel. Their bins are interleaved: bin `j` of block `b` is at `b + j * short_blocks`. A mono frame of a stereo input takes the mean of the two spectra. When the input had a lower rate, the bins above that rate are 0 and the others are scaled up.

```fern
<<mdcts>>=
// The spectrum of each of `cc` input channels, or of `c` coded channels
// when `c` is less. `input` holds `b * n + modes.OVERLAP` samples for
// each channel.
pub function compute_mdcts(input: [f32], short_blocks: i32, c: i32, cc: i32, lm: i32, upsample: i32): f32[] {
  let b: i32 = 1;
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  let shift: i32 = modes.MAX_LM - lm;
  if (short_blocks > 0) {
    b = short_blocks;
    n = modes.SHORT_MDCT_SIZE;
    shift = modes.MAX_LM;
  }
  let out: f32[] = [];
  for i in 0..cc * b * n {
    out = out.append(0.0f32);
  }
  for ch in 0..cc {
    for blk in 0..b {
      out = mdct.forward(input, ch * (b * n + modes.OVERLAP) + blk * n, out, blk + ch * n * b, shift, b);
    }
  }
  if (cc == 2 && c == 1) {
    for i in 0..b * n {
      out = out.with(i, 0.5f32 * out[i] + 0.5f32 * out[b * n + i]);
    }
  }
  if (upsample != 1) {
    return upsampled(out, c, b * n, upsample);
  }
  return out;
}

// Each of the `c` spectra of `len` bins, scaled by `upsample` below the
// rate of the input and 0 above it.
function upsampled(own out: f32[], c: i32, len: i32, upsample: i32): f32[] {
  let bound: i32 = len / upsample;
  for ch in 0..c {
    for i in 0..len {
      let at: i32 = ch * len + i;
      if (i < bound) {
        out = out.with(at, out[at] * (upsample as f32));
      } else {
        out = out.with(at, 0.0f32);
      }
    }
  }
  return out;
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
  let log_xc: f32 = energy.celt_log2(1.001f32 - sum * sum);
  let log_xc2: f32 = max16(0.5f32 * log_xc, energy.celt_log2(1.001f32 - min_xc * min_xc));
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

### `dynalloc`

The dynamic allocation boosts a band that stands above a smooth follower of the band energies. A median filter keeps a single high band from a boost. The noise floor of each band, from its width, the mean energy and the pre-emphasis, bounds the follower from below. The same model gives the spreading weight of each band from its signal-to-mask ratio.

```fern
<<dynalloc>>=
const LEAK_BANDS: i32 = 19;

pub function median_of_5(x: [f32], at: i32): f32 {
  let t0: f32 = x[at];
  let t1: f32 = x[at + 1];
  if (x[at] > x[at + 1]) {
    t0 = x[at + 1];
    t1 = x[at];
  }
  let t2: f32 = x[at + 2];
  let t3: f32 = x[at + 3];
  let t4: f32 = x[at + 4];
  if (x[at + 3] > x[at + 4]) {
    t3 = x[at + 4];
    t4 = x[at + 3];
  }
  if (t0 > t3) {
    let (a, b) = (t3, t1);
    t3 = t0;
    t0 = a;
    t1 = t4;
    t4 = b;
  }
  if (t2 > t1) {
    if (t1 < t3) {
      return min16(t2, t3);
    }
    return min16(t4, t1);
  }
  if (t2 < t3) {
    return min16(t1, t3);
  }
  return min16(t2, t4);
}

pub function median_of_3(x: [f32], at: i32): f32 {
  let t0: f32 = x[at];
  let t1: f32 = x[at + 1];
  if (x[at] > x[at + 1]) {
    t0 = x[at + 1];
    t1 = x[at];
  }
  let t2: f32 = x[at + 2];
  if (t1 < t2) {
    return t1;
  }
  if (t0 < t2) {
    return t2;
  }
  return t0;
}

pub struct Dynalloc { max_depth: f32, offsets: i32[], importance: i32[], spread_weight: i32[], tot_boost: i32 }

function noise_floor(end: i32, lsb_depth: i32): f32[] {
  let out: f32[] = [];
  for i in 0..end {
    let n: f32 = 0.0625f32 * (modes.LOGN[i] as f32) + 0.5f32 + (9 - lsb_depth) as f32 - modes.E_MEANS[i] + 0.0062f32 * (((i + 5) * (i + 5)) as f32);
    out = out.append(n);
  }
  return out;
}

function zeros_i(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n {
    out = out.append(0);
  }
  return out;
}

// The spreading weight of each band: 32 for a band well above its mask, and
// less for a band that the mask covers.
function spread_weights(e: [f32], noise: [f32], end: i32, channels: i32, max_depth: f32): i32[] {
  let mask: f32[] = [];
  for i in 0..end {
    let m: f32 = e[i] - noise[i];
    if (channels == 2) {
      m = max16(m, e[modes.BANDS + i] - noise[i]);
    }
    mask = mask.append(m);
  }
  let sig: f32[] = part(mask, 0, end);
  for i in 1..end {
    mask = mask.with(i, max16(mask[i], mask[i - 1] - 2.0f32));
  }
  let i: i32 = end - 2;
  while (i >= 0) {
    mask = mask.with(i, max16(mask[i], mask[i + 1] - 3.0f32));
    i = i - 1;
  }
  let out: i32[] = [];
  for k in 0..end {
    let smr: f32 = sig[k] - max16(max16(0.0f32, max_depth - 12.0f32), mask[k]);
    let shift: i32 = 0 - ((0.5f32 + smr) as f64).floor() as i32;
    if (shift < 0) {
      shift = 0;
    }
    if (shift > 5) {
      shift = 5;
    }
    out = out.append(32 >> shift);
  }
  return out;
}

// The follower of channel `c`: a smooth envelope of the energies, raised to
// their median and to the noise floor.
function follower_of(e2: [f32], c: i32, end: i32, noise: [f32]): f32[] {
  let base: i32 = c * modes.BANDS;
  let f: f32[] = [e2[base]];
  let last: i32 = 0;
  for i in 1..end {
    if (e2[base + i] > e2[base + i - 1] + 0.5f32) {
      last = i;
    }
    f = f.append(min16(f[i - 1] + 1.5f32, e2[base + i]));
  }
  let i: i32 = last - 1;
  while (i >= 0) {
    f = f.with(i, min16(f[i], min16(f[i + 1] + 2.0f32, e2[base + i])));
    i = i - 1;
  }
  for k in 2..end - 2 {
    f = f.with(k, max16(f[k], median_of_5(e2, base + k - 2) - 1.0f32));
  }
  let low: f32 = median_of_3(e2, base) - 1.0f32;
  f = f.with(0, max16(f[0], low)).with(1, max16(f[1], low));
  let high: f32 = median_of_3(e2, base + end - 3) - 1.0f32;
  f = f.with(end - 2, max16(f[end - 2], high)).with(end - 1, max16(f[end - 1], high));
  for k in 0..end {
    f = f.with(k, max16(f[k], noise[k]));
  }
  return f;
}

// The boost of each band above its follower, in the energy units.
function boost_levels(e: [f32], e2: [f32], start: i32, end: i32, channels: i32, noise: [f32], surround: [f32]): f32[] {
  let f0: f32[] = follower_of(e2, 0, end, noise);
  let out: f32[] = part(f0, 0, end);
  if (channels == 2) {
    let f1: f32[] = follower_of(e2, 1, end, noise);
    for i in start..end {
      let b: f32 = max16(f1[i], f0[i] - 4.0f32);
      let a: f32 = max16(f0[i], b - 4.0f32);
      out = out.with(i, 0.5f32 * (max16(0.0f32, e[i] - a) + max16(0.0f32, e[modes.BANDS + i] - b)));
    }
  } else {
    for i in start..end {
      out = out.with(i, max16(0.0f32, e[i] - f0[i]));
    }
  }
  for i in start..end {
    out = out.with(i, max16(out[i], surround[i]));
  }
  return out;
}

function importance_of(levels: [f32], start: i32, end: i32): i32[] {
  let out: i32[] = zeros_i(modes.BANDS);
  for i in start..end {
    let v: f32 = 0.5f32 + 13.0f32 * ((0.6931471805599453094 * (min16(levels[i], 4.0f32) as f64)).exp() as f32);
    out = out.with(i, (v as f64).floor() as i32);
  }
  return out;
}

// The weights of the band boosts: half for CBR and constrained VBR frames
// that are not transients, twice for the low bands, half for the high ones,
// and the leak boost of the tonality analysis.
function weigh_levels(own f: f32[], start: i32, end: i32, halve: boolean, analysis: Analysis): f32[] {
  for i in start..end {
    let v: f32 = f[i];
    if (halve) {
      v = 0.5f32 * v;
    }
    if (i < 8) {
      v = v * 2.0f32;
    }
    if (i >= 12) {
      v = 0.5f32 * v;
    }
    f = f.with(i, v);
  }
  if (analysis.valid) {
    for i in start..min_i(LEAK_BANDS, end) {
      f = f.with(i, f[i] + 0.015625f32 * (analysis.leak_boost[i] as f32));
    }
  }
  return f;
}

// The boost of a band in steps and in eighth bits.
function band_boost(level: f32, width: i32): (i32, i32) {
  if (width < 6) {
    let boost: i32 = level as i32;
    return (boost, boost * width << rate.BITRES);
  }
  if (width > 48) {
    let boost: i32 = (level * 8.0f32) as i32;
    return (boost, (boost * width << rate.BITRES) / 8);
  }
  let boost: i32 = (level * (width as f32) / 6.0f32) as i32;
  return (boost, boost * 6 << rate.BITRES);
}

function max_depth_of(e: [f32], noise: [f32], end: i32, channels: i32): f32 {
  let depth: f32 = -31.9f32;
  for c in 0..channels {
    for i in 0..end {
      depth = max16(depth, e[c * modes.BANDS + i] - noise[i]);
    }
  }
  return depth;
}

// The offset of each band from its boost level, and the total boost in
// eighth bits. CBR and constrained VBR frames that are not transients spend
// at most two thirds of the bits on boosts.
function apply_boosts(levels: [f32], start: i32, end: i32, channels: i32, lm: i32, capped: boolean, effective_bytes: i32): (i32[], i32) {
  let offsets: i32[] = zeros_i(modes.BANDS);
  let tot: i32 = 0;
  for i in start..end {
    let width: i32 = channels * (modes.EBANDS[i + 1] - modes.EBANDS[i]) << lm;
    let (boost, boost_bits) = band_boost(min16(levels[i], 4.0f32), width);
    if (capped && tot + boost_bits >> rate.BITRES >> 3 > 2 * effective_bytes / 3) {
      let cap: i32 = 2 * effective_bytes / 3 << rate.BITRES << 3;
      offsets = offsets.with(i, cap - tot);
      return (offsets, cap);
    }
    offsets = offsets.with(i, boost);
    tot = tot + boost_bits;
  }
  return (offsets, tot);
}

pub function dynalloc_analysis(e: [f32], e2: [f32], start: i32, end: i32, channels: i32, lsb_depth: i32, transient: boolean, vbr: boolean, constrained_vbr: boolean, lm: i32, effective_bytes: i32, lfe: boolean, surround: [f32], analysis: Analysis): Dynalloc {
  let noise: f32[] = noise_floor(end, lsb_depth);
  let max_depth: f32 = max_depth_of(e, noise, end, channels);
  let spread: i32[] = spread_weights(e, noise, end, channels, max_depth);
  if (effective_bytes <= 50 || lm < 1 || lfe) {
    let importance: i32[] = zeros_i(modes.BANDS);
    for i in start..end {
      importance = importance.with(i, 13);
    }
    return Dynalloc { max_depth: max_depth, offsets: zeros_i(modes.BANDS), importance: importance, spread_weight: spread, tot_boost: 0 };
  }
  let levels: f32[] = boost_levels(e, e2, start, end, channels, noise, surround);
  let importance: i32[] = importance_of(levels, start, end);
  levels = weigh_levels(levels, start, end, (!vbr || constrained_vbr) && !transient, analysis);
  let (offsets, tot) = apply_boosts(levels, start, end, channels, lm, !vbr || constrained_vbr && !transient, effective_bytes);
  return Dynalloc { max_depth: max_depth, offsets: offsets, importance: importance, spread_weight: spread, tot_boost: tot };
}
```

### `vbr`

The VBR target starts from the base target of the bitrate. The activity and the tonality of the tonality analysis change it. The stereo saving, the dynamic allocation and the TF estimate change it too. The depth of the signal and the temporal VBR limit it, and it never doubles.

```fern
<<vbr>>=
pub struct VbrRequest { base_target: i32, lm: i32, bitrate: i32, last_coded_bands: i32, channels: i32, intensity: i32, constrained_vbr: boolean, stereo_saving: f32, tot_boost: i32, tf_estimate: f32, pitch_change: boolean, max_depth: f32, lfe: boolean, has_surround_mask: boolean, surround_masking: f32, temporal_vbr: f32 }

function coded_bins_of(r: VbrRequest, coded_bands: i32): i32 {
  let bins: i32 = modes.EBANDS[coded_bands] << r.lm;
  if (r.channels == 2) {
    bins = bins + (modes.EBANDS[min_i(r.intensity, coded_bands)] << r.lm);
  }
  return bins;
}

function stereo_target(r: VbrRequest, target: i32, coded_bands: i32, coded_bins: i32): i32 {
  let bands: i32 = min_i(r.intensity, coded_bands);
  let dof: i32 = (modes.EBANDS[bands] << r.lm) - bands;
  let max_frac: f32 = 0.8f32 * (dof as f32) / (coded_bins as f32);
  let saving: f32 = min16(r.stereo_saving, 1.0f32);
  return target - min32(max_frac * (target as f32), (saving - 0.1f32) * ((dof << rate.BITRES) as f32)) as i32;
}

function analysis_target(r: VbrRequest, analysis: Analysis, target: i32, coded_bins: i32): i32 {
  let tonal: f32 = max16(0.0f32, analysis.tonality - 0.15f32) - 0.12f32;
  let t: i32 = target + ((coded_bins << rate.BITRES) as f32 * 1.2f32 * tonal) as i32;
  if (r.pitch_change) {
    t = t + ((coded_bins << rate.BITRES) as f32 * 0.8f32) as i32;
  }
  return t;
}

// The limits on the target: the depth of the signal, the constrained VBR,
// the temporal VBR and two times the base target.
function limit_target(r: VbrRequest, target: i32): i32 {
  let bins: i32 = modes.EBANDS[modes.BANDS - 2] << r.lm;
  let floor_depth: i32 = ((r.channels * bins << rate.BITRES) as f32 * r.max_depth) as i32;
  if (floor_depth < target >> 2) {
    floor_depth = target >> 2;
  }
  let t: i32 = min_i(target, floor_depth);
  if ((!r.has_surround_mask || r.lfe) && r.constrained_vbr) {
    t = r.base_target + (0.67f32 * ((t - r.base_target) as f32)) as i32;
  }
  if (!r.has_surround_mask && r.tf_estimate < 0.2f32) {
    let amount: f32 = 0.0000031f32 * (max_i(0, min_i(32000, 96000 - r.bitrate)) as f32);
    let factor: f32 = r.temporal_vbr * amount;
    t = t + (factor * (t as f32)) as i32;
  }
  return min_i(2 * r.base_target, t);
}

function max_i(a: i32, b: i32): i32 {
  if (a > b) {
    return a;
  }
  return b;
}

pub function compute_vbr(r: VbrRequest, analysis: Analysis): i32 {
  let coded_bands: i32 = r.last_coded_bands;
  if (coded_bands == 0) {
    coded_bands = modes.BANDS;
  }
  let coded_bins: i32 = coded_bins_of(r, coded_bands);
  let target: i32 = r.base_target;
  if (analysis.valid && analysis.activity as f64 < 0.4) {
    target = target - ((coded_bins << rate.BITRES) as f32 * (0.4f32 - analysis.activity)) as i32;
  }
  if (r.channels == 2) {
    target = stereo_target(r, target, coded_bands, coded_bins);
  }
  target = target + r.tot_boost - (19 << r.lm);
  target = target + ((r.tf_estimate - 0.044f32) * (target as f32)) as i32;
  if (analysis.valid && !r.lfe) {
    target = analysis_target(r, analysis, target, coded_bins);
  }
  if (r.has_surround_mask && !r.lfe) {
    let surround: i32 = target + (r.surround_masking * ((coded_bins << rate.BITRES) as f32)) as i32;
    target = max_i(target / 4, surround);
  }
  return limit_target(r, target);
}
```

### `prefilter`

The prefilter is the inverse of the pitch postfilter of the decoder. It finds the pitch period of the frame at half the rate, and removes the doubled periods. It makes the gain smaller when the network loses packets.

A frame uses the prefilter only when its gain passes a threshold. The threshold is higher when the period jumps or the packet is small. It is lower when the last frame had a strong prefilter. The gain is then quantised to one of eight steps of 3/32.

The filter reads the history of the input in `mem`, and writes the filtered frame over the input. Thus it is an FIR filter: each output is the input minus the taps of the input one period before it. libopus filters the first `shortMdctSize - overlap` samples with only the old filter. That count is 0 in the mode of this port, so the port does not have that step.

```fern
<<prefilter>>=
pub struct PrefilterState { period: i32, gain: f32, tapset: i32, loss_rate: i32 }

// `last_period` is the period of the last frame, at least
// `celt.COMBFILTER_MINPERIOD`. `input`, `mem` and `in_mem` replace the
// arrays that `run_prefilter` takes.
pub struct Prefilter { on: boolean, pitch: i32, gain: f32, qgain: i32, last_period: i32, input: f32[], mem: f32[], in_mem: f32[] }

pub struct PrefilterFrame { channels: i32, n: i32, tapset: i32, enabled: boolean, available_bytes: i32 }

const PF_STEP: f32 = 0.10000000149011612f32;

const PF_FLOOR: f32 = 0.20000000298023224f32;

const PF_STRONG: f32 = 0.4000000059604645f32;

const PF_STRONGER: f32 = 0.550000011920929f32;

const PF_SCALE: f32 = 0.699999988079071f32;

const PF_QUANTUM: f32 = 0.09375f32;

// The history and then the new frame of each channel.
function prefilter_input(input: [f32], mem: [f32], cc: i32, n: i32): f32[] {
  let pre: f32[] = [];
  for c in 0..cc {
    for i in 0..celt.COMBFILTER_MAXPERIOD {
      pre = pre.append(mem[c * celt.COMBFILTER_MAXPERIOD + i]);
    }
    for i in 0..n {
      pre = pre.append(input[c * (n + modes.OVERLAP) + modes.OVERLAP + i]);
    }
  }
  return pre;
}

struct PitchGain { pitch: i32, gain: f32 }

// The period and the gain that the search finds in `pre`.
function prefilter_pitch(pre: [f32], st: PrefilterState, cc: i32, n: i32): PitchGain {
  let len: i32 = celt.COMBFILTER_MAXPERIOD + n;
  let lp: f32[] = pitch.pitch_downsample(pre, 0, pre, len, len, cc);
  let found: i32 = pitch.pitch_search(lp, celt.COMBFILTER_MAXPERIOD >> 1, lp, n, celt.COMBFILTER_MAXPERIOD - 3 * celt.COMBFILTER_MINPERIOD);
  let d: pitch.Doubling = pitch.remove_doubling(lp, celt.COMBFILTER_MAXPERIOD, celt.COMBFILTER_MINPERIOD, n, celt.COMBFILTER_MAXPERIOD - found, st.period, st.gain);
  let p: i32 = d.period;
  if (p > celt.COMBFILTER_MAXPERIOD - 2) {
    p = celt.COMBFILTER_MAXPERIOD - 2;
  }
  let g: f32 = PF_SCALE * d.gain;
  if (st.loss_rate > 2) {
    g = 0.5f32 * g;
  }
  if (st.loss_rate > 4) {
    g = 0.5f32 * g;
  }
  if (st.loss_rate > 8) {
    g = 0.0f32;
  }
  return PitchGain { pitch: p, gain: g };
}

// The gain that the prefilter needs before the frame uses it.
function prefilter_threshold(st: PrefilterState, p: i32, available_bytes: i32): f32 {
  let th: f32 = PF_FLOOR;
  if (abs_i(p - st.period) * 10 > p) {
    th = th + PF_FLOOR;
  }
  if (available_bytes < 25) {
    th = th + PF_STEP;
  }
  if (available_bytes < 35) {
    th = th + PF_STEP;
  }
  if (st.gain > PF_STRONG) {
    th = th - PF_STEP;
  }
  if (st.gain > PF_STRONGER) {
    th = th - PF_STEP;
  }
  if (th > PF_FLOOR) {
    return th;
  }
  return PF_FLOOR;
}

// The quantised gain: the last gain when it is near, then the nearest
// of the eight steps.
function quantised_gain(g: f32, last: f32): i32 {
  let d: f32 = g - last;
  if (d < 0.0f32) {
    d = -d;
  }
  if (d < PF_STEP) {
    g = last;
  }
  let q: i32 = ((0.5f32 + g * 32.0f32 / 3.0f32) as f64).floor() as i32 - 1;
  if (q > 7) {
    return 7;
  }
  if (q < 0) {
    return 0;
  }
  return q;
}

// The prefilter of a frame of `f.n` samples a channel. `input` holds the
// overlap and then the frame of each channel. `mem` holds the last
// `celt.COMBFILTER_MAXPERIOD` input samples of each channel, and `in_mem`
// the last overlap of each channel.
pub function run_prefilter(st: PrefilterState, own input: f32[], mem: [f32], own in_mem: f32[], f: PrefilterFrame, an: Analysis): Prefilter {
  let n: i32 = f.n;
  let len: i32 = celt.COMBFILTER_MAXPERIOD + n;
  let pre: f32[] = prefilter_input(input, mem, f.channels, n);
  let pg: PitchGain = PitchGain { pitch: celt.COMBFILTER_MINPERIOD, gain: 0.0f32 };
  if (f.enabled) {
    pg = prefilter_pitch(pre, st, f.channels, n);
  }
  let gain: f32 = pg.gain;
  if (an.valid) {
    gain = gain * an.max_pitch_ratio;
  }
  let on: boolean = !(gain < prefilter_threshold(st, pg.pitch, f.available_bytes));
  let qg: i32 = 0;
  if (on) {
    qg = quantised_gain(gain, st.gain);
    gain = PF_QUANTUM * ((qg + 1) as f32);
  } else {
    gain = 0.0f32;
  }
  let last: i32 = st.period;
  if (last < celt.COMBFILTER_MINPERIOD) {
    last = celt.COMBFILTER_MINPERIOD;
  }
  let comb: celt.Comb = celt.Comb { t0: last, t1: pg.pitch, n: n, g0: -st.gain, g1: -gain, tapset0: st.tapset, tapset1: f.tapset };
  let next: f32[] = [];
  for c in 0..f.channels {
    let at: i32 = c * (n + modes.OVERLAP);
    for i in 0..modes.OVERLAP {
      input = input.with(at + i, in_mem[c * modes.OVERLAP + i]);
    }
    input = celt.comb_fir(input, at + modes.OVERLAP, pre, c * len + celt.COMBFILTER_MAXPERIOD, comb);
    for i in 0..modes.OVERLAP {
      in_mem = in_mem.with(c * modes.OVERLAP + i, input[at + n + i]);
    }
    for i in 0..celt.COMBFILTER_MAXPERIOD {
      next = next.append(pre[c * len + n + i]);
    }
  }
  return Prefilter { on: on, pitch: pg.pitch, gain: gain, qgain: qg, last_period: last, input: input, mem: next, in_mem: in_mem };
}
```
