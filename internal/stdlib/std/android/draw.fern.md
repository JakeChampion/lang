# Drawing on an Android frame

This module draws rectangles and text on the pixels of an Android window.

## Model

A `Frame` holds the pixels of a window while the window is locked: 4 bytes for each pixel, in the order red, green, blue and alpha, and `stride` pixels for each row. `std/android` locks a window and gives its frame. The functions here only write into the pixels, so a test can make a frame from plain memory.

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
// This module draws rectangles and text on the pixels of an Android window.
import "std/android/font";
import "std/string";
```

### `frames`

```fern
<<frames>>=
pub struct Frame { window: usize, buffer: usize, bits: usize, width: i32, height: i32, stride: i32 }

// The pixel word of an opaque color.
pub function rgb(r: i32, g: i32, b: i32): i32 {
  return 0 - 16777216 | (b & 255) << 16 | (g & 255) << 8 | r & 255;
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

// Fills the rectangle at x and y, of width w and height h, with the pixel
// word `color`. The part outside the frame is not drawn.
pub function (f: Frame) fill(x: i32, y: i32, w: i32, h: i32, color: i32): void {
  let x0: i32 = clamp(x, 0, f.width);
  let y0: i32 = clamp(y, 0, f.height);
  let x1: i32 = clamp(x + w, 0, f.width);
  let y1: i32 = clamp(y + h, 0, f.height);
  if (x1 <= x0 || y1 <= y0) {
    return;
  }
  unsafe {
    let first: usize = f.bits + ((y0 * f.stride + x0) * 4) as usize;
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
```

### `text`

A byte from 128 to 191 continues a UTF-8 character, so it takes no cell. Each other byte outside printable ASCII draws as "?".

```fern
<<text>>=
// The width in pixels of `s` at `scale`.
pub function text_width(s: string, scale: i32): i32 {
  let n: i32 = 0;
  for c in s.bytes() {
    if (c < 128 as u8 || c >= 192 as u8) {
      n = n + 1;
    }
  }
  return n * font.CELL_W * scale;
}

// The height in pixels of a line of text at `scale`.
pub function text_height(scale: i32): i32 {
  return font.CELL_H * scale;
}

// One channel of `fg` over `bg`, with the coverage `a` from 0 to 15.
function mix(fg: i32, bg: i32, shift: i32, a: i32): i32 {
  let f: i32 = fg >> shift & 255;
  let b: i32 = bg >> shift & 255;
  return (f * a + b * (15 - a)) / 15 << shift;
}

function blend(fg: i32, bg: i32, a: i32): i32 {
  if (a >= 15) {
    return fg;
  }
  return 0 - 16777216 | mix(fg, bg, 16, a) | mix(fg, bg, 8, a) | mix(fg, bg, 0, a);
}

// Blends `color` over the square of `scale` pixels at x and y.
function dot(f: Frame, x: i32, y: i32, scale: i32, color: i32, a: i32): void {
  for dy in 0..scale {
    for dx in 0..scale {
      let px: i32 = x + dx;
      let py: i32 = y + dy;
      if (px >= 0 && py >= 0 && px < f.width && py < f.height) {
        unsafe {
          let at: usize = f.bits + ((py * f.stride + px) * 4) as usize;
          __store_i32(at, blend(color, __load_i32(at), a));
        }
      }
    }
  }
}

function glyph(f: Frame, x: i32, y: i32, c: i32, scale: i32, color: i32): void {
  for gy in 0..font.CELL_H {
    for gx in 0..font.CELL_W {
      let a: i32 = font.coverage(c, gx, gy);
      if (a > 0) {
        dot(f, x + gx * scale, y + gy * scale, scale, color, a);
      }
    }
  }
}

// Draws `s` with the top-left corner of its first cell at x and y, in the
// pixel word `color`, at `scale`.
pub function (f: Frame) text(x: i32, y: i32, s: string, scale: i32, color: i32): void {
  let at: i32 = x;
  for c in s.bytes() {
    if (c < 128 as u8 || c >= 192 as u8) {
      glyph(f, at, y, c as i32, scale, color);
      at = at + font.CELL_W * scale;
    }
  }
}
```
