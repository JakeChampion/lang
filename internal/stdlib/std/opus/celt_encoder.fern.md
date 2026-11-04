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
- `encode` codes a whole frame with these functions, and `new_encoder` gives the state of a new encoder.
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

<<frame>>
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
import "std/opus/bands";
import "std/opus/celt";
import "std/opus/energy";
import "std/opus/mdct";
import "std/opus/modes";
import "std/opus/pitch";
import "std/opus/range";
import "std/opus/rate";
import "std/opus/vq";
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
pub struct Analysis { valid: boolean, tonality: f32, tonality_slope: f32, activity: f32, max_pitch_ratio: f32, bandwidth: i32, leak_boost: i32[] }

pub function no_analysis(): Analysis {
  return Analysis { valid: false, tonality: 0.0f32, tonality_slope: 0.0f32, activity: 0.0f32, max_pitch_ratio: 0.0f32, bandwidth: 0, leak_boost: [] };
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
pub function run_prefilter(st: PrefilterState, own input: f32[], mem: [f32], in_mem: [f32], f: PrefilterFrame, an: Analysis): Prefilter {
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
  let next_in: f32[] = [];
  for c in 0..f.channels {
    let at: i32 = c * (n + modes.OVERLAP);
    for i in 0..modes.OVERLAP {
      input = input.with(at + i, in_mem[c * modes.OVERLAP + i]);
    }
    input = celt.comb_fir(input, at + modes.OVERLAP, pre, c * len + celt.COMBFILTER_MAXPERIOD, comb);
    for i in 0..modes.OVERLAP {
      next_in = next_in.append(input[at + n + i]);
    }
    for i in 0..celt.COMBFILTER_MAXPERIOD {
      next = next.append(pre[c * len + n + i]);
    }
  }
  return Prefilter { on: on, pitch: pg.pitch, gain: gain, qgain: qg, last_period: last, input: input, mem: next, in_mem: next_in };
}
```

### `frame`

`encode` codes one frame of a CELT-only Opus stream: the frame starts at band 0 and has no SILK part. libopus also uses an energy mask and an LFE flag from its multistream layer, and a hybrid mode with SILK. This port does not have them. The frame does not use the custom-mode signalling byte either, as in an Opus stream.

The steps are those of `celt_encode_with_ec` in libopus:

1. The budget: the packet size, the VBR rate, and the bound of the constrained VBR.
2. The silence flag and the pre-emphasis of the input.
3. The prefilter, and its period and gain in the packet.
4. The transient decision, the MDCTs, the band energies and the temporal VBR. A second look at the energies can still select short blocks.
5. The band shapes, the dynamic allocation, the TF resolution and the coarse energy.
6. The spreading, the boost of each band, the intensity band and the allocation trim.
7. The VBR target, which can make the packet smaller.
8. The allocation, the fine energy, the band shapes, and the last energy bits.

The state of the encoder holds the history that the next frame needs. `new_encoder` gives the state after `OPUS_RESET_STATE`, with the defaults of `opus_custom_encoder_init`.

```fern
<<frame>>=
pub const BITRATE_MAX: i32 = -1;

pub const BAD_ARG: i32 = -1;

pub const INTERNAL_ERROR: i32 = -3;

pub struct Encoder { channels: i32, stream_channels: i32, force_intra: boolean, clip: boolean, disable_pf: boolean, complexity: i32, upsample: i32, end: i32, bitrate: i32, vbr: boolean, constrained_vbr: boolean, loss_rate: i32, lsb_depth: i32, disable_inv: boolean, rng: u32, spread_decision: i32, delayed_intra: f32, tonal_average: i32, last_coded_bands: i32, hf_average: i32, tapset_decision: i32, prefilter_period: i32, prefilter_gain: f32, prefilter_tapset: i32, consec_transient: i32, analysis: Analysis, preemph_mem: f32[], vbr_reservoir: i32, vbr_drift: i32, vbr_offset: i32, vbr_count: i32, overlap_max: f32, stereo_saving: f32, intensity: i32, spec_avg: f32, in_mem: f32[], prefilter_mem: f32[], old_band_e: f32[], old_log_e: f32[], old_log_e2: f32[], energy_error: f32[] }

function filled(n: i32, v: f32): f32[] {
  let out: f32[] = [];
  for i in 0..n {
    out = out.append(v);
  }
  return out;
}

pub function new_encoder(channels: i32): Encoder {
  let bands: i32 = channels * modes.BANDS;
  return Encoder { channels: channels, stream_channels: channels, force_intra: false, clip: true, disable_pf: false, complexity: 5, upsample: 1, end: modes.BANDS, bitrate: BITRATE_MAX, vbr: false, constrained_vbr: true, loss_rate: 0, lsb_depth: 24, disable_inv: false, rng: 0u32, spread_decision: vq.SPREAD_NORMAL, delayed_intra: 1.0f32, tonal_average: 256, last_coded_bands: 0, hf_average: 0, tapset_decision: 0, prefilter_period: 0, prefilter_gain: 0.0f32, prefilter_tapset: 0, consec_transient: 0, analysis: no_analysis(), preemph_mem: filled(2, 0.0f32), vbr_reservoir: 0, vbr_drift: 0, vbr_offset: 0, vbr_count: 0, overlap_max: 0.0f32, stereo_saving: 0.0f32, intensity: 0, spec_avg: 0.0f32, in_mem: filled(channels * modes.OVERLAP, 0.0f32), prefilter_mem: filled(channels * celt.COMBFILTER_MAXPERIOD, 0.0f32), old_band_e: filled(bands, 0.0f32), old_log_e: filled(bands, -28.0f32), old_log_e2: filled(bands, -28.0f32), energy_error: filled(bands, 0.0f32) };
}

// `packet` is empty when `error` is not 0.
pub struct Encoded { st: Encoder, packet: u8[], error: i32 }

// The sizes of the frame in bytes and bits. `bytes` is the packet size,
// `available` the bytes that the frame can still use, and `effective` the
// size that the analysis assumes.
struct Budget { lm: i32, n: i32, bytes: i32, available: i32, effective: i32, vbr_rate: i32, equiv_rate: i32, total_bits: i32 }

function frame_lm(frame_size: i32): i32 {
  for lm in 0..modes.MAX_LM + 1 {
    if (modes.SHORT_MDCT_SIZE << lm == frame_size) {
      return lm;
    }
  }
  return -1;
}

function budget(st: Encoder, frame_size: i32, lm: i32, max_bytes: i32): (Budget, range.Encoder) {
  let c: i32 = st.stream_channels;
  let bytes: i32 = min_i(max_bytes, 1275);
  // libopus takes the available bytes before the CBR size.
  let available: i32 = bytes;
  let vbr_rate: i32 = 0;
  let effective: i32 = 0;
  if (st.vbr && st.bitrate != BITRATE_MAX) {
    let den: i32 = modes.SAMPLE_RATE >> rate.BITRES;
    vbr_rate = (st.bitrate * frame_size + (den >> 1)) / den;
    effective = vbr_rate >> 3 + rate.BITRES;
  } else {
    if (st.bitrate != BITRATE_MAX) {
      bytes = max_i(2, min_i(bytes, (st.bitrate * frame_size + 4 * modes.SAMPLE_RATE) / (8 * modes.SAMPLE_RATE)));
    }
    effective = bytes;
  }
  let overhead: i32 = (40 * c + 20) * ((400 >> lm) - 50);
  let equiv_rate: i32 = (bytes * 8 * 50 << 3 - lm) - overhead;
  if (st.bitrate != BITRATE_MAX) {
    equiv_rate = min_i(equiv_rate, st.bitrate - overhead);
  }
  let e: range.Encoder = range.encoder(bytes);
  if (vbr_rate > 0 && st.constrained_vbr) {
    let allowed: i32 = min_i(max_i(2, vbr_rate + vbr_rate - st.vbr_reservoir >> rate.BITRES + 3), available);
    if (allowed < available) {
      bytes = allowed;
      available = allowed;
      e = e.shrink(bytes);
    }
  }
  return (Budget { lm: lm, n: modes.SHORT_MDCT_SIZE << lm, bytes: bytes, available: available, effective: effective, vbr_rate: vbr_rate, equiv_rate: equiv_rate, total_bits: bytes * 8 }, e);
}

function max_abs(x: [f32], at: i32, n: i32): f32 {
  let hi: f32 = 0.0f32;
  let lo: f32 = 0.0f32;
  for i in at..at + n {
    hi = max16(hi, x[i]);
    lo = min16(lo, x[i]);
  }
  return max16(hi, -lo);
}

struct Input { st: Encoder, e: range.Encoder, b: Budget, input: f32[], silence: boolean, tell: i32 }

// The silence flag, and the pre-emphasised input of each channel after
// one overlap of space.
function frame_input(own st: Encoder, own e: range.Encoder, b0: Budget, pcm: [f32]): Input {
  let b: Budget = b0;
  let c: i32 = st.stream_channels;
  let cc: i32 = st.channels;
  let n: i32 = b.n;
  let head: i32 = c * (n - modes.OVERLAP) / st.upsample;
  let peak: f32 = max16(st.overlap_max, max_abs(pcm, 0, head));
  st = Encoder { ...st, overlap_max: max_abs(pcm, head, c * modes.OVERLAP / st.upsample) };
  peak = max16(peak, st.overlap_max);
  let silence: boolean = peak <= 1.0f32 / ((1 << st.lsb_depth) as f32);
  e = e.bit_logp(silence, 15);
  let tell: i32 = 1;
  if (silence) {
    if (b.vbr_rate > 0) {
      let bytes: i32 = min_i(b.bytes, 2);
      e = e.shrink(bytes);
      b = Budget { ...b, bytes: bytes, effective: bytes, total_bits: bytes * 8, available: 2 };
    }
    tell = b.bytes * 8;
    let used: i32 = e.tell();
    e = range.Encoder { ...e, nbits_total: e.nbits_total + tell - used };
  }
  let input: f32[] = filled(cc * (n + modes.OVERLAP), 0.0f32);
  let mem: f32[] = st.preemph_mem;
  for ch in 0..cc {
    let p: Preemphasis = preemphasis(pcm, ch, n, cc, st.upsample, mem[ch], st.clip && peak > 65536.0f32);
    mem = mem.with(ch, p.mem);
    for i in 0..n {
      input = input.with(ch * (n + modes.OVERLAP) + modes.OVERLAP + i, p.samples[i]);
    }
  }
  return Input { st: Encoder { ...st, preemph_mem: mem }, e: e, b: b, input: input, silence: silence, tell: tell };
}

function ilog_i(v: i32): i32 {
  let n: i32 = 0;
  while (v >> n > 0) {
    n = n + 1;
  }
  return n;
}

// The prefilter of the frame: `on`, its period, gain and tapset, and if
// the period changed much.
struct Filter { on: boolean, pitch: i32, gain: f32, tapset: i32, change: boolean }

struct Pitch { st: Encoder, e: range.Encoder, input: f32[], f: Filter }

function pitch_changed(st: Encoder, pitch: i32, gain: f32): boolean {
  if (!(gain > 0.4000000059604645f32 || st.prefilter_gain > 0.4000000059604645f32)) {
    return false;
  }
  if (st.analysis.valid && !(st.analysis.tonality as f64 > 0.3)) {
    return false;
  }
  let last: f64 = st.prefilter_period as f64;
  return pitch as f64 > 1.26 * last || pitch as f64 < 0.79 * last;
}

// The prefilter of the frame, and its period, gain and tapset in the
// packet.
function frame_pitch(own st: Encoder, own e: range.Encoder, own input: f32[], b: Budget, silence: boolean, tell: i32): Pitch {
  let c: i32 = st.stream_channels;
  let enabled: boolean = b.available > 12 * c && !silence && !st.disable_pf && st.complexity >= 5;
  let tapset: i32 = st.tapset_decision;
  let pf: Prefilter = run_prefilter(PrefilterState { period: st.prefilter_period, gain: st.prefilter_gain, tapset: st.prefilter_tapset, loss_rate: st.loss_rate }, input, st.prefilter_mem, st.in_mem, PrefilterFrame { channels: st.channels, n: b.n, tapset: tapset, enabled: enabled, available_bytes: b.available }, st.analysis);
  st = Encoder { ...st, prefilter_period: pf.last_period, prefilter_mem: pf.mem, in_mem: pf.in_mem };
  let change: boolean = pitch_changed(st, pf.pitch, pf.gain);
  if (!pf.on) {
    if (tell + 16 <= b.total_bits) {
      e = e.bit_logp(false, 1);
    }
    return Pitch { st: st, e: e, input: pf.input, f: Filter { on: false, pitch: pf.pitch, gain: pf.gain, tapset: tapset, change: change } };
  }
  let p: i32 = pf.pitch + 1;
  let octave: i32 = ilog_i(p) - 5;
  e = e.bit_logp(true, 1).uint(octave as u32, 6u32).bits((p - (16 << octave)) as u32, 4 + octave).bits(pf.qgain as u32, 3).icdf(tapset, celt.TAPSET_ICDF, 2);
  return Pitch { st: st, e: e, input: pf.input, f: Filter { on: true, pitch: pf.pitch, gain: pf.gain, tapset: tapset, change: change } };
}

// The spectrum, the band amplitudes and the band energies for one block
// size.
struct Spectrum { freq: f32[], band_e: f32[], log_e: f32[] }

function spectrum_of(input: [f32], short_blocks: i32, st: Encoder, lm: i32, eff_end: i32): Spectrum {
  let c: i32 = st.stream_channels;
  let freq: f32[] = compute_mdcts(input, short_blocks, c, st.channels, lm, st.upsample);
  let band_e: f32[] = bands.compute_band_energies(freq, eff_end, c, lm);
  return Spectrum { freq: freq, band_e: band_e, log_e: energy.amp2log2(band_e, eff_end, st.end, c) };
}

// The energies `e` with `0.5 * lm` added to each coded band, to match the
// scale of short blocks.
function short_scaled(e: [f32], c: i32, end: i32, lm: i32): f32[] {
  let out: f32[] = [];
  for ch in 0..e.len() / modes.BANDS {
    for i in 0..modes.BANDS {
      let v: f32 = e[modes.BANDS * ch + i];
      if (ch < c && i < end) {
        v = v + 0.5f32 * (lm as f32);
      }
      out = out.append(v);
    }
  }
  return out;
}

// The change of the mean band energy from the long-term mean, which the
// VBR target follows.
function temporal_vbr(st: Encoder, log_e: [f32], short_blocks: i32, lm: i32): (f32, f32) {
  let follow: f32 = -10.0f32;
  let avg: f32 = 0.0f32;
  let offset: f32 = 0.0f32;
  if (short_blocks > 0) {
    offset = 0.5f32 * (lm as f32);
  }
  for i in 0..st.end {
    follow = max16(follow - 1.0f32, log_e[i] - offset);
    if (st.stream_channels == 2) {
      follow = max16(follow, log_e[i + modes.BANDS] - offset);
    }
    avg = avg + follow;
  }
  avg = avg / (st.end as f32);
  let tv: f32 = min16(3.0f32, max16(-1.5f32, avg - st.spec_avg));
  return (tv, st.spec_avg + 0.0199999995529651641845703125f32 * tv);
}

struct Analysed { transient: boolean, disabled: boolean, short_blocks: i32, tf_estimate: f32, tf_chan: i32, s: Spectrum, log_e2: f32[], temporal_vbr: f32, spec_avg: f32 }

// The transient decision and the spectrum of the frame.
function frame_spectrum(e: range.Encoder, st: Encoder, input: [f32], b: Budget): Analysed {
  let lm: i32 = b.lm;
  let c: i32 = st.stream_channels;
  let eff_end: i32 = min_i(st.end, modes.BANDS);
  let t: Transient = Transient { transient: false, weak: false, tf_estimate: 0.0f32, tf_chan: 0 };
  if (st.complexity >= 1) {
    t = transient_analysis(input, b.n + modes.OVERLAP, st.channels, false);
  }
  let transient: boolean = t.transient;
  let disabled: boolean = false;
  let short_blocks: i32 = 0;
  if (lm > 0 && e.tell() + 3 <= b.total_bits) {
    if (transient) {
      short_blocks = 1 << lm;
    }
  } else {
    transient = false;
    disabled = true;
  }
  let log_e2: f32[] = [];
  if (short_blocks > 0 && st.complexity >= 8) {
    log_e2 = short_scaled(spectrum_of(input, 0, st, lm, eff_end).log_e, c, st.end, lm);
  }
  let s: Spectrum = spectrum_of(input, short_blocks, st, lm, eff_end);
  let tf_chan: i32 = t.tf_chan;
  if (st.channels == 2 && c == 1) {
    tf_chan = 0;
  }
  if (log_e2.len() == 0) {
    log_e2 = s.log_e;
  }
  let (tv, spec_avg) = temporal_vbr(st, s.log_e, short_blocks, lm);
  return Analysed { transient: transient, disabled: disabled, short_blocks: short_blocks, tf_estimate: t.tf_estimate, tf_chan: tf_chan, s: s, log_e2: log_e2, temporal_vbr: tv, spec_avg: spec_avg };
}

// The late transient decision from the energies, which selects short
// blocks when the energy rises fast. Then the transient flag.
function frame_patch(own e: range.Encoder, own a: Analysed, st: Encoder, input: [f32], b: Budget): (range.Encoder, Analysed) {
  let lm: i32 = b.lm;
  let room: boolean = lm > 0 && e.tell() + 3 <= b.total_bits;
  if (room && !a.transient && st.complexity >= 5 && patch_transient_decision(a.s.log_e, st.old_band_e, modes.BANDS, 0, st.end, st.stream_channels)) {
    let s: Spectrum = spectrum_of(input, 1 << lm, st, lm, min_i(st.end, modes.BANDS));
    a = Analysed { ...a, transient: true, short_blocks: 1 << lm, s: s, log_e2: short_scaled(a.log_e2, st.stream_channels, st.end, lm), tf_estimate: 0.2000000029802322387695312f32 };
  }
  if (room) {
    e = e.bit_logp(a.transient, 3);
  }
  return (e, a);
}

// The TF resolution of each band below `end`.
function frame_tf(a: Analysed, st: Encoder, x: [f32], b: Budget, importance: [i32]): Tf {
  let eff_end: i32 = min_i(st.end, modes.BANDS);
  let flag: i32 = 0;
  if (a.transient) {
    flag = 1;
  }
  let res: i32[] = [];
  if (b.effective >= 15 * st.stream_channels && st.complexity >= 2) {
    let lambda: i32 = max_i(80, 20480 / b.effective + 2);
    let tf: Tf = tf_analysis(eff_end, a.transient, lambda, x, b.n, b.lm, a.tf_estimate, a.tf_chan, importance);
    res = tf.res;
    for i in eff_end..modes.BANDS {
      res = res.append(res[eff_end - 1]);
    }
    return Tf { res: res, select: tf.select };
  }
  for i in 0..modes.BANDS {
    res = res.append(flag);
  }
  return Tf { res: res, select: 0 };
}

// The band energies, moved by a quarter of the coding error of the last
// frame when the energy is stable. A constant offset sounds better than a
// gain that changes.
function biased(log_e: [f32], st: Encoder): f32[] {
  let out: f32[] = [];
  for at in 0..log_e.len() {
    let v: f32 = log_e[at];
    if (at < st.stream_channels * modes.BANDS && at % modes.BANDS < st.end && abs16(v - st.old_band_e[at]) < 2.0f32) {
      v = v - st.energy_error[at] * 0.25f32;
    }
    out = out.append(v);
  }
  return out;
}

// The spreading of the frame, and the state that the decision keeps.
function frame_spread(own st: Encoder, own e: range.Encoder, x: [f32], b: Budget, short_blocks: i32, pf_on: boolean, weights: [i32]): (Encoder, range.Encoder) {
  if (e.tell() + 4 > b.total_bits) {
    return (st, e);
  }
  let c: i32 = st.stream_channels;
  if (short_blocks > 0 || st.complexity < 3 || b.available < 10 * c) {
    let spread: i32 = vq.SPREAD_NORMAL;
    if (st.complexity == 0) {
      spread = vq.SPREAD_NONE;
    }
    st = Encoder { ...st, spread_decision: spread };
  } else {
    let s: bands.Spread = bands.spreading_decision(x, bands.Spread { decision: st.spread_decision, average: st.tonal_average, hf_average: st.hf_average, tapset: st.tapset_decision }, pf_on && short_blocks == 0, min_i(st.end, modes.BANDS), c, 1 << b.lm, weights);
    st = Encoder { ...st, spread_decision: s.decision, tonal_average: s.average, hf_average: s.hf_average, tapset_decision: s.tapset };
  }
  let spread: i32 = st.spread_decision;
  return (st, e.icdf(spread, celt.SPREAD_ICDF, 5));
}

struct Boosts { e: range.Encoder, offsets: i32[], total_boost: i32, tell: i32 }

// The boost of each band in the packet: a flag for each step of boost,
// up to the boost that the analysis asks for.
function frame_boosts(own e: range.Encoder, offsets: [i32], cap: [i32], c: i32, lm: i32, total: i32, end: i32): Boosts {
  let logp: i32 = 6;
  let total_boost: i32 = 0;
  let tell: i32 = e.tell_frac();
  let out: i32[] = [];
  for i in 0..modes.BANDS {
    if (i >= end) {
      out = out.append(offsets[i]);
      continue;
    }
    let width: i32 = c * (modes.EBANDS[i + 1] - modes.EBANDS[i]) << lm;
    let quanta: i32 = min_i(width << rate.BITRES, max_i(6 << rate.BITRES, width));
    let loop_logp: i32 = logp;
    let boost: i32 = 0;
    let j: i32 = 0;
    while (tell + (loop_logp << rate.BITRES) < total - total_boost && boost < cap[i]) {
      let more: boolean = j < offsets[i];
      e = e.bit_logp(more, loop_logp);
      tell = e.tell_frac();
      if (!more) {
        break;
      }
      boost = boost + quanta;
      total_boost = total_boost + quanta;
      loop_logp = 1;
      j = j + 1;
    }
    if (j > 0) {
      logp = max_i(2, logp - 1);
    }
    out = out.append(boost);
  }
  return Boosts { e: e, offsets: out, total_boost: total_boost, tell: tell };
}

const INTENSITY_THRESHOLDS: f32[] = [1.0f32, 2.0f32, 3.0f32, 4.0f32, 5.0f32, 6.0f32, 7.0f32, 8.0f32, 16.0f32, 24.0f32, 36.0f32, 44.0f32, 50.0f32, 56.0f32, 62.0f32, 67.0f32, 72.0f32, 79.0f32, 88.0f32, 106.0f32, 134.0f32];

const INTENSITY_HYSTERESIS: f32[] = [1.0f32, 1.0f32, 1.0f32, 1.0f32, 1.0f32, 1.0f32, 1.0f32, 2.0f32, 2.0f32, 2.0f32, 2.0f32, 2.0f32, 2.0f32, 2.0f32, 3.0f32, 3.0f32, 4.0f32, 5.0f32, 6.0f32, 8.0f32, 8.0f32];

// The dual stereo flag and the intensity band of a stereo frame.
function frame_stereo(own st: Encoder, x: [f32], b: Budget): (Encoder, boolean) {
  if (st.stream_channels != 2) {
    return (st, false);
  }
  let dual: boolean = false;
  if (b.lm != 0) {
    dual = stereo_analysis(x, b.lm, b.n);
  }
  let intensity: i32 = bands.hysteresis_decision((b.equiv_rate / 1000) as f32, INTENSITY_THRESHOLDS, INTENSITY_HYSTERESIS, modes.BANDS, st.intensity);
  return (Encoder { ...st, intensity: min_i(st.end, max_i(0, intensity)) }, dual);
}

struct Sized { st: Encoder, e: range.Encoder, bytes: i32 }

// The VBR size of the packet: the target of `compute_vbr`, at least the
// bits so far, then the reservoir and the drift of the constrained VBR.
function frame_vbr(own st: Encoder, own e: range.Encoder, b: Budget, r: VbrRequest, silence: boolean, tell: i32, total_boost: i32): Sized {
  let lm_diff: i32 = modes.MAX_LM - b.lm;
  let bytes: i32 = min_i(b.bytes, 1275 >> 3 - b.lm);
  let target: i32 = compute_vbr(r, st.analysis) + tell;
  let min_allowed: i32 = (tell + total_boost + (1 << rate.BITRES + 3) - 1 >> rate.BITRES + 3) + 2;
  let available: i32 = min_i(bytes, max_i(min_allowed, target + (1 << rate.BITRES + 2) >> rate.BITRES + 3));
  let delta: i32 = target - b.vbr_rate;
  target = available << rate.BITRES + 3;
  if (silence) {
    available = 2;
    target = 2 * 8 << rate.BITRES;
    delta = 0;
  }
  let alpha: f32 = 0.001000000047497451305389404296875f32;
  if (st.vbr_count < 970) {
    st = Encoder { ...st, vbr_count: st.vbr_count + 1 };
    alpha = 1.0f32 / ((st.vbr_count + 20) as f32);
  }
  if (st.constrained_vbr) {
    let drift: i32 = st.vbr_drift + (alpha * ((delta * (1 << lm_diff) - st.vbr_offset - st.vbr_drift) as f32)) as i32;
    st = Encoder { ...st, vbr_reservoir: st.vbr_reservoir + target - b.vbr_rate, vbr_drift: drift, vbr_offset: -drift };
    if (st.vbr_reservoir < 0) {
      if (!silence) {
        available = available + -st.vbr_reservoir / (8 << rate.BITRES);
      }
      st = Encoder { ...st, vbr_reservoir: 0 };
    }
  }
  bytes = min_i(bytes, available);
  return Sized { st: st, e: e.shrink(bytes), bytes: bytes };
}

function signal_bandwidth(st: Encoder, equiv_rate: i32): i32 {
  if (!st.analysis.valid) {
    return st.end - 1;
  }
  let c: i32 = st.stream_channels;
  let min_bandwidth: i32 = 20;
  if (equiv_rate < 32000 * c) {
    min_bandwidth = 13;
  } else if (equiv_rate < 48000 * c) {
    min_bandwidth = 16;
  } else if (equiv_rate < 60000 * c) {
    min_bandwidth = 18;
  } else if (equiv_rate < 80000 * c) {
    min_bandwidth = 19;
  }
  return max_i(st.analysis.bandwidth, min_bandwidth);
}

function encoding_of(c: range.Coder): range.Encoder {
  match (c) {
    range.Encoding(e) => {
      return e;
    },
    range.Decoding(_) => {
      return range.encoder(0);
    }
  }
}

function half(x: [f32], at: i32, n: i32): f32[] {
  let out: f32[] = [];
  for i in at..at + n {
    out = out.append(x[i]);
  }
  return out;
}

struct Coded { st: Encoder, e: range.Encoder, old: f32[], error: f32[] }

// The allocation, the fine energy, the band shapes, the anti-collapse
// flag and the last energy bits of the frame.
function frame_bands(own st: Encoder, own e: range.Encoder, own old: f32[], own error: f32[], q: Quantised, bytes: i32): Coded {
  let c: i32 = st.stream_channels;
  let lm: i32 = q.lm;
  let bits: i32 = (bytes * 8 << rate.BITRES) - e.tell_frac() - 1;
  let rsv: i32 = 0;
  if (q.transient && lm >= 2 && bits >= lm + 2 << rate.BITRES) {
    rsv = 1 << rate.BITRES;
  }
  let areq: rate.Request = rate.Request { start: 0, end: st.end, offsets: q.offsets, cap: q.cap, alloc_trim: q.trim, intensity: st.intensity, dual_stereo: q.dual_stereo, total: bits - rsv, channels: c, lm: lm, prev: st.last_coded_bands, signal_bandwidth: signal_bandwidth(st, q.equiv_rate) };
  let (c1, a) = rate.compute_allocation(range.Encoding(e), areq);
  let last: i32 = a.coded_bands;
  if (st.last_coded_bands != 0) {
    last = min_i(st.last_coded_bands + 1, max_i(st.last_coded_bands - 1, a.coded_bands));
  }
  st = Encoder { ...st, intensity: a.intensity, last_coded_bands: last };
  let energy.Fine { e: fe, old: fold, error: ferr } = energy.quant_fine_energy(encoding_of(c1), 0, st.end, old, error, a.ebits, c);
  let breq: bands.Request = bands.Request { start: 0, end: st.end, short_blocks: q.short_blocks > 0, spread: st.spread_decision, dual_stereo: a.dual_stereo, intensity: a.intensity, tf_res: q.tf_res, total_bits: bytes * (8 << rate.BITRES) - rsv, balance: a.balance, lm: lm, coded_bands: a.coded_bands, pulses: a.pulses, seed: st.rng, complexity: st.complexity, disable_inv: st.disable_inv, channels: c };
  let y: f32[] = [];
  if (c == 2) {
    y = half(q.x, q.n, q.n);
  }
  let res: bands.Result = bands.quant_all_bands(range.Encoding(fe), breq, half(q.x, 0, q.n), y, q.band_e);
  let e2: range.Encoder = encoding_of(res.c);
  if (rsv > 0) {
    let on: u32 = 0u32;
    if (st.consec_transient < 2) {
      on = 1u32;
    }
    e2 = e2.bits(on, 1);
  }
  let left: i32 = bytes * 8 - e2.tell();
  let energy.Fine { e: e3, old: old3, error: err3 } = energy.quant_energy_finalise(e2, 0, st.end, fold, ferr, a.ebits, a.fine_priority, left, c);
  return Coded { st: st, e: e3, old: old3, error: err3 };
}

// The inputs of `frame_bands`.
struct Quantised { lm: i32, n: i32, transient: boolean, short_blocks: i32, offsets: i32[], cap: i32[], trim: i32, dual_stereo: boolean, equiv_rate: i32, tf_res: i32[], x: f32[], band_e: f32[] }

// The coding error of each coded band, at most half a step.
function clamped_error(error: [f32], st: Encoder): f32[] {
  let nb: i32 = modes.BANDS;
  let out: f32[] = filled(st.channels * nb, 0.0f32);
  for ch in 0..st.stream_channels {
    for i in 0..st.end {
      out = out.with(i + ch * nb, max16(-0.5f32, min16(0.5f32, error[i + ch * nb])));
    }
  }
  return out;
}

// The coded energies, with the silence floor, and the mono energies
// copied to the second channel of a stereo input.
function coded_energies(own old: f32[], st: Encoder, silence: boolean): f32[] {
  let nb: i32 = modes.BANDS;
  if (silence) {
    for i in 0..st.stream_channels * nb {
      old = old.with(i, -28.0f32);
    }
  }
  if (st.channels == 2 && st.stream_channels == 1) {
    for i in 0..nb {
      old = old.with(nb + i, old[i]);
    }
  }
  return old;
}

struct History { old: f32[], log_e: f32[], log_e2: f32[] }

// The energies of the last two frames, which the loss concealment and the
// anti-collapse of the decoder use. A transient keeps the lower energy.
// The bands from `end` are reset.
function history(own old: f32[], st: Encoder, transient: boolean): History {
  let n: i32 = st.channels * modes.BANDS;
  let log_e: f32[] = st.old_log_e;
  let log_e2: f32[] = st.old_log_e2;
  if (!transient) {
    log_e2 = log_e;
    log_e = half(old, 0, n);
  } else {
    for i in 0..n {
      log_e = log_e.with(i, min16(log_e[i], old[i]));
    }
  }
  for i in 0..n {
    if (i % modes.BANDS >= st.end) {
      old = old.with(i, 0.0f32);
      log_e = log_e.with(i, -28.0f32);
      log_e2 = log_e2.with(i, -28.0f32);
    }
  }
  return History { old: old, log_e: log_e, log_e2: log_e2 };
}

// The state for the next frame: the coding error, the band energies of
// the last frames, and the prefilter.
function next_state(own st: Encoder, own old: f32[], error: [f32], f: Filter, a: Analysed, silence: boolean): Encoder {
  let energy_error: f32[] = clamped_error(error, st);
  let History { old: old2, log_e, log_e2 } = history(coded_energies(old, st, silence), st, a.transient);
  let consec: i32 = 0;
  if (a.transient || a.disabled) {
    consec = st.consec_transient + 1;
  }
  return Encoder { ...st, energy_error: energy_error, old_band_e: old2, old_log_e: log_e, old_log_e2: log_e2, prefilter_period: f.pitch, prefilter_gain: f.gain, prefilter_tapset: f.tapset, consec_transient: consec, spec_avg: a.spec_avg };
}

// One frame of `frame_size` samples a channel, interleaved in `pcm` from
// -1.0 to 1.0, in a packet of at most `max_bytes` bytes.
pub function encode(own st: Encoder, pcm: [f32], frame_size: i32, max_bytes: i32): Encoded {
  let lm: i32 = frame_lm(frame_size * st.upsample);
  if (max_bytes < 2 || lm < 0) {
    return Encoded { st: st, packet: [], error: BAD_ARG };
  }
  let (b0, e0) = budget(st, frame_size * st.upsample, lm, max_bytes);
  let Input { st: st0, e: e1, b, input: input0, silence, tell: tell0 } = frame_input(st, e0, b0, pcm);
  let Pitch { st: st1, e: e2, input, f } = frame_pitch(st0, e1, input0, b, silence, tell0);
  st = st1;
  let spectrum: Analysed = frame_spectrum(e2, st, input, b);
  let (e3, a) = frame_patch(e2, spectrum, st, input, b);
  let coded: Coded = frame_rest(st, e3, a, f, b, silence);
  let Coded { st: st2, e: e4, old, error } = coded;
  st = next_state(st2, old, error, f, a, silence);
  st = Encoder { ...st, rng: e4.rng };
  let done: range.Encoder = e4.done();
  if (done.error) {
    return Encoded { st: st, packet: [], error: INTERNAL_ERROR };
  }
  return Encoded { st: st, packet: done.bytes(), error: 0 };
}

// The frame from its band shapes to its last energy bits.
function frame_rest(own st: Encoder, own e: range.Encoder, a: Analysed, f: Filter, b: Budget, silence: boolean): Coded {
  let lm: i32 = b.lm;
  let c: i32 = st.stream_channels;
  let eff_end: i32 = min_i(st.end, modes.BANDS);
  let x: f32[] = bands.normalise_bands(a.s.freq, a.s.band_e, eff_end, c, 1 << lm);
  let alloc: Dynalloc = dynalloc_analysis(a.s.log_e, a.log_e2, 0, st.end, c, st.lsb_depth, a.transient, st.vbr, st.constrained_vbr, lm, b.effective, false, filled(c * modes.BANDS, 0.0f32), st.analysis);
  let Tf { res: res0, select } = frame_tf(a, st, x, b, alloc.importance);
  let log_e: f32[] = biased(a.s.log_e, st);
  let energy.Coarse { e: ce, old: cold, error: cerr, delayed_intra: delayed } = energy.quant_coarse_energy(e, energy.CoarseRequest { start: 0, end: st.end, eff_end: eff_end, budget: b.total_bits, channels: c, lm: lm, available_bytes: b.available, force_intra: st.force_intra, delayed_intra: st.delayed_intra, two_pass: st.complexity >= 4, loss_rate: st.loss_rate, lfe: false }, log_e, st.old_band_e, filled(c * modes.BANDS, 0.0f32));
  st = Encoder { ...st, delayed_intra: delayed };
  let (e1, tf_res) = tf_encode(ce, 0, st.end, a.transient, res0, lm, select);
  let (st1, e2) = frame_spread(st, e1, x, b, a.short_blocks, f.on, alloc.spread_weight);
  let cap: i32[] = celt.init_caps(lm, c);
  let Boosts { e: e3, offsets, total_boost, tell: tell0 } = frame_boosts(e2, alloc.offsets, cap, c, lm, b.total_bits << rate.BITRES, st1.end);
  let (st2, dual) = frame_stereo(st1, x, b);
  st = st2;
  let tell: i32 = tell0;
  let trim: i32 = 5;
  if (tell + (6 << rate.BITRES) <= (b.total_bits << rate.BITRES) - total_boost) {
    let t: Trim = alloc_trim_analysis(x, log_e, st.end, lm, c, b.n, st.analysis, st.stereo_saving, a.tf_estimate, st.intensity, 0.0f32, b.equiv_rate);
    trim = t.index;
    st = Encoder { ...st, stereo_saving: t.stereo_saving };
    e3 = e3.icdf(trim, celt.TRIM_ICDF, 7);
    tell = e3.tell_frac();
  }
  let bytes: i32 = b.bytes;
  if (b.vbr_rate > 0) {
    let base: i32 = b.vbr_rate - (40 * c + 20 << rate.BITRES);
    if (st.constrained_vbr) {
      base = base + (st.vbr_offset >> modes.MAX_LM - lm);
    }
    let r: VbrRequest = VbrRequest { base_target: base, lm: lm, bitrate: b.equiv_rate, last_coded_bands: st.last_coded_bands, channels: c, intensity: st.intensity, constrained_vbr: st.constrained_vbr, stereo_saving: st.stereo_saving, tot_boost: alloc.tot_boost, tf_estimate: a.tf_estimate, pitch_change: f.change, max_depth: alloc.max_depth, lfe: false, has_surround_mask: false, surround_masking: 0.0f32, temporal_vbr: a.temporal_vbr };
    let Sized { st: st3, e: e4, bytes: sized } = frame_vbr(st, e3, b, r, silence, tell, total_boost);
    st = st3;
    e3 = e4;
    bytes = sized;
  }
  let q: Quantised = Quantised { lm: lm, n: b.n, transient: a.transient, short_blocks: a.short_blocks, offsets: offsets, cap: cap, trim: trim, dual_stereo: dual, equiv_rate: b.equiv_rate, tf_res: tf_res, x: x, band_e: a.s.band_e };
  return frame_bands(st, e3, cold, cerr, q, bytes);
}
```
