# The CELT bit allocation

This module divides the bits of a CELT frame between the bands (RFC 6716, section 4.3.3). It is a port of `rate.h` and `rate.c` from libopus 1.4. The encoder and the decoder run the same calculation, so they get the same allocation.

## Model

Bit counts are in eighths of a bit. `compute_allocation` finds the two rows of the allocation table that bracket the bits of the frame, and interpolates between them in 64 steps. Then it decides how many bands to code, and splits the bits of each band between fine energy and the PVQ shape.

Some of these decisions are symbols in the frame. The encoder codes the skipped bands, the intensity band and the dual stereo flag, and the decoder reads them. A `range.Coder` holds the side.

`bits2pulses` and `pulses2bits` convert between the bits of a band and its pulses. They use the pulse cache of `std/opus/modes`. A pulse count above 8 is a pseudo-pulse: `get_pulses` gives the real number of pulses.

## Contracts

- `start` and `end` are band numbers, with `start < end <= modes.BANDS`.
- `offsets` and `cap` have a value for each band of the mode.
- `channels` is 1 or 2, and `lm` is from 0 to `modes.MAX_LM`.
- The result has a value for each band of the mode. Bands outside `start` to `end` get 0.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<pulses>>

<<allocation>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT bit allocation, ported from libopus 1.4.
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
import "std/opus/modes";
import "std/opus/range";
```

### `pulses`

The cache of a band size starts with its largest pseudo-pulse count. Then it holds the bits for each count, less one.

```fern
<<pulses>>=
pub const BITRES: i32 = 3;

pub const MAX_FINE_BITS: i32 = 8;

const FINE_OFFSET: i32 = 21;

const LOG_MAX_PSEUDO: i32 = 6;

const ALLOC_STEPS: i32 = 6;

// The bits that the intensity band needs, in eighths, for each number of bands.
const LOG2_FRAC: i32[] = [0, 8, 13, 16, 19, 21, 23, 24, 26, 27, 28, 29, 30, 31, 32, 32, 33, 34, 34, 35, 36, 36, 37, 37];

// The number of pulses of the pseudo-pulse count `i`.
pub function get_pulses(i: i32): i32 {
  if (i < 8) {
    return i;
  }
  return 8 + (i & 7) << (i >> 3) - 1;
}

function cache_at(band: i32, lm: i32): i32 {
  return modes.CACHE_INDEX[(lm + 1) * modes.BANDS + band];
}

// The pseudo-pulse count of `band` that uses the number of bits nearest to
// `bits`.
pub function bits2pulses(band: i32, lm: i32, bits: i32): i32 {
  let at: i32 = cache_at(band, lm);
  let lo: i32 = 0;
  let hi: i32 = modes.CACHE_BITS[at];
  let want: i32 = bits - 1;
  for i in 0..LOG_MAX_PSEUDO {
    let mid: i32 = lo + hi + 1 >> 1;
    if (modes.CACHE_BITS[at + mid] >= want) {
      hi = mid;
    } else {
      lo = mid;
    }
  }
  let below: i32 = 0 - 1;
  if (lo > 0) {
    below = modes.CACHE_BITS[at + lo];
  }
  if (want - below <= modes.CACHE_BITS[at + hi] - want) {
    return lo;
  }
  return hi;
}

// The bits of `band` with the pseudo-pulse count `pulses`.
pub function pulses2bits(band: i32, lm: i32, pulses: i32): i32 {
  if (pulses == 0) {
    return 0;
  }
  return modes.CACHE_BITS[cache_at(band, lm) + pulses] + 1;
}
```

### `allocation`

The values of a band of the mode are at its index in each array. `pulses` holds the PVQ bits of each band, which the band coder converts to pulses.

```fern
<<allocation>>=
pub struct Request { start: i32, end: i32, offsets: i32[], cap: i32[], alloc_trim: i32, intensity: i32, dual_stereo: boolean, total: i32, channels: i32, lm: i32, prev: i32, signal_bandwidth: i32 }

pub struct Allocation { coded_bands: i32, intensity: i32, dual_stereo: boolean, balance: i32, pulses: i32[], ebits: i32[], fine_priority: i32[] }

function width(j: i32): i32 {
  return modes.EBANDS[j + 1] - modes.EBANDS[j];
}

function zeros(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n {
    out = out.append(0);
  }
  return out;
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

// The bits of row `row` of the allocation table for band `j`.
function row_bits(r: Request, row: i32, j: i32): i32 {
  return r.channels * width(j) * modes.ALLOC[row * modes.BANDS + j] << r.lm >> 2;
}

struct Plan { bits1: i32[], bits2: i32[], thresh: i32[], skip_start: i32, skip_rsv: i32, intensity_rsv: i32, dual_stereo_rsv: i32, total: i32 }

// The bits of each band with an interpolation step `step` from 0 to 64, as the
// two passes of interp_bits2pulses count them.
function interp_sum(r: Request, p: Plan, step: i32): i32 {
  let floor: i32 = r.channels << BITRES;
  let psum: i32 = 0;
  let done: boolean = false;
  let j: i32 = r.end;
  while (j > r.start) {
    j = j - 1;
    let tmp: i32 = p.bits1[j] + (step * p.bits2[j] >> ALLOC_STEPS);
    if (tmp >= p.thresh[j] || done) {
      done = true;
      psum = psum + min(tmp, r.cap[j]);
    } else if (tmp >= floor) {
      psum = psum + floor;
    }
  }
  return psum;
}

function trim_offsets(r: Request): i32[] {
  let out: i32[] = zeros(modes.BANDS);
  for j in r.start..r.end {
    let t: i32 = r.channels * width(j) * (r.alloc_trim - 5 - r.lm) * (r.end - j - 1) * (1 << r.lm + BITRES) >> 6;
    if (width(j) << r.lm == 1) {
      t = t - (r.channels << BITRES);
    }
    out = out.with(j, t);
  }
  return out;
}

struct Reserve { total: i32, skip_rsv: i32, intensity_rsv: i32, dual_stereo_rsv: i32 }

// The bits kept for the skip flag, the intensity band and the dual stereo flag.
function reserve(r: Request): Reserve {
  let total: i32 = max(r.total, 0);
  let skip_rsv: i32 = 0;
  if (total >= 1 << BITRES) {
    skip_rsv = 1 << BITRES;
  }
  total = total - skip_rsv;
  let intensity_rsv: i32 = 0;
  let dual_stereo_rsv: i32 = 0;
  if (r.channels == 2) {
    intensity_rsv = LOG2_FRAC[r.end - r.start];
    if (intensity_rsv > total) {
      intensity_rsv = 0;
    } else {
      total = total - intensity_rsv;
      if (total >= 1 << BITRES) {
        dual_stereo_rsv = 1 << BITRES;
      }
      total = total - dual_stereo_rsv;
    }
  }
  return Reserve { total: total, skip_rsv: skip_rsv, intensity_rsv: intensity_rsv, dual_stereo_rsv: dual_stereo_rsv };
}

// The bits under which a band gets no shape bits.
function thresholds(r: Request): i32[] {
  let thresh: i32[] = zeros(modes.BANDS);
  for j in r.start..r.end {
    thresh = thresh.with(j, max(r.channels << BITRES, 3 * width(j) << r.lm << BITRES >> 4));
  }
  return thresh;
}

// The bits of row `row` for band `j`, with the trim.
function row_trimmed(r: Request, row: i32, j: i32, trim: i32): i32 {
  let b: i32 = row_bits(r, row, j);
  if (b > 0) {
    b = max(0, b + trim);
  }
  return b;
}

// The bits that the row `row` gives the frame.
function row_sum(r: Request, row: i32, thresh: [i32], trim: [i32]): i32 {
  let done: boolean = false;
  let psum: i32 = 0;
  let j: i32 = r.end;
  while (j > r.start) {
    j = j - 1;
    let b: i32 = row_trimmed(r, row, j, trim[j]) + r.offsets[j];
    if (b >= thresh[j] || done) {
      done = true;
      psum = psum + min(b, r.cap[j]);
    } else if (b >= r.channels << BITRES) {
      psum = psum + (r.channels << BITRES);
    }
  }
  return psum;
}

// The first row of the table whose bits do not fit in `total`.
function first_row_over(r: Request, total: i32, thresh: [i32], trim: [i32]): i32 {
  let lo: i32 = 1;
  let hi: i32 = modes.ALLOC_VECTORS - 1;
  while (lo <= hi) {
    let mid: i32 = lo + hi >> 1;
    if (row_sum(r, mid, thresh, trim) > total) {
      hi = mid - 1;
    } else {
      lo = mid + 1;
    }
  }
  return lo;
}

// The two rows of the table that bracket the frame, as bits1, the lower row,
// and bits2, the step up to the higher row.
function plan(r: Request): Plan {
  let rs: Reserve = reserve(r);
  let thresh: i32[] = thresholds(r);
  let trim: i32[] = trim_offsets(r);
  let hi: i32 = first_row_over(r, rs.total, thresh, trim);
  let lo: i32 = hi - 1;
  let bits1: i32[] = zeros(modes.BANDS);
  let bits2: i32[] = zeros(modes.BANDS);
  let skip_start: i32 = r.start;
  for j in r.start..r.end {
    let b1: i32 = row_trimmed(r, lo, j, trim[j]);
    let b2: i32 = r.cap[j];
    if (hi < modes.ALLOC_VECTORS) {
      b2 = row_trimmed(r, hi, j, trim[j]);
    } else if (b2 > 0) {
      b2 = max(0, b2 + trim[j]);
    }
    if (lo > 0) {
      b1 = b1 + r.offsets[j];
    }
    b2 = b2 + r.offsets[j];
    if (r.offsets[j] > 0) {
      skip_start = j;
    }
    bits1 = bits1.with(j, b1);
    bits2 = bits2.with(j, max(0, b2 - b1));
  }
  return Plan { bits1: bits1, bits2: bits2, thresh: thresh, skip_start: skip_start, skip_rsv: rs.skip_rsv, intensity_rsv: rs.intensity_rsv, dual_stereo_rsv: rs.dual_stereo_rsv, total: rs.total };
}

// The interpolation step: the largest of 64 steps whose bits fit.
function interp_step(r: Request, p: Plan): i32 {
  let lo: i32 = 0;
  let hi: i32 = 1 << ALLOC_STEPS;
  for i in 0..ALLOC_STEPS {
    let mid: i32 = lo + hi >> 1;
    if (interp_sum(r, p, mid) > p.total) {
      hi = mid;
    } else {
      lo = mid;
    }
  }
  return lo;
}

struct Skip { c: range.Coder, coded: i32, bits: i32[], psum: i32, total: i32, intensity_rsv: i32 }

// The encoder's choice to code band `j` and stop skipping. A threshold with
// some hysteresis keeps a band from going in and out of the coded set.
function keep_band(r: Request, coded: i32, j: i32, band_bits: i32, band_width: i32): boolean {
  let depth: i32 = 0;
  if (coded > 17) {
    depth = 9;
    if (j < r.prev) {
      depth = 7;
    }
  }
  return coded <= r.start + 2 || band_bits > depth * band_width << r.lm << BITRES >> 4 && j <= r.signal_bandwidth;
}

// Decides how many bands to code, from the end. A band above its threshold
// codes a skip flag. A band below it is skipped without a flag.
function skip_bands(own c: range.Coder, r: Request, p: Plan, own bits: i32[], psum0: i32): Skip {
  let floor: i32 = r.channels << BITRES;
  let psum: i32 = psum0;
  let total: i32 = p.total;
  let intensity_rsv: i32 = p.intensity_rsv;
  let coded: i32 = r.end;
  while (true) {
    let j: i32 = coded - 1;
    if (j <= p.skip_start) {
      total = total + p.skip_rsv;
      break;
    }
    let span: i32 = modes.EBANDS[coded] - modes.EBANDS[r.start];
    let left: i32 = total - psum;
    let percoeff: i32 = left / span;
    left = left - span * percoeff;
    let rem: i32 = max(left - (modes.EBANDS[j] - modes.EBANDS[r.start]), 0);
    let band_width: i32 = modes.EBANDS[coded] - modes.EBANDS[j];
    let band_bits: i32 = bits[j] + percoeff * band_width + rem;
    if (band_bits >= max(p.thresh[j], floor + (1 << BITRES))) {
      let keep: boolean = c.encoding() && keep_band(r, coded, j, band_bits, band_width);
      let (c1, stop) = c.bit(keep, 1);
      c = c1;
      if (stop) {
        break;
      }
      psum = psum + (1 << BITRES);
      band_bits = band_bits - (1 << BITRES);
    }
    psum = psum - (bits[j] + intensity_rsv);
    if (intensity_rsv > 0) {
      intensity_rsv = LOG2_FRAC[j - r.start];
    }
    psum = psum + intensity_rsv;
    if (band_bits >= floor) {
      psum = psum + floor;
      bits = bits.with(j, floor);
    } else {
      bits = bits.with(j, 0);
    }
    coded = coded - 1;
  }
  return Skip { c: c, coded: coded, bits: bits, psum: psum, total: total, intensity_rsv: intensity_rsv };
}

struct Split { pulses: i32[], ebits: i32[], fine_priority: i32[], balance: i32 }

struct FineSplit { ebits: i32, priority: i32 }

// The fine energy bits of band `j`, which has `n` values and `b` bits, and
// whether it is a candidate for the final fine energy pass.
function fine_split(r: Request, j: i32, n: i32, b: i32, intensity: i32, dual_stereo: boolean): FineSplit {
  let c: i32 = r.channels;
  let den: i32 = c * n;
  if (c == 2 && n > 2 && !dual_stereo && j < intensity) {
    den = den + 1;
  }
  let nclogn: i32 = den * (modes.LOGN[j] + (r.lm << BITRES));
  let offset: i32 = (nclogn >> 1) - den * FINE_OFFSET;
  if (n == 2) {
    offset = offset + (den << BITRES >> 2);
  }
  if (b + offset < den * 2 << BITRES) {
    offset = offset + (nclogn >> 2);
  } else if (b + offset < den * 3 << BITRES) {
    offset = offset + (nclogn >> 3);
  }
  let eb: i32 = max(0, b + offset + (den << BITRES - 1)) / den >> BITRES;
  if (c * eb > b >> BITRES) {
    eb = b >> c - 1 >> BITRES;
  }
  eb = min(eb, MAX_FINE_BITS);
  let fp: i32 = 0;
  if (eb * (den << BITRES) >= b + offset) {
    fp = 1;
  }
  return FineSplit { ebits: eb, priority: fp };
}

// Splits the bits of each coded band between fine energy and the shape, and
// gives the skipped bands all their bits as fine energy.
function split_bands(r: Request, coded: i32, own bits: i32[], intensity: i32, dual_stereo: boolean): Split {
  let ebits: i32[] = zeros(modes.BANDS);
  let fine: i32[] = zeros(modes.BANDS);
  let stereo: i32 = 0;
  if (r.channels > 1) {
    stereo = 1;
  }
  let c: i32 = r.channels;
  let balance: i32 = 0;
  for j in r.start..coded {
    let n: i32 = width(j) << r.lm;
    let bit: i32 = bits[j] + balance;
    let excess: i32 = 0;
    let eb: i32 = 0;
    let fp: i32 = 0;
    if (n > 1) {
      excess = max(bit - r.cap[j], 0);
      let b: i32 = bit - excess;
      let fs: FineSplit = fine_split(r, j, n, b, intensity, dual_stereo);
      eb = fs.ebits;
      fp = fs.priority;
      bits = bits.with(j, b - (c * eb << BITRES));
    } else {
      excess = max(0, bit - (c << BITRES));
      bits = bits.with(j, bit - excess);
      fp = 1;
    }
    if (excess > 0) {
      let extra_fine: i32 = min(excess >> stereo + BITRES, MAX_FINE_BITS - eb);
      eb = eb + extra_fine;
      let extra_bits: i32 = extra_fine * c << BITRES;
      fp = 0;
      if (extra_bits >= excess - balance) {
        fp = 1;
      }
      excess = excess - extra_bits;
    }
    balance = excess;
    ebits = ebits.with(j, eb);
    fine = fine.with(j, fp);
  }
  for j in coded..r.end {
    let eb: i32 = bits[j] >> stereo >> BITRES;
    ebits = ebits.with(j, eb);
    bits = bits.with(j, 0);
    if (eb < 1) {
      fine = fine.with(j, 1);
    }
  }
  return Split { pulses: bits, ebits: ebits, fine_priority: fine, balance: balance };
}

struct Stereo { c: range.Coder, intensity: i32, dual_stereo: boolean, total: i32 }

// Codes the intensity band and the dual stereo flag, when their bits are kept.
// The bits of a flag that is not coded go back to the total.
function stereo_params(own c: range.Coder, r: Request, p: Plan, coded: i32, intensity_rsv: i32, total0: i32): Stereo {
  let total: i32 = total0;
  let intensity: i32 = 0;
  if (intensity_rsv > 0) {
    let want: u32 = 0u32;
    if (c.encoding()) {
      want = (min(r.intensity, coded) - r.start) as u32;
    }
    let (c3, v) = c.uint_of(want, (coded + 1 - r.start) as u32);
    c = c3;
    intensity = r.start + v as i32;
  }
  let dual_stereo_rsv: i32 = p.dual_stereo_rsv;
  if (intensity <= r.start) {
    total = total + dual_stereo_rsv;
    dual_stereo_rsv = 0;
  }
  let dual_stereo: boolean = false;
  if (dual_stereo_rsv > 0) {
    let (c4, b) = c.bit(r.dual_stereo, 1);
    c = c4;
    dual_stereo = b;
  }
  return Stereo { c: c, intensity: intensity, dual_stereo: dual_stereo, total: total };
}

// Divides `r.total` bits between the bands of a frame.
pub function compute_allocation(own c: range.Coder, r: Request): (range.Coder, Allocation) {
  let p: Plan = plan(r);
  let step: i32 = interp_step(r, p);
  let floor: i32 = r.channels << BITRES;
  let bits: i32[] = zeros(modes.BANDS);
  let psum: i32 = 0;
  let done: boolean = false;
  let j: i32 = r.end;
  while (j > r.start) {
    j = j - 1;
    let tmp: i32 = p.bits1[j] + (step * p.bits2[j] >> ALLOC_STEPS);
    if (tmp < p.thresh[j] && !done) {
      if (tmp >= floor) {
        tmp = floor;
      } else {
        tmp = 0;
      }
    } else {
      done = true;
    }
    tmp = min(tmp, r.cap[j]);
    bits = bits.with(j, tmp);
    psum = psum + tmp;
  }
  let s: Skip = skip_bands(c, r, p, bits, psum);
  let Skip { c: c2, coded, bits: bits2, psum: psum2, total: total0, intensity_rsv } = s;
  c = c2;
  let st: Stereo = stereo_params(c, r, p, coded, intensity_rsv, total0);
  c = st.c;
  let total: i32 = st.total;
  let intensity: i32 = st.intensity;
  let dual_stereo: boolean = st.dual_stereo;
  let span: i32 = modes.EBANDS[coded] - modes.EBANDS[r.start];
  let left: i32 = total - psum2;
  let percoeff: i32 = left / span;
  left = left - span * percoeff;
  let bits3: i32[] = bits2;
  for k in r.start..coded {
    bits3 = bits3.with(k, bits3[k] + percoeff * width(k));
  }
  for k in r.start..coded {
    let tmp: i32 = min(left, width(k));
    bits3 = bits3.with(k, bits3[k] + tmp);
    left = left - tmp;
  }
  let sp: Split = split_bands(r, coded, bits3, intensity, dual_stereo);
  return (c, Allocation { coded_bands: coded, intensity: intensity, dual_stereo: dual_stereo, balance: sp.balance, pulses: sp.pulses, ebits: sp.ebits, fine_priority: sp.fine_priority });
}
```
