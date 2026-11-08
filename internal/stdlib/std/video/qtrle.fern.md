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

struct Span { at: i32, row: i32, end: i32 }

struct Row { pixels: u32[], at: i32, finished: boolean }

struct Step { pixels: u32[], at: i32, x: i32 }

// The size word: the packet's own length, with the two flag bits clear.
function decode_size(input: [u8]): Result[void, frame.VideoError] {
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
  return Ok(());
}

// The header after the size word: where the row data starts and which rows
// it covers. A packet of the size word alone changes no row.
function decode_span(input: [u8], format: frame.Format): Result[Span, frame.VideoError] {
  decode_size(input)?;
  if (input.len() < 8) {
    return Ok(Span { at: input.len(), row: 0, end: 0 });
  }
  let header = wire.be(input, 4, 2);
  if (header != 0u64 && header != 8u64) {
    return Err(frame.Unsupported);
  }
  if (header == 0u64) {
    return Ok(Span { at: 6, row: 0, end: format.height });
  }
  if (input.len() < 14) {
    return Err(frame.Truncated);
  }
  let row = wire.be(input, 6, 2) as i32;
  let lines = wire.be(input, 10, 2) as i32;
  if (row > format.height || lines > format.height - row) {
    return Err(frame.Header);
  }
  return Ok(Span { at: 14, row: row, end: row + lines });
}

// A literal run of n pixels from the input, or one input pixel repeated n times.
function write_run(own pixels: u32[], dst: i32, n: i32, input: [u8], at: i32, bpp: i32, literal: boolean): u32[] {
  if (literal) {
    for i in 0..n {
      pixels = pixels.with(dst + i, pixel(input, at + i * bpp, bpp));
    }
    return pixels;
  }
  let value = pixel(input, at, bpp);
  for i in 0..n {
    pixels = pixels.with(dst + i, value);
  }
  return pixels;
}

// One code of a row: a skip, a literal run or a repeated pixel.
function decode_code(input: [u8], at: i32, code: i32, row: i32, x: i32, format: frame.Format, bpp: i32, own pixels: u32[]): Result[Step, frame.VideoError] {
  if (code == 0) {
    if (at == input.len()) {
      return Err(frame.Truncated);
    }
    let n = input[at] as i32 - 1;
    if (n < 0 || n > format.width - x) {
      return Err(frame.Header);
    }
    return Ok(Step { pixels: pixels, at: at + 1, x: x + n });
  }
  let n = if (code < 128) { code } else { 256 - code };
  let bytes = if (code < 128) { n * bpp } else { bpp };
  if (n > format.width - x) {
    return Err(frame.Header);
  }
  if (bytes > input.len() - at) {
    return Err(frame.Truncated);
  }
  let written = write_run(pixels, row * format.width + x, n, input, at, bpp, code < 128);
  return Ok(Step { pixels: written, at: at + bytes, x: x + n });
}

// One row, from its skip byte to its 255 terminator. A skip byte of zero
// ends the packet instead, and the row is finished.
function decode_row(input: [u8], at0: i32, row: i32, format: frame.Format, bpp: i32, own pixels: u32[]): Result[Row, frame.VideoError] {
  let at = at0;
  if (at == input.len()) {
    return Err(frame.Truncated);
  }
  let skip = input[at] as i32;
  at += 1;
  if (skip == 0) {
    if (at != input.len()) {
      return Err(frame.Header);
    }
    return Ok(Row { pixels: pixels, at: at, finished: true });
  }
  let x = skip - 1;
  if (x > format.width) {
    return Err(frame.Header);
  }
  loop {
    if (at == input.len()) {
      return Err(frame.Truncated);
    }
    let code = input[at] as i32;
    at += 1;
    if (code == 255) {
      return Ok(Row { pixels: pixels, at: at, finished: false });
    }
    let step = decode_code(input, at, code, row, x, format, bpp, pixels)?;
    pixels = step.pixels;
    at = step.at;
    x = step.x;
  }
}

// After the last row: a frame terminator some encoders omit, then the end.
function decode_end(input: [u8], at0: i32): Result[void, frame.VideoError] {
  let at = at0;
  if (at < input.len() && input[at] == 0) {
    at += 1;
  }
  if (at != input.len()) {
    return Err(frame.Header);
  }
  return Ok(());
}

pub function decode_into(input: [u8], format: frame.Format, depth: i32, own pixels: u32[], limits: frame.Limits): Result[frame.Frame, frame.VideoError] {
  frame.check_packet(input, limits)?;
  let count = frame.pixel_count(format, limits)?;
  let bpp = bytes_per_pixel(depth)?;
  if (pixels.len() != count) {
    return Err(frame.Buffer);
  }
  let span = decode_span(input, format)?;
  let at = span.at;
  let row = span.row;
  while (row < span.end) {
    let r = decode_row(input, at, row, format, bpp, pixels)?;
    pixels = r.pixels;
    at = r.at;
    if (r.finished) {
      return Ok(frame.Frame { format: format, pixels: pixels });
    }
    row += 1;
  }
  decode_end(input, at)?;
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

// How many pixels from `at` the previous frame already holds, at most 254.
function skip_len(pixels: [u32], previous: [u32], at: i32, end: i32): i32 {
  let n = 1;
  while (n < 254 && n < end - at && previous[at + n] == pixels[at + n]) {
    n += 1;
  }
  return n;
}

// How many pixels from `at` a literal run covers before a skip or a repeat
// would pay, at most 127.
function literal_len(pixels: [u32], previous: [u32], at: i32, end: i32): i32 {
  let n = 1;
  while (n < 127 && n < end - at) {
    if (previous.len() != 0 && previous[at + n] == pixels[at + n] || repeat_count(pixels, at + n, end) >= 2) {
      break;
    }
    n += 1;
  }
  return n;
}

function encode_row(own out: u8[], pixels: [u32], previous: [u32], row: i32, width: i32, bpp: i32, budget: i32): Result[u8[], frame.VideoError] {
  let at = row * width;
  let end = at + width;
  out = out.append(1);
  while (at < end) {
    if (previous.len() != 0 && previous[at] == pixels[at]) {
      let n = skip_len(pixels, previous, at, end);
      if (budget - out.len() < 2) {
        return Err(frame.Limit);
      }
      out = out.append(0).append((n + 1) as u8);
      at += n;
      continue;
    }
    let n = repeat_count(pixels, at, end);
    if (n >= 2) {
      if (budget - out.len() < 1 + bpp) {
        return Err(frame.Limit);
      }
      out = out.append((256 - n) as u8);
      out = put_pixel(out, pixels[at], bpp);
      at += n;
      continue;
    }
    n = literal_len(pixels, previous, at, end);
    if (budget - out.len() < 1 + n * bpp) {
      return Err(frame.Limit);
    }
    out = out.append(n as u8);
    for i in 0..n {
      out = put_pixel(out, pixels[at + i], bpp);
    }
    at += n;
  }
  if (out.len() >= budget) {
    return Err(frame.Limit);
  }
  return Ok(out.append(255));
}

// The frame buffer and, when a delta is asked, the previous one, each whole.
function buffers_ok(pixels: [u32], previous: [u32], count: i32): Result[void, frame.VideoError] {
  if (pixels.len() != count || previous.len() != 0 && previous.len() != count) {
    return Err(frame.Buffer);
  }
  return Ok(());
}

// RGB24 input must be opaque: the depth has no room for alpha.
function opaque(pixels: [u32], depth: i32): Result[void, frame.VideoError] {
  if (depth != 24) {
    return Ok(());
  }
  for p in pixels {
    if (p >> 24 != 255u32) {
      return Err(frame.Header);
    }
  }
  return Ok(());
}

// The first row that differs from the previous frame, and one past the last.
function changed_rows(pixels: [u32], previous: [u32], format: frame.Format): (i32, i32) {
  let first = 0;
  let end = format.height;
  if (previous.len() == 0) {
    return (first, end);
  }
  while (first < end && row_equal(pixels, previous, first, format.width)) {
    first += 1;
  }
  while (end > first && row_equal(pixels, previous, end - 1, format.width)) {
    end -= 1;
  }
  return (first, end);
}

// The size word, left zero, and the header: empty for a whole frame, the
// row range for a partial one.
function header(first: i32, end: i32, format: frame.Format, limits: frame.Limits): Result[u8[], frame.VideoError] {
  let out: u8[] = [0, 0, 0, 0, 0, 0];
  let partial = first < end && (first != 0 || end != format.height);
  if (!partial) {
    return Ok(out);
  }
  if (first > 65535 || end - first > 65535) {
    return Err(frame.Unsupported);
  }
  if (limits.max_packet_bytes < 15) {
    return Err(frame.Limit);
  }
  out = out.with(5, 8);
  out = wire.put_be(out, first as u64, 2);
  out = wire.put_be(out, 0u64, 2);
  out = wire.put_be(out, (end - first) as u64, 2);
  return Ok(wire.put_be(out, 0u64, 2));
}

// The frame terminator, then the size word written over the first four bytes.
function sized(own out: u8[], limits: frame.Limits): Result[u8[], frame.VideoError] {
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

pub function encode(pixels: [u32], previous: [u32], format: frame.Format, depth: i32, limits: frame.Limits): Result[u8[], frame.VideoError] {
  let count = frame.pixel_count(format, limits)?;
  let bpp = bytes_per_pixel(depth)?;
  buffers_ok(pixels, previous, count)?;
  if (limits.max_packet_bytes < 7) {
    return Err(frame.Limit);
  }
  opaque(pixels, depth)?;
  let (first, end) = changed_rows(pixels, previous, format);
  let out = header(first, end, format, limits)?;
  for row in first..end {
    if (limits.max_packet_bytes - out.len() < 3) {
      return Err(frame.Limit);
    }
    out = encode_row(out, pixels, previous, row, format.width, bpp, limits.max_packet_bytes - 1)?;
  }
  return sized(out, limits);
}
```
