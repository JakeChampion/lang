# The LZW data of Unix compress

This module decompresses the `.Z` files of Unix `compress`.

## Model

A file starts with the bytes 0x1F and 0x9D, and a byte of flags: the largest width of a code, from 9 to 16 bits, and the block mode. The codes start with 9 bits, and the width grows by one bit when the table is full. In the block mode, code 256 clears the table. At each change of the width and at each clear, the codes go on at the next group of eight codes, as `compress` writes them.

## Contracts

`decode` gives the bytes before the first error, and the error, as `gzip -d` does for a `.Z` file. `decompress` gives the bytes, or the error.

## Executable example

```fern test name=std-lzw-example
import "std/lzw";

function main(): i32 {
  // "aaaa" as compress writes it.
  let Ok(out) = lzw.decompress([31u8, 157u8, 144u8, 97u8, 2u8, 134u8, 1u8]) else { return 1; };
  if (out.len() != 4 || out[3] != 97u8) { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module decompresses the `.Z` files of Unix `compress`.

<<errors>>

<<decode>>
```

### Errors

```fern
<<errors>>=
// The errors of a `.Z` file: not a file of compress, a width that is not
// valid, a code that is not in the table, or a file that ends early.
pub enum LzwError { NotLzw, BadWidth, BadCode, Truncated }

pub function (e: LzwError) message(): string {
  return match (e) {
    NotLzw => "not in compressed format",
    BadWidth => "compressed with more than 16 bits",
    BadCode => "corrupt input",
    Truncated => "unexpected end of file",
  };
}

// The bytes before an error, and the error.
pub struct Decoded { out: u8[], error: Option[LzwError] }
```

### Decode

`prefix` and `suffix` hold the table: each code above 255 is the string of its prefix code and its suffix byte. A string is written from its end to its start, so `__lzw_string` collects it on a stack first.

```fern
<<decode>>=
// decompress gives the bytes of a `.Z` file, or its error.
pub function decompress(input: [u8]): Result[u8[], LzwError] {
  let d = decode(input);
  return match (d.error) {
    Some(e) => Err(e),
    None => Ok(d.out),
  };
}

// The state of a decode: the table, the width of a code and its largest
// code, the next free code, the bit of the next code, and the bit where the
// groups of codes start.
struct __Lzw { prefix: i32[], suffix: u8[], bits: i32, max: i32, free: i32, pos: i64, base: i64 }

// decode gives the bytes of a `.Z` file before the first error, and the
// error.
pub function decode(input: [u8]): Decoded {
  let out: u8[] = [];
  if let Some(e) = __lzw_header(input) {
    return Decoded { out: out, error: Some(e) };
  }
  let maxbits = input[2] as i32 & 31;
  let block = (input[2] & 128u8) != 0u8;
  let s = __Lzw { prefix: __alloc_i32(65536), suffix: __alloc_u8(65536), bits: 9, max: 511, free: if (block) { 257 } else { 256 }, pos: 24i64, base: 24i64 };
  let total = input.len() as i64 * 8i64;
  let old = -1;
  let fin = 0;
  while (true) {
    s = if (s.free > s.max) { __lzw_widen(s, maxbits) } else { s };
    if (s.pos + s.bits as i64 > total) {
      return Decoded { out: out, error: None };
    }
    let code = __lzw_code(input, s.pos, s.bits);
    s = __Lzw { ...s, pos: s.pos + s.bits as i64 };
    if (block && __lzw_clear(code, old)) {
      s = __Lzw { ...__lzw_align(s), bits: 9, max: 511, free: 256 };
      continue;
    }
    if (!__lzw_known(code, s.free, old)) {
      return Decoded { out: out, error: Some(BadCode) };
    }
    let kwk = code == s.free;
    let (next, first) = __lzw_string(s, out, if (kwk) { old } else { code }, kwk, fin);
    out = next;
    fin = first;
    s = __lzw_add(s, old, fin, 1 << maxbits);
    old = code;
  }
  return Decoded { out: out, error: None };
}

// Whether a code of the block mode clears the table: 256 after the first
// code.
function __lzw_clear(code: i32, old: i32): boolean {
  return code == 256 && old >= 0;
}

// Whether the table has the code, or the code is the next free one after a
// first code.
function __lzw_known(code: i32, free: i32, old: i32): boolean {
  return code <= free && (old >= 0 || code < 256);
}

// The error of the header of a `.Z` file, or None.
function __lzw_header(input: [u8]): Option[LzwError] {
  if (input.len() < 3 || input[0] != 31u8 || input[1] != 157u8) {
    return Some(NotLzw);
  }
  return if ((input[2] & 31u8) > 16u8) { Some(BadWidth) } else { None };
}

// The table with the string of `old` and the byte `fin` at the next free
// code, while the table has room under `limit`.
function __lzw_add(s: __Lzw, old: i32, fin: i32, limit: i32): __Lzw {
  if (old < 0 || s.free >= limit) {
    return s;
  }
  return __Lzw { ...s, prefix: s.prefix.with(s.free, old), suffix: s.suffix.with(s.free, fin as u8), free: s.free + 1 };
}

// The state after the width grows by one bit.
function __lzw_widen(s0: __Lzw, maxbits: i32): __Lzw {
  let s = __lzw_align(s0);
  let bits = s.bits + 1;
  return __Lzw { ...s, bits: bits, max: if (bits == maxbits) { 1 << maxbits } else { (1 << bits) - 1 } };
}

// The state with the next code at the start of the next group of eight
// codes, as compress writes them after a change of the width.
function __lzw_align(s: __Lzw): __Lzw {
  let group = s.bits as i64 * 8i64;
  let rel = s.pos - s.base - 1i64;
  let pos = s.base + rel + (group - (rel + group) % group);
  return __Lzw { ...s, pos: pos, base: pos };
}

// The code of `bits` bits at the bit `pos`, low bits first.
function __lzw_code(input: [u8], pos: i64, bits: i32): i32 {
  let at = (pos >> 3i64) as i32;
  let v = 0;
  for k in 0..3 {
    if (at + k < input.len()) {
      v |= input[at + k] as i32 << 8 * k;
    }
  }
  return v >> (pos & 7i64) as i32 & (1 << bits) - 1;
}

// Appends the string of `code`, with the byte `fin` after it when `kwk`.
// It gives the output and the first byte of the string.
function __lzw_string(s: __Lzw, own out: u8[], code0: i32, kwk: boolean, fin: i32): (u8[], i32) {
  let stack: u8[] = [];
  if (kwk) {
    stack = stack.append(fin as u8);
  }
  let code = code0;
  while (code >= 256) {
    stack = stack.append(s.suffix[code]);
    code = s.prefix[code];
  }
  out = out.append(code as u8);
  let k = stack.len() - 1;
  while (k >= 0) {
    out = out.append(stack[k]);
    k -= 1;
  }
  return (out, code);
}
```
