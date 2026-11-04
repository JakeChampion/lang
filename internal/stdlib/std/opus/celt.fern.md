# The CELT decoder

This module decodes CELT frames (RFC 6716, section 4.3). It is a port of the float build of `celt_decoder.c` from libopus 1.4, with the parts of `celt.c` and `bands.c` that the decoder uses.

## Model

A `Decoder` keeps the state between frames: the energy of each band, the synthesis memory of each channel, the pitch postfilter, and the de-emphasis filter. Each call to `decode` reads one frame from a range decoder and gives its samples.

A frame is 120, 240, 480 or 960 samples at 48 kHz. The decoder reads the header flags, the coarse energy, the time-frequency resolution, the spread and the allocation. Then it reads the fine energy and the band shapes through `std/opus/bands`. It scales each band by its energy, runs the inverse MDCT of `std/opus/mdct`, and applies the pitch postfilter and the de-emphasis.

The Opus layer can set some values for each frame: the number of channels in the stream, the first band (17 in hybrid frames), and the last band (smaller for narrower audio). A stream of one channel can play on two channels, and a stream of two channels can play on one.

## Contracts

- `decoder` takes a sample rate of 48000, 24000, 16000, 12000 or 8000, and 1 or 2 channels. At a rate below 48000, the decoder keeps one sample in each `48000 / rate`.
- `Frame.frame_size` is the number of samples at the sample rate of the decoder.
- The packet of a frame has at most 1275 bytes. A packet of 0 or 1 byte is a lost frame, and the decoder conceals it.
- The samples are f32 values in the range -1 to 1, and the channels are interleaved.
- The arithmetic is in f32, in the same order as libopus, so the results are the same bits. The decoder calls `exp` and `sqrt` of `std/f64` where libopus calls the C library.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<state>>

<<header>>

<<energy scaling>>

<<anti-collapse>>

<<postfilter>>

<<synthesis>>

<<loss concealment>>

<<frame>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT decoder, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2010 Xiph.Org Foundation
// Copyright (c) 2008 Gregory Maxwell
// Written by Jean-Marc Valin and Gregory Maxwell
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
import "core/cmp";
import "core/error";
import "std/f64";
import "std/opus/bands";
import "std/opus/energy";
import "std/opus/mdct";
import "std/opus/modes";
import "std/opus/pitch";
import "std/opus/range";
import "std/opus/rate";
import "std/opus/vq";
```

### `state`

The synthesis memory of each channel holds `DECODE_BUFFER_SIZE` samples of output history, plus the overlap. The newest frame goes at the end of the history.

```fern
<<state>>=
pub const DECODE_BUFFER_SIZE: i32 = 2048;

pub const MAX_PACKET: i32 = 1275;

pub const COMBFILTER_MINPERIOD: i32 = 15;

pub const COMBFILTER_MAXPERIOD: i32 = 1024;

pub const LPC_ORDER: i32 = 24;

const MEM: i32 = 2168;

const NO_ENERGY: f32 = -28.0f32;

pub enum CeltError {
  BadArg(string),
  InternalError,
}

impl error.Error for CeltError {}

impl cmp.Display for CeltError {}

pub function (e: CeltError) message(): string {
  match (e) {
    BadArg(what) => {
      return "bad CELT argument: " + what;
    },
    InternalError => {
      return "the CELT frame read past the end of its packet";
    }
  }
  return "";
}

pub function (e: CeltError) to_string(): string {
  return e.message();
}

pub struct Decoder {
  channels: i32,
  downsample: i32,
  disable_inv: boolean,
  rng: u32,
  error: boolean,
  last_pitch_index: i32,
  loss_duration: i32,
  skip_plc: boolean,
  postfilter_period: i32,
  postfilter_period_old: i32,
  postfilter_gain: f32,
  postfilter_gain_old: f32,
  postfilter_tapset: i32,
  postfilter_tapset_old: i32,
  preemph_mem: f32[],
  mem: f32[],
  lpc: f32[],
  old_band_e: f32[],
  old_log_e: f32[],
  old_log_e2: f32[],
  background_log_e: f32[],
}

// The values that the Opus layer sets for each frame.
pub struct Frame { frame_size: i32, stream_channels: i32, start: i32, end: i32 }

function filled(n: i32, v: f32): f32[] {
  let x: f32[] = [];
  for i in 0..n {
    x = x.append(v);
  }
  return x;
}

function downsample_of(rate: i32): i32 {
  if (rate == 48000) {
    return 1;
  }
  if (rate == 24000) {
    return 2;
  }
  if (rate == 16000) {
    return 3;
  }
  if (rate == 12000) {
    return 4;
  }
  if (rate == 8000) {
    return 6;
  }
  return 0;
}

// A new decoder for `channels` channels at the sample rate `rate`.
pub function decoder(rate: i32, channels: i32): Result[Decoder, CeltError] {
  let downsample: i32 = downsample_of(rate);
  if (downsample == 0) {
    return Err(BadArg("the sample rate " + rate.to_string() + " is not one that Opus uses"));
  }
  if (channels != 1 && channels != 2) {
    return Err(BadArg("a decoder has 1 or 2 channels, not " + channels.to_string()));
  }
  return Ok(reset(channels, downsample));
}

function reset(channels: i32, downsample: i32): Decoder {
  let bands: i32 = 2 * modes.BANDS;
  return Decoder { channels: channels, downsample: downsample, disable_inv: channels == 1, rng: 0u32, error: false, last_pitch_index: 0, loss_duration: 0, skip_plc: true, postfilter_period: 0, postfilter_period_old: 0, postfilter_gain: 0.0f32, postfilter_gain_old: 0.0f32, postfilter_tapset: 0, postfilter_tapset_old: 0, preemph_mem: filled(2, 0.0f32), mem: filled(channels * MEM, 0.0f32), lpc: filled(channels * LPC_ORDER, 0.0f32), old_band_e: filled(bands, 0.0f32), old_log_e: filled(bands, NO_ENERGY), old_log_e2: filled(bands, NO_ENERGY), background_log_e: filled(bands, 0.0f32) };
}

// The decoder of `st` in the state of a new decoder.
pub function (st: Decoder) reset(): Decoder {
  return reset(st.channels, st.downsample);
}
```

### `header`

The header of a frame holds the silence flag, the pitch postfilter, the transient flag, and the intra flag of the coarse energy. Each field is present only when the packet has bits for it.

`tf_decode` reads the change of time-frequency resolution of each band, and `init_caps` gives the largest allocation of each band.

```fern
<<header>>=
const TAPSET_ICDF: u8[] = [2u8, 1u8, 0u8];

pub const SPREAD_ICDF: u8[] = [25u8, 23u8, 2u8, 0u8];

pub const TRIM_ICDF: u8[] = [126u8, 124u8, 119u8, 109u8, 87u8, 41u8, 19u8, 9u8, 4u8, 2u8, 0u8];

// The TF change of each band, by frame size, then by 4 * transient +
// 2 * tf_select + the flag of the band.
pub const TF_SELECT: i32[] = [
  0, -1, 0, -1, 0, -1, 0, -1,
  0, -1, 0, -2, 1, 0, 1, -1,
  0, -2, 0, -3, 2, 0, 1, -1,
  0, -2, 0, -3, 3, 0, 1, -1,
];

struct Header { silence: boolean, pitch: i32, gain: f32, tapset: i32, transient: boolean, intra: boolean, tell: i32 }

struct Postfilter { pitch: i32, gain: f32, tapset: i32 }

function postfilter(own d: range.Decoder, total_bits: i32): (range.Decoder, Postfilter) {
  let (d1, on) = d.bit_logp(1);
  if (!on) {
    return (d1, Postfilter { pitch: 0, gain: 0.0f32, tapset: 0 });
  }
  let (d2, octave) = d1.uint(6u32);
  let o: i32 = octave as i32;
  let (d3, low) = d2.raw(4 + o);
  let (d4, qg) = d3.raw(3);
  let tapset: i32 = 0;
  if (d4.tell() + 2 <= total_bits) {
    let (d5, t) = d4.icdf(TAPSET_ICDF, 2);
    d4 = d5;
    tapset = t;
  }
  return (d4, Postfilter { pitch: (16 << o) + low as i32 - 1, gain: 0.09375f32 * ((qg as i32 + 1) as f32), tapset: tapset });
}

function silence_of(own d: range.Decoder, total_bits: i32): (range.Decoder, boolean) {
  let tell: i32 = d.tell();
  if (tell >= total_bits) {
    return (d, true);
  }
  if (tell == 1) {
    return d.bit_logp(15);
  }
  return (d, false);
}

function header(own d: range.Decoder, total_bits: i32, start: i32, lm: i32): (range.Decoder, Header) {
  let (d1, silence) = silence_of(d, total_bits);
  let tell: i32 = d1.tell();
  if (silence) {
    tell = total_bits;
    d1 = range.Decoder { ...d1, nbits_total: d1.nbits_total + tell - d1.tell() };
  }
  let pf: Postfilter = Postfilter { pitch: 0, gain: 0.0f32, tapset: 0 };
  let d2: range.Decoder = d1;
  if (start == 0 && tell + 16 <= total_bits) {
    let (d3, p) = postfilter(d2, total_bits);
    d2 = d3;
    pf = p;
    tell = d2.tell();
  }
  let transient: boolean = false;
  if (lm > 0 && tell + 3 <= total_bits) {
    let (d3, t) = d2.bit_logp(3);
    d2 = d3;
    transient = t;
    tell = d2.tell();
  }
  let intra: boolean = false;
  if (tell + 3 <= total_bits) {
    let (d4, i) = d2.bit_logp(3);
    d2 = d4;
    intra = i;
  }
  return (d2, Header { silence: silence, pitch: pf.pitch, gain: pf.gain, tapset: pf.tapset, transient: transient, intra: intra, tell: tell });
}

function flag(b: boolean): i32 {
  if (b) {
    return 1;
  }
  return 0;
}

function tf_decode(own d: range.Decoder, start: i32, end: i32, transient: boolean, lm: i32): (range.Decoder, i32[]) {
  let budget: i32 = d.storage * 8;
  let tell: i32 = d.tell();
  let logp: i32 = 4;
  if (transient) {
    logp = 2;
  }
  let rsv: boolean = lm > 0 && tell + logp + 1 <= budget;
  budget = budget - flag(rsv);
  let changed: i32 = 0;
  let curr: i32 = 0;
  let res: i32[] = [];
  for i in 0..modes.BANDS {
    res = res.append(0);
  }
  for i in start..end {
    if (tell + logp <= budget) {
      let (d1, b) = d.bit_logp(logp);
      d = d1;
      curr = curr ^ flag(b);
      tell = d.tell();
      changed = changed | curr;
    }
    res = res.with(i, curr);
    logp = 5;
    if (transient) {
      logp = 4;
    }
  }
  let row: i32 = 8 * lm + 4 * flag(transient);
  let select: i32 = 0;
  if (rsv && TF_SELECT[row + changed] != TF_SELECT[row + 2 + changed]) {
    let (d2, b) = d.bit_logp(1);
    d = d2;
    select = flag(b);
  }
  for i in start..end {
    res = res.with(i, TF_SELECT[row + 2 * select + res[i]]);
  }
  return (d, res);
}

// The largest allocation of each band, in 1/8 bits.
function init_caps(lm: i32, channels: i32): i32[] {
  let cap: i32[] = [];
  for i in 0..modes.BANDS {
    let n: i32 = modes.EBANDS[i + 1] - modes.EBANDS[i] << lm;
    cap = cap.append((modes.CACHE_CAPS[modes.BANDS * (2 * lm + channels - 1) + i] + 64) * channels * n >> 2);
  }
  return cap;
}
```

### `energy scaling`

`denormalise_bands` scales the unit shape of each band by its energy. The energy is in log2 units, relative to the mean energy of the band.

```fern
<<energy scaling>>=
// 2 to the power `x`, as libopus calculates it.
function exp2(x: f32): f32 {
  return (0.6931471805599453094f64 * (x as f64)).exp() as f32;
}

// The spectrum of one channel from its band shapes in `x` and its energies
// from `log_e[e_at]`.
function denormalise_bands(x: [f32], log_e: [f32], e_at: i32, start0: i32, end0: i32, m: i32, downsample: i32, silence: boolean): f32[] {
  let n: i32 = m * modes.SHORT_MDCT_SIZE;
  let start: i32 = start0;
  let end: i32 = end0;
  let bound: i32 = m * modes.EBANDS[end];
  if (downsample != 1 && n / downsample < bound) {
    bound = n / downsample;
  }
  if (silence) {
    bound = 0;
    start = 0;
    end = 0;
  }
  let freq: f32[] = [];
  for i in 0..m * modes.EBANDS[start] {
    freq = freq.append(0.0f32);
  }
  for i in start..end {
    let lg: f32 = log_e[e_at + i] + modes.E_MEANS[i];
    if (32.0f32 < lg) {
      lg = 32.0f32;
    }
    let g: f32 = exp2(lg);
    for j in m * modes.EBANDS[i]..m * modes.EBANDS[i + 1] {
      freq = freq.append(x[j] * g);
    }
  }
  for i in freq.len()..n {
    freq = freq.append(0.0f32);
  }
  for i in bound..n {
    freq = freq.with(i, 0.0f32);
  }
  return freq;
}
```

### `anti-collapse`

A transient frame codes each band as `M` short blocks. When a block of a band gets no pulses, its energy collapses to zero. The anti-collapse step fills each such block with noise at the level of the lost energy. Then it makes the band unit length again.

```fern
<<anti-collapse>>=
const SQRT_2: f32 = 1.4142135381698608f32;

struct Collapse { lm: i32, channels: i32, start: i32, end: i32, masks: i32[], pulses: i32[] }

struct Filled { x: f32[], seed: u32 }

function min16(a: f32, b: f32): f32 {
  if (a < b) {
    return a;
  }
  return b;
}

function max16(a: f32, b: f32): f32 {
  if (a > b) {
    return a;
  }
  return b;
}

// Fills the collapsed blocks of band `i` of one channel with noise of the
// level `r`.
function fill_band(own x: f32[], at: i32, n0: i32, lm: i32, mask: i32, r: f32, seed0: u32): Filled {
  let seed: u32 = seed0;
  let filled: boolean = false;
  let blocks: i32 = 1 << lm;
  for k in 0..blocks {
    if ((mask & 1 << k) == 0) {
      for j in 0..n0 {
        seed = bands.lcg_rand(seed);
        let v: f32 = -r;
        if ((seed & 32768u32) != 0u32) {
          v = r;
        }
        x = x.with(at + (j << lm) + k, v);
      }
      filled = true;
    }
  }
  if (filled) {
    let n: i32 = n0 << lm;
    let band: f32[] = [];
    for j in 0..n {
      band = band.append(x[at + j]);
    }
    let unit: f32[] = vq.renormalise_vector(band, n, 1.0f32);
    for j in 0..n {
      x = x.with(at + j, unit[j]);
    }
  }
  return Filled { x: x, seed: seed };
}

// The level of the noise for band `i` of channel `c`.
function collapse_level(r: Collapse, i: i32, c: i32, log_e: [f32], prev1: [f32], prev2: [f32], thresh: f32, sqrt_1: f32): f32 {
  let k: i32 = c * modes.BANDS + i;
  let p1: f32 = prev1[k];
  let p2: f32 = prev2[k];
  if (r.channels == 1) {
    p1 = max16(p1, prev1[modes.BANDS + i]);
    p2 = max16(p2, prev2[modes.BANDS + i]);
  }
  let ediff: f32 = log_e[k] - min16(p1, p2);
  if (0.0f32 > ediff) {
    ediff = 0.0f32;
  }
  let level: f32 = 2.0f32 * exp2(-ediff);
  if (r.lm == 3) {
    level = level * SQRT_2;
  }
  return min16(thresh, level) * sqrt_1;
}

struct AntiCollapse { x: f32[], y: f32[], seed: u32 }

function anti_collapse(r: Collapse, own x: f32[], own y: f32[], log_e: [f32], prev1: [f32], prev2: [f32], seed0: u32): AntiCollapse {
  let seed: u32 = seed0;
  for i in r.start..r.end {
    let n0: i32 = modes.EBANDS[i + 1] - modes.EBANDS[i];
    let depth: i32 = (1 + r.pulses[i]) / n0 >> r.lm;
    let thresh: f32 = 0.5f32 * exp2(-0.125f32 * (depth as f32));
    let sqrt_1: f32 = 1.0f32 / (((n0 << r.lm) as f64).sqrt() as f32);
    let at: i32 = modes.EBANDS[i] << r.lm;
    let level0: f32 = collapse_level(r, i, 0, log_e, prev1, prev2, thresh, sqrt_1);
    let f0: Filled = fill_band(x, at, n0, r.lm, r.masks[i * r.channels], level0, seed);
    x = f0.x;
    seed = f0.seed;
    if (r.channels == 2) {
      let level1: f32 = collapse_level(r, i, 1, log_e, prev1, prev2, thresh, sqrt_1);
      let f1: Filled = fill_band(y, at, n0, r.lm, r.masks[i * 2 + 1], level1, seed);
      y = f1.x;
      seed = f1.seed;
    }
  }
  return AntiCollapse { x: x, y: y, seed: seed };
}
```

### `postfilter`

The pitch postfilter is a comb filter with three taps around the pitch period. Over the overlap, the window fades from the filter of the last frame to the filter of this frame. The decoder runs the filter in place, so each output sample feeds the samples one period after it. The prefilter of the encoder runs the same taps as an FIR filter, with `comb_fir`.

```fern
<<postfilter>>=
const GAINS: f32[] = [
  0.306640625f32, 0.217041015625f32, 0.129638671875f32,
  0.4638671875f32, 0.26806640625f32, 0.0f32,
  0.7998046875f32, 0.10009765625f32, 0.0f32,
];

pub struct Comb { t0: i32, t1: i32, n: i32, g0: f32, g1: f32, tapset0: i32, tapset1: i32 }

struct Taps { a: f32, b: f32, c: f32 }

function taps_of(g: f32, tapset: i32): Taps {
  return Taps { a: g * GAINS[3 * tapset], b: g * GAINS[3 * tapset + 1], c: g * GAINS[3 * tapset + 2] };
}

// `acc` plus the taps `t` at the period `d` before `x[p]`, each tap scaled
// by `s`. The additions are in the order of libopus.
function add_taps(acc: f32, x: [f32], p: i32, d: i32, t: Taps, s: f32): f32 {
  return acc + s * t.a * x[p - d] + s * t.b * (x[p - d + 1] + x[p - d - 1]) + s * t.c * (x[p - d + 2] + x[p - d - 2]);
}

// The value at `p` of the comb filter of `x` over the overlap: the window
// fades from the taps `old` at the period `t0` to the taps `new` at `t1`.
function fade_taps(x: [f32], p: i32, i: i32, t0: i32, t1: i32, old: Taps, new: Taps): f32 {
  let w: f32 = modes.WINDOW[i] * modes.WINDOW[i];
  return add_taps(add_taps(x[p], x, p, t0, old, 1.0f32 - w), x, p, t1, new, w);
}

struct CombPlan { t0: i32, t1: i32, overlap: i32, old: Taps, new: Taps }

function comb_plan(f: Comb): CombPlan {
  let t0: i32 = f.t0;
  let t1: i32 = f.t1;
  if (t0 < COMBFILTER_MINPERIOD) {
    t0 = COMBFILTER_MINPERIOD;
  }
  if (t1 < COMBFILTER_MINPERIOD) {
    t1 = COMBFILTER_MINPERIOD;
  }
  let overlap: i32 = modes.OVERLAP;
  if (f.g0 == f.g1 && t0 == t1 && f.tapset0 == f.tapset1) {
    overlap = 0;
  }
  return CombPlan { t0: t0, t1: t1, overlap: overlap, old: taps_of(f.g0, f.tapset0), new: taps_of(f.g1, f.tapset1) };
}

// The comb filter, in place, on the `f.n` samples of `x` from `at`.
function comb_filter(own x: f32[], at: i32, f: Comb): f32[] {
  if (f.g0 == 0.0f32 && f.g1 == 0.0f32) {
    return x;
  }
  let c: CombPlan = comb_plan(f);
  for i in 0..c.overlap {
    x = x.with(at + i, fade_taps(x, at + i, i, c.t0, c.t1, c.old, c.new));
  }
  if (f.g1 == 0.0f32) {
    return x;
  }
  for i in c.overlap..f.n {
    x = x.with(at + i, add_taps(x[at + i], x, at + i, c.t1, c.new, 1.0f32));
  }
  return x;
}

// The comb filter of the `f.n` samples of `x` from `xat`, written to `y`
// from `yat`. It reads only `x`, so it is an FIR filter.
pub function comb_fir(own y: f32[], yat: i32, x: [f32], xat: i32, f: Comb): f32[] {
  let c: CombPlan = comb_plan(f);
  let first: i32 = c.overlap;
  if (f.g0 == 0.0f32 && f.g1 == 0.0f32) {
    first = 0;
  }
  for i in 0..first {
    y = y.with(yat + i, fade_taps(x, xat + i, i, c.t0, c.t1, c.old, c.new));
  }
  if (f.g1 == 0.0f32) {
    for i in first..f.n {
      y = y.with(yat + i, x[xat + i]);
    }
    return y;
  }
  for i in first..f.n {
    y = y.with(yat + i, add_taps(x[xat + i], x, xat + i, c.t1, c.new, 1.0f32));
  }
  return y;
}
```

### `synthesis`

The synthesis scales the bands of each channel, and runs the inverse MDCT into the synthesis memory. A transient frame runs one short MDCT for each block. The de-emphasis filter then gives the output samples.

```fern
<<synthesis>>=
const VERY_SMALL: f32 = 1.0000000031710769e-30f32;

const SIG_SCALE: f32 = 0.000030517578125f32;

struct Synthesis { start: i32, end: i32, channels: i32, transient: boolean, lm: i32, silence: boolean }

function inverse(freq: [f32], own mem: f32[], at: i32, s: Synthesis): f32[] {
  let blocks: i32 = 1;
  let nb: i32 = modes.SHORT_MDCT_SIZE << s.lm;
  let shift: i32 = modes.MAX_LM - s.lm;
  if (s.transient) {
    blocks = 1 << s.lm;
    nb = modes.SHORT_MDCT_SIZE;
    shift = modes.MAX_LM;
  }
  for b in 0..blocks {
    mem = mdct.backward(freq, b, mem, at + nb * b, shift, blocks);
  }
  return mem;
}

// The inverse MDCT of each output channel into the synthesis memory.
function synthesis(own st: Decoder, x: [f32], y: [f32], s: Synthesis): Decoder {
  let m: i32 = 1 << s.lm;
  let n: i32 = modes.SHORT_MDCT_SIZE << s.lm;
  let syn: i32 = DECODE_BUFFER_SIZE - n;
  let freq: f32[] = denormalise_bands(x, st.old_band_e, 0, s.start, s.end, m, st.downsample, s.silence);
  if (st.channels == 1 && s.channels == 2) {
    let freq2: f32[] = denormalise_bands(y, st.old_band_e, modes.BANDS, s.start, s.end, m, st.downsample, s.silence);
    for i in 0..n {
      freq = freq.with(i, 0.5f32 * freq[i] + 0.5f32 * freq2[i]);
    }
  }
  let mem: f32[] = inverse(freq, st.mem, syn, s);
  if (st.channels == 2 && s.channels == 2) {
    freq = denormalise_bands(y, st.old_band_e, modes.BANDS, s.start, s.end, m, st.downsample, s.silence);
  }
  if (st.channels == 2) {
    mem = inverse(freq, mem, MEM + syn, s);
  }
  return Decoder { ...st, mem: mem };
}

// The de-emphasis of the `n` newest samples of each channel, with one sample
// in each `downsample`.
function deemphasis(own st: Decoder, n: i32): (Decoder, f32[]) {
  let cc: i32 = st.channels;
  let nd: i32 = n / st.downsample;
  let pcm: f32[] = filled(cc * nd, 0.0f32);
  let pre: f32[] = st.preemph_mem;
  for c in 0..cc {
    let at: i32 = c * MEM + DECODE_BUFFER_SIZE - n;
    let m: f32 = pre[c];
    for j in 0..n {
      let tmp: f32 = st.mem[at + j] + VERY_SMALL + m;
      m = modes.PREEMPH * tmp;
      if (j % st.downsample == 0) {
        pcm = pcm.with(j / st.downsample * cc + c, tmp * SIG_SCALE);
      }
    }
    pre = pre.with(c, m);
  }
  return (Decoder { ...st, preemph_mem: pre }, pcm);
}
```

### `loss concealment`

A lost frame continues the signal. For the first frames of a loss, the decoder finds the pitch period of the last output. It repeats the last period of the prediction error with a decay. Then the prediction filter makes a signal of that error. The energy of the result is at most the energy of the period that it repeats. The comb filter of the last frame and a mirror of the overlap let the next frame blend in.

The decoder makes noise at the energy of each band in three cases. These are a loss of 100 ms or more, a loss at the start of a stream, and a hybrid frame. A loss can start with a hybrid frame and continue with a CELT frame. Then the decoder has no pitch period, and it also makes noise. In that case libopus reads past the end of its excitation buffer.

In the noise, the energy falls by 1.5 dB in the first lost frame, and by 0.5 dB in each frame after it. It does not fall below the background noise.

```fern
<<loss concealment>>=
const MAX_PERIOD: i32 = 1024;

const PLC_PITCH_LAG_MAX: i32 = 720;

const PLC_PITCH_LAG_MIN: i32 = 100;

const FADE: f32 = 0.800000011920929f32;

const KEEP: f32 = 0.20000000298023224f32;

// The pitch period of the output history of each channel.
function plc_pitch(mem: [f32], channels: i32): i32 {
  let lp: f32[] = pitch.pitch_downsample(mem, 0, mem, MEM, DECODE_BUFFER_SIZE, channels);
  let found: i32 = pitch.pitch_search(lp, PLC_PITCH_LAG_MAX >> 1, lp, DECODE_BUFFER_SIZE - PLC_PITCH_LAG_MAX, PLC_PITCH_LAG_MAX - PLC_PITCH_LAG_MIN);
  return PLC_PITCH_LAG_MAX - found;
}

// The noise of one channel, a unit shape in each band.
function noise_bands(seed0: u32, start: i32, end: i32, lm: i32): Filled {
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  let x: f32[] = filled(n, 0.0f32);
  let seed: u32 = seed0;
  for i in start..end {
    let at: i32 = modes.EBANDS[i] << lm;
    let len: i32 = modes.EBANDS[i + 1] - modes.EBANDS[i] << lm;
    let band: f32[] = [];
    for j in 0..len {
      seed = bands.lcg_rand(seed);
      band = band.append((seed as i32 >> 20) as f32);
    }
    let unit: f32[] = vq.renormalise_vector(band, len, 1.0f32);
    for j in 0..len {
      x = x.with(at + j, unit[j]);
    }
  }
  return Filled { x: x, seed: seed };
}

function conceal_noise(own st: Decoder, f: Frame, lm: i32): Decoder {
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  let eff_end: i32 = f.end;
  if (eff_end < f.start) {
    eff_end = f.start;
  }
  let mem: f32[] = age(st.mem, st.channels, n);
  let decay: f32 = 0.5f32;
  if (st.loss_duration == 0) {
    decay = 1.5f32;
  }
  let old: f32[] = st.old_band_e;
  for c in 0..st.channels {
    for i in f.start..f.end {
      let k: i32 = c * modes.BANDS + i;
      old = old.with(k, max16(st.background_log_e[k], old[k] - decay));
    }
  }
  let x: Filled = noise_bands(st.rng, f.start, eff_end, lm);
  let y: Filled = Filled { x: filled(n, 0.0f32), seed: x.seed };
  if (st.channels == 2) {
    y = noise_bands(x.seed, f.start, eff_end, lm);
  }
  let syn: Synthesis = Synthesis { start: f.start, end: eff_end, channels: st.channels, transient: false, lm: lm, silence: false };
  return synthesis(Decoder { ...st, mem: mem, rng: y.seed, old_band_e: old }, x.x, y.x, syn);
}

// The prediction error of the last `MAX_PERIOD` samples of one channel, from
// `exc[LPC_ORDER]`, with the samples before them.
struct Excitation { exc: f32[], lpc: f32[], decay: f32 }

function excitation(st: Decoder, at: i32, c: i32, exc_length: i32): Excitation {
  let exc: f32[] = [];
  for i in 0..MAX_PERIOD + LPC_ORDER {
    exc = exc.append(st.mem[at + DECODE_BUFFER_SIZE - MAX_PERIOD - LPC_ORDER + i]);
  }
  let a: f32[] = [];
  for i in 0..LPC_ORDER {
    a = a.append(st.lpc[c * LPC_ORDER + i]);
  }
  if (st.loss_duration == 0) {
    let ac: f32[] = pitch.autocorr(exc, LPC_ORDER, true, LPC_ORDER, MAX_PERIOD);
    ac = ac.with(0, ac[0] * 1.000100016593933f32);
    let k: f32 = 0.00800000037997961f32 * 0.00800000037997961f32;
    for i in 1..LPC_ORDER + 1 {
      ac = ac.with(i, ac[i] - ac[i] * k * (i as f32) * (i as f32));
    }
    a = pitch.lpc(ac, LPC_ORDER);
  }
  let from: i32 = LPC_ORDER + MAX_PERIOD - exc_length;
  let err: f32[] = pitch.fir(exc, from, a, exc_length, LPC_ORDER);
  for i in 0..exc_length {
    exc = exc.with(from + i, err[i]);
  }
  let e1: f32 = 1.0f32;
  let e2: f32 = 1.0f32;
  let half: i32 = exc_length >> 1;
  for i in 0..half {
    let v1: f32 = exc[LPC_ORDER + MAX_PERIOD - half + i];
    e1 = e1 + v1 * v1;
    let v2: f32 = exc[LPC_ORDER + MAX_PERIOD - 2 * half + i];
    e2 = e2 + v2 * v2;
  }
  if (e2 < e1) {
    e1 = e2;
  }
  return Excitation { exc: exc, lpc: a, decay: ((e1 / e2) as f64).sqrt() as f32 };
}

// The energy of `len` samples of `x` from `at`.
function energy_of(x: [f32], at: i32, len: i32): f32 {
  let s: f32 = 0.0f32;
  for i in 0..len {
    s = s + x[at + i] * x[at + i];
  }
  return s;
}

// The attenuation of the concealed samples whose energy is more than the
// energy `s1` of the samples that they repeat.
function limit_energy(own buf: f32[], at: i32, len: i32, s1: f32): f32[] {
  let s2: f32 = energy_of(buf, at, len);
  if (!(s1 > KEEP * s2)) {
    for i in 0..len {
      buf = buf.with(at + i, 0.0f32);
    }
    return buf;
  }
  if (s1 < s2) {
    let ratio: f32 = (((s1 + 1.0f32) / (s2 + 1.0f32)) as f64).sqrt() as f32;
    for i in 0..len {
      let g: f32 = ratio;
      if (i < modes.OVERLAP) {
        g = 1.0f32 - modes.WINDOW[i] * (1.0f32 - ratio);
      }
      buf = buf.with(at + i, g * buf[at + i]);
    }
  }
  return buf;
}

// The comb filter of the last frame, applied in the opposite sense to the
// overlap, then the mirror of the overlap for the next frame.
function blend_overlap(own buf: f32[], at: i32, st: Decoder): f32[] {
  let x: i32 = at + DECODE_BUFFER_SIZE;
  let etmp: f32[] = [];
  let g: f32 = -st.postfilter_gain;
  let t: i32 = st.postfilter_period;
  if (t < COMBFILTER_MINPERIOD) {
    t = COMBFILTER_MINPERIOD;
  }
  let g0: f32 = g * GAINS[3 * st.postfilter_tapset];
  let g1: f32 = g * GAINS[3 * st.postfilter_tapset + 1];
  let g2: f32 = g * GAINS[3 * st.postfilter_tapset + 2];
  for i in 0..modes.OVERLAP {
    let p: i32 = x + i;
    if (g == 0.0f32) {
      etmp = etmp.append(buf[p]);
    } else {
      etmp = etmp.append(buf[p] + g0 * buf[p - t] + g1 * (buf[p - t + 1] + buf[p - t - 1]) + g2 * (buf[p - t + 2] + buf[p - t - 2]));
    }
  }
  for i in 0..modes.OVERLAP / 2 {
    buf = buf.with(x + i, modes.WINDOW[i] * etmp[modes.OVERLAP - 1 - i] + modes.WINDOW[modes.OVERLAP - i - 1] * etmp[i]);
  }
  return buf;
}

// One channel of the pitch-based concealment.
function conceal_channel(own st: Decoder, c: i32, n: i32, pitch_index: i32, fade: f32): Decoder {
  let at: i32 = c * MEM;
  let exc_length: i32 = 2 * pitch_index;
  if (exc_length > MAX_PERIOD) {
    exc_length = MAX_PERIOD;
  }
  let e: Excitation = excitation(st, at, c, exc_length);
  let buf: f32[] = st.mem;
  for i in 0..DECODE_BUFFER_SIZE - n {
    buf = buf.with(at + i, buf[at + i + n]);
  }
  let offset: i32 = MAX_PERIOD - pitch_index;
  let len: i32 = n + modes.OVERLAP;
  let attenuation: f32 = fade * e.decay;
  let s1: f32 = 0.0f32;
  let j: i32 = 0;
  for i in 0..len {
    if (j >= pitch_index) {
      j = j - pitch_index;
      attenuation = attenuation * e.decay;
    }
    buf = buf.with(at + DECODE_BUFFER_SIZE - n + i, attenuation * e.exc[LPC_ORDER + offset + j]);
    let v: f32 = buf[at + DECODE_BUFFER_SIZE - MAX_PERIOD - n + offset + j];
    s1 = s1 + v * v;
    j = j + 1;
  }
  let lpc_mem: f32[] = [];
  for i in 0..LPC_ORDER {
    lpc_mem = lpc_mem.append(buf[at + DECODE_BUFFER_SIZE - n - 1 - i]);
  }
  let out: pitch.Iir = pitch.iir(buf, at + DECODE_BUFFER_SIZE - n, e.lpc, len, LPC_ORDER, lpc_mem);
  for i in 0..len {
    buf = buf.with(at + DECODE_BUFFER_SIZE - n + i, out.y[i]);
  }
  buf = limit_energy(buf, at + DECODE_BUFFER_SIZE - n, len, s1);
  buf = blend_overlap(buf, at, st);
  let lpc: f32[] = st.lpc;
  for i in 0..LPC_ORDER {
    lpc = lpc.with(c * LPC_ORDER + i, e.lpc[i]);
  }
  return Decoder { ...st, mem: buf, lpc: lpc };
}

// The concealment of one lost frame into the synthesis memory.
function conceal(own st: Decoder, f: Frame, lm: i32): Decoder {
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  let duration: i32 = st.loss_duration + (1 << lm);
  if (duration > 10000) {
    duration = 10000;
  }
  if (st.loss_duration >= 40 || f.start != 0 || st.skip_plc || st.loss_duration != 0 && st.last_pitch_index == 0) {
    return Decoder { ...conceal_noise(st, f, lm), loss_duration: duration };
  }
  let pitch_index: i32 = st.last_pitch_index;
  let fade: f32 = FADE;
  if (st.loss_duration == 0) {
    pitch_index = plc_pitch(st.mem, st.channels);
    fade = 1.0f32;
  }
  let out: Decoder = Decoder { ...st, last_pitch_index: pitch_index };
  for c in 0..st.channels {
    out = conceal_channel(out, c, n, pitch_index, fade);
  }
  return Decoder { ...out, loss_duration: duration };
}
```

### `frame`

`decode` reads one frame, in the order of the bit stream. First it reads the header, the coarse energy, the TF changes, the spread, the dynamic allocation and the trim. Then it reads the allocation, the fine energy, the band shapes, the anti-collapse bit and the final energy bits.

```fern
<<frame>>=
struct Dynalloc { d: range.Decoder, offsets: i32[], tell: i32, total: i32 }

// The boosts of the dynamic allocation of each band.
function dynalloc(own d: range.Decoder, start: i32, end: i32, cap: [i32], channels: i32, lm: i32, total0: i32): Dynalloc {
  let total: i32 = total0;
  let tell: i32 = d.tell_frac();
  let logp: i32 = 6;
  let offsets: i32[] = [];
  for i in 0..modes.BANDS {
    offsets = offsets.append(0);
  }
  for i in start..end {
    let width: i32 = channels * (modes.EBANDS[i + 1] - modes.EBANDS[i]) << lm;
    let quanta: i32 = 6 << rate.BITRES;
    if (width > quanta) {
      quanta = width;
    }
    if (width << rate.BITRES < quanta) {
      quanta = width << rate.BITRES;
    }
    let loop_logp: i32 = logp;
    let boost: i32 = 0;
    while (tell + (loop_logp << rate.BITRES) < total && boost < cap[i]) {
      let (d1, on) = d.bit_logp(loop_logp);
      d = d1;
      tell = d.tell_frac();
      if (!on) {
        break;
      }
      boost = boost + quanta;
      total = total - quanta;
      loop_logp = 1;
    }
    offsets = offsets.with(i, boost);
    if (boost > 0 && logp > 2) {
      logp = logp - 1;
    }
  }
  return Dynalloc { d: d, offsets: offsets, tell: tell, total: total };
}

function decoding(own c: range.Coder): range.Decoder {
  match (c) {
    range.Decoding(d) => {
      return d;
    },
    range.Encoding(_) => {}
  }
  return range.decoder([]);
}

function lm_of(frame_size: i32): i32 {
  for lm in 0..modes.MAX_LM + 1 {
    if (modes.SHORT_MDCT_SIZE << lm == frame_size) {
      return lm;
    }
  }
  return -1;
}

// The band energies after the frame, for the next frame and for the loss
// concealment.
function keep_energy(own st: Decoder, channels: i32, transient: boolean, m: i32, start: i32, end: i32): Decoder {
  let nb: i32 = modes.BANDS;
  let old: f32[] = st.old_band_e;
  if (channels == 1) {
    for i in 0..nb {
      old = old.with(nb + i, old[i]);
    }
  }
  let log_e: f32[] = st.old_log_e;
  let log_e2: f32[] = st.old_log_e2;
  for i in 0..2 * nb {
    if (transient) {
      log_e = log_e.with(i, min16(log_e[i], old[i]));
    } else {
      log_e2 = log_e2.with(i, log_e[i]);
      log_e = log_e.with(i, old[i]);
    }
  }
  let steps: i32 = st.loss_duration + m;
  if (steps > 160) {
    steps = 160;
  }
  let increase: f32 = steps as f32 * 0.0010000000474974513f32;
  let background: f32[] = st.background_log_e;
  for i in 0..2 * nb {
    background = background.with(i, min16(background[i] + increase, old[i]));
  }
  for k in 0..2 * nb {
    let i: i32 = k % nb;
    if (i < start || i >= end) {
      old = old.with(k, 0.0f32);
      log_e = log_e.with(k, NO_ENERGY);
      log_e2 = log_e2.with(k, NO_ENERGY);
    }
  }
  return Decoder { ...st, old_band_e: old, old_log_e: log_e, old_log_e2: log_e2, background_log_e: background };
}

// The pitch postfilter of each channel, from the filter of the last frame to
// the filter of this frame.
function apply_postfilter(own st: Decoder, h: Header, lm: i32): Decoder {
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  let period: i32 = st.postfilter_period;
  let period_old: i32 = st.postfilter_period_old;
  if (period < COMBFILTER_MINPERIOD) {
    period = COMBFILTER_MINPERIOD;
  }
  if (period_old < COMBFILTER_MINPERIOD) {
    period_old = COMBFILTER_MINPERIOD;
  }
  let mem: f32[] = st.mem;
  for c in 0..st.channels {
    let at: i32 = c * MEM + DECODE_BUFFER_SIZE - n;
    mem = comb_filter(mem, at, Comb { t0: period_old, t1: period, n: modes.SHORT_MDCT_SIZE, g0: st.postfilter_gain_old, g1: st.postfilter_gain, tapset0: st.postfilter_tapset_old, tapset1: st.postfilter_tapset });
    if (lm != 0) {
      mem = comb_filter(mem, at + modes.SHORT_MDCT_SIZE, Comb { t0: period, t1: h.pitch, n: n - modes.SHORT_MDCT_SIZE, g0: st.postfilter_gain, g1: h.gain, tapset0: st.postfilter_tapset, tapset1: h.tapset });
    }
  }
  if (lm != 0) {
    return Decoder { ...st, mem: mem, postfilter_period: h.pitch, postfilter_period_old: h.pitch, postfilter_gain: h.gain, postfilter_gain_old: h.gain, postfilter_tapset: h.tapset, postfilter_tapset_old: h.tapset };
  }
  return Decoder { ...st, mem: mem, postfilter_period: h.pitch, postfilter_period_old: period, postfilter_gain: h.gain, postfilter_gain_old: st.postfilter_gain, postfilter_tapset: h.tapset, postfilter_tapset_old: st.postfilter_tapset };
}

// The synthesis memory of each channel, one frame of `n` samples older.
function age(own mem: f32[], channels: i32, n: i32): f32[] {
  for c in 0..channels {
    let at: i32 = c * MEM;
    for i in 0..DECODE_BUFFER_SIZE - n + modes.OVERLAP / 2 {
      mem = mem.with(at + i, mem[at + i + n]);
    }
  }
  return mem;
}

struct Shapes { d: range.Decoder, st: Decoder, h: Header, x: f32[], y: f32[] }

// The frame up to its band shapes and energies.
function shapes(own st: Decoder, own d0: range.Decoder, len: i32, f: Frame, lm: i32): Shapes {
  let c: i32 = f.stream_channels;
  let nb: i32 = modes.BANDS;
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  let old: f32[] = st.old_band_e;
  if (c == 1) {
    for i in 0..nb {
      old = old.with(i, max16(old[i], old[nb + i]));
    }
  }
  let total_bits: i32 = len * 8;
  let (d1, h) = header(d0, total_bits, f.start, lm);
  let (d2, old1) = energy.unquant_coarse_energy(d1, f.start, f.end, old, h.intra, c, lm);
  let (d3, tf_res) = tf_decode(d2, f.start, f.end, h.transient, lm);
  let spread: i32 = vq.SPREAD_NORMAL;
  if (d3.tell() + 4 <= total_bits) {
    let (d4, s) = d3.icdf(SPREAD_ICDF, 5);
    d3 = d4;
    spread = s;
  }
  let cap: i32[] = init_caps(lm, c);
  let boosts: Dynalloc = dynalloc(d3, f.start, f.end, cap, c, lm, total_bits << rate.BITRES);
  let d5: range.Decoder = boosts.d;
  let trim: i32 = 5;
  if (boosts.tell + (6 << rate.BITRES) <= boosts.total) {
    let (d6, t) = d5.icdf(TRIM_ICDF, 7);
    d5 = d6;
    trim = t;
  }
  let bits: i32 = (len * 8 << rate.BITRES) - d5.tell_frac() - 1;
  let rsv: i32 = 0;
  if (h.transient && lm >= 2 && bits >= lm + 2 << rate.BITRES) {
    rsv = 1 << rate.BITRES;
  }
  let areq: rate.Request = rate.Request { start: f.start, end: f.end, offsets: boosts.offsets, cap: cap, alloc_trim: trim, intensity: 0, dual_stereo: false, total: bits - rsv, channels: c, lm: lm, prev: 0, signal_bandwidth: 0 };
  let (c1, a) = rate.compute_allocation(range.Decoding(d5), areq);
  let (d7, old2) = energy.unquant_fine_energy(decoding(c1), f.start, f.end, old1, a.ebits, c);
  let mem: f32[] = age(st.mem, st.channels, n);
  let breq: bands.Request = bands.Request { start: f.start, end: f.end, short_blocks: h.transient, spread: spread, dual_stereo: a.dual_stereo, intensity: a.intensity, tf_res: tf_res, total_bits: len * (8 << rate.BITRES) - rsv, balance: a.balance, lm: lm, coded_bands: a.coded_bands, pulses: a.pulses, seed: st.rng, complexity: 0, disable_inv: st.disable_inv, channels: c };
  let res: bands.Result = bands.quant_all_bands(range.Decoding(d7), breq, filled(n, 0.0f32), filled(n, 0.0f32), []);
  let d8: range.Decoder = decoding(res.c);
  let on: boolean = false;
  if (rsv > 0) {
    let (d9, bit) = d8.raw(1);
    d8 = d9;
    on = bit == 1u32;
  }
  let left: i32 = len * 8 - d8.tell();
  let (d10, old3) = energy.unquant_energy_finalise(d8, f.start, f.end, old2, a.ebits, a.fine_priority, left, c);
  let x: f32[] = res.x;
  let y: f32[] = res.y;
  let seed: u32 = res.seed;
  if (on) {
    let ac: AntiCollapse = anti_collapse(Collapse { lm: lm, channels: c, start: f.start, end: f.end, masks: res.collapse_masks, pulses: a.pulses }, x, y, old3, st.old_log_e, st.old_log_e2, seed);
    x = ac.x;
    y = ac.y;
    seed = ac.seed;
  }
  if (h.silence) {
    old3 = filled(2 * nb, NO_ENERGY);
  }
  return Shapes { d: d10, st: Decoder { ...st, mem: mem, rng: seed, old_band_e: old3 }, h: h, x: x, y: y };
}

// What is wrong with the arguments of a frame, or "" when they are valid.
function problem(st: Decoder, len: i32, f: Frame): string {
  if (lm_of(f.frame_size * st.downsample) < 0) {
    return "the frame size " + f.frame_size.to_string() + " is not one that CELT uses";
  }
  if (len > MAX_PACKET) {
    return "a CELT packet has at most 1275 bytes, not " + len.to_string();
  }
  if (f.stream_channels != 1 && f.stream_channels != 2) {
    return "a CELT stream has 1 or 2 channels";
  }
  if (f.start < 0 || f.start >= f.end || f.end > modes.BANDS) {
    return "the bands " + f.start.to_string() + " to " + f.end.to_string() + " are not in the range 0 to 21";
  }
  return "";
}

// Decodes a frame of `len` bytes from `d`. The Opus layer can give a range
// decoder that the SILK layer of a hybrid frame read first.
pub function (own st: Decoder) decode_with(own d: range.Decoder, len: i32, f: Frame): (Decoder, Result[f32[], CeltError]) {
  let p: string = problem(st, len, f);
  if (p != "") {
    return (st, Err(BadArg(p)));
  }
  let lm: i32 = lm_of(f.frame_size * st.downsample);
  let n: i32 = modes.SHORT_MDCT_SIZE << lm;
  if (len <= 1) {
    let (st0, pcm0) = deemphasis(conceal(st, f, lm), n);
    return (st0, Ok(pcm0));
  }
  let skip: boolean = st.loss_duration != 0;
  let s: Shapes = shapes(Decoder { ...st, skip_plc: skip }, d, len, f, lm);
  let h: Header = s.h;
  let syn: Synthesis = Synthesis { start: f.start, end: f.end, channels: f.stream_channels, transient: h.transient, lm: lm, silence: h.silence };
  let st1: Decoder = apply_postfilter(synthesis(s.st, s.x, s.y, syn), h, lm);
  let st2: Decoder = keep_energy(st1, f.stream_channels, h.transient, 1 << lm, f.start, f.end);
  let (st3, pcm) = deemphasis(Decoder { ...st2, rng: s.d.rng, loss_duration: 0 }, n);
  if (s.d.tell() > 8 * len) {
    return (st3, Err(InternalError));
  }
  if (s.d.error) {
    st3 = Decoder { ...st3, error: true };
  }
  return (st3, Ok(pcm));
}

// Decodes the frame in `packet`.
pub function (own st: Decoder) decode(packet: [u8], f: Frame): (Decoder, Result[f32[], CeltError]) {
  return st.decode_with(range.decoder(packet), packet.len(), f);
}

// Conceals a lost frame.
pub function (own st: Decoder) decode_lost(f: Frame): (Decoder, Result[f32[], CeltError]) {
  return st.decode([], f);
}
```
