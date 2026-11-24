# MP3 hybrid and polyphase synthesis

This module transforms reconstructed spectra into floating-point PCM.
Its arithmetic preserves the scalar reference operation order within each lane.
The DCT and synthesis kernels use portable four-lane float operations.
Long blocks pair two cosine transforms and two sine transforms in four lanes without temporary arrays.
The public decoder supplies correctly sized arrays and validated block descriptions.

The implementation derives from the scalar [minimp3 reference](https://github.com/lieff/minimp3/tree/ea99364f61c14656440e8d77e9c233ccf3124633).
The minimp3 authors dedicated that source to the public domain under [CC0](https://creativecommons.org/publicdomain/zero/1.0/).
The module executes Fern code and needs no external codec library.

```fern
<<*>>=
// MP3 hybrid transforms and portable vector PCM synthesis.
import "std/audio/mp3/tables";
import "std/audio/mp3/spectral";

function long_pair(own samples: f32[], own overlap: f32[], at: i32, other: i32, ov: i32, ov_other: i32, window: i32): (f32[], f32[]) {
  // Lanes hold cosine and sine values for the first block, then the second block.
  let v0 = __f32x4_of(-samples[at], samples[at + 17], -samples[other], samples[other + 17]);
  let v1 = __f32x4_of(samples[at + 1] + samples[at + 2], samples[at + 16] - samples[at + 15], samples[other + 1] + samples[other + 2], samples[other + 16] - samples[other + 15]);
  let v2 = __f32x4_of(-(samples[at + 3] + samples[at + 4]), samples[at + 13] - samples[at + 14], -(samples[other + 3] + samples[other + 4]), samples[other + 13] - samples[other + 14]);
  let v3 = __f32x4_of(samples[at + 5] + samples[at + 6], samples[at + 12] - samples[at + 11], samples[other + 5] + samples[other + 6], samples[other + 12] - samples[other + 11]);
  let v4 = __f32x4_of(-(samples[at + 7] + samples[at + 8]), samples[at + 9] - samples[at + 10], -(samples[other + 7] + samples[other + 8]), samples[other + 9] - samples[other + 10]);
  let v5 = __f32x4_of(samples[at + 9] + samples[at + 10], samples[at + 8] - samples[at + 7], samples[other + 9] + samples[other + 10], samples[other + 8] - samples[other + 7]);
  let v6 = __f32x4_of(-(samples[at + 11] + samples[at + 12]), samples[at + 5] - samples[at + 6], -(samples[other + 11] + samples[other + 12]), samples[other + 5] - samples[other + 6]);
  let v7 = __f32x4_of(samples[at + 13] + samples[at + 14], samples[at + 4] - samples[at + 3], samples[other + 13] + samples[other + 14], samples[other + 4] - samples[other + 3]);
  let v8 = __f32x4_of(-(samples[at + 15] + samples[at + 16]), samples[at + 1] - samples[at + 2], -(samples[other + 15] + samples[other + 16]), samples[other + 1] - samples[other + 2]);
  let s0 = v0;
  let s2 = v2;
  let s4 = v4;
  let s6 = v6;
  let s8 = v8;
  let t0 = s0 + s6 * __f32x4_splat(0.5f32);
  s0 -= s6;
  let t4 = (s4 + s2) * __f32x4_splat(0.93969262f32);
  let t2 = (s8 + s2) * __f32x4_splat(0.76604444f32);
  s6 = (s4 - s8) * __f32x4_splat(0.17364818f32);
  s4 += s8 - s2;
  s2 = s0 - s4 * __f32x4_splat(0.5f32);
  let out4 = s4 + s0;
  s8 = t0 - t2 + s6;
  s0 = t0 - t4 + t2;
  s4 = t0 + t4 - s6;
  let s1 = v1;
  let s3 = v3;
  let s5 = v5;
  let s7 = v7;
  s3 *= __f32x4_splat(0.86602540f32);
  t0 = (s5 + s1) * __f32x4_splat(0.98480775f32);
  t4 = (s5 - s7) * __f32x4_splat(0.34202014f32);
  t2 = (s1 + s7) * __f32x4_splat(0.64278761f32);
  s1 = (s1 - s5 - s7) * __f32x4_splat(0.86602540f32);
  s5 = t0 - s3 - t2;
  s7 = t4 - s3 - t0;
  s3 = t4 + s3 - t2;
  let (next_samples0, next_overlap0) = long_output(s4 - s7, samples, overlap, at, other, ov, ov_other, window, 0);
  samples = next_samples0;
  overlap = next_overlap0;
  let (next_samples1, next_overlap1) = long_output(s2 + s1, samples, overlap, at, other, ov, ov_other, window, 1);
  samples = next_samples1;
  overlap = next_overlap1;
  let (next_samples2, next_overlap2) = long_output(s0 - s3, samples, overlap, at, other, ov, ov_other, window, 2);
  samples = next_samples2;
  overlap = next_overlap2;
  let (next_samples3, next_overlap3) = long_output(s8 + s5, samples, overlap, at, other, ov, ov_other, window, 3);
  samples = next_samples3;
  overlap = next_overlap3;
  let (next_samples4, next_overlap4) = long_output(out4, samples, overlap, at, other, ov, ov_other, window, 4);
  samples = next_samples4;
  overlap = next_overlap4;
  let (next_samples5, next_overlap5) = long_output(s8 - s5, samples, overlap, at, other, ov, ov_other, window, 5);
  samples = next_samples5;
  overlap = next_overlap5;
  let (next_samples6, next_overlap6) = long_output(s0 + s3, samples, overlap, at, other, ov, ov_other, window, 6);
  samples = next_samples6;
  overlap = next_overlap6;
  let (next_samples7, next_overlap7) = long_output(s2 - s1, samples, overlap, at, other, ov, ov_other, window, 7);
  samples = next_samples7;
  overlap = next_overlap7;
  let (next_samples8, next_overlap8) = long_output(s4 + s7, samples, overlap, at, other, ov, ov_other, window, 8);
  samples = next_samples8;
  overlap = next_overlap8;
  return (samples, overlap);
}

@inline
function long_output(value: f32x4, own samples: f32[], own overlap: f32[], at: i32, other: i32, ov: i32, ov_other: i32, window: i32, i: i32): (f32[], f32[]) {
  let co = __f32x4_of(__f32x4_lane(value, 0), __f32x4_lane(value, 2), 0.0f32, 0.0f32);
  let si = __f32x4_of(__f32x4_lane(value, 1), __f32x4_lane(value, 3), 0.0f32, 0.0f32);
  if ((i & 1) != 0) { si = -si; }
  let old = __f32x4_of(overlap[ov + i], overlap[ov_other + i], 0.0f32, 0.0f32);
  let sum = co * __f32x4_splat(tables.G_TWID9[9 + i]) + si * __f32x4_splat(tables.G_TWID9[i]);
  let next = co * __f32x4_splat(tables.G_TWID9[i]) - si * __f32x4_splat(tables.G_TWID9[9 + i]);
  let left = old * __f32x4_splat(tables.G_MDCT_WINDOW[window + i]) - sum * __f32x4_splat(tables.G_MDCT_WINDOW[window + 9 + i]);
  let right = old * __f32x4_splat(tables.G_MDCT_WINDOW[window + 9 + i]) + sum * __f32x4_splat(tables.G_MDCT_WINDOW[window + i]);
  overlap = overlap.with(ov + i, __f32x4_lane(next, 0)).with(ov_other + i, __f32x4_lane(next, 1));
  samples = samples.with(at + i, __f32x4_lane(left, 0)).with(other + i, __f32x4_lane(left, 1));
  samples = samples.with(at + 17 - i, __f32x4_lane(right, 0)).with(other + 17 - i, __f32x4_lane(right, 1));
  return (samples, overlap);
}

function long_blocks(own samples: f32[], own overlap: f32[], base: i32, ov_at: i32, window: i32, count: i32): (f32[], f32[]) {
  let block = 0;
  while (block < count) {
    let other_block = if (block + 1 < count) { block + 1 } else { block };
    let at = base + block * 18;
    let other = base + other_block * 18;
    let ov = ov_at + block * 9;
    let ov_other = ov_at + other_block * 9;
    let (next_samples, next_overlap) = long_pair(samples, overlap, at, other, ov, ov_other, window);
    samples = next_samples;
    overlap = next_overlap;
    block += 2;
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
  let (result, _) = dct32_tail_into(samples, base, t);
  return result;
}

function dct32_tail_into(own samples: f32[], base: i32, own t: f32[]): (f32[], f32[]) {
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
  return (samples, t);
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

function dct32x4(own samples: f32[], base: i32, groups: i32): f32[] {
  let t = spectral.zeros(128);
<<four-lane DCT body>>
  return samples;
}

function dct32x4_into(own samples: f32[], base: i32, groups: i32, own t: f32[]): (f32[], f32[]) {
<<four-lane DCT body>>
  return (samples, t);
}

function pair4(z: f32[], at: i32): f32x4 {
  let a = (__f32x4_load(z, at + 14 * 64) - __f32x4_load(z, at)) * __f32x4_splat(29.0f32);
  a += (__f32x4_load(z, at + 64) + __f32x4_load(z, at + 13 * 64)) * __f32x4_splat(213.0f32);
  a += (__f32x4_load(z, at + 12 * 64) - __f32x4_load(z, at + 2 * 64)) * __f32x4_splat(459.0f32);
  a += (__f32x4_load(z, at + 3 * 64) + __f32x4_load(z, at + 11 * 64)) * __f32x4_splat(2037.0f32);
  a += (__f32x4_load(z, at + 10 * 64) - __f32x4_load(z, at + 4 * 64)) * __f32x4_splat(5153.0f32);
  a += (__f32x4_load(z, at + 5 * 64) + __f32x4_load(z, at + 9 * 64)) * __f32x4_splat(6574.0f32);
  a += (__f32x4_load(z, at + 8 * 64) - __f32x4_load(z, at + 6 * 64)) * __f32x4_splat(37489.0f32);
  a += __f32x4_load(z, at + 7 * 64) * __f32x4_splat(75038.0f32);
  let b = __f32x4_load(z, at + 14 * 64) * __f32x4_splat(104.0f32);
  b += __f32x4_load(z, at + 12 * 64) * __f32x4_splat(1567.0f32);
  b += __f32x4_load(z, at + 10 * 64) * __f32x4_splat(9727.0f32);
  b += __f32x4_load(z, at + 8 * 64) * __f32x4_splat(64019.0f32);
  b += __f32x4_load(z, at + 6 * 64) * __f32x4_splat(-9975.0f32);
  b += __f32x4_load(z, at + 4 * 64) * __f32x4_splat(-45.0f32);
  b += __f32x4_load(z, at + 2 * 64) * __f32x4_splat(146.0f32);
  b += __f32x4_load(z, at) * __f32x4_splat(-5.0f32);
  return __f32x4_of(__f32x4_lane(a, 0), __f32x4_lane(a, 1), __f32x4_lane(b, 2), __f32x4_lane(b, 3)) / __f32x4_splat(32768.0f32);
}

function synth(samples: [f32], base: i32, channels: i32, own lines: f32[], offset: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  let right = base + 576 * (channels - 1);
  let z = offset + 15 * 64;
  lines = lines.with(z + 60, samples[base + 18 * 16]).with(z + 61, samples[right + 18 * 16]).with(z + 62, samples[base]).with(z + 63, samples[right]);
  lines = lines.with(z + 124, samples[base + 1 + 18 * 16]).with(z + 125, samples[right + 1 + 18 * 16]).with(z + 126, samples[base + 1]).with(z + 127, samples[right + 1]);
  let first = pair4(lines, offset + 60);
  let second = pair4(lines, offset + 124);
  output = output.with(out_at, __f32x4_lane(first, 0)).with(out_at + 16 * channels, __f32x4_lane(first, 2)).with(out_at + 32 * channels, __f32x4_lane(second, 0)).with(out_at + 48 * channels, __f32x4_lane(second, 2));
  if (channels == 2) {
    output = output.with(out_at + 1, __f32x4_lane(first, 1)).with(out_at + 16 * channels + 1, __f32x4_lane(first, 3)).with(out_at + 32 * channels + 1, __f32x4_lane(second, 1)).with(out_at + 48 * channels + 1, __f32x4_lane(second, 3));
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

pub function granule(own qmf: f32[], own samples: f32[], channels: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  return granule_columns(qmf, samples, channels, 18, output, out_at);
}

function granule_columns(own qmf: f32[], own samples: f32[], channels: i32, columns: i32, own output: f32[], out_at: i32): (f32[], f32[]) {
  for ch in 0..channels {
    samples = dct32x4(samples, ch * 576, columns / 4);
    if (columns == 18) {
      samples = dct32_tail(samples, ch * 576);
    }
  }
  let lines = spectral.zeros((15 + columns) * 64);
<<polyphase synthesis body>>
  return (qmf, output);
}

// Seed 15 history columns followed by space for two MPEG-1 granules.
pub function frame_lines(history: [f32]): f32[] {
  let lines: f32[] = [];
  for sample in history {
    lines = lines.append(sample);
  }
  for _ in 0..36 * 64 {
    lines = lines.append(0.0f32);
  }
  return lines;
}

pub function frame_history(own history: f32[], lines: [f32], channels: i32): f32[] {
  for i in 0..960 {
    if (channels == 2 || (i & 1) == 0) {
      history = history.with(i, lines[36 * 64 + i]);
    }
  }
  return history;
}

// The caller seeds the first 960 lines with history once per Layer III frame.
// Adjacent granules share the trailing 15 columns directly.
pub function granule_at(own lines: f32[], offset: i32, own samples: f32[], channels: i32, own output: f32[], out_at: i32, own transform: f32[]): (f32[], f32[], f32[]) {
  for ch in 0..channels {
    let (four, scratch) = dct32x4_into(samples, ch * 576, 4, transform);
    let (tail, next) = dct32_tail_into(four, ch * 576, scratch);
    samples = tail;
    transform = next;
  }
  for block in 0..9 {
    let i = block * 2;
    let (next, pcm) = synth(samples, i, channels, lines, offset + i * 64, output, out_at + i * 32 * channels);
    lines = next;
    output = pcm;
  }
  return (lines, output, transform);
}

// Put the 960 history values at the start of the lines of one Layer II frame.
pub function seed_lines(own lines: f32[], history: [f32]): f32[] {
  for i in 0..15 * 64 {
    lines = lines.with(i, history[i]);
  }
  return lines;
}

// One Layer II granule of twelve columns. The three granules of a frame share 3,264 lines from seed_lines.
pub function granule12_at(own lines: f32[], offset: i32, own samples: f32[], channels: i32, own output: f32[], out_at: i32, own transform: f32[]): (f32[], f32[], f32[], f32[]) {
  for ch in 0..channels {
    let (values, scratch) = dct32x4_into(samples, ch * 576, 3, transform);
    samples = values;
    transform = scratch;
  }
  for block in 0..6 {
    let i = block * 2;
    let (next, pcm) = synth(samples, i, channels, lines, offset + i * 64, output, out_at + i * 32 * channels);
    lines = next;
    output = pcm;
  }
  return (lines, output, samples, transform);
}

function synthesize(own qmf: f32[], samples: [f32], channels: i32, columns: i32, own output: f32[], out_at: i32, own lines: f32[]): (f32[], f32[], f32[]) {
<<polyphase synthesis body>>
  return (qmf, output, lines);
}

// Reuse the scratch of one Layer I frame.
// Supply 1,728 line values and 128 transform values. Initialize line storage to zero before its first use.
pub function granule12_into(own qmf: f32[], own samples: f32[], channels: i32, own output: f32[], out_at: i32, own lines: f32[], own transform: f32[]): (f32[], f32[], f32[], f32[], f32[]) {
  for ch in 0..channels {
    let (values, scratch) = dct32x4_into(samples, ch * 576, 3, transform);
    samples = values;
    transform = scratch;
  }
  let (history, pcm, scratch) = synthesize(qmf, samples, channels, 12, output, out_at, lines);
  return (history, pcm, samples, scratch, transform);
}
```

## Shared transform body

Both transform entry points use the same arithmetic.
The MP3 entry point keeps its original allocation and return path.

```fern
<<four-lane DCT body>>=
for group in 0..groups {
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
```

## Shared synthesis body

Both synthesis entry points use the same arithmetic and history update.
The Layer II entry point returns its temporary arrays for reuse within one frame.

```fern
<<polyphase synthesis body>>=
for i in 0..15 * 64 {
  lines = lines.with(i, qmf[i]);
}
for block in 0..columns / 2 {
  let i = block * 2;
  let (next, pcm) = synth(samples, i, channels, lines, i * 64, output, out_at + 32 * channels * i);
  lines = next;
  output = pcm;
}
for i in 0..15 * 64 {
  if (channels == 2 || (i & 1) == 0) {
    qmf = qmf.with(i, lines[columns * 64 + i]);
  }
}
```
