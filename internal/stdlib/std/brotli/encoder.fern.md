# A fast Brotli encoder

This encoder writes RFC 7932 streams with LZ77 matches and canonical Huffman codes.
It uses one literal context and a 64 KiB history window. Fixed prefix trees avoid a frequency pass.
The match table uses a fixed amount of memory. Incompressible input uses stored meta-blocks.

```fern
<<*>>=
// This module encodes Brotli streams with bounded LZ77 history and fixed prefix trees.

<<Types and length codes>>
<<Write bits>>
<<Describe prefix trees>>
<<Encode commands>>
<<Find matches>>
<<Encode a stream>>
<<Stored blocks>>
```

## Types and length codes

A writer owns its output buffer. The accumulator keeps at most seven bits between writes.
Insert and copy lengths use the base and extra-bit tables from section 5 of the RFC.

```fern
<<Types and length codes>>=
import "std/array";

struct Writer { bytes: u8[], at: i32, acc: u64, used: i32 }

const REVERSE_BYTE: u8[] = [0, 128, 64, 192, 32, 160, 96, 224, 16, 144, 80, 208, 48, 176, 112, 240, 8, 136, 72, 200, 40, 168, 104, 232, 24, 152, 88, 216, 56, 184, 120, 248, 4, 132, 68, 196, 36, 164, 100, 228, 20, 148, 84, 212, 52, 180, 116, 244, 12, 140, 76, 204, 44, 172, 108, 236, 28, 156, 92, 220, 60, 188, 124, 252, 2, 130, 66, 194, 34, 162, 98, 226, 18, 146, 82, 210, 50, 178, 114, 242, 10, 138, 74, 202, 42, 170, 106, 234, 26, 154, 90, 218, 58, 186, 122, 250, 6, 134, 70, 198, 38, 166, 102, 230, 22, 150, 86, 214, 54, 182, 118, 246, 14, 142, 78, 206, 46, 174, 110, 238, 30, 158, 94, 222, 62, 190, 126, 254, 1, 129, 65, 193, 33, 161, 97, 225, 17, 145, 81, 209, 49, 177, 113, 241, 9, 137, 73, 201, 41, 169, 105, 233, 25, 153, 89, 217, 57, 185, 121, 249, 5, 133, 69, 197, 37, 165, 101, 229, 21, 149, 85, 213, 53, 181, 117, 245, 13, 141, 77, 205, 45, 173, 109, 237, 29, 157, 93, 221, 61, 189, 125, 253, 3, 131, 67, 195, 35, 163, 99, 227, 19, 147, 83, 211, 51, 179, 115, 243, 11, 139, 75, 203, 43, 171, 107, 235, 27, 155, 91, 219, 59, 187, 123, 251, 7, 135, 71, 199, 39, 167, 103, 231, 23, 151, 87, 215, 55, 183, 119, 247, 15, 143, 79, 207, 47, 175, 111, 239, 31, 159, 95, 223, 63, 191, 127, 255];

const INSERT_BASE: i32[] = [0, 1, 2, 3, 4, 5, 6, 8, 10, 14, 18, 26, 34, 50, 66, 98, 130, 194, 322, 578, 1090, 2114, 6210, 22594];

const INSERT_EXTRA: i32[] = [0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 12, 14, 24];

const COPY_BASE: i32[] = [2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 14, 18, 22, 30, 38, 54, 70, 102, 134, 198, 326, 582, 1094, 2118];

const COPY_EXTRA: i32[] = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 24];
```

## Write bits

Brotli packs fields from the least significant bit. Huffman codes use the opposite bit order.
The owned writer lets the compiler reuse its buffer and record across calls.

```fern
<<Write bits>>=
fip function write_byte(own bytes: u8[], at: i32, value: u8): u8[] {
  return bytes.with(at, value);
}

function put(own w: Writer, value: i32, bits: i32): Writer {
  let acc: u64 = w.acc | (value as u32) as u64 << w.used as u64;
  let used: i32 = w.used + bits;
  let at: i32 = w.at;
  let bytes: u8[] = w.bytes;
  while (used >= 8) {
    bytes = write_byte(bytes, at, (acc & 255) as u8);
    at = at + 1;
    acc = acc >> 8;
    used = used - 8;
  }
  return Writer { bytes: bytes, at: at, acc: acc, used: used };
}

fip function reverse(value: i32, bits: i32): i32 {
  let v: i32 = value;
  v = v >> 1 & 0x5555 | (v & 0x5555) << 1;
  v = v >> 2 & 0x3333 | (v & 0x3333) << 2;
  v = v >> 4 & 0x0f0f | (v & 0x0f0f) << 4;
  v = v >> 8 | (v & 255) << 8;
  return v >> 16 - bits;
}

function code(own w: Writer, value: i32, bits: i32): Writer {
  return put(w, reverse(value, bits), bits);
}

function literals(own w: Writer, input: [u8], from: i32, to: i32): Writer {
  let used: i32 = w.used;
  let shift: u64 = used as u64;
  let acc: u64 = w.acc;
  let at: i32 = w.at;
  let bytes: u8[] = w.bytes;
  let position: i32 = from;
  while (position < to) {
    let packed: u64 = acc | REVERSE_BYTE[input[position] as i32] as u64 << shift;
    bytes = write_byte(bytes, at, (packed & 255) as u8);
    acc = packed >> 8;
    at = at + 1;
    position = position + 1;
  }
  return Writer { bytes: bytes, at: at, acc: acc, used: used };
}

function align(own w: Writer): Writer {
  if (w.used > 0) {
    let padding: i32 = 8 - w.used;
    return put(w, 0, padding);
  }
  return w;
}
```

## Describe prefix trees

All 256 literals have eight-bit codes. All 64 distance symbols have six-bit codes.
The command alphabet has 320 nine-bit codes and 384 ten-bit codes. Each tree fills its prefix space exactly.
Code-length symbol 16 repeats a previous length. Its base-four recurrence reduces the command tree to a short header.

```fern
<<Describe prefix trees>>=
function code_length(own w: Writer, length: i32): Writer {
  return put(w, [0, 7, 3, 2, 1, 15][length], [2, 4, 3, 2, 2, 4][length]);
}

function uniform_tree(own w: Writer, length: i32): Writer {
  w = put(w, 0, 2);
  for symbol in [1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15] {
    let bits: i32 = 0;
    if (symbol == length) {
      bits = 1;
    }
    w = code_length(w, bits);
  }
  return w;
}

function repeat_length(own w: Writer, count: i32): Writer {
  let digits: i32[] = [];
  let n: i32 = count;
  while (n > 6) {
    let extra: i32 = (n - 3) % 4;
    digits = digits.append(extra);
    n = (n - extra - 3) / 4 + 2;
  }
  digits = digits.append(n - 3);
  let at: i32 = digits.len() - 1;
  while (at >= 0) {
    w = put(code(w, 3, 2), digits[at], 2);
    at = at - 1;
  }
  return w;
}

function command_tree(own w: Writer): Writer {
  w = put(w, 0, 2);
  let space: i32 = 32;
  for symbol in [1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10] {
    let length: i32 = 0;
    if (symbol == 9) {
      length = 1;
    }
    if (symbol == 10 || symbol == 16) {
      length = 2;
    }
    w = code_length(w, length);
  }
  w = repeat_length(code(w, 0, 1), 319);
  return repeat_length(code(w, 2, 2), 383);
}
```

## Encode commands

An explicit-distance command inserts literals and then copies an earlier byte sequence.
The final command can end after its literals. A repeated distance needs no extra distance bits.

```fern
<<Encode commands>>=
function length_code(length: i32, bases: i32[]): i32 {
  let at: i32 = 0;
  while (at < 23 && bases[at + 1] <= length) {
    at = at + 1;
  }
  return at;
}

function command(own w: Writer, insert: i32, copy: i32): Writer {
  let ic: i32 = length_code(insert, INSERT_BASE);
  let cc: i32 = length_code(copy, COPY_BASE);
  let cell: i32 = [2, 3, 6, 4, 5, 8, 7, 9, 10][ic / 8 * 3 + cc / 8];
  let symbol: i32 = cell * 64 + ic % 8 * 8 + cc % 8;
  if (symbol < 320) {
    w = code(w, symbol, 9);
  } else {
    w = code(w, symbol + 320, 10);
  }
  w = put(w, insert - INSERT_BASE[ic], INSERT_EXTRA[ic]);
  return put(w, copy - COPY_BASE[cc], COPY_EXTRA[cc]);
}

function distance(own w: Writer, value: i32, last: Option[i32]): Writer {
  match (last) {
    Some(previous) => {
      if (value == previous) {
        return code(w, 0, 6);
      }
    },
    None => {}
  }
  let n: i32 = 1;
  while (1 << n + 2 <= value + 3) {
    n = n + 1;
  }
  let high: i32 = (value + 3 >> n) - 2;
  w = code(w, 16 + 2 * (n - 1) + high, 6);
  return put(w, value + 3 - (2 + high << n), n);
}
```

## Find matches

A four-byte hash selects a recent position. A match must contain at least five bytes.
The encoder never refers beyond the 65520-byte window allowed by a sixteen-bit Brotli window.
Each meta-block starts a fresh match table. Earlier blocks remain valid decoder history.

```fern
<<Find matches>>=
function hash(input: [u8], at: i32): i32 {
  let word: u32 = input[at] as u32 | input[at + 1] as u32 << 8 | input[at + 2] as u32 << 16 | input[at + 3] as u32 << 24;
  return (word * (0x1e35a7bd as u32) >> 17) as i32;
}

function block(own w: Writer, input: u8[], start: i32, end: i32, last_block: boolean): Writer {
  let last_flag: i32 = 0;
  if (last_block) {
    last_flag = 1;
  }
  w = put(w, last_flag, 1);
  if (last_block) {
    w = put(w, 0, 1);
  }
  w = put(w, 0, 2);
  w = put(w, end - start - 1, 16);
  if (!last_block) {
    w = put(w, 0, 1);
  }
  w = put(w, 0, 3);
  w = put(w, 0, 6);
  w = put(w, 0, 2);
  w = put(w, 0, 2);
  w = uniform_tree(w, 8);
  w = command_tree(w);
  w = uniform_tree(w, 6);
  let heads: i32[] = __alloc_i32(32768);
  let at: i32 = start;
  let anchor: i32 = start;
  let last: Option[i32] = None;
  while (at + 5 <= end) {
    let slot: i32 = hash(input, at);
    let previous: i32 = heads[slot] - 1;
    heads = heads.with(slot, at + 1);
    let length: i32 = 0;
    if (previous >= start && at - previous <= 65520) {
      length = __mismatch_bytes(input, previous, input, at, end - at);
    }
    if (length >= 5) {
      w = command(w, at - anchor, length);
      w = literals(w, input, anchor, at);
      w = distance(w, at - previous, last);
      last = Some(at - previous);
      at = at + length;
      anchor = at;
    } else {
      at = at + 1;
    }
  }
  if (anchor < end) {
    w = command(w, end - anchor, 2);
    w = literals(w, input, anchor, end);
  }
  return w;
}
```

## Encode a stream

The encoder splits large inputs into 64 KiB meta-blocks. A zero-length stream has one final empty block.

```fern
<<Encode a stream>>=
pub function compress(input: u8[]): u8[] {
  let w: Writer = Writer { bytes: __alloc_u8(input.len() * 2 + 1024), at: 0, acc: 0, used: 0 };
  w = put(w, 0, 1);
  if (input.len() == 0) {
    w = put(w, 3, 2);
  }
  let start: i32 = 0;
  while (start < input.len()) {
    let end: i32 = start + 65536;
    if (end > input.len()) {
      end = input.len();
    }
    w = block(w, input, start, end, end == input.len());
    start = end;
  }
  w = align(w);
  let encoded: u8[] = w.bytes.take(w.at);
  let raw_size: i32 = input.len() + (input.len() + 65535) / 65536 * 3 + 1;
  if (encoded.len() > raw_size) {
    return stored(input);
  }
  return encoded;
}
```

## Stored blocks

Random bytes can expand under a compressed prefix. A stored stream bounds that expansion to block headers.

```fern
<<Stored blocks>>=
function stored(input: [u8]): u8[] {
  let w: Writer = Writer { bytes: __alloc_u8(input.len() + (input.len() + 65535) / 65536 * 4 + 16), at: 0, acc: 0, used: 0 };
  w = put(w, 0, 1);
  let start: i32 = 0;
  while (start < input.len()) {
    let end: i32 = start + 65536;
    if (end > input.len()) {
      end = input.len();
    }
    w = put(w, 0, 3);
    w = put(w, end - start - 1, 16);
    w = align(put(w, 1, 1));
    while (start < end) {
      w = put(w, input[start] as i32, 8);
      start = start + 1;
    }
  }
  w = align(put(w, 3, 2));
  return w.bytes.take(w.at);
}
```
