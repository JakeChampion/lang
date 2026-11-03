# The CELT MDCT

This module changes CELT audio between time and frequency (RFC 6716, section 4.3.7). It is a port of the float build of `kiss_fft.c` and `mdct.c` from libopus 1.4.

## Model

The MDCT of a block of `n / 2` values, plus `OVERLAP` values of the block after it, gives `n / 2` frequency values. The window of `std/opus/modes` fades one block into the other over the overlap. The inverse MDCT adds the tail of the block before to the start of its block, so the aliasing of the two blocks cancels.

CELT uses four sizes of MDCT. `shift` 0 gives the size `modes.MDCT_SIZE`, 1920, for a long block of 960 values. Each step of `shift` makes the size half as large. A frame of short blocks uses `shift` 3 and interleaves the frequency values of its blocks, with `stride` equal to the number of blocks.

The core is a complex FFT of `n / 4` values. Its size factors into stages of radix 2, 3, 4 and 5. The tables of each size come from libopus, through `std/opus/modes`.

## Contracts

- `shift` is 0 to 3.
- A complex array holds the real part and the imaginary part of each value, one after the other.
- `forward` reads `n / 2 + OVERLAP` values of `input` from `in_at`. It writes `n / 2` values of `out`, from `out_at`, at a step of `stride`.
- `backward` reads `n / 2` values of `input` from `in_at`, at a step of `stride`. It changes `n / 2 + OVERLAP / 2` values of `out` from `out_at`. Before the call, the first `OVERLAP / 2` of these values must be the tail that `backward` wrote for the block before. After the call, the first `n / 2` values are the output, and the last `OVERLAP / 2` values are the tail for the next block.
- The arithmetic is in f32, in the same order as libopus, so the results are the same bits.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<butterflies>>

<<fft>>

<<mdct>>
```

### `module introduction`

```fern
<<module introduction>>=
// The CELT MDCT, ported from libopus 1.4.
//
// Copyright (c) 2003-2004, Mark Borgerding
// Copyright (c) 2005-2007, Xiph.Org Foundation
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2008, Xiph.Org Foundation, CSIRO
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
```

### `butterflies`

Each butterfly does one stage of the FFT in place. `at` is the offset in the array of the first complex value, and the other offsets are in complex values. The twiddles of all sizes share one table of the largest size, so a smaller FFT reads them at a step of `fstride`.

A radix-2 stage always comes after a radix-4 stage, so it has `m` equal to 4. A radix-4 stage with `m` equal to 1 has twiddles of 1, and has a faster path.

```fern
<<butterflies>>=
const TW: f32 = 0.7071067690849304f32;

function tw_r(j: i32): f32 {
  return modes.FFT_TWIDDLES[2 * j];
}

function tw_i(j: i32): f32 {
  return modes.FFT_TWIDDLES[2 * j + 1];
}

// The product of the complex value at `a` and the twiddle `j`.
function cmul(f: [f32], a: i32, j: i32): (f32, f32) {
  let r: f32 = f[a];
  let i: f32 = f[a + 1];
  return (r * tw_r(j) - i * tw_i(j), r * tw_i(j) + i * tw_r(j));
}

function set(own f: f32[], a: i32, r: f32, i: f32): f32[] {
  f = f.with(a, r);
  return f.with(a + 1, i);
}

// The value at `a` minus `t` goes to `b`, and the value at `a` plus `t` goes
// to `a`.
function cross(own f: f32[], a: i32, b: i32, tr: f32, ti: f32): f32[] {
  let ar: f32 = f[a];
  let ai: f32 = f[a + 1];
  f = set(f, b, ar - tr, ai - ti);
  return set(f, a, ar + tr, ai + ti);
}

function bfly2(own f: f32[], at: i32, n: i32): f32[] {
  for i in 0..n {
    let o: i32 = at + 16 * i;
    let r0: f32 = f[o + 8];
    let i0: f32 = f[o + 9];
    let r1: f32 = (f[o + 10] + f[o + 11]) * TW;
    let i1: f32 = (f[o + 11] - f[o + 10]) * TW;
    let r2: f32 = f[o + 13];
    let i2: f32 = -f[o + 12];
    let r3: f32 = (f[o + 15] - f[o + 14]) * TW;
    let i3: f32 = -(f[o + 15] + f[o + 14]) * TW;
    f = cross(f, o, o + 8, r0, i0);
    f = cross(f, o + 2, o + 10, r1, i1);
    f = cross(f, o + 4, o + 12, r2, i2);
    f = cross(f, o + 6, o + 14, r3, i3);
  }
  return f;
}

function bfly4_unit(own f: f32[], at: i32, n: i32): f32[] {
  for i in 0..n {
    let o: i32 = at + 8 * i;
    let s0r: f32 = f[o] - f[o + 4];
    let s0i: f32 = f[o + 1] - f[o + 5];
    let f0r: f32 = f[o] + f[o + 4];
    let f0i: f32 = f[o + 1] + f[o + 5];
    let s1r: f32 = f[o + 2] + f[o + 6];
    let s1i: f32 = f[o + 3] + f[o + 7];
    let dr: f32 = f[o + 2] - f[o + 6];
    let di: f32 = f[o + 3] - f[o + 7];
    f = set(f, o + 4, f0r - s1r, f0i - s1i);
    f = set(f, o, f0r + s1r, f0i + s1i);
    f = set(f, o + 2, s0r + di, s0i - dr);
    f = set(f, o + 6, s0r - di, s0i + dr);
  }
  return f;
}

function bfly4(own f: f32[], at: i32, fstride: i32, m: i32, n: i32, mm: i32): f32[] {
  if (m == 1) {
    return bfly4_unit(f, at, n);
  }
  for i in 0..n {
    for j in 0..m {
      let o: i32 = at + 2 * (i * mm + j);
      let (s0r, s0i) = cmul(f, o + 2 * m, j * fstride);
      let (s1r, s1i) = cmul(f, o + 4 * m, 2 * j * fstride);
      let (s2r, s2i) = cmul(f, o + 6 * m, 3 * j * fstride);
      let s5r: f32 = f[o] - s1r;
      let s5i: f32 = f[o + 1] - s1i;
      let f0r: f32 = f[o] + s1r;
      let f0i: f32 = f[o + 1] + s1i;
      let s3r: f32 = s0r + s2r;
      let s3i: f32 = s0i + s2i;
      let s4r: f32 = s0r - s2r;
      let s4i: f32 = s0i - s2i;
      f = set(f, o + 4 * m, f0r - s3r, f0i - s3i);
      f = set(f, o, f0r + s3r, f0i + s3i);
      f = set(f, o + 2 * m, s5r + s4i, s5i - s4r);
      f = set(f, o + 6 * m, s5r - s4i, s5i + s4r);
    }
  }
  return f;
}

function bfly3(own f: f32[], at: i32, fstride: i32, m: i32, n: i32, mm: i32): f32[] {
  let epi: f32 = tw_i(fstride * m);
  for i in 0..n {
    for j in 0..m {
      let o: i32 = at + 2 * (i * mm + j);
      let (s1r, s1i) = cmul(f, o + 2 * m, j * fstride);
      let (s2r, s2i) = cmul(f, o + 4 * m, 2 * j * fstride);
      let s3r: f32 = s1r + s2r;
      let s3i: f32 = s1i + s2i;
      let er: f32 = (s1r - s2r) * epi;
      let ei: f32 = (s1i - s2i) * epi;
      let mr: f32 = f[o] - s3r * 0.5f32;
      let mi: f32 = f[o + 1] - s3i * 0.5f32;
      let f0r: f32 = f[o] + s3r;
      let f0i: f32 = f[o + 1] + s3i;
      f = set(f, o, f0r, f0i);
      f = set(f, o + 4 * m, mr + ei, mi - er);
      f = set(f, o + 2 * m, mr - ei, mi + er);
    }
  }
  return f;
}

// The five outputs of one radix-5 butterfly. `ya` and `yb` are the twiddles of
// one and two fifths of a turn.
function five(own f: f32[], o: i32, m: i32, fstride: i32, u: i32): f32[] {
  let y: i32 = fstride * m;
  let yar: f32 = tw_r(y);
  let yai: f32 = tw_i(y);
  let ybr: f32 = tw_r(2 * y);
  let ybi: f32 = tw_i(2 * y);
  let s0r: f32 = f[o];
  let s0i: f32 = f[o + 1];
  let (s1r, s1i) = cmul(f, o + 2 * m, u * fstride);
  let (s2r, s2i) = cmul(f, o + 4 * m, 2 * u * fstride);
  let (s3r, s3i) = cmul(f, o + 6 * m, 3 * u * fstride);
  let (s4r, s4i) = cmul(f, o + 8 * m, 4 * u * fstride);
  let s7r: f32 = s1r + s4r;
  let s7i: f32 = s1i + s4i;
  let s10r: f32 = s1r - s4r;
  let s10i: f32 = s1i - s4i;
  let s8r: f32 = s2r + s3r;
  let s8i: f32 = s2i + s3i;
  let s9r: f32 = s2r - s3r;
  let s9i: f32 = s2i - s3i;
  let s5r: f32 = s0r + (s7r * yar + s8r * ybr);
  let s5i: f32 = s0i + (s7i * yar + s8i * ybr);
  let s6r: f32 = s10i * yai + s9i * ybi;
  let s6i: f32 = -(s10r * yai + s9r * ybi);
  let s11r: f32 = s0r + (s7r * ybr + s8r * yar);
  let s11i: f32 = s0i + (s7i * ybr + s8i * yar);
  let s12r: f32 = s9i * yai - s10i * ybi;
  let s12i: f32 = s10r * ybi - s9r * yai;
  f = set(f, o, s0r + (s7r + s8r), s0i + (s7i + s8i));
  f = set(f, o + 2 * m, s5r - s6r, s5i - s6i);
  f = set(f, o + 8 * m, s5r + s6r, s5i + s6i);
  f = set(f, o + 4 * m, s11r + s12r, s11i + s12i);
  return set(f, o + 6 * m, s11r - s12r, s11i - s12i);
}

function bfly5(own f: f32[], at: i32, fstride: i32, m: i32, n: i32, mm: i32): f32[] {
  for i in 0..n {
    for u in 0..m {
      f = five(f, at + 2 * (i * mm + u), m, fstride, u);
    }
  }
  return f;
}
```

### `fft`

`fft_impl` runs the stages of the FFT of size index `s` on values that are already in bit-reverse order. It does not scale them. `fft` puts its input in bit-reverse order, scales it by `1 / size`, and runs the stages.

```fern
<<fft>>=
const BITREV_AT: i32[] = [0, 480, 720, 840];

function zeros(n: i32): f32[] {
  let x: f32[] = [];
  for i in 0..n {
    x = x.append(0.0f32);
  }
  return x;
}

function factor(s: i32, i: i32): i32 {
  return modes.FFT_FACTORS[16 * s + i];
}

function stage(own f: f32[], at: i32, s: i32, i: i32, m: i32, stride: i32): f32[] {
  let shift: i32 = modes.FFT_SHIFT[s];
  if (shift < 0) {
    shift = 0;
  }
  let m2: i32 = 1;
  if (i != 0) {
    m2 = factor(s, 2 * i - 1);
  }
  let p: i32 = factor(s, 2 * i);
  if (p == 2) {
    return bfly2(f, at, stride);
  }
  if (p == 4) {
    return bfly4(f, at, stride << shift, m, stride, m2);
  }
  if (p == 3) {
    return bfly3(f, at, stride << shift, m, stride, m2);
  }
  return bfly5(f, at, stride << shift, m, stride, m2);
}

// The FFT of size index `s`, in place, on the complex values from `at`.
pub function fft_impl(own f: f32[], at: i32, s: i32): f32[] {
  let fstride: i32[] = [1];
  let l: i32 = 0;
  let m: i32 = 0;
  while (m != 1) {
    m = factor(s, 2 * l + 1);
    fstride = fstride.append(fstride[l] * factor(s, 2 * l));
    l = l + 1;
  }
  m = factor(s, 2 * l - 1);
  let i: i32 = l - 1;
  while (i >= 0) {
    f = stage(f, at, s, i, m, fstride[i]);
    m = 1;
    if (i != 0) {
      m = factor(s, 2 * i - 1);
    }
    i = i - 1;
  }
  return f;
}

// The forward FFT of the `modes.FFT_SIZE[s]` complex values of `input`,
// scaled by `1 / size`.
pub function fft(input: [f32], s: i32): f32[] {
  let n: i32 = modes.FFT_SIZE[s];
  let scale: f32 = modes.FFT_SCALE[s];
  let out: f32[] = zeros(2 * n);
  for i in 0..n {
    out = set(out, 2 * modes.FFT_BITREV[BITREV_AT[s] + i], scale * input[2 * i], scale * input[2 * i + 1]);
  }
  return fft_impl(out, 0, s);
}
```

### `mdct`

The forward MDCT folds the windowed input into `n / 2` values, rotates them, runs an FFT of `n / 4` complex values, and rotates the result. The inverse MDCT does the same steps in the opposite order. Then it mirrors the overlap with the window on it, and adds the tail of the block before.

```fern
<<mdct>>=
const TRIG_AT: i32[] = [0, 960, 1440, 1680];

function trig(at: i32, i: i32): f32 {
  return modes.MDCT_TWIDDLES[at + i];
}

function win(i: i32): f32 {
  return modes.WINDOW[i];
}

// The window, shuffle and fold of the four quarters of the input.
function fold(x: [f32], at: i32, n2: i32, n4: i32): f32[] {
  let ov: i32 = modes.OVERLAP;
  let q: i32 = ov + 3 >> 2;
  let xp1: i32 = at + (ov >> 1);
  let xp2: i32 = at + n2 - 1 + (ov >> 1);
  let wp1: i32 = ov >> 1;
  let wp2: i32 = (ov >> 1) - 1;
  let y: f32[] = [];
  for i in 0..q {
    y = y.append(win(wp2) * x[xp1 + n2] + win(wp1) * x[xp2]);
    y = y.append(win(wp1) * x[xp1] - win(wp2) * x[xp2 - n2]);
    xp1 = xp1 + 2;
    xp2 = xp2 - 2;
    wp1 = wp1 + 2;
    wp2 = wp2 - 2;
  }
  wp1 = 0;
  wp2 = ov - 1;
  for i in q..n4 - q {
    y = y.append(x[xp2]);
    y = y.append(x[xp1]);
    xp1 = xp1 + 2;
    xp2 = xp2 - 2;
  }
  for i in n4 - q..n4 {
    y = y.append(-(win(wp1) * x[xp1 - n2]) + win(wp2) * x[xp2]);
    y = y.append(win(wp2) * x[xp1] + win(wp1) * x[xp2 + n2]);
    xp1 = xp1 + 2;
    xp2 = xp2 - 2;
    wp1 = wp1 + 2;
    wp2 = wp2 - 2;
  }
  return y;
}

// The rotation before the FFT, with the scale of the FFT, into bit-reverse
// order.
function pre_rotate(f: [f32], shift: i32, n4: i32): f32[] {
  let t: i32 = TRIG_AT[shift];
  let scale: f32 = modes.FFT_SCALE[shift];
  let out: f32[] = zeros(2 * n4);
  for i in 0..n4 {
    let t0: f32 = trig(t, i);
    let t1: f32 = trig(t, n4 + i);
    let re: f32 = f[2 * i];
    let im: f32 = f[2 * i + 1];
    let yr: f32 = re * t0 - im * t1;
    let yi: f32 = im * t0 + re * t1;
    out = set(out, 2 * modes.FFT_BITREV[BITREV_AT[shift] + i], scale * yr, scale * yi);
  }
  return out;
}

// The forward MDCT of size `modes.MDCT_SIZE >> shift`.
pub function forward(input: [f32], in_at: i32, own out: f32[], out_at: i32, shift: i32, stride: i32): f32[] {
  let n: i32 = modes.MDCT_SIZE >> shift;
  let n2: i32 = n >> 1;
  let n4: i32 = n >> 2;
  let t: i32 = TRIG_AT[shift];
  let f2: f32[] = fft_impl(pre_rotate(fold(input, in_at, n2, n4), shift, n4), 0, shift);
  for i in 0..n4 {
    let fr: f32 = f2[2 * i];
    let fi: f32 = f2[2 * i + 1];
    out = out.with(out_at + 2 * stride * i, fi * trig(t, n4 + i) - fr * trig(t, i));
    out = out.with(out_at + stride * (n2 - 1) - 2 * stride * i, fr * trig(t, n4 + i) + fi * trig(t, i));
  }
  return out;
}

// The rotation after the FFT of the inverse MDCT, from both ends of the
// buffer at once, so that it is in place.
function unrotate(own out: f32[], yp: i32, t: i32, n2: i32, n4: i32): f32[] {
  let p0: i32 = yp;
  let p1: i32 = yp + n2 - 2;
  let pairs: i32 = n4 + 1 >> 1;
  for i in 0..pairs {
    let re0: f32 = out[p0 + 1];
    let im0: f32 = out[p0];
    let re1: f32 = out[p1 + 1];
    let im1: f32 = out[p1];
    let t0: f32 = trig(t, i);
    let t1: f32 = trig(t, n4 + i);
    let u0: f32 = trig(t, n4 - i - 1);
    let u1: f32 = trig(t, n2 - i - 1);
    out = out.with(p0, re0 * t0 + im0 * t1);
    out = out.with(p1 + 1, re0 * t1 - im0 * t0);
    out = out.with(p1, re1 * u0 + im1 * u1);
    out = out.with(p0 + 1, re1 * u1 - im1 * u0);
    p0 = p0 + 2;
    p1 = p1 - 2;
  }
  return out;
}

// The mirror of the overlap, with the window on it, for the time-domain
// aliasing cancellation.
function mirror(own out: f32[], at: i32): f32[] {
  let ov: i32 = modes.OVERLAP;
  let half: i32 = ov / 2;
  for i in 0..half {
    let a: i32 = at + ov - 1 - i;
    let b: i32 = at + i;
    let x1: f32 = out[a];
    let x2: f32 = out[b];
    out = out.with(b, win(ov - 1 - i) * x2 - win(i) * x1);
    out = out.with(a, win(i) * x2 + win(ov - 1 - i) * x1);
  }
  return out;
}

// The inverse MDCT of size `modes.MDCT_SIZE >> shift`.
pub function backward(input: [f32], in_at: i32, own out: f32[], out_at: i32, shift: i32, stride: i32): f32[] {
  let n: i32 = modes.MDCT_SIZE >> shift;
  let n2: i32 = n >> 1;
  let n4: i32 = n >> 2;
  let t: i32 = TRIG_AT[shift];
  let yp: i32 = out_at + (modes.OVERLAP >> 1);
  for i in 0..n4 {
    let x1: f32 = input[in_at + 2 * stride * i];
    let x2: f32 = input[in_at + stride * (n2 - 1) - 2 * stride * i];
    let r: i32 = yp + 2 * modes.FFT_BITREV[BITREV_AT[shift] + i];
    out = out.with(r + 1, x2 * trig(t, i) + x1 * trig(t, n4 + i));
    out = out.with(r, x1 * trig(t, i) - x2 * trig(t, n4 + i));
  }
  out = fft_impl(out, yp, shift);
  out = unrotate(out, yp, t, n2, n4);
  return mirror(out, out_at);
}
```
