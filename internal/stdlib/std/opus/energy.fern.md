# The CELT band energies

This module codes the energy of each CELT band (RFC 6716, section 4.3.2). It is a port of `laplace.c` and the float build of `quant_bands.c` from libopus 1.4.

## Model

An energy is a base-2 logarithm of the band amplitude. The coder codes it in three steps:

The encoder first makes the energy of each band from its amplitude, with `amp2log2`.

1. The coarse step codes an integer for each band. The integer is the difference from a prediction, from the band before it and from the last frame. A Laplace distribution gives its probability.
2. The fine step adds raw bits, as the bit allocation gives them.
3. The final step uses the bits that are left, one bit for each band and channel.

An energy array has `modes.BANDS` values for each channel. Channel `c` of band `i` is at `i + c * modes.BANDS`.

An intra frame does not use the energies of the last frame. The encoder can code a frame both ways and keep the one that is better.

## Contracts

- `start < end <= modes.BANDS`, and `channels` is 1 or 2.
- An energy array has `2 * modes.BANDS` values. The functions change only the values of the bands from `start` to `end`.
- The `error` array of the encoder holds the part of each energy that the steps so far did not code.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<laplace>>

<<coarse>>

<<fine>>

<<amplitudes>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT band energies, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2009 Xiph.Org Foundation
// Copyright (c) 2007-2009 Timothy B. Terriberry
// Written by Timothy B. Terriberry and Jean-Marc Valin
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
import "std/opus/range";
import "std/opus/rate";
```

### `laplace`

`fs` is the probability of 0, out of 32768. `decay` is the ratio of the probability of each next magnitude, out of 16384. Each value keeps a probability of at least 1. A value that is too large for the distribution is clipped, and the encoder gives the clipped value.

```fern
<<laplace>>=
const LAPLACE_MINP: i32 = 1;

const LAPLACE_NMIN: i32 = 16;

function laplace_freq1(fs0: i32, decay: i32): i32 {
  let ft = 32768 - LAPLACE_MINP * 2 * LAPLACE_NMIN - fs0;
  return ft * (16384 - decay) >> 15;
}

function min(a: i32, b: i32): i32 {
  if (a < b) {
    return a;
  }
  return b;
}

function max(a: i32, b: i32): i32 {
  if (a > b) {
    return a;
  }
  return b;
}

// Codes `value` and gives the value that was coded.
pub function laplace_encode(own e: range.Encoder, value: i32, fs0: i32, decay: i32): (range.Encoder, i32) {
  let fl = 0;
  let fs = fs0;
  let out = value;
  if (value != 0) {
    let s = 0;
    if (value < 0) {
      s = -1;
    }
    let val = value + s ^ s;
    fl = fs;
    fs = laplace_freq1(fs, decay);
    let i = 1;
    while (fs > 0 && i < val) {
      fs *= 2;
      fl = fl + fs + 2 * LAPLACE_MINP;
      fs = fs * decay >> 15;
      i += 1;
    }
    if (fs == 0) {
      let ndi_max = 32768 - fl + LAPLACE_MINP - 1;
      ndi_max = ndi_max - s >> 1;
      let di = min(val - i, ndi_max - 1);
      fl = fl + (2 * di + 1 + s) * LAPLACE_MINP;
      fs = min(LAPLACE_MINP, 32768 - fl);
      out = i + di + s ^ s;
    } else {
      fs += LAPLACE_MINP;
      if (s == 0) {
        fl += fs;
      }
    }
  }
  return (e.encode_bin(fl, fl + fs, 15), out);
}

pub function laplace_decode(own d: range.Decoder, fs0: i32, decay: i32): (range.Decoder, i32) {
  let (d1, fm) = d.decode_bin(15);
  let val = 0;
  let fl = 0;
  let fs = fs0;
  if (fm >= fs) {
    val = 1;
    fl = fs;
    fs = laplace_freq1(fs, decay) + LAPLACE_MINP;
    while (fs > LAPLACE_MINP && fm >= fl + 2 * fs) {
      fs *= 2;
      fl += fs;
      fs = (fs - 2 * LAPLACE_MINP) * decay >> 15;
      fs += LAPLACE_MINP;
      val += 1;
    }
    if (fs <= LAPLACE_MINP) {
      let di = fm - fl >> 1;
      val += di;
      fl = fl + 2 * di * LAPLACE_MINP;
    }
    if (fm < fl + fs) {
      val = -val;
    } else {
      fl += fs;
    }
  }
  return (d1.update(fl, min(fl + fs, 32768), 32768), val);
}
```

### `coarse`

The prediction of band `i` is `coef` times its energy in the last frame, plus `prev`. `prev` is the sum of the coded integers of the bands before it, each less a part `beta`. `coef` and `beta` depend on the frame size. An intra frame has no `coef`. With few bits left, a band uses a 3-symbol table, then one bit, and then no bits.

```fern
<<coarse>>=
const PRED_COEF: f32[] = [0.8984375f32, 0.796875f32, 0.6484375f32, 0.5f32];

const BETA_COEF: f32[] = [0.920013427734375f32, 0.67999267578125f32, 0.3699951171875f32, 0.20001220703125f32];

const BETA_INTRA: f32 = 0.149993896484375f32;

const SMALL_ENERGY_ICDF: u8[] = [2u8, 1u8, 0u8];

function fmax(a: f32, b: f32): f32 {
  if (a > b) {
    return a;
  }
  return b;
}

function model_at(lm: i32, intra: boolean, i: i32): i32 {
  let k = 0;
  if (intra) {
    k = 1;
  }
  return (lm * 2 + k) * 42 + 2 * min(i, 20);
}

function coef_of(lm: i32, intra: boolean): f32 {
  if (intra) {
    return 0.0f32;
  }
  return PRED_COEF[lm];
}

function beta_of(lm: i32, intra: boolean): f32 {
  if (intra) {
    return BETA_INTRA;
  }
  return BETA_COEF[lm];
}

// Decodes the coarse energies of the bands from `start` to `end`.
pub function unquant_coarse_energy(own d: range.Decoder, start: i32, end: i32, own old: f32[], intra: boolean, channels: i32, lm: i32): (range.Decoder, f32[]) {
  let coef = coef_of(lm, intra);
  let beta = beta_of(lm, intra);
  let budget = d.storage * 8;
  let prev: f32[] = [0.0f32, 0.0f32];
  for i in start..end {
    for c in 0..channels {
      let left = budget - d.tell();
      let qi: i32 = 0 - 1;
      if (left >= 15) {
        let at = model_at(lm, intra, i);
        let (d1, v) = laplace_decode(d, modes.E_PROB_MODEL[at] << 7, modes.E_PROB_MODEL[at + 1] << 6);
        d = d1;
        qi = v;
      } else if (left >= 2) {
        let (d1, s) = d.icdf(SMALL_ENERGY_ICDF, 2);
        d = d1;
        qi = s >> 1 ^ 0 - (s & 1);
      } else if (left >= 1) {
        let (d1, b) = d.bit_logp(1);
        d = d1;
        qi = 0;
        if (b) {
          qi = 0 - 1;
        }
      }
      let q = qi as f32;
      let at = i + c * modes.BANDS;
      let o = fmax(0.0f32 - 9.0f32, old[at]);
      old = old.with(at, coef * o + prev[c] + q);
      prev = prev.with(c, prev[c] + q - beta * q);
    }
  }
  return (d, old);
}

struct Pass { e: range.Encoder, old: f32[], error: f32[], badness: i32 }

// The integer that the encoder codes, before the encoder clips it.
function target(f: f32, x: f32, decay_bound: f32): i32 {
  let qi = (0.5f32 + f).floor() as i32;
  if (qi < 0 && x < decay_bound) {
    qi = qi + (decay_bound - x) as i32;
    if (qi > 0) {
      qi = 0;
    }
  }
  return qi;
}

function abs(x: i32): i32 {
  if (x < 0) {
    return -x;
  }
  return x;
}

// `qi` limited for the bits that are left: with few bits, a band after the
// first takes a small step. The LFE channel does not raise the energy of its
// upper bands.
function limit(qi0: i32, bits_left: i32, not_first: boolean, lfe_band: boolean): i32 {
  let qi = qi0;
  if (not_first && bits_left < 24) {
    qi = min(1, qi);
  }
  if (not_first && bits_left < 16) {
    qi = max(-1, qi);
  }
  if (lfe_band) {
    qi = min(qi, 0);
  }
  return qi;
}

// Codes `qi` with the bits that are left, and gives the value that was coded.
function code_coarse(own e: range.Encoder, qi0: i32, left: i32, at: i32): (range.Encoder, i32) {
  if (left >= 15) {
    return laplace_encode(e, qi0, modes.E_PROB_MODEL[at] << 7, modes.E_PROB_MODEL[at + 1] << 6);
  }
  if (left >= 2) {
    let qi = max(-1, min(qi0, 1));
    let s = 2 * qi;
    if (qi < 0) {
      s = s ^ 0 - 1;
    }
    return (e.icdf(s, SMALL_ENERGY_ICDF, 2), qi);
  }
  if (left >= 1) {
    let qi = min(0, qi0);
    return (e.bit_logp(qi != 0, 1), qi);
  }
  return (e, -1);
}

function coarse_pass(own e0: range.Encoder, start: i32, end: i32, bands: [f32], old0: f32[], error0: f32[], budget: i32, tell0: i32, channels: i32, lm: i32, intra: boolean, max_decay: f32, lfe: boolean): Pass {
  let e = e0;
  let old = old0;
  let error = error0;
  if (tell0 + 3 <= budget) {
    e = e.bit_logp(intra, 3);
  }
  let coef = coef_of(lm, intra);
  let beta = beta_of(lm, intra);
  let prev: f32[] = [0.0f32, 0.0f32];
  let badness = 0;
  for i in start..end {
    for c in 0..channels {
      let at = i + c * modes.BANDS;
      let x = bands[at];
      let o = fmax(0.0f32 - 9.0f32, old[at]);
      let f = x - coef * o - prev[c];
      let decay_bound = fmax(0.0f32 - 28.0f32, old[at]) - max_decay;
      let qi0 = target(f, x, decay_bound);
      let tell = e.tell();
      let qi = limit(qi0, budget - tell - 3 * channels * (end - i), i != start, lfe && i >= 2);
      let (e1, coded) = code_coarse(e, qi, budget - tell, model_at(lm, intra, i));
      e = e1;
      qi = coded;
      let q = qi as f32;
      error = error.with(at, f - q);
      badness += abs(qi0 - qi);
      old = old.with(at, coef * o + prev[c] + q);
      prev = prev.with(c, prev[c] + q - beta * q);
    }
  }
  if (lfe) {
    badness = 0;
  }
  return Pass { e: e, old: old, error: error, badness: badness };
}

// The distance between the energies of a frame and those of the last frame,
// at most 200.
function loss_distortion(bands: [f32], old: [f32], start: i32, end: i32, channels: i32): f32 {
  let dist: f32 = 0.0f32;
  for c in 0..channels {
    for i in start..end {
      let at = i + c * modes.BANDS;
      let d = bands[at] - old[at];
      dist = dist + d * d;
    }
  }
  if (200.0f32 < dist) {
    return 200.0f32;
  }
  return dist;
}

pub struct Coarse { e: range.Encoder, old: f32[], error: f32[], delayed_intra: f32 }

pub struct CoarseRequest { start: i32, end: i32, eff_end: i32, budget: i32, channels: i32, lm: i32, available_bytes: i32, force_intra: boolean, delayed_intra: f32, two_pass: boolean, loss_rate: i32, lfe: boolean }

// Whether the frame is intra before the encoder tries both ways: when the
// caller asks for it, or when the last frames drifted and the frame has room.
function start_intra(r: CoarseRequest): boolean {
  let width = r.end - r.start;
  return r.force_intra || !r.two_pass && r.delayed_intra > (2 * r.channels * width) as f32 && r.available_bytes > width * r.channels;
}

// How far the energy of a band can fall in one frame.
function max_decay_of(r: CoarseRequest): f32 {
  if (r.lfe) {
    return 3.0f32;
  }
  let room: f32 = 0.125f32 * (r.available_bytes as f32);
  if (r.end - r.start > 10 && room < 16.0f32) {
    return room;
  }
  return 16.0f32;
}

// Whether the intra pass is better: it moved the energies less, or the same
// with fewer bits, after the bias for packet loss.
function intra_wins(first: Pass, second: Pass, intra_bias: i32, tell_intra: i32): boolean {
  return first.badness < second.badness || first.badness == second.badness && second.e.tell_frac() + intra_bias > tell_intra;
}

// Codes the coarse energies of `bands`, and gives the coded energies, the error
// that is left, and the next `delayed_intra`.
pub function quant_coarse_energy(e: range.Encoder, r: CoarseRequest, bands: [f32], old: f32[], error: f32[]): Coarse {
  let tell = e.tell();
  let two_pass = r.two_pass && tell + 3 <= r.budget;
  let intra = start_intra(r) && tell + 3 <= r.budget;
  let intra_bias = (r.budget as f32 * r.delayed_intra * (r.loss_rate as f32) / ((r.channels * 512) as f32)) as i32;
  let new_distortion = loss_distortion(bands, old, r.start, r.eff_end, r.channels);
  let max_decay = max_decay_of(r);
  let first = Pass { e: e, old: old, error: error, badness: 0 };
  if (two_pass || intra) {
    first = coarse_pass(range.Encoder { ...e }, r.start, r.end, bands, old, error, r.budget, tell, r.channels, r.lm, true, max_decay, r.lfe);
  }
  let chosen = first;
  if (!intra) {
    let tell_intra = first.e.tell_frac();
    let second = coarse_pass(range.Encoder { ...e }, r.start, r.end, bands, old, error, r.budget, tell, r.channels, r.lm, false, max_decay, r.lfe);
    chosen = second;
    if (two_pass && intra_wins(first, second, intra_bias, tell_intra)) {
      chosen = first;
      intra = true;
    }
  }
  let delayed = new_distortion;
  if (!intra) {
    delayed = PRED_COEF[r.lm] * PRED_COEF[r.lm] * r.delayed_intra + new_distortion;
  }
  return Coarse { e: chosen.e, old: chosen.old, error: chosen.error, delayed_intra: delayed };
}
```

### `fine`

A fine step of `n` bits splits the error into 2 to the power `n` parts, and codes the part that the error is in. The final step codes the sign of the error that is left. It codes the bands that the allocation gave priority 0 first, and then the bands with priority 1.

```fern
<<fine>>=
function fine_offset(q2: i32, bits: i32): f32 {
  return (q2 as f32 + 0.5f32) * ((1 << 14 - bits) as f32) * (1.0f32 / 16384.0f32) - 0.5f32;
}

function final_offset(q2: i32, bits: i32): f32 {
  return (q2 as f32 - 0.5f32) * ((1 << 14 - bits - 1) as f32) * (1.0f32 / 16384.0f32);
}

pub function unquant_fine_energy(own d: range.Decoder, start: i32, end: i32, own old: f32[], fine_quant: [i32], channels: i32): (range.Decoder, f32[]) {
  for i in start..end {
    if (fine_quant[i] > 0) {
      for c in 0..channels {
        let (d1, q2) = d.raw(fine_quant[i]);
        d = d1;
        let at = i + c * modes.BANDS;
        old = old.with(at, old[at] + fine_offset(q2 as i32, fine_quant[i]));
      }
    }
  }
  return (d, old);
}

pub function unquant_energy_finalise(own d: range.Decoder, start: i32, end: i32, own old: f32[], fine_quant: [i32], fine_priority: [i32], bits_left0: i32, channels: i32): (range.Decoder, f32[]) {
  let bits_left = bits_left0;
  for prio in 0..2 {
    let i = start;
    while (i < end && bits_left >= channels) {
      if (fine_quant[i] < rate.MAX_FINE_BITS && fine_priority[i] == prio) {
        for c in 0..channels {
          let (d1, q2) = d.raw(1);
          d = d1;
          let at = i + c * modes.BANDS;
          old = old.with(at, old[at] + final_offset(q2 as i32, fine_quant[i]));
          bits_left -= 1;
        }
      }
      i += 1;
    }
  }
  return (d, old);
}

pub struct Fine { e: range.Encoder, old: f32[], error: f32[] }

pub function quant_fine_energy(own e: range.Encoder, start: i32, end: i32, own old: f32[], own error: f32[], fine_quant: [i32], channels: i32): Fine {
  for i in start..end {
    let bits = fine_quant[i];
    if (bits > 0) {
      let frac = 1 << bits;
      for c in 0..channels {
        let at = i + c * modes.BANDS;
        let q2 = ((error[at] + 0.5f32) * (frac as f32)).floor() as i32;
        q2 = max(0, min(q2, frac - 1));
        e = e.bits(q2 as u32, bits);
        let offset = fine_offset(q2, bits);
        old = old.with(at, old[at] + offset);
        error = error.with(at, error[at] - offset);
      }
    }
  }
  return Fine { e: e, old: old, error: error };
}

pub function quant_energy_finalise(own e: range.Encoder, start: i32, end: i32, own old: f32[], own error: f32[], fine_quant: [i32], fine_priority: [i32], bits_left0: i32, channels: i32): Fine {
  let bits_left = bits_left0;
  for prio in 0..2 {
    let i = start;
    while (i < end && bits_left >= channels) {
      if (fine_quant[i] < rate.MAX_FINE_BITS && fine_priority[i] == prio) {
        for c in 0..channels {
          let at = i + c * modes.BANDS;
          let q2 = 1;
          if (error[at] < 0.0f32) {
            q2 = 0;
          }
          e = e.bits(q2 as u32, 1);
          let offset = final_offset(q2, fine_quant[i]);
          old = old.with(at, old[at] + offset);
          error = error.with(at, error[at] - offset);
          bits_left -= 1;
        }
      }
      i += 1;
    }
  }
  return Fine { e: e, old: old, error: error };
}
```

### `amplitudes`

`celt_log2` is the exact base-2 logarithm of the float build. libopus can also use an approximation, but it does not by default. The log is in f64, then the result becomes f32, as in libopus. A band from `eff_end` to `end` has no signal, because the input had a lower sample rate. It gets the energy -14.

```fern
<<amplitudes>>=
pub function celt_log2(x: f32): f32 {
  return (1.442695040888963387 * (x as f64).log()) as f32;
}

// The energy of each band, from its amplitude in `band_e`, less the mean
// energy of the band.
pub function amp2log2(band_e: [f32], eff_end: i32, end: i32, channels: i32): f32[] {
  let out: f32[] = [];
  for c in 0..channels {
    for i in 0..modes.BANDS {
      if (i < eff_end) {
        out = out.append(celt_log2(band_e[i + c * modes.BANDS]) - modes.E_MEANS[i]);
      } else if (i < end) {
        out = out.append(-14.0f32);
      } else {
        out = out.append(0.0f32);
      }
    }
  }
  return out;
}
```
