# bzip2 streams

This module decompresses the streams of bzip2.

## Model

A stream starts with `BZh` and the size of its blocks, from 1 to 9 hundred thousand bytes. Each block has a CRC, the start of the Burrows-Wheeler transform, the bytes that the block uses, and the Huffman tables. The decoder reads the symbols of the move-to-front coding with runs, undoes the transform, and expands the runs of four equal bytes. A file can hold more than one stream.

## Contracts

`decode` gives the bytes of the blocks before the first error, and the error, as the `bzip2` tool writes them. The decoder checks the CRC of each block. It does not decode the randomised blocks of bzip2 0.9.0, and it reports them as an error of the data. `decompress` gives the bytes, or the error.

## Executable example

```fern test name=std-bzip2-example
import "std/bzip2";

function main(): i32 {
  let Err(e) = bzip2.decompress([66u8, 90u8]) else { return 1; };
  if (e != bzip2.Bzip2Error.Truncated) { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<errors>>

<<bits>>

<<blocks>>

<<tables>>
```

### Errors

```fern
<<errors>>=
import "core/cmp";

// The errors of bzip2 data: not a stream of bzip2, data that ends early, or
// data that is not valid.
@derive(cmp.Eq)
pub enum Bzip2Error { NotBzip2, Truncated, Corrupt }

pub function (e: Bzip2Error) message(): string {
  return match (e) {
    NotBzip2 => "not a bzip2 file",
    Truncated => "compressed file ends unexpectedly",
    Corrupt => "data integrity error when decompressing",
  };
}

// The bytes before an error, and the error.
pub struct Decoded { out: u8[], error: Option[Bzip2Error] }
```

### Bits

The bits of a stream come from the high bit of each byte first. A read past the end gives -1.

```fern
<<bits>>=
// decompress gives the bytes of bzip2 streams, or the error.
pub function decompress(input: [u8]): Result[u8[], Bzip2Error] {
  let d = decode(input);
  return match (d.error) {
    Some(e) => Err(e),
    None => Ok(d.out),
  };
}

// The value of `n` bits at the bit `pos`, or -1 past the end.
function __bz_bits(input: [u8], pos: i64, n: i32): i32 {
  if (pos + n as i64 > input.len() as i64 * 8i64) {
    return -1;
  }
  let v = 0;
  for k in 0..n {
    let p = pos + k as i64;
    v = v << 1 | (input[(p >> 3i64) as i32] >> (7i64 - (p & 7i64)) as u8 & 1u8) as i32;
  }
  return v;
}

// decode gives the bytes of the streams of `input` before the first error,
// and the error.
pub function decode(input: [u8]): Decoded {
  let out: u8[] = [];
  let pos = 0i64;
  let first = true;
  while (pos < input.len() as i64 * 8i64) {
    let start = (pos >> 3i64) as i32;
    if (!__bz_magic(input, start)) {
      let err = if (!first) { None } else if (input.len() < 4 && __bz_prefix(input)) { Some(Truncated) } else { Some(NotBzip2) };
      return Decoded { out: out, error: err };
    }
    let r = __bz_stream(input, (start as i64 + 4i64) * 8i64, (input[start + 3] as i32 - 48) * 100000, out);
    out = r.out;
    if (r.pos < 0i64) {
      return Decoded { out: out, error: Some(r.error) };
    }
    pos = (r.pos + 7i64) / 8i64 * 8i64;
    first = false;
  }
  return Decoded { out: out, error: if (first) { Some(NotBzip2) } else { None } };
}

// Whether `BZh` and a size from 1 to 9 start at `at`.
function __bz_magic(input: [u8], at: i32): boolean {
  return at + 4 <= input.len() && input[at] == 66u8 && input[at + 1] == 90u8 && input[at + 2] == 104u8 && input[at + 3] >= 49u8 && input[at + 3] <= 57u8;
}

// Whether the input is the start of `BZh`.
function __bz_prefix(input: [u8]): boolean {
  let magic = [66u8, 90u8, 104u8];
  for k in 0..input.len() {
    if (k < 3 && input[k] != magic[k]) {
      return false;
    }
  }
  return true;
}
```

### Blocks

```fern
<<blocks>>=
// The output after a stream or a block, the bit after it or -1, and the
// error for -1.
struct __BzRun { out: u8[], pos: i64, error: Bzip2Error }

// The blocks of a stream from the bit `pos`, up to its end mark.
function __bz_stream(input: [u8], pos0: i64, size: i32, own out: u8[]): __BzRun {
  let pos = pos0;
  while (true) {
    let hi = __bz_bits(input, pos, 24);
    let lo = __bz_bits(input, pos + 24i64, 24);
    if (hi < 0 || lo < 0) {
      return __BzRun { out: out, pos: -1i64, error: Truncated };
    }
    if (hi == 1536581 && lo == 3690640) {
      return __BzRun { out: out, pos: if (pos + 80i64 > input.len() as i64 * 8i64) { -1i64 } else { pos + 80i64 }, error: Truncated };
    }
    if (hi != 3227993 || lo != 2511705) {
      return __BzRun { out: out, pos: -1i64, error: Corrupt };
    }
    let r = __bz_block(input, pos + 48i64, size, out);
    out = r.out;
    if (r.pos < 0i64) {
      return r;
    }
    pos = r.pos;
  }
  return __BzRun { out: out, pos: -1i64, error: Corrupt };
}

// The header of a block after its magic number: its CRC, the start of the
// transform, the bytes it uses, the Huffman tables and the selectors.
struct __BzHead { crc: i64, orig: i32, used: u8[], lens: i32[][], selectors: i32[], pos: i64 }

// One block from the bit `pos` after its magic number.
function __bz_block(input: [u8], pos: i64, size: i32, own out: u8[]): __BzRun {
  let (h, error) = __bz_head(input, pos);
  if (h.pos < 0i64) {
    return __BzRun { out: out, pos: -1i64, error: error };
  }
  let (tt, n, end, err2) = __bz_symbols(input, h, size);
  if (end < 0i64) {
    return __BzRun { out: out, pos: -1i64, error: err2 };
  }
  if (h.orig >= n && n > 0) {
    return __BzRun { out: out, pos: -1i64, error: Corrupt };
  }
  let before = out.len();
  out = __bz_untransform(tt, n, h.orig, out);
  if (__bz_crc(out, before) != h.crc) {
    return __BzRun { out: out.truncate(before), pos: -1i64, error: Corrupt };
  }
  return __BzRun { out: out, pos: end, error: Corrupt };
}

// The header of a block, or a header with `pos` -1 and the error.
function __bz_head(input: [u8], pos0: i64): (__BzHead, Bzip2Error) {
  let none: i32[] = [];
  let bad = __BzHead { crc: 0i64, orig: 0, used: [], lens: [], selectors: none, pos: -1i64 };
  let crc_hi = __bz_bits(input, pos0, 16);
  let crc_lo = __bz_bits(input, pos0 + 16i64, 16);
  let random = __bz_bits(input, pos0 + 32i64, 1);
  let orig = __bz_bits(input, pos0 + 33i64, 24);
  let map = __bz_bits(input, pos0 + 57i64, 16);
  if (map < 0) {
    return (bad, Truncated);
  }
  if (random != 0) {
    return (bad, Corrupt);
  }
  let pos = pos0 + 73i64;
  let used: u8[] = [];
  for i in 0..16 {
    if ((map >> 15 - i & 1) == 1) {
      let bits = __bz_bits(input, pos, 16);
      if (bits < 0) {
        return (bad, Truncated);
      }
      pos += 16i64;
      for j in 0..16 {
        if ((bits >> 15 - j & 1) == 1) {
          used = used.append((i * 16 + j) as u8);
        }
      }
    }
  }
  if (used.len() == 0) {
    return (bad, Corrupt);
  }
  let t = __bz_tables(input, pos, used.len() + 2);
  if (t.pos < 0i64) {
    return (bad, t.error);
  }
  return (__BzHead { crc: crc_hi as i64 << 16i64 | crc_lo as i64, orig: orig, used: used, lens: t.lens, selectors: t.selectors, pos: t.pos }, Corrupt);
}
```

### Tables

The selectors say which of two to six Huffman tables codes each group of 50 symbols. They come in move-to-front coding, each as a run of 1 bits. The lengths of the codes of a table come as a delta from the length before.

```fern
<<tables>>=
struct __BzTables { lens: i32[][], selectors: i32[], pos: i64, error: Bzip2Error }

function __bz_tables(input: [u8], pos0: i64, alpha: i32): __BzTables {
  let none: i32[] = [];
  let bad = __BzTables { lens: [], selectors: none, pos: -1i64, error: Truncated };
  let groups = __bz_bits(input, pos0, 3);
  let count = __bz_bits(input, pos0 + 3i64, 15);
  if (count < 0) {
    return bad;
  }
  if (groups < 2 || groups > 6 || count < 1) {
    return __BzTables { ...bad, error: Corrupt };
  }
  let (selectors, p0, error) = __bz_selectors(input, pos0 + 18i64, groups, count);
  if (p0 < 0i64) {
    return __BzTables { ...bad, error: error };
  }
  let pos = p0;
  let lens: i32[][] = [];
  for _ in 0..groups {
    let (row, next) = __bz_lengths(input, pos, alpha);
    if (next < 0i64) {
      return __BzTables { ...bad, error: if (next == -1i64) { Truncated } else { Corrupt } };
    }
    lens = lens.append(row);
    pos = next;
  }
  return __BzTables { lens: lens, selectors: selectors, pos: pos, error: Truncated };
}

// The selectors, each a run of 1 bits in move-to-front coding, and the bit
// after them, or -1 and the error.
function __bz_selectors(input: [u8], pos0: i64, groups: i32, count: i32): (i32[], i64, Bzip2Error) {
  let mtf = [0, 1, 2, 3, 4, 5];
  let selectors = __alloc_i32(count);
  let pos = pos0;
  for s in 0..count {
    let j = 0;
    let b = __bz_bits(input, pos, 1);
    while (b == 1 && j < groups) {
      j += 1;
      pos += 1i64;
      b = __bz_bits(input, pos, 1);
    }
    if (b != 0) {
      return (selectors, -1i64, if (b < 0) { Truncated } else { Corrupt });
    }
    pos += 1i64;
    let v = mtf[j];
    let k = j;
    while (k > 0) {
      mtf = mtf.with(k, mtf[k - 1]);
      k -= 1;
    }
    mtf = mtf.with(0, v);
    selectors = selectors.with(s, v);
  }
  return (selectors, pos, Corrupt);
}

// The lengths of the codes of one table, and the bit after them, or -1 at
// the end and -2 for a length that is not valid.
function __bz_lengths(input: [u8], pos0: i64, alpha: i32): (i32[], i64) {
  let row = __alloc_i32(alpha);
  let cur = __bz_bits(input, pos0, 5);
  let pos = pos0 + 5i64;
  for i in 0..alpha {
    while (true) {
      if (cur < 1 || cur > 20) {
        return (row, -2i64);
      }
      let b = __bz_bits(input, pos, 1);
      if (b < 0) {
        return (row, -1i64);
      }
      if (b == 0) {
        pos += 1i64;
        break;
      }
      let d = __bz_bits(input, pos + 1i64, 1);
      if (d < 0) {
        return (row, -1i64);
      }
      cur += if (d == 0) { 1 } else { -1 };
      pos += 2i64;
    }
    row = row.with(i, cur);
  }
  return (row, pos);
}

// A canonical Huffman code: for each length the largest code, the first
// code, and the offset of its first symbol in `perm`.
struct __BzHuff { min: i32, max: i32, limit: i32[], base: i32[], start: i32[], perm: i32[] }

function __bz_huffman(lens: i32[]): __BzHuff {
  let min = 32;
  let max = 0;
  for l in lens {
    min = if (l < min) { l } else { min };
    max = if (l > max) { l } else { max };
  }
  let perm: i32[] = [];
  let limit = __alloc_i32(22);
  let base = __alloc_i32(22);
  let start = __alloc_i32(22);
  let code = 0;
  for len in min..max + 1 {
    start = start.with(len, perm.len());
    base = base.with(len, code);
    for s in 0..lens.len() {
      if (lens[s] == len) {
        perm = perm.append(s);
        code += 1;
      }
    }
    limit = limit.with(len, code - 1);
    code <<= 1;
  }
  return __BzHuff { min: min, max: max, limit: limit, base: base, start: start, perm: perm };
}

// The next symbol at the bit `pos`, and the bit after it, or -1 at the end
// and -2 for a code that is not valid.
function __bz_symbol(input: [u8], h: __BzHuff, pos: i64): (i32, i64) {
  let v = __bz_bits(input, pos, h.min);
  if (v < 0) {
    return (0, -1i64);
  }
  let len = h.min;
  while (v > h.limit[len]) {
    len += 1;
    if (len > h.max) {
      return (0, -2i64);
    }
    let b = __bz_bits(input, pos + len as i64 - 1i64, 1);
    if (b < 0) {
      return (0, -1i64);
    }
    v = v << 1 | b;
  }
  return (h.perm[h.start[len] + v - h.base[len]], pos + len as i64);
}

// The bytes of a block before the transform is undone, as the low byte of
// each word of `tt`, their count, and the bit after the block, or -1 and
// the error. A run of RUNA and RUNB gives a count in base two.
function __bz_symbols(input: [u8], h: __BzHead, size: i32): (i32[], i32, i64, Bzip2Error) {
  let huffs: __BzHuff[] = [];
  for lens in h.lens {
    huffs = huffs.append(__bz_huffman(lens));
  }
  let eob = h.used.len() + 1;
  let tt = __alloc_i32(size);
  let n = 0;
  let mtf = h.used;
  let run = 0;
  let weight = 1;
  let pos = h.pos;
  let k = 0;
  while (k / 50 < h.selectors.len() && run <= size) {
    let (sym, next) = __bz_symbol(input, huffs[h.selectors[k / 50]], pos);
    if (next < 0i64) {
      return (tt, 0, -1i64, if (next == -1i64) { Truncated } else { Corrupt });
    }
    pos = next;
    k += 1;
    if (sym <= 1) {
      run += weight << sym;
      weight <<= 1;
      continue;
    }
    if (n + run + (if (sym == eob) { 0 } else { 1 }) > size) {
      return (tt, 0, -1i64, Corrupt);
    }
    tt = __bz_fill(tt, n, run, mtf[0] as i32);
    n += run;
    run = 0;
    weight = 1;
    if (sym == eob) {
      return (tt, n, pos, Corrupt);
    }
    let v = mtf[sym - 1];
    mtf = __bz_front(mtf, sym - 1);
    tt = tt.with(n, v as i32);
    n += 1;
  }
  return (tt, 0, -1i64, Corrupt);
}

// The words of `tt` from `n` with `count` copies of `v`.
function __bz_fill(own tt: i32[], n: i32, count: i32, v: i32): i32[] {
  for k in 0..count {
    tt = tt.with(n + k, v);
  }
  return tt;
}

// The list with its entry at `j` moved to the front.
function __bz_front(own mtf: u8[], j: i32): u8[] {
  let v = mtf[j];
  let k = j;
  while (k > 0) {
    mtf = mtf.with(k, mtf[k - 1]);
    k -= 1;
  }
  return mtf.with(0, v);
}

// Undoes the transform and the runs of four equal bytes, and appends the
// bytes of the block.
function __bz_untransform(tt0: i32[], n: i32, orig: i32, own out: u8[]): u8[] {
  if (n == 0) {
    return out;
  }
  let tt = tt0;
  let count = __alloc_i32(256);
  for i in 0..n {
    let b = tt[i] & 255;
    count = count.with(b, count[b] + 1);
  }
  let sum = 0;
  for b in 0..256 {
    let c = count[b];
    count = count.with(b, sum);
    sum += c;
  }
  for i in 0..n {
    let b = tt[i] & 255;
    let at = count[b];
    tt = tt.with(at, tt[at] | i << 8);
    count = count.with(b, at + 1);
  }
  let p = tt[orig] >> 8;
  let last = -1;
  let same = 0;
  let k = 0;
  while (k < n) {
    let b = tt[p] & 255;
    p = tt[p] >> 8;
    k += 1;
    if (same == 4) {
      for _ in 0..b {
        out = out.append(last as u8);
      }
      same = 0;
      last = -1;
      continue;
    }
    same = if (b == last) { same + 1 } else { 1 };
    last = b;
    out = out.append(b as u8);
  }
  return out;
}

// The CRC of bzip2, which is CRC-32 with the high bit first, of the bytes
// of `out` from `from`.
function __bz_crc(out: u8[], from: i32): i64 {
  let table = __alloc_i64(256);
  for i in 0..256 {
    let c = i as i64 << 24i64;
    for _ in 0..8 {
      c = if ((c & 2147483648i64) != 0i64) { (c << 1i64 ^ 79764919i64) & 4294967295i64 } else { c << 1i64 & 4294967295i64 };
    }
    table = table.with(i, c);
  }
  let crc = 4294967295i64;
  for i in from..out.len() {
    crc = crc << 8i64 & 4294967295i64 ^ table[(crc >> 24i64 ^ out[i] as i64) as i32 & 255];
  }
  return crc ^ 4294967295i64;
}
```
