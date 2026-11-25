# WebP files

This module decodes lossless WebP files to pixels, still or animated. It also makes an animated WebP file from still WebP files, one for each frame.

## Model

A WebP file is a RIFF file: the tag `RIFF`, the size, the tag `WEBP`, and chunks. A still image is one `VP8 ` chunk (lossy) or one `VP8L` chunk (lossless). A lossy image with alpha has a `VP8X` chunk, then an `ALPH` chunk and a `VP8 ` chunk.

An animated file has a `VP8X` chunk with the animation flag, an `ANIM` chunk with the loop count, and an `ANMF` chunk for each frame. An `ANMF` chunk holds the place, the size and the duration of the frame, and the image chunks of a still file. `animate` takes these chunks from each still file, so any encoder of still WebP can make the frames.

## Contracts

Each still file is `width` by `height`. The frames cover the canvas, and each frame replaces the frame before: it does not blend. The animation loops for ever. A duration is in milliseconds, from 0 to 16777215.

No frames, or a count of durations that is not the count of frames, gives `NoFrames`. A file that is not a still WebP file gives `NotWebp`. A width or a height that is not from 1 to 16384 gives `InvalidDimensions`.

`decode` reads a still file with a `VP8L` chunk, the lossless format of RFC 9649. It gives an `Image` with four bytes for each pixel: red, green, blue and alpha. A file without alpha gives 255 for each alpha byte.

A lossy file (a `VP8 ` chunk) gives `Unsupported`, and an animated file gives `Animated`. A file that is not WebP gives `NotWebpFile`. A `VP8L` chunk that is not valid gives `BadHeader`, `BadCode`, `BadData` or `Truncated`.

`decode` accepts the files that libwebp accepts. Thus the bitstream can read past the end of its chunk, to the end of the file. An `ALPH` chunk has no effect on a lossless image.

`decode_animation` reads an animated file whose frames are lossless. It gives the canvas after each frame, as the animation decoder of libwebp gives it.

The canvas is transparent at the start. A frame with the blend flag is blended over the canvas, with its alpha. A frame without the flag replaces its rectangle of the canvas. A frame with the dispose flag makes its rectangle transparent before the next frame. The background color of the `ANIM` chunk has no effect, as in libwebp. A loop count of 0 means that the animation loops for ever.

`decode_animation` also reads a still lossless file, as one frame with a duration of 0. A lossy frame gives `Unsupported`. A `VP8L` frame cannot read past the end of its chunk. These files give `BadHeader`:

- a frame that is not inside the canvas,
- a frame before the `ANIM` chunk,
- an animation with no frame,
- a canvas of more than 2^28 pixels.

## Compatibility

| Part of the format | Status |
| --- | --- |
| RIFF container, `VP8X`, and `ICCP`, `EXIF`, `XMP ` and unknown chunks | Read. The metadata chunks are skipped. |
| Lossless still image (`VP8L`) | Decoded by `decode`. |
| Lossless animation (`ANIM`, `ANMF` with `VP8L` frames) | Decoded by `decode_animation`. Written by `animate`. |
| Lossy image (`VP8 `) and lossy alpha (`ALPH`) | Not decoded. `animate` copies these chunks into frames. |
| Metadata (ICC profile, Exif, XMP) | Not given to the caller. |

## Executable example

```fern test name=std-webp-example
import "std/webp";

function main(): i32 {
  match (webp.animate(1, 1, [[1u8, 2u8]], [100])) {
    Ok(_) => {
      return 1;
    },
    Err(e) => {
      if (e.message() != "webp: a frame is not a still WebP file") {
        return 2;
      }
    }
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module makes animated WebP files from still WebP files.
import "core/cmp";
import "core/error";
import "std/i32";

<<errors>>

<<chunks>>

<<animation>>

<<bits>>

<<prefix codes>>

<<pixels>>

<<transforms>>

<<decoding>>

<<animated decoding>>
```

### `errors`

```fern
<<errors>>=
pub enum AnimateError { NoFrames, NotWebp, InvalidDimensions }

pub function (e: AnimateError) message(): string {
  match (e) {
    NoFrames => {
      return "webp: an animation needs one frame or more, and one duration for each frame";
    },
    NotWebp => {
      return "webp: a frame is not a still WebP file";
    },
    InvalidDimensions => {
      return "webp: the width and the height must be from 1 to 16384";
    }
  }
}

pub function (e: AnimateError) to_string(): string {
  return e.message();
}

impl error.Error for AnimateError {}

impl cmp.Display for AnimateError {}

pub enum DecodeError { NotWebpFile, Unsupported, Animated, BadHeader, BadCode, BadData, Truncated }

pub function (e: DecodeError) message(): string {
  match (e) {
    NotWebpFile => {
      return "webp: not a WebP file";
    },
    Unsupported => {
      return "webp: only lossless images can be decoded";
    },
    Animated => {
      return "webp: the file is animated, so decode_animation must read it";
    },
    BadHeader => {
      return "webp: the header of the lossless image is not valid";
    },
    BadCode => {
      return "webp: a prefix code of the lossless image is not valid";
    },
    BadData => {
      return "webp: the data of the lossless image is not valid";
    },
    Truncated => {
      return "webp: the data ends early";
    }
  }
}

pub function (e: DecodeError) to_string(): string {
  return e.message();
}

impl error.Error for DecodeError {}

impl cmp.Display for DecodeError {}

// Red, green, blue and alpha bytes for each pixel, row by row.
pub struct Image { width: i32, height: i32, pixels: u8[] }

// The canvas after a frame, and the time to show it in milliseconds.
pub struct Frame { image: Image, duration_ms: i32 }

// The frames of an animation on a canvas of `width` by `height`. A
// `loop_count` of 0 means that the animation loops for ever.
pub struct Animation { width: i32, height: i32, loop_count: i32, frames: Frame[] }
```

### `chunks`

A chunk is a tag of four bytes, the size of its data as a 32-bit little-endian word, and the data. Data of an odd size has one byte of padding after it.

```fern
<<chunks>>=
function le24(own out: u8[], v: i32): u8[] {
  return out.append((v & 255) as u8).append((v >> 8 & 255) as u8).append((v >> 16 & 255) as u8);
}

function le32(own out: u8[], v: i32): u8[] {
  return le24(out, v).append((v >> 24 & 255) as u8);
}

function u32_at(b: [u8], at: i32): i32 {
  return b[at] as i32 | b[at + 1] as i32 << 8 | b[at + 2] as i32 << 16 | b[at + 3] as i32 << 24;
}

function tag_at(b: [u8], at: i32, tag: string): boolean {
  let t = tag.as_bytes();
  return b[at] == t[0] && b[at + 1] == t[1] && b[at + 2] == t[2] && b[at + 3] == t[3];
}

function chunk(own out: u8[], tag: string, data: [u8]): u8[] {
  for c in tag.as_bytes() {
    out = out.append(c);
  }
  out = le32(out, data.len());
  for b in data {
    out = out.append(b);
  }
  if (data.len() % 2 == 1) {
    out = out.append(0u8);
  }
  return out;
}

function is_webp(file: [u8]): boolean {
  return file.len() >= 20 && tag_at(file, 0, "RIFF") && tag_at(file, 8, "WEBP");
}

// The chunk at `at` is a chunk of the image: `ALPH`, `VP8 ` or `VP8L`.
function is_image(file: [u8], at: i32): boolean {
  return tag_at(file, at, "ALPH") || tag_at(file, at, "VP8 ") || tag_at(file, at, "VP8L");
}

// The image chunks of the still WebP file `file`, with their headers and
// their padding: `ALPH`, `VP8 ` and `VP8L`. It is None for a file that is
// not WebP, or that has no image chunk.
function image_chunks(file: [u8]): Option[u8[]] {
  if (!is_webp(file)) {
    return None;
  }
  let out: u8[] = [];
  let found = false;
  let at = 12;
  while (at + 8 <= file.len()) {
    let size = u32_at(file, at + 4);
    let end = at + 8 + size + size % 2;
    if (size < 0 || at + 8 + size > file.len()) {
      return None;
    }
    if (is_image(file, at)) {
      found = found || !tag_at(file, at, "ALPH");
      for k in at..at + 8 + size {
        out = out.append(file[k]);
      }
      if (size % 2 == 1) {
        out = out.append(0u8);
      }
    }
    at = end;
  }
  return if (found) { Some(out) } else { None };
}
```

### `animation`

The `VP8X` flags are the animation flag and the alpha flag, so a frame can have alpha. The `ANMF` flags ask for no blending and no disposal.

```fern
<<animation>>=
function checked(width: i32, height: i32, stills: [u8[]], durations_ms: [i32]): Option[AnimateError] {
  if (width < 1 || height < 1 || width > 16384 || height > 16384) {
    return Some(InvalidDimensions);
  }
  if (stills.len() == 0 || durations_ms.len() != stills.len()) {
    return Some(NoFrames);
  }
  return None;
}

// The animated WebP file of the still WebP files `stills`, each `width` by
// `height`, with the durations of the frames in milliseconds.
pub function animate(width: i32, height: i32, stills: [u8[]], durations_ms: [i32]): Result[u8[], AnimateError] {
  if let Some(e) = checked(width, height, stills, durations_ms) {
    return Err(e);
  }
  let header = le24(le24([18u8, 0u8, 0u8, 0u8], width - 1), height - 1);
  let body: u8[] = [87u8, 69u8, 66u8, 80u8];
  body = chunk(body, "VP8X", header);
  body = chunk(body, "ANIM", [0u8, 0u8, 0u8, 0u8, 0u8, 0u8]);
  for i in 0..stills.len() {
    let Some(images) = image_chunks(stills[i]) else {
      return Err(NotWebp);
    };
    let d = durations_ms[i].max(0).min(16777215);
    let frame = le24(le24(le24(le24(le24([], 0), 0), width - 1), height - 1), d).append(2u8);
    for b in images {
      frame = frame.append(b);
    }
    body = chunk(body, "ANMF", frame);
  }
  let out: u8[] = [82u8, 73u8, 70u8, 70u8];
  out = le32(out, body.len());
  for b in body {
    out = out.append(b);
  }
  return Ok(out);
}
```

### `bits`

The lossless bitstream packs its fields from the low bit of each byte. The reader keeps up to 56 bits in a word and takes from its low end. A read past the end of the chunk fails with `Truncated`. A chunk of fewer than 8 bytes reads as 8 bytes, with zero bytes after its end, as the reference decoder reads it.

```fern
<<bits>>=
struct Bits { at: i32, end: i32, padded: i32, word: i64, count: i32, failed: boolean, error: DecodeError }

function failed(own b: Bits, e: DecodeError): Bits {
  if (b.failed) {
    return b;
  }
  return Bits { ...b, failed: true, error: e, word: 0i64, count: 0 };
}

function refilled(own b: Bits, data: [u8]): Bits {
  let word = b.word;
  let count = b.count;
  let at = b.at;
  while (count <= 48 && at < b.padded) {
    if (at < b.end) {
      word |= data[at] as i64 << count as i64;
    }
    at += 1;
    count += 8;
  }
  return Bits { ...b, at: at, word: word, count: count };
}

// The next `n` bits, from 0 to 24, as a number.
function take(own b: Bits, data: [u8], n: i32): (i32, Bits) {
  let r = if (b.count < n) { refilled(b, data) } else { b };
  if (r.count < n) {
    return (0, failed(r, Truncated));
  }
  let v = (r.word & (1i64 << n as i64) - 1i64) as i32;
  return (v, Bits { ...r, word: r.word >> n as i64, count: r.count - n });
}
```

### `prefix codes`

A prefix code is canonical, as in DEFLATE: the codes of one length are in the order of their symbols, and a code is read from its first bit. `counts` holds the count of codes of each length from 1 to 15, and `symbols` holds the symbols in the order of their codes. A code with one symbol takes no bits. A code with no symbol, or with lengths that do not fill the code space, is not valid.

```fern
<<prefix codes>>=
struct Code { counts: i32[], symbols: i32[] }

function no_code(): Code {
  return Code { counts: [], symbols: [0] };
}

// The counts of codes of each length fill the code space, with no code left
// over and no code short.
function complete(counts: [i32]): boolean {
  let left = 1;
  for n in 1..16 {
    left = left * 2 - counts[n];
    if (left < 0) {
      return false;
    }
  }
  return left == 0;
}

// The canonical code of `lengths`, one length for each symbol.
function code_of(lengths: [i32]): Option[Code] {
  let counts = __alloc_i32(16);
  let used = 0;
  let last = 0;
  for s in 0..lengths.len() {
    let n = lengths[s];
    if (n > 0) {
      counts = counts.with(n, counts[n] + 1);
      used += 1;
      last = s;
    }
  }
  if (used == 0) {
    return None;
  }
  if (used == 1) {
    return Some(Code { counts: counts, symbols: [last] });
  }
  if (!complete(counts)) {
    return None;
  }
  let next = __alloc_i32(16);
  for n in 1..15 {
    next = next.with(n + 1, next[n] + counts[n]);
  }
  let symbols = __alloc_i32(used);
  for s in 0..lengths.len() {
    let n = lengths[s];
    if (n > 0) {
      symbols = symbols.with(next[n], s);
      next = next.with(n, next[n] + 1);
    }
  }
  return Some(Code { counts: counts, symbols: symbols });
}

function code_or_fail(lengths: [i32], own b: Bits): (Code, Bits) {
  match (code_of(lengths)) {
    Some(c) => {
      return (c, b);
    },
    None => {
      return (no_code(), failed(b, BadCode));
    }
  }
}

// The next symbol of the code `c`.
function symbol(own b: Bits, data: [u8], c: Code): (i32, Bits) {
  if (c.symbols.len() == 1) {
    return (c.symbols[0], b);
  }
  let r = if (b.count < 15) { refilled(b, data) } else { b };
  let word = r.word;
  let code = 0;
  let first = 0;
  let index = 0;
  for n in 1..16 {
    code |= (word & 1i64) as i32;
    word >>= 1i64;
    let count = c.counts[n];
    if (code - first < count) {
      if (n > r.count) {
        return (0, failed(r, Truncated));
      }
      return (c.symbols[index + code - first], Bits { ...r, word: r.word >> n as i64, count: r.count - n });
    }
    index += count;
    first = first + count << 1;
    code <<= 1;
  }
  return (0, failed(r, BadCode));
}

// The order in which a code of code lengths gives its own lengths.
function length_order(): i32[] {
  return [17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15];
}

// A simple code: one or two symbols, each with a length of 1.
function simple_code(own b: Bits, data: [u8], alphabet: i32): (Code, Bits) {
  let (two, b1) = take(b, data, 1);
  let (wide, b2) = take(b1, data, 1);
  let (s0, b3) = take(b2, data, 1 + 7 * wide);
  let (s1, b4) = if (two == 1) { take(b3, data, 8) } else { (s0, b3) };
  let lengths = __alloc_i32(alphabet);
  if (s0 < alphabet) {
    lengths = lengths.with(s0, 1);
  }
  if (s1 < alphabet) {
    lengths = lengths.with(s1, 1);
  }
  return code_or_fail(lengths, b4);
}

// The count of code lengths to read: all of them, or the count that the
// stream gives.
function length_budget(own b: Bits, data: [u8], alphabet: i32): (i32, Bits) {
  let (bounded, b1) = take(b, data, 1);
  if (bounded == 0) {
    return (alphabet, b1);
  }
  let (k, b2) = take(b1, data, 3);
  let (m, b3) = take(b2, data, 2 + 2 * k);
  if (m + 2 > alphabet) {
    return (0, failed(b3, BadCode));
  }
  return (m + 2, b3);
}

struct Lengths { lengths: i32[], next: i32, prev: i32 }

// One length, or a repeat of the length `n` from 16 to 18 gives.
function length_step(own l: Lengths, own b: Bits, data: [u8], n: i32): (Lengths, Bits) {
  if (n < 16) {
    let prev = if (n != 0) { n } else { l.prev };
    let at = l.next;
    return (Lengths { lengths: l.lengths.with(at, n), next: at + 1, prev: prev }, b);
  }
  let extra = if (n == 16) { 2 } else if (n == 17) { 3 } else { 7 };
  let (r, b1) = take(b, data, extra);
  let repeat = r + (if (n == 18) { 11 } else { 3 });
  if (repeat > l.lengths.len() - l.next) {
    return (l, failed(b1, BadCode));
  }
  let v = if (n == 16) { l.prev } else { 0 };
  let lengths = l.lengths;
  for k in l.next..l.next + repeat {
    lengths = lengths.with(k, v);
  }
  return (Lengths { lengths: lengths, next: l.next + repeat, prev: l.prev }, b1);
}

// The lengths of a code of `alphabet` symbols, read with the code of code
// lengths `lc`. Code 16 repeats the last length that is not zero, and codes 17
// and 18 repeat zero.
function code_lengths(own b: Bits, data: [u8], lc: Code, alphabet: i32): (i32[], Bits) {
  let (budget, b1) = length_budget(b, data, alphabet);
  let cur = b1;
  let l = Lengths { lengths: __alloc_i32(alphabet), next: 0, prev: 8 };
  while (l.next < alphabet && budget > 0 && !cur.failed) {
    budget -= 1;
    let (n, b2) = symbol(cur, data, lc);
    let (next, b3) = length_step(l, b2, data, n);
    l = next;
    cur = b3;
  }
  return (l.lengths, cur);
}

// A prefix code of `alphabet` symbols.
function read_code(own b: Bits, data: [u8], alphabet: i32): (Code, Bits) {
  let (simple, b1) = take(b, data, 1);
  if (simple == 1) {
    return simple_code(b1, data, alphabet);
  }
  let (n, b2) = take(b1, data, 4);
  let order = length_order();
  let cl = __alloc_i32(19);
  let cur = b2;
  for i in 0..n + 4 {
    let (v, b3) = take(cur, data, 3);
    cl = cl.with(order[i], v);
    cur = b3;
  }
  let (lc, b4) = code_or_fail(cl, cur);
  if (b4.failed) {
    return (no_code(), b4);
  }
  let (lengths, b5) = code_lengths(b4, data, lc, alphabet);
  if (b5.failed) {
    return (no_code(), b5);
  }
  return code_or_fail(lengths, b5);
}
```

### `pixels`

An entropy-coded image is a run of symbols. A green symbol below 256 starts a literal pixel, and red, blue and alpha follow. A symbol from 256 to 279 is the length of a copy from earlier pixels, and a distance follows. A symbol from 280 is an index in the color cache, which holds the pixels that were decoded last, by a hash of their value. Each group of five codes is green, red, blue, alpha and distance.

The main image can have an entropy image: one pixel for each block of `1 << bits` by `1 << bits` pixels, whose red and green bytes select the group of codes for the block. A pixel is a word: alpha, red, green and blue, from the high byte.

```fern
<<pixels>>=
// The blocks of `1 << bits` pixels across `size` pixels.
function blocks(size: i32, bits: i32): i32 {
  return size + (1 << bits) - 1 >> bits;
}

function cache_slot(argb: i32, bits: i32): i32 {
  let v = argb as i64 & 4294967295i64;
  return ((v * 506832829i64 & 4294967295i64) >> (32 - bits) as i64) as i32;
}

// The number of a length or a distance symbol, with its extra bits.
function amount(own b: Bits, data: [u8], sym: i32): (i32, Bits) {
  if (sym < 4) {
    return (sym + 1, b);
  }
  let extra = sym - 2 >> 1;
  let offset = 2 + (sym & 1) << extra;
  let (v, b1) = take(b, data, extra);
  return (offset + v + 1, b1);
}

// The offsets of the 120 nearest pixels: the row above in the high four bits,
// and 8 minus the column to the left in the low four bits.
function near_codes(): i32[] {
  return [24, 7, 23, 25, 40, 6, 39, 41, 22, 26, 38, 42, 56, 5, 55, 57, 21, 27, 54, 58, 37, 43, 72, 4, 71, 73, 20, 28, 53, 59, 70, 74, 36, 44, 88, 69, 75, 52, 60, 3, 87, 89, 19, 29, 86, 90, 35, 45, 68, 76, 85, 91, 51, 61, 104, 2, 103, 105, 18, 30, 102, 106, 34, 46, 84, 92, 67, 77, 101, 107, 50, 62, 120, 1, 119, 121, 83, 93, 17, 31, 100, 108, 66, 78, 118, 122, 33, 47, 117, 123, 49, 63, 99, 109, 82, 94, 0, 116, 124, 65, 79, 16, 32, 98, 110, 48, 115, 125, 81, 95, 64, 114, 126, 97, 111, 80, 113, 127, 96, 112];
}

function distance(width: i32, code: i32, near: [i32]): i32 {
  if (code > 120) {
    return code - 120;
  }
  let c = near[code - 1];
  let d = (c >> 4) * width + 8 - (c & 15);
  return if (d < 1) { 1 } else { d };
}

// The codes of `groups` groups, for a color cache of `cache` entries.
function read_groups(own b: Bits, data: [u8], groups: i32, cache: i32): (Code[], Bits) {
  let codes: Code[] = [];
  let cur = b;
  for g in 0..groups {
    for k in 0..5 {
      let alphabet = if (k == 0) { 280 + cache } else if (k == 4) { 40 } else { 256 };
      let (c, b1) = read_code(cur, data, alphabet);
      cur = b1;
      codes = codes.append(c);
      if (cur.failed) {
        return (codes, cur);
      }
    }
  }
  return (codes, cur);
}

// The largest group in the entropy image `meta`, plus one.
function group_count(meta: [i32]): i32 {
  let most = 0;
  for p in meta {
    most = most.max(p >> 8 & 65535);
  }
  return most + 1;
}

// The bits of the color cache, from 1 to 11, or 0 for no cache.
function cache_bits_of(own b: Bits, data: [u8]): (i32, Bits) {
  let (has, b1) = take(b, data, 1);
  if (has == 0) {
    return (0, b1);
  }
  let (bits, b2) = take(b1, data, 4);
  if (bits < 1 || bits > 11) {
    return (0, failed(b2, BadData));
  }
  return (bits, b2);
}

// The entropy image: one pixel for each block of `1 << bits` pixels, `across`
// blocks in a row. It is empty when one group of codes covers the image.
struct Groups { meta: i32[], bits: i32, across: i32 }

function entropy_image(own b: Bits, data: [u8], width: i32, height: i32, main: boolean): (Groups, Bits) {
  let (has, b1) = if (main) { take(b, data, 1) } else { (0, b) };
  if (has == 0) {
    return (Groups { meta: [], bits: 0, across: 0 }, b1);
  }
  let (k, b2) = take(b1, data, 3);
  let bits = k + 2;
  let across = blocks(width, bits);
  let (meta, b3) = coded_image(b2, data, across, blocks(height, bits), false);
  return (Groups { meta: meta, bits: bits, across: across }, b3);
}

// The index of the first code of the group of the pixel at `x`, `y`.
function group_at(g: Groups, x: i32, y: i32): i32 {
  if (g.meta.len() == 0) {
    return 0;
  }
  return (g.meta[(y >> g.bits) * g.across + (x >> g.bits)] >> 8 & 65535) * 5;
}

// The pixels so far, the color cache, and the position of the next pixel.
struct Decoded { px: i32[], colors: i32[], pos: i32 }

function remembered(own colors: i32[], argb: i32, bits: i32): i32[] {
  if (bits == 0) {
    return colors;
  }
  return colors.with(cache_slot(argb, bits), argb);
}

function literal(own d: Decoded, own b: Bits, data: [u8], codes: [Code], g: i32, green: i32, cache_bits: i32): (Decoded, Bits) {
  let (red, b1) = symbol(b, data, codes[g + 1]);
  let (blue, b2) = symbol(b1, data, codes[g + 2]);
  let (alpha, b3) = symbol(b2, data, codes[g + 3]);
  let argb = alpha << 24 | red << 16 | green << 8 | blue;
  let at = d.pos;
  return (Decoded { px: d.px.with(at, argb), colors: remembered(d.colors, argb, cache_bits), pos: at + 1 }, b3);
}

// A copy of `length` pixels from `dist` pixels back. Each copied pixel goes
// into the color cache.
function copied(own d: Decoded, own b: Bits, data: [u8], distances: Code, green: i32, width: i32, cache_bits: i32, near: [i32]): (Decoded, Bits) {
  let (length, b1) = amount(b, data, green - 256);
  let (dsym, b2) = symbol(b1, data, distances);
  let (dcode, b3) = amount(b2, data, dsym);
  let dist = distance(width, dcode, near);
  if (b3.failed || dist > d.pos || length > d.px.len() - d.pos) {
    return (d, failed(b3, BadData));
  }
  let px = d.px;
  let colors = d.colors;
  for k in d.pos..d.pos + length {
    let argb = px[k - dist];
    px = px.with(k, argb);
    colors = remembered(colors, argb, cache_bits);
  }
  return (Decoded { px: px, colors: colors, pos: d.pos + length }, b3);
}

function cached(own d: Decoded, green: i32, cache_bits: i32): Decoded {
  let argb = d.colors[green - 280];
  let at = d.pos;
  return Decoded { px: d.px.with(at, argb), colors: remembered(d.colors, argb, cache_bits), pos: at + 1 };
}

// The pixels of an entropy-coded image of `width` by `height`. Only the main
// image (`main`) can have an entropy image.
function coded_image(own b: Bits, data: [u8], width: i32, height: i32, main: boolean): (i32[], Bits) {
  let (cache_bits, b1) = cache_bits_of(b, data);
  let (groups, b2) = entropy_image(b1, data, width, height, main);
  if (b2.failed) {
    return ([], b2);
  }
  let cache = if (cache_bits > 0) { 1 << cache_bits } else { 0 };
  let (codes, b3) = read_groups(b2, data, group_count(groups.meta), cache);
  if (b3.failed) {
    return ([], b3);
  }
  let near = near_codes();
  let total = width * height;
  let d = Decoded { px: __alloc_i32(total), colors: __alloc_i32(cache), pos: 0 };
  let cur = b3;
  while (d.pos < total && !cur.failed) {
    let g = group_at(groups, d.pos % width, d.pos / width);
    let (green, b4) = symbol(cur, data, codes[g]);
    if (green < 256) {
      let (next, b5) = literal(d, b4, data, codes, g, green, cache_bits);
      d = next;
      cur = b5;
    } else if (green < 280) {
      let (next, b5) = copied(d, b4, data, codes[g + 4], green, width, cache_bits, near);
      d = next;
      cur = b5;
    } else {
      d = cached(d, green, cache_bits);
      cur = b4;
    }
  }
  return (d.px, cur);
}
```

### `transforms`

The encoder applies up to four transforms, each one time at most, and the decoder undoes them in the opposite order. The predictor and the color transform each keep an image of one pixel for each block of `1 << bits` pixels. The color-indexing transform keeps its palette. After it, one coded pixel can hold 2, 4 or 8 indices. This occurs when the palette has 16, 4 or 2 colors or fewer. Thus the transforms after it see a narrower image.

```fern
<<transforms>>=
enum Transform { Predict(i32, i32, i32[]), Colors(i32, i32, i32[]), SubtractGreen, Index(i32, i32, i32[]) }

// The sum of two pixels, byte by byte, modulo 256.
function add_pixels(a: i32, b: i32): i32 {
  let ag = (a & -16711936) + (b & -16711936);
  let rb = (a & 16711935) + (b & 16711935);
  return ag & -16711936 | rb & 16711935;
}

// The mean of two pixels, byte by byte, rounded down.
function average(a: i32, b: i32): i32 {
  return (((a ^ b) & -16843010) >> 1 & 2147483647) + (a & b);
}

function byte_of(p: i32, shift: i32): i32 {
  return p >> shift & 255;
}

function clamped(v: i32): i32 {
  return if (v < 0) { 0 } else if (v > 255) { 255 } else { v };
}

function select(l: i32, t: i32, tl: i32): i32 {
  let to_l = 0;
  let to_t = 0;
  for shift in [0, 8, 16, 24] {
    to_l += (byte_of(t, shift) - byte_of(tl, shift)).abs();
    to_t += (byte_of(l, shift) - byte_of(tl, shift)).abs();
  }
  return if (to_l < to_t) { l } else { t };
}

function add_subtract_full(a: i32, b: i32, c: i32): i32 {
  let out = 0;
  for shift in [0, 8, 16, 24] {
    out |= clamped(byte_of(a, shift) + byte_of(b, shift) - byte_of(c, shift)) << shift;
  }
  return out;
}

function add_subtract_half(a: i32, b: i32): i32 {
  let out = 0;
  for shift in [0, 8, 16, 24] {
    let x = byte_of(a, shift);
    out |= clamped(x + (x - byte_of(b, shift)) / 2) << shift;
  }
  return out;
}

// The prediction of mode `mode` from the left, top, top-right and top-left
// pixels. Modes 14 and 15 predict as mode 0. The match has one arm for each
// mode.
// fern-lint: allow cyclomatic-complexity
function predicted(mode: i32, l: i32, t: i32, tr: i32, tl: i32): i32 {
  match (mode) {
    1 => {
      return l;
    },
    2 => {
      return t;
    },
    3 => {
      return tr;
    },
    4 => {
      return tl;
    },
    5 => {
      return average(average(l, tr), t);
    },
    6 => {
      return average(l, tl);
    },
    7 => {
      return average(l, t);
    },
    8 => {
      return average(tl, t);
    },
    9 => {
      return average(t, tr);
    },
    10 => {
      return average(average(l, tl), average(t, tr));
    },
    11 => {
      return select(l, t, tl);
    },
    12 => {
      return add_subtract_full(l, t, tl);
    },
    13 => {
      return add_subtract_half(average(l, t), tl);
    },
    _ => {
      return -16777216;
    }
  }
}

// The first pixel predicts black, the rest of the top row predicts from the
// left, and the left column predicts from the top. The top-right pixel of the
// last column is the first pixel of the row.
function unpredict(own px: i32[], width: i32, height: i32, bits: i32, modes: [i32]): i32[] {
  let across = blocks(width, bits);
  for y in 0..height {
    for x in 0..width {
      let pos = y * width + x;
      let prediction = if (y == 0 && x == 0) {
        -16777216
      } else if (y == 0) {
        px[pos - 1]
      } else if (x == 0) {
        px[pos - width]
      } else {
        predicted(modes[(y >> bits) * across + (x >> bits)] >> 8 & 15, px[pos - 1], px[pos - width], px[pos - width + 1], px[pos - width - 1])
      };
      px = px.with(pos, add_pixels(px[pos], prediction));
    }
  }
  return px;
}

// A signed byte.
function signed(v: i32): i32 {
  let b = v & 255;
  return if (b > 127) { b - 256 } else { b };
}

function delta(t: i32, c: i32): i32 {
  return signed(t) * signed(c) >> 5;
}

function uncolor(own px: i32[], width: i32, height: i32, bits: i32, elements: [i32]): i32[] {
  let across = blocks(width, bits);
  for y in 0..height {
    for x in 0..width {
      let pos = y * width + x;
      let e = elements[(y >> bits) * across + (x >> bits)];
      let p = px[pos];
      let green = byte_of(p, 8);
      let red = byte_of(p, 16) + delta(e, green) & 255;
      let blue = byte_of(p, 0) + delta(e >> 8, green) + delta(e >> 16, red) & 255;
      px = px.with(pos, p & -16711936 | red << 16 | blue);
    }
  }
  return px;
}

function add_green(own px: i32[]): i32[] {
  for pos in 0..px.len() {
    let p = px[pos];
    let green = byte_of(p, 8);
    let red = byte_of(p, 16) + green & 255;
    let blue = byte_of(p, 0) + green & 255;
    px = px.with(pos, p & -16711936 | red << 16 | blue);
  }
  return px;
}

// The pixels of the palette indices in `packed`, `1 << bits` indices in the
// green byte of each packed pixel. An index past the palette gives
// transparent black.
function unindex(packed: [i32], width: i32, height: i32, bits: i32, palette: [i32]): i32[] {
  let across = blocks(width, bits);
  let per = 8 >> bits;
  let mask = (1 << per) - 1;
  let out = __alloc_i32(width * height);
  for y in 0..height {
    for x in 0..width {
      let p = packed[y * across + (x >> bits)];
      let index = byte_of(p, 8) >> (x & (1 << bits) - 1) * per & mask;
      out = out.with(y * width + x, if (index < palette.len()) { palette[index] } else { 0 });
    }
  }
  return out;
}

function undone(own px: i32[], t: Transform, height: i32): i32[] {
  match (t) {
    Predict(width, bits, modes) => {
      return unpredict(px, width, height, bits, modes);
    },
    Colors(width, bits, elements) => {
      return uncolor(px, width, height, bits, elements);
    },
    SubtractGreen => {
      return add_green(px);
    },
    Index(width, bits, palette) => {
      return unindex(px, width, height, bits, palette);
    }
  }
}

// The palette of the color-indexing transform. Each color after the first is
// coded as its difference from the color before.
function palette_of(own colors: i32[]): i32[] {
  for i in 1..colors.len() {
    colors = colors.with(i, add_pixels(colors[i], colors[i - 1]));
  }
  return colors;
}

// One transform of an image `width` pixels across, and the width of the image
// after it.
function read_transform(own b: Bits, data: [u8], kind: i32, width: i32, height: i32): (Transform, i32, Bits) {
  if (kind == 2) {
    return (SubtractGreen, width, b);
  }
  if (kind == 3) {
    let (n, b1) = take(b, data, 8);
    let size = n + 1;
    let (colors, b2) = coded_image(b1, data, size, 1, false);
    let bits = if (size <= 2) { 3 } else if (size <= 4) { 2 } else if (size <= 16) { 1 } else { 0 };
    return (Index(width, bits, palette_of(colors)), blocks(width, bits), b2);
  }
  let (k, b1) = take(b, data, 3);
  let bits = k + 2;
  let (sub, b2) = coded_image(b1, data, blocks(width, bits), blocks(height, bits), false);
  if (kind == 0) {
    return (Predict(width, bits, sub), width, b2);
  }
  return (Colors(width, bits, sub), width, b2);
}
```

### `decoding`

The `VP8L` chunk starts with the byte 0x2F. Then it has 14 bits of the width minus one and 14 bits of the height minus one. One bit tells if alpha is used, and three bits hold the version, 0. The transforms follow, each after a bit of 1, and then the main image.

```fern
<<decoding>>=
struct Located { start: i32, canvas_width: i32, canvas_height: i32 }

// The position of the first `VP8 ` or `VP8L` chunk after the `VP8X` chunk.
// Each chunk before it must be complete and inside the size `riff` of the RIFF
// header.
function after_extended(file: [u8], riff: i32): Result[i32, DecodeError] {
  let at = 30;
  while (true) {
    if (at + 8 > file.len()) {
      return Err(Truncated);
    }
    let size = u32_at(file, at + 4);
    if (size < 0 || size > riff - at - size % 2) {
      return Err(BadHeader);
    }
    if (tag_at(file, at, "VP8 ") || tag_at(file, at, "VP8L")) {
      return Ok(at);
    }
    if (size + size % 2 > file.len() - at - 8) {
      return Err(Truncated);
    }
    at += 8 + size + size % 2;
  }
}

// The start of the data of the `VP8L` chunk at `at`.
function lossless_at(file: [u8], at: i32, riff: i32): Result[i32, DecodeError] {
  if (at + 8 > file.len()) {
    return Err(Truncated);
  }
  if (tag_at(file, at, "VP8 ")) {
    return Err(Unsupported);
  }
  if (!tag_at(file, at, "VP8L")) {
    return Err(BadHeader);
  }
  let size = u32_at(file, at + 4);
  if (size < 0 || size > riff - 12) {
    return Err(BadHeader);
  }
  if (size > file.len() - at - 8) {
    return Err(Truncated);
  }
  return Ok(at + 8);
}

// The size of the RIFF data, which must hold a chunk header and be inside the
// file.
function riff_size(file: [u8]): Result[i32, DecodeError] {
  if (!is_webp(file)) {
    return Err(NotWebpFile);
  }
  let riff = u32_at(file, 4);
  if (riff < 12) {
    return Err(BadHeader);
  }
  if (riff > file.len() - 8) {
    return Err(Truncated);
  }
  return Ok(riff);
}

// The start of the data of the `VP8L` chunk of a still file, and the canvas of
// its `VP8X` chunk, or 0 by 0 without one. The chunks must be inside the size
// that the RIFF header gives.
function lossless_chunk(file: [u8]): Result[Located, DecodeError] {
  let riff = riff_size(file)?;
  if (!tag_at(file, 12, "VP8X")) {
    return Ok(Located { start: lossless_at(file, 12, riff)?, canvas_width: 0, canvas_height: 0 });
  }
  if (u32_at(file, 16) != 10) {
    return Err(BadHeader);
  }
  if (file.len() < 30) {
    return Err(Truncated);
  }
  if ((file[20] as i32 & 2) != 0) {
    return Err(Animated);
  }
  let at = after_extended(file, riff)?;
  return Ok(Located { start: lossless_at(file, at, riff)?, canvas_width: le24_at(file, 24) + 1, canvas_height: le24_at(file, 27) + 1 });
}

function le24_at(b: [u8], at: i32): i32 {
  return b[at] as i32 | b[at + 1] as i32 << 8 | b[at + 2] as i32 << 16;
}

function rgba(px: [i32]): u8[] {
  let out = __alloc_u8(px.len() * 4);
  for i in 0..px.len() {
    let p = px[i];
    out = out.with(i * 4, byte_of(p, 16) as u8).with(i * 4 + 1, byte_of(p, 8) as u8).with(i * 4 + 2, byte_of(p, 0) as u8).with(i * 4 + 3, byte_of(p, 24) as u8);
  }
  return out;
}

// The pixels before the transforms, which are undone from the last to the
// first.
function undone_all(own px: i32[], transforms: [Transform], height: i32): i32[] {
  let i = transforms.len();
  while (i > 0) {
    i -= 1;
    px = undone(px, transforms[i], height);
  }
  return px;
}

// The transforms, each after a bit of 1, and the width of the coded image
// after them. A transform of a kind that came before fails with `BadData`.
function read_transforms(own b: Bits, data: [u8], width: i32, height: i32): (Transform[], i32, Bits) {
  let transforms: Transform[] = [];
  let seen = 0;
  let coded_width = width;
  let (more, b1) = take(b, data, 1);
  let cur = b1;
  while (more == 1 && !cur.failed) {
    let (kind, b2) = take(cur, data, 2);
    if ((seen >> kind & 1) == 1) {
      return (transforms, coded_width, failed(b2, BadData));
    }
    seen |= 1 << kind;
    let (t, next_width, b3) = read_transform(b2, data, kind, coded_width, height);
    transforms = transforms.append(t);
    coded_width = next_width;
    let (again, b4) = take(b3, data, 1);
    more = again;
    cur = b4;
  }
  return (transforms, coded_width, cur);
}

// The image of the still lossless WebP file `file`. As in the reference
// decoder, the bitstream can read past the end of its chunk, to the end of the
// file.
pub function decode(file: [u8]): Result[Image, DecodeError] {
  let found = lossless_chunk(file)?;
  return lossless_image(file, found.start, file.len(), found.canvas_width, found.canvas_height);
}

// The image of the `VP8L` data that starts at `start`, with the bytes before
// `end`. Its size must be `want_width` by `want_height`, unless that is 0 by 0.
function lossless_image(file: [u8], start: i32, end: i32, want_width: i32, want_height: i32): Result[Image, DecodeError] {
  if (end - start < 5 || file[start] != 47u8) {
    return Err(BadHeader);
  }
  let b = Bits { at: start + 1, end: end, padded: end.max(start + 8), word: 0i64, count: 0, failed: false, error: BadHeader };
  let (w, b1) = take(b, file, 14);
  let (h, b2) = take(b1, file, 14);
  let (_, b3) = take(b2, file, 1);
  let (version, b4) = take(b3, file, 3);
  if (version != 0) {
    return Err(BadHeader);
  }
  let width = w + 1;
  let height = h + 1;
  if (want_width > 0 && (want_width != width || want_height != height)) {
    return Err(BadHeader);
  }
  let (transforms, coded_width, cur) = read_transforms(b4, file, width, height);
  if (cur.failed) {
    return Err(cur.error);
  }
  let (px, b9) = coded_image(cur, file, coded_width, height, true);
  if (b9.failed) {
    return Err(b9.error);
  }
  return Ok(Image { width: width, height: height, pixels: rgba(undone_all(px, transforms, height)) });
}
```

### `animated decoding`

An `ANMF` chunk starts with five fields of 24 bits and a byte of flags. The fields are the place of the frame in units of two pixels, the width minus one, the height minus one, and the duration. Bit 1 of the flags turns the blend off, and bit 0 disposes of the frame. The image chunks of the frame follow.

The canvas has the same bytes as the canvas of the animation decoder of libwebp, so the frames follow its rules:

- A key frame goes on a transparent canvas, with no blend. The first frame is a key frame. A frame that covers the canvas, without alpha or without the blend flag, is a key frame. A frame after a disposed frame that covered the canvas or was a key frame is a key frame.
- A blend uses the integer formula of libwebp for pixels that are not premultiplied. A pixel with an alpha of 255 replaces the pixel of the canvas, and a pixel with an alpha of 0 leaves it.
- A pixel inside the rectangle of a disposed frame replaces the transparent pixel of the canvas, with no blend.

```fern
<<animated decoding>>=
struct Placed { image: Image, x: i32, y: i32, duration_ms: i32, flags: i32, alpha: boolean }

function covers(f: Placed, width: i32, height: i32): boolean {
  return f.image.width == width && f.image.height == height;
}

function inside(f: Placed, x: i32, y: i32): boolean {
  return x >= f.x && x < f.x + f.image.width && y >= f.y && y < f.y + f.image.height;
}

// The image of the first `VP8L` chunk from `at` to `end`, which must be
// `width` by `height`, and whether the image uses alpha. `ALPH` chunks before
// it are skipped.
function frame_image(file: [u8], at: i32, end: i32, width: i32, height: i32): Result[(Image, boolean), DecodeError] {
  let inner = at;
  while (inner + 8 <= end) {
    let size = u32_at(file, inner + 4);
    if (size < 0 || size > end - inner - 8) {
      return Err(BadHeader);
    }
    if (tag_at(file, inner, "VP8 ")) {
      return Err(Unsupported);
    }
    if (tag_at(file, inner, "VP8L")) {
      let image = lossless_image(file, inner + 8, inner + 8 + size, width, height)?;
      return Ok((image, (file[inner + 12] as i32 >> 4 & 1) == 1));
    }
    if (!tag_at(file, inner, "ALPH")) {
      return Err(BadHeader);
    }
    inner += 8 + size + size % 2;
  }
  return Err(BadHeader);
}

// The frame of the `ANMF` chunk at `at`, with `size` bytes of data, on a
// canvas of `width` by `height`.
function placed_frame(file: [u8], at: i32, size: i32, width: i32, height: i32): Result[Placed, DecodeError] {
  if (size < 24) {
    return Err(BadHeader);
  }
  let d = at + 8;
  let x = le24_at(file, d) * 2;
  let y = le24_at(file, d + 3) * 2;
  let w = le24_at(file, d + 6) + 1;
  let h = le24_at(file, d + 9) + 1;
  if (x + w > width || y + h > height) {
    return Err(BadHeader);
  }
  let (image, alpha) = frame_image(file, d + 16, d + size, w, h)?;
  return Ok(Placed { image: image, x: x, y: y, duration_ms: le24_at(file, d + 12), flags: file[d + 15] as i32, alpha: alpha });
}

// The canvas with the pixel `p` of `px` blended over its pixel at `at`.
function blended(own canvas: u8[], at: i32, px: [u8], p: i32): u8[] {
  let src_a = px[p + 3] as i32;
  if (src_a == 0) {
    return canvas;
  }
  if (src_a == 255) {
    return replaced(canvas, at, px, p);
  }
  let dst_a = canvas[at + 3] as i32 * (256 - src_a) >> 8;
  let a = src_a + dst_a;
  let scale = (1i64 << 24i64) / (a as i64);
  let r = (px[p] as i32 * src_a + canvas[at] as i32 * dst_a) as i64 * scale >> 24i64;
  let g = (px[p + 1] as i32 * src_a + canvas[at + 1] as i32 * dst_a) as i64 * scale >> 24i64;
  let b = (px[p + 2] as i32 * src_a + canvas[at + 2] as i32 * dst_a) as i64 * scale >> 24i64;
  return canvas.with(at, r as u8).with(at + 1, g as u8).with(at + 2, b as u8).with(at + 3, a as u8);
}

function replaced(own canvas: u8[], at: i32, px: [u8], p: i32): u8[] {
  return canvas.with(at, px[p]).with(at + 1, px[p + 1]).with(at + 2, px[p + 2]).with(at + 3, px[p + 3]);
}

// The canvas, `width` pixels wide, with the frame `f` drawn on it. A pixel
// inside the rectangle of `no_blend` is not blended.
function drawn(own canvas: u8[], width: i32, f: Placed, blend: boolean, no_blend: Option[Placed]): u8[] {
  let px = f.image.pixels;
  for row in 0..f.image.height {
    for col in 0..f.image.width {
      let p = (row * f.image.width + col) * 4;
      let at = ((f.y + row) * width + f.x + col) * 4;
      let mix = blend && match (no_blend) {
        Some(d) => !inside(d, f.x + col, f.y + row),
        None => true,
      };
      canvas = if (mix) { blended(canvas, at, px, p) } else { replaced(canvas, at, px, p) };
    }
  }
  return canvas;
}

// The canvas, `width` pixels wide, with the rectangle of the frame `f` made
// transparent.
function disposed(own canvas: u8[], width: i32, f: Placed): u8[] {
  for row in 0..f.image.height {
    let at = ((f.y + row) * width + f.x) * 4;
    for k in at..at + f.image.width * 4 {
      canvas = canvas.with(k, 0u8);
    }
  }
  return canvas;
}

function still_animation(file: [u8]): Result[Animation, DecodeError] {
  let image = decode(file)?;
  return Ok(Animation { width: image.width, height: image.height, loop_count: 0, frames: [Frame { image: image, duration_ms: 0 }] });
}

// The canvas after the frames so far, the last frame, and whether that frame
// was a key frame.
struct Canvas { width: i32, height: i32, pixels: u8[], last: Option[Placed], last_key: boolean }

function disposes(f: Placed): boolean {
  return (f.flags & 1) != 0;
}

function is_key(c: Canvas, f: Placed, blend: boolean): boolean {
  return match (c.last) {
    None => true,
    Some(prev) => (!f.alpha || !blend) && covers(f, c.width, c.height) || disposes(prev) && (covers(prev, c.width, c.height) || c.last_key),
  };
}

// The canvas with the frame `f` drawn on it.
function with_frame(own c: Canvas, f: Placed): Canvas {
  let blend = (f.flags & 2) == 0;
  let key = is_key(c, f, blend);
  let gone = match (c.last) {
    Some(prev) => if (disposes(prev)) { Some(prev) } else { None },
    None => None,
  };
  let pixels = c.pixels;
  if (key) {
    pixels = drawn(__alloc_u8(c.width * c.height * 4), c.width, f, false, None);
  } else {
    if let Some(prev) = gone {
      pixels = disposed(pixels, c.width, prev);
    }
    pixels = drawn(pixels, c.width, f, blend, gone);
  }
  return Canvas { ...c, pixels: pixels, last: Some(f), last_key: key };
}

// The size of the data of the chunk at `at`. The chunk must be inside the size
// `riff` of the RIFF header and inside the file.
function chunk_size(file: [u8], at: i32, riff: i32): Result[i32, DecodeError] {
  if (at + 8 > file.len()) {
    return Err(Truncated);
  }
  let size = u32_at(file, at + 4);
  if (size < 0 || size > riff - at - size % 2) {
    return Err(BadHeader);
  }
  if (size + size % 2 > file.len() - at - 8) {
    return Err(Truncated);
  }
  return Ok(size);
}

// The canvas of the `VP8X` chunk of an animated file.
function animation_canvas(file: [u8]): Result[Canvas, DecodeError] {
  if (u32_at(file, 16) != 10) {
    return Err(BadHeader);
  }
  let width = le24_at(file, 24) + 1;
  let height = le24_at(file, 27) + 1;
  if (width as i64 * (height as i64) > 268435456i64) {
    return Err(BadHeader);
  }
  return Ok(Canvas { width: width, height: height, pixels: __alloc_u8(width * height * 4), last: None, last_key: false });
}

// The canvas and the frames so far, and the loop count of the `ANIM` chunk,
// or -1 before it.
struct Reading { canvas: Canvas, frames: Frame[], loop_count: i32 }

// The reading after the chunk at `at`, with `size` bytes of data.
function with_chunk(own r: Reading, file: [u8], at: i32, size: i32): Result[Reading, DecodeError] {
  if (tag_at(file, at, "ANIM")) {
    if (size < 6) {
      return Err(BadHeader);
    }
    return Ok(Reading { ...r, loop_count: file[at + 12] as i32 | file[at + 13] as i32 << 8 });
  }
  if (tag_at(file, at, "ANMF")) {
    if (r.loop_count < 0) {
      return Err(BadHeader);
    }
    let f = placed_frame(file, at, size, r.canvas.width, r.canvas.height)?;
    let c = with_frame(r.canvas, f);
    let frame = Frame { image: Image { width: c.width, height: c.height, pixels: c.pixels }, duration_ms: f.duration_ms };
    return Ok(Reading { ...r, canvas: c, frames: r.frames.append(frame) });
  }
  if (is_image(file, at)) {
    return Err(BadHeader);
  }
  return Ok(r);
}

// The frames of the lossless WebP file `file`, animated or still.
pub function decode_animation(file: [u8]): Result[Animation, DecodeError] {
  let riff = riff_size(file)?;
  if (!tag_at(file, 12, "VP8X") || file.len() < 30 || (file[20] as i32 & 2) == 0) {
    return still_animation(file);
  }
  let r = Reading { canvas: animation_canvas(file)?, frames: [], loop_count: -1 };
  let at = 30;
  while (at + 8 <= riff + 8) {
    let size = chunk_size(file, at, riff)?;
    r = with_chunk(r, file, at, size)?;
    at += 8 + size + size % 2;
  }
  if (r.frames.len() == 0) {
    return Err(BadHeader);
  }
  return Ok(Animation { width: r.canvas.width, height: r.canvas.height, loop_count: r.loop_count, frames: r.frames });
}
```
