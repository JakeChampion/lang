# Drawing on an Android frame

This module draws rectangles and text on the pixels of an Android window.

## Model

A `Frame` holds the pixels of a window while the window is locked: 4 bytes for each pixel, in the order red, green, blue and alpha, and `stride` pixels for each row. `std/android` locks a window and gives its frame. The functions here only write into the pixels, so a test can make a frame from plain memory.

A color is a pixel word with an alpha from 0, for none, to 255, for opaque: `rgba` makes one, and `rgb` makes an opaque one. Each shape and each glyph blends its color over the pixels of the frame by the alpha and by the part of each pixel that it covers. A shape over opaque pixels leaves them opaque. `store` writes pixel words as they are, so it can make transparent pixels, for example the corners of an icon.

Text uses the Fern raster of DejaVu Sans Mono in `std/android/font`. Each glyph pixel has a coverage from 0 to 15, and the text color blends over the frame by that coverage. `scale` draws each raster pixel as a square of `scale` by `scale` pixels.

## Contracts

The pixels of a frame are valid until the window is posted. A rectangle or text outside the frame is not drawn.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<frames>>

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

// The opaque pixel word of `color` drawn over the pixel word `under`.
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

### `text`

A byte from 128 to 191 continues a UTF-8 character, so it takes no cell. Each other byte outside printable ASCII draws as "?".

```fern
<<text>>=
// The width in pixels of `s` at `scale`.
pub function text_width(s: string, scale: i32): i32 {
  let n = 0;
  for c in s.bytes() {
    if (c < 128 as u8 || c >= 192 as u8) {
      n += 1;
    }
  }
  return n * font.CELL_W * scale;
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

// `fg` over `bg` with the weight `w` from 0 to 255. The result is opaque.
function blend(fg: i32, bg: i32, w: i32): i32 {
  if (w >= 255) {
    return fg;
  }
  return -16777216 | mix(fg, bg, 16, w) | mix(fg, bg, 8, w) | mix(fg, bg, 0, w);
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
  for gy in 0..font.CELL_H {
    for gx in 0..font.CELL_W {
      let a = font.coverage(c, gx, gy);
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
  for c in s.bytes() {
    if (c < 128 as u8 || c >= 192 as u8) {
      glyph(f, at, y, c as i32, scale, color);
      at += font.CELL_W * scale;
    }
  }
}
```
