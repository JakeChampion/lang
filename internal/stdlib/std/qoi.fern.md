# QOI images

This module encodes and decodes images in the Quite OK Image format.

## Model

A QOI file is a header of 14 bytes, a stream of operations, and an end marker of eight bytes. The header gives the width, the height, the channels (3 or 4) and the color space. Each operation gives one or more pixels from the pixel before it:

- a run of the pixel before, of 1 to 62 pixels,
- an index into a table of 64 pixels that the stream saw, at the position of a hash of the pixel,
- a small difference of each of red, green and blue,
- a difference of green, and the differences of red and blue from it,
- a full RGB pixel, or a full RGBA pixel.

The first pixel before the image is black, with an alpha of 255.

## Contracts

`encode_rgb8` and `encode_rgba8` accept a width and a height from 1, and pixels with three or four bytes each. Other dimensions give `InvalidDimensions`, and pixels of a different length give `InvalidBufferLength`. The encoder writes the color space sRGB.

`decode_image` gives the pixels as RGBA, with an alpha of 255 for a file of three channels. It refuses a file with more pixels than `MAX_PIXELS` before it allocates for the image. A file that ends before its pixels gives `Truncated`.

## Executable example

```fern test name=std-qoi-example
import "std/qoi";

function main(): i32 {
  let px: u8[] = [255, 0, 0, 255, 255, 0, 0, 255, 0, 0, 255, 128];
  let Ok(file) = qoi.encode_rgba8(3, 1, px) else { return 1; };
  let Ok(img) = qoi.decode_image(file) else { return 2; };
  if (img.width != 3 || img.channels != 4 || img.pixels != px) { return 3; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module encodes and decodes QOI images.

<<images and errors>>

<<encoding>>

<<decoding>>
```

### `images and errors`

```fern
<<images and errors>>=
// An image of `width` by `height` pixels, row by row: red, green, blue and
// alpha for each pixel. `channels` is what the file gave: 3 or 4.
pub struct Image { width: i32, height: i32, channels: i32, pixels: u8[] }

pub enum EncodeError { InvalidDimensions, InvalidBufferLength }

pub function (e: EncodeError) message(): string {
  match (e) {
    InvalidDimensions => {
      return "qoi: the width and the height must be 1 or more";
    },
    InvalidBufferLength => {
      return "qoi: the length of the pixels does not agree with the dimensions";
    }
  }
}

pub enum DecodeError { NotQoi, BadHeader, TooLarge, Truncated }

pub function (e: DecodeError) message(): string {
  match (e) {
    NotQoi => {
      return "qoi: not a QOI file";
    },
    BadHeader => {
      return "qoi: the header is not valid";
    },
    TooLarge => {
      return "qoi: the image has more pixels than the limit";
    },
    Truncated => {
      return "qoi: the data ends early";
    }
  }
}

// The most pixels that the four bytes of each pixel of an image can hold.
pub const MAX_PIXELS: i64 = 536870911i64;

// The position of a pixel in the table of 64 pixels.
function slot(r: i32, g: i32, b: i32, a: i32): i32 {
  return (r * 3 + g * 5 + b * 7 + a * 11) % 64;
}

// The difference `v` of two bytes as a signed byte: -128 to 127.
function wrapped(v: i32): i32 {
  let d = v & 255;
  return if (d > 127) { d - 256 } else { d };
}
```

### `encoding`

The encoder and the decoder keep each pixel as one word: red, green, blue and alpha in its bytes.

```fern
<<encoding>>=
// The QOI file of `pixels`, which have red, green and blue for each pixel.
pub function encode_rgb8(width: i32, height: i32, pixels: [u8]): Result[u8[], EncodeError] {
  return encode(width, height, pixels, 3);
}

// The QOI file of `pixels`, which have red, green, blue and alpha for each
// pixel.
pub function encode_rgba8(width: i32, height: i32, pixels: [u8]): Result[u8[], EncodeError] {
  return encode(width, height, pixels, 4);
}

function word(r: i32, g: i32, b: i32, a: i32): i32 {
  return r << 24 | g << 16 | b << 8 | a;
}

function put_u32(own out: u8[], v: i32): u8[] {
  return out.append((v >> 24 & 255) as u8).append((v >> 16 & 255) as u8).append((v >> 8 & 255) as u8).append((v & 255) as u8);
}

function checked(width: i32, height: i32, px: [u8], bpp: i32): Option[EncodeError] {
  if (width <= 0 || height <= 0 || width as i64 * (height as i64) > MAX_PIXELS) {
    return Some(InvalidDimensions);
  }
  if (px.len() as i64 != width as i64 * (height as i64) * (bpp as i64)) {
    return Some(InvalidBufferLength);
  }
  return None;
}

// The word of pixel `i`.
function pixel_at(px: [u8], i: i32, bpp: i32): i32 {
  let at = i * bpp;
  let a = if (bpp == 4) { px[at + 3] as i32 } else { 255 };
  return word(px[at] as i32, px[at + 1] as i32, px[at + 2] as i32, a);
}

function slot_of(w: i32): i32 {
  return slot(w >> 24 & 255, w >> 16 & 255, w >> 8 & 255, w & 255);
}

function in_range(v: i32, lo: i32, hi: i32): boolean {
  return v >= lo && v <= hi;
}

// The operation for the pixel `w` after `prev`, when the table does not
// hold it: a difference, a luma difference, or the full pixel.
function changed_op(own out: u8[], w: i32, prev: i32): u8[] {
  if ((w & 255) != (prev & 255)) {
    return put_u32(out.append(255u8), w);
  }
  let dr = wrapped((w >> 24) - (prev >> 24));
  let dg = wrapped((w >> 16) - (prev >> 16));
  let db = wrapped((w >> 8) - (prev >> 8));
  if (in_range(dr, -2, 1) && in_range(dg, -2, 1) && in_range(db, -2, 1)) {
    return out.append((64 | dr + 2 << 4 | dg + 2 << 2 | db + 2) as u8);
  }
  if (in_range(dg, -32, 31) && in_range(dr - dg, -8, 7) && in_range(db - dg, -8, 7)) {
    return out.append((128 | dg + 32) as u8).append((dr - dg + 8 << 4 | db - dg + 8) as u8);
  }
  return out.append(254u8).append((w >> 24 & 255) as u8).append((w >> 16 & 255) as u8).append((w >> 8 & 255) as u8);
}

function encode(width: i32, height: i32, px: [u8], bpp: i32): Result[u8[], EncodeError] {
  if let Some(e) = checked(width, height, px, bpp) {
    return Err(e);
  }
  let n = width * height;
  let out: u8[] = [113u8, 111u8, 105u8, 102u8];
  out = put_u32(put_u32(out, width), height).append(bpp as u8).append(0u8);
  let table = __alloc_i32(64);
  let prev = word(0, 0, 0, 255);
  let run = 0;
  for i in 0..n {
    let w = pixel_at(px, i, bpp);
    if (w == prev) {
      run += 1;
      if (run == 62 || i == n - 1) {
        out = out.append((192 | run - 1) as u8);
        run = 0;
      }
      continue;
    }
    if (run > 0) {
      out = out.append((192 | run - 1) as u8);
      run = 0;
    }
    let k = slot_of(w);
    if (table[k] == w) {
      out = out.append(k as u8);
    } else {
      table = table.with(k, w);
      out = changed_op(out, w, prev);
    }
    prev = w;
  }
  for z in 0..7 {
    out = out.append(0u8);
  }
  return Ok(out.append(1u8));
}
```

### `decoding`

```fern
<<decoding>>=
function get_u32(b: [u8], at: i32): i64 {
  return b[at] as i64 << 24i64 | b[at + 1] as i64 << 16i64 | b[at + 2] as i64 << 8i64 | b[at + 3] as i64;
}

// The file starts with the signature "qoif" and holds a header.
function is_qoi(data: [u8]): boolean {
  return data.len() >= 14 && data[0] == 113u8 && data[1] == 111u8 && data[2] == 105u8 && data[3] == 102u8;
}

// The width, the height and the channels of the header of `data`.
function header(data: [u8]): Result[(i32, i32, i32), DecodeError] {
  if (!is_qoi(data)) {
    return Err(NotQoi);
  }
  let w = get_u32(data, 4);
  let h = get_u32(data, 8);
  let channels = data[12] as i32;
  if (w == 0i64 || h == 0i64 || channels != 3 && channels != 4 || data[13] > 1u8) {
    return Err(BadHeader);
  }
  if (w > MAX_PIXELS / h) {
    return Err(TooLarge);
  }
  return Ok((w as i32, h as i32, channels));
}

// The bytes after the operation byte `op`.
function operands(op: i32): i32 {
  if (op == 254) {
    return 3;
  }
  if (op == 255) {
    return 4;
  }
  return if (op >> 6 == 2) { 1 } else { 0 };
}

// The pixel that the operation at `p` gives after the pixel `w`. A run gives
// the pixel before.
function next_word(data: [u8], p: i32, op: i32, w: i32, table: [i32]): i32 {
  if (op == 254) {
    return word(data[p] as i32, data[p + 1] as i32, data[p + 2] as i32, w & 255);
  }
  if (op == 255) {
    return word(data[p] as i32, data[p + 1] as i32, data[p + 2] as i32, data[p + 3] as i32);
  }
  let r = w >> 24 & 255;
  let g = w >> 16 & 255;
  let b = w >> 8 & 255;
  let kind = op >> 6;
  if (kind == 0) {
    return table[op];
  }
  if (kind == 1) {
    return word(r + (op >> 4 & 3) - 2 & 255, g + (op >> 2 & 3) - 2 & 255, b + (op & 3) - 2 & 255, w & 255);
  }
  if (kind == 2) {
    let dg = (op & 63) - 32;
    let next = data[p] as i32;
    return word(r + dg + (next >> 4) - 8 & 255, g + dg & 255, b + dg + (next & 15) - 8 & 255, w & 255);
  }
  return w;
}

// The image in the QOI file `data`, or an error.
pub function decode_image(data: [u8]): Result[Image, DecodeError] {
  let (width, height, channels) = match (header(data)) {
    Ok(h) => h,
    Err(e) => {
      return Err(e);
    },
  };
  let n = width * height;
  let out = __alloc_u8(n * 4);
  let table = __alloc_i32(64);
  let w = word(0, 0, 0, 255);
  let p = 14;
  let i = 0;
  while (i < n) {
    if (p >= data.len() || p + 1 + operands(data[p] as i32) > data.len()) {
      return Err(Truncated);
    }
    let op = data[p] as i32;
    w = next_word(data, p + 1, op, w, table);
    p += 1 + operands(op);
    table = table.with(slot_of(w), w);
    let run = if (op >= 192 && op < 254) { (op & 63) + 1 } else { 1 };
    let end = if (i + run > n) { n } else { i + run };
    while (i < end) {
      out = out.with(i * 4, (w >> 24 & 255) as u8).with(i * 4 + 1, (w >> 16 & 255) as u8).with(i * 4 + 2, (w >> 8 & 255) as u8).with(i * 4 + 3, (w & 255) as u8);
      i += 1;
    }
  }
  return Ok(Image { width: width, height: height, channels: channels, pixels: out });
}
```
