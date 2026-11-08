# The CELT bands

This module codes the normalised spectrum of a CELT frame, band by band (RFC 6716, section 4.3.4). It is a port of the float build of `bands.c` from libopus 1.4. The encoder and the decoder run the same code. A `range.Coder` holds the side.

## Model

Each band has a shape of unit length. A band with many bits is split in two halves, and an angle `itheta` gives the share of the energy in each half. The split repeats until a half has few enough bits for one PVQ codeword. In a stereo frame, the angle splits each band into the mid and the side.

A band with no bits takes the shape of a lower band, the lowband, with some noise. The folding memory `norm` holds the coded shapes of the bands so far for that.

A transient frame has `b` short blocks. `tf_change` changes the time and frequency resolution of a band with Haar steps. The collapse mask of a band has a bit for each block that has energy, and keeps 8 bits, as in libopus.

The values of a frame are in four arrays of `Bands`: `x` and `y` for the two channels, `norm` for the folding memory of both channels, and `scratch` for a copy of a lowband. A `Ref` names an array and a start in it. In libopus, the decoder uses the end of `x` as its scratch array. A separate array gives the same values in the coded bands.

## Contracts

- `start < end <= modes.BANDS`, and `lm` is from 0 to `modes.MAX_LM`.
- `x` and `y` hold `modes.SHORT_MDCT_SIZE << lm` values. The functions change only the values of the bands from `start` to `end`.
- The arithmetic is in f32, in the same order as libopus, so the results are the same bits.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<buffers>>

<<coder>>

<<integer helpers>>

<<vector helpers>>

<<theta>>

<<partition>>

<<band>>

<<stereo band>>

<<all bands>>

<<analysis>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT bands, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2009 Xiph.Org Foundation
// Copyright (c) 2008-2009 Gregory Maxwell
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
import "std/f64";
import "std/opus/modes";
import "std/opus/pitch";
import "std/opus/range";
import "std/opus/rate";
import "std/opus/vq";
```

### `buffers`

The state of the band coder is a `Work`. It holds the range coder, the arrays, and the values of the band in progress. Each function takes the `Work` and gives the next one.

```fern
<<buffers>>=
pub const BUF_NONE: i32 = -1;

pub const BUF_X: i32 = 0;

pub const BUF_Y: i32 = 1;

pub const BUF_NORM: i32 = 2;

pub const BUF_SCRATCH: i32 = 3;

pub struct Bands { x: f32[], y: f32[], norm: f32[], scratch: f32[] }

pub struct Ref { buf: i32, at: i32 }

pub struct Ctx { encode: boolean, legacy_encoder: boolean, resynth: boolean, i: i32, intensity: i32, spread: i32, tf_change: i32, remaining_bits: i32, seed: u32, theta_round: i32, disable_inv: boolean, avoid_split_noise: boolean }

pub struct Work { c: range.Coder, b: Bands, ctx: Ctx, band_e: f32[] }

function none(): Ref {
  return Ref { buf: BUF_NONE, at: 0 };
}

function is_none(r: Ref): boolean {
  return r.buf == BUF_NONE;
}

function plus(r: Ref, n: i32): Ref {
  return Ref { buf: r.buf, at: r.at + n };
}

function get(b: Bands, r: Ref, j: i32): f32 {
  if (r.buf == BUF_X) {
    return b.x[r.at + j];
  }
  if (r.buf == BUF_Y) {
    return b.y[r.at + j];
  }
  if (r.buf == BUF_NORM) {
    return b.norm[r.at + j];
  }
  return b.scratch[r.at + j];
}

function set(own b: Bands, r: Ref, j: i32, v: f32): Bands {
  let Bands { x, y, norm, scratch } = b;
  let at = r.at + j;
  if (r.buf == BUF_X) {
    return Bands { x: x.with(at, v), y: y, norm: norm, scratch: scratch };
  }
  if (r.buf == BUF_Y) {
    return Bands { x: x, y: y.with(at, v), norm: norm, scratch: scratch };
  }
  if (r.buf == BUF_NORM) {
    return Bands { x: x, y: y, norm: norm.with(at, v), scratch: scratch };
  }
  return Bands { x: x, y: y, norm: norm, scratch: scratch.with(at, v) };
}

function wget(w: Work, r: Ref, j: i32): f32 {
  return get(w.b, r, j);
}

function wset(own w: Work, r: Ref, j: i32, v: f32): Work {
  let Work { c, b, ctx, band_e } = w;
  return Work { c: c, b: set(b, r, j, v), ctx: ctx, band_e: band_e };
}

function with_ctx(own w: Work, ctx: Ctx): Work {
  let Work { c, b, ctx: _, band_e } = w;
  return Work { c: c, b: b, ctx: ctx, band_e: band_e };
}

function spend(own w: Work, bits: i32): Work {
  let ctx = Ctx { ...w.ctx, remaining_bits: w.ctx.remaining_bits - bits };
  return with_ctx(w, ctx);
}

// The `n` values at `r`, as a new array.
function take(w: Work, r: Ref, n: i32): f32[] {
  let out: f32[] = [];
  for j in 0..n {
    out = out.append(wget(w, r, j));
  }
  return out;
}

// Writes the `n` values of `v` at `r`.
function put(own w: Work, r: Ref, v: [f32], n: i32): Work {
  for j in 0..n {
    w = wset(w, r, j, v[j]);
  }
  return w;
}

function copy(own w: Work, dst: Ref, src: Ref, n: i32): Work {
  let v = take(w, src, n);
  return put(w, dst, v, n);
}

// A copy of `w` that the caller can give to a function that takes it. The
// arrays are shared until one of the two is written.
function clone(w: Work): Work {
  return Work { c: w.c, b: w.b, ctx: w.ctx, band_e: w.band_e };
}
```

### `coder`

```fern
<<coder>>=
function code_bit(own w: Work, v: boolean, logp: i32): (Work, boolean) {
  let Work { c, b, ctx, band_e } = w;
  let (c1, bit) = c.bit(v, logp);
  return (Work { c: c1, b: b, ctx: ctx, band_e: band_e }, bit);
}

function code_uint(own w: Work, v: u32, ft: u32): (Work, u32) {
  let Work { c, b, ctx, band_e } = w;
  let (c1, u) = c.uint_of(v, ft);
  return (Work { c: c1, b: b, ctx: ctx, band_e: band_e }, u);
}

// One raw bit: the encoder writes `v`, and the decoder reads a bit.
function code_raw_bit(own w: Work, v: u32): (Work, u32) {
  let Work { c, b, ctx, band_e } = w;
  match (c) {
    range.Encoding(e) => {
      return (Work { c: range.Encoding(e.bits(v, 1)), b: b, ctx: ctx, band_e: band_e }, v);
    },
    range.Decoding(d) => {
      let (d1, u) = d.raw(1);
      return (Work { c: range.Decoding(d1), b: b, ctx: ctx, band_e: band_e }, u);
    }
  }
}
```

### `integer helpers`

`bitexact_cos` and `bitexact_log2tan` are integer approximations. Thus the encoder and the decoder split the bits of a band in the same way on each platform.

```fern
<<integer helpers>>=
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

pub function lcg_rand(seed: u32): u32 {
  return 1664525u32 * seed + 1013904223u32;
}

// The product of two Q15 values, rounded, as libopus calculates it in 16 bits.
function frac_mul16(a: i32, b: i32): i32 {
  let a16 = a << 16 >> 16;
  let b16 = b << 16 >> 16;
  return 16384 + a16 * b16 >> 15;
}

pub function bitexact_cos(x: i32): i32 {
  let x16 = x << 16 >> 16;
  let x2 = 4096 + x16 * x16 >> 13;
  x2 = 32767 - x2 + frac_mul16(x2, -7651 + frac_mul16(x2, 8277 + frac_mul16(-626, x2))) << 16 >> 16;
  return 1 + x2 << 16 >> 16;
}

function ilog(v: i32): i32 {
  return 32 - __clz32(v as u32);
}

pub function bitexact_log2tan(isin0: i32, icos0: i32): i32 {
  let lc = ilog(icos0);
  let ls = ilog(isin0);
  let icos = icos0 << 15 - lc;
  let isin = isin0 << 15 - ls;
  return (ls - lc) * (1 << 11) + frac_mul16(isin, frac_mul16(isin, -2597) + 7932) - frac_mul16(icos, frac_mul16(icos, -2597) + 7932);
}

pub function isqrt32(v0: u32): u32 {
  let v = v0;
  let g: u32 = 0u32;
  let bshift = ilog(v0 as i32) - 1 >> 1;
  let b = 1u32 << bshift as u32;
  while (bshift >= 0) {
    let t = (g << 1u32) + b << bshift as u32;
    if (t <= v) {
      g += b;
      v -= t;
    }
    b = b >> 1u32;
    bshift -= 1;
  }
  return g;
}

const EXP2_TABLE8: i32[] = [16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048];

// The number of steps of the angle of a split.
function compute_qn(n: i32, b: i32, offset: i32, pulse_cap: i32, stereo: boolean): i32 {
  let n2 = 2 * n - 1;
  if (stereo && n == 2) {
    n2 -= 1;
  }
  let qb = (b + n2 * offset) / n2;
  qb = min(b - pulse_cap - (4 << rate.BITRES), qb);
  qb = min(8 << rate.BITRES, qb);
  if (qb < 1 << rate.BITRES >> 1) {
    return 1;
  }
  let qn = EXP2_TABLE8[qb & 7] >> 14 - (qb >> rate.BITRES);
  return qn + 1 >> 1 << 1;
}
```

### `vector helpers`

```fern
<<vector helpers>>=
const EPSILON: f32 = 1.0e-15f32;

const SQRT_HALF: f32 = 0.70710678f32;

// One Haar step on pairs of values `stride` apart.
function haar1(own w: Work, r: Ref, n0: i32, stride: i32): Work {
  for i in 0..stride {
    for j in 0..n0 >> 1 {
      let a = stride * 2 * j + i;
      let c = stride * (2 * j + 1) + i;
      let tmp1 = SQRT_HALF * wget(w, r, a);
      let tmp2 = SQRT_HALF * wget(w, r, c);
      w = wset(w, r, a, tmp1 + tmp2);
      w = wset(w, r, c, tmp1 - tmp2);
    }
  }
  return w;
}

const ORDERY: i32[] = [1, 0, 3, 0, 2, 1, 7, 0, 4, 3, 6, 1, 5, 2, 15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5];

function order_of(stride: i32, hadamard: boolean, i: i32): i32 {
  if (hadamard) {
    return ORDERY[stride - 2 + i];
  }
  return i;
}

// Puts the values of each of the `stride` blocks next to each other.
function deinterleave_hadamard(own w: Work, r: Ref, n0: i32, stride: i32, hadamard: boolean): Work {
  let src = take(w, r, n0 * stride);
  let tmp = take(w, r, n0 * stride);
  for i in 0..stride {
    let row = order_of(stride, hadamard, i) * n0;
    for j in 0..n0 {
      tmp = tmp.with(row + j, src[j * stride + i]);
    }
  }
  return put(w, r, tmp, n0 * stride);
}

function interleave_hadamard(own w: Work, r: Ref, n0: i32, stride: i32, hadamard: boolean): Work {
  let src = take(w, r, n0 * stride);
  let tmp = take(w, r, n0 * stride);
  for i in 0..stride {
    let row = order_of(stride, hadamard, i) * n0;
    for j in 0..n0 {
      tmp = tmp.with(j * stride + i, src[row + j]);
    }
  }
  return put(w, r, tmp, n0 * stride);
}

// Mixes the side into the mid with the weights of the two band energies.
function intensity_stereo(own w: Work, x: Ref, y: Ref, n: i32): Work {
  let left = w.band_e[w.ctx.i];
  let right = w.band_e[w.ctx.i + modes.BANDS];
  let norm = EPSILON + ((EPSILON + left * left + right * right) as f64).sqrt() as f32;
  let a1 = left / norm;
  let a2 = right / norm;
  for j in 0..n {
    let v = a1 * wget(w, x, j) + a2 * wget(w, y, j);
    w = wset(w, x, j, v);
  }
  return w;
}

function stereo_split(own w: Work, x: Ref, y: Ref, n: i32): Work {
  for j in 0..n {
    let l = SQRT_HALF * wget(w, x, j);
    let r = SQRT_HALF * wget(w, y, j);
    w = wset(w, x, j, l + r);
    w = wset(w, y, j, r - l);
  }
  return w;
}

// Turns the mid in `x` and the side in `y` back into the two channels.
function stereo_merge(own w: Work, x: Ref, y: Ref, mid: f32, n: i32): Work {
  let xp: f32 = 0.0f32;
  let side: f32 = 0.0f32;
  for j in 0..n {
    let yj = wget(w, y, j);
    xp = xp + yj * wget(w, x, j);
    side = side + yj * yj;
  }
  xp = mid * xp;
  let el = mid * mid + side - 2.0f32 * xp;
  let er = mid * mid + side + 2.0f32 * xp;
  if (er < 6.0e-4f32 || el < 6.0e-4f32) {
    return copy(w, y, x, n);
  }
  let lgain: f32 = 1.0f32 / ((el as f64).sqrt() as f32);
  let rgain: f32 = 1.0f32 / ((er as f64).sqrt() as f32);
  for j in 0..n {
    let l = mid * wget(w, x, j);
    let r = wget(w, y, j);
    w = wset(w, x, j, lgain * (l - r));
    w = wset(w, y, j, rgain * (l + r));
  }
  return w;
}

function negate(own w: Work, r: Ref, n: i32): Work {
  for j in 0..n {
    let v = -wget(w, r, j);
    w = wset(w, r, j, v);
  }
  return w;
}

function renormalise(own w: Work, r: Ref, n: i32, gain: f32): Work {
  let v = vq.renormalise_vector(take(w, r, n), n, gain);
  return put(w, r, v, n);
}
```

### `theta`

```fern
<<theta>>=
struct Split { inv: boolean, imid: i32, iside: i32, delta: i32, itheta: i32, qalloc: i32, b: i32, fill: i32 }

// A split of a transient band that the bits cannot pay for codes all the
// energy in one half, so that the other half does not fill with noise.
function avoid_split_noise(itheta: i32, qn: i32, n: i32, b: i32): i32 {
  let unquantized = itheta * 16384 / qn;
  let imid = bitexact_cos(unquantized);
  let iside = bitexact_cos(16384 - unquantized);
  let delta = frac_mul16(n - 1 << 7, bitexact_log2tan(iside, imid));
  if (delta > b) {
    return qn;
  }
  if (delta < -b) {
    return 0;
  }
  return itheta;
}

// The angle that the encoder codes, in `qn` steps.
function quantise_theta(w: Work, itheta0: i32, qn: i32, n: i32, b: i32, stereo: boolean): i32 {
  if (!stereo || w.ctx.theta_round == 0) {
    let itheta = itheta0 * qn + 8192 >> 14;
    if (!stereo && w.ctx.avoid_split_noise && itheta > 0 && itheta < qn) {
      return avoid_split_noise(itheta, qn, n, b);
    }
    return itheta;
  }
  let bias = -32767 / qn;
  if (itheta0 > 8192) {
    bias = 32767 / qn;
  }
  let down = min(qn - 1, max(0, itheta0 * qn + bias >> 14));
  if (w.ctx.theta_round < 0) {
    return down;
  }
  return down + 1;
}

function stepped_range(x: i32, x0: i32, p0: i32): (i32, i32) {
  if (x <= x0) {
    return (p0 * x, p0 * (x + 1));
  }
  return (x - 1 - x0 + (x0 + 1) * p0, x - x0 + (x0 + 1) * p0);
}

// Codes the angle with a step distribution for a stereo band of more than two
// values: the steps up to the middle have three times the probability.
function code_theta_stepped(own w: Work, itheta: i32, qn: i32): (Work, i32) {
  let p0 = 3;
  let x0 = qn / 2;
  let ft = p0 * (x0 + 1) + x0;
  let Work { c, b, ctx, band_e } = w;
  match (c) {
    range.Encoding(e) => {
      let (fl, fh) = stepped_range(itheta, x0, p0);
      return (Work { c: range.Encoding(e.encode(fl, fh, ft)), b: b, ctx: ctx, band_e: band_e }, itheta);
    },
    range.Decoding(d) => {
      let (d1, fs) = d.decode(ft);
      let x = x0 + 1 + (fs - (x0 + 1) * p0);
      if (fs < (x0 + 1) * p0) {
        x = fs / p0;
      }
      let (fl, fh) = stepped_range(x, x0, p0);
      return (Work { c: range.Decoding(d1.update(fl, fh, ft)), b: b, ctx: ctx, band_e: band_e }, x);
    }
  }
}

function triangular_range(itheta: i32, qn: i32, ft: i32): (i32, i32) {
  if (itheta <= qn >> 1) {
    return (itheta * (itheta + 1) >> 1, itheta + 1);
  }
  return (ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1), qn + 1 - itheta);
}

// Codes the angle with a triangular distribution for a mono band of one block.
function code_theta_triangular(own w: Work, itheta: i32, qn: i32): (Work, i32) {
  let half = qn >> 1;
  let ft: i32 = (half + 1) * (half + 1);
  let Work { c, b, ctx, band_e } = w;
  match (c) {
    range.Encoding(e) => {
      let (fl, fs) = triangular_range(itheta, qn, ft);
      return (Work { c: range.Encoding(e.encode(fl, fl + fs, ft)), b: b, ctx: ctx, band_e: band_e }, itheta);
    },
    range.Decoding(d) => {
      let (d1, fm) = d.decode(ft);
      let it = 2 * (qn + 1) - isqrt32((8 * (ft - fm - 1) + 1) as u32) as i32 >> 1;
      if (fm < half * (half + 1) >> 1) {
        it = isqrt32((8 * fm + 1) as u32) as i32 - 1 >> 1;
      }
      let (fl, fs) = triangular_range(it, qn, ft);
      return (Work { c: range.Decoding(d1.update(fl, fl + fs, ft)), b: b, ctx: ctx, band_e: band_e }, it);
    }
  }
}

// Codes the angle in `qn` steps and gives it in steps of 1 / 16384.
function code_theta(own w: Work, itheta0: i32, qn: i32, n: i32, b0: i32, big_b: i32, stereo: boolean): (Work, i32) {
  let itheta = itheta0;
  if (w.ctx.encode) {
    itheta = quantise_theta(w, itheta0, qn, n, b0, stereo);
  }
  if (stereo && n > 2) {
    let (w1, v) = code_theta_stepped(w, itheta, qn);
    return (w1, v * 16384 / qn);
  }
  if (big_b > 1 || stereo) {
    let (w1, v) = code_uint(w, itheta as u32, (qn + 1) as u32);
    return (w1, v as i32 * 16384 / qn);
  }
  let (w1, v) = code_theta_triangular(w, itheta, qn);
  return (w1, v * 16384 / qn);
}

// The stereo band with one angle step: the encoder mixes the side into the mid,
// and a bit can code an inversion of the side.
function code_inversion(own w: Work, x: Ref, y: Ref, n: i32, b: i32, itheta0: i32): (Work, boolean) {
  let inv = false;
  if (w.ctx.encode) {
    inv = itheta0 > 8192 && !w.ctx.disable_inv;
    if (inv) {
      w = negate(w, y, n);
    }
    w = intensity_stereo(w, x, y, n);
  }
  if (b > 2 << rate.BITRES && w.ctx.remaining_bits > 2 << rate.BITRES) {
    let (w1, v) = code_bit(w, inv, 2);
    w = w1;
    inv = v;
  } else {
    inv = false;
  }
  let keep = inv && !w.ctx.disable_inv;
  return (w, keep);
}

// The bits that the mid and the side of a split get, from the angle.
function split_of(itheta: i32, n: i32, b: i32, big_b: i32, fill0: i32, inv: boolean, qalloc: i32): Split {
  if (itheta == 0) {
    return Split { inv: inv, imid: 32767, iside: 0, delta: -16384, itheta: itheta, qalloc: qalloc, b: b, fill: fill0 & (1 << big_b) - 1 };
  }
  if (itheta == 16384) {
    return Split { inv: inv, imid: 0, iside: 32767, delta: 16384, itheta: itheta, qalloc: qalloc, b: b, fill: fill0 & (1 << big_b) - 1 << big_b };
  }
  let imid = bitexact_cos(itheta);
  let iside = bitexact_cos(16384 - itheta);
  let delta = frac_mul16(n - 1 << 7, bitexact_log2tan(iside, imid));
  return Split { inv: inv, imid: imid, iside: iside, delta: delta, itheta: itheta, qalloc: qalloc, b: b, fill: fill0 };
}

function theta_steps(w: Work, n: i32, b0: i32, lm: i32, stereo: boolean): i32 {
  let i = w.ctx.i;
  let pulse_cap = modes.LOGN[i] + lm * (1 << rate.BITRES);
  let off = 4;
  if (stereo && n == 2) {
    off = 16;
  }
  if (stereo && i >= w.ctx.intensity) {
    return 1;
  }
  return compute_qn(n, b0, (pulse_cap >> 1) - off, pulse_cap, stereo);
}

// Finds and codes the angle of a split of `x` and `y`, and gives the bits that
// are left for the two halves.
function compute_theta(own w: Work, x: Ref, y: Ref, n: i32, b0: i32, big_b: i32, b0_blocks: i32, lm: i32, stereo: boolean, fill: i32): (Work, Split) {
  let qn = theta_steps(w, n, b0, lm, stereo);
  let itheta = 0;
  if (w.ctx.encode) {
    itheta = if (w.ctx.legacy_encoder) { vq.stereo_itheta_encoder(take(w, x, n), take(w, y, n), stereo, n) } else { vq.stereo_itheta(take(w, x, n), take(w, y, n), stereo, n) };
  }
  let tell = w.c.tell_frac();
  let inv = false;
  if (qn != 1) {
    let (w1, t) = code_theta(w, itheta, qn, n, b0, b0_blocks, stereo);
    w = w1;
    itheta = t;
    if (w.ctx.encode && stereo && itheta == 0) {
      w = intensity_stereo(w, x, y, n);
    } else if (w.ctx.encode && stereo) {
      w = stereo_split(w, x, y, n);
    }
  } else if (stereo) {
    let (w1, v) = code_inversion(w, x, y, n, b0, itheta);
    w = w1;
    inv = v;
    itheta = 0;
  }
  let qalloc = w.c.tell_frac() - tell;
  return (w, split_of(itheta, n, b0 - qalloc, big_b, fill, inv, qalloc));
}
```

### `partition`

```fern
<<partition>>=
// The sign of a band of one value.
function code_sign(own w: Work, r: Ref): (Work, boolean) {
  if (w.ctx.remaining_bits < 1 << rate.BITRES) {
    return (w, false);
  }
  let want: u32 = 0u32;
  if (w.ctx.encode && wget(w, r, 0) < 0.0f32) {
    want = 1u32;
  }
  let (w1, v) = code_raw_bit(w, want);
  return (spend(w1, 1 << rate.BITRES), v == 1u32);
}

function code_n1(own w: Work, r: Ref): Work {
  let (w1, negative) = code_sign(w, r);
  if (!w1.ctx.resynth) {
    return w1;
  }
  let v = 1.0f32;
  if (negative) {
    v = -1.0f32;
  }
  return wset(w1, r, 0, v);
}

// A band of one value codes only its sign.
function quant_band_n1(own w: Work, x: Ref, y: Ref, lowband_out: Ref): (Work, i32) {
  w = code_n1(w, x);
  if (!is_none(y)) {
    w = code_n1(w, y);
  }
  if (!is_none(lowband_out)) {
    let v = wget(w, x, 0);
    w = wset(w, lowband_out, 0, v);
  }
  return (w, 1);
}

// A codeword of no pulses: noise, or the lowband with some noise.
function fill_band(own w: Work, x: Ref, n: i32, big_b: i32, lowband: Ref, gain: f32, fill0: i32): (Work, i32) {
  if (!w.ctx.resynth) {
    return (w, 0);
  }
  let cm_mask: i32 = (1 << big_b) - 1;
  let fill = fill0 & cm_mask;
  if (fill == 0) {
    for j in 0..n {
      w = wset(w, x, j, 0.0f32);
    }
    return (w, 0);
  }
  let seed = w.ctx.seed;
  for j in 0..n {
    seed = lcg_rand(seed);
    let v = (seed as i32 >> 20) as f32;
    if (!is_none(lowband)) {
      let tmp: f32 = -0.00390625f32;
      if ((seed & 32768u32) != 0u32) {
        tmp = 0.00390625f32;
      }
      v = wget(w, lowband, j) + tmp;
    }
    w = wset(w, x, j, v);
  }
  let cm = fill;
  if (is_none(lowband)) {
    cm = cm_mask;
  }
  let ctx = Ctx { ...w.ctx, seed: seed };
  return (renormalise(with_ctx(w, ctx), x, n, gain), cm);
}

// The pulses that fit in `b` bits and in the bits that are left.
function pulses_for(own w: Work, lm: i32, b: i32): (Work, i32) {
  let i = w.ctx.i;
  let q = rate.bits2pulses(i, lm, b);
  let curr = rate.pulses2bits(i, lm, q);
  let left = w.ctx.remaining_bits - curr;
  while (left < 0 && q > 0) {
    left += curr;
    q -= 1;
    curr = rate.pulses2bits(i, lm, q);
    left -= curr;
  }
  let ctx = Ctx { ...w.ctx, remaining_bits: left };
  return (with_ctx(w, ctx), q);
}

// Codes the shape of `x` with `k` pulses.
function code_shape(own w: Work, x: Ref, n: i32, k: i32, big_b: i32, gain: f32): (Work, i32) {
  let v = take(w, x, n);
  let Work { c, b, ctx, band_e } = w;
  match (c) {
    range.Encoding(e) => {
      let q = if (ctx.legacy_encoder) { vq.alg_quant_encoder(e, v, n, k, ctx.spread, big_b, gain, ctx.resynth) } else { vq.alg_quant(e, v, n, k, ctx.spread, big_b, gain, ctx.resynth) };
      let vq.Quant { e: e1, x: xs, mask } = q;
      return (put(Work { c: range.Encoding(e1), b: b, ctx: ctx, band_e: band_e }, x, xs, n), mask);
    },
    range.Decoding(d) => {
      let u = vq.alg_unquant(d, n, k, ctx.spread, big_b, gain);
      let vq.Unquant { d: d1, x: xs, mask } = u;
      return (put(Work { c: range.Decoding(d1), b: b, ctx: ctx, band_e: band_e }, x, xs, n), mask);
    }
  }
}

// Codes a band, or a part of a band, either as one codeword or as two halves.
function quant_partition(own w: Work, x: Ref, n: i32, b: i32, big_b: i32, lowband: Ref, lm: i32, gain: f32, fill: i32): (Work, i32) {
  let at = modes.CACHE_INDEX[(lm + 1) * modes.BANDS + w.ctx.i];
  if (lm != -1 && b > modes.CACHE_BITS[at + modes.CACHE_BITS[at]] + 12 && n > 2) {
    return split_partition(w, x, n, b, big_b, lowband, lm, gain, fill);
  }
  let (w1, q) = pulses_for(w, lm, b);
  if (q != 0) {
    return code_shape(w1, x, n, rate.get_pulses(q), big_b, gain);
  }
  return fill_band(w1, x, n, big_b, lowband, gain, fill);
}

// The bits of the mid and the side of a split, with the bias that a transient
// band gives the half that holds the transient.
function split_bits(s: Split, n: i32, big_b0: i32, lm: i32): (i32, i32) {
  let delta = s.delta;
  if (big_b0 > 1 && (s.itheta & 16383) != 0) {
    if (s.itheta > 8192) {
      delta -= delta >> 4 - lm;
    } else {
      delta = min(0, delta + (n << rate.BITRES >> 5 - lm));
    }
  }
  let mbits = max(0, min(s.b, (s.b - delta) / 2));
  return (mbits, s.b - mbits);
}

// The bits that the first half did not use, given to the second half above a
// margin of 3 bits.
function rebalanced(bits: i32, used: i32, given: i32, keep: boolean): i32 {
  let rebalance = given - used;
  if (rebalance > 3 << rate.BITRES && keep) {
    return bits + rebalance - (3 << rate.BITRES);
  }
  return bits;
}

function split_partition(own w: Work, x: Ref, n0: i32, b0: i32, big_b0: i32, lowband: Ref, lm0: i32, gain: f32, fill0: i32): (Work, i32) {
  let n = n0 >> 1;
  let y = plus(x, n);
  let lm = lm0 - 1;
  let fill = fill0;
  if (big_b0 == 1) {
    fill = fill & 1 | fill << 1;
  }
  let big_b = big_b0 + 1 >> 1;
  let (w1, s) = compute_theta(w, x, y, n, b0, big_b, big_b0, lm, false, fill);
  let mid: f32 = 1.0f32 / 32768.0f32 * (s.imid as f32);
  let side: f32 = 1.0f32 / 32768.0f32 * (s.iside as f32);
  let (mbits, sbits) = split_bits(s, n, big_b0, lm);
  let w2 = spend(w1, s.qalloc);
  let next_lowband2 = none();
  if (!is_none(lowband)) {
    next_lowband2 = plus(lowband, n);
  }
  let before = w2.ctx.remaining_bits;
  let shift = big_b0 >> 1;
  if (mbits >= sbits) {
    let (w3, cm1) = quant_partition(w2, x, n, mbits, big_b, lowband, lm, gain * mid, s.fill);
    let sb = rebalanced(sbits, before - w3.ctx.remaining_bits, mbits, s.itheta != 0);
    let (w4, cm2) = quant_partition(w3, y, n, sb, big_b, next_lowband2, lm, gain * side, s.fill >> big_b);
    return (w4, cm1 | cm2 << shift);
  }
  let (w3, cm2) = quant_partition(w2, y, n, sbits, big_b, next_lowband2, lm, gain * side, s.fill >> big_b);
  let mb = rebalanced(mbits, before - w3.ctx.remaining_bits, sbits, s.itheta != 16384);
  let (w4, cm1) = quant_partition(w3, x, n, mb, big_b, lowband, lm, gain * mid, s.fill);
  return (w4, cm1 | cm2 << shift);
}
```

### `band`

`tf_change` above 0 recombines short blocks into fewer, longer ones. Below 0, it divides a long block in time. Both use Haar steps, and the lowband goes through the same steps so that it can fold into the band.

```fern
<<band>>=
const BIT_INTERLEAVE: i32[] = [0, 1, 1, 1, 2, 3, 3, 3, 2, 3, 3, 3, 2, 3, 3, 3];

const BIT_DEINTERLEAVE: i32[] = [0, 3, 12, 15, 48, 51, 60, 63, 192, 195, 204, 207, 240, 243, 252, 255];

struct Steps { big_b: i32, n_b: i32, recombine: i32, time_divide: i32 }

struct Shape { w: Work, lowband: Ref, fill: i32, steps: Steps }

function haar_both(own w: Work, x: Ref, lowband: Ref, n0: i32, stride: i32): Work {
  if (w.ctx.encode) {
    w = haar1(w, x, n0, stride);
  }
  if (!is_none(lowband)) {
    w = haar1(w, lowband, n0, stride);
  }
  return w;
}

// The lowband in the scratch array when the steps would change it.
function scratch_lowband(own w: Work, n: i32, n_b: i32, big_b0: i32, lowband: Ref, scratch: Ref): (Work, Ref) {
  let tf = w.ctx.tf_change;
  if (!is_none(scratch) && !is_none(lowband) && (tf > 0 || (n_b & 1) == 0 && tf < 0 || big_b0 > 1)) {
    return (copy(w, scratch, lowband, n), scratch);
  }
  return (w, lowband);
}

// Changes the time and frequency resolution of the band and of its lowband
// before the partition.
function to_partition(own w: Work, x: Ref, n: i32, big_b0: i32, lowband0: Ref, scratch: Ref, fill0: i32): Shape {
  let tf_change = w.ctx.tf_change;
  let n_b = n / big_b0;
  let (w0, lowband) = scratch_lowband(w, n, n_b, big_b0, lowband0, scratch);
  w = w0;
  let recombine = max(tf_change, 0);
  let fill = fill0;
  for k in 0..recombine {
    w = haar_both(w, x, lowband, n >> k, 1 << k);
    fill = BIT_INTERLEAVE[fill & 15] | BIT_INTERLEAVE[fill >> 4] << 2;
  }
  let big_b = big_b0 >> recombine;
  n_b = n_b << recombine;
  let time_divide = 0;
  let tf = tf_change;
  while ((n_b & 1) == 0 && tf < 0) {
    w = haar_both(w, x, lowband, n_b, big_b);
    fill = fill | fill << big_b;
    big_b = big_b << 1;
    n_b = n_b >> 1;
    time_divide += 1;
    tf += 1;
  }
  if (big_b > 1 && w.ctx.encode) {
    w = deinterleave_hadamard(w, x, n_b >> recombine, big_b << recombine, big_b0 == 1);
  }
  if (big_b > 1 && !is_none(lowband)) {
    w = deinterleave_hadamard(w, lowband, n_b >> recombine, big_b << recombine, big_b0 == 1);
  }
  return Shape { w: w, lowband: lowband, fill: fill, steps: Steps { big_b: big_b, n_b: n_b, recombine: recombine, time_divide: time_divide } };
}

// Undoes the steps of to_partition on the decoded band, and gives the band
// to the folding memory.
function from_partition(own w: Work, x: Ref, n0: i32, s: Steps, big_b0: i32, cm0: i32, lowband_out: Ref): (Work, i32) {
  let cm = cm0;
  if (s.big_b > 1) {
    w = interleave_hadamard(w, x, s.n_b >> s.recombine, s.big_b << s.recombine, big_b0 == 1);
  }
  let n_b = s.n_b;
  let big_b = s.big_b;
  for k in 0..s.time_divide {
    big_b = big_b >> 1;
    n_b = n_b << 1;
    cm = cm | cm >> big_b;
    w = haar1(w, x, n_b, big_b);
  }
  for k in 0..s.recombine {
    cm = BIT_DEINTERLEAVE[cm];
    w = haar1(w, x, n0 >> k, 1 << k);
  }
  big_b = big_b << s.recombine;
  if (!is_none(lowband_out)) {
    let scale = (n0 as f64).sqrt() as f32;
    for j in 0..n0 {
      let v = scale * wget(w, x, j);
      w = wset(w, lowband_out, j, v);
    }
  }
  return (w, cm & (1 << big_b) - 1);
}

// Codes one band of one channel.
function quant_band(own w: Work, x: Ref, n: i32, b: i32, big_b: i32, lowband: Ref, lm: i32, lowband_out: Ref, gain: f32, scratch: Ref, fill: i32): (Work, i32) {
  if (n == 1) {
    return quant_band_n1(w, x, none(), lowband_out);
  }
  let Shape { w: w1, lowband: low, fill: f, steps: st } = to_partition(w, x, n, big_b, lowband, scratch, fill);
  let (w2, cm) = quant_partition(w1, x, n, b, st.big_b, low, lm, gain, f);
  if (!w2.ctx.resynth) {
    return (w2, cm);
  }
  return from_partition(w2, x, n, st, big_b, cm, lowband_out);
}
```

### `stereo band`

```fern
<<stereo band>>=
// The sign of the side of a stereo band of two values.
function code_side_sign(own w: Work, x2: Ref, y2: Ref, sbits: i32): (Work, i32) {
  if (sbits == 0) {
    return (w, 1);
  }
  let want: u32 = 0u32;
  if (w.ctx.encode && wget(w, x2, 0) * wget(w, y2, 1) - wget(w, x2, 1) * wget(w, y2, 0) < 0.0f32) {
    want = 1u32;
  }
  let (w1, v) = code_raw_bit(w, want);
  return (w1, 1 - 2 * (v as i32));
}

// The mid and the side of a stereo band of two values back into the channels.
function merge2(own w: Work, x: Ref, y: Ref, mid: f32, side: f32): Work {
  for j in 0..2 {
    let l = mid * wget(w, x, j);
    let r = side * wget(w, y, j);
    w = wset(w, x, j, l - r);
    w = wset(w, y, j, l + r);
  }
  return w;
}

// A stereo band of two values: one channel takes the codeword and the other
// is at a right angle to it, with a sign bit.
function quant_band_stereo2(own w: Work, x: Ref, y: Ref, s: Split, big_b: i32, lowband: Ref, lm: i32, lowband_out: Ref, scratch: Ref, orig_fill: i32, mid: f32, side: f32): (Work, i32) {
  let sbits = 0;
  if (s.itheta != 0 && s.itheta != 16384) {
    sbits = 1 << rate.BITRES;
  }
  let x2 = x;
  let y2 = y;
  if (s.itheta > 8192) {
    x2 = y;
    y2 = x;
  }
  let (w1, sign) = code_side_sign(spend(w, s.qalloc + sbits), x2, y2, sbits);
  let (w2, cm) = quant_band(w1, x2, 2, s.b - sbits, big_b, lowband, lm, lowband_out, 1.0f32, scratch, orig_fill);
  let a = (-sign) as f32 * wget(w2, x2, 1);
  let c = sign as f32 * wget(w2, x2, 0);
  let w3 = wset(wset(w2, y2, 0, a), y2, 1, c);
  if (!w3.ctx.resynth) {
    return (w3, cm);
  }
  return (merge2(w3, x, y, mid, side), cm);
}

// Codes the mid and the side of a stereo band, the half with more bits first.
function quant_band_ms(own w: Work, x: Ref, y: Ref, n: i32, s: Split, big_b: i32, lowband: Ref, lm: i32, lowband_out: Ref, scratch: Ref, side: f32): (Work, i32) {
  let mbits = max(0, min(s.b, (s.b - s.delta) / 2));
  let sbits = s.b - mbits;
  let w1 = spend(w, s.qalloc);
  let before = w1.ctx.remaining_bits;
  if (mbits >= sbits) {
    let (w2, cm1) = quant_band(w1, x, n, mbits, big_b, lowband, lm, lowband_out, 1.0f32, scratch, s.fill);
    let sb = rebalanced(sbits, before - w2.ctx.remaining_bits, mbits, s.itheta != 0);
    let (w3, cm2) = quant_band(w2, y, n, sb, big_b, none(), lm, none(), side, none(), s.fill >> big_b);
    return (w3, cm1 | cm2);
  }
  let (w2, cm2) = quant_band(w1, y, n, sbits, big_b, none(), lm, none(), side, none(), s.fill >> big_b);
  let mb = rebalanced(mbits, before - w2.ctx.remaining_bits, sbits, s.itheta != 16384);
  let (w3, cm1) = quant_band(w2, x, n, mb, big_b, lowband, lm, lowband_out, 1.0f32, scratch, s.fill);
  return (w3, cm1 | cm2);
}

function finish_stereo(own w: Work, x: Ref, y: Ref, n: i32, mid: f32, inv: boolean): Work {
  if (!w.ctx.resynth) {
    return w;
  }
  if (n != 2) {
    w = stereo_merge(w, x, y, mid, n);
  }
  if (inv) {
    w = negate(w, y, n);
  }
  return w;
}

// Codes one band of a stereo frame as its mid and side.
function quant_band_stereo(own w: Work, x: Ref, y: Ref, n: i32, b: i32, big_b: i32, lowband: Ref, lm: i32, lowband_out: Ref, scratch: Ref, fill: i32): (Work, i32) {
  if (n == 1) {
    return quant_band_n1(w, x, y, lowband_out);
  }
  let (w1, s) = compute_theta(w, x, y, n, b, big_b, big_b, lm, true, fill);
  let mid = 1.0f32 / 32768.0f32 * (s.imid as f32);
  let side = 1.0f32 / 32768.0f32 * (s.iside as f32);
  if (n == 2) {
    let (w2, cm) = quant_band_stereo2(w1, x, y, s, big_b, lowband, lm, lowband_out, scratch, fill, mid, side);
    return (finish_stereo(w2, x, y, n, mid, s.inv), cm);
  }
  let (w2, cm) = quant_band_ms(w1, x, y, n, s, big_b, lowband, lm, lowband_out, scratch, side);
  return (finish_stereo(w2, x, y, n, mid, s.inv), cm);
}
```

### `all bands`

```fern
<<all bands>>=
pub struct Request { start: i32, end: i32, short_blocks: boolean, spread: i32, dual_stereo: boolean, intensity: i32, tf_res: i32[], total_bits: i32, balance: i32, lm: i32, coded_bands: i32, pulses: i32[], seed: u32, complexity: i32, disable_inv: boolean, channels: i32 }

pub struct Result { c: range.Coder, x: f32[], y: f32[], collapse_masks: i32[], seed: u32 }

struct Fold { lowband: i32, x_cm: i32, y_cm: i32 }

// The collapse masks of the bands from `fold_start` up to `fold_end`.
function fold_masks(masks: [i32], fold_start: i32, fold_end: i32, channels: i32, lowband: i32): Fold {
  let x_cm = 0;
  let y_cm = 0;
  let k = fold_start;
  while (true) {
    x_cm = x_cm | masks[k * channels];
    y_cm = y_cm | masks[k * channels + channels - 1];
    k += 1;
    if (k >= fold_end) {
      break;
    }
  }
  return Fold { lowband: lowband, x_cm: x_cm, y_cm: y_cm };
}

// The lowband of band `i` in the folding memory, and the collapse masks of the
// bands that it covers.
function fold_of(masks: [i32], lowband_offset: i32, i: i32, m: i32, n: i32, norm_offset: i32, channels: i32, big_b: i32, use_fold: boolean): Fold {
  if (lowband_offset == 0 || !use_fold) {
    return Fold { lowband: -1, x_cm: (1 << big_b) - 1, y_cm: (1 << big_b) - 1 };
  }
  let effective = max(0, m * modes.EBANDS[lowband_offset] - norm_offset - n);
  let fold_start = lowband_offset - 1;
  while (m * modes.EBANDS[fold_start] > effective + norm_offset) {
    fold_start -= 1;
  }
  let fold_end = lowband_offset;
  while (fold_end < i && m * modes.EBANDS[fold_end] < effective + norm_offset + n) {
    fold_end += 1;
  }
  return fold_masks(masks, fold_start, fold_end, channels, effective);
}

// Copies the folding memory of the first band of a hybrid frame up into the
// second band, which is wider.
function special_hybrid_folding(own w: Work, start: i32, m: i32, dual_stereo: boolean, norm2: i32): Work {
  let n1 = m * (modes.EBANDS[start + 1] - modes.EBANDS[start]);
  let n2 = m * (modes.EBANDS[start + 2] - modes.EBANDS[start + 1]);
  w = copy(w, Ref { buf: BUF_NORM, at: n1 }, Ref { buf: BUF_NORM, at: 2 * n1 - n2 }, n2 - n1);
  if (dual_stereo) {
    w = copy(w, Ref { buf: BUF_NORM, at: norm2 + n1 }, Ref { buf: BUF_NORM, at: norm2 + 2 * n1 - n2 }, n2 - n1);
  }
  return w;
}

// Mixes the two folding memories into one, when dual stereo stops.
function mix_norms(own w: Work, n: i32, norm2: i32): Work {
  let norm = Ref { buf: BUF_NORM, at: 0 };
  for j in 0..n {
    let v: f32 = 0.5f32 * (wget(w, norm, j) + wget(w, norm, norm2 + j));
    w = wset(w, norm, j, v);
  }
  return w;
}

function zeros(n: i32): f32[] {
  let out: f32[] = [];
  for j in 0..n {
    out = out.append(0.0f32);
  }
  return out;
}

struct Frame { r: Request, m: i32, big_b: i32, norm_offset: i32, norm_len: i32, theta_rdo: boolean, resynth: boolean }

function frame_of(c: range.Coder, r: Request): Frame {
  let m = 1 << r.lm;
  let big_b = 1;
  if (r.short_blocks) {
    big_b = m;
  }
  let norm_offset = m * modes.EBANDS[r.start];
  let theta_rdo = c.encoding() && r.channels == 2 && !r.dual_stereo && r.complexity >= 8;
  return Frame { r: r, m: m, big_b: big_b, norm_offset: norm_offset, norm_len: m * modes.EBANDS[modes.BANDS - 1] - norm_offset, theta_rdo: theta_rdo, resynth: !c.encoding() || theta_rdo };
}

// The bits of band `i`: its share of the allocation, with a part of the
// balance that the bands before it left.
function band_bits(r: Request, i: i32, balance: i32, remaining: i32): i32 {
  if (i > r.coded_bands - 1) {
    return 0;
  }
  let curr_balance = balance / min(3, r.coded_bands - i);
  return max(0, min(16383, min(remaining + 1, r.pulses[i] + curr_balance)));
}

struct Loop { w: Work, masks: i32[], balance: i32, lowband_offset: i32, update_lowband: boolean, dual_stereo: boolean }

pub function quant_all_bands(own c: range.Coder, r: Request, own x: f32[], own y: f32[], band_e: [f32]): Result {
  return quant_reference(c, r, x, y, band_e, false);
}

pub function quant_all_bands_encoder(own c: range.Coder, r: Request, own x: f32[], own y: f32[], band_e: [f32]): Result {
  return quant_reference(c, r, x, y, band_e, true);
}

// Codes the normalised spectrum of the bands from `start` to `end`.
function quant_reference(own c: range.Coder, r: Request, own x: f32[], own y: f32[], band_e: [f32], legacy_encoder: boolean): Result {
  let fr = frame_of(c, r);
  let tail = fr.m * (modes.EBANDS[modes.BANDS] - modes.EBANDS[modes.BANDS - 1]);
  let band_e_copy: f32[] = [];
  for v in band_e {
    band_e_copy = band_e_copy.append(v);
  }
  let ctx = Ctx { encode: c.encoding(), legacy_encoder: legacy_encoder, resynth: fr.resynth, i: 0, intensity: r.intensity, spread: r.spread, tf_change: 0, remaining_bits: 0, seed: r.seed, theta_round: 0, disable_inv: r.disable_inv, avoid_split_noise: fr.big_b > 1 };
  let masks: i32[] = [];
  for i in 0..r.channels * modes.BANDS {
    masks = masks.append(0);
  }
  let st = Loop { w: Work { c: c, b: Bands { x: x, y: y, norm: zeros(2 * fr.norm_len), scratch: zeros(tail) }, ctx: ctx, band_e: band_e_copy }, masks: masks, balance: r.balance, lowband_offset: 0, update_lowband: true, dual_stereo: r.dual_stereo };
  for i in r.start..r.end {
    st = band_step(st, fr, i);
  }
  let Loop { w, masks: masks2, balance: _, lowband_offset: _, update_lowband: _, dual_stereo: _ } = st;
  let Work { c: c2, b: out, ctx: ctx2, band_e: _ } = w;
  let Bands { x: x2, y: y2, norm: _, scratch: _ } = out;
  return Result { c: c2, x: x2, y: y2, collapse_masks: masks2, seed: ctx2.seed };
}

// Whether band `i` starts a new region of the folding memory for the bands
// after it.
function moves_lowband(fr: Frame, i: i32, n: i32, update_lowband: boolean, lowband_offset: i32): boolean {
  let m = fr.m;
  return fr.resynth && (m * modes.EBANDS[i] - n >= m * modes.EBANDS[fr.r.start] || i == fr.r.start + 1) && (update_lowband || lowband_offset == 0);
}

// The first band keeps the balance of the allocation as it is.
function tell_unless_first(tell: i32, first: boolean): i32 {
  if (first) {
    return 0;
  }
  return tell;
}

function scratch_of(skip: boolean): Ref {
  if (skip) {
    return none();
  }
  return Ref { buf: BUF_SCRATCH, at: 0 };
}

// Codes band `i` of each channel.
function band_step(own st: Loop, fr: Frame, i: i32): Loop {
  let r = fr.r;
  let Loop { w, masks, balance, lowband_offset, update_lowband, dual_stereo } = st;
  let m = fr.m;
  let last = i == r.end - 1;
  let n = m * modes.EBANDS[i + 1] - m * modes.EBANDS[i];
  let tell = w.c.tell_frac();
  balance -= tell_unless_first(tell, i == r.start);
  let remaining = r.total_bits - tell - 1;
  let b = band_bits(r, i, balance, remaining);
  let band_ctx = Ctx { ...w.ctx, i: i, remaining_bits: remaining, tf_change: r.tf_res[i] };
  w = with_ctx(w, band_ctx);
  if (moves_lowband(fr, i, n, update_lowband, lowband_offset)) {
    lowband_offset = i;
  }
  if (i == r.start + 1) {
    w = special_hybrid_folding(w, r.start, m, dual_stereo, fr.norm_len);
  }
  let scratch = scratch_of(last && !fr.theta_rdo);
  let f = fold_of(masks, lowband_offset, i, m, n, fr.norm_offset, r.channels, fr.big_b, r.spread != vq.SPREAD_AGGRESSIVE || fr.big_b > 1 || r.tf_res[i] < 0);
  let ends_dual = dual_stereo && i == r.intensity;
  if (ends_dual && fr.resynth) {
    w = mix_norms(w, m * modes.EBANDS[i] - fr.norm_offset, fr.norm_len);
  }
  dual_stereo = dual_stereo && !ends_dual;
  let at = m * modes.EBANDS[i];
  let place = Place { x: Ref { buf: BUF_X, at: at }, y: Ref { buf: BUF_Y, at: at }, n: n, b: b, f: f, last: last, out_at: at - fr.norm_offset, scratch: scratch };
  let (w2, cms) = code_band(w, fr, place, dual_stereo);
  masks = masks.with(i * r.channels, cms.x_cm & 255);
  masks = masks.with(i * r.channels + r.channels - 1, cms.y_cm & 255);
  let next_ctx = Ctx { ...w2.ctx, avoid_split_noise: false };
  let w3 = with_ctx(w2, next_ctx);
  return Loop { w: w3, masks: masks, balance: balance + r.pulses[i] + tell, lowband_offset: lowband_offset, update_lowband: b > n << rate.BITRES, dual_stereo: dual_stereo };
}

struct Place { x: Ref, y: Ref, n: i32, b: i32, f: Fold, last: boolean, out_at: i32, scratch: Ref }

function low_ref(f: Fold, base: i32): Ref {
  if (f.lowband < 0) {
    return none();
  }
  return Ref { buf: BUF_NORM, at: base + f.lowband };
}

function out_ref(last: boolean, at: i32): Ref {
  if (last) {
    return none();
  }
  return Ref { buf: BUF_NORM, at: at };
}

// Codes band `i` of each channel: as two mono bands, as one mono band, or as a
// stereo band.
function code_band(own w: Work, fr: Frame, p: Place, dual_stereo: boolean): (Work, Fold) {
  let r = fr.r;
  if (dual_stereo) {
    let (w1, x_cm) = quant_band(w, p.x, p.n, p.b / 2, fr.big_b, low_ref(p.f, 0), r.lm, out_ref(p.last, p.out_at), 1.0f32, p.scratch, p.f.x_cm);
    let (w2, y_cm) = quant_band(w1, p.y, p.n, p.b / 2, fr.big_b, low_ref(p.f, fr.norm_len), r.lm, out_ref(p.last, fr.norm_len + p.out_at), 1.0f32, p.scratch, p.f.y_cm);
    return (w2, Fold { lowband: p.f.lowband, x_cm: x_cm, y_cm: y_cm });
  }
  if (r.channels == 1) {
    let (w1, cm) = quant_band(w, p.x, p.n, p.b, fr.big_b, low_ref(p.f, 0), r.lm, out_ref(p.last, p.out_at), 1.0f32, p.scratch, p.f.x_cm | p.f.y_cm);
    return (w1, Fold { lowband: p.f.lowband, x_cm: cm, y_cm: cm });
  }
  if (fr.theta_rdo && w.ctx.i < r.intensity) {
    return theta_rdo_band(w, fr, p);
  }
  let (w1, cm) = quant_band_stereo(w, p.x, p.y, p.n, p.b, fr.big_b, low_ref(p.f, 0), r.lm, out_ref(p.last, p.out_at), p.scratch, p.f.x_cm | p.f.y_cm);
  return (w1, Fold { lowband: p.f.lowband, x_cm: cm, y_cm: cm });
}

function stereo_pass(w: Work, fr: Frame, p: Place, round: i32): (Work, i32) {
  let ctx = Ctx { ...w.ctx, theta_round: round };
  let start = with_ctx(clone(w), ctx);
  if (round > 0 && w.ctx.i == fr.r.start + 1) {
    start = special_hybrid_folding(start, fr.r.start, fr.m, false, 0);
  }
  return quant_band_stereo(start, p.x, p.y, p.n, p.b, fr.big_b, low_ref(p.f, 0), fr.r.lm, out_ref(p.last, p.out_at), p.scratch, p.f.x_cm | p.f.y_cm);
}

// The encoder codes the stereo band twice, with the angle rounded down and up,
// and keeps the one whose result is nearer to the band.
function theta_rdo_band(own w: Work, fr: Frame, p: Place): (Work, Fold) {
  let i = w.ctx.i;
  let ex = w.band_e[i];
  let ey = w.band_e[i + modes.BANDS];
  let min_e = ex;
  if (ey < ex) {
    min_e = ey;
  }
  let w0 = ex + min_e / 3.0f32;
  let w1 = ey + min_e / 3.0f32;
  let x_save = take(w, p.x, p.n);
  let y_save = take(w, p.y, p.n);
  let (wd, cm_down) = stereo_pass(w, fr, p, -1);
  let dist0 = w0 * dot(x_save, take(wd, p.x, p.n), p.n) + w1 * dot(y_save, take(wd, p.y, p.n), p.n);
  let (wu, cm_up) = stereo_pass(w, fr, p, 1);
  let dist1 = w0 * dot(x_save, take(wu, p.x, p.n), p.n) + w1 * dot(y_save, take(wu, p.y, p.n), p.n);
  if (dist0 >= dist1) {
    return (wd, Fold { lowband: p.f.lowband, x_cm: cm_down, y_cm: cm_down });
  }
  return (wu, Fold { lowband: p.f.lowband, x_cm: cm_up, y_cm: cm_up });
}

function dot(a: [f32], b: [f32], n: i32): f32 {
  let s: f32 = 0.0f32;
  for j in 0..n {
    s = s + a[j] * b[j];
  }
  return s;
}
```

### `analysis`

The encoder runs these functions before it codes the bands. `compute_band_energies` gives the amplitude of each band, and `normalise_bands` scales each band to unit length. `spreading_decision` counts the small values of each band, and selects the spreading of the frame and the tapset of the prefilter. `hysteresis_decision` selects a step from a list of thresholds. It changes the step of the last frame only when the value is clearly past the threshold.

```fern
<<analysis>>=
const ENERGY_FLOOR: f32 = 1.0000000272452012e-27f32;

// The amplitude of each band below `end`, with `modes.BANDS` values for
// each of `channels` channels. The bands from `end` are 0.
pub function compute_band_energies(x: [f32], end: i32, channels: i32, lm: i32): f32[] {
  let n = modes.SHORT_MDCT_SIZE << lm;
  let out: f32[] = [];
  for c in 0..channels {
    for i in 0..modes.BANDS {
      if (i < end) {
        let at = c * n + (modes.EBANDS[i] << lm);
        let sum = ENERGY_FLOOR + pitch.inner_prod(x, at, x, at, modes.EBANDS[i + 1] - modes.EBANDS[i] << lm);
        out = out.append((sum as f64).sqrt() as f32);
      } else {
        out = out.append(0.0f32);
      }
    }
  }
  return out;
}

// The bands below `end` of `freq`, each scaled to unit length by its
// amplitude in `band_e`. The other values are 0.
pub function normalise_bands(freq: [f32], band_e: [f32], end: i32, channels: i32, m: i32): f32[] {
  let n = m * modes.SHORT_MDCT_SIZE;
  let out: f32[] = [];
  for c in 0..channels {
    for j in 0..n {
      out = out.append(0.0f32);
    }
    for i in 0..end {
      let g = 1.0f32 / (ENERGY_FLOOR + band_e[i + c * modes.BANDS]);
      for j in m * modes.EBANDS[i]..m * modes.EBANDS[i + 1] {
        out = out.with(j + c * n, freq[j + c * n] * g);
      }
    }
  }
  return out;
}

// `decision` is the spreading of the last frame on input. `average`,
// `hf_average` and `tapset` are the state that the decision keeps.
pub struct Spread { decision: i32, average: i32, hf_average: i32, tapset: i32 }

struct Counts { quarter: i32, sixteenth: i32, tiny: i32 }

// The number of values of the band whose energy share is below 1/4,
// 1/16 and 1/64 of the mean.
function small_counts(x: [f32], at: i32, n: i32): Counts {
  let t = Counts { quarter: 0, sixteenth: 0, tiny: 0 };
  for j in 0..n {
    let x2n = x[at + j] * x[at + j] * (n as f32);
    if (x2n < 0.25f32) {
      t = Counts { ...t, quarter: t.quarter + 1 };
    }
    if (x2n < 0.0625f32) {
      t = Counts { ...t, sixteenth: t.sixteenth + 1 };
    }
    if (x2n < 0.015625f32) {
      t = Counts { ...t, tiny: t.tiny + 1 };
    }
  }
  return t;
}

function half_or_more(count: i32, n: i32): i32 {
  if (2 * count >= n) {
    return 1;
  }
  return 0;
}

// The tapset of the prefilter, from the share of small values in the
// highest bands. The tapset of the last frame wins a near value.
function tapset_of(hf_average: i32, last: i32): i32 {
  let hf = hf_average;
  if (last == 2) {
    hf += 4;
  } else if (last == 0) {
    hf -= 4;
  }
  if (hf > 22) {
    return 2;
  }
  if (hf > 18) {
    return 1;
  }
  return 0;
}

function spread_of(sum: i32): i32 {
  if (sum < 80) {
    return vq.SPREAD_AGGRESSIVE;
  }
  if (sum < 256) {
    return vq.SPREAD_NORMAL;
  }
  if (sum < 384) {
    return vq.SPREAD_LIGHT;
  }
  return vq.SPREAD_NONE;
}

pub function spreading_decision(x: [f32], s: Spread, update_hf: boolean, end: i32, channels: i32, m: i32, weights: [i32]): Spread {
  let n0 = m * modes.SHORT_MDCT_SIZE;
  if (m * (modes.EBANDS[end] - modes.EBANDS[end - 1]) <= 8) {
    return Spread { ...s, decision: vq.SPREAD_NONE };
  }
  let sum = 0;
  let bands = 0;
  let hf_sum = 0;
  for c in 0..channels {
    for i in 0..end {
      let n = m * (modes.EBANDS[i + 1] - modes.EBANDS[i]);
      if (n > 8) {
        let t = small_counts(x, m * modes.EBANDS[i] + c * n0, n);
        if (i > modes.BANDS - 4) {
          hf_sum += 32 * (t.sixteenth + t.quarter) / n;
        }
        let tmp = half_or_more(t.tiny, n) + half_or_more(t.sixteenth, n) + half_or_more(t.quarter, n);
        sum += tmp * weights[i];
        bands += weights[i];
      }
    }
  }
  let r = s;
  if (update_hf) {
    if (hf_sum != 0) {
      hf_sum /= channels * (4 - modes.BANDS + end);
    }
    let hf_average = s.hf_average + hf_sum >> 1;
    r = Spread { ...r, hf_average: hf_average, tapset: tapset_of(hf_average, s.tapset) };
  }
  let average = (sum << 8) / bands + s.average >> 1;
  let biased = 3 * average + ((3 - s.decision << 7) + 64) + 2 >> 2;
  return Spread { ...r, decision: spread_of(biased), average: average };
}

pub function hysteresis_decision(val: f32, thresholds: [f32], hysteresis: [f32], n: i32, prev: i32): i32 {
  let i = 0;
  while (i < n && !(val < thresholds[i])) {
    i += 1;
  }
  if (i > prev && val < thresholds[prev] + hysteresis[prev]) {
    return prev;
  }
  if (i < prev && val > thresholds[prev - 1] - hysteresis[prev - 1]) {
    return prev;
  }
  return i;
}
```
