# WebP files

This module decodes WebP files to pixels, lossy or lossless, still or animated. It also makes an animated WebP file from still WebP files, one for each frame.

## Model

A WebP file is a RIFF file: the tag `RIFF`, the size, the tag `WEBP`, and chunks. A still image is one `VP8 ` chunk (lossy) or one `VP8L` chunk (lossless). A lossy image with alpha has a `VP8X` chunk, then an `ALPH` chunk and a `VP8 ` chunk.

An animated file has a `VP8X` chunk with the animation flag, an `ANIM` chunk with the loop count, and an `ANMF` chunk for each frame. An `ANMF` chunk holds the place, the size and the duration of the frame, and the image chunks of a still file. `animate` takes these chunks from each still file, so any encoder of still WebP can make the frames.

## Contracts

Each still file is `width` by `height`. The frames cover the canvas, and each frame replaces the frame before: it does not blend. The animation loops for ever. A duration is in milliseconds, from 0 to 16777215.

No frames, or a count of durations that is not the count of frames, gives `NoFrames`. A file that is not a still WebP file gives `NotWebp`. A width or a height that is not from 1 to 16384 gives `InvalidDimensions`.

`decode` reads a still file. A `VP8L` chunk holds the lossless format of RFC 9649. A `VP8 ` chunk holds a key frame of the lossy format of RFC 6386, and an `ALPH` chunk before it gives its alpha. `decode` gives an `Image` with four bytes for each pixel: red, green, blue and alpha. A file without alpha gives 255 for each alpha byte.

An animated file gives `Animated`. A file that is not WebP gives `NotWebpFile`. An image chunk that is not valid gives `BadHeader`, `BadCode`, `BadData` or `Truncated`.

`decode` gives the pixels of `dwebp` of libwebp: the same loop filter, the same upsampling of the chroma, and the same conversion to RGB. It accepts the files that libwebp accepts, with the exception below. Thus the bitstream can read past the end of its chunk, to the end of the file. An `ALPH` chunk has no effect on a lossless image. The last `ALPH` chunk before a `VP8 ` chunk gives the alpha, also when the `VP8X` chunk has no alpha flag.

These are the differences from libwebp, each for a file that no encoder writes:

- A lossless `ALPH` stream that reads past the end of its chunk gives `Truncated`. libwebp accepts some of these streams, and reads the last bytes of the chunk again.
- A coefficient outside the range of the format gives the pixels of the C code of libwebp (`dwebp -noasm`). The SIMD code of libwebp computes in 16 bits and can give other pixels.

`decode_animation` reads an animated file. It gives the canvas after each frame, as the animation decoder of libwebp gives it.

The canvas is transparent at the start. A frame with the blend flag is blended over the canvas, with its alpha. A frame without the flag replaces its rectangle of the canvas. A frame with the dispose flag makes its rectangle transparent before the next frame. The background color of the `ANIM` chunk has no effect, as in libwebp. A loop count of 0 means that the animation loops for ever.

`decode_animation` also reads a still file, as one frame with a duration of 0. The image chunk of a frame cannot read past the end of its chunk. A frame can have one `ALPH` chunk before a `VP8 ` chunk. These files give `BadHeader`:

- a frame that is not inside the canvas,
- a frame before the `ANIM` chunk,
- a frame with an `ALPH` chunk before a `VP8L` chunk, or with two `ALPH` chunks,
- an animation with no frame,
- a canvas of more than 2^28 pixels.

## Compatibility

| Part of the format | Status |
| --- | --- |
| RIFF container, `VP8X`, and `ICCP`, `EXIF`, `XMP ` and unknown chunks | Read. The metadata chunks are skipped. |
| Lossless still image (`VP8L`) | Decoded by `decode`. |
| Lossy still image (`VP8 `), key frames | Decoded by `decode`. |
| Alpha of a lossy image (`ALPH`), raw or lossless, with each filter | Decoded by `decode`. |
| Animation (`ANIM`, `ANMF` with `VP8L` frames or `VP8 ` frames with alpha) | Decoded by `decode_animation`. Written by `animate`. |
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
// This module decodes WebP files and makes animated WebP files.
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

<<lossy bits>>

<<lossy tables>>

<<lossy header>>

<<lossy modes>>

<<lossy tokens>>

<<lossy prediction>>

<<lossy loop filter>>

<<lossy decoding>>

<<alpha>>

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

pub enum DecodeError { NotWebpFile, Animated, BadHeader, BadCode, BadData, Truncated }

pub function (e: DecodeError) message(): string {
  match (e) {
    NotWebpFile => {
      return "webp: not a WebP file";
    },
    Animated => {
      return "webp: the file is animated, so decode_animation must read it";
    },
    BadHeader => {
      return "webp: the header of the image is not valid";
    },
    BadCode => {
      return "webp: a prefix code of the lossless image is not valid";
    },
    BadData => {
      return "webp: the data of the image is not valid";
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
// The image chunk of a still file, the canvas of its `VP8X` chunk or 0 by 0
// without one, and the last `ALPH` chunk before the image chunk, or -1.
struct Located { at: i32, canvas_width: i32, canvas_height: i32, alpha: i32 }

// The position of the first `VP8 ` or `VP8L` chunk after the `VP8X` chunk,
// and of the last `ALPH` chunk before it, or -1. Each chunk before the image
// chunk must be complete and inside the size `riff` of the RIFF header.
function after_extended(file: [u8], riff: i32): Result[(i32, i32), DecodeError] {
  let at = 30;
  let alpha = -1;
  while (true) {
    if (at + 8 > file.len()) {
      return Err(Truncated);
    }
    let size = u32_at(file, at + 4);
    if (size < 0 || size > riff - at - size % 2) {
      return Err(BadHeader);
    }
    if (tag_at(file, at, "VP8 ") || tag_at(file, at, "VP8L")) {
      return Ok((at, alpha));
    }
    if (size + size % 2 > file.len() - at - 8) {
      return Err(Truncated);
    }
    if (tag_at(file, at, "ALPH")) {
      alpha = at;
    }
    at += 8 + size + size % 2;
  }
}

// The `VP8 ` or `VP8L` chunk at `at`, which must be inside the file and inside
// the size `riff` of the RIFF header.
function image_at(file: [u8], at: i32, riff: i32): Result[i32, DecodeError] {
  if (at + 8 > file.len()) {
    return Err(Truncated);
  }
  if (!tag_at(file, at, "VP8 ") && !tag_at(file, at, "VP8L")) {
    return Err(BadHeader);
  }
  let size = u32_at(file, at + 4);
  if (size < 0 || size > riff - 12) {
    return Err(BadHeader);
  }
  if (size > file.len() - at - 8) {
    return Err(Truncated);
  }
  return Ok(at);
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

// The image chunk of a still file. The chunks must be inside the size that
// the RIFF header gives.
function image_chunk(file: [u8]): Result[Located, DecodeError] {
  let riff = riff_size(file)?;
  if (!tag_at(file, 12, "VP8X")) {
    return Ok(Located { at: image_at(file, 12, riff)?, canvas_width: 0, canvas_height: 0, alpha: -1 });
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
  let (at, alpha) = after_extended(file, riff)?;
  return Ok(Located { at: image_at(file, at, riff)?, canvas_width: le24_at(file, 24) + 1, canvas_height: le24_at(file, 27) + 1, alpha: alpha });
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

// The image of the still WebP file `file`. As in the reference decoder, the
// bitstream can read past the end of its chunk, to the end of the file.
pub function decode(file: [u8]): Result[Image, DecodeError] {
  let found = image_chunk(file)?;
  let data = found.at + 8;
  if (tag_at(file, found.at, "VP8L")) {
    return lossless_image(file, data, file.len(), found.canvas_width, found.canvas_height);
  }
  return lossy_image(file, data, u32_at(file, found.at + 4), file.len(), found.alpha, found.canvas_width, found.canvas_height);
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

### `lossy bits`

The lossy format of RFC 6386 codes its fields with a boolean entropy decoder. Each decision has a probability of a 0, from 0 to 255.

The decoder has the form of libwebp: `range` is the range minus one, and `value` holds `bits` bits more than the 8 bits that the next decision reads. The decoder reads a byte only when it needs one. After the end of its partition it reads zero bytes and sets `eof`. Then the decode fails, as in libwebp.

```fern
<<lossy bits>>=
struct Bool { at: i32, end: i32, value: i32, range: i32, bits: i32, eof: boolean }

function bool_reader(data: [u8], at: i32, end: i32): Bool {
  return loaded(Bool { at: at, end: end, value: 0, range: 254, bits: -8, eof: false }, data);
}

function loaded(own r: Bool, data: [u8]): Bool {
  if (r.at < r.end) {
    return Bool { ...r, at: r.at + 1, value: r.value << 8 | data[r.at] as i32, bits: r.bits + 8 };
  }
  if (!r.eof) {
    return Bool { ...r, value: r.value << 8, bits: r.bits + 8, eof: true };
  }
  return Bool { ...r, bits: 0 };
}

// One decision, with the probability `prob` of a 0.
function bit(own r: Bool, data: [u8], prob: i32): (i32, Bool) {
  let s = if (r.bits < 0) { loaded(r, data) } else { r };
  let split = s.range * prob >> 8;
  if (s.value >> s.bits > split) {
    let range = s.range - split;
    let shift = range.leading_zeros() - 24;
    return (1, Bool { ...s, value: s.value - (split + 1 << s.bits), range: (range << shift) - 1, bits: s.bits - shift });
  }
  let range = split + 1;
  let shift = range.leading_zeros() - 24;
  return (0, Bool { ...s, range: (range << shift) - 1, bits: s.bits - shift });
}

// A number of `n` bits, from the high bit, each with a probability of one
// half.
function bool_value(own r: Bool, data: [u8], n: i32): (i32, Bool) {
  let v = 0;
  let cur = r;
  for _ in 0..n {
    let (b, next) = bit(cur, data, 128);
    v = v << 1 | b;
    cur = next;
  }
  return (v, cur);
}

// A number of `n` bits and a sign bit after it.
function bool_signed(own r: Bool, data: [u8], n: i32): (i32, Bool) {
  let (v, r1) = bool_value(r, data, n);
  let (negative, r2) = bit(r1, data, 128);
  return (if (negative == 1) { -v } else { v }, r2);
}

// A flag, and a signed number of `n` bits when the flag is 1, or 0.
function bool_optional(own r: Bool, data: [u8], n: i32): (i32, Bool) {
  let (present, r1) = bit(r, data, 128);
  if (present == 0) {
    return (0, r1);
  }
  return bool_signed(r1, data, n);
}
```

### `lossy tables`

The tables of RFC 6386 that the decoder reads. The token probabilities are in the order of plane type, band, context and token: 4 by 8 by 3 by 11 numbers.

```fern
<<lossy tables>>=
// The probability that each token probability changes, by plane type, band, context and token (RFC 6386, section 13.4).
function coeff_update_probs(): i32[] {
  return [
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 176, 246, 255, 255, 255, 255, 255, 255, 255, 255, 255, 223, 241, 252, 255,
    255, 255, 255, 255, 255, 255, 255, 249, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 244, 252, 255, 255, 255,
    255, 255, 255, 255, 255, 234, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 253, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 246, 254, 255, 255, 255, 255, 255, 255, 255, 255, 239, 253, 254, 255, 255, 255, 255, 255, 255, 255,
    255, 254, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 248, 254, 255, 255, 255, 255, 255, 255, 255, 255, 251,
    255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 253, 254,
    255, 255, 255, 255, 255, 255, 255, 255, 251, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 254, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 254, 253, 255, 254, 255, 255, 255, 255, 255, 255, 250, 255, 254, 255, 254, 255, 255,
    255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    217, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 225, 252, 241, 253, 255, 255, 254, 255, 255, 255, 255, 234, 250,
    241, 250, 253, 255, 253, 254, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 223, 254, 254, 255,
    255, 255, 255, 255, 255, 255, 255, 238, 253, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 248, 254, 255, 255, 255,
    255, 255, 255, 255, 255, 249, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 247, 254, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 252,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 254,
    255, 255, 255, 255, 255, 255, 255, 255, 253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 254, 253, 255, 255, 255, 255, 255, 255, 255, 255, 250, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    186, 251, 250, 255, 255, 255, 255, 255, 255, 255, 255, 234, 251, 244, 254, 255, 255, 255, 255, 255, 255, 255, 251, 251,
    243, 253, 254, 255, 254, 255, 255, 255, 255, 255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 236, 253, 254, 255,
    255, 255, 255, 255, 255, 255, 255, 251, 253, 253, 254, 254, 255, 255, 255, 255, 255, 255, 255, 254, 254, 255, 255, 255,
    255, 255, 255, 255, 255, 254, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    248, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 250, 254, 252, 254, 255, 255, 255, 255, 255, 255, 255, 248, 254,
    249, 253, 255, 255, 255, 255, 255, 255, 255, 255, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255, 246, 253, 253, 255,
    255, 255, 255, 255, 255, 255, 255, 252, 254, 251, 254, 254, 255, 255, 255, 255, 255, 255, 255, 254, 252, 255, 255, 255,
    255, 255, 255, 255, 255, 248, 254, 253, 255, 255, 255, 255, 255, 255, 255, 255, 253, 255, 254, 254, 255, 255, 255, 255,
    255, 255, 255, 255, 251, 254, 255, 255, 255, 255, 255, 255, 255, 255, 245, 251, 254, 255, 255, 255, 255, 255, 255, 255,
    255, 253, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 251, 253, 255, 255, 255, 255, 255, 255, 255, 255, 252,
    253, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 252, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 249, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 254, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 253, 255, 255, 255, 255, 255, 255, 255, 255, 250, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
  ];
}

// The token probabilities of a key frame before the changes (RFC 6386, section 13.5).
function coeff_default_probs(): i32[] {
  return [
    128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
    128, 128, 128, 128, 128, 128, 128, 128, 128, 253, 136, 254, 255, 228, 219, 128, 128, 128, 128, 128, 189, 129, 242, 255,
    227, 213, 255, 219, 128, 128, 128, 106, 126, 227, 252, 214, 209, 255, 255, 128, 128, 128, 1, 98, 248, 255, 236, 226,
    255, 255, 128, 128, 128, 181, 133, 238, 254, 221, 234, 255, 154, 128, 128, 128, 78, 134, 202, 247, 198, 180, 255, 219,
    128, 128, 128, 1, 185, 249, 255, 243, 255, 128, 128, 128, 128, 128, 184, 150, 247, 255, 236, 224, 128, 128, 128, 128,
    128, 77, 110, 216, 255, 236, 230, 128, 128, 128, 128, 128, 1, 101, 251, 255, 241, 255, 128, 128, 128, 128, 128, 170,
    139, 241, 252, 236, 209, 255, 255, 128, 128, 128, 37, 116, 196, 243, 228, 255, 255, 255, 128, 128, 128, 1, 204, 254,
    255, 245, 255, 128, 128, 128, 128, 128, 207, 160, 250, 255, 238, 128, 128, 128, 128, 128, 128, 102, 103, 231, 255, 211,
    171, 128, 128, 128, 128, 128, 1, 152, 252, 255, 240, 255, 128, 128, 128, 128, 128, 177, 135, 243, 255, 234, 225, 128,
    128, 128, 128, 128, 80, 129, 211, 255, 194, 224, 128, 128, 128, 128, 128, 1, 1, 255, 128, 128, 128, 128, 128, 128,
    128, 128, 246, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128, 255, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
    198, 35, 237, 223, 193, 187, 162, 160, 145, 155, 62, 131, 45, 198, 221, 172, 176, 220, 157, 252, 221, 1, 68, 47,
    146, 208, 149, 167, 221, 162, 255, 223, 128, 1, 149, 241, 255, 221, 224, 255, 255, 128, 128, 128, 184, 141, 234, 253,
    222, 220, 255, 199, 128, 128, 128, 81, 99, 181, 242, 176, 190, 249, 202, 255, 255, 128, 1, 129, 232, 253, 214, 197,
    242, 196, 255, 255, 128, 99, 121, 210, 250, 201, 198, 255, 202, 128, 128, 128, 23, 91, 163, 242, 170, 187, 247, 210,
    255, 255, 128, 1, 200, 246, 255, 234, 255, 128, 128, 128, 128, 128, 109, 178, 241, 255, 231, 245, 255, 255, 128, 128,
    128, 44, 130, 201, 253, 205, 192, 255, 255, 128, 128, 128, 1, 132, 239, 251, 219, 209, 255, 165, 128, 128, 128, 94,
    136, 225, 251, 218, 190, 255, 255, 128, 128, 128, 22, 100, 174, 245, 186, 161, 255, 199, 128, 128, 128, 1, 182, 249,
    255, 232, 235, 128, 128, 128, 128, 128, 124, 143, 241, 255, 227, 234, 128, 128, 128, 128, 128, 35, 77, 181, 251, 193,
    211, 255, 205, 128, 128, 128, 1, 157, 247, 255, 236, 231, 255, 255, 128, 128, 128, 121, 141, 235, 255, 225, 227, 255,
    255, 128, 128, 128, 45, 99, 188, 251, 195, 217, 255, 224, 128, 128, 128, 1, 1, 251, 255, 213, 255, 128, 128, 128,
    128, 128, 203, 1, 248, 255, 255, 128, 128, 128, 128, 128, 128, 137, 1, 177, 255, 224, 255, 128, 128, 128, 128, 128,
    253, 9, 248, 251, 207, 208, 255, 192, 128, 128, 128, 175, 13, 224, 243, 193, 185, 249, 198, 255, 255, 128, 73, 17,
    171, 221, 161, 179, 236, 167, 255, 234, 128, 1, 95, 247, 253, 212, 183, 255, 255, 128, 128, 128, 239, 90, 244, 250,
    211, 209, 255, 255, 128, 128, 128, 155, 77, 195, 248, 188, 195, 255, 255, 128, 128, 128, 1, 24, 239, 251, 218, 219,
    255, 205, 128, 128, 128, 201, 51, 219, 255, 196, 186, 128, 128, 128, 128, 128, 69, 46, 190, 239, 201, 218, 255, 228,
    128, 128, 128, 1, 191, 251, 255, 255, 128, 128, 128, 128, 128, 128, 223, 165, 249, 255, 213, 255, 128, 128, 128, 128,
    128, 141, 124, 248, 255, 255, 128, 128, 128, 128, 128, 128, 1, 16, 248, 255, 255, 128, 128, 128, 128, 128, 128, 190,
    36, 230, 255, 236, 255, 128, 128, 128, 128, 128, 149, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128, 1, 226, 255,
    128, 128, 128, 128, 128, 128, 128, 128, 247, 192, 255, 128, 128, 128, 128, 128, 128, 128, 128, 240, 128, 255, 128, 128,
    128, 128, 128, 128, 128, 128, 1, 134, 252, 255, 255, 128, 128, 128, 128, 128, 128, 213, 62, 250, 255, 255, 128, 128,
    128, 128, 128, 128, 55, 93, 255, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
    128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128,
    202, 24, 213, 235, 186, 191, 220, 160, 240, 175, 255, 126, 38, 182, 232, 169, 184, 228, 174, 255, 187, 128, 61, 46,
    138, 219, 151, 178, 240, 170, 255, 216, 128, 1, 112, 230, 250, 199, 191, 247, 159, 255, 255, 128, 166, 109, 228, 252,
    211, 215, 255, 174, 128, 128, 128, 39, 77, 162, 232, 172, 180, 245, 178, 255, 255, 128, 1, 52, 220, 246, 198, 199,
    249, 220, 255, 255, 128, 124, 74, 191, 243, 183, 193, 250, 221, 255, 255, 128, 24, 71, 130, 219, 154, 170, 243, 182,
    255, 255, 128, 1, 182, 225, 249, 219, 240, 255, 224, 128, 128, 128, 149, 150, 226, 252, 216, 205, 255, 171, 128, 128,
    128, 28, 108, 170, 242, 183, 194, 254, 223, 255, 255, 128, 1, 81, 230, 252, 204, 203, 255, 192, 128, 128, 128, 123,
    102, 209, 247, 188, 196, 255, 233, 128, 128, 128, 20, 95, 153, 243, 164, 173, 255, 203, 128, 128, 128, 1, 222, 248,
    255, 216, 213, 128, 128, 128, 128, 128, 168, 175, 246, 252, 235, 205, 255, 255, 128, 128, 128, 47, 116, 215, 255, 211,
    212, 255, 255, 128, 128, 128, 1, 121, 236, 253, 212, 214, 255, 255, 128, 128, 128, 141, 84, 213, 252, 201, 202, 255,
    219, 128, 128, 128, 42, 80, 160, 240, 162, 185, 255, 205, 128, 128, 128, 1, 1, 255, 128, 128, 128, 128, 128, 128,
    128, 128, 244, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128, 238, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128,
  ];
}

// The probabilities of the tree of a 4x4 mode, by the mode above and the mode on the left (RFC 6386, section 11.5).
function submode_probs(): i32[] {
  return [
    231, 120, 48, 89, 115, 113, 120, 152, 112, 152, 179, 64, 126, 170, 118, 46, 70, 95, 175, 69, 143, 80, 85, 82,
    72, 155, 103, 56, 58, 10, 171, 218, 189, 17, 13, 152, 114, 26, 17, 163, 44, 195, 21, 10, 173, 121, 24, 80,
    195, 26, 62, 44, 64, 85, 144, 71, 10, 38, 171, 213, 144, 34, 26, 170, 46, 55, 19, 136, 160, 33, 206, 71,
    63, 20, 8, 114, 114, 208, 12, 9, 226, 81, 40, 11, 96, 182, 84, 29, 16, 36, 134, 183, 89, 137, 98, 101,
    106, 165, 148, 72, 187, 100, 130, 157, 111, 32, 75, 80, 66, 102, 167, 99, 74, 62, 40, 234, 128, 41, 53, 9,
    178, 241, 141, 26, 8, 107, 74, 43, 26, 146, 73, 166, 49, 23, 157, 65, 38, 105, 160, 51, 52, 31, 115, 128,
    104, 79, 12, 27, 217, 255, 87, 17, 7, 87, 68, 71, 44, 114, 51, 15, 186, 23, 47, 41, 14, 110, 182, 183,
    21, 17, 194, 66, 45, 25, 102, 197, 189, 23, 18, 22, 88, 88, 147, 150, 42, 46, 45, 196, 205, 43, 97, 183,
    117, 85, 38, 35, 179, 61, 39, 53, 200, 87, 26, 21, 43, 232, 171, 56, 34, 51, 104, 114, 102, 29, 93, 77,
    39, 28, 85, 171, 58, 165, 90, 98, 64, 34, 22, 116, 206, 23, 34, 43, 166, 73, 107, 54, 32, 26, 51, 1,
    81, 43, 31, 68, 25, 106, 22, 64, 171, 36, 225, 114, 34, 19, 21, 102, 132, 188, 16, 76, 124, 62, 18, 78,
    95, 85, 57, 50, 48, 51, 193, 101, 35, 159, 215, 111, 89, 46, 111, 60, 148, 31, 172, 219, 228, 21, 18, 111,
    112, 113, 77, 85, 179, 255, 38, 120, 114, 40, 42, 1, 196, 245, 209, 10, 25, 109, 88, 43, 29, 140, 166, 213,
    37, 43, 154, 61, 63, 30, 155, 67, 45, 68, 1, 209, 100, 80, 8, 43, 154, 1, 51, 26, 71, 142, 78, 78,
    16, 255, 128, 34, 197, 171, 41, 40, 5, 102, 211, 183, 4, 1, 221, 51, 50, 17, 168, 209, 192, 23, 25, 82,
    138, 31, 36, 171, 27, 166, 38, 44, 229, 67, 87, 58, 169, 82, 115, 26, 59, 179, 63, 59, 90, 180, 59, 166,
    93, 73, 154, 40, 40, 21, 116, 143, 209, 34, 39, 175, 47, 15, 16, 183, 34, 223, 49, 45, 183, 46, 17, 33,
    183, 6, 98, 15, 32, 183, 57, 46, 22, 24, 128, 1, 54, 17, 37, 65, 32, 73, 115, 28, 128, 23, 128, 205,
    40, 3, 9, 115, 51, 192, 18, 6, 223, 87, 37, 9, 115, 59, 77, 64, 21, 47, 104, 55, 44, 218, 9, 54,
    53, 130, 226, 64, 90, 70, 205, 40, 41, 23, 26, 57, 54, 57, 112, 184, 5, 41, 38, 166, 213, 30, 34, 26,
    133, 152, 116, 10, 32, 134, 39, 19, 53, 221, 26, 114, 32, 73, 255, 31, 9, 65, 234, 2, 15, 1, 118, 73,
    75, 32, 12, 51, 192, 255, 160, 43, 51, 88, 31, 35, 67, 102, 85, 55, 186, 85, 56, 21, 23, 111, 59, 205,
    45, 37, 192, 55, 38, 70, 124, 73, 102, 1, 34, 98, 125, 98, 42, 88, 104, 85, 117, 175, 82, 95, 84, 53,
    89, 128, 100, 113, 101, 45, 75, 79, 123, 47, 51, 128, 81, 171, 1, 57, 17, 5, 71, 102, 57, 53, 41, 49,
    38, 33, 13, 121, 57, 73, 26, 1, 85, 41, 10, 67, 138, 77, 110, 90, 47, 114, 115, 21, 2, 10, 102, 255,
    166, 23, 6, 101, 29, 16, 10, 85, 128, 101, 196, 26, 57, 18, 10, 102, 102, 213, 34, 20, 43, 117, 20, 15,
    36, 163, 128, 68, 1, 26, 102, 61, 71, 37, 34, 53, 31, 243, 192, 69, 60, 71, 38, 73, 119, 28, 222, 37,
    68, 45, 128, 34, 1, 47, 11, 245, 171, 62, 17, 19, 70, 146, 85, 55, 62, 70, 37, 43, 37, 154, 100, 163,
    85, 160, 1, 63, 9, 92, 136, 28, 64, 32, 201, 85, 75, 15, 9, 9, 64, 255, 184, 119, 16, 86, 6, 28,
    5, 64, 255, 25, 248, 1, 56, 8, 17, 132, 137, 255, 55, 116, 128, 58, 15, 20, 82, 135, 57, 26, 121, 40,
    164, 50, 31, 137, 154, 133, 25, 35, 218, 51, 103, 44, 131, 131, 123, 31, 6, 158, 86, 40, 64, 135, 148, 224,
    45, 183, 128, 22, 26, 17, 131, 240, 154, 14, 1, 209, 45, 16, 21, 91, 64, 222, 7, 1, 197, 56, 21, 39,
    155, 60, 138, 23, 102, 213, 83, 12, 13, 54, 192, 255, 68, 47, 28, 85, 26, 85, 85, 128, 128, 32, 146, 171,
    18, 11, 7, 63, 144, 171, 4, 4, 246, 35, 27, 10, 146, 174, 171, 12, 26, 128, 190, 80, 35, 99, 180, 80,
    126, 54, 45, 85, 126, 47, 87, 176, 51, 41, 20, 32, 101, 75, 128, 139, 118, 146, 116, 128, 85, 56, 41, 15,
    176, 236, 85, 37, 9, 62, 71, 30, 17, 119, 118, 255, 17, 18, 138, 101, 38, 60, 138, 55, 70, 43, 26, 142,
    146, 36, 19, 30, 171, 255, 97, 27, 20, 138, 45, 61, 62, 219, 1, 81, 188, 64, 32, 41, 20, 117, 151, 142,
    20, 21, 163, 112, 19, 12, 61, 195, 128, 48, 4, 24,
  ];
}

// The DC quantizer step for each index (RFC 6386, section 14.1).
function dc_steps(): i32[] {
  return [
    4, 5, 6, 7, 8, 9, 10, 10, 11, 12, 13, 14, 15, 16, 17, 17, 18, 19, 20, 20, 21, 21, 22, 22,
    23, 23, 24, 25, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 37, 38, 39, 40, 41, 42, 43,
    44, 45, 46, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66,
    67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89,
    91, 93, 95, 96, 98, 100, 101, 102, 104, 106, 108, 110, 112, 114, 116, 118, 122, 124, 126, 128, 130, 132, 134, 136,
    138, 140, 143, 145, 148, 151, 154, 157,
  ];
}

// The AC quantizer step for each index (RFC 6386, section 14.1).
function ac_steps(): i32[] {
  return [
    4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27,
    28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51,
    52, 53, 54, 55, 56, 57, 58, 60, 62, 64, 66, 68, 70, 72, 74, 76, 78, 80, 82, 84, 86, 88, 90, 92,
    94, 96, 98, 100, 102, 104, 106, 108, 110, 112, 114, 116, 119, 122, 125, 128, 131, 134, 137, 140, 143, 146, 149, 152,
    155, 158, 161, 164, 167, 170, 173, 177, 181, 185, 189, 193, 197, 201, 205, 209, 213, 217, 221, 225, 229, 234, 239, 245,
    249, 254, 259, 264, 269, 274, 279, 284,
  ];
}

// The tables that the decode of each macroblock reads.
struct Tables { submodes: i32[], zigzag: i32[], bands: i32[] }

function lossy_tables(): Tables {
  return Tables { submodes: submode_probs(), zigzag: [0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15], bands: [0, 1, 2, 3, 6, 4, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7, 0] };
}
```

### `lossy header`

A `VP8 ` chunk starts with a frame tag of three bytes, the start code `9D 01 2A`, and the width and the height in 14 bits each. The frame must be a key frame that is shown. The first partition follows. It holds the header of the frame and the modes of the macroblocks. The token partitions follow it. Their sizes come before them, three bytes for each partition but the last.

The header gives the segments, the loop filter, the quantizers and the token probabilities. Each of the four segments has its own quantizers and loop filter level. As in libwebp, the loop filter is off when the level of the frame is 0, also when a segment gives a level.

```fern
<<lossy header>>=
// The header of a frame. `quant` holds six steps for each segment: Y DC, Y
// AC, Y2 DC, Y2 AC, chroma DC and chroma AC. `filters` holds the limit, the
// interior limit and the threshold of high edge variance for each segment,
// for a 16x16 macroblock and then for a 4x4 one. `filter_type` is 0 for no
// filter, 1 for the simple filter and 2 for the normal filter. `skip_prob`
// is -1 when the macroblocks have no skip flag.
struct Lossy { width: i32, height: i32, mb_w: i32, mb_h: i32, update_map: boolean, segment_probs: i32[], quant: i32[], filter_type: i32, filters: i32[], probs: i32[], skip_prob: i32 }

struct Segments { enabled: boolean, update_map: boolean, absolute: boolean, quant: i32[], strength: i32[], probs: i32[] }

function segments_of(own r: Bool, data: [u8]): (Segments, Bool) {
  let s = Segments { enabled: false, update_map: false, absolute: true, quant: [0, 0, 0, 0], strength: [0, 0, 0, 0], probs: [255, 255, 255] };
  let (enabled, r1) = bit(r, data, 128);
  if (enabled == 0) {
    return (s, r1);
  }
  let (update_map, r2) = bit(r1, data, 128);
  let (update_data, r3) = bit(r2, data, 128);
  let cur = r3;
  let absolute = true;
  let quant = s.quant;
  let strength = s.strength;
  if (update_data == 1) {
    let (a, r4) = bit(cur, data, 128);
    absolute = a == 1;
    cur = r4;
    for i in 0..4 {
      let (q, next) = bool_optional(cur, data, 7);
      quant = quant.with(i, q);
      cur = next;
    }
    for i in 0..4 {
      let (f, next) = bool_optional(cur, data, 6);
      strength = strength.with(i, f);
      cur = next;
    }
  }
  let probs = s.probs;
  if (update_map == 1) {
    for i in 0..3 {
      let (present, r5) = bit(cur, data, 128);
      let (p, r6) = if (present == 1) { bool_value(r5, data, 8) } else { (255, r5) };
      probs = probs.with(i, p);
      cur = r6;
    }
  }
  return (Segments { enabled: true, update_map: update_map == 1, absolute: absolute, quant: quant, strength: strength, probs: probs }, cur);
}

// The level of the frame, the sharpness, whether the filter is simple, and
// the deltas of the first reference frame and of the 4x4 mode. Only these
// deltas apply to a key frame.
struct Filtering { simple: boolean, level: i32, sharpness: i32, ref_delta: i32, mode_delta: i32 }

function filtering_of(own r: Bool, data: [u8]): (Filtering, Bool) {
  let (simple, r1) = bit(r, data, 128);
  let (level, r2) = bool_value(r1, data, 6);
  let (sharpness, r3) = bool_value(r2, data, 3);
  let (deltas, r4) = bit(r3, data, 128);
  let cur = r4;
  let ref_delta = 0;
  let mode_delta = 0;
  if (deltas == 1) {
    let (update, r5) = bit(cur, data, 128);
    cur = r5;
    if (update == 1) {
      for i in 0..8 {
        let (d, next) = bool_optional(cur, data, 6);
        cur = next;
        if (i == 0) {
          ref_delta = d;
        } else if (i == 4) {
          mode_delta = d;
        }
      }
    }
  }
  return (Filtering { simple: simple == 1, level: level, sharpness: sharpness, ref_delta: ref_delta, mode_delta: mode_delta }, cur);
}

// The limit, the interior limit and the threshold of high edge variance of
// each segment, for a 16x16 and a 4x4 macroblock, from section 15 of RFC 6386
// and `PrecomputeFilterStrengths` of libwebp. A limit of 0 turns the filter
// off.
function filter_levels(s: Segments, f: Filtering): i32[] {
  let out: i32[] = [];
  for seg in 0..4 {
    let base = if (!s.enabled) { f.level } else if (s.absolute) { s.strength[seg] } else { s.strength[seg] + f.level };
    out = out.concat(level_params((base + f.ref_delta).clamp(0, 63), f.sharpness));
    out = out.concat(level_params((base + f.ref_delta + f.mode_delta).clamp(0, 63), f.sharpness));
  }
  return out;
}

// The limit, the interior limit and the threshold of high edge variance of the
// loop filter level `level`.
function level_params(level: i32, sharpness: i32): i32[] {
  if (level == 0) {
    return [0, 0, 0];
  }
  let interior = if (sharpness > 4) { level >> 2 } else if (sharpness > 0) { level >> 1 } else { level };
  if (sharpness > 0) {
    interior = interior.min(9 - sharpness);
  }
  interior = interior.max(1);
  let hev = if (level >= 40) { 2 } else if (level >= 15) { 1 } else { 0 };
  return [2 * level + interior, interior, hev];
}

// The six quantizer steps of each segment.
function quant_of(own r: Bool, data: [u8], s: Segments): (i32[], Bool) {
  let (base, r1) = bool_value(r, data, 7);
  let (y_dc, r2) = bool_optional(r1, data, 4);
  let (y2_dc, r3) = bool_optional(r2, data, 4);
  let (y2_ac, r4) = bool_optional(r3, data, 4);
  let (uv_dc, r5) = bool_optional(r4, data, 4);
  let (uv_ac, r6) = bool_optional(r5, data, 4);
  let dc = dc_steps();
  let ac = ac_steps();
  let out: i32[] = [];
  for seg in 0..4 {
    let q = if (!s.enabled) { base } else if (s.absolute) { s.quant[seg] } else { s.quant[seg] + base };
    let y2 = (ac[(q + y2_ac).clamp(0, 127)] * 101581 >> 16).max(8);
    out = out.append(dc[(q + y_dc).clamp(0, 127)]).append(ac[q.clamp(0, 127)]).append(dc[(q + y2_dc).clamp(0, 127)] * 2).append(y2).append(dc[(q + uv_dc).clamp(0, 117)]).append(ac[(q + uv_ac).clamp(0, 127)]);
  }
  return (out, r6);
}

// The token probabilities, each the default or a new value of 8 bits.
function probs_of(own r: Bool, data: [u8]): (i32[], Bool) {
  let update = coeff_update_probs();
  let probs = coeff_default_probs();
  let cur = r;
  for i in 0..probs.len() {
    let (changed, r1) = bit(cur, data, update[i]);
    cur = r1;
    if (changed == 1) {
      let (p, r2) = bool_value(cur, data, 8);
      probs = probs.with(i, p);
      cur = r2;
    }
  }
  return (probs, cur);
}

// The token partitions in the `size` bytes from `at`. A partition that the
// sizes put past the end ends at the end. As in libwebp, the last partition
// must start before the end.
function partitions(own r: Bool, data: [u8], at: i32, size: i32): Result[(Bool[], Bool), DecodeError] {
  let (log, r1) = bool_value(r, data, 2);
  let last = (1 << log) - 1;
  if (size < 3 * last) {
    return Err(Truncated);
  }
  let start = at + 3 * last;
  let end = at + size;
  let parts: Bool[] = [];
  for p in 0..last {
    let psize = le24_at(data, at + 3 * p).min(end - start);
    parts = parts.append(bool_reader(data, start, start + psize));
    start += psize;
  }
  if (start >= end) {
    return Err(Truncated);
  }
  return Ok((parts.append(bool_reader(data, start, end)), r1));
}

// The width, the height and the size of the first partition of the `VP8 `
// chunk with `chunk` bytes of data at `start`, which can read to `end`. The
// frame must be a key frame that is shown.
function frame_size(data: [u8], start: i32, chunk: i32, end: i32): Result[(i32, i32, i32), DecodeError] {
  if (chunk < 10 || end - start < 10) {
    return Err(BadHeader);
  }
  let tag = le24_at(data, start);
  let part0 = tag >> 5;
  let width = (data[start + 6] as i32 | data[start + 7] as i32 << 8) & 16383;
  let height = (data[start + 8] as i32 | data[start + 9] as i32 << 8) & 16383;
  if (!shown_key_frame(tag) || le24_at(data, start + 3) != 2752925 || part0 >= chunk || width * height == 0) {
    return Err(BadHeader);
  }
  if (part0 > end - start - 10) {
    return Err(Truncated);
  }
  return Ok((width, height, part0));
}

// Whether the frame tag `tag` is of a key frame that is shown, with a version
// from 0 to 3.
function shown_key_frame(tag: i32): boolean {
  return (tag & 1) == 0 && (tag >> 1 & 7) <= 3 && (tag >> 4 & 1) == 1;
}

// The header of the `VP8 ` chunk with `chunk` bytes of data at `start`, and
// the readers of the first partition and of the token partitions. The
// partitions can read to `end`, past the end of the chunk, as in libwebp.
function frame_header(data: [u8], start: i32, chunk: i32, end: i32): Result[(Lossy, Bool, Bool[]), DecodeError] {
  let (width, height, part0) = frame_size(data, start, chunk, end)?;
  let r = bool_reader(data, start + 10, start + 10 + part0);
  let (_, r1) = bool_value(r, data, 2);
  let (segments, r2) = segments_of(r1, data);
  let (filtering, r3) = filtering_of(r2, data);
  let (parts, r4) = partitions(r3, data, start + 10 + part0, end - start - 10 - part0)?;
  let (quant, r5) = quant_of(r4, data, segments);
  let (_, r6) = bit(r5, data, 128);
  let (probs, r7) = probs_of(r6, data);
  let (skips, r8) = bit(r7, data, 128);
  let (skip_prob, r9) = if (skips == 1) { bool_value(r8, data, 8) } else { (-1, r8) };
  let filter_type = if (filtering.level == 0) { 0 } else if (filtering.simple) { 1 } else { 2 };
  let header = Lossy { width: width, height: height, mb_w: width + 15 >> 4, mb_h: height + 15 >> 4, update_map: segments.update_map, segment_probs: segments.probs, quant: quant, filter_type: filter_type, filters: filter_levels(segments, filtering), probs: probs, skip_prob: skip_prob };
  return Ok((header, r9, parts));
}
```

### `lossy modes`

The first partition gives the modes of each macroblock after the header: its segment, its skip flag, the mode of its luma and the mode of its chroma. A 16x16 luma mode is DC, TM, VE or HE. A macroblock of 4x4 blocks gives one of ten modes for each block. The probabilities depend on the modes of the blocks above and on the left. Outside the frame, and in a 16x16 macroblock, the mode of a block is the 16x16 mode (section 11.3).

```fern
<<lossy modes>>=
const DC_PRED: i32 = 0;

const TM_PRED: i32 = 1;

const VE_PRED: i32 = 2;

const HE_PRED: i32 = 3;

// The modes of one macroblock. `y` holds the mode of each 4x4 block, or
// the 16x16 mode 16 times.
struct Modes { segment: i32, skip: boolean, i4: boolean, y: i32[], uv: i32 }

// The mode of a 4x4 block, by the tree of section 11.2.
function submode(own r: Bool, data: [u8], probs: [i32], at: i32): (i32, Bool) {
  let (b0, r0) = bit(r, data, probs[at]);
  if (b0 == 0) {
    return (DC_PRED, r0);
  }
  let (b1, r1) = bit(r0, data, probs[at + 1]);
  if (b1 == 0) {
    return (TM_PRED, r1);
  }
  let (b2, r2) = bit(r1, data, probs[at + 2]);
  if (b2 == 0) {
    return (VE_PRED, r2);
  }
  let (b3, r3) = bit(r2, data, probs[at + 3]);
  if (b3 == 0) {
    let (b4, r4) = bit(r3, data, probs[at + 4]);
    if (b4 == 0) {
      return (HE_PRED, r4);
    }
    let (b5, r5) = bit(r4, data, probs[at + 5]);
    return (if (b5 == 0) { 4 } else { 5 }, r5);
  }
  let (b6, r6) = bit(r3, data, probs[at + 6]);
  if (b6 == 0) {
    return (6, r6);
  }
  let (b7, r7) = bit(r6, data, probs[at + 7]);
  if (b7 == 0) {
    return (7, r7);
  }
  let (b8, r8) = bit(r7, data, probs[at + 8]);
  return (if (b8 == 0) { 8 } else { 9 }, r8);
}

// The modes of the macroblock in column `mb_x`, with the modes of the blocks
// above in `above` and on the left in `left`, and those contexts after it.
function modes_of(own r: Bool, data: [u8], h: Lossy, tab: Tables, own above: i32[], own left: i32[], mb_x: i32): (Modes, Bool, i32[], i32[]) {
  let (segment, skip, r1) = segment_and_skip(r, data, h);
  let (whole, r2) = bit(r1, data, 145);
  let cur = r2;
  let y = __alloc_i32(16);
  if (whole == 1) {
    let (mode, r3) = luma_mode(cur, data);
    cur = r3;
    for k in 0..16 {
      y = y.with(k, mode);
    }
    for k in 0..4 {
      above = above.with(mb_x * 4 + k, mode);
      left = left.with(k, mode);
    }
  } else {
    for row in 0..4 {
      let l = left[row];
      for col in 0..4 {
        let (mode, next) = submode(cur, data, tab.submodes, (above[mb_x * 4 + col] * 10 + l) * 9);
        cur = next;
        y = y.with(row * 4 + col, mode);
        above = above.with(mb_x * 4 + col, mode);
        l = mode;
      }
      left = left.with(row, l);
    }
  }
  let (uv, r4) = chroma_mode(cur, data);
  return (Modes { segment: segment, skip: skip, i4: whole == 0, y: y, uv: uv }, r4, above, left);
}

// The segment and the skip flag of a macroblock.
function segment_and_skip(own r: Bool, data: [u8], h: Lossy): (i32, boolean, Bool) {
  let cur = r;
  let segment = 0;
  if (h.update_map) {
    let (high, r1) = bit(cur, data, h.segment_probs[0]);
    let (low, r2) = bit(r1, data, h.segment_probs[1 + high]);
    segment = 2 * high + low;
    cur = r2;
  }
  if (h.skip_prob < 0) {
    return (segment, false, cur);
  }
  let (skip, r3) = bit(cur, data, h.skip_prob);
  return (segment, skip == 1, r3);
}

// A 16x16 luma mode (section 11.2).
function luma_mode(own r: Bool, data: [u8]): (i32, Bool) {
  let (b0, r1) = bit(r, data, 156);
  if (b0 == 0) {
    let (b1, r2) = bit(r1, data, 163);
    return (if (b1 == 0) { DC_PRED } else { VE_PRED }, r2);
  }
  let (b1, r2) = bit(r1, data, 128);
  return (if (b1 == 0) { HE_PRED } else { TM_PRED }, r2);
}

// A chroma mode (section 11.2).
function chroma_mode(own r: Bool, data: [u8]): (i32, Bool) {
  let (c0, r1) = bit(r, data, 142);
  if (c0 == 0) {
    return (DC_PRED, r1);
  }
  let (c1, r2) = bit(r1, data, 114);
  if (c1 == 0) {
    return (VE_PRED, r2);
  }
  let (c2, r3) = bit(r2, data, 183);
  return (if (c2 == 1) { TM_PRED } else { HE_PRED }, r3);
}
```

### `lossy tokens`

The token partitions hold the coefficients of each macroblock: the 16 luma blocks, then the four blocks of U and the four blocks of V. A 16x16 macroblock first gives the block Y2, which holds the DC coefficients of its luma blocks. The luma blocks then start at their second coefficient. The context of a block is the count of the blocks above and on the left that have a coefficient that is not 0 (section 13.3).

A coefficient is stored as a 16-bit integer after the multiply by its step, as in libwebp. The Walsh-Hadamard transform of Y2 gives the DC coefficients of the luma blocks.

```fern
<<lossy tokens>>=
// The value of a token larger than 1, from its fourth probability on.
function large(own r: Bool, data: [u8], probs: [i32], p: i32): (i32, Bool) {
  let (b3, r1) = bit(r, data, probs[p + 3]);
  if (b3 == 0) {
    let (b4, r2) = bit(r1, data, probs[p + 4]);
    if (b4 == 0) {
      return (2, r2);
    }
    let (b5, r3) = bit(r2, data, probs[p + 5]);
    return (3 + b5, r3);
  }
  let (b6, r2) = bit(r1, data, probs[p + 6]);
  if (b6 == 0) {
    let (b7, r3) = bit(r2, data, probs[p + 7]);
    if (b7 == 0) {
      let (e, r4) = bit(r3, data, 159);
      return (5 + e, r4);
    }
    let (e1, r4) = bit(r3, data, 165);
    let (e0, r5) = bit(r4, data, 145);
    return (7 + 2 * e1 + e0, r5);
  }
  let (high, r3) = bit(r2, data, probs[p + 8]);
  let (low, r4) = bit(r3, data, probs[p + 9 + high]);
  let cat = 2 * high + low;
  let extra = match (cat) {
    0 => [173, 148, 140],
    1 => [176, 155, 140, 135],
    2 => [180, 157, 141, 134, 130],
    _ => [254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129],
  };
  let v = 0;
  let cur = r4;
  for prob in extra {
    let (e, next) = bit(cur, data, prob);
    v = v + v + e;
    cur = next;
  }
  return (v + 3 + (8 << cat), cur);
}

function wrap16(v: i32): i32 {
  return (v + 32768 & 65535) - 32768;
}

// The index of the 11 probabilities of plane type `t`, band `band` and
// context `ctx`.
function prob_at(t: i32, band: i32, ctx: i32): i32 {
  return ((t * 8 + band) * 3 + ctx) * 11;
}

// The coefficients of one block, from index `first`, into `out` at `base`.
// `dc` and `ac` are the steps. The count is the index after the last token
// that is not the end of the block.
function coeffs(own r: Bool, data: [u8], tab: Tables, probs: [i32], t: i32, ctx: i32, dc: i32, ac: i32, first: i32, own out: i32[], base: i32): (i32, i32[], Bool) {
  let cur = r;
  let n = first;
  let p = prob_at(t, tab.bands[n], ctx);
  while (n < 16) {
    let (more, r1) = bit(cur, data, probs[p]);
    cur = r1;
    if (more == 0) {
      return (n, out, cur);
    }
    loop {
      let (nonzero, r2) = bit(cur, data, probs[p + 1]);
      cur = r2;
      if (nonzero == 1) {
        break;
      }
      n += 1;
      if (n == 16) {
        return (16, out, cur);
      }
      p = prob_at(t, tab.bands[n], 0);
    }
    let band = tab.bands[n + 1];
    let (big, r3) = bit(cur, data, probs[p + 2]);
    cur = r3;
    let v = 1;
    if (big == 0) {
      p = prob_at(t, band, 1);
    } else {
      let (lv, r4) = large(cur, data, probs, p);
      v = lv;
      cur = r4;
      p = prob_at(t, band, 2);
    }
    let (negative, r5) = bit(cur, data, 128);
    cur = r5;
    let step = if (n > 0) { ac } else { dc };
    let level = if (negative == 1) { -v } else { v };
    out = out.with(base + tab.zigzag[n], wrap16(level * step));
    n += 1;
  }
  return (16, out, cur);
}

// The DC coefficients of the 16 luma blocks from the coefficients of Y2.
function wht(y2: [i32], own coef: i32[]): i32[] {
  let tmp = __alloc_i32(16);
  for i in 0..4 {
    let a0 = y2[i] + y2[12 + i];
    let a1 = y2[4 + i] + y2[8 + i];
    let a2 = y2[4 + i] - y2[8 + i];
    let a3 = y2[i] - y2[12 + i];
    tmp = tmp.with(i, a0 + a1).with(8 + i, a0 - a1).with(4 + i, a3 + a2).with(12 + i, a3 - a2);
  }
  for i in 0..4 {
    let dc = tmp[i * 4] + 3;
    let a0 = dc + tmp[i * 4 + 3];
    let a1 = tmp[i * 4 + 1] + tmp[i * 4 + 2];
    let a2 = tmp[i * 4 + 1] - tmp[i * 4 + 2];
    let a3 = dc - tmp[i * 4 + 3];
    let o = i * 64;
    coef = coef.with(o, wrap16(a0 + a1 >> 3)).with(o + 16, wrap16(a3 + a2 >> 3)).with(o + 32, wrap16(a0 - a1 >> 3)).with(o + 48, wrap16(a3 - a2 >> 3));
  }
  return coef;
}

// The contexts of the blocks: for each column of macroblocks, 4 luma bits, 2
// U bits and 2 V bits of the bottom blocks, and the bit of Y2. `left` and
// `left_dc` are the same bits for the macroblock on the left.
struct Nz { top: i32[], top_dc: i32[], left: i32, left_dc: i32 }

// The coefficients of a macroblock, and whether the macroblock has no block
// with a coefficient. `q` holds the steps of its segment at `qs`.
function residuals(own r: Bool, data: [u8], tab: Tables, probs: [i32], q: [i32], qs: i32, i4: boolean, own nz: Nz, mb_x: i32): (i32[], boolean, Nz, Bool) {
  let top = nz.top[mb_x];
  let left = nz.left;
  let (coef, dc, cur) = if (i4) { (__alloc_i32(384), -1, r) } else { y2_coeffs(r, data, tab, probs, q, qs, nz.top_dc[mb_x] + nz.left_dc) };
  let first = if (i4) { 0 } else { 1 };
  let luma_type = if (i4) { 3 } else { 0 };
  let (y, r1) = luma_coeffs(cur, data, tab, probs, q, qs, first, luma_type, Blocks { coef: coef, any: false, top: top & 15, left: left & 15 });
  let (u, r2) = chroma_coeffs(r1, data, tab, probs, q, qs, 16, Blocks { coef: y.coef, any: y.any, top: top >> 4 & 3, left: left >> 4 & 3 });
  let (v, r3) = chroma_coeffs(r2, data, tab, probs, q, qs, 20, Blocks { coef: u.coef, any: u.any, top: top >> 6 & 3, left: left >> 6 & 3 });
  let top_dc = if (dc < 0) { nz.top_dc } else { nz.top_dc.with(mb_x, dc) };
  let left_dc = if (dc < 0) { nz.left_dc } else { dc };
  let next = Nz { top: nz.top.with(mb_x, y.top | u.top << 4 | v.top << 6), top_dc: top_dc, left: y.left | u.left << 4 | v.left << 6, left_dc: left_dc };
  return (v.coef, !v.any, next, r3);
}

// The coefficients of a 16x16 macroblock with the DC coefficients from its
// block Y2, with the context `ctx`, and the context bit of Y2.
function y2_coeffs(own r: Bool, data: [u8], tab: Tables, probs: [i32], q: [i32], qs: i32, ctx: i32): (i32[], i32, Bool) {
  let (n, y2, r1) = coeffs(r, data, tab, probs, 1, ctx, q[qs + 2], q[qs + 3], 0, __alloc_i32(16), 0);
  let coef = __alloc_i32(384);
  if (n > 1) {
    coef = wht(y2, coef);
  } else {
    let dc0 = y2[0] + 3 >> 3;
    for i in 0..16 {
      coef = coef.with(i * 16, dc0);
    }
  }
  return (coef, if (n > 0) { 1 } else { 0 }, r1);
}

// The coefficients so far, whether a block has a coefficient that is not 0,
// and a context bit for each column (`top`) and each row (`left`) of blocks.
struct Blocks { coef: i32[], any: boolean, top: i32, left: i32 }

// The 16 luma blocks of plane type `t`, from the coefficient `first`.
function luma_coeffs(own r: Bool, data: [u8], tab: Tables, probs: [i32], q: [i32], qs: i32, first: i32, t: i32, own b: Blocks): (Blocks, Bool) {
  let Blocks { coef, any, top, left } = b;
  let cur = r;
  for y in 0..4 {
    let l = left >> y & 1;
    for x in 0..4 {
      let base = (y * 4 + x) * 16;
      let (n, c1, r1) = coeffs(cur, data, tab, probs, t, l + (top >> x & 1), q[qs], q[qs + 1], first, coef, base);
      coef = c1;
      cur = r1;
      l = if (n > first) { 1 } else { 0 };
      top = top & ~(1 << x) | l << x;
      any = any || n > 1 || coef[base] != 0;
    }
    left = left & ~(1 << y) | l << y;
  }
  return (Blocks { coef: coef, any: any, top: top, left: left }, cur);
}

// The four blocks of a chroma plane, from the block `block`: 16 for U and 20
// for V.
function chroma_coeffs(own r: Bool, data: [u8], tab: Tables, probs: [i32], q: [i32], qs: i32, block: i32, own b: Blocks): (Blocks, Bool) {
  let Blocks { coef, any, top, left } = b;
  let cur = r;
  for y in 0..2 {
    let l = left >> y & 1;
    for x in 0..2 {
      let base = (block + y * 2 + x) * 16;
      let (n, c1, r1) = coeffs(cur, data, tab, probs, 2, l + (top >> x & 1), q[qs + 4], q[qs + 5], 0, coef, base);
      coef = c1;
      cur = r1;
      l = if (n > 0) { 1 } else { 0 };
      top = top & ~(1 << x) | l << x;
      any = any || n > 1 || coef[base] != 0;
    }
    left = left & ~(1 << y) | l << y;
  }
  return (Blocks { coef: coef, any: any, top: top, left: left }, cur);
}
```

### `lossy prediction`

Each macroblock is predicted from the pixels above it and on the left, and the inverse DCT of each block adds to the prediction. The work buffer holds the macroblock with one row above it and four columns on the left, as in libwebp, 32 bytes for each row. Above the frame each pixel is 127. On the left of the frame each pixel is 129, also the pixel above on the left, but not in the first row. The prediction reads pixels before the loop filter.

A 4x4 block in the right column of its macroblock needs four pixels above on the right. They come from the macroblock above on the right, also for the lower rows. In the last column of macroblocks, they are copies of the last pixel above.

```fern
<<lossy prediction>>=
const BPS: i32 = 32;

const Y_AT: i32 = 40;

const U_AT: i32 = 584;

const V_AT: i32 = 600;

function clip255(v: i32): i32 {
  return v.clamp(0, 255);
}

function avg3(a: i32, b: i32, c: i32): i32 {
  return a + 2 * b + c + 2 >> 2;
}

function avg2(a: i32, b: i32): i32 {
  return a + b + 1 >> 1;
}

// The block of `size` by `size` at `o` filled with `v`.
function filled(own w: u8[], o: i32, size: i32, v: i32): u8[] {
  for y in 0..size {
    for x in 0..size {
      w = w.with(o + y * BPS + x, v as u8);
    }
  }
  return w;
}

// The prediction of a 16x16 luma or an 8x8 chroma block at `o`, for the
// macroblock at `mb_x`, `mb_y`. The DC mode uses only the edges inside the
// frame (section 12.2).
function predicted_block(own w: u8[], o: i32, size: i32, mode: i32, mb_x: i32, mb_y: i32): u8[] {
  if (mode == DC_PRED) {
    let dc = dc_value(w, o, size, mb_x, mb_y);
    return filled(w, o, size, dc);
  }
  let corner = w[o - BPS - 1] as i32;
  for y in 0..size {
    let left = w[o - 1 + y * BPS] as i32;
    for x in 0..size {
      let top = w[o - BPS + x] as i32;
      let v = match (mode) {
        TM_PRED => clip255(top + left - corner),
        VE_PRED => top,
        _ => left,
      };
      w = w.with(o + y * BPS + x, v as u8);
    }
  }
  return w;
}

// The DC prediction of a block of `size` by `size`, from the edges that are
// inside the frame.
function dc_value(w: [u8], o: i32, size: i32, mb_x: i32, mb_y: i32): i32 {
  let shift = if (size == 16) { 4 } else { 3 };
  let top = 0;
  let left = 0;
  for i in 0..size {
    top += w[o - BPS + i] as i32;
    left += w[o - 1 + i * BPS] as i32;
  }
  if (mb_x > 0 && mb_y > 0) {
    return top + left + size >> shift + 1;
  }
  if (mb_y > 0) {
    return top + (size >> 1) >> shift;
  }
  if (mb_x > 0) {
    return left + (size >> 1) >> shift;
  }
  return 128;
}

// The 16 pixels of the prediction of a 4x4 block in the mode `mode`, row by
// row (section 12.3), from the pixels above on the left `x0`, above `t` (8
// pixels) and on the left `l` (4 pixels).
function subblock(mode: i32, x0: i32, t: [i32], l: [i32]): i32[] {
  let (a, b, c, d, e, f, g, h) = (t[0], t[1], t[2], t[3], t[4], t[5], t[6], t[7]);
  let (i, j, k, m) = (l[0], l[1], l[2], l[3]);
  return match (mode) {
    0 => {
      let dc = a + b + c + d + i + j + k + m + 4 >> 3;
      [dc, dc, dc, dc, dc, dc, dc, dc, dc, dc, dc, dc, dc, dc, dc, dc]
    },
    1 => true_motion4(x0, t, l),
    2 => {
      let row = [avg3(x0, a, b), avg3(a, b, c), avg3(b, c, d), avg3(c, d, e)];
      row.concat(row).concat(row).concat(row)
    },
    3 => {
      let r0 = avg3(x0, i, j);
      let r1 = avg3(i, j, k);
      let r2 = avg3(j, k, m);
      let r3 = avg3(k, m, m);
      [r0, r0, r0, r0, r1, r1, r1, r1, r2, r2, r2, r2, r3, r3, r3, r3]
    },
    4 => down_right4([m, k, j, i, x0, a, b, c, d]),
    5 => [avg2(x0, a), avg2(a, b), avg2(b, c), avg2(c, d), avg3(i, x0, a), avg3(x0, a, b), avg3(a, b, c), avg3(b, c, d), avg3(j, i, x0), avg2(x0, a), avg2(a, b), avg2(b, c), avg3(k, j, i), avg3(i, x0, a), avg3(x0, a, b), avg3(a, b, c)],
    6 => down_left4(t),
    7 => [avg2(a, b), avg2(b, c), avg2(c, d), avg2(d, e), avg3(a, b, c), avg3(b, c, d), avg3(c, d, e), avg3(d, e, f), avg2(b, c), avg2(c, d), avg2(d, e), avg3(e, f, g), avg3(b, c, d), avg3(c, d, e), avg3(d, e, f), avg3(f, g, h)],
    8 => [avg2(i, x0), avg3(i, x0, a), avg3(x0, a, b), avg3(a, b, c), avg2(j, i), avg3(j, i, x0), avg2(i, x0), avg3(i, x0, a), avg2(k, j), avg3(k, j, i), avg2(j, i), avg3(j, i, x0), avg2(m, k), avg3(m, k, j), avg2(k, j), avg3(k, j, i)],
    _ => [avg2(i, j), avg3(i, j, k), avg2(j, k), avg3(j, k, m), avg2(j, k), avg3(j, k, m), avg2(k, m), avg3(k, m, m), avg2(k, m), avg3(k, m, m), m, m, m, m, m, m],
  };
}

function true_motion4(x0: i32, t: [i32], l: [i32]): i32[] {
  let out: i32[] = [];
  for y in 0..4 {
    for x in 0..4 {
      out = out.append(clip255(t[x] + l[y] - x0));
    }
  }
  return out;
}

// The down-right prediction from the edge from the bottom left pixel, up the
// left column, through the corner and along the top row.
function down_right4(edge: [i32]): i32[] {
  let out: i32[] = [];
  for y in 0..4 {
    for x in 0..4 {
      let s = x - y + 3;
      out = out.append(avg3(edge[s], edge[s + 1], edge[s + 2]));
    }
  }
  return out;
}

function down_left4(t: [i32]): i32[] {
  let out: i32[] = [];
  for y in 0..4 {
    for x in 0..4 {
      let s = x + y;
      out = out.append(avg3(t[s], t[s + 1], t[(s + 2).min(7)]));
    }
  }
  return out;
}

// The prediction of the 4x4 block at `o`.
function predicted4(own w: u8[], o: i32, mode: i32): u8[] {
  let t: i32[] = [];
  for x in 0..8 {
    t = t.append(w[o - BPS + x] as i32);
  }
  let l = [w[o - 1] as i32, w[o - 1 + BPS] as i32, w[o - 1 + 2 * BPS] as i32, w[o - 1 + 3 * BPS] as i32];
  let px = subblock(mode, w[o - BPS - 1] as i32, t, l);
  for y in 0..4 {
    for x in 0..4 {
      w = w.with(o + y * BPS + x, px[y * 4 + x] as u8);
    }
  }
  return w;
}

function mul1(a: i32): i32 {
  return (a * 20091 >> 16) + a;
}

function mul2(a: i32): i32 {
  return a * 35468 >> 16;
}

// The block at `o` with the inverse DCT of the coefficients at `base` added
// (section 14.3). A block whose coefficients are all 0 is not changed.
function idct_added(coef: [i32], base: i32, own w: u8[], o: i32): u8[] {
  let zero = true;
  for k in base..base + 16 {
    if (coef[k] != 0) {
      zero = false;
      break;
    }
  }
  if (zero) {
    return w;
  }
  let tmp = __alloc_i32(16);
  for i in 0..4 {
    let a = coef[base + i] + coef[base + 8 + i];
    let b = coef[base + i] - coef[base + 8 + i];
    let c = mul2(coef[base + 4 + i]) - mul1(coef[base + 12 + i]);
    let d = mul1(coef[base + 4 + i]) + mul2(coef[base + 12 + i]);
    tmp = tmp.with(i * 4, a + d).with(i * 4 + 1, b + c).with(i * 4 + 2, b - c).with(i * 4 + 3, a - d);
  }
  for i in 0..4 {
    let dc = tmp[i] + 4;
    let a = dc + tmp[8 + i];
    let b = dc - tmp[8 + i];
    let c = mul2(tmp[4 + i]) - mul1(tmp[12 + i]);
    let d = mul1(tmp[4 + i]) + mul2(tmp[12 + i]);
    let row = o + i * BPS;
    w = w.with(row, clip255(w[row] as i32 + (a + d >> 3)) as u8);
    w = w.with(row + 1, clip255(w[row + 1] as i32 + (b + c >> 3)) as u8);
    w = w.with(row + 2, clip255(w[row + 2] as i32 + (b - c >> 3)) as u8);
    w = w.with(row + 3, clip255(w[row + 3] as i32 + (a - d >> 3)) as u8);
  }
  return w;
}

// The planes of the frame before the loop filter, `mb_w * 16` and `mb_w * 8`
// pixels across, the work buffer, and the bottom rows of the macroblocks
// above, before the loop filter.
struct Recon { y: u8[], u: u8[], v: u8[], work: u8[], top_y: u8[], top_u: u8[], top_v: u8[] }

// The work buffer at the start of the macroblock `mb_x`, `mb_y`.
function edges(own w: u8[], top_y: [u8], top_u: [u8], top_v: [u8], mb_x: i32, mb_y: i32): u8[] {
  w = if (mb_x == 0) { frame_left(w, mb_y) } else { shifted_left(w) };
  if (mb_y > 0) {
    for k in 0..16 {
      w = w.with(Y_AT - BPS + k, top_y[mb_x * 16 + k]);
    }
    for k in 0..8 {
      w = w.with(U_AT - BPS + k, top_u[mb_x * 8 + k]).with(V_AT - BPS + k, top_v[mb_x * 8 + k]);
    }
  }
  return w;
}

// The work buffer with the edges of the left of the frame, and in the first
// row the edge above the frame.
function frame_left(own w: u8[], mb_y: i32): u8[] {
  for j in 0..16 {
    w = w.with(Y_AT - 1 + j * BPS, 129u8);
  }
  for j in 0..8 {
    w = w.with(U_AT - 1 + j * BPS, 129u8).with(V_AT - 1 + j * BPS, 129u8);
  }
  if (mb_y > 0) {
    return w.with(Y_AT - BPS - 1, 129u8).with(U_AT - BPS - 1, 129u8).with(V_AT - BPS - 1, 129u8);
  }
  for k in -1..20 {
    w = w.with(Y_AT - BPS + k, 127u8);
  }
  for k in -1..8 {
    w = w.with(U_AT - BPS + k, 127u8).with(V_AT - BPS + k, 127u8);
  }
  return w;
}

// The work buffer with the four right columns of the last macroblock, and the
// row above them, moved to the four columns on the left.
function shifted_left(own w: u8[]): u8[] {
  for j in -1..16 {
    for k in 0..4 {
      w = w.with(Y_AT + j * BPS - 4 + k, w[Y_AT + j * BPS + 12 + k]);
    }
  }
  for j in -1..8 {
    for k in 0..4 {
      w = w.with(U_AT + j * BPS - 4 + k, w[U_AT + j * BPS + 4 + k]).with(V_AT + j * BPS - 4 + k, w[V_AT + j * BPS + 4 + k]);
    }
  }
  return w;
}

// The work buffer with the pixels above on the right of a macroblock of 4x4
// blocks, and their copies for the blocks in its right column.
function top_right(own w: u8[], top_y: [u8], mb_x: i32, mb_y: i32, mb_w: i32): u8[] {
  if (mb_y > 0) {
    for k in 0..4 {
      let p = if (mb_x == mb_w - 1) { top_y[mb_x * 16 + 15] } else { top_y[mb_x * 16 + 16 + k] };
      w = w.with(Y_AT - BPS + 16 + k, p);
    }
  }
  for row in [3, 7, 11] {
    for k in 0..4 {
      w = w.with(Y_AT + row * BPS + 16 + k, w[Y_AT - BPS + 16 + k]);
    }
  }
  return w;
}

// The reconstruction with the macroblock at `mb_x`, `mb_y` added.
function with_macroblock(own r: Recon, m: Modes, coef: [i32], mb_x: i32, mb_y: i32, mb_w: i32, mb_h: i32): Recon {
  let Recon { y, u, v, work, top_y, top_u, top_v } = r;
  let w = edges(work, top_y, top_u, top_v, mb_x, mb_y);
  if (m.i4) {
    w = top_right(w, top_y, mb_x, mb_y, mb_w);
    for n in 0..16 {
      let o = Y_AT + (n & 3) * 4 + (n >> 2) * 4 * BPS;
      w = idct_added(coef, n * 16, predicted4(w, o, m.y[n]), o);
    }
  } else {
    w = predicted_block(w, Y_AT, 16, m.y[0], mb_x, mb_y);
    for n in 0..16 {
      w = idct_added(coef, n * 16, w, Y_AT + (n & 3) * 4 + (n >> 2) * 4 * BPS);
    }
  }
  w = predicted_block(predicted_block(w, U_AT, 8, m.uv, mb_x, mb_y), V_AT, 8, m.uv, mb_x, mb_y);
  for n in 0..4 {
    let o = (n & 1) * 4 + (n >> 1) * 4 * BPS;
    w = idct_added(coef, (16 + n) * 16, w, U_AT + o);
    w = idct_added(coef, (20 + n) * 16, w, V_AT + o);
  }
  if (mb_y < mb_h - 1) {
    top_y = stashed(top_y, w, Y_AT, 16, mb_x);
    top_u = stashed(top_u, w, U_AT, 8, mb_x);
    top_v = stashed(top_v, w, V_AT, 8, mb_x);
  }
  y = copied_block(y, w, Y_AT, 16, mb_w * 16, mb_x, mb_y);
  u = copied_block(u, w, U_AT, 8, mb_w * 8, mb_x, mb_y);
  v = copied_block(v, w, V_AT, 8, mb_w * 8, mb_x, mb_y);
  return Recon { y: y, u: u, v: v, work: w, top_y: top_y, top_u: top_u, top_v: top_v };
}

// The bottom row of the block of `size` by `size` at `o`, kept for the
// prediction of the macroblock below.
function stashed(own top: u8[], w: [u8], o: i32, size: i32, mb_x: i32): u8[] {
  for k in 0..size {
    top = top.with(mb_x * size + k, w[o + (size - 1) * BPS + k]);
  }
  return top;
}

// The plane, `stride` pixels across, with the block of `size` by `size` at
// `o` copied into the macroblock at `mb_x`, `mb_y`.
function copied_block(own plane: u8[], w: [u8], o: i32, size: i32, stride: i32, mb_x: i32, mb_y: i32): u8[] {
  for j in 0..size {
    let at = (mb_y * size + j) * stride + mb_x * size;
    for k in 0..size {
      plane = plane.with(at + k, w[o + j * BPS + k]);
    }
  }
  return plane;
}
```

### `lossy loop filter`

The loop filter runs over the macroblocks in raster order after the reconstruction, with the functions of section 15 in the form of libwebp. For each macroblock it filters the left edge, the inner vertical edges, the top edge and the inner horizontal edges. It filters the inner edges only in a macroblock of 4x4 blocks or with a coefficient that is not 0. The simple filter changes only the luma.

```fern
<<lossy loop filter>>=
// The filter of two pixels on each side of the edge at `p`, across `step`.
function filtered2(own px: u8[], p: i32, step: i32): u8[] {
  let p1 = px[p - 2 * step] as i32;
  let p0 = px[p - step] as i32;
  let q0 = px[p] as i32;
  let q1 = px[p + step] as i32;
  let a = 3 * (q0 - p0) + (p1 - q1).clamp(-128, 127);
  let a1 = (a + 4 >> 3).clamp(-16, 15);
  let a2 = (a + 3 >> 3).clamp(-16, 15);
  return px.with(p - step, clip255(p0 + a2) as u8).with(p, clip255(q0 - a1) as u8);
}

function filtered4(own px: u8[], p: i32, step: i32): u8[] {
  let p1 = px[p - 2 * step] as i32;
  let p0 = px[p - step] as i32;
  let q0 = px[p] as i32;
  let q1 = px[p + step] as i32;
  let a = 3 * (q0 - p0);
  let a1 = (a + 4 >> 3).clamp(-16, 15);
  let a2 = (a + 3 >> 3).clamp(-16, 15);
  let a3 = a1 + 1 >> 1;
  return px.with(p - 2 * step, clip255(p1 + a3) as u8).with(p - step, clip255(p0 + a2) as u8).with(p, clip255(q0 - a1) as u8).with(p + step, clip255(q1 - a3) as u8);
}

function filtered6(own px: u8[], p: i32, step: i32): u8[] {
  let p2 = px[p - 3 * step] as i32;
  let p1 = px[p - 2 * step] as i32;
  let p0 = px[p - step] as i32;
  let q0 = px[p] as i32;
  let q1 = px[p + step] as i32;
  let q2 = px[p + 2 * step] as i32;
  let a = (3 * (q0 - p0) + (p1 - q1).clamp(-128, 127)).clamp(-128, 127);
  let a1 = 27 * a + 63 >> 7;
  let a2 = 18 * a + 63 >> 7;
  let a3 = 9 * a + 63 >> 7;
  return px.with(p - 3 * step, clip255(p2 + a3) as u8).with(p - 2 * step, clip255(p1 + a2) as u8).with(p - step, clip255(p0 + a1) as u8).with(p, clip255(q0 - a1) as u8).with(p + step, clip255(q1 - a2) as u8).with(p + 2 * step, clip255(q2 - a3) as u8);
}

function diff(px: [u8], a: i32, b: i32): i32 {
  return (px[a] as i32 - px[b] as i32).abs();
}

// Whether the edge at `p` is below the limit `t` of the simple filter.
function edge_below(px: [u8], p: i32, step: i32, t: i32): boolean {
  return 4 * diff(px, p - step, p) + diff(px, p - 2 * step, p + step) <= t;
}

// Whether the edge at `p` is below the limit `t` and each step inside it is
// below the interior limit `it`.
function edge_below2(px: [u8], p: i32, step: i32, t: i32, it: i32): boolean {
  if (!edge_below(px, p, step, t)) {
    return false;
  }
  return diff(px, p - 4 * step, p - 3 * step) <= it && diff(px, p - 3 * step, p - 2 * step) <= it && diff(px, p - 2 * step, p - step) <= it && diff(px, p + 3 * step, p + 2 * step) <= it && diff(px, p + 2 * step, p + step) <= it && diff(px, p + step, p) <= it;
}

function high_variance(px: [u8], p: i32, step: i32, t: i32): boolean {
  return diff(px, p - 2 * step, p - step) > t || diff(px, p + step, p) > t;
}

// The simple filter of `n` pixels along an edge from `p`, each `along` from
// the last, across `step`.
function simple_edge(own px: u8[], p: i32, step: i32, along: i32, n: i32, limit: i32): u8[] {
  let t = 2 * limit + 1;
  for i in 0..n {
    let at = p + i * along;
    if (edge_below(px, at, step, t)) {
      px = filtered2(px, at, step);
    }
  }
  return px;
}

// The normal filter of `n` pixels along an edge. An edge between macroblocks
// (`outer`) changes three pixels on each side, and an inner edge two.
function normal_edge(own px: u8[], p: i32, step: i32, along: i32, n: i32, limit: i32, interior: i32, hev: i32, outer: boolean): u8[] {
  let t = 2 * limit + 1;
  for i in 0..n {
    let at = p + i * along;
    if (edge_below2(px, at, step, t, interior)) {
      px = if (high_variance(px, at, step, hev)) { filtered2(px, at, step) } else if (outer) { filtered6(px, at, step) } else { filtered4(px, at, step) };
    }
  }
  return px;
}

// The edges of the macroblock at `o` in a plane `stride` pixels across, of
// `size` pixels. The parameters are those of the normal filter.
function plane_edges(own px: u8[], o: i32, stride: i32, size: i32, left: boolean, above: boolean, inner: boolean, limit: i32, interior: i32, hev: i32): u8[] {
  if (left) {
    px = normal_edge(px, o, 1, stride, size, limit + 4, interior, hev, true);
  }
  if (inner) {
    let k = 4;
    while (k < size) {
      px = normal_edge(px, o + k, 1, stride, size, limit, interior, hev, false);
      k += 4;
    }
  }
  if (above) {
    px = normal_edge(px, o, stride, 1, size, limit + 4, interior, hev, true);
  }
  if (inner) {
    let k = 4;
    while (k < size) {
      px = normal_edge(px, o + k * stride, stride, 1, size, limit, interior, hev, false);
      k += 4;
    }
  }
  return px;
}

// The luma of a macroblock with the simple filter.
function simple_edges(own px: u8[], o: i32, stride: i32, left: boolean, above: boolean, inner: boolean, limit: i32): u8[] {
  if (left) {
    px = simple_edge(px, o, 1, stride, 16, limit + 4);
  }
  if (inner) {
    for k in [4, 8, 12] {
      px = simple_edge(px, o + k, 1, stride, 16, limit);
    }
  }
  if (above) {
    px = simple_edge(px, o, stride, 1, 16, limit + 4);
  }
  if (inner) {
    for k in [4, 8, 12] {
      px = simple_edge(px, o + k * stride, stride, 1, 16, limit);
    }
  }
  return px;
}

// The planes after the loop filter. `info` holds the limit, the interior
// limit, the threshold of high edge variance and the inner flag of each
// macroblock.
function loop_filtered(own r: Recon, h: Lossy, info: [i32]): Recon {
  if (h.filter_type == 0) {
    return r;
  }
  let Recon { y, u, v, work, top_y, top_u, top_v } = r;
  let ys = h.mb_w * 16;
  let cs = h.mb_w * 8;
  for mb_y in 0..h.mb_h {
    for mb_x in 0..h.mb_w {
      let at = (mb_y * h.mb_w + mb_x) * 4;
      let limit = info[at];
      if (limit > 0) {
        let inner = info[at + 3] == 1;
        if (h.filter_type == 1) {
          y = simple_edges(y, mb_y * 16 * ys + mb_x * 16, ys, mb_x > 0, mb_y > 0, inner, limit);
        } else {
          y = plane_edges(y, mb_y * 16 * ys + mb_x * 16, ys, 16, mb_x > 0, mb_y > 0, inner, limit, info[at + 1], info[at + 2]);
          let co = mb_y * 8 * cs + mb_x * 8;
          u = plane_edges(u, co, cs, 8, mb_x > 0, mb_y > 0, inner, limit, info[at + 1], info[at + 2]);
          v = plane_edges(v, co, cs, 8, mb_x > 0, mb_y > 0, inner, limit, info[at + 1], info[at + 2]);
        }
      }
    }
  }
  return Recon { y: y, u: u, v: v, work: work, top_y: top_y, top_u: top_u, top_v: top_v };
}
```

### `lossy decoding`

The decoder reads the macroblocks in raster order. The modes come from the first partition. The coefficients of the row `mb_y` come from the token partition `mb_y` modulo the count of partitions. A read past the end of a partition fails with `Truncated` at the macroblock that made it, as in libwebp. Thus a short file stops after the bytes that it has, and it does not decode the rest of a large frame.

The output has the fancy upsampling of libwebp: each chroma sample between two luma rows or columns has the weights 3/4 and 1/4, from the nearer and the farther sample. The conversion to RGB is the integer conversion of libwebp, with 14 bits of precision.

```fern
<<lossy decoding>>=
// The planes of the frame after the loop filter.
function lossy_planes(data: [u8], h: Lossy, own first: Bool, own parts: Bool[]): Result[Recon, DecodeError] {
  let tab = lossy_tables();
  let mb_w = h.mb_w;
  let r = Recon { y: __alloc_u8(mb_w * 16 * h.mb_h * 16), u: __alloc_u8(mb_w * 8 * h.mb_h * 8), v: __alloc_u8(mb_w * 8 * h.mb_h * 8), work: __alloc_u8(BPS * 27), top_y: __alloc_u8(mb_w * 16), top_u: __alloc_u8(mb_w * 8), top_v: __alloc_u8(mb_w * 8) };
  let info = __alloc_i32(mb_w * h.mb_h * 4);
  let above = __alloc_i32(mb_w * 4);
  let nz = Nz { top: __alloc_i32(mb_w), top_dc: __alloc_i32(mb_w), left: 0, left_dc: 0 };
  let modes_reader = first;
  for mb_y in 0..h.mb_h {
    let left = [DC_PRED, DC_PRED, DC_PRED, DC_PRED];
    nz = Nz { ...nz, left: 0, left_dc: 0 };
    let part = mb_y & parts.len() - 1;
    let tokens = parts[part];
    for mb_x in 0..mb_w {
      let (m, r1, above1, left1) = modes_of(modes_reader, data, h, tab, above, left, mb_x);
      modes_reader = r1;
      above = above1;
      left = left1;
      let (coef, empty, nz1, t1) = macroblock_coeffs(tokens, data, tab, h, m, nz, mb_x);
      nz = nz1;
      tokens = t1;
      if (tokens.eof || modes_reader.eof) {
        return Err(Truncated);
      }
      info = with_filter(info, h, m, empty, (mb_y * mb_w + mb_x) * 4);
      r = with_macroblock(r, m, coef, mb_x, mb_y, mb_w, h.mb_h);
    }
    parts = parts.with(part, tokens);
  }
  return Ok(loop_filtered(r, h, info));
}

// The coefficients of the macroblock with the modes `m`, and whether it has no
// coefficient that is not 0. A skipped macroblock has none and reads no token.
function macroblock_coeffs(own tokens: Bool, data: [u8], tab: Tables, h: Lossy, m: Modes, own nz: Nz, mb_x: i32): (i32[], boolean, Nz, Bool) {
  if (!m.skip) {
    return residuals(tokens, data, tab, h.probs, h.quant, m.segment * 6, m.i4, nz, mb_x);
  }
  let top_dc = if (m.i4) { nz.top_dc } else { nz.top_dc.with(mb_x, 0) };
  let left_dc = if (m.i4) { nz.left_dc } else { 0 };
  return (__alloc_i32(384), true, Nz { top: nz.top.with(mb_x, 0), top_dc: top_dc, left: 0, left_dc: left_dc }, tokens);
}

// The filter parameters at `at` for the macroblock with the modes `m`. The
// inner edges are filtered in a macroblock of 4x4 blocks or with a
// coefficient that is not 0.
function with_filter(own info: i32[], h: Lossy, m: Modes, empty: boolean, at: i32): i32[] {
  let f = (m.segment * 2 + (if (m.i4) { 1 } else { 0 })) * 3;
  let inner = if (m.i4 || !empty) { 1 } else { 0 };
  return info.with(at, h.filters[f]).with(at + 1, h.filters[f + 1]).with(at + 2, h.filters[f + 2]).with(at + 3, inner);
}

// The red, green and blue of a pixel, from section 8 of the code of libwebp
// (`yuv.h`).
function clip8(v: i32): i32 {
  if ((v & -16384) == 0) {
    return v >> 6;
  }
  return if (v < 0) { 0 } else { 255 };
}

function rgb_into(own out: u8[], at: i32, y: i32, u: i32, v: i32): u8[] {
  let luma = y * 19077 >> 8;
  let r = clip8(luma + (v * 26149 >> 8) - 14234);
  let g = clip8(luma - (u * 6419 >> 8) - (v * 13320 >> 8) + 8708);
  let b = clip8(luma + (u * 33050 >> 8) - 17685);
  return out.with(at, r as u8).with(at + 1, g as u8).with(at + 2, b as u8);
}

// The samples of a chroma row at `near` and `far` for the luma column `x`, in
// the fancy upsampling of libwebp: `near` is the nearer row.
function upsampled(c: [u8], near: i32, far: i32, x: i32, width: i32): i32 {
  if (x == 0 || x == width - 1 && (width & 1) == 0) {
    let k = x >> 1;
    return 3 * (c[near + k] as i32) + c[far + k] as i32 + 2 >> 2;
  }
  let k = x + 1 >> 1;
  let tl = c[near + k - 1] as i32;
  let t = c[near + k] as i32;
  let l = c[far + k - 1] as i32;
  let cur = c[far + k] as i32;
  let avg = tl + t + l + cur + 8;
  if ((x & 1) == 1) {
    return (avg + 2 * (t + l) >> 3) + tl >> 1;
  }
  return (avg + 2 * (tl + cur) >> 3) + t >> 1;
}

// The pixels of the frame: red, green, blue and alpha for each pixel. Alpha
// is 255.
function lossy_rgba(r: Recon, h: Lossy): u8[] {
  let out = __alloc_u8(h.width * h.height * 4);
  let ys = h.mb_w * 16;
  let cs = h.mb_w * 8;
  let last = (h.height + 1 >> 1) - 1;
  for j in 0..h.height {
    let near = if (j == 0) { 0 } else if ((j & 1) == 1) { j - 1 >> 1 } else { j >> 1 };
    let far = if (j == 0) { 0 } else if ((j & 1) == 1) { (j + 1 >> 1).min(last) } else { (j >> 1) - 1 };
    for x in 0..h.width {
      let u = upsampled(r.u, near * cs, far * cs, x, h.width);
      let v = upsampled(r.v, near * cs, far * cs, x, h.width);
      let at = (j * h.width + x) * 4;
      out = rgb_into(out, at, r.y[j * ys + x] as i32, u, v).with(at + 3, 255u8);
    }
  }
  return out;
}
```

### `alpha`

An `ALPH` chunk gives the alpha of a lossy image. Its first byte gives the compression in bits 0 and 1, and the filter in bits 2 and 3. Bits 4 and 5 give the preprocessing, which has no effect on the decode. Bits 6 and 7 are 0. The alpha values follow as bytes, or as a lossless image stream without its header. The green values of that image are the alpha values.

The filter predicts each value from the value on the left (1) or above (2). The filter 3 predicts from these values and the value above on the left. The first row is predicted from the left, and the first value of a row from above.

```fern
<<alpha>>=
// The alpha values of a `width` by `height` image from the `ALPH` chunk
// with `size` bytes of data at `at`.
function alpha_values(file: [u8], at: i32, size: i32, width: i32, height: i32): Result[u8[], DecodeError] {
  if (size <= 1) {
    return Err(BadHeader);
  }
  let head = file[at] as i32;
  let method = head & 3;
  let filter = head >> 2 & 3;
  if (method > 1 || (head >> 4 & 3) > 1 || head >> 6 != 0) {
    return Err(BadHeader);
  }
  let raw = if (method == 0) { raw_alpha(file, at + 1, size - 1, width * height)? } else { lossless_alpha(file, at + 1, at + size, width, height)? };
  return Ok(unfiltered(raw, width, height, filter));
}

function raw_alpha(file: [u8], at: i32, size: i32, total: i32): Result[u8[], DecodeError] {
  if (size < total) {
    return Err(Truncated);
  }
  let raw = __alloc_u8(total);
  for i in 0..total {
    raw = raw.with(i, file[at + i]);
  }
  return Ok(raw);
}

// The green values of the lossless image stream from `at` to `end`, which has
// no header.
function lossless_alpha(file: [u8], at: i32, end: i32, width: i32, height: i32): Result[u8[], DecodeError] {
  let b = Bits { at: at, end: end, padded: end.max(at + 8), word: 0i64, count: 0, failed: false, error: BadHeader };
  let (transforms, coded_width, cur) = read_transforms(b, file, width, height);
  if (cur.failed) {
    return Err(cur.error);
  }
  let (px, b1) = coded_image(cur, file, coded_width, height, true);
  if (b1.failed) {
    return Err(b1.error);
  }
  let argb = undone_all(px, transforms, height);
  let raw = __alloc_u8(width * height);
  for i in 0..raw.len() {
    raw = raw.with(i, byte_of(argb[i], 8) as u8);
  }
  return Ok(raw);
}

// The alpha values with the prediction of the filter added.
function unfiltered(own a: u8[], width: i32, height: i32, filter: i32): u8[] {
  if (filter == 0) {
    return a;
  }
  for y in 0..height {
    let row = y * width;
    for x in 0..width {
      let at = row + x;
      let pred = if (y == 0) {
        if (x == 0) { 0 } else { a[at - 1] as i32 }
      } else if (x == 0) {
        a[at - width] as i32
      } else {
        match (filter) {
          1 => a[at - 1] as i32,
          2 => a[at - width] as i32,
          _ => clip255(a[at - 1] as i32 + a[at - width] as i32 - a[at - width - 1] as i32),
        }
      };
      a = a.with(at, (a[at] as i32 + pred & 255) as u8);
    }
  }
  return a;
}

// The image of the `VP8 ` chunk with `chunk` bytes of data at `start`, which
// can read to `end`, with the alpha of the `ALPH` chunk at `alpha` when it is
// not -1. Its size must be `want_width` by `want_height`, unless that is 0 by
// 0.
function lossy_image(file: [u8], start: i32, chunk: i32, end: i32, alpha: i32, want_width: i32, want_height: i32): Result[Image, DecodeError] {
  let (h, first, parts) = frame_header(file, start, chunk, end)?;
  if (want_width > 0 && (want_width != h.width || want_height != h.height)) {
    return Err(BadHeader);
  }
  let planes = lossy_planes(file, h, first, parts)?;
  let px = lossy_rgba(planes, h);
  if (alpha >= 0) {
    let a = alpha_values(file, alpha + 8, u32_at(file, alpha + 4), h.width, h.height)?;
    for i in 0..a.len() {
      px = px.with(i * 4 + 3, a[i]);
    }
  }
  return Ok(Image { width: h.width, height: h.height, pixels: px });
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

// The image of the `VP8 ` or `VP8L` chunk from `at` to `end`, which must be
// `width` by `height`, and whether the image uses alpha. One `ALPH` chunk can
// come before a `VP8 ` chunk, as the demuxer of libwebp reads a frame.
function frame_image(file: [u8], at: i32, end: i32, width: i32, height: i32): Result[(Image, boolean), DecodeError] {
  let inner = at;
  let alpha = -1;
  while (inner + 8 <= end) {
    let size = u32_at(file, inner + 4);
    if (size < 0 || size > end - inner - 8) {
      return Err(BadHeader);
    }
    if (tag_at(file, inner, "VP8 ") || tag_at(file, inner, "VP8L")) {
      return framed(file, inner, size, alpha, width, height);
    }
    if (!tag_at(file, inner, "ALPH") || alpha >= 0) {
      return Err(BadHeader);
    }
    alpha = inner;
    inner += 8 + size + size % 2;
  }
  return Err(BadHeader);
}

// The image of the `VP8 ` or `VP8L` chunk at `at` with `size` bytes of data
// in a frame, with the `ALPH` chunk at `alpha` before it, or -1.
function framed(file: [u8], at: i32, size: i32, alpha: i32, width: i32, height: i32): Result[(Image, boolean), DecodeError] {
  if (tag_at(file, at, "VP8 ")) {
    let image = lossy_image(file, at + 8, size, at + 8 + size, alpha, width, height)?;
    return Ok((image, alpha >= 0));
  }
  if (alpha >= 0) {
    return Err(BadHeader);
  }
  let image = lossless_image(file, at + 8, at + 8 + size, width, height)?;
  return Ok((image, (file[at + 12] as i32 >> 4 & 1) == 1));
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

// The frames of the WebP file `file`, animated or still.
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
