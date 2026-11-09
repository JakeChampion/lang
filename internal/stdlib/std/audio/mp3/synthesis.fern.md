# MP3 hybrid and polyphase synthesis

This module transforms reconstructed spectra into floating-point PCM.
Its arithmetic preserves the scalar reference operation order within each lane.
The DCT and synthesis kernels use portable four-lane float operations.
The public decoder supplies correctly sized arrays and validated block descriptions.

`granule` retains the compact 960-sample history contract.
`granule_cached` uses `QMF_SAMPLES` slots with an initially zero workspace.
The first 960 slots contain the same history.
The following 1,152 slots retain synthesis workspace, and the final 15 slots preserve inactive mono history.
The caller reuses this array between granules.

The implementation derives from the scalar [minimp3 reference](https://github.com/lieff/minimp3/tree/ea99364f61c14656440e8d77e9c233ccf3124633).
The minimp3 authors dedicated that source to the public domain under [CC0](https://creativecommons.org/publicdomain/zero/1.0/).
The module executes Fern code and needs no external codec library.

```fern
<<*>>=
// MP3 hybrid transforms and portable vector PCM synthesis.
import "std/audio/mp3/tables";
import "std/audio/mp3/spectral";

// The final 15 slots preserve inactive mono history during in-place synthesis.
pub const QMF_SAMPLES: i32 = 33 * 64 + 15;

function dct9(own y: f32[]): f32[] {
  let s0 = y[0];
  let s2 = y[2];
  let s4 = y[4];
  let s6 = y[6];
  let s8 = y[8];
  let t0 = s0 + s6 * 0.5f32;
  s0 -= s6;
  let t4 = (s4 + s2) * 0.93969262f32;
  let t2 = (s8 + s2) * 0.76604444f32;
  s6 = (s4 - s8) * 0.17364818f32;
  s4 += s8 - s2;
  s2 = s0 - s4 * 0.5f32;
  y = y.with(4, s4 + s0);
  s8 = t0 - t2 + s6;
  s0 = t0 - t4 + t2;
  s4 = t0 + t4 - s6;
  let s1 = y[1];
  let s3 = y[3];
  let s5 = y[5];
  let s7 = y[7];
  s3 *= 0.86602540f32;
  t0 = (s5 + s1) * 0.98480775f32;
  t4 = (s5 - s7) * 0.34202014f32;
  t2 = (s1 + s7) * 0.64278761f32;
  s1 = (s1 - s5 - s7) * 0.86602540f32;
  s5 = t0 - s3 - t2;
  s7 = t4 - s3 - t0;
  s3 = t4 + s3 - t2;
  return y.with(0, s4 - s7).with(1, s2 + s1).with(2, s0 - s3).with(3, s8 + s5).with(5, s8 - s5).with(6, s0 + s3).with(7, s2 - s1).with(8, s4 + s7);
}

function long_blocks(own samples: f32[], own overlap: f32[], base: i32, ov_at: i32, window: i32, count: i32): (f32[], f32[]) {
  let co = spectral.zeros(9);
  let si = spectral.zeros(9);
  for block in 0..count {
    let at = base + block * 18;
    let ov = ov_at + block * 9;
    co = co.with(0, -samples[at]);
    si = si.with(0, samples[at + 17]);
    for i in 0..4 {
      si = si.with(8 - 2 * i, samples[at + 4 * i + 1] - samples[at + 4 * i + 2]);
      co = co.with(1 + 2 * i, samples[at + 4 * i + 1] + samples[at + 4 * i + 2]);
      si = si.with(7 - 2 * i, samples[at + 4 * i + 4] - samples[at + 4 * i + 3]);
      co = co.with(2 + 2 * i, -(samples[at + 4 * i + 3] + samples[at + 4 * i + 4]));
    }
    co = dct9(co);
    si = dct9(si);
    for i in [1, 3, 5, 7] {
      si = si.with(i, -si[i]);
    }
    for i in 0..9 {
      let old = overlap[ov + i];
      let sum = co[i] * tables.G_TWID9[9 + i] + si[i] * tables.G_TWID9[i];
      overlap = overlap.with(ov + i, co[i] * tables.G_TWID9[i] - si[i] * tables.G_TWID9[9 + i]);
      samples = samples.with(at + i, old * tables.G_MDCT_WINDOW[window + i] - sum * tables.G_MDCT_WINDOW[window + 9 + i]);
      samples = samples.with(at + 17 - i, old * tables.G_MDCT_WINDOW[window + 9 + i] + sum * tables.G_MDCT_WINDOW[window + i]);
    }
  }
  return (samples, overlap);
}

function idct3(x0: f32, x1: f32, x2: f32): (f32, f32, f32) {
  let m1 = x1 * 0.86602540f32;
  let a1 = x0 - x2 * 0.5f32;
  return (a1 + m1, x0 + x2, a1 - m1);
}

struct Short { a0: f32, a1: f32, a2: f32, a3: f32, a4: f32, a5: f32, o0: f32, o1: f32, o2: f32 }

function rotate(co: f32, si: f32, ov: f32, i: i32): (f32, f32, f32) {
  let sum = co * tables.G_TWID3[3 + i] + si * tables.G_TWID3[i];
  let next = co * tables.G_TWID3[i] - si * tables.G_TWID3[3 + i];
  return (ov * tables.G_TWID3[2 - i] - sum * tables.G_TWID3[5 - i], ov * tables.G_TWID3[5 - i] + sum * tables.G_TWID3[2 - i], next);
}

function short_values(x: [f32], at: i32, ov0: f32, ov1: f32, ov2: f32): Short {
  let (c0, c1, c2) = idct3(-x[at], x[at + 6] + x[at + 3], x[at + 12] + x[at + 9]);
  let (s0, s1, s2) = idct3(x[at + 15], x[at + 12] - x[at + 9], x[at + 6] - x[at + 3]);
  let (a0, b0, o0) = rotate(c0, s0, ov0, 0);
  let (a1, b1, o1) = rotate(c1, -s1, ov1, 1);
  let (a2, b2, o2) = rotate(c2, s2, ov2, 2);
  return Short { a0: a0, a1: a1, a2: a2, a3: b2, a4: b1, a5: b0, o0: o0, o1: o1, o2: o2 };
}

function store_short(own samples: f32[], at: i32, s: Short): f32[] {
  return samples.with(at, s.a0).with(at + 1, s.a1).with(at + 2, s.a2).with(at + 3, s.a3).with(at + 4, s.a4).with(at + 5, s.a5);
}

function short_blocks(own samples: f32[], own overlap: f32[], base: i32, ov_at: i32, count: i32): (f32[], f32[]) {
  for block in 0..count {
    let at = base + block * 18;
    let ov = ov_at + block * 9;
    let tmp: f32[] = [];
    for i in 0..18 {
      tmp = tmp.append(samples[at + i]);
    }
    for i in 0..6 {
      samples = samples.with(at + i, overlap[ov + i]);
    }
    let a = short_values(tmp, 0, overlap[ov + 6], overlap[ov + 7], overlap[ov + 8]);
    samples = store_short(samples, at + 6, a);
    let b = short_values(tmp, 1, a.o0, a.o1, a.o2);
    samples = store_short(samples, at + 12, b);
    let c = short_values(tmp, 2, b.o0, b.o1, b.o2);
    overlap = store_short(overlap, ov, c).with(ov + 6, c.o0).with(ov + 7, c.o1).with(ov + 8, c.o2);
  }
  return (samples, overlap);
}

pub function hybrid(own samples: f32[], own overlap: f32[], base: i32, ov_at: i32, kind: i32, longs: i32, antialias: i32): (f32[], f32[]) {
  for block in 0..antialias {
    for i in 0..8 {
      let u_at = base + block * 18 + 18 + i;
      let d_at = base + block * 18 + 17 - i;
      let u = samples[u_at];
      let d = samples[d_at];
      samples = samples.with(u_at, u * tables.G_AA[i] - d * tables.G_AA[8 + i]);
      samples = samples.with(d_at, u * tables.G_AA[8 + i] + d * tables.G_AA[i]);
    }
  }
  if (longs != 0) {
    let (a, b) = long_blocks(samples, overlap, base, ov_at, 0, longs);
    samples = a;
    overlap = b;
  }
  let (a, b) = if (kind == 2) { short_blocks(samples, overlap, base + longs * 18, ov_at + longs * 9, 32 - longs) } else { long_blocks(samples, overlap, base + longs * 18, ov_at + longs * 9, if (kind == 3) { 18 } else { 0 }, 32 - longs) };
  samples = a;
  overlap = b;
  for block in 0..16 {
    for i in 0..9 {
      let at = base + (block * 2 + 1) * 18 + i * 2 + 1;
      samples = samples.with(at, -samples[at]);
    }
  }
  return (samples, overlap);
}

function butterfly8(own t: f32[], at: i32): f32[] {
  let x0 = t[at];
  let x1 = t[at + 1];
  let x2 = t[at + 2];
  let x3 = t[at + 3];
  let x4 = t[at + 4];
  let x5 = t[at + 5];
  let x6 = t[at + 6];
  let x7 = t[at + 7];
  let xt = x0 - x7;
  x0 += x7;
  x7 = x1 - x6;
  x1 += x6;
  x6 = x2 - x5;
  x2 += x5;
  x5 = x3 - x4;
  x3 += x4;
  x4 = x0 - x3;
  x0 += x3;
  x3 = x1 - x2;
  x1 += x2;
  t = t.with(at, x0 + x1).with(at + 4, (x0 - x1) * 0.70710677f32);
  x5 += x6;
  x6 = (x6 + x7) * 0.70710677f32;
  x7 += xt;
  x3 = (x3 + x4) * 0.70710677f32;
  x5 -= x7 * 0.198912367f32;
  x7 += x5 * 0.382683432f32;
  x5 -= x7 * 0.198912367f32;
  x0 = xt - x6;
  xt += x6;
  return t.with(at + 1, (xt + x7) * 0.50979561f32).with(at + 2, (x4 + x3) * 0.54119611f32).with(at + 3, (x0 - x5) * 0.60134488f32).with(at + 5, (x0 + x5) * 0.89997619f32).with(at + 6, (x4 - x3) * 1.30656302f32).with(at + 7, (xt - x7) * 2.56291556f32);
}

function dct32_tail(own samples: f32[], base: i32): f32[] {
  let t = spectral.zeros(32);
  for k in 16..18 {
    for i in 0..8 {
      let x0 = samples[base + k + i * 18];
      let x1 = samples[base + k + (15 - i) * 18];
      let x2 = samples[base + k + (16 + i) * 18];
      let x3 = samples[base + k + (31 - i) * 18];
      let t0 = x0 + x3;
      let t1 = x1 + x2;
      let t2 = (x1 - x2) * tables.G_SEC[3 * i];
      let t3 = (x0 - x3) * tables.G_SEC[3 * i + 1];
      t = t.with(i, t0 + t1).with(8 + i, (t0 - t1) * tables.G_SEC[3 * i + 2]);
      t = t.with(16 + i, t3 + t2).with(24 + i, (t3 - t2) * tables.G_SEC[3 * i + 2]);
    }
    for i in 0..4 {
      t = butterfly8(t, i * 8);
    }
    for i in 0..7 {
      let at = base + k + i * 4 * 18;
      samples = samples.with(at, t[i]);
      samples = samples.with(at + 18, t[16 + i] + t[24 + i] + t[24 + i + 1]);
      samples = samples.with(at + 36, t[8 + i] + t[8 + i + 1]);
      samples = samples.with(at + 54, t[16 + i + 1] + t[24 + i] + t[24 + i + 1]);
    }
    let at = base + k + 28 * 18;
    samples = samples.with(at, t[7]).with(at + 18, t[23] + t[31]).with(at + 36, t[15]).with(at + 54, t[31]);
  }
  return samples;
}

function butterfly8x4(own t: f32[], at: i32): f32[] {
  let x0 = __f32x4_load(t, at);
  let x1 = __f32x4_load(t, at + 4);
  let x2 = __f32x4_load(t, at + 8);
  let x3 = __f32x4_load(t, at + 12);
  let x4 = __f32x4_load(t, at + 16);
  let x5 = __f32x4_load(t, at + 20);
  let x6 = __f32x4_load(t, at + 24);
  let x7 = __f32x4_load(t, at + 28);
  let xt = x0 - x7;
  x0 += x7;
  x7 = x1 - x6;
  x1 += x6;
  x6 = x2 - x5;
  x2 += x5;
  x5 = x3 - x4;
  x3 += x4;
  x4 = x0 - x3;
  x0 += x3;
  x3 = x1 - x2;
  x1 += x2;
  let inv_sqrt2 = __f32x4_splat(0.70710677f32);
  t = __f32x4_with(t, at, x0 + x1);
  t = __f32x4_with(t, at + 16, (x0 - x1) * inv_sqrt2);
  x5 += x6;
  x6 = (x6 + x7) * inv_sqrt2;
  x7 += xt;
  x3 = (x3 + x4) * inv_sqrt2;
  x5 -= x7 * __f32x4_splat(0.198912367f32);
  x7 += x5 * __f32x4_splat(0.382683432f32);
  x5 -= x7 * __f32x4_splat(0.198912367f32);
  x0 = xt - x6;
  xt += x6;
  t = __f32x4_with(t, at + 4, (xt + x7) * __f32x4_splat(0.50979561f32));
  t = __f32x4_with(t, at + 8, (x4 + x3) * __f32x4_splat(0.54119611f32));
  t = __f32x4_with(t, at + 12, (x0 - x5) * __f32x4_splat(0.60134488f32));
  t = __f32x4_with(t, at + 20, (x0 + x5) * __f32x4_splat(0.89997619f32));
  t = __f32x4_with(t, at + 24, (x4 - x3) * __f32x4_splat(1.30656302f32));
  return __f32x4_with(t, at + 28, (xt - x7) * __f32x4_splat(2.56291556f32));
}

function dct32x4(own samples: f32[], base: i32): f32[] {
  let t = spectral.zeros(128);
  for group in 0..4 {
    let k = group * 4;
    for i in 0..8 {
      let x0 = __f32x4_load(samples, base + k + i * 18);
      let x1 = __f32x4_load(samples, base + k + (15 - i) * 18);
      let x2 = __f32x4_load(samples, base + k + (16 + i) * 18);
      let x3 = __f32x4_load(samples, base + k + (31 - i) * 18);
      let t0 = x0 + x3;
      let t1 = x1 + x2;
      let t2 = (x1 - x2) * __f32x4_splat(tables.G_SEC[3 * i]);
      let t3 = (x0 - x3) * __f32x4_splat(tables.G_SEC[3 * i + 1]);
      let factor = __f32x4_splat(tables.G_SEC[3 * i + 2]);
      t = __f32x4_with(t, i * 4, t0 + t1);
      t = __f32x4_with(t, (8 + i) * 4, (t0 - t1) * factor);
      t = __f32x4_with(t, (16 + i) * 4, t3 + t2);
      t = __f32x4_with(t, (24 + i) * 4, (t3 - t2) * factor);
    }
    for i in 0..4 {
      t = butterfly8x4(t, i * 32);
    }
    for i in 0..8 {
      let at = base + k + i * 4 * 18;
      let y0 = __f32x4_load(t, i * 4);
      let a = __f32x4_load(t, (16 + i) * 4);
      let b = __f32x4_load(t, (24 + i) * 4);
      let y1 = a + b;
      let y2 = __f32x4_load(t, (8 + i) * 4);
      let y3 = b;
      if (i < 7) {
        let c = __f32x4_load(t, (24 + i + 1) * 4);
        y1 += c;
        y2 += __f32x4_load(t, (8 + i + 1) * 4);
        y3 = __f32x4_load(t, (16 + i + 1) * 4) + b + c;
      }
      samples = __f32x4_with(samples, at, y0);
      samples = __f32x4_with(samples, at + 18, y1);
      samples = __f32x4_with(samples, at + 36, y2);
      samples = __f32x4_with(samples, at + 54, y3);
    }
  }
  return samples;
}

function pair(z: [f32], at: i32): (f32, f32) {
  let a = (z[at + 14 * 64] - z[at]) * 29.0f32;
  a += (z[at + 64] + z[at + 13 * 64]) * 213.0f32;
  a += (z[at + 12 * 64] - z[at + 2 * 64]) * 459.0f32;
  a += (z[at + 3 * 64] + z[at + 11 * 64]) * 2037.0f32;
  a += (z[at + 10 * 64] - z[at + 4 * 64]) * 5153.0f32;
  a += (z[at + 5 * 64] + z[at + 9 * 64]) * 6574.0f32;
  a += (z[at + 8 * 64] - z[at + 6 * 64]) * 37489.0f32;
  a += z[at + 7 * 64] * 75038.0f32;
  let b = z[at + 2 + 14 * 64] * 104.0f32;
  b += z[at + 2 + 12 * 64] * 1567.0f32;
  b += z[at + 2 + 10 * 64] * 9727.0f32;
  b += z[at + 2 + 8 * 64] * 64019.0f32;
  b += z[at + 2 + 6 * 64] * -9975.0f32;
  b += z[at + 2 + 4 * 64] * -45.0f32;
  b += z[at + 2 + 2 * 64] * 146.0f32;
  b += z[at + 2] * -5.0f32;
  return (a / 32768.0f32, b / 32768.0f32);
}

function synth(samples: [f32], base: i32, channels: i32, own lines: f32[], offset: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  let right = base + 576 * (channels - 1);
  let z = offset + 15 * 64;
  lines = lines.with(z + 60, samples[base + 18 * 16]).with(z + 61, samples[right + 18 * 16]).with(z + 62, samples[base]).with(z + 63, samples[right]);
  lines = lines.with(z + 124, samples[base + 1 + 18 * 16]).with(z + 125, samples[right + 1 + 18 * 16]).with(z + 126, samples[base + 1]).with(z + 127, samples[right + 1]);
  for ch in 0..channels {
    let (a, b) = pair(lines, offset + 60 + ch);
    let (c, d) = pair(lines, offset + 124 + ch);
    output = output.with(out_at + ch, a).with(out_at + 16 * channels + ch, b).with(out_at + 32 * channels + ch, c).with(out_at + 48 * channels + ch, d);
  }
  let wa = 0;
  let i = 14;
  let a = __f32x4_splat(0.0f32);
  let b = __f32x4_splat(0.0f32);
  while (i >= 0) {
    lines = lines.with(z + 4 * i, samples[base + 18 * (31 - i)]).with(z + 4 * i + 1, samples[right + 18 * (31 - i)]);
    lines = lines.with(z + 4 * i + 2, samples[base + 1 + 18 * (31 - i)]).with(z + 4 * i + 3, samples[right + 1 + 18 * (31 - i)]);
    lines = lines.with(z + 4 * (i + 16), samples[base + 1 + 18 * (1 + i)]).with(z + 4 * (i + 16) + 1, samples[right + 1 + 18 * (1 + i)]);
    lines = lines.with(z + 4 * (i - 16) + 2, samples[base + 18 * (1 + i)]).with(z + 4 * (i - 16) + 3, samples[right + 18 * (1 + i)]);
    for k in 0..8 {
      let w0 = tables.G_WIN[wa];
      let w1 = tables.G_WIN[wa + 1];
      wa += 2;
      let vz = __f32x4_load(lines, z + 4 * i - k * 64);
      let vy = __f32x4_load(lines, z + 4 * i - (15 - k) * 64);
      let w0v = __f32x4_splat(w0);
      let w1v = __f32x4_splat(w1);
      let bv = vz * w1v + vy * w0v;
      let av = if ((k & 1) != 0) { vy * w1v - vz * w0v } else { vz * w0v - vy * w1v };
      if (k == 0) {
        a = av;
        b = bv;
      } else {
        a += av;
        b += bv;
      }
    }
    output = output.with(out_at + (15 - i) * channels, __f32x4_lane(a, 0) / 32768.0f32);
    output = output.with(out_at + (17 + i) * channels, __f32x4_lane(b, 0) / 32768.0f32);
    output = output.with(out_at + (47 - i) * channels, __f32x4_lane(a, 2) / 32768.0f32);
    output = output.with(out_at + (49 + i) * channels, __f32x4_lane(b, 2) / 32768.0f32);
    if (channels == 2) {
      output = output.with(out_at + (15 - i) * channels + 1, __f32x4_lane(a, 1) / 32768.0f32);
      output = output.with(out_at + (17 + i) * channels + 1, __f32x4_lane(b, 1) / 32768.0f32);
      output = output.with(out_at + (47 - i) * channels + 1, __f32x4_lane(a, 3) / 32768.0f32);
      output = output.with(out_at + (49 + i) * channels + 1, __f32x4_lane(b, 3) / 32768.0f32);
    }
    i -= 1;
  }
  return (lines, output);
}

function render(own lines: f32[], own samples: f32[], channels: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  for ch in 0..channels {
    samples = dct32_tail(dct32x4(samples, ch * 576), ch * 576);
  }
  for block in 0..9 {
    let i = block * 2;
    let (next, pcm) = synth(samples, i, channels, lines, i * 64, output, out_at + 32 * channels * i);
    lines = next;
    output = pcm;
  }
  return (lines, output);
}

pub function granule(own qmf: f32[], own samples: f32[], channels: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  let lines = spectral.zeros(33 * 64);
  for i in 0..15 * 64 {
    lines = lines.with(i, qmf[i]);
  }
  let (next, pcm) = render(lines, samples, channels, output, out_at);
  for i in 0..15 * 64 {
    if (channels == 2 || (i & 1) == 0) {
      qmf = qmf.with(i, next[18 * 64 + i]);
    }
  }
  return (qmf, pcm);
}

pub function granule_cached(own lines: f32[], own samples: f32[], channels: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  if (channels == 1) {
    // The first pair overwrites these inactive odd history slots.
    for i in 0..15 {
      lines = lines.with(33 * 64 + i, lines[899 + i * 4]);
    }
  }
  let (next, pcm) = render(lines, samples, channels, output, out_at);
  lines = next;
  for i in 0..15 * 64 {
    if (channels == 2 || (i & 1) == 0) {
      lines = lines.with(i, lines[18 * 64 + i]);
    }
  }
  if (channels == 1) {
    for i in 0..15 {
      lines = lines.with(899 + i * 4, lines[33 * 64 + i]);
    }
  }
  return (lines, pcm);
}
```
