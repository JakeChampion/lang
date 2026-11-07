# MP3 spectral reconstruction

This module reads side information, scalefactors, and Huffman symbols.
It reconstructs spectra and applies stereo and short-block reordering.
The public decoder validates the frame and output ranges before these helper calls.

The implementation derives from the scalar [minimp3 reference](https://github.com/lieff/minimp3/tree/ea99364f61c14656440e8d77e9c233ccf3124633).
The minimp3 authors dedicated that source to the public domain under [CC0](https://creativecommons.org/publicdomain/zero/1.0/).
The module executes Fern code and needs no external codec library.

```fern
<<*>>=
// Reconstruct MP3 spectra, stereo bands, and short blocks.
import "std/audio/pcm";
import "std/audio/mp3/header";
import "std/audio/mp3/bits";
import "std/audio/mp3/tables";

pub struct Granule { part: i32, big: i32, gain: i32, compress: i32, block: i32, mixed: boolean, long_bands: i32, short_bands: i32, row: i32, table0: i32, table1: i32, table2: i32, region0: i32, region1: i32, sub0: i32, sub1: i32, sub2: i32, pre: boolean, scale: i32, count1: i32, scfsi: i32 }

pub struct Side { begin: i32, granules: Granule[] }

pub struct Scales { bits: bits.Bits, scale: f32[], positions: i32[] }

pub function band(g: Granule, i: i32): i32 {
  if (i < 0 || i >= 40) {
    return 0;
  }
  if (g.short_bands == 0) {
    return if (i < 23) { tables.G_SCF_LONG[g.row * 23 + i] } else { 0 };
  }
  if (g.long_bands == 0) {
    return tables.G_SCF_SHORT[g.row * 40 + i];
  }
  if (i < g.long_bands) {
    return tables.G_SCF_LONG[g.row * 23 + i];
  }
  let short_index = i - g.long_bands + 9;
  return if (short_index < 40) { tables.G_SCF_SHORT[g.row * 40 + short_index] } else { 0 };
}

// Side information uses one bit cursor across version and block-type field variants.
// fern-lint: allow cyclomatic-complexity
pub function side_info(input: [u8], h: header.Header): Result[Side, pcm.AudioError] {
  let b = bits.new(input.len() * 8);
  let (b0, begin) = bits.read(input, b, if (h.version == 1) { 9 } else { 8 });
  b = b0;
  let private_bits = if (h.version == 1) { if (h.channels == 1) { 5 } else { 3 } } else { if (h.channels == 1) { 1 } else { 2 } };
  let (b1, _) = bits.read(input, b, private_bits);
  b = b1;
  let flags: i32[] = [0, 0];
  if (h.version == 1) {
    for c in 0..h.channels {
      let (next, v) = bits.read(input, b, 4);
      b = next;
      flags = flags.with(c, v);
    }
  }
  let count = h.channels * (if (h.version == 1) { 2 } else { 1 });
  let output: Granule[] = [];
  for index in 0..count {
    let (n0, part) = bits.read(input, b, 12);
    let (n1, big) = bits.read(input, n0, 9);
    let (n2, gain) = bits.read(input, n1, 8);
    let (n3, compress) = bits.read(input, n2, if (h.version == 1) { 4 } else { 9 });
    let (n4, switching) = bits.read(input, n3, 1);
    b = n4;
    if (big > 288) {
      return Err(pcm.Header);
    }
    let block = 0;
    let mixed = false;
    let longs = 22;
    let shorts = 0;
    let r0 = 0;
    let r1 = 0;
    let tab = 0;
    let sub0 = 0;
    let sub1 = 0;
    let sub2 = 0;
    if (switching != 0) {
      let (n5, kind) = bits.read(input, b, 2);
      let (n6, mix) = bits.read(input, n5, 1);
      let (n7, ts) = bits.read(input, n6, 10);
      let (n8, s0) = bits.read(input, n7, 3);
      let (n9, s1) = bits.read(input, n8, 3);
      let (n10, s2) = bits.read(input, n9, 3);
      b = n10;
      block = kind;
      mixed = mix != 0;
      tab = ts << 5;
      sub0 = s0;
      sub1 = s1;
      sub2 = s2;
      if (block == 0) {
        return Err(pcm.Header);
      }
      r0 = 7;
      r1 = 255;
      if (block == 2) {
        flags = flags.with(index % h.channels, 0);
        if (mixed) {
          longs = if (h.version == 1) { 8 } else { 6 };
          shorts = 30;
        } else {
          r0 = 8;
          longs = 0;
          shorts = 39;
        }
      }
    } else {
      let (n5, ts) = bits.read(input, b, 15);
      let (n6, a) = bits.read(input, n5, 4);
      let (n7, v) = bits.read(input, n6, 3);
      b = n7;
      tab = ts;
      r0 = a;
      r1 = v;
    }
    let pre = compress >= 500;
    if (h.version == 1) {
      let (next, v) = bits.read(input, b, 1);
      b = next;
      pre = v != 0;
    }
    let (n11, scale) = bits.read(input, b, 1);
    let (n12, count1) = bits.read(input, n11, 1);
    b = n12;
    let t0 = tab >> 10;
    let t1 = tab >> 5 & 31;
    let t2 = tab & 31;
    if (t0 == 4 || t0 == 14 || t1 == 4 || t1 == 14 || t2 == 4 || t2 == 14) {
      return Err(pcm.Header);
    }
    let row = h.rate_index - (if (h.rate_index != 0) { 1 } else { 0 });
    let scfsi = if (index < h.channels) { 0 } else { flags[index % h.channels] };
    output = output.append(Granule { part: part, big: big, gain: gain, compress: compress, block: block, mixed: mixed, long_bands: longs, short_bands: shorts, row: row, table0: t0, table1: t1, table2: t2, region0: r0, region1: r1, sub0: sub0, sub1: sub1, sub2: sub2, pre: pre, scale: scale, count1: count1, scfsi: scfsi });
  }
  if (b.bad || b.pos != b.limit) {
    return Err(pcm.Header);
  }
  return Ok(Side { begin: begin, granules: output });
}

pub function zeros(n: i32): f32[] {
  let output: f32[] = [];
  for _ in 0..n {
    output = output.append(0.0f32);
  }
  return output;
}

pub function ldexp_q2(own value: f32, own exponent: i32): f32 {
  while (true) {
    let e = if (exponent < 120) { exponent } else { 120 };
    value *= tables.G_EXPFRAC[e & 3] * ((1 << 30 >> (e >> 2)) as f32);
    exponent -= e;
    if (exponent <= 0) {
      return value;
    }
  }
  return value;
}

// Scalefactor reconstruction preserves the reference partition and reuse order.
// fern-lint: allow cyclomatic-complexity
pub function scales(input: [u8], b: bits.Bits, g: Granule, h: header.Header, positions: i32[], channel: i32): Result[Scales, pcm.AudioError] {
  let row = (if (g.short_bands != 0) { 1 } else { 0 }) + (if (g.long_bands == 0) { 1 } else { 0 });
  let offset = 0;
  let sizes: i32[] = [0, 0, 0, 0];
  let flags = g.scfsi;
  if (h.version == 1) {
    let part = tables.G_SCFC_DECODE[g.compress];
    sizes = [part >> 2, part >> 2, part & 3, part & 3];
  } else {
    let intensity = header.intensity(h) && channel != 0;
    let sfc = g.compress >> (if (intensity) { 1 } else { 0 });
    let k = if (intensity) { 12 } else { 0 };
    while (sfc >= 0) {
      let product = 1;
      let i = 3;
      while (i >= 0) {
        if (k + i >= tables.G_MOD.len()) {
          return Err(pcm.Header);
        }
        sizes = sizes.with(i, sfc / product % tables.G_MOD[k + i]);
        product *= tables.G_MOD[k + i];
        i -= 1;
      }
      sfc -= product;
      k += 4;
    }
    offset = k;
    flags = -16;
  }
  let raw: i32[] = [];
  let at = 0;
  for group in 0..4 {
    let count = tables.G_SCF_PARTITIONS[row * 28 + offset + group];
    if (count == 0) {
      break;
    }
    if (at + count > 39) {
      return Err(pcm.Header);
    }
    let width = sizes[group];
    for _ in 0..count {
      let value = 0;
      if ((flags & 8) != 0) {
        value = positions[at];
      } else {
        let (next, v) = bits.read(input, b, width);
        b = next;
        value = v;
        let maximum = if (flags < 0 && width != 0) { (1 << width) - 1 } else { -1 };
        positions = positions.with(at, if (value == maximum) { 255 } else { value });
      }
      raw = raw.append(value);
      at += 1;
    }
    flags *= 2;
  }
  for _ in 0..3 {
    raw = raw.append(0);
  }
  let shift = g.scale + 1;
  if (g.short_bands != 0) {
    for i in 0..g.short_bands {
      let sub = if (i % 3 == 0) { g.sub0 } else if (i % 3 == 1) { g.sub1 } else { g.sub2 };
      raw = raw.with(g.long_bands + i, raw[g.long_bands + i] + (sub << 3 - shift));
    }
  } else if (g.pre) {
    for i in 0..10 {
      raw = raw.with(11 + i, raw[11 + i] + tables.G_PREAMP[i]);
    }
  }
  let gain_exp = g.gain - 214 - (if (header.midside(h)) { 2 } else { 0 });
  let gain = ldexp_q2(2048.0f32, 44 - gain_exp);
  let scale: f32[] = [];
  for i in 0..g.long_bands + g.short_bands {
    scale = scale.append(ldexp_q2(gain, raw[i] << shift));
  }
  if (b.bad) {
    return Err(pcm.Truncated);
  }
  return Ok(Scales { bits: b, scale: scale, positions: positions });
}

function pow43(own x: i32): f32 {
  if (x < 129) {
    return tables.G_POW43[16 + x];
  }
  let mult = 256.0f32;
  if (x < 1024) {
    mult = 16.0f32;
    x = x << 3;
  }
  let sign = 2 * x & 64;
  let frac = ((x & 63) - sign) as f32 / (((x & ~63) + sign) as f32);
  return tables.G_POW43[16 + (x + sign >> 6)] * (1.0f32 + frac * (4.0f32 / 3.0f32 + frac * (2.0f32 / 9.0f32))) * mult;
}

// Huffman traversal shares bit, band, and output cursors across variable-length symbols.
// fern-lint: allow cyclomatic-complexity
pub function huffman(input: [u8], own b: bits.Bits, g: Granule, scale: [f32], end: i32, own output: f32[], base: i32): Result[(bits.Bits, f32[]), pcm.AudioError] {
  let band_index = 0;
  let band_end = band(g, 0);
  let at = 0;
  for _ in 0..g.big {
    while (at >= band_end) {
      band_index += 1;
      let n = band(g, band_index);
      if (n == 0) {
        return Err(pcm.Header);
      }
      band_end += n;
    }
    let table = if (band_index <= g.region0) { g.table0 } else if (band_index <= g.region0 + g.region1 + 1) { g.table1 } else { g.table2 };
    let start = tables.TABINDEX[table];
    let width = 5;
    let index = start + bits.peek(input, b.pos, width) as i32;
    let leaf = tables.TABS[index];
    let steps = 0;
    while (leaf < 0) {
      let (next, _) = bits.read(input, b, width);
      b = next;
      width = leaf & 7;
      index = start + bits.peek(input, b.pos, width) as i32 - (leaf >> 3);
      if (index < 0 || index >= tables.TABS.len() || steps > 32 || b.bad) {
        return Err(pcm.Header);
      }
      leaf = tables.TABS[index];
      steps += 1;
    }
    let (next, _) = bits.read(input, b, leaf >> 8);
    b = next;
    let gain = scale[band_index];
    for _ in 0..2 {
      let value = leaf & 15;
      if (value == 15 && tables.G_LINBITS[table] != 0) {
        let (next1, low) = bits.read(input, b, tables.G_LINBITS[table]);
        b = next1;
        value += low;
      }
      let sign = 0;
      if (value != 0) {
        let (next2, v) = bits.read(input, b, 1);
        b = next2;
        sign = v;
      }
      let sample = if (sign != 0) { -pow43(value) } else { pow43(value) };
      output = output.with(base + at, gain * sample);
      at += 1;
      leaf = leaf >> 4;
    }
    if (b.bad || b.pos > end) {
      return Err(pcm.Header);
    }
  }
  while (at + 4 <= 576) {
    let leaf = if (g.count1 != 0) { tables.TAB33[bits.peek(input, b.pos, 4) as i32] } else { tables.TAB32[bits.peek(input, b.pos, 4) as i32] };
    if ((leaf & 8) == 0) {
      let width = leaf & 3;
      let index = (leaf >> 3) + (bits.peek(input, b.pos, 4 + width) as i32 & (1 << width) - 1);
      leaf = if (g.count1 != 0) { tables.TAB33[index] } else { tables.TAB32[index] };
    }
    let next_pos = b.pos + (leaf & 7);
    if (next_pos > end) {
      break;
    }
    b = bits.Bits { ...b, pos: next_pos };
    for i in 0..4 {
      while (at >= band_end) {
        band_index += 1;
        let n = band(g, band_index);
        if (n == 0) {
          return Ok((bits.Bits { ...b, pos: end }, output));
        }
        band_end += n;
      }
      if ((leaf & 128 >> i) != 0) {
        let (next, sign) = bits.read(input, b, 1);
        b = next;
        output = output.with(base + at, if (sign != 0) { -scale[band_index] } else { scale[band_index] });
      }
      at += 1;
    }
    if (b.bad) {
      return Err(pcm.Header);
    }
  }
  return Ok((bits.Bits { ...b, pos: end }, output));
}

// Intensity and mid-side reconstruction share the spectral band traversal.
// fern-lint: allow cyclomatic-complexity
pub function stereo(own samples: f32[], own positions: i32[], g: Granule, right: Granule, h: header.Header): (f32[], i32[]) {
  if (!header.intensity(h)) {
    if (header.midside(h)) {
      for i in 0..576 {
        let a = samples[i];
        let b = samples[576 + i];
        samples = samples.with(i, a + b).with(576 + i, a - b);
      }
    }
    return (samples, positions);
  }
  let count = g.long_bands + g.short_bands;
  let top: i32[] = [-1, -1, -1];
  let at = 0;
  for i in 0..count {
    for k in 0..band(g, i) {
      if (samples[576 + at + k] != 0.0f32) {
        top = top.with(i % 3, i);
        break;
      }
    }
    at += band(g, i);
  }
  if (g.long_bands != 0) {
    let max = top[0];
    if (top[1] > max) {
      max = top[1];
    }
    if (top[2] > max) {
      max = top[2];
    }
    top = [max, max, max];
  }
  let blocks = if (g.short_bands != 0) { 3 } else { 1 };
  for i in 0..blocks {
    let last = count - blocks + i;
    let previous = last - blocks;
    positions = positions.with(last, if (top[i] >= previous) { if (h.version == 1) { 3 } else { 0 } } else { positions[previous] });
  }
  at = 0;
  for i in 0..count {
    let pos = positions[i];
    let n = band(g, i);
    if (i > top[i % 3] && pos < (if (h.version == 1) { 7 } else { 64 })) {
      let kl = 1.0f32;
      let kr = 1.0f32;
      if (h.version == 1) {
        kl = tables.G_PAN[2 * pos];
        kr = tables.G_PAN[2 * pos + 1];
      } else {
        kr = ldexp_q2(1.0f32, pos + 1 >> 1 << (right.compress & 1));
        if ((pos & 1) != 0) {
          kl = kr;
          kr = 1.0f32;
        }
      }
      let scale = if (header.midside(h)) { 1.41421356f32 } else { 1.0f32 };
      kl *= scale;
      kr *= scale;
      for k in 0..n {
        let v = samples[at + k];
        samples = samples.with(at + k, v * kl).with(576 + at + k, v * kr);
      }
    } else if (header.midside(h)) {
      for k in 0..n {
        let a = samples[at + k];
        let b = samples[576 + at + k];
        samples = samples.with(at + k, a + b).with(576 + at + k, a - b);
      }
    }
    at += n;
  }
  return (samples, positions);
}

pub function reorder(own samples: f32[], base: i32, g: Granule, first_band: i32): f32[] {
  let scratch: f32[] = [];
  let source = base;
  let i = first_band;
  while (band(g, i) != 0) {
    let n = band(g, i);
    for k in 0..n {
      scratch = scratch.append(samples[source + k]).append(samples[source + n + k]).append(samples[source + 2 * n + k]);
    }
    source += 3 * n;
    i += 3;
  }
  for k in 0..scratch.len() {
    samples = samples.with(base + k, scratch[k]);
  }
  return samples;
}
```
