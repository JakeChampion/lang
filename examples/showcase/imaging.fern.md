# Image pipeline

The image lab runs one image pipeline in two places.
Fern compiled to WebAssembly processes the visitor's own image in the browser, so the file never leaves the device.
The server runs the same pipeline natively on a generated test image.

The stages come from the `fern-image` packages: `pixels` checks the layout, `resize` scales the image, `color` changes the tone, and the PNG codec encodes the result.
This document is the source program. The server imports it natively, and the build compiles it to the `imaging.wasm` kernel.

```fern
<<*>>=
<<Imports>>
<<Test image>>
<<Stages>>
<<Command entry>>
```

## Imports

```fern
<<Imports>>=
import "../../fern-image/packages/pixels/pixels";
import "../../fern-image/packages/resize/resize";
import "../../fern-image/packages/color/color";
import "../../fern-image/packages/codecs/png/png";
```

## Test image

The generator draws a diagonal gradient and twelve discs from a seed.
A xorshift step supplies the disc positions and colors, so the server and the browser draw identical pixels.
Integer arithmetic sets every pixel, and no float result differs between targets.

```fern
<<Test image>>=
fip function rand(state: u32): u32 {
  let x: u32 = state ^ state << 13;
  x = x ^ x >> 17;
  return x ^ x << 5;
}

struct Disc { x: i32, y: i32, r2: i32, color: u32 }

@export("fern:showcase/image", "sample")
pub function sample(seed: i32, width: i32, height: i32): u8[] {
  let s: u32 = seed as u32 * 2654435761 | 1;
  let discs: Disc[] = [];
  for i in 0..12 {
    s = rand(s);
    let x = (s % (width as u32)) as i32;
    s = rand(s);
    let y = (s % (height as u32)) as i32;
    s = rand(s);
    let r = (s % (height as u32 / 4 + 1)) as i32 + height / 16;
    s = rand(s);
    discs = discs.append(Disc { x: x, y: y, r2: r * r, color: s });
  }
  let data: u8[] = __alloc_u8(width * height * 4);
  let span = width + height;
  for y in 0..height {
    for x in 0..width {
      let t = (x + y) * 255 / span;
      let r = 24 + t / 3;
      let g = 64 + t / 2;
      let b = 48 + (255 - t) / 3;
      for d in discs {
        let dx = x - d.x;
        let dy = y - d.y;
        if (dx * dx + dy * dy <= d.r2) {
          r = (d.color & 255) as i32;
          g = (d.color >> 8 & 255) as i32;
          b = (d.color >> 16 & 255) as i32;
        }
      }
      let i = (y * width + x) * 4;
      data = data.with(i, r as u8).with(i + 1, g as u8).with(i + 2, b as u8).with(i + 3, 255 as u8);
    }
  }
  return data;
}
```

## Stages

Each stage receives RGBA bytes with eight bits per channel and no row padding.
A stage returns an empty array when the package rejects its input, and the caller reports the stage that failed.

`fit` gives the output height for a target width, with the aspect ratio kept. Filter 0 is bilinear and 1 is nearest neighbor.
Tone 0 keeps the colors. Tone 1 converts to grayscale with linear-light luminance.

```fern
<<Stages>>=
function image(rgba: u8[], width: i32, height: i32): Result[pixels.Image, pixels.ImageError] {
  let layout = pixels.checked_layout(width, height, width * 4, pixels.RGBA8, pixels.default_limits())?;
  if (rgba.len() != layout.bytes) {
    return Err(pixels.Storage);
  }
  return Ok(pixels.Image { layout: layout, data: rgba });
}

@export("fern:showcase/image", "fit")
pub function fit(width: i32, height: i32, target: i32): i32 {
  let Ok(size) = resize.fit_dimensions(width, height, target, 1000000) else {
    return 0;
  };
  return size.height;
}

@export("fern:showcase/image", "resize")
pub function scale(rgba: u8[], width: i32, height: i32, target: i32, filter: i32): u8[] {
  let Ok(source) = image(rgba, width, height) else {
    return [];
  };
  let mode = resize.Bilinear;
  if (filter == 1) {
    mode = resize.Nearest;
  }
  let Ok(output) = resize.resize(source, target, fit(width, height, target), mode, pixels.default_limits()) else {
    return [];
  };
  return output.data;
}

@export("fern:showcase/image", "tone")
pub function tone(rgba: u8[], width: i32, height: i32, effect: i32): u8[] {
  let Ok(source) = image(rgba, width, height) else {
    return [];
  };
  if (effect == 0) {
    return source.data;
  }
  let output = source;
  for y in 0..height {
    for x in 0..width {
      let Ok(pixel) = pixels.read_pixel(output, x, y) else {
        return [];
      };
      let Ok(next) = pixels.write_pixel(output, x, y, color.grayscale(pixel)) else {
        return [];
      };
      output = next;
    }
  }
  return output.data;
}

@export("fern:showcase/image", "encode")
pub function encode(rgba: u8[], width: i32, height: i32): u8[] {
  let Ok(source) = image(rgba, width, height) else {
    return [];
  };
  let Ok(bytes) = png.encode(source, 16777216) else {
    return [];
  };
  return bytes;
}
```

## Command entry

The browser instantiates the core module and calls its exports. It never calls `_start`.

```fern
<<Command entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

The generator is deterministic, each stage keeps the expected size, and the PNG output decodes to the same pixels.

```fern test name=imaging-example
<<Imports>>
<<Test image>>
<<Stages>>
function main(): i32 {
  let source = sample(5, 120, 80);
  if (source.len() != 120 * 80 * 4 || source.len() != sample(5, 120, 80).len()) { return 1; }
  let height = fit(120, 80, 48);
  if (height != 32) { return 2; }
  let small = scale(source, 120, 80, 48, 0);
  if (small.len() != 48 * height * 4 || scale(source, 120, 80, 48, 1).len() != small.len()) { return 3; }
  let gray = tone(small, 48, height, 1);
  if (gray.len() != small.len() || gray[0] != gray[1] || gray[1] != gray[2]) { return 4; }
  let Ok(decoded) = png.decode(encode(gray, 48, height), pixels.default_limits()) else { return 5; };
  if (decoded.layout.width != 48 || decoded.data.len() != gray.len()) { return 6; }
  for i in 0..gray.len() {
    if (decoded.data[i] != gray[i]) { return 7; }
  }
  if (scale(source, 120, 79, 48, 0).len() != 0) { return 8; }
  return 0;
}
```
