# Drawing on an Android frame

This module draws rectangles, images and text on the pixels of an Android window.

## Model

A `Frame` holds the pixels of a window while the window is locked: 4 bytes for each pixel, in the order red, green, blue and alpha, and `stride` pixels for each row. `std/android` locks a window and gives its frame. The functions here only write into the pixels, so a test can make a frame from plain memory.

A color is a pixel word with an alpha from 0, for none, to 255, for opaque: `rgba` makes one, and `rgb` makes an opaque one. Each shape and each glyph blends its color over the pixels of the frame. The weight is the alpha times the part of each pixel that the shape covers. A shape over opaque pixels leaves them opaque.

Over transparent pixels, a shape gives premultiplied pixels: each channel holds its value times the alpha. So a frame from `offscreen` that starts transparent holds a premultiplied image, which a GPU can draw over other pixels. `store` writes pixel words as they are, so it can make transparent pixels, for example the corners of an icon.

An `Image` holds 4 bytes for each pixel, red, green, blue and alpha, row by row with no stride. `std/android/image` decodes one, and `image` copies a part of it into a frame. `cover_size` gives the size of an image scaled to fill a box with its shape kept. The box then shows the middle of the image. `clip_corners` rounds the corners of an opaque image on a flat background.

Text uses the Fern raster of DejaVu Sans Mono in `std/android/font`. Each glyph pixel has a coverage from 0 to 15, and the text color blends over the frame by that coverage. `scale` draws each raster pixel as a square of `scale` by `scale` pixels.

## Contracts

The pixels of a frame are valid until the window is posted. A rectangle or text outside the frame is not drawn.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<frames>>

<<images>>

<<text>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module draws rectangles, discs, rings and text on the pixels of an
// Android window.
import "std/android/font";
import "std/f64";
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
  for row in clamp(y, 0, f.height)..clamp(y + h, 0, f.height) {
    for col in clamp(x, 0, f.width)..clamp(x + w, 0, f.width) {
      put(f, col, row, color, 15);
    }
  }
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

// Blends `src` over the frame with its top left corner at x and y, with the
// weight `alpha` from 0 to 255 for each pixel.
pub function (f: Frame) blend_frame(src: Frame, x: i32, y: i32, alpha: i32): void {
  if (alpha >= 255) {
    f.blit(src, x, y);
    return;
  }
  let w = clamp(alpha, 0, 255);
  w = w + (w >> 7);
  let x0 = clamp(x, 0, f.width);
  let x1 = clamp(x + src.width, 0, f.width);
  let y0 = clamp(y, 0, f.height);
  let y1 = clamp(y + src.height, 0, f.height);
  if (x1 <= x0 || y1 <= y0 || w == 0) {
    return;
  }
  unsafe {
    for row in y0..y1 {
      let to = f.bits + ((row * f.stride + x0) * 4) as usize;
      let from = src.bits + (((row - y) * src.stride + x0 - x) * 4) as usize;
      for i in 0..x1 - x0 {
        let at = (i * 4) as usize;
        __store_i32(to + at, mix_word(__load_i32(from + at), __load_i32(to + at), w));
      }
    }
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
  let solid = -16777216 | color;
  unsafe {
    for row in y0..y1 {
      let to = f.bits + (row * f.stride * 4) as usize;
      let from = (row - y) * w - x;
      for col in x0..x1 {
        let a = cov[from + col] as i32;
        if (a != 0) {
          let at = to + (col * 4) as usize;
          let k = a * ca * 257 + 32768 >> 16;
          if (k >= 255) {
            __store_i32(at, solid);
          } else {
            __store_i32(at, mix_word(color, __load_i32(at), k + (k >> 7)));
          }
        }
      }
    }
  }
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

function dist(x: i32, y: i32, cx: f64, cy: f64): f64 {
  let dx = x as f64 + 0.5 - cx;
  let dy = y as f64 + 0.5 - cy;
  return (dx * dx + dy * dy).sqrt();
}

// Fills the disc of radius `r` around cx and cy. Each pixel of the edge
// takes the part of `color` that the disc covers.
pub function (f: Frame) disc(cx: i32, cy: i32, r: i32, color: i32): void {
  f.ring(cx, cy, r, r + 1, color);
}

// Fills the band of `width` pixels inside the circle of radius `r` around cx
// and cy, with smooth edges.
pub function (f: Frame) ring(cx: i32, cy: i32, r: i32, width: i32, color: i32): void {
  let ox = cx as f64;
  let oy = cy as f64;
  let outer = r as f64;
  let inner = (r - width) as f64;
  for y in cy - r - 1..cy + r + 1 {
    for x in cx - r - 1..cx + r + 1 {
      let d = dist(x, y, ox, oy);
      let a = cover(d, outer) - cover(d, inner);
      put(f, x, y, color, a);
    }
  }
}

// Fills the rectangle at x and y, of width w and height h, with its corners
// rounded to radius `r` and smooth edges. A radius of half the height makes
// a pill.
pub function (f: Frame) round_rect(x: i32, y: i32, w: i32, h: i32, r0: i32, color: i32): void {
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
  corner(f, x + r, y + r, x, y, r, color);
  corner(f, x + w - r, y + r, x + w - r, y, r, color);
  corner(f, x + r, y + h - r, x, y + h - r, r, color);
  corner(f, x + w - r, y + h - r, x + w - r, y + h - r, r, color);
}

// The square of side `r` at x0 and y0, covered by the circle of radius `r`
// around cx and cy.
function corner(f: Frame, cx: i32, cy: i32, x0: i32, y0: i32, r: i32, color: i32): void {
  for y in y0..y0 + r {
    for x in x0..x0 + r {
      put(f, x, y, color, cover(dist(x, y, cx as f64, cy as f64), r as f64));
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
  let out: u8[] = [];
  for y in 0..h {
    let (y0, y1, ty) = nearest(y, sy, img.height);
    for x in 0..w {
      let (x0, x1, tx) = nearest(x, sx, img.width);
      let a = (y0 * img.width + x0) * 4;
      let b = (y0 * img.width + x1) * 4;
      let c = (y1 * img.width + x0) * 4;
      let d = (y1 * img.width + x1) * 4;
      for k in 0..4 {
        let top = px[a + k] as f64 + (px[b + k] as f64 - px[a + k] as f64) * tx;
        let bottom = px[c + k] as f64 + (px[d + k] as f64 - px[c + k] as f64) * tx;
        out = out.append((top + (bottom - top) * ty + 0.5) as u8);
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
  if (x1 <= x0 || y1 <= y0 || img.pixels.len() < img.width * img.height * 4) {
    return;
  }
  let px = img.pixels;
  for row in y0..y1 {
    let from = ((sy + row - dy) * img.width + sx + x0 - dx) * 4;
    unsafe {
      let to = f.bits + ((row * f.stride + x0) * 4) as usize;
      for i in 0..x1 - x0 {
        let k = from + i * 4;
        __store_i32(to + (i * 4) as usize, rgba(px[k] as i32, px[k + 1] as i32, px[k + 2] as i32, px[k + 3] as i32));
      }
    }
  }
}

// Paints `bg` over the corners of the box at x and y, of width w and height
// h, outside the quarter circles of radius `r`, with smooth edges. Over an
// opaque image it gives the image rounded corners.
pub function (f: Frame) clip_corners(x: i32, y: i32, w: i32, h: i32, r: i32, bg: i32): void {
  outside(f, x + r, y + r, x, y, r, bg);
  outside(f, x + w - r, y + r, x + w - r, y, r, bg);
  outside(f, x + r, y + h - r, x, y + h - r, r, bg);
  outside(f, x + w - r, y + h - r, x + w - r, y + h - r, r, bg);
}

function outside(f: Frame, cx: i32, cy: i32, x0: i32, y0: i32, r: i32, color: i32): void {
  for y in y0..y0 + r {
    for x in x0..x0 + r {
      put(f, x, y, color, 15 - cover(dist(x, y, cx as f64, cy as f64), r as f64));
    }
  }
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
  let g = font.glyph_index(c);
  for gy in 0..font.CELL_H {
    for gx in 0..font.CELL_W {
      let a = font.glyph_coverage(g, gx, gy);
      if (a > 0) {
        dot(f, x + gx * scale, y + gy * scale, scale, color, a);
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
