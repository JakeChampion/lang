# LZ4 frames

This module decompresses the frames of LZ4, as the `lz4` tool writes them.

## Model

A file is one or more frames. A frame of LZ4 starts with the magic number 0x184D2204, a descriptor, and blocks. Each block is compressed with the LZ4 block format, or stored. A block can copy bytes from the blocks before it. The module also reads the legacy frames of `lz4 -l` and skips the skippable frames.

## Contracts

`decode` gives the bytes before the first error, and the error. `decompress` gives the bytes, or the error. The module does not check the checksums of the frames.

## Executable example

```fern test name=std-lz4-example
import "std/lz4";

function main(): i32 {
  // A frame with one compressed block: the literal "ab" and a copy of
  // "ab" four more times.
  let frame = [4u8, 34u8, 77u8, 24u8, 96u8, 64u8, 130u8, 5u8, 0u8, 0u8, 0u8, 36u8, 97u8, 98u8, 2u8, 0u8, 0u8, 0u8, 0u8, 0u8];
  let Ok(out) = lz4.decompress(frame) else { return 1; };
  if (out.len() != 10 || out[9] != 98u8) { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module decompresses the frames of LZ4, as the `lz4` tool writes them.

<<errors>>

<<frames>>

<<blocks>>
```

### Errors

```fern
<<errors>>=
// The errors of LZ4 data: an unknown magic number, a descriptor that is not
// valid, a block that is not valid, or data that ends early.
pub enum Lz4Error { NotLz4, BadFrame, BadBlock, Truncated }

pub function (e: Lz4Error) message(): string {
  return match (e) {
    NotLz4 => "unrecognized header : file cannot be decoded",
    BadFrame => "frame descriptor is not valid",
    BadBlock => "corrupted block detected",
    Truncated => "unexpected end of input",
  };
}

// The bytes before an error, and the error.
pub struct Decoded { out: u8[], error: Option[Lz4Error] }
```

### Frames

```fern
<<frames>>=
// decompress gives the bytes of LZ4 frames, or the error.
pub function decompress(input: [u8]): Result[u8[], Lz4Error] {
  let d = decode(input);
  return match (d.error) {
    Some(e) => Err(e),
    None => Ok(d.out),
  };
}

function __lz4_le(b: [u8], at: i32): i64 {
  return b[at] as i64 | b[at + 1] as i64 << 8i64 | b[at + 2] as i64 << 16i64 | b[at + 3] as i64 << 24i64;
}

// decode gives the bytes of the frames of `input` before the first error,
// and the error.
pub function decode(input: [u8]): Decoded {
  let out: u8[] = [];
  let at = 0;
  while (at < input.len()) {
    if (at + 4 > input.len()) {
      return Decoded { out: out, error: Some(Truncated) };
    }
    let magic = __lz4_le(input, at);
    let r = if (magic == 407708164i64) {
      __lz4_frame(input, at + 4, out)
    } else if (magic == 407642370i64) {
      __lz4_legacy(input, at + 4, out)
    } else if ((magic & 4294967280i64) == 407710288i64) {
      __lz4_skip(input, at + 4, out)
    } else {
      __Lz4Run { out: out, at: -1, error: if (at == 0) { NotLz4 } else { BadFrame } }
    };
    out = r.out;
    if (r.at < 0) {
      return Decoded { out: out, error: Some(r.error) };
    }
    at = r.at;
  }
  if (input.len() == 0) {
    return Decoded { out: out, error: Some(NotLz4) };
  }
  return Decoded { out: out, error: None };
}

// The output after a frame, the offset after the frame or -1, and the
// error for -1.
struct __Lz4Run { out: u8[], at: i32, error: Lz4Error }

function __lz4_skip(input: [u8], at: i32, own out: u8[]): __Lz4Run {
  if (at + 4 > input.len()) {
    return __Lz4Run { out: out, at: -1, error: Truncated };
  }
  let end = at as i64 + 4i64 + __lz4_le(input, at);
  if (end > input.len() as i64) {
    return __Lz4Run { out: out, at: -1, error: Truncated };
  }
  return __Lz4Run { out: out, at: end as i32, error: Truncated };
}

// A frame from its descriptor at `at`.
function __lz4_frame(input: [u8], at: i32, own out: u8[]): __Lz4Run {
  if (at + 3 > input.len()) {
    return __Lz4Run { out: out, at: -1, error: Truncated };
  }
  let flg = input[at] as i32;
  if (flg >> 6 != 1 || (flg & 2) != 0) {
    return __Lz4Run { out: out, at: -1, error: BadFrame };
  }
  let pos = at + 3 + (flg >> 3 & 1) * 8 + (flg & 1) * 4;
  while (true) {
    if (pos + 4 > input.len()) {
      return __Lz4Run { out: out, at: -1, error: Truncated };
    }
    let size = __lz4_le(input, pos);
    if (size == 0i64) {
      break;
    }
    let r = __lz4_data(input, pos + 4, size, out);
    out = r.out;
    if (r.at < 0) {
      return r;
    }
    pos = r.at + (flg >> 4 & 1) * 4;
  }
  pos += 4 + (flg >> 2 & 1) * 4;
  return __Lz4Run { out: out, at: if (pos > input.len()) { -1 } else { pos }, error: Truncated };
}

// The data of a block of `size` at `at`: stored when the high bit of the
// size is set, else compressed.
function __lz4_data(input: [u8], at: i32, size: i64, own out: u8[]): __Lz4Run {
  let n = (size & 2147483647i64) as i32;
  if (at + n > input.len()) {
    return __Lz4Run { out: out, at: -1, error: Truncated };
  }
  return if (size >= 2147483648i64) { __lz4_stored(input, at, n, out) } else { __lz4_block(input, at, at + n, out) };
}

// A legacy frame from its first block at `at`: blocks of compressed data,
// each after its size, up to the next magic number or the end.
function __lz4_legacy(input: [u8], at: i32, own out: u8[]): __Lz4Run {
  let pos = at;
  while (pos + 4 <= input.len()) {
    let size = __lz4_le(input, pos);
    if (size == 407642370i64 || size == 407708164i64) {
      break;
    }
    if (pos as i64 + 4i64 + size > input.len() as i64) {
      return __Lz4Run { out: out, at: -1, error: Truncated };
    }
    let r = __lz4_block(input, pos + 4, pos + 4 + size as i32, out);
    out = r.out;
    if (r.at < 0) {
      return r;
    }
    pos += 4 + size as i32;
  }
  return __Lz4Run { out: out, at: if (pos + 4 > input.len()) { input.len() } else { pos }, error: Truncated };
}

function __lz4_stored(input: [u8], at: i32, n: i32, own out: u8[]): __Lz4Run {
  for k in 0..n {
    out = out.append(input[at + k]);
  }
  return __Lz4Run { out: out, at: at + n, error: Truncated };
}
```

### Blocks

A sequence of a block is a token, the literals, and a copy. The high four bits of the token count the literals, and the low four bits count the bytes of the copy less four. A count of 15 goes on in the bytes after it. A copy reads two bytes of distance back into the output. The last sequence has no copy.

```fern
<<blocks>>=
// The compressed block from `at` to `end`.
function __lz4_block(input: [u8], at: i32, end: i32, own out: u8[]): __Lz4Run {
  let pos = at;
  while (pos < end) {
    let token = input[pos] as i32;
    let (lits, p) = __lz4_count(input, pos + 1, end, token >> 4);
    if (p < 0 || p + lits > end) {
      return __Lz4Run { out: out, at: -1, error: BadBlock };
    }
    for k in 0..lits {
      out = out.append(input[p + k]);
    }
    pos = p + lits;
    if (pos == end) {
      break;
    }
    let r = __lz4_copy(input, pos, end, token, out);
    out = r.out;
    if (r.at < 0) {
      return r;
    }
    pos = r.at;
  }
  return __Lz4Run { out: out, at: end, error: BadBlock };
}

// The copy of a sequence from its distance at `pos`.
function __lz4_copy(input: [u8], pos: i32, end: i32, token: i32, own out: u8[]): __Lz4Run {
  if (pos + 2 > end) {
    return __Lz4Run { out: out, at: -1, error: BadBlock };
  }
  let dist = input[pos] as i32 | input[pos + 1] as i32 << 8;
  let (len, q) = __lz4_count(input, pos + 2, end, token & 15);
  if (q < 0 || dist == 0 || dist > out.len()) {
    return __Lz4Run { out: out, at: -1, error: BadBlock };
  }
  let from = out.len() - dist;
  for k in 0..len + 4 {
    out = out.append(out[from + k]);
  }
  return __Lz4Run { out: out, at: q, error: BadBlock };
}

// A count that starts with the four bits `n`: 15 goes on with each byte
// after it, up to a byte below 255. It gives the count and the offset after
// it, or -1.
function __lz4_count(input: [u8], at: i32, end: i32, n: i32): (i32, i32) {
  if (n < 15) {
    return (n, at);
  }
  let count = n;
  let pos = at;
  while (true) {
    if (pos >= end) {
      return (0, -1);
    }
    let b = input[pos] as i32;
    count += b;
    pos += 1;
    if (b != 255) {
      return (count, pos);
    }
  }
  return (0, -1);
}
```
