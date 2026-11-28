# Drawing on an Android frame

This module draws rectangles, images and text on the pixels of an Android window.

## Model

A `Frame` holds the pixels of a window while the window is locked: 4 bytes for each pixel, in the order red, green, blue and alpha, and `stride` pixels for each row. `std/android` locks a window and gives its frame. The functions here only write into the pixels, so a test can make a frame from plain memory.

A color is a pixel word with an alpha from 0, for none, to 255, for opaque: `rgba` makes one, and `rgb` makes an opaque one. Each shape and each glyph blends its color over the pixels of the frame. The weight is the alpha times the part of each pixel that the shape covers. A shape over opaque pixels leaves them opaque.

Over transparent pixels, a shape gives premultiplied pixels: each channel holds its value times the alpha. So a frame from `offscreen` that starts transparent holds a premultiplied image, which a GPU can draw over other pixels. `store` writes pixel words as they are, so it can make transparent pixels, for example the corners of an icon.

An `Image` holds 4 bytes for each pixel, red, green, blue and alpha, row by row with no stride. `std/android/image` decodes one, and `image` copies a part of it into a frame. `cover_size` gives the size of an image scaled to fill a box with its shape kept. The box then shows the middle of the image. `clip_corners` rounds the corners of an opaque image on a flat background.

`Lch` is a color in the OKLCH space: a lightness from 0 to 1, a chroma, and a hue. Equal steps of lightness look equal, so a theme can make its colors from one hue. `lch_of` and `color_of` change a pixel word to it and back. `key_color` gives the color that an image shows most, for example the color of an album cover.

Text uses the Fern raster of DejaVu Sans Mono in `std/android/font`. Each glyph pixel has a coverage from 0 to 15, and the text color blends over the frame by that coverage. `scale` draws each raster pixel as a square of `scale` by `scale` pixels.

## Contracts

The pixels of a frame are valid until the window is posted. A rectangle or text outside the frame is not drawn.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<frames>>

<<images>>

<<colors>>

<<text>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module draws rectangles, discs, rings and text on the pixels of an
// Android window.
import "std/android/font";
import "std/f64";
import "std/i64";
import "std/string";
import "std/utf8";
```

### `frames`

```fern
<<frames>>=
pub struct Frame { window: usize, buffer: usize, bits: usize, width: i32, height: i32, stride: i32 }

// The pixel word of an opaque color.
pub function rgb(r: i32, g: i32, b: i32): i32 {
  return rgba(r, g, b, 255);
}

// The pixel word of a color with the alpha `a`, from 0 for none to 255 for
// opaque.
pub function rgba(r: i32, g: i32, b: i32, a: i32): i32 {
  return (a & 255) << 24 | (b & 255) << 16 | (g & 255) << 8 | r & 255;
}

// The pixel word of `color` drawn over the pixel word `under`. It is opaque
// when `under` is opaque.
pub function over(color: i32, under: i32): i32 {
  return blend(color, under, weight(color, 15));
}

function alpha_of(color: i32): i32 {
  return color >> 24 & 255;
}

// The weight from 0 to 255 of `color` on a pixel that it covers by `a`, from
// 0 to 15.
function weight(color: i32, a: i32): i32 {
  return a * 17 * alpha_of(color) / 255;
}

function clamp(v: i32, lo: i32, hi: i32): i32 {
  if (v < lo) {
    return lo;
  }
  if (v > hi) {
    return hi;
  }
  return v;
}

// Fills the rectangle at x and y, of width w and height h, with `color`
// blended over the frame. The part outside the frame is not drawn.
pub function (f: Frame) fill(x: i32, y: i32, w: i32, h: i32, color: i32): void {
  if (alpha_of(color) == 255) {
    f.store(x, y, w, h, color);
    return;
  }
  let x0 = clamp(x, 0, f.width);
  let x1 = clamp(x + w, 0, f.width);
  for row in clamp(y, 0, f.height)..clamp(y + h, 0, f.height) {
    cover_row(f.bits + ((row * f.stride + x0) * 4) as usize, x1 - x0, color);
  }
}

// `blend` of `color` over each of the `n` pixels at `to`, with the alpha of
// `color` as the weight w. A step does 4 pixels on lanes of 16 bits: each
// channel is `(s * w + d * (255 - w)) / 255`, with the alpha of `color` as
// 255.
function cover_row(to: usize, n: i32, color: i32): void {
  let w = alpha_of(color);
  let r = (color & 255) * w;
  let g = (color >> 8 & 255) * w;
  let b = (color >> 16 & 255) * w;
  let part = __u16x8_of(r, g, b, 255 * w, r, g, b, 255 * w);
  let keep = __u16x8_splat(255 - w);
  unsafe {
    for i in 0..n / 4 {
      let at = to + (i * 16) as usize;
      let d = __u8x16_load_ptr(at);
      let lo = by_255(__u8x16_widen_lo(d) * keep + part);
      let hi = by_255(__u8x16_widen_hi(d) * keep + part);
      __u8x16_store(at, __u16x8_narrow(lo, hi));
    }
    for i in n / 4 * 4..n {
      let at = to + (i * 4) as usize;
      __store_i32(at, blend(color, __load_i32(at), w));
    }
  }
}

// Each lane of `x` divided by 255, for a lane up to 255 * 255.
function by_255(x: u16x8): u16x8 {
  return __u16x8_shr(x + __u16x8_shr(x, 8) + __u16x8_splat(1), 8);
}

// Writes the pixel word `color` into each pixel of the rectangle at x and y,
// of width w and height h, with no blend. The part outside the frame is not
// written.
pub function (f: Frame) store(x: i32, y: i32, w: i32, h: i32, color: i32): void {
  let x0 = clamp(x, 0, f.width);
  let y0 = clamp(y, 0, f.height);
  let x1 = clamp(x + w, 0, f.width);
  let y1 = clamp(y + h, 0, f.height);
  if (x1 <= x0 || y1 <= y0) {
    return;
  }
  unsafe {
    let first = f.bits + ((y0 * f.stride + x0) * 4) as usize;
    for i in 0..x1 - x0 {
      __store_i32(first + (i * 4) as usize, color);
    }
    for row in y0 + 1..y1 {
      __memcpy(f.bits + ((row * f.stride + x0) * 4) as usize, first, (x1 - x0) * 4);
    }
  }
}

// Blends `color` over the pixel at x and y, which it covers by `a`, from 0
// for none to 255 for all of it. A pixel outside the frame is not drawn.
pub function (f: Frame) cover(x: i32, y: i32, color: i32, a: i32): void {
  if (a <= 0 || x < 0 || y < 0 || x >= f.width || y >= f.height) {
    return;
  }
  unsafe {
    let at = f.bits + ((y * f.stride + x) * 4) as usize;
    __store_i32(at, blend(color, __load_i32(at), clamp(a, 0, 255) * alpha_of(color) / 255));
  }
}

// A frame of w by h pixels in plain memory, for a layer that a program
// draws once and copies to the window with `blit`. Free it with `release`.
pub function offscreen(w: i32, h: i32): Frame {
  unsafe {
    let n = w * h * 4;
    if (w <= 0 || h <= 0) {
      return Frame { window: 0 as usize, buffer: 0 as usize, bits: 0 as usize, width: 0, height: 0, stride: 0 };
    }
    let bits = __alloc(n);
    return Frame { window: 0 as usize, buffer: 0 as usize, bits: bits, width: w, height: h, stride: w };
  }
}

// Frees a frame that `offscreen` gave. Do not give it the frame of a
// window.
pub function release(f: Frame): void {
  if (f.bits == 0 as usize || f.window != 0 as usize) {
    return;
  }
  unsafe {
    __free(f.bits, f.stride * f.height * 4);
  }
}

// Copies `src` with its top left corner at x and y, with no blend. The part
// outside the frame is not written.
pub function (f: Frame) blit(src: Frame, x: i32, y: i32): void {
  let x0 = clamp(x, 0, f.width);
  let x1 = clamp(x + src.width, 0, f.width);
  let y0 = clamp(y, 0, f.height);
  let y1 = clamp(y + src.height, 0, f.height);
  if (x1 <= x0 || y1 <= y0) {
    return;
  }
  unsafe {
    for row in y0..y1 {
      __memcpy(f.bits + ((row * f.stride + x0) * 4) as usize, src.bits + (((row - y) * src.stride + x0 - x) * 4) as usize, (x1 - x0) * 4);
    }
  }
}

// The pixel word of the opaque `src` over the premultiplied `dst` with the
// weight w, from 0 for none to 256 for all of `src`. Red and blue go in one
// 64-bit product.
function mix_word(src: i32, dst: i32, w: i32): i32 {
  let k = w as i64;
  let srb = (src & 16711935) as i64;
  let drb = (dst & 16711935) as i64;
  let rb = srb * k + drb * (256i64 - k) >> 8 & 16711935i64;
  let g = (src & 65280) * w + (dst & 65280) * (256 - w) >> 8 & 65280;
  let a = 255 * w + (dst >> 24 & 255) * (256 - w) >> 8;
  return a << 24 | rb as i32 | g;
}

// `mix_word` for each of the `n` pixels from `from` over the pixels at `to`.
// A step does 4 pixels on lanes of 16 bits: each channel is
// `(s * w + d * (256 - w)) >> 8`, with the alpha of `src` as 255.
function mix_row(to: usize, from: usize, n: i32, w: i32): void {
  let opaque = __u16x8_of(0, 0, 0, 255, 0, 0, 0, 255);
  let k = __u16x8_splat(w);
  let keep = __u16x8_splat(256 - w);
  unsafe {
    for i in 0..n / 4 {
      let at = (i * 16) as usize;
      let s = __u8x16_load_ptr(from + at);
      let d = __u8x16_load_ptr(to + at);
      let lo = __u16x8_or(__u8x16_widen_lo(s), opaque) * k + __u8x16_widen_lo(d) * keep;
      let hi = __u16x8_or(__u8x16_widen_hi(s), opaque) * k + __u8x16_widen_hi(d) * keep;
      __u8x16_store(to + at, __u16x8_narrow(__u16x8_shr(lo, 8), __u16x8_shr(hi, 8)));
    }
    for i in n / 4 * 4..n {
      let at = (i * 4) as usize;
      __store_i32(to + at, mix_word(__load_i32(from + at), __load_i32(to + at), w));
    }
  }
}

// Blends `src` over the frame with its top left corner at x and y, with the
// weight `alpha` from 0 to 255 for each pixel.
pub function (f: Frame) blend_frame(src: Frame, x: i32, y: i32, alpha: i32): void {
  if (alpha >= 255) {
    f.blit(src, x, y);
    return;
  }
  let w = clamp(alpha, 0, 255);
  w += w >> 7;
  let x0 = clamp(x, 0, f.width);
  let x1 = clamp(x + src.width, 0, f.width);
  let y0 = clamp(y, 0, f.height);
  let y1 = clamp(y + src.height, 0, f.height);
  if (x1 <= x0 || y1 <= y0 || w == 0) {
    return;
  }
  for row in y0..y1 {
    let to = f.bits + ((row * f.stride + x0) * 4) as usize;
    let from = src.bits + (((row - y) * src.stride + x0 - x) * 4) as usize;
    mix_row(to, from, x1 - x0, w);
  }
}

// Blends `color` over the box of w by h at x and y, by the coverage of each
// pixel in `cov`, row by row, from 0 for none to 255 for all of it. Text
// that a program draws once into a coverage map goes on the frame this way.
pub function (f: Frame) mask(x: i32, y: i32, w: i32, h: i32, cov: [u8], color: i32): void {
  let ca = alpha_of(color);
  let x0 = clamp(x, 0, f.width);
  let x1 = clamp(x + w, 0, f.width);
  let y0 = clamp(y, 0, f.height);
  let y1 = clamp(y + h, 0, f.height);
  if (x1 <= x0 || y1 <= y0 || cov.len() < w * h || ca == 0) {
    return;
  }
  for row in y0..y1 {
    mask_row(f.bits + ((row * f.stride + x0) * 4) as usize, cov, (row - y) * w + x0 - x, x1 - x0, color);
  }
}

// `mix_word` of `color` over each of the `n` pixels at `to`, by the coverage
// in `cov` from the index `from`. The weight of a coverage a is k + (k >> 7),
// with k = (a * ca * 257 + 32768) >> 16 for the alpha ca of `color`. It is
// 256 for a k of 255, so the pixel takes `color`. A step does 16 pixels on
// lanes of 16 bits, and k is (p + (p >> 8) + 128) >> 8 for p = a * ca.
function mask_row(to: usize, cov: [u8], from: i32, n: i32, color: i32): void {
  let ca = alpha_of(color);
  let r = color & 255;
  let g = color >> 8 & 255;
  let b = color >> 16 & 255;
  let part = __u16x8_of(r, g, b, 255, r, g, b, 255);
  let alpha = __u16x8_splat(ca);
  let half = __u16x8_splat(128);
  let all = __u16x8_splat(256);
  let four = __u8x16_splat(4 as u8);
  let first = __u8x16_of(0 as u8, 0 as u8, 0 as u8, 0 as u8, 1 as u8, 1 as u8, 1 as u8, 1 as u8, 2 as u8, 2 as u8, 2 as u8, 2 as u8, 3 as u8, 3 as u8, 3 as u8, 3 as u8);
  unsafe {
    for j in 0..n / 16 {
      let c = __u8x16_load_view(cov, from + j * 16);
      let pick = first;
      for q in 0..4 {
        let at = to + (j * 64 + q * 16) as usize;
        let a = __u8x16_swizzle(c, pick);
        pick += four;
        let d = __u8x16_load_ptr(at);
        let p = __u8x16_widen_lo(a) * alpha;
        let k = __u16x8_shr(p + __u16x8_shr(p, 8) + half, 8);
        let w = k + __u16x8_shr(k, 7);
        let lo = __u16x8_shr(part * w + __u8x16_widen_lo(d) * (all - w), 8);
        p = __u8x16_widen_hi(a) * alpha;
        k = __u16x8_shr(p + __u16x8_shr(p, 8) + half, 8);
        w = k + __u16x8_shr(k, 7);
        let hi = __u16x8_shr(part * w + __u8x16_widen_hi(d) * (all - w), 8);
        __u8x16_store(at, __u16x8_narrow(lo, hi));
      }
    }
    for i in n / 16 * 16..n {
      let at = to + (i * 4) as usize;
      let k = cov[from + i] as i32 * ca * 257 + 32768 >> 16;
      __store_i32(at, mix_word(color, __load_i32(at), k + (k >> 7)));
    }
  }
}

// Draws the `w` by `h` premultiplied RGBA pixels of `rgba` over the frame,
// with their top left at (x, y).
pub function (f: Frame) blend(x: i32, y: i32, w: i32, h: i32, rgba: [u8]): void {
  let x0 = clamp(x, 0, f.width);
  let x1 = clamp(x + w, 0, f.width);
  let y0 = clamp(y, 0, f.height);
  let y1 = clamp(y + h, 0, f.height);
  if (x1 <= x0 || y1 <= y0 || rgba.len() < 4 * w * h) {
    return;
  }
  for row in y0..y1 {
    blend_row(f.bits + ((row * f.stride + x0) * 4) as usize, rgba, 4 * ((row - y) * w + x0 - x), x1 - x0);
  }
}

// `premultiplied_over` of each of the `n` pixels of `rgba` from the index
// `from` over the pixels at `to`. A pixel with an alpha of 0 leaves the pixel
// under it. A step does 4 pixels on lanes of 16 bits: each channel is
// `v + (d * (255 - a) + 127) / 255`, held to 255.
function blend_row(to: usize, rgba: [u8], from: i32, n: i32): void {
  let alphas = __u8x16_of(3 as u8, 3 as u8, 3 as u8, 3 as u8, 7 as u8, 7 as u8, 7 as u8, 7 as u8, 11 as u8, 11 as u8, 11 as u8, 11 as u8, 15 as u8, 15 as u8, 15 as u8, 15 as u8);
  let full = __u8x16_splat(255 as u8);
  let none = __u8x16_splat(0 as u8);
  let half = __u16x8_splat(127);
  unsafe {
    for i in 0..n / 4 {
      let at = to + (i * 16) as usize;
      let v = __u8x16_load_view(rgba, from + i * 16);
      let a = __u8x16_swizzle(v, alphas);
      let keep = full - a;
      let d = __u8x16_load_ptr(at);
      let lo = by_255(__u8x16_widen_lo(d) * __u8x16_widen_lo(keep) + half) + __u8x16_widen_lo(v);
      let hi = by_255(__u8x16_widen_hi(d) * __u8x16_widen_hi(keep) + half) + __u8x16_widen_hi(v);
      __u8x16_store(at, __u8x16_select(d, __u16x8_narrow(lo, hi), __u8x16_eq(a, none)));
    }
    for i in n / 4 * 4..n {
      let k = from + 4 * i;
      let a = rgba[k + 3] as i32;
      if (a != 0) {
        let at = to + (i * 4) as usize;
        __store_i32(at, premultiplied_over(rgba[k] as i32, rgba[k + 1] as i32, rgba[k + 2] as i32, a, __load_i32(at)));
      }
    }
  }
}

// The premultiplied color (r, g, b, a) over the pixel word `under`.
function premultiplied_over(r: i32, g: i32, b: i32, a: i32, under: i32): i32 {
  let keep = 255 - a;
  let part = (v: i32, shift: i32): i32 => (v + ((under >> shift & 255) * keep + 127) / 255).min(255);
  return rgba(part(r, 0), part(g, 8), part(b, 16), part(a, 24));
}

// The pixel word at x and y, or 0 outside the frame.
pub function (f: Frame) pixel(x: i32, y: i32): i32 {
  if (x < 0 || y < 0 || x >= f.width || y >= f.height) {
    return 0;
  }
  unsafe {
    return __load_i32(f.bits + ((y * f.stride + x) * 4) as usize);
  }
}

// The coverage from 0 to 15 of a pixel whose centre is `d` pixels from the
// centre of a disc of radius `r`. The edge is one pixel wide.
function cover(d: f64, r: f64): i32 {
  let c = r - d + 0.5;
  if (c <= 0.0) {
    return 0;
  }
  if (c >= 1.0) {
    return 15;
  }
  return (c * 15.0) as i32;
}

// Blends `color` over the pixel at x and y with the coverage `a`.
function put(f: Frame, x: i32, y: i32, color: i32, a: i32): void {
  if (a <= 0 || x < 0 || y < 0 || x >= f.width || y >= f.height) {
    return;
  }
  unsafe {
    let at = f.bits + ((y * f.stride + x) * 4) as usize;
    __store_i32(at, blend(color, __load_i32(at), weight(color, a)));
  }
}

// Fills the disc of radius `r` around cx and cy. Each pixel of the edge
// takes the part of `color` that the disc covers.
pub function (f: Frame) disc(cx: i32, cy: i32, r: i32, color: i32): void {
  f.ring(cx, cy, r, r, color);
}

// Fills the band of `width` pixels inside the circle of radius `r` around cx
// and cy, with smooth edges.
pub function (f: Frame) ring(cx: i32, cy: i32, r: i32, width: i32, color: i32): void {
  if (r <= 0 || width <= 0 || alpha_of(color) == 0) {
    return;
  }
  let outer = r as f64;
  let inner = outer - width as f64;
  // Squared radii separate the solid runs from the two antialiased edges.
  let full = (outer - 0.5) * (outer - 0.5);
  let hole = if (inner < 0.5) { -1.0 } else { (inner - 0.5) * (inner - 0.5) };
  let band = if (inner < 0.0) { -1.0 } else { (inner + 0.5) * (inner + 0.5) };
  let edge = (outer + 0.5) * (outer + 0.5);
  let x0 = (cx as i64 - r as i64).clamp(0i64, f.width as i64) as i32;
  let x1 = (cx as i64 + r as i64).clamp(0i64, f.width as i64) as i32;
  let y0 = (cy as i64 - r as i64).clamp(0i64, f.height as i64) as i32;
  let y1 = (cy as i64 + r as i64).clamp(0i64, f.height as i64) as i32;
  for y in y0..y1 {
    let dy = y as f64 + 0.5 - cy as f64;
    let start = x0;
    for x in x0..x1 {
      let dx = x as f64 + 0.5 - cx as f64;
      let squared = dx * dx + dy * dy;
      if (squared < band || squared > full) {
        if (x > start) {
          f.fill(start, y, x - start, 1, color);
        }
        if (squared > hole && squared < edge) {
          let d = squared.sqrt();
          put(f, x, y, color, cover(d, outer) - cover(d, inner));
        }
        start = x + 1;
      }
    }
    if (x1 > start) {
      f.fill(start, y, x1 - start, 1, color);
    }
  }
}

// Fills the rectangle at x and y, of width w and height h, with its corners
// rounded to radius `r` and smooth edges. A radius of half the height makes
// a pill.
pub function (f: Frame) round_rect(x: i32, y: i32, w: i32, h: i32, r0: i32, color: i32): void {
  if (alpha_of(color) == 0) {
    return;
  }
  let r = r0;
  if (r > w / 2) {
    r = w / 2;
  }
  if (r > h / 2) {
    r = h / 2;
  }
  f.fill(x + r, y, w - 2 * r, h, color);
  f.fill(x, y + r, r, h - 2 * r, color);
  f.fill(x + w - r, y + r, r, h - 2 * r, color);
  corner(f, x + r, y + r, x, y, r, color, false);
  corner(f, x + w - r, y + r, x + w - r, y, r, color, false);
  corner(f, x + r, y + h - r, x, y + h - r, r, color, false);
  corner(f, x + w - r, y + h - r, x + w - r, y + h - r, r, color, false);
}

// Paints the part inside a quarter circle, or its complement for clipping
// an image. Each row has at most one solid span and one antialiased edge.
function corner(f: Frame, cx: i32, cy: i32, x0: i32, y0: i32, r: i32, color: i32, outside: boolean): void {
  let x1 = (x0 as i64 + r as i64).clamp(0i64, f.width as i64) as i32;
  let y1 = (y0 as i64 + r as i64).clamp(0i64, f.height as i64) as i32;
  let left = clamp(x0, 0, f.width);
  if (left >= x1) {
    return;
  }
  let radius = r as f64;
  let inner = (radius - 0.5) * (radius - 0.5);
  let outer = (radius + 0.5) * (radius + 0.5);
  for y in clamp(y0, 0, f.height)..y1 {
    let dy = y as f64 + 0.5 - cy as f64;
    let start = left;
    for x in left..x1 {
      let dx = x as f64 + 0.5 - cx as f64;
      let squared = dx * dx + dy * dy;
      let solid = if (outside) { squared >= outer } else { squared <= inner };
      if (!solid) {
        if (start < x) {
          f.fill(start, y, x - start, 1, color);
        }
        if (squared > inner && squared < outer) {
          let a = cover(squared.sqrt(), radius);
          put(f, x, y, color, if (outside) { 15 - a } else { a });
        }
        start = x + 1;
      }
    }
    if (start < x1) {
      f.fill(start, y, x1 - start, 1, color);
    }
  }
}
```

### `images`

`image` copies rows with no blend, so an image overwrites the pixels under it, transparent pixels included.

```fern
<<images>>=
// An image of `width` by `height` pixels, row by row: red, green, blue and
// alpha for each pixel.
pub struct Image { width: i32, height: i32, pixels: u8[] }

// The pixel word of pixel x, y of `img`, or 0 outside it.
pub function (img: Image) pixel(x: i32, y: i32): i32 {
  if (x < 0 || y < 0 || x >= img.width || y >= img.height) {
    return 0;
  }
  let at = (y * img.width + x) * 4;
  return rgba(img.pixels[at] as i32, img.pixels[at + 1] as i32, img.pixels[at + 2] as i32, img.pixels[at + 3] as i32);
}

// `img` at w by h pixels. Each pixel takes the four pixels of `img` nearest
// to its centre, weighed by how near they are.
pub function (img: Image) scaled(w: i32, h: i32): Image {
  if (w <= 0 || h <= 0 || img.width <= 0 || img.height <= 0 || img.pixels.len() < img.width * img.height * 4) {
    return Image { width: 0, height: 0, pixels: [] };
  }
  let px = img.pixels;
  let sx = img.width as f64 / (w as f64);
  let sy = img.height as f64 / (h as f64);
  let cols = __alloc_i32(2 * w);
  let weights = __alloc_f64(w);
  for x in 0..w {
    let (x0, x1, tx) = nearest(x, sx, img.width);
    cols = cols.with(2 * x, x0 * 4).with(2 * x + 1, x1 * 4);
    weights = weights.with(x, tx);
  }
  let half = __f64x2_splat(0.5);
  // 2^52: a whole number from 0 to 255 plus it has that number in its low byte.
  let point = __f64x2_splat(4503599627370496.0);
  let out = __alloc_u8(w * h * 4);
  for y in 0..h {
    let (y0, y1, ty) = nearest(y, sy, img.height);
    let up = y0 * img.width * 4;
    let down = y1 * img.width * 4;
    let tyv = __f64x2_splat(ty);
    for x in 0..w {
      let txv = __f64x2_splat(weights[x]);
      let a = up + cols[2 * x];
      let b = up + cols[2 * x + 1];
      let c = down + cols[2 * x];
      let d = down + cols[2 * x + 1];
      // Two channels in each step, each lane with the operations of one.
      // A channel is from 0 to 255.5, so its floor is the cast to u8.
      for k in [0, 2] {
        let pa = __f64x2_of(px[a + k] as f64, px[a + k + 1] as f64);
        let pc = __f64x2_of(px[c + k] as f64, px[c + k + 1] as f64);
        let top = pa + (__f64x2_of(px[b + k] as f64, px[b + k + 1] as f64) - pa) * txv;
        let bottom = pc + (__f64x2_of(px[d + k] as f64, px[d + k + 1] as f64) - pc) * txv;
        let v = __f64x2_floor(top + (bottom - top) * tyv + half) + point;
        let at = (y * w + x) * 4 + k;
        out = out.with(at, (__f64x2_lane_bits(v, 0) & 255u64) as u8).with(at + 1, (__f64x2_lane_bits(v, 1) & 255u64) as u8);
      }
    }
  }
  return Image { width: w, height: h, pixels: out };
}

// The two source pixels, of `n`, nearest to the centre of pixel `i` at
// scale `s`, and the weight of the second.
function nearest(i: i32, s: f64, n: i32): (i32, i32, f64) {
  let f = (i as f64 + 0.5) * s - 0.5;
  let i0 = clamp(f.floor() as i32, 0, n - 1);
  let i1 = clamp(i0 + 1, 0, n - 1);
  return (i0, i1, (f - i0 as f64).max(0.0));
}

// The size of an image of w0 by h0 scaled to cover a box of w by h, with its
// shape kept: one side is the side of the box, and the other is at least as
// long.
pub function cover_size(w0: i32, h0: i32, w: i32, h: i32): (i32, i32) {
  if (w0 <= 0 || h0 <= 0) {
    return (w, h);
  }
  if (w0 as i64 * (h as i64) >= h0 as i64 * (w as i64)) {
    return (((w0 as i64 * (h as i64) + h0 as i64 - 1i64) / (h0 as i64)) as i32, h);
  }
  return (w, ((h0 as i64 * (w as i64) + w0 as i64 - 1i64) / (w0 as i64)) as i32);
}

// Copies the middle w by h part of `img` to x and y. A box larger than the
// image keeps the frame where the image does not reach. The part outside the
// frame is not written.
pub function (f: Frame) image(x: i32, y: i32, w: i32, h: i32, img: Image): void {
  let cw = w;
  if (cw > img.width) {
    cw = img.width;
  }
  let ch = h;
  if (ch > img.height) {
    ch = img.height;
  }
  let sx = (img.width - cw) / 2;
  let sy = (img.height - ch) / 2;
  let dx = x + (w - cw) / 2;
  let dy = y + (h - ch) / 2;
  let x0 = clamp(dx, 0, f.width);
  let x1 = clamp(dx + cw, 0, f.width);
  let y0 = clamp(dy, 0, f.height);
  let y1 = clamp(dy + ch, 0, f.height);
  if (x1 <= x0 || y1 <= y0 || img.width as i64 * (img.height as i64) > (img.pixels.len() / 4) as i64) {
    return;
  }
  // An image and a frame hold the bytes of a pixel in the same order, so each
  // row is a copy.
  let n = (x1 - x0) * 4;
  if (x1 - x0 == img.width && x1 - x0 == f.stride) {
    let from = (sy + y0 - dy) * n;
    let bytes = (y1 - y0) * n;
    unsafe {
      __memcpy(f.bits + (y0 * n) as usize, __bytes_data(img.pixels[from:from + bytes]), bytes);
    }
    return;
  }
  for row in y0..y1 {
    let from = ((sy + row - dy) * img.width + sx + x0 - dx) * 4;
    unsafe {
      __memcpy(f.bits + ((row * f.stride + x0) * 4) as usize, __bytes_data(img.pixels[from:from + n]), n);
    }
  }
}

// Paints `bg` over the corners of the box at x and y, of width w and height
// h, outside the quarter circles of radius `r`, with smooth edges. Over an
// opaque image it gives the image rounded corners.
pub function (f: Frame) clip_corners(x: i32, y: i32, w: i32, h: i32, r: i32, bg: i32): void {
  if (alpha_of(bg) == 0) {
    return;
  }
  corner(f, x + r, y + r, x, y, r, bg, true);
  corner(f, x + w - r, y + r, x + w - r, y, r, bg, true);
  corner(f, x + r, y + h - r, x, y + h - r, r, bg, true);
  corner(f, x + w - r, y + h - r, x + w - r, y + h - r, r, bg, true);
}
```

### `colors`

`color_of` keeps the lightness and the hue of a color that no pixel word can show. It makes the chroma smaller until the color fits.

`key_color` looks at the image at 48 by 48 pixels at most. A pixel counts when a person sees it as a color, not as grey, black or white. It goes into one of 24 hues, by weight of its chroma. The key color is the mean of the pixels of the heaviest hue.

```fern
<<colors>>=
// A color in the OKLCH space: the lightness `l` from 0 to 1, the chroma
// `c` from 0, and the hue `h` in radians.
pub struct Lch { l: f64, c: f64, h: f64 }

const HUES: i32 = 24;

function linear(v: i32): f64 {
  let c = v as f64 / 255.0;
  if (c <= 0.04045) {
    return c / 12.92;
  }
  return ((c + 0.055) / 1.055).pow(2.4);
}

function encoded(c: f64): i32 {
  let v = c.max(0.0).min(1.0);
  if (v <= 0.0031308) {
    return (v * 12.92 * 255.0).round() as i32;
  }
  return ((1.055 * v.pow(1.0 / 2.4) - 0.055) * 255.0).round() as i32;
}

// The OKLCH form of the pixel word `color`. The alpha is not part of it.
pub function lch_of(color: i32): Lch {
  let r = linear(color & 255);
  let g = linear(color >> 8 & 255);
  let b = linear(color >> 16 & 255);
  let l = (0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b).cbrt();
  let m = (0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b).cbrt();
  let s = (0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b).cbrt();
  let a = 1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s;
  let bb = 0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s;
  return Lch { l: 0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s, c: (a * a + bb * bb).sqrt(), h: bb.atan2(a) };
}

// The linear red, green and blue of `lch`.
function linear_rgb(lch: Lch): (f64, f64, f64) {
  let a = lch.c * lch.h.cos();
  let b = lch.c * lch.h.sin();
  let l = lch.l + 0.3963377774 * a + 0.2158037573 * b;
  let m = lch.l - 0.1055613458 * a - 0.0638541728 * b;
  let s = lch.l - 0.0894841775 * a - 1.2914855480 * b;
  let l3 = l * l * l;
  let m3 = m * m * m;
  let s3 = s * s * s;
  return (4.0767416621 * l3 - 3.3077115913 * m3 + 0.2309699292 * s3, -1.2684380046 * l3 + 2.6097574011 * m3 - 0.3413193965 * s3, -0.0041960863 * l3 - 0.7034186147 * m3 + 1.7076147010 * s3);
}

function fits(v: f64): boolean {
  return v >= -0.0005 && v <= 1.0005;
}

// The opaque pixel word of `lch`.
pub function color_of(lch: Lch): i32 {
  let at = lch;
  for turn in 0..16 {
    let (r, g, b) = linear_rgb(at);
    if (fits(r) && fits(g) && fits(b) || at.c < 0.001) {
      return rgb(encoded(r), encoded(g), encoded(b));
    }
    at = Lch { ...at, c: at.c * 0.85 };
  }
  let (r, g, b) = linear_rgb(at);
  return rgb(encoded(r), encoded(g), encoded(b));
}

// The pixel word `color` with the alpha `a`.
pub function with_alpha(color: i32, a: i32): i32 {
  return color & 16777215 | (a & 255) << 24;
}

// The hue of a pixel that a person sees as a color, from 0 to HUES - 1, or
// -1 for a grey, a dark, a light or a transparent pixel.
function hue_of(color: i32): i32 {
  if (alpha_of(color) < 128) {
    return -1;
  }
  let lch = lch_of(color);
  if (lch.c < 0.03 || lch.l < 0.15 || lch.l > 0.95) {
    return -1;
  }
  let turn = (lch.h + 3.141592653589793) / (2.0 * 3.141592653589793);
  return (turn * (HUES as f64)) as i32 % HUES;
}

// The color that `img` shows most, or None when it shows no color.
pub function (img: Image) key_color(): Option[i32] {
  let small = img;
  if (img.width > 48 || img.height > 48) {
    small = img.scaled(48.min(img.width), 48.min(img.height));
  }
  let weights: f64[] = [];
  let sums: i64[] = [];
  for k in 0..HUES {
    weights = weights.append(0.0);
    sums = sums.append(0i64).append(0i64).append(0i64).append(0i64);
  }
  for y in 0..small.height {
    for x in 0..small.width {
      let color = small.pixel(x, y);
      let hue = hue_of(color);
      if (hue >= 0) {
        weights = weights.with(hue, weights[hue] + lch_of(color).c);
        let at = hue * 4;
        sums = sums.with(at, sums[at] + (color & 255) as i64).with(at + 1, sums[at + 1] + (color >> 8 & 255) as i64).with(at + 2, sums[at + 2] + (color >> 16 & 255) as i64).with(at + 3, sums[at + 3] + 1i64);
      }
    }
  }
  return heaviest(weights, sums);
}

// The mean color of the heaviest hue of `weights`, or None.
function heaviest(weights: f64[], sums: i64[]): Option[i32] {
  let best = -1;
  for k in 0..weights.len() {
    if (weights[k] > 0.0 && (best < 0 || weights[k] > weights[best])) {
      best = k;
    }
  }
  if (best < 0) {
    return None;
  }
  let n = sums[best * 4 + 3];
  return Some(rgb((sums[best * 4] / n) as i32, (sums[best * 4 + 1] / n) as i32, (sums[best * 4 + 2] / n) as i32));
}
```

### `text`

Text is UTF-8. Each code point takes one cell. The font has the printable ASCII, Latin-1 and Latin Extended-A glyphs, the dashes, the curly quotes, the bullet and the ellipsis. Each other code point, and each byte that does not start a valid sequence, draws as "?".

```fern
<<text>>=
// The width in pixels of `s` at `scale`.
pub function text_width(s: string, scale: i32): i32 {
  return text_cells(s) * font.CELL_W * scale;
}

// The count of cells of `s`: one for each code point.
pub function text_cells(s: string): i32 {
  let n = 0;
  let i = 0;
  while (i < s.len()) {
    n += 1;
    i += code_point(s, i).1;
  }
  return n;
}

// The height in pixels of a line of text at `scale`.
pub function text_height(scale: i32): i32 {
  return font.CELL_H * scale;
}

// One channel of `fg` over `bg`, with the coverage `a` from 0 to 15.
function mix(fg: i32, bg: i32, shift: i32, w: i32): i32 {
  let f = fg >> shift & 255;
  let b = bg >> shift & 255;
  return (f * w + b * (255 - w)) / 255 << shift;
}

// `fg` over the premultiplied `bg` with the weight `w` from 0 to 255. The
// result is opaque when `bg` is opaque.
function blend(fg: i32, bg: i32, w: i32): i32 {
  if (w >= 255) {
    return fg | -16777216;
  }
  let a = (255 * w + (bg >> 24 & 255) * (255 - w)) / 255;
  return a << 24 | mix(fg, bg, 16, w) | mix(fg, bg, 8, w) | mix(fg, bg, 0, w);
}

// Blends `color` over the square of `scale` pixels at x and y.
function dot(f: Frame, x: i32, y: i32, scale: i32, color: i32, a: i32): void {
  for dy in 0..scale {
    for dx in 0..scale {
      let px = x + dx;
      let py = y + dy;
      if (px >= 0 && py >= 0 && px < f.width && py < f.height) {
        unsafe {
          let at = f.bits + ((py * f.stride + px) * 4) as usize;
          __store_i32(at, blend(color, __load_i32(at), weight(color, a)));
        }
      }
    }
  }
}

function glyph(f: Frame, x: i32, y: i32, c: i32, scale: i32, color: i32): void {
  let right = x as i64 + font.CELL_W as i64 * (scale as i64);
  let bottom = y as i64 + font.CELL_H as i64 * (scale as i64);
  if (scale <= 0 || right <= 0 || bottom <= 0 || x >= f.width || y >= f.height) {
    return;
  }
  let x0 = if (x < 0) { (-(x as i64) / (scale as i64)) as i32 } else { 0 };
  let y0 = if (y < 0) { (-(y as i64) / (scale as i64)) as i32 } else { 0 };
  let x1 = if (right > f.width as i64) { ((f.width as i64 - x as i64 - 1i64) / (scale as i64) + 1i64) as i32 } else { font.CELL_W };
  let y1 = if (bottom > f.height as i64) { ((f.height as i64 - y as i64 - 1i64) / (scale as i64) + 1i64) as i32 } else { font.CELL_H };
  let g = font.glyph_index(c);
  for gy in y0..y1 {
    for gx in x0..x1 {
      let a = font.glyph_coverage(g, gx, gy);
      if (a > 0) {
        let px = x + gx * scale;
        let py = y + gy * scale;
        if (scale >= 4) {
          f.fill(px, py, scale, scale, with_alpha(color, weight(color, a)));
        } else {
          dot(f, px, py, scale, color, a);
        }
      }
    }
  }
}

// Draws `s` with the top-left corner of its first cell at x and y, in the
// pixel word `color`, at `scale`.
pub function (f: Frame) text(x: i32, y: i32, s: string, scale: i32, color: i32): void {
  let at = x;
  let i = 0;
  while (i < s.len()) {
    let (c, n) = code_point(s, i);
    glyph(f, at, y, c, scale, color);
    at += font.CELL_W * scale;
    i += n;
  }
}

// The code point that starts at byte `i` of `s`, and its length in bytes. A
// byte that does not start a valid sequence is one code point that the font
// draws as "?".
function code_point(s: string, i: i32): (i32, i32) {
  if let Some(got) = utf8.decode_at(s, i) {
    return got;
  }
  return (63, 1);
}
```
