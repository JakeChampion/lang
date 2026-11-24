# GIF images

This module encodes GIF images, with one frame or with an animation.

## Model

A GIF file holds a palette of at most 256 colors. Each frame holds an index into the palette for each pixel. The encoder makes one palette for all the frames, by median cut. It divides the colors of the pixels into boxes, and the color of each box is the mean of its pixels. With `dither`, the encoder spreads the error of each pixel to its neighbors, as Floyd and Steinberg do. A pixel with an alpha below 128 takes a transparent index.

The indices of each frame go through LZW compression with codes of 3 to 12 bits. A file with more than one frame has the NETSCAPE2.0 extension, so it loops for ever.

## Contracts

The encoder accepts a width and a height from 1 to 65535. Each frame has RGBA pixels with four bytes each. Other dimensions give `InvalidDimensions`, and a frame with a different length gives `InvalidBufferLength`. No frames, or a count of delays that is not the count of frames, gives `NoFrames`. A count of colors below 2 is 2, and above 256 is 256. The delay of a frame is in milliseconds, and the file keeps it in hundredths of a second.

## Executable example

```fern test name=std-gif-example
import "std/gif";

function main(): i32 {
  let px: u8[] = [255, 0, 0, 255, 0, 0, 255, 255];
  let Ok(file) = gif.encode_rgba8(2, 1, px, gif.options()) else { return 1; };
  if (file[0] != 71u8 || file[file.len() - 1] != 59u8) { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module encodes GIF images.
import "core/cmp";
import "core/error";
import "std/i32";

<<frames and errors>>

<<the palette>>

<<the indices>>

<<LZW>>

<<the file>>
```

### `frames and errors`

```fern
<<frames and errors>>=
// The colors of the palette, from 2 to 256, and the error diffusion.
pub struct Options { colors: i32, dither: boolean }

// 256 colors with dithering.
pub function options(): Options {
  return Options { colors: 256, dither: true };
}

pub enum EncodeError { InvalidDimensions, InvalidBufferLength, NoFrames }

pub function (e: EncodeError) message(): string {
  match (e) {
    InvalidDimensions => {
      return "gif: the width and the height must be from 1 to 65535";
    },
    InvalidBufferLength => {
      return "gif: the length of the pixels of a frame does not agree with the dimensions";
    },
    NoFrames => {
      return "gif: an image needs one frame or more, and one delay for each frame";
    }
  }
}

pub function (e: EncodeError) to_string(): string {
  return e.message();
}

impl error.Error for EncodeError {}

impl cmp.Display for EncodeError {}

// The GIF file of one frame of RGBA pixels.
pub function encode_rgba8(width: i32, height: i32, pixels: u8[], opts: Options): Result[u8[], EncodeError] {
  return encode_frames(width, height, [pixels], [0], opts);
}

// The GIF file of the frames, RGBA pixels each, which loops for ever when
// there are two or more. Each frame shows for its delay in milliseconds.
pub function encode_frames(width: i32, height: i32, frames: [u8[]], delays_ms: [i32], opts: Options): Result[u8[], EncodeError] {
  if let Some(e) = checked(width, height, frames, delays_ms) {
    return Err(e);
  }
  let colors = opts.colors.max(2).min(256);
  let clear = has_transparency(frames);
  let palette = median_cut(histogram(frames), if (clear) { colors - 1 } else { colors });
  let count = palette.len() / 3 + (if (clear) { 1 } else { 0 });
  let bits = 1;
  while (1 << bits < count) {
    bits += 1;
  }
  let out = header(width, height, palette, bits, frames.len() > 1);
  let transparent = if (clear) { palette.len() / 3 } else { -1 };
  let cache = __alloc_i32(32768);
  for i in 0..frames.len() {
    let (indices, next_cache) = indexed(frames[i], width, height, palette, transparent, opts.dither, cache);
    cache = next_cache;
    out = frame(out, width, height, delays_ms[i], transparent, if (bits < 2) { 2 } else { bits }, indices);
  }
  return Ok(out.append(59u8));
}

function checked(width: i32, height: i32, frames: [u8[]], delays_ms: [i32]): Option[EncodeError] {
  if (width <= 0 || height <= 0 || width > 65535 || height > 65535) {
    return Some(InvalidDimensions);
  }
  if (frames.len() == 0 || delays_ms.len() != frames.len()) {
    return Some(NoFrames);
  }
  let n = width as i64 * (height as i64) * 4i64;
  for f in frames {
    if (f.len() as i64 != n) {
      return Some(InvalidBufferLength);
    }
  }
  return None;
}

function has_transparency(frames: [u8[]]): boolean {
  for px in frames {
    let i = 3;
    while (i < px.len()) {
      if (px[i] < 128u8) {
        return true;
      }
      i += 4;
    }
  }
  return false;
}
```

### `the palette`

A cell of the histogram has 5 bits of each channel. The histogram counts the pixels of each cell and keeps the sums of their channels. Thus the color of a box is the mean of its pixels, not of its cells. A box is a range of cells on each axis. The encoder splits the box with the most pixels that has more than one cell. The cut is on the longest axis of the box, where half of its pixels are on each side.

```fern
<<the palette>>=
// The count and the sums of red, green and blue of the opaque pixels of each
// cell.
struct Histogram { count: i64[], r: i64[], g: i64[], b: i64[] }

function cell(r: i32, g: i32, b: i32): i32 {
  return r >> 3 << 10 | g >> 3 << 5 | b >> 3;
}

function histogram(frames: [u8[]]): Histogram {
  let count = __alloc_i64(32768);
  let rs = __alloc_i64(32768);
  let gs = __alloc_i64(32768);
  let bs = __alloc_i64(32768);
  for px in frames {
    let i = 0;
    while (i + 3 < px.len()) {
      if (px[i + 3] >= 128u8) {
        let r = px[i] as i32;
        let g = px[i + 1] as i32;
        let b = px[i + 2] as i32;
        let c = cell(r, g, b);
        count = count.with(c, count[c] + 1i64);
        rs = rs.with(c, rs[c] + r as i64);
        gs = gs.with(c, gs[c] + g as i64);
        bs = bs.with(c, bs[c] + b as i64);
      }
      i += 4;
    }
  }
  return Histogram { count: count, r: rs, g: gs, b: bs };
}

// A box of cells: the first and the last cell on each axis.
struct Box { lo: i32[], hi: i32[] }

// The pixels of the box, and the smallest box that holds them.
function measured(h: Histogram, b: Box): (i64, Box) {
  let total = 0i64;
  let r0 = 31;
  let r1 = 0;
  let g0 = 31;
  let g1 = 0;
  let b0 = 31;
  let b1 = 0;
  for r in b.lo[0]..b.hi[0] + 1 {
    for g in b.lo[1]..b.hi[1] + 1 {
      for bl in b.lo[2]..b.hi[2] + 1 {
        let n = h.count[r << 10 | g << 5 | bl];
        if (n > 0i64) {
          total += n;
          r0 = r0.min(r);
          r1 = r1.max(r);
          g0 = g0.min(g);
          g1 = g1.max(g);
          b0 = b0.min(bl);
          b1 = b1.max(bl);
        }
      }
    }
  }
  if (total == 0i64) {
    return (0i64, b);
  }
  return (total, Box { lo: [r0, g0, b0], hi: [r1, g1, b1] });
}

// The pixels of the box on each plane of `axis`, from its low cell.
function planes(h: Histogram, b: Box, axis: i32): i64[] {
  let out = __alloc_i64(b.hi[axis] - b.lo[axis] + 1);
  for r in b.lo[0]..b.hi[0] + 1 {
    for g in b.lo[1]..b.hi[1] + 1 {
      for bl in b.lo[2]..b.hi[2] + 1 {
        let k = if (axis == 0) { r } else if (axis == 1) { g } else { bl };
        k -= b.lo[axis];
        out = out.with(k, out[k] + h.count[r << 10 | g << 5 | bl]);
      }
    }
  }
  return out;
}

// The box with the most pixels that has more than one cell, or -1.
function widest(boxes: [Box], totals: [i64]): i32 {
  let pick = -1;
  for i in 0..boxes.len() {
    let b = boxes[i];
    let wide = b.hi[0] > b.lo[0] || b.hi[1] > b.lo[1] || b.hi[2] > b.lo[2];
    if (wide && (pick < 0 || totals[i] > totals[pick])) {
      pick = i;
    }
  }
  return pick;
}

function longest_axis(b: Box): i32 {
  let axis = 0;
  for k in 1..3 {
    if (b.hi[k] - b.lo[k] > b.hi[axis] - b.lo[axis]) {
      axis = k;
    }
  }
  return axis;
}

// The planes of the low part of a cut: the first planes that hold at most
// half of `total`, and one plane at least.
function low_planes(ps: [i64], total: i64): i32 {
  let half = 0i64;
  let cut = 0;
  while (cut < ps.len() - 1 && (half + ps[cut]) * 2i64 <= total) {
    half += ps[cut];
    cut += 1;
  }
  return cut.max(1);
}

// The mean color of the pixels of the box, or none for an empty box.
function mean(h: Histogram, b: Box): u8[] {
  let n = 0i64;
  let sr = 0i64;
  let sg = 0i64;
  let sb = 0i64;
  for r in b.lo[0]..b.hi[0] + 1 {
    for g in b.lo[1]..b.hi[1] + 1 {
      for bl in b.lo[2]..b.hi[2] + 1 {
        let c = r << 10 | g << 5 | bl;
        n += h.count[c];
        sr += h.r[c];
        sg += h.g[c];
        sb += h.b[c];
      }
    }
  }
  if (n == 0i64) {
    return [];
  }
  return [((sr + n / 2i64) / n) as u8, ((sg + n / 2i64) / n) as u8, ((sb + n / 2i64) / n) as u8];
}

// The palette of at most `most` colors, three bytes each.
function median_cut(h: Histogram, most: i32): u8[] {
  let (first_total, first) = measured(h, Box { lo: [0, 0, 0], hi: [31, 31, 31] });
  if (first_total == 0i64) {
    return [0u8, 0u8, 0u8];
  }
  let boxes: Box[] = [first];
  let totals: i64[] = [first_total];
  while (boxes.len() < most) {
    let pick = widest(boxes, totals);
    if (pick < 0) {
      break;
    }
    let b = boxes[pick];
    let axis = longest_axis(b);
    let cut = low_planes(planes(h, b, axis), totals[pick]);
    let (ta, a) = measured(h, Box { lo: b.lo, hi: b.hi.with(axis, b.lo[axis] + cut - 1) });
    let (tb, c) = measured(h, Box { lo: b.lo.with(axis, b.lo[axis] + cut), hi: b.hi });
    boxes = boxes.with(pick, a).append(c);
    totals = totals.with(pick, ta).append(tb);
  }
  let out: u8[] = [];
  for b in boxes {
    for v in mean(h, b) {
      out = out.append(v);
    }
  }
  return out;
}
```

### `the indices`

A cache keeps the nearest color of the palette for each cell. Thus the search over the palette runs one time for each cell that the image uses. When `dither` is true, the encoder keeps the error of the row and of the next row for each channel.

```fern
<<the indices>>=
function nearest(palette: [u8], r: i32, g: i32, b: i32): i32 {
  let best = 0;
  let best_d = 2147483647;
  for i in 0..palette.len() / 3 {
    let dr = palette[i * 3] as i32 - r;
    let dg = palette[i * 3 + 1] as i32 - g;
    let db = palette[i * 3 + 2] as i32 - b;
    let d = dr * dr * 2 + dg * dg * 4 + db * db * 3;
    if (d < best_d) {
      best_d = d;
      best = i;
    }
  }
  return best;
}

function byte(v: i32): i32 {
  if (v < 0) {
    return 0;
  }
  if (v > 255) {
    return 255;
  }
  return v;
}

// The index of each pixel. A value of the cache is the color of the palette
// nearest to the middle of a cell, plus one, and 0 for a cell that the search
// did not see yet.
function indexed(px: [u8], width: i32, height: i32, palette: [u8], transparent: i32, dither: boolean, own cache: i32[]): (u8[], i32[]) {
  let out = __alloc_u8(width * height);
  let err = __alloc_i32((width + 2) * 6);
  for y in 0..height {
    let cur = (y & 1) * (width + 2) * 3;
    let nxt = (width + 2) * 3 - cur;
    for i in 0..(width + 2) * 3 {
      err = err.with(nxt + i, 0);
    }
    for x in 0..width {
      let at = (y * width + x) * 4;
      if (transparent >= 0 && px[at + 3] < 128u8) {
        out = out.with(y * width + x, transparent as u8);
        continue;
      }
      let e = cur + (x + 1) * 3;
      let r = px[at] as i32;
      let g = px[at + 1] as i32;
      let b = px[at + 2] as i32;
      if (dither) {
        r = byte(r + (err[e] + 8 >> 4));
        g = byte(g + (err[e + 1] + 8 >> 4));
        b = byte(b + (err[e + 2] + 8 >> 4));
      }
      let c = cell(r, g, b);
      let k = cache[c] - 1;
      if (k < 0) {
        k = nearest(palette, (r >> 3 << 3) + 4, (g >> 3 << 3) + 4, (b >> 3 << 3) + 4);
        cache = cache.with(c, k + 1);
      }
      out = out.with(y * width + x, k as u8);
      if (dither) {
        let qs = [r - palette[k * 3] as i32, g - palette[k * 3 + 1] as i32, b - palette[k * 3 + 2] as i32];
        for ch in 0..3 {
          let q = qs[ch];
          err = err.with(e + 3 + ch, err[e + 3 + ch] + q * 7);
          let below = nxt + (x + 1) * 3 + ch;
          err = err.with(below - 3, err[below - 3] + q * 3).with(below, err[below] + q * 5).with(below + 3, err[below + 3] + q);
        }
      }
    }
  }
  return (out, cache);
}
```

### `LZW`

The codes follow the LZW writer of Go, which `image/gif` uses. `hi` is the last code that the table holds. The codes get one bit wider when `hi` reaches `overflow`. The writer sends a clear code when the table is full. A key is a code and the next index. The table keeps the code of a key with the generation of the table, so a clear needs no pass over the table.

```fern
<<LZW>>=
const MAX_CODE: i32 = 4095;

struct Bits { out: u8[], acc: i32, n: i32 }

function emit(own b: Bits, code: i32, width: i32): Bits {
  let acc = b.acc | code << b.n;
  let n = b.n + width;
  let out = b.out;
  while (n >= 8) {
    out = out.append((acc & 255) as u8);
    acc >>= 8;
    n -= 8;
  }
  return Bits { out: out, acc: acc, n: n };
}

// The LZW codes of `indices`, with `lit` bits for a literal, as bytes.
function lzw(indices: [u8], lit: i32): u8[] {
  let clear = 1 << lit;
  let eof = clear + 1;
  let width = lit + 1;
  let hi = eof;
  let overflow = 1 << width;
  let codes = __alloc_i32(4096 * 256);
  let stamps = __alloc_i32(4096 * 256);
  let generation = 1;
  let bits = emit(Bits { out: [], acc: 0, n: 0 }, clear, width);
  let saved = indices[0] as i32;
  for i in 1..indices.len() {
    let c = indices[i] as i32;
    let key = saved * 256 + c;
    if (stamps[key] == generation) {
      saved = codes[key];
      continue;
    }
    bits = emit(bits, saved, width);
    saved = c;
    hi += 1;
    if (hi == overflow) {
      width += 1;
      overflow <<= 1;
    }
    if (hi == MAX_CODE) {
      bits = emit(bits, clear, width);
      width = lit + 1;
      hi = eof;
      overflow = 1 << width;
      generation += 1;
      continue;
    }
    codes = codes.with(key, hi);
    stamps = stamps.with(key, generation);
  }
  bits = emit(bits, saved, width);
  hi += 1;
  if (hi == overflow) {
    width += 1;
  }
  bits = emit(bits, eof, width);
  let out = bits.out;
  if (bits.n > 0) {
    out = out.append((bits.acc & 255) as u8);
  }
  return out;
}
```

### `the file`

```fern
<<the file>>=
function le16(own out: u8[], v: i32): u8[] {
  return out.append((v & 255) as u8).append((v >> 8 & 255) as u8);
}

// The header, the screen, the global palette of 2^bits colors, and the loop
// extension of an animation.
function header(width: i32, height: i32, palette: [u8], bits: i32, animated: boolean): u8[] {
  let out: u8[] = [71u8, 73u8, 70u8, 56u8, 57u8, 97u8];
  out = le16(le16(out, width), height);
  out = out.append((128 | 112 | bits - 1) as u8).append(0u8).append(0u8);
  for i in 0..(1 << bits) * 3 {
    out = out.append(if (i < palette.len()) { palette[i] } else { 0u8 });
  }
  if (animated) {
    out = out.append(33u8).append(255u8).append(11u8);
    for c in "NETSCAPE2.0".as_bytes() {
      out = out.append(c);
    }
    out = le16(out.append(3u8).append(1u8), 0).append(0u8);
  }
  return out;
}

// A frame: its control extension, its descriptor, and its LZW data in blocks
// of at most 255 bytes. A frame with transparent pixels clears to the
// background before the next frame.
function frame(own out: u8[], width: i32, height: i32, delay_ms: i32, transparent: i32, lit: i32, indices: [u8]): u8[] {
  let flags = if (transparent >= 0) { 2 << 2 | 1 } else { 1 << 2 };
  out = le16(out.append(33u8).append(249u8).append(4u8).append(flags as u8), (delay_ms + 5) / 10);
  out = out.append(if (transparent >= 0) { transparent as u8 } else { 0u8 }).append(0u8);
  out = le16(le16(le16(le16(out.append(44u8), 0), 0), width), height).append(0u8);
  out = out.append(lit as u8);
  let data = lzw(indices, lit);
  let at = 0;
  while (at < data.len()) {
    let n = if (data.len() - at > 255) { 255 } else { data.len() - at };
    out = out.append(n as u8);
    for k in at..at + n {
      out = out.append(data[k]);
    }
    at += n;
  }
  return out.append(0u8);
}
```
