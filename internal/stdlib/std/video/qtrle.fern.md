# QuickTime Animation packets

This native Fern codec reads and writes RGB24 and ARGB32 Animation packets.
The container FourCC is `rle `, with a final space.
The format uses literal runs, repeated pixels, and skips into the previous frame.
The [format description](https://wiki.multimedia.cx/index.php/Apple_QuickTime_RLE) defines these operations.

`decode_into` consumes the previous frame buffer and returns its updated value.
The caller supplies the dimensions and depth from the container.
The caller must start with a keyframe after a seek.
Skipped pixels retain their previous values, including alpha.
On error, discard the consumed buffer and resume from a keyframe.

`encode` emits a keyframe when `previous` is empty.
Otherwise, `previous` must contain one complete frame.
The encoder preserves every RGB24 or ARGB32 sample.
RGB24 input requires opaque alpha.
The encoder uses greedy runs and does not promise minimum packet size.

The APIs accept exactly one packet, including its size field.
Palette formats, RGB555, and unknown header flags return `Unsupported`.
There is no MOV container reader in this module.

```fern
<<*>>=
// This module encodes and decodes QuickTime Animation packets.
import "std/video/frame";
import "std/audio/wire";

function bytes_per_pixel(depth: i32): Result[i32, frame.VideoError] {
  if (depth == 24) {
    return Ok(3);
  }
  if (depth == 32) {
    return Ok(4);
  }
  return Err(frame.Unsupported);
}

function pixel(input: [u8], at: i32, bpp: i32): u32 {
  if (bpp == 4) {
    return wire.be(input, at, 4) as u32;
  }
  return 4278190080u32 | wire.be(input, at, 3) as u32;
}

pub function decode_into(input: [u8], format: frame.Format, depth: i32, own pixels: u32[], limits: frame.Limits): Result[frame.Frame, frame.VideoError] {
  frame.check_packet(input, limits)?;
  let count = frame.pixel_count(format, limits)?;
  let bpp = bytes_per_pixel(depth)?;
  if (pixels.len() != count) {
    return Err(frame.Buffer);
  }
  if (input.len() < 4) {
    return Err(frame.Truncated);
  }
  let size = wire.be(input, 0, 4) as u32;
  if ((size & 3221225472u32) != 0u32) {
    return Err(frame.Unsupported);
  }
  if (size != input.len() as u32) {
    return Err(frame.Header);
  }
  if (input.len() < 8) {
    return Ok(frame.Frame { format: format, pixels: pixels });
  }
  let header = wire.be(input, 4, 2);
  if (header != 0u64 && header != 8u64) {
    return Err(frame.Unsupported);
  }
  let at = 6;
  let row = 0;
  let end = format.height;
  if (header == 8u64) {
    if (input.len() < 14) {
      return Err(frame.Truncated);
    }
    row = wire.be(input, 6, 2) as i32;
    let lines = wire.be(input, 10, 2) as i32;
    if (row > format.height || lines > format.height - row) {
      return Err(frame.Header);
    }
    end = row + lines;
    at = 14;
  }
  while (row < end) {
    if (at == input.len()) {
      return Err(frame.Truncated);
    }
    let skip = input[at] as i32;
    at += 1;
    if (skip == 0) {
      if (at != input.len()) {
        return Err(frame.Header);
      }
      return Ok(frame.Frame { format: format, pixels: pixels });
    }
    let x = skip - 1;
    if (x > format.width) {
      return Err(frame.Header);
    }
    let done = false;
    while (!done) {
      if (at == input.len()) {
        return Err(frame.Truncated);
      }
      let code = input[at] as i32;
      at += 1;
      if (code == 255) {
        done = true;
      } else if (code == 0) {
        if (at == input.len()) {
          return Err(frame.Truncated);
        }
        let n = input[at] as i32 - 1;
        at += 1;
        if (n < 0 || n > format.width - x) {
          return Err(frame.Header);
        }
        x += n;
      } else {
        let n = if (code < 128) { code } else { 256 - code };
        let bytes = if (code < 128) { n * bpp } else { bpp };
        if (n > format.width - x) {
          return Err(frame.Header);
        }
        if (bytes > input.len() - at) {
          return Err(frame.Truncated);
        }
        let dst = row * format.width + x;
        if (code < 128) {
          for i in 0..n {
            pixels = pixels.with(dst + i, pixel(input, at + i * bpp, bpp));
          }
        } else {
          let value = pixel(input, at, bpp);
          for i in 0..n {
            pixels = pixels.with(dst + i, value);
          }
        }
        x += n;
        at += bytes;
      }
    }
    row += 1;
  }
  // Some encoders omit the final frame terminator after the last row.
  if (at < input.len() && input[at] == 0) {
    at += 1;
  }
  if (at != input.len()) {
    return Err(frame.Header);
  }
  return Ok(frame.Frame { format: format, pixels: pixels });
}

function put_pixel(own out: u8[], p: u32, bpp: i32): u8[] {
  return wire.put_be(out, p as u64, bpp);
}

function repeat_count(pixels: [u32], at: i32, end: i32): i32 {
  let n = 1;
  while (n < 128 && n < end - at && pixels[at + n] == pixels[at]) {
    n += 1;
  }
  return n;
}

function row_equal(a: [u32], b: [u32], row: i32, width: i32): boolean {
  for x in 0..width {
    if (a[row * width + x] != b[row * width + x]) {
      return false;
    }
  }
  return true;
}

function encode_row(own out: u8[], pixels: [u32], previous: [u32], row: i32, width: i32, bpp: i32, budget: i32): Result[u8[], frame.VideoError] {
  let at = row * width;
  let end = at + width;
  out = out.append(1);
  while (at < end) {
    if (previous.len() != 0 && previous[at] == pixels[at]) {
      let n = 1;
      while (n < 254 && n < end - at && previous[at + n] == pixels[at + n]) {
        n += 1;
      }
      if (budget - out.len() < 2) {
        return Err(frame.Limit);
      }
      out = out.append(0).append((n + 1) as u8);
      at += n;
    } else {
      let n = repeat_count(pixels, at, end);
      if (n >= 2) {
        if (budget - out.len() < 1 + bpp) {
          return Err(frame.Limit);
        }
        out = out.append((256 - n) as u8);
        out = put_pixel(out, pixels[at], bpp);
        at += n;
      } else {
        n = 1;
        while (n < 127 && n < end - at) {
          if (previous.len() != 0 && previous[at + n] == pixels[at + n] || repeat_count(pixels, at + n, end) >= 2) {
            break;
          }
          n += 1;
        }
        if (budget - out.len() < 1 + n * bpp) {
          return Err(frame.Limit);
        }
        out = out.append(n as u8);
        for i in 0..n {
          out = put_pixel(out, pixels[at + i], bpp);
        }
        at += n;
      }
    }
  }
  if (out.len() >= budget) {
    return Err(frame.Limit);
  }
  return Ok(out.append(255));
}

pub function encode(pixels: [u32], previous: [u32], format: frame.Format, depth: i32, limits: frame.Limits): Result[u8[], frame.VideoError] {
  let count = frame.pixel_count(format, limits)?;
  let bpp = bytes_per_pixel(depth)?;
  if (pixels.len() != count || previous.len() != 0 && previous.len() != count) {
    return Err(frame.Buffer);
  }
  if (limits.max_packet_bytes < 7) {
    return Err(frame.Limit);
  }
  if (depth == 24) {
    for p in pixels {
      if (p >> 24 != 255u32) {
        return Err(frame.Header);
      }
    }
  }
  let first = 0;
  let end = format.height;
  if (previous.len() != 0) {
    while (first < end && row_equal(pixels, previous, first, format.width)) {
      first += 1;
    }
    while (end > first && row_equal(pixels, previous, end - 1, format.width)) {
      end -= 1;
    }
  }
  let partial = first < end && (first != 0 || end != format.height);
  if (partial && (first > 65535 || end - first > 65535)) {
    return Err(frame.Unsupported);
  }
  let out: u8[] = [0, 0, 0, 0, 0, 0];
  if (partial) {
    if (limits.max_packet_bytes < 15) {
      return Err(frame.Limit);
    }
    out = out.with(5, 8);
    out = wire.put_be(out, first as u64, 2);
    out = wire.put_be(out, 0u64, 2);
    out = wire.put_be(out, (end - first) as u64, 2);
    out = wire.put_be(out, 0u64, 2);
  }
  for row in first..end {
    if (limits.max_packet_bytes - out.len() < 3) {
      return Err(frame.Limit);
    }
    out = encode_row(out, pixels, previous, row, format.width, bpp, limits.max_packet_bytes - 1)?;
  }
  if (out.len() >= limits.max_packet_bytes) {
    return Err(frame.Limit);
  }
  out = out.append(0);
  let size = out.len() as u32;
  if (size > 1073741823u32) {
    return Err(frame.Limit);
  }
  for i in 0..4 {
    out = out.with(i, (size >> ((3 - i) * 8) as u32) as u8);
  }
  return Ok(out);
}
```
