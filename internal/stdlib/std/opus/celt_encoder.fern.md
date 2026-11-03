# The CELT encoder

This module is the encoder side of CELT, the transform layer of Opus (RFC 6716, section 4.3). It is a port of the float build of `celt_encoder.c` from libopus 1.4. For the same samples and the same settings, it gives the same bits as libopus.

## Model

The encoder works on frames of 2.5 to 20 ms at 48 kHz. For each frame, it analyses the signal, then codes the frame with the shared parts of `std/opus`: the range coder, the energy quantiser, the band coder and the bit allocation.

The analysis functions come first. Each one makes one decision for the frame:

- `preemphasis` scales the samples to the CELT range and applies the pre-emphasis filter.
- `transient_analysis` decides if the frame has a transient, so that it takes short blocks.
- `patch_transient_decision` finds a sudden energy increase that the transient analysis missed.

## Contracts

- The arithmetic is in f32, in the same order as libopus. Where libopus does a step in f64, this module does the same step in f64.
- The samples of `preemphasis` are interleaved, with `channels` samples for each instant, from -1.0 to 1.0.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<preemphasis>>

<<transients>>
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
import "std/opus/modes";
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
