# Zstandard data without dictionaries

This module encodes and decodes Zstandard data without dictionaries.

## Model

The decoder reads Zstandard frames and reconstructs literals and sequence references. Compression produces a frame without an external dictionary.

## Contracts

The output cap bounds decompressed bytes. Errors distinguish truncated input, malformed data, excessive size, checksum failure, and unsupported features.

## Executable example

```fern test name=std-zstd-example
import "std/zstd";
import "std/string";

function main(): i32 {
  let packed = zstd.compress("hello".as_bytes());
  let Ok(plain) = zstd.decompress(packed[:], 5) else { return 1; };
  if (plain.len() != 5 || plain[0] != 104 as u8) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub enum ZstdError>>

<<3 pub function (e: ZstdError) to_string(): string>>

<<4 impl error.Error for ZstdError>>

<<5 pub function (e: ZstdError) message(): string>>

<<6 function __le(buf: [u8], at: i32, n: i32): i64>>

<<7 struct __Frame>>

<<8 function __frame(input: [u8], at: i32, max_out: i32): Result[__Frame, ZstdError]>>

<<9 struct __Tables>>

<<10 function __tables_none(): __Tables>>

<<11 struct __Block>>

<<12 function __block(input: [u8], at: i32, kind: i32, size: i32, out: u8[], z: __Tables, max_out: i32): Result[...>>

<<13 struct __Fse>>

<<14 function __fse_table(probs: i32[], log: i32): __Fse>>

<<15 struct __Nc>>

<<16 function __read_fse(buf: [u8], at: i32, end: i32, max_sym: i32, max_log: i32): Result[(__Fse, i32), ZstdError]>>

<<17 struct __Huf>>

<<18 function __literals(input: [u8], at: i32, end: i32, prev: __Huf): Result[__Literals, ZstdError]>>

<<19 function __huf_table(weights: i32[]): Result[__Huf, ZstdError]>>

<<20 struct __Seqs>>

<<21 function __ll_default(): i32[]>>

<<22 struct __Field>>

<<23 function __seq_table(input: [u8], at: i32, end: i32, mode: i32, prev: __Fse, f: __Field): Result[(__Fse, i3...>>

<<24 struct __Codes>>

<<25 function __decode_seqs(input: [u8], start: i32, end: i32, n: i32, t: __Tables): Result[__Seqs, ZstdError]>>

<<26 pub function compress(input: [u8]): u8[]>>

<<27 function __window_for(n: i32): i32>>

<<28 struct __Seqb>>

<<29 function __block_seqs(input: [u8], toks: i64[], t: i32, at: i32, end: i32): __Seqb>>

<<30 function __huf_head(n: i32, body: u8[]): Option[u8[]]>>

<<31 function __same(a: i32[], b: i32[]): boolean>>

<<32 struct __Ct>>

<<33 function __ctable(probs: i32[], log: i32): __Ct>>

<<34 function __two_state(own head: u8[], w: i32[], ct: __Ct): u8[]>>

<<35 struct __Choice>>

<<36 function __seq_section(b: __Seqb, reps: i32[]): (u8[], i32[])>>

<<37 struct __Codes3>>

<<38 function __seq_stream(own out: u8[], b: __Seqb, c: __Codes3, t: __Tables3, codes: __Codes): u8[]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module encodes and decodes Zstandard data without dictionaries.
import "std/deflate";
import "std/hash";
import "core/cmp";
import "std/error";
```

### `2 pub enum ZstdError`

```fern
<<2 pub enum ZstdError>>=
pub enum ZstdError { ZstdTruncated, ZstdMalformed(string), ZstdTooLarge, ZstdChecksum, ZstdUnsupported(string) }
```

### `3 pub function (e: ZstdError) to_string(): string`

```fern
<<3 pub function (e: ZstdError) to_string(): string>>=
pub function (e: ZstdError) to_string(): string {
  return e.message();
}
```

### `4 impl error.Error for ZstdError`

```fern
<<4 impl error.Error for ZstdError>>=
impl error.Error for ZstdError {}

impl cmp.Display for ZstdError {}
```

### `5 pub function (e: ZstdError) message(): string`

```fern
<<5 pub function (e: ZstdError) message(): string>>=
pub function (e: ZstdError) message(): string {
  match (e) {
    ZstdTruncated => {
      return "zstd: truncated";
    },
    ZstdMalformed(what) => {
      return "zstd: malformed: " + what;
    },
    ZstdTooLarge => {
      return "zstd: output past the limit";
    },
    ZstdChecksum => {
      return "zstd: checksum mismatch";
    },
    ZstdUnsupported(what) => {
      return "zstd: unsupported: " + what;
    }
  }
  return "zstd";
}

pub function decompress(input: [u8], max_out: i32): Result[u8[], ZstdError] {
  if (input.len() == 0) {
    return Err(ZstdTruncated);
  }
  let out: u8[] = [];
  let at: i32 = 0;
  while (at < input.len()) {
    if (at + 4 > input.len()) {
      return Err(ZstdTruncated);
    }
    let magic: i64 = __le(input, at, 4);
    if ((magic & 0xFFFFFFF0) == 0x184D2A50) {
      at = at + 8 + __le(input, at + 4, 4) as i32;
    } else if (magic == 0xFD2FB528) {
      let f: __Frame = __frame(input, at + 4, max_out - out.len())?;
      out = __copied(out, f.out, 0, f.out.len());
      at = f.end;
    } else {
      return Err(ZstdMalformed("magic number"));
    }
  }
  if (at > input.len()) {
    return Err(ZstdTruncated);
  }
  return Ok(out);
}
```

### `6 function __le(buf: [u8], at: i32, n: i32): i64`

```fern
<<6 function __le(buf: [u8], at: i32, n: i32): i64>>=
function __le(buf: [u8], at: i32, n: i32): i64 {
  let v: i64 = 0;
  let i: i32 = n - 1;
  while (i >= 0) {
    v = v << 8 | buf[at + i] as i64;
    i = i - 1;
  }
  return v;
}

function __highbit(x: i32): i32 {
  let k: i32 = 0 - 1;
  while (x > 0) {
    x = x >> 1;
    k = k + 1;
  }
  return k;
}
```

### `7 struct __Frame`

```fern
<<7 struct __Frame>>=
struct __Frame { out: u8[], end: i32 }

struct __Header { size: i64, checksum: boolean, at: i32 }
```

### `8 function __frame(input: [u8], at: i32, max_out: i32): Result[__Frame, ZstdError]`

```fern
<<8 function __frame(input: [u8], at: i32, max_out: i32): Result[__Frame, ZstdError]>>=
function __frame(input: [u8], at: i32, max_out: i32): Result[__Frame, ZstdError] {
  let h: __Header = __frame_header(input, at)?;
  if (h.size > max_out as i64) {
    return Err(ZstdTooLarge);
  }
  let out: u8[] = [];
  let z: __Tables = __tables_none();
  let pos: i32 = h.at;
  let last: boolean = false;
  while (!last) {
    if (pos + 3 > input.len()) {
      return Err(ZstdTruncated);
    }
    let bh: i32 = __le(input, pos, 3) as i32;
    last = (bh & 1) == 1;
    let size: i32 = bh >> 3;
    let r: __Block = __block(input, pos + 3, bh >> 1 & 3, size, out, z, max_out)?;
    out = r.out;
    z = r.z;
    pos = r.end;
  }
  return __frame_end(input, pos, out, h);
}

function __frame_end(input: [u8], pos: i32, out: u8[], h: __Header): Result[__Frame, ZstdError] {
  if (h.size >= 0 && h.size != out.len() as i64) {
    return Err(ZstdMalformed("content size"));
  }
  if (!h.checksum) {
    return Ok(__Frame { out: out, end: pos });
  }
  if (pos + 4 > input.len()) {
    return Err(ZstdTruncated);
  }
  let sum: u64 = hash.xxh64(out, 0) & 0xFFFFFFFF;
  if (sum != __le(input, pos, 4) as u64) {
    return Err(ZstdChecksum);
  }
  return Ok(__Frame { out: out, end: pos + 4 });
}

function __frame_header(input: [u8], at: i32): Result[__Header, ZstdError] {
  if (at >= input.len()) {
    return Err(ZstdTruncated);
  }
  let d: i32 = input[at] as i32;
  if ((d & 8) != 0) {
    return Err(ZstdMalformed("reserved bit"));
  }
  let single: boolean = (d & 32) != 0;
  let dict: i32 = [0, 1, 2, 4][d & 3];
  let fcs: i32 = __fcs_width(d >> 6, single);
  let pos: i32 = at + 2;
  if (single) {
    pos = at + 1;
  }
  if (pos + dict + fcs > input.len()) {
    return Err(ZstdTruncated);
  }
  if (dict > 0 && __le(input, pos, dict) != 0) {
    return Err(ZstdUnsupported("dictionary"));
  }
  pos = pos + dict;
  let size: i64 = 0 - 1;
  if (fcs > 0) {
    size = __le(input, pos, fcs);
  }
  if (fcs == 2) {
    size = size + 256;
  }
  return Ok(__Header { size: size, checksum: (d & 4) != 0, at: pos + fcs });
}

function __fcs_width(flag: i32, single: boolean): i32 {
  if (flag == 0 && single) {
    return 1;
  }
  return [0, 2, 4, 8][flag];
}
```

### `9 struct __Tables`

```fern
<<9 struct __Tables>>=
struct __Tables { huf: __Huf, ll: __Fse, of: __Fse, ml: __Fse, reps: i32[] }
```

### `10 function __tables_none(): __Tables`

```fern
<<10 function __tables_none(): __Tables>>=
function __tables_none(): __Tables {
  let none: i32[] = [];
  let f: __Fse = __Fse { log: 0 - 1, sym: none, nb: none, base: none };
  return __Tables { huf: __Huf { max: 0 - 1, sym: none, nb: none }, ll: f, of: f, ml: f, reps: [1, 4, 8] };
}
```

### `11 struct __Block`

```fern
<<11 struct __Block>>=
struct __Block { out: u8[], z: __Tables, end: i32 }
```

### `12 function __block(input: [u8], at: i32, kind: i32, size: i32, out: u8[], z: __Tables, max_out: i32): Result[...`

```fern
<<12 function __block(input: [u8], at: i32, kind: i32, size: i32, out: u8[], z: __Tables, max_out: i32): Result[...>>=
function __block(input: [u8], at: i32, kind: i32, size: i32, out: u8[], z: __Tables, max_out: i32): Result[__Block, ZstdError] {
  if (kind == 3) {
    return Err(ZstdMalformed("reserved block type"));
  }
  let span: i32 = size;
  if (kind == 1) {
    span = 1;
  }
  if (size > 131072) {
    return Err(ZstdMalformed("block size"));
  }
  if (at + span > input.len()) {
    return Err(ZstdTruncated);
  }
  if (kind == 2) {
    return __compressed(input, at, at + size, out, z, max_out);
  }
  if (out.len() + size > max_out) {
    return Err(ZstdTooLarge);
  }
  if (kind == 1) {
    return Ok(__Block { out: __repeated(out, input[at], size), z: z, end: at + 1 });
  }
  return Ok(__Block { out: __copied(out, input, at, at + size), z: z, end: at + size });
}

function __repeated(out: u8[], b: u8, n: i32): u8[] {
  let i: i32 = 0;
  while (i < n) {
    out = out.append(b);
    i = i + 1;
  }
  return out;
}

function __copied(out: u8[], src: [u8], from: i32, to: i32): u8[] {
  let i: i32 = from;
  while (i < to) {
    out = out.append(src[i]);
    i = i + 1;
  }
  return out;
}

function __compressed(input: [u8], at: i32, end: i32, out: u8[], z: __Tables, max_out: i32): Result[__Block, ZstdError] {
  let lit: __Literals = __literals(input, at, end, z.huf)?;
  let seq: __Seqs = __sequences(input, lit.end, end, __Tables { ...z, huf: lit.huf })?;
  if (out.len() + lit.bytes.len() + seq.matched > max_out) {
    return Err(ZstdTooLarge);
  }
  let done: u8[] = __execute(out, lit.bytes, seq)?;
  return Ok(__Block { out: done, z: seq.z, end: end });
}

function __fwd(buf: [u8], bit: i32, n: i32): i32 {
  let v: i64 = 0;
  let first: i32 = bit >> 3;
  let i: i32 = (bit + n + 7 >> 3) - 1;
  while (i >= first) {
    let b: i64 = 0;
    if (i < buf.len()) {
      b = buf[i] as i64;
    }
    v = v << 8 | b;
    i = i - 1;
  }
  return (v >> (bit & 7) & (1 as i64 << n) - 1) as i32;
}

function __back(buf: [u8], start: i32, pos: i32, n: i32): i32 {
  if (n == 0) {
    return 0;
  }
  let lo: i32 = pos - n;
  if (lo >= 0) {
    return __fwd(buf, start * 8 + lo, n);
  }
  if (pos <= 0) {
    return 0;
  }
  return __fwd(buf, start * 8, pos) << 0 - lo;
}

function __back_start(buf: [u8], start: i32, end: i32): Result[i32, ZstdError] {
  if (end <= start || buf[end - 1] == 0) {
    return Err(ZstdMalformed("bitstream end"));
  }
  return Ok((end - 1 - start) * 8 + __highbit(buf[end - 1] as i32));
}
```

### `13 struct __Fse`

```fern
<<13 struct __Fse>>=
struct __Fse { log: i32, sym: i32[], nb: i32[], base: i32[] }
```

### `14 function __fse_table(probs: i32[], log: i32): __Fse`

```fern
<<14 function __fse_table(probs: i32[], log: i32): __Fse>>=
function __fse_table(probs: i32[], log: i32): __Fse {
  let next: i32[] = [];
  for p in probs {
    if (p == 0 - 1) {
      next = next.append(1);
    } else {
      next = next.append(p);
    }
  }
  return __fse_states(__symbols(probs, log), next, log);
}

function __symbols(probs: i32[], log: i32): i32[] {
  let size: i32 = 1 << log;
  let sym: i32[] = __zeros(size);
  let high: i32 = size - 1;
  let s: i32 = 0;
  while (s < probs.len()) {
    if (probs[s] == 0 - 1) {
      sym = sym.with(high, s);
      high = high - 1;
    }
    s = s + 1;
  }
  return __spread(sym, probs, size, high);
}

function __spread(own sym: i32[], probs: i32[], size: i32, high: i32): i32[] {
  let step: i32 = (size >> 1) + (size >> 3) + 3;
  let pos: i32 = 0;
  let s: i32 = 0;
  while (s < probs.len()) {
    let k: i32 = 0;
    while (k < probs[s]) {
      sym = sym.with(pos, s);
      pos = pos + step & size - 1;
      while (pos > high) {
        pos = pos + step & size - 1;
      }
      k = k + 1;
    }
    s = s + 1;
  }
  return sym;
}

function __fse_states(sym: i32[], own next: i32[], log: i32): __Fse {
  let size: i32 = 1 << log;
  let nb: i32[] = [];
  let base: i32[] = [];
  let u: i32 = 0;
  while (u < size) {
    let x: i32 = next[sym[u]];
    next = next.with(sym[u], x + 1);
    let bits: i32 = log - __highbit(x);
    nb = nb.append(bits);
    base = base.append((x << bits) - size);
    u = u + 1;
  }
  return __Fse { log: log, sym: sym, nb: nb, base: base };
}

function __zeros(n: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(0);
    i = i + 1;
  }
  return out;
}
```

### `15 struct __Nc`

```fern
<<15 struct __Nc>>=
struct __Nc { probs: i32[], bit: i32, remaining: i32, threshold: i32, nbits: i32, zero: boolean }
```

### `16 function __read_fse(buf: [u8], at: i32, end: i32, max_sym: i32, max_log: i32): Result[(__Fse, i32), ZstdError]`

```fern
<<16 function __read_fse(buf: [u8], at: i32, end: i32, max_sym: i32, max_log: i32): Result[(__Fse, i32), ZstdError]>>=
function __read_fse(buf: [u8], at: i32, end: i32, max_sym: i32, max_log: i32): Result[(__Fse, i32), ZstdError] {
  if (at >= end) {
    return Err(ZstdTruncated);
  }
  let log: i32 = (buf[at] as i32 & 15) + 5;
  if (log > max_log) {
    return Err(ZstdMalformed("FSE accuracy"));
  }
  let none: i32[] = [];
  let c: __Nc = __Nc { probs: none, bit: at * 8 + 4, remaining: (1 << log) + 1, threshold: 1 << log, nbits: log + 1, zero: false };
  while (c.remaining > 1 && c.probs.len() <= max_sym) {
    if (c.zero) {
      c = __nc_zeros(buf, c);
    } else {
      c = __nc_count(buf, c);
    }
  }
  if (c.remaining != 1 || c.probs.len() > max_sym + 1 || c.bit + 7 >> 3 > end) {
    return Err(ZstdMalformed("FSE table"));
  }
  return Ok((__fse_table(c.probs, log), (c.bit + 7 >> 3) - at));
}

function __nc_zeros(buf: [u8], c: __Nc): __Nc {
  let probs: i32[] = c.probs;
  let bit: i32 = c.bit;
  let r: i32 = 3;
  while (r == 3) {
    r = __fwd(buf, bit, 2);
    bit = bit + 2;
    let k: i32 = 0;
    while (k < r) {
      probs = probs.append(0);
      k = k + 1;
    }
  }
  return __Nc { ...c, probs: probs, bit: bit, zero: false };
}

function __nc_count(buf: [u8], c: __Nc): __Nc {
  let max: i32 = 2 * c.threshold - 1 - c.remaining;
  let count: i32 = __fwd(buf, c.bit, c.nbits - 1);
  let bit: i32 = c.bit + c.nbits - 1;
  if (count >= max) {
    count = __fwd(buf, c.bit, c.nbits);
    if (count >= c.threshold) {
      count = count - max;
    }
    bit = c.bit + c.nbits;
  }
  let prob: i32 = count - 1;
  let remaining: i32 = c.remaining - prob;
  if (prob < 0) {
    remaining = c.remaining - 1;
  }
  let threshold: i32 = c.threshold;
  let nbits: i32 = c.nbits;
  while (remaining < threshold && nbits > 1) {
    nbits = nbits - 1;
    threshold = threshold >> 1;
  }
  return __Nc { probs: c.probs.append(prob), bit: bit, remaining: remaining, threshold: threshold, nbits: nbits, zero: prob == 0 };
}

function __rle_fse(sym: i32): __Fse {
  return __Fse { log: 0, sym: [sym], nb: [0], base: [0] };
}
```

### `17 struct __Huf`

```fern
<<17 struct __Huf>>=
struct __Huf { max: i32, sym: i32[], nb: i32[] }

struct __Literals { bytes: u8[], huf: __Huf, end: i32 }
```

### `18 function __literals(input: [u8], at: i32, end: i32, prev: __Huf): Result[__Literals, ZstdError]`

```fern
<<18 function __literals(input: [u8], at: i32, end: i32, prev: __Huf): Result[__Literals, ZstdError]>>=
function __literals(input: [u8], at: i32, end: i32, prev: __Huf): Result[__Literals, ZstdError] {
  if (at >= end) {
    return Err(ZstdTruncated);
  }
  let b0: i32 = input[at] as i32;
  let kind: i32 = b0 & 3;
  if (kind < 2) {
    return __plain_literals(input, at, end, prev, kind);
  }
  let sizes: (i32, i32, i32, i32) = __coded_sizes(input, at, end)?;
  let start: i32 = at + sizes.2;
  if (start + sizes.1 > end) {
    return Err(ZstdTruncated);
  }
  let huf: __Huf = prev;
  let streams: i32 = start;
  if (kind == 2) {
    let read: (__Huf, i32) = __read_huf(input, start, start + sizes.1)?;
    huf = read.0;
    streams = start + read.1;
  }
  if (huf.max < 0) {
    return Err(ZstdMalformed("treeless literals without a table"));
  }
  let bytes: u8[] = __huf_streams(input, streams, start + sizes.1, huf, sizes.0, sizes.3)?;
  return Ok(__Literals { bytes: bytes, huf: huf, end: start + sizes.1 });
}

function __plain_literals(input: [u8], at: i32, end: i32, prev: __Huf, kind: i32): Result[__Literals, ZstdError] {
  let b0: i32 = input[at] as i32;
  let format: i32 = b0 >> 2 & 3;
  let size: i32 = b0 >> 3;
  let head: i32 = 1;
  if (format == 1) {
    head = 2;
  } else if (format == 3) {
    head = 3;
  }
  if (at + head > end) {
    return Err(ZstdTruncated);
  }
  if (head > 1) {
    size = (b0 >> 4) + (__le(input, at + 1, head - 1) as i32 << 4);
  }
  let empty: u8[] = [];
  if (kind == 1) {
    if (at + head + 1 > end) {
      return Err(ZstdTruncated);
    }
    return Ok(__Literals { bytes: __repeated(empty, input[at + head], size), huf: prev, end: at + head + 1 });
  }
  if (at + head + size > end) {
    return Err(ZstdTruncated);
  }
  return Ok(__Literals { bytes: __copied(empty, input, at + head, at + head + size), huf: prev, end: at + head + size });
}

function __coded_sizes(input: [u8], at: i32, end: i32): Result[(i32, i32, i32, i32), ZstdError] {
  let format: i32 = input[at] as i32 >> 2 & 3;
  let head: i32 = [3, 3, 4, 5][format];
  let bits: i32 = [10, 10, 14, 18][format];
  if (at + head > end) {
    return Err(ZstdTruncated);
  }
  let v: i64 = __le(input, at, head) >> 4;
  let mask: i64 = (1 as i64 << bits) - 1;
  let streams: i32 = 4;
  if (format == 0) {
    streams = 1;
  }
  return Ok(((v & mask) as i32, (v >> bits & mask) as i32, head, streams));
}

function __read_huf(input: [u8], at: i32, end: i32): Result[(__Huf, i32), ZstdError] {
  if (at >= end) {
    return Err(ZstdTruncated);
  }
  let head: i32 = input[at] as i32;
  if (head >= 128) {
    let n: i32 = head - 127;
    let len: i32 = n + 1 >> 1;
    if (at + 1 + len > end) {
      return Err(ZstdTruncated);
    }
    let weights: i32[] = [];
    let i: i32 = 0;
    while (i < n) {
      weights = weights.append(input[at + 1 + (i >> 1)] as i32 >> 4 * (1 - (i & 1)) & 15);
      i = i + 1;
    }
    return Ok((__huf_table(weights)?, 1 + len));
  }
  if (at + 1 + head > end) {
    return Err(ZstdTruncated);
  }
  let weights: i32[] = __fse_weights(input, at + 1, at + 1 + head)?;
  return Ok((__huf_table(weights)?, 1 + head));
}

function __fse_weights(input: [u8], at: i32, end: i32): Result[i32[], ZstdError] {
  let read: (__Fse, i32) = __read_fse(input, at, end, 255, 6)?;
  let t: __Fse = read.0;
  let start: i32 = at + read.1;
  let pos: i32 = __back_start(input, start, end)?;
  let s1: i32 = __back(input, start, pos, t.log);
  let s2: i32 = __back(input, start, pos - t.log, t.log);
  pos = pos - 2 * t.log;
  let out: i32[] = [];
  while (out.len() < 255) {
    out = out.append(t.sym[s1]);
    let nb: i32 = t.nb[s1];
    s1 = t.base[s1] + __back(input, start, pos, nb);
    pos = pos - nb;
    if (pos < 0) {
      return Ok(out.append(t.sym[s2]));
    }
    let tmp: i32 = s1;
    s1 = s2;
    s2 = tmp;
  }
  return Err(ZstdMalformed("Huffman weights"));
}
```

### `19 function __huf_table(weights: i32[]): Result[__Huf, ZstdError]`

```fern
<<19 function __huf_table(weights: i32[]): Result[__Huf, ZstdError]>>=
function __huf_table(weights: i32[]): Result[__Huf, ZstdError] {
  let sum: i32 = 0;
  for w in weights {
    if (w > 11) {
      return Err(ZstdMalformed("Huffman weight"));
    }
    if (w > 0) {
      sum = sum + (1 << w - 1);
    }
  }
  if (sum == 0) {
    return Err(ZstdMalformed("Huffman weights"));
  }
  let max: i32 = __highbit(sum) + 1;
  let left: i32 = (1 << max) - sum;
  if (max > 11 || (left & left - 1) != 0) {
    return Err(ZstdMalformed("Huffman weights"));
  }
  return Ok(__huf_fill(weights.append(__highbit(left) + 1), max));
}

function __huf_fill(weights: i32[], max: i32): __Huf {
  let start: i32[] = __zeros(max + 2);
  for w in weights {
    if (w > 0) {
      start = start.with(w + 1, start[w + 1] + (1 << w - 1));
    }
  }
  let w: i32 = 1;
  while (w <= max) {
    start = start.with(w + 1, start[w + 1] + start[w]);
    w = w + 1;
  }
  let sym: i32[] = __zeros(1 << max);
  let nb: i32[] = __zeros(1 << max);
  let s: i32 = 0;
  while (s < weights.len()) {
    let wt: i32 = weights[s];
    let k: i32 = 0;
    while (wt > 0 && k < 1 << wt - 1) {
      sym = sym.with(start[wt] + k, s);
      nb = nb.with(start[wt] + k, max + 1 - wt);
      k = k + 1;
    }
    if (wt > 0) {
      start = start.with(wt, start[wt] + k);
    }
    s = s + 1;
  }
  return __Huf { max: max, sym: sym, nb: nb };
}

function __huf_streams(input: [u8], at: i32, end: i32, huf: __Huf, size: i32, streams: i32): Result[u8[], ZstdError] {
  let out: u8[] = [];
  if (streams == 1) {
    return __huf_stream(out, input, at, end, huf, size);
  }
  if (at + 6 > end) {
    return Err(ZstdTruncated);
  }
  let seg: i32 = size + 3 >> 2;
  let from: i32 = at + 6;
  let k: i32 = 0;
  while (k < 4) {
    let len: i32 = end - from;
    let n: i32 = size - 3 * seg;
    if (k < 3) {
      len = __le(input, at + 2 * k, 2) as i32;
      n = seg;
    }
    if (from + len > end || n < 0) {
      return Err(ZstdMalformed("literal streams"));
    }
    match (__huf_stream(out, input, from, from + len, huf, n)) {
      Ok(more) => {
        out = more;
      },
      Err(e) => {
        return Err(e);
      }
    }
    from = from + len;
    k = k + 1;
  }
  return Ok(out);
}

function __huf_stream(out: u8[], input: [u8], at: i32, end: i32, huf: __Huf, n: i32): Result[u8[], ZstdError] {
  let pos: i32 = __back_start(input, at, end)?;
  let i: i32 = 0;
  while (i < n) {
    let idx: i32 = __back(input, at, pos, huf.max);
    out = out.append(huf.sym[idx] as u8);
    pos = pos - huf.nb[idx];
    i = i + 1;
  }
  if (pos != 0) {
    return Err(ZstdMalformed("literal stream length"));
  }
  return Ok(out);
}
```

### `20 struct __Seqs`

```fern
<<20 struct __Seqs>>=
struct __Seqs { ll: i32[], ml: i32[], of: i32[], matched: i32, z: __Tables }
```

### `21 function __ll_default(): i32[]`

```fern
<<21 function __ll_default(): i32[]>>=
function __ll_default(): i32[] {
  return [4, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2, 1, 1, 1, 1, 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1];
}

function __ml_default(): i32[] {
  return [1, 4, 3, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1];
}

function __of_default(): i32[] {
  return [1, 1, 1, 1, 1, 1, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1];
}

function __ll_base(): i32[] {
  return [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24, 28, 32, 40, 48, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536];
}

function __ll_bits(): i32[] {
  return [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 3, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];
}

function __ml_base(): i32[] {
  return [3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 37, 39, 41, 43, 47, 51, 59, 67, 83, 99, 131, 259, 515, 1027, 2051, 4099, 8195, 16387, 32771, 65539];
}

function __ml_bits(): i32[] {
  return [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 3, 3, 4, 4, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];
}

function __sequences(input: [u8], at: i32, end: i32, z: __Tables): Result[__Seqs, ZstdError] {
  let none: i32[] = [];
  let count: (i32, i32) = __seq_count(input, at, end)?;
  if (count.0 == 0) {
    if (count.1 != end) {
      return Err(ZstdMalformed("sequences section"));
    }
    return Ok(__Seqs { ll: none, ml: none, of: none, matched: 0, z: z });
  }
  let pos: i32 = count.1 + 1;
  if (pos > end || (input[count.1] as i32 & 3) != 0) {
    return Err(ZstdMalformed("sequence modes"));
  }
  let modes: i32 = input[count.1] as i32;
  let ll: (__Fse, i32) = __seq_table(input, pos, end, modes >> 6, z.ll, __Field { probs: __ll_default(), log: 6, max_sym: 35, max_log: 9 })?;
  let of: (__Fse, i32) = __seq_table(input, pos + ll.1, end, modes >> 4 & 3, z.of, __Field { probs: __of_default(), log: 5, max_sym: 31, max_log: 8 })?;
  let ml: (__Fse, i32) = __seq_table(input, pos + ll.1 + of.1, end, modes >> 2 & 3, z.ml, __Field { probs: __ml_default(), log: 6, max_sym: 52, max_log: 9 })?;
  let t: __Tables = __Tables { ...z, ll: ll.0, of: of.0, ml: ml.0 };
  return __decode_seqs(input, pos + ll.1 + of.1 + ml.1, end, count.0, t);
}

function __seq_count(input: [u8], at: i32, end: i32): Result[(i32, i32), ZstdError] {
  if (at >= end) {
    return Err(ZstdTruncated);
  }
  let b0: i32 = input[at] as i32;
  if (b0 < 128) {
    return Ok((b0, at + 1));
  }
  if (b0 < 255) {
    if (at + 2 > end) {
      return Err(ZstdTruncated);
    }
    return Ok(((b0 - 128 << 8) + input[at + 1] as i32, at + 2));
  }
  if (at + 3 > end) {
    return Err(ZstdTruncated);
  }
  return Ok((__le(input, at + 1, 2) as i32 + 0x7F00, at + 3));
}
```

### `22 struct __Field`

```fern
<<22 struct __Field>>=
struct __Field { probs: i32[], log: i32, max_sym: i32, max_log: i32 }
```

### `23 function __seq_table(input: [u8], at: i32, end: i32, mode: i32, prev: __Fse, f: __Field): Result[(__Fse, i3...`

```fern
<<23 function __seq_table(input: [u8], at: i32, end: i32, mode: i32, prev: __Fse, f: __Field): Result[(__Fse, i3...>>=
function __seq_table(input: [u8], at: i32, end: i32, mode: i32, prev: __Fse, f: __Field): Result[(__Fse, i32), ZstdError] {
  if (mode == 0) {
    return Ok((__fse_table(f.probs, f.log), 0));
  }
  if (mode == 1) {
    if (at >= end || input[at] as i32 > f.max_sym) {
      return Err(ZstdMalformed("sequence RLE symbol"));
    }
    return Ok((__rle_fse(input[at] as i32), 1));
  }
  if (mode == 2) {
    return __read_fse(input, at, end, f.max_sym, f.max_log);
  }
  if (prev.log < 0) {
    return Err(ZstdMalformed("repeated table without one"));
  }
  return Ok((prev, 0));
}
```

### `24 struct __Codes`

```fern
<<24 struct __Codes>>=
struct __Codes { llb: i32[], llx: i32[], mlb: i32[], mlx: i32[] }

struct __Sd { pos: i32, ll: i32, of: i32, ml: i32 }
```

### `25 function __decode_seqs(input: [u8], start: i32, end: i32, n: i32, t: __Tables): Result[__Seqs, ZstdError]`

```fern
<<25 function __decode_seqs(input: [u8], start: i32, end: i32, n: i32, t: __Tables): Result[__Seqs, ZstdError]>>=
function __decode_seqs(input: [u8], start: i32, end: i32, n: i32, t: __Tables): Result[__Seqs, ZstdError] {
  let codes: __Codes = __Codes { llb: __ll_base(), llx: __ll_bits(), mlb: __ml_base(), mlx: __ml_bits() };
  let pos: i32 = __back_start(input, start, end)?;
  let d: __Sd = __Sd { pos: pos - t.ll.log - t.of.log - t.ml.log, ll: __back(input, start, pos, t.ll.log), of: __back(input, start, pos - t.ll.log, t.of.log), ml: __back(input, start, pos - t.ll.log - t.of.log, t.ml.log) };
  let lls: i32[] = [];
  let mls: i32[] = [];
  let ofs: i32[] = [];
  let reps: i32[] = t.reps;
  let matched: i32 = 0;
  while (lls.len() < n) {
    let v: (i32, i32, i32, i32) = __seq_values(input, start, d, t, codes)?;
    let off: (i32, i32[]) = __resolve(v.0, v.2, reps);
    reps = off.1;
    lls = lls.append(v.2);
    mls = mls.append(v.1);
    ofs = ofs.append(off.0);
    matched = matched + v.1;
    d = __Sd { ...d, pos: v.3 };
    if (lls.len() < n) {
      d = __seq_update(input, start, d, t);
    }
  }
  if (d.pos != 0) {
    return Err(ZstdMalformed("sequence stream length"));
  }
  return Ok(__Seqs { ll: lls, ml: mls, of: ofs, matched: matched, z: __Tables { ...t, reps: reps } });
}

function __seq_values(input: [u8], start: i32, d: __Sd, t: __Tables, codes: __Codes): Result[(i32, i32, i32, i32), ZstdError] {
  let oc: i32 = t.of.sym[d.of];
  let mc: i32 = t.ml.sym[d.ml];
  let lc: i32 = t.ll.sym[d.ll];
  if (oc > 30) {
    return Err(ZstdUnsupported("offset code past 30"));
  }
  let pos: i32 = d.pos;
  let ofv: i32 = (1 << oc) + __back(input, start, pos, oc);
  pos = pos - oc;
  let ml: i32 = codes.mlb[mc] + __back(input, start, pos, codes.mlx[mc]);
  pos = pos - codes.mlx[mc];
  let ll: i32 = codes.llb[lc] + __back(input, start, pos, codes.llx[lc]);
  return Ok((ofv, ml, ll, pos - codes.llx[lc]));
}

function __seq_update(input: [u8], start: i32, d: __Sd, t: __Tables): __Sd {
  let pos: i32 = d.pos;
  let ll: i32 = t.ll.base[d.ll] + __back(input, start, pos, t.ll.nb[d.ll]);
  pos = pos - t.ll.nb[d.ll];
  let ml: i32 = t.ml.base[d.ml] + __back(input, start, pos, t.ml.nb[d.ml]);
  pos = pos - t.ml.nb[d.ml];
  let of: i32 = t.of.base[d.of] + __back(input, start, pos, t.of.nb[d.of]);
  return __Sd { pos: pos - t.of.nb[d.of], ll: ll, of: of, ml: ml };
}

function __resolve(ofv: i32, ll: i32, reps: i32[]): (i32, i32[]) {
  if (ofv > 3) {
    return (ofv - 3, [ofv - 3, reps[0], reps[1]]);
  }
  let idx: i32 = ofv - 1;
  if (ll == 0) {
    idx = idx + 1;
  }
  if (idx == 0) {
    return (reps[0], reps);
  }
  if (idx == 1) {
    return (reps[1], [reps[1], reps[0], reps[2]]);
  }
  let off: i32 = reps[0] - 1;
  if (idx == 2) {
    off = reps[2];
  }
  return (off, [off, reps[0], reps[1]]);
}

function __execute(out: u8[], lits: u8[], seq: __Seqs): Result[u8[], ZstdError] {
  let li: i32 = 0;
  let i: i32 = 0;
  while (i < seq.ll.len()) {
    let ll: i32 = seq.ll[i];
    let off: i32 = seq.of[i];
    if (li + ll > lits.len() || off <= 0 || off > out.len() + ll) {
      return Err(ZstdMalformed("sequence"));
    }
    let k: i32 = 0;
    while (k < ll) {
      out = out.append(lits[li + k]);
      k = k + 1;
    }
    li = li + ll;
    let from: i32 = out.len() - off;
    k = 0;
    while (k < seq.ml[i]) {
      out = out.append(out[from + k]);
      k = k + 1;
    }
    i = i + 1;
  }
  while (li < lits.len()) {
    out = out.append(lits[li]);
    li = li + 1;
  }
  return Ok(out);
}
```

### `26 pub function compress(input: [u8]): u8[]`

```fern
<<26 pub function compress(input: [u8]): u8[]>>=
pub function compress(input: [u8]): u8[] {
  let window: i32 = __window_for(input.len());
  let toks: i64[] = __ztokens(input, window);
  let out: u8[] = __frame_head(input.len(), window);
  let reps: i32[] = [1, 4, 8];
  let at: i32 = 0;
  let t: i32 = 0;
  while (at < input.len() || at == 0) {
    let end: i32 = cmp.min(at + 131072, input.len());
    let b: __Seqb = __block_seqs(input, toks, t, at, end);
    let seqs: (u8[], i32[]) = __seq_section(b, reps);
    let body: u8[] = __copied(__lits_section(b.lits), seqs.0, 0, seqs.0.len());
    let kind: i32 = __kind(input, at, end, body);
    if (kind == 2) {
      reps = seqs.1;
    }
    out = __put_block(out, input, at, end, body, kind);
    t = b.next;
    at = end;
    if (end == 0) {
      at = 1;
    }
  }
  let sum: i64 = (hash.xxh64(input, 0) & 0xFFFFFFFF) as i64;
  return __put_le(out, sum, 4);
}
```

### `27 function __window_for(n: i32): i32`

```fern
<<27 function __window_for(n: i32): i32>>=
function __window_for(n: i32): i32 {
  let w: i32 = 1024;
  while (w < n && w < 1048576) {
    w = w * 2;
  }
  return w;
}

function __put_le(own out: u8[], v: i64, n: i32): u8[] {
  let i: i32 = 0;
  while (i < n) {
    out = out.append((v >> 8 * i & 255) as u8);
    i = i + 1;
  }
  return out;
}

function __frame_head(n: i32, window: i32): u8[] {
  let out: u8[] = __put_le([], 0xFD2FB528, 4);
  let flag: i32 = 2;
  let size: i64 = n as i64;
  if (n < 256) {
    flag = 0;
  } else if (n < 65792) {
    flag = 1;
    size = size - 256;
  }
  if (n <= window) {
    out = out.append((flag << 6 | 32 | 4) as u8);
  } else {
    out = out.append((2 << 6 | 4) as u8).append((__highbit(window) - 10 << 3) as u8);
    flag = 2;
    size = n as i64;
  }
  return __put_le(out, size, [1, 2, 4][flag]);
}

function __ztokens(input: [u8], window: i32): i64[] {
  let n: i32 = input.len();
  let head: i32[] = __filled_neg(65536);
  let prev: i32[] = __filled_neg(window);
  let out: i64[] = [];
  let held: (i32, i32) = (0 - 1, 0);
  let skip: i32 = 0;
  let rep: i32 = 1;
  let pos: i32 = 0;
  while (pos < n) {
    let cur: (i32, i32) = __zmatch_at(input, pos, skip, head, prev, window, rep);
    if (pos + 3 < n) {
      let h: i32 = __hash4(input, pos);
      prev = prev.with(pos & window - 1, head[h]);
      head = head.with(h, pos);
    }
    if (pos >= skip && held.0 >= 4 && __gain(cur, rep) <= __gain(held, rep) + 4) {
      out = out.append(held.0 as i64 << 32 | held.1 as i64);
      skip = pos - 1 + held.0;
      rep = held.1;
      held = (0 - 1, 0);
    } else if (pos >= skip) {
      if (held.0 >= 0) {
        out = out.append(0);
      }
      held = cur;
    }
    pos = pos + 1;
  }
  if (held.0 >= 4) {
    return out.append(held.0 as i64 << 32 | held.1 as i64);
  }
  if (held.0 >= 0) {
    return out.append(0);
  }
  return out;
}

function __filled_neg(n: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(0 - 1);
    i = i + 1;
  }
  return out;
}

function __hash4(input: [u8], pos: i32): i32 {
  let v: i64 = __le(input, pos, 4);
  return (v * 2654435761 >> 16 & 65535) as i32;
}

function __zmatch_at(input: [u8], pos: i32, skip: i32, head: i32[], prev: i32[], window: i32, rep: i32): (i32, i32) {
  if (pos < skip || pos + 3 >= input.len()) {
    return (0, 0);
  }
  let limit: i32 = cmp.min(input.len(), (pos / 131072 + 1) * 131072);
  let best: (i32, i32) = (0, 0);
  if (rep <= pos) {
    best = (__match_len(input, pos - rep, pos, limit), rep);
  }
  let cand: i32 = head[__hash4(input, pos)];
  let depth: i32 = 0;
  while (cand >= 0 && pos - cand < window && depth < 32 && best.0 < 256) {
    let m: (i32, i32) = (__match_len(input, cand, pos, limit), pos - cand);
    if (__gain(m, rep) > __gain(best, rep)) {
      best = m;
    }
    cand = prev[cand & window - 1];
    depth = depth + 1;
  }
  if (best.0 < 4) {
    return (0, 0);
  }
  return best;
}

function __match_len(input: [u8], from: i32, pos: i32, limit: i32): i32 {
  let len: i32 = 0;
  while (pos + len < limit && input[from + len] == input[pos + len]) {
    len = len + 1;
  }
  return len;
}

function __gain(m: (i32, i32), rep: i32): i32 {
  if (m.0 < 4) {
    return 0;
  }
  if (m.1 == rep) {
    return 4 * m.0 - 1;
  }
  return 4 * m.0 - __highbit(m.1 + 1);
}
```

### `28 struct __Seqb`

```fern
<<28 struct __Seqb>>=
struct __Seqb { lits: u8[], ll: i32[], ml: i32[], of: i32[], next: i32 }
```

### `29 function __block_seqs(input: [u8], toks: i64[], t: i32, at: i32, end: i32): __Seqb`

```fern
<<29 function __block_seqs(input: [u8], toks: i64[], t: i32, at: i32, end: i32): __Seqb>>=
function __block_seqs(input: [u8], toks: i64[], t: i32, at: i32, end: i32): __Seqb {
  let lits: u8[] = [];
  let ll: i32[] = [];
  let ml: i32[] = [];
  let of: i32[] = [];
  let run: i32 = 0;
  let pos: i32 = at;
  while (pos < end) {
    let tok: i64 = toks[t];
    if (tok == 0) {
      lits = lits.append(input[pos]);
      run = run + 1;
      pos = pos + 1;
    } else {
      ll = ll.append(run);
      ml = ml.append((tok >> 32) as i32);
      of = of.append((tok & 0xFFFFFFFF) as i32);
      run = 0;
      pos = pos + (tok >> 32) as i32;
    }
    t = t + 1;
  }
  return __Seqb { lits: lits, ll: ll, ml: ml, of: of, next: t };
}

function __kind(input: [u8], at: i32, end: i32, body: u8[]): i32 {
  if (end - at > 1 && __one_byte(input, at, end)) {
    return 1;
  }
  if (body.len() < end - at) {
    return 2;
  }
  return 0;
}

function __put_block(own out: u8[], input: [u8], at: i32, end: i32, body: u8[], kind: i32): u8[] {
  let last: i32 = 0;
  if (end == input.len()) {
    last = 1;
  }
  if (kind == 1) {
    return __put_le(out, (last | 1 << 1 | end - at << 3) as i64, 3).append(input[at]);
  }
  if (kind == 2) {
    return __copied(__put_le(out, (last | 2 << 1 | body.len() << 3) as i64, 3), body, 0, body.len());
  }
  return __copied(__put_le(out, (last | end - at << 3) as i64, 3), input, at, end);
}

function __one_byte(input: [u8], at: i32, end: i32): boolean {
  let i: i32 = at + 1;
  while (i < end) {
    if (input[i] != input[at]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __acc(acc: i64, nb: i32, v: i64, bits: i32): i64 {
  return acc | (v & (1 as i64 << bits) - 1) << nb;
}

function __flushed(own out: u8[], acc: i64, nb: i32): u8[] {
  let i: i32 = 0;
  while (i + 8 <= nb) {
    out = out.append((acc >> i & 255) as u8);
    i = i + 8;
  }
  return out;
}

function __closed(own out: u8[], acc: i64, nb: i32): u8[] {
  return __flushed(out, acc | 1 as i64 << nb, nb + 8 >> 3 << 3);
}

function __lit_head(kind: i32, n: i32): u8[] {
  if (n <= 31) {
    return [(kind | n << 3) as u8];
  }
  if (n <= 4095) {
    return [(kind | 1 << 2 | (n & 15) << 4) as u8, (n >> 4) as u8];
  }
  return [(kind | 3 << 2 | (n & 15) << 4) as u8, (n >> 4 & 255) as u8, (n >> 12) as u8];
}

function __lits_section(lits: u8[]): u8[] {
  let raw: u8[] = __copied(__lit_head(0, lits.len()), lits, 0, lits.len());
  if (lits.len() < 32) {
    return raw;
  }
  let freq: i32[] = __zeros(256);
  let distinct: i32 = 0;
  for b in lits {
    if (freq[b as i32] == 0) {
      distinct = distinct + 1;
    }
    freq = freq.with(b as i32, freq[b as i32] + 1);
  }
  if (distinct == 1) {
    return __lit_head(1, lits.len()).append(lits[0]);
  }
  match (__huf_lits(lits, freq)) {
    Some(coded) => {
      if (coded.len() < raw.len()) {
        return coded;
      }
    },
    None => {}
  }
  return raw;
}

function __huf_lits(lits: u8[], freq: i32[]): Option[u8[]] {
  let lens: i32[] = deflate.huffman_lengths(freq, 11);
  let max: i32 = 0;
  for l in lens {
    max = cmp.max(max, l);
  }
  let weights: i32[] = [];
  let last: i32 = 0;
  for l in lens {
    if (l > 0) {
      last = weights.len();
      weights = weights.append(max + 1 - l);
    } else {
      weights = weights.append(0);
    }
  }
  let desc: u8[] = __huf_desc(weights, last)?;
  let streams: u8[] = __huf_encode(lits, __huf_codes(weights, max), lens);
  let body: u8[] = __copied(desc, streams, 0, streams.len());
  return __huf_head(lits.len(), body);
}
```

### `30 function __huf_head(n: i32, body: u8[]): Option[u8[]]`

```fern
<<30 function __huf_head(n: i32, body: u8[]): Option[u8[]]>>=
function __huf_head(n: i32, body: u8[]): Option[u8[]] {
  let format: i32 = 0;
  if (n > 1023) {
    format = 1;
  }
  while (format < 4 && cmp.max(n, body.len()) >= 1 << [10, 10, 14, 18][cmp.min(format, 3)]) {
    format = format + 1;
  }
  if (format > 3 || n <= 1023 && format > 0) {
    return None;
  }
  let bits: i32 = [10, 10, 14, 18][format];
  let v: i64 = (2 | format << 2) as i64 | n as i64 << 4 | body.len() as i64 << 4 + bits;
  return Some(__copied(__put_le([], v, [3, 3, 4, 5][format]), body, 0, body.len()));
}

function __huf_desc(weights: i32[], last: i32): Option[u8[]] {
  if (last <= 128) {
    let out: u8[] = [(127 + last) as u8];
    let i: i32 = 0;
    while (i < last) {
      let low: i32 = 0;
      if (i + 1 < last) {
        low = weights[i + 1];
      }
      out = out.append((weights[i] << 4 | low) as u8);
      i = i + 2;
    }
    return Some(out);
  }
  return __fse_weight_desc(__head_of(weights, last));
}

function __head_of(xs: i32[], n: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

function __huf_codes(weights: i32[], max: i32): i32[] {
  let start: i32[] = __zeros(max + 2);
  for w in weights {
    if (w > 0) {
      start = start.with(w + 1, start[w + 1] + (1 << w - 1));
    }
  }
  let w: i32 = 1;
  while (w <= max) {
    start = start.with(w + 1, start[w + 1] + start[w]);
    w = w + 1;
  }
  let codes: i32[] = [];
  for wt in weights {
    if (wt > 0) {
      codes = codes.append(start[wt] >> wt - 1);
      start = start.with(wt, start[wt] + (1 << wt - 1));
    } else {
      codes = codes.append(0);
    }
  }
  return codes;
}

function __huf_encode(lits: u8[], codes: i32[], lens: i32[]): u8[] {
  let n: i32 = lits.len();
  let empty: u8[] = [];
  if (n <= 1023) {
    return __huf_one(empty, lits, 0, n, codes, lens);
  }
  let seg: i32 = n + 3 >> 2;
  let parts: u8[] = [];
  let sizes: u8[] = [];
  let k: i32 = 0;
  while (k < 4) {
    let before: i32 = parts.len();
    parts = __huf_one(parts, lits, k * seg, cmp.min(n, (k + 1) * seg), codes, lens);
    if (k < 3) {
      sizes = __put_le(sizes, (parts.len() - before) as i64, 2);
    }
    k = k + 1;
  }
  return __copied(sizes, parts, 0, parts.len());
}

function __huf_one(own out: u8[], lits: u8[], from: i32, to: i32, codes: i32[], lens: i32[]): u8[] {
  let acc: i64 = 0;
  let nb: i32 = 0;
  let i: i32 = to - 1;
  while (i >= from) {
    let s: i32 = lits[i] as i32;
    acc = __acc(acc, nb, codes[s] as i64, lens[s]);
    nb = nb + lens[s];
    if (nb >= 32) {
      out = __flushed(out, acc, nb);
      acc = acc >> (nb >> 3 << 3);
      nb = nb & 7;
    }
    i = i - 1;
  }
  return __closed(out, acc, nb);
}

function __fse_weight_desc(w: i32[]): Option[u8[]] {
  let counts: i32[] = __zeros(13);
  for x in w {
    counts = counts.with(x, counts[x] + 1);
  }
  let probs: i32[] = __normalize(counts, w.len(), 6)?;
  let ct: __Ct = __ctable(probs, 6);
  let body: u8[] = __two_state(__write_ncount(probs, 6), w, ct);
  if (body.len() > 127) {
    return None;
  }
  let out: u8[] = __copied([body.len() as u8], body, 0, body.len());
  match (__fse_weights(out, 1, out.len())) {
    Ok(back) => {
      if (__same(back, w)) {
        return Some(out);
      }
    },
    Err(e) => {}
  }
  return None;
}
```

### `31 function __same(a: i32[], b: i32[]): boolean`

```fern
<<31 function __same(a: i32[], b: i32[]): boolean>>=
function __same(a: i32[], b: i32[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `32 struct __Ct`

```fern
<<32 struct __Ct>>=
struct __Ct { log: i32, state: i32[], dnb: i32[], dfs: i32[] }
```

### `33 function __ctable(probs: i32[], log: i32): __Ct`

```fern
<<33 function __ctable(probs: i32[], log: i32): __Ct>>=
function __ctable(probs: i32[], log: i32): __Ct {
  let size: i32 = 1 << log;
  let sym: i32[] = __symbols(probs, log);
  let next: i32[] = [];
  let dnb: i32[] = [];
  let dfs: i32[] = [];
  let total: i32 = 0;
  for p in probs {
    next = next.append(total);
    let slots: i32 = cmp.max(p, 1);
    if (p == 0) {
      slots = 0;
    }
    let bits: i32 = log - __highbit(slots - 1);
    if (slots <= 1) {
      bits = log;
    }
    dnb = dnb.append((bits << 16) - (cmp.max(slots, 1) << bits));
    dfs = dfs.append(total - slots);
    total = total + slots;
  }
  let state: i32[] = __zeros(size);
  let u: i32 = 0;
  while (u < size) {
    state = state.with(next[sym[u]], size + u);
    next = next.with(sym[u], next[sym[u]] + 1);
    u = u + 1;
  }
  return __Ct { log: log, state: state, dnb: dnb, dfs: dfs };
}

function __ct_init(ct: __Ct, sym: i32): i32 {
  let bits: i32 = ct.dnb[sym] + (1 << 15) >> 16;
  let value: i32 = (bits << 16) - ct.dnb[sym];
  return ct.state[(value >> bits) + ct.dfs[sym]];
}

function __ct_bits(ct: __Ct, st: i32, sym: i32): i32 {
  return st + ct.dnb[sym] >> 16;
}

function __ct_next(ct: __Ct, st: i32, sym: i32): i32 {
  return ct.state[(st >> __ct_bits(ct, st, sym)) + ct.dfs[sym]];
}

function __normalize(counts: i32[], total: i32, log: i32): Option[i32[]] {
  let size: i32 = 1 << log;
  let probs: i32[] = [];
  let sum: i32 = 0;
  let big: i32 = 0;
  for c in counts {
    let p: i32 = 0;
    if (c > 0) {
      p = ((c as i64 * (size as i64) + (total / 2) as i64) / (total as i64)) as i32;
      if (p == 0) {
        p = 0 - 1;
      }
    }
    if (c > counts[big]) {
      big = probs.len();
    }
    probs = probs.append(p);
    sum = sum + cmp.max(p, 1);
    if (p == 0) {
      sum = sum - 1;
    }
  }
  let fixed: i32 = probs[big] + size - sum;
  if (fixed < 1) {
    return None;
  }
  return Some(probs.with(big, fixed));
}

function __write_ncount(probs: i32[], log: i32): u8[] {
  let out: u8[] = [];
  let acc: i64 = (log - 5) as i64;
  let nb: i32 = 4;
  let c: __Nc = __Nc { probs: probs, bit: 0, remaining: (1 << log) + 1, threshold: 1 << log, nbits: log + 1, zero: false };
  while (c.remaining > 1 && c.bit < probs.len()) {
    if (c.zero) {
      let start: i32 = c.bit;
      while (probs[c.bit] == 0) {
        c = __Nc { ...c, bit: c.bit + 1 };
      }
      let run: i32 = c.bit - start;
      while (run >= 3) {
        acc = __acc(acc, nb, 3, 2);
        nb = nb + 2;
        run = run - 3;
      }
      acc = __acc(acc, nb, run as i64, 2);
      nb = nb + 2;
    }
    let field: (i32, i32) = __ncount_field(c);
    acc = __acc(acc, nb, field.0 as i64, field.1);
    nb = nb + field.1;
    c = __nc_after(c);
    if (nb >= 32) {
      out = __flushed(out, acc, nb);
      acc = acc >> (nb >> 3 << 3);
      nb = nb & 7;
    }
  }
  return __flushed(out, acc, nb + 7 >> 3 << 3);
}

function __ncount_field(c: __Nc): (i32, i32) {
  let p: i32 = c.probs[c.bit];
  let max: i32 = 2 * c.threshold - 1 - c.remaining;
  let v: i32 = p + 1;
  if (v >= c.threshold) {
    v = v + max;
  }
  if (v < max) {
    return (v, c.nbits - 1);
  }
  return (v, c.nbits);
}

function __nc_after(c: __Nc): __Nc {
  let p: i32 = c.probs[c.bit];
  let remaining: i32 = c.remaining - cmp.max(p, 0 - p);
  let threshold: i32 = c.threshold;
  let nbits: i32 = c.nbits;
  while (remaining < threshold && nbits > 1) {
    nbits = nbits - 1;
    threshold = threshold >> 1;
  }
  return __Nc { ...c, bit: c.bit + 1, remaining: remaining, threshold: threshold, nbits: nbits, zero: p == 0 };
}
```

### `34 function __two_state(own head: u8[], w: i32[], ct: __Ct): u8[]`

```fern
<<34 function __two_state(own head: u8[], w: i32[], ct: __Ct): u8[]>>=
function __two_state(own head: u8[], w: i32[], ct: __Ct): u8[] {
  let n: i32 = w.len();
  let acc: i64 = 0;
  let nb: i32 = 0;
  let s1: i32 = 0;
  let s2: i32 = 0;
  let i: i32 = n;
  if ((n & 1) == 1) {
    s1 = __ct_init(ct, w[n - 1]);
    s2 = __ct_init(ct, w[n - 2]);
    let bits: i32 = __ct_bits(ct, s1, w[n - 3]);
    acc = __acc(acc, nb, s1 as i64, bits);
    nb = nb + bits;
    s1 = __ct_next(ct, s1, w[n - 3]);
    i = n - 3;
  } else {
    s2 = __ct_init(ct, w[n - 1]);
    s1 = __ct_init(ct, w[n - 2]);
    i = n - 2;
  }
  while (i > 0) {
    let b2: i32 = __ct_bits(ct, s2, w[i - 1]);
    acc = __acc(acc, nb, s2 as i64, b2);
    nb = nb + b2;
    s2 = __ct_next(ct, s2, w[i - 1]);
    let b1: i32 = __ct_bits(ct, s1, w[i - 2]);
    acc = __acc(acc, nb, s1 as i64, b1);
    nb = nb + b1;
    s1 = __ct_next(ct, s1, w[i - 2]);
    head = __flushed(head, acc, nb);
    acc = acc >> (nb >> 3 << 3);
    nb = nb & 7;
    i = i - 2;
  }
  acc = __acc(acc, nb, s2 as i64, ct.log);
  acc = __acc(acc, nb + ct.log, s1 as i64, ct.log);
  return __closed(head, acc, nb + 2 * ct.log);
}
```

### `35 struct __Choice`

```fern
<<35 struct __Choice>>=
struct __Choice { mode: i32, ct: __Ct, desc: u8[] }
```

### `36 function __seq_section(b: __Seqb, reps: i32[]): (u8[], i32[])`

```fern
<<36 function __seq_section(b: __Seqb, reps: i32[]): (u8[], i32[])>>=
function __seq_section(b: __Seqb, reps: i32[]): (u8[], i32[]) {
  let n: i32 = b.ll.len();
  let out: u8[] = __seq_head(n);
  if (n == 0) {
    return (out, reps);
  }
  let codes: __Codes = __Codes { llb: __ll_base(), llx: __ll_bits(), mlb: __ml_base(), mlx: __ml_bits() };
  let lc: i32[] = [];
  let mc: i32[] = [];
  let oc: i32[] = [];
  let ofv: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    lc = lc.append(__code_of(codes.llb, b.ll[i]));
    mc = mc.append(__code_of(codes.mlb, b.ml[i]));
    let v: i32 = __rep_value(b.of[i], b.ll[i], reps);
    reps = __resolve(v, b.ll[i], reps).1;
    ofv = ofv.append(v);
    oc = oc.append(__highbit(v));
    i = i + 1;
  }
  let ll: __Choice = __choose(lc, __Field { probs: __ll_default(), log: 6, max_sym: 35, max_log: 9 });
  let of: __Choice = __choose(oc, __Field { probs: __of_default(), log: 5, max_sym: 31, max_log: 8 });
  let ml: __Choice = __choose(mc, __Field { probs: __ml_default(), log: 6, max_sym: 52, max_log: 9 });
  out = out.append((ll.mode << 6 | of.mode << 4 | ml.mode << 2) as u8);
  out = __copied(__copied(__copied(out, ll.desc, 0, ll.desc.len()), of.desc, 0, of.desc.len()), ml.desc, 0, ml.desc.len());
  return (__seq_stream(out, __Seqb { ...b, of: ofv }, __Codes3 { lc: lc, mc: mc, oc: oc }, __Tables3 { ll: ll.ct, of: of.ct, ml: ml.ct }, codes), reps);
}

function __seq_head(n: i32): u8[] {
  if (n < 128) {
    return [n as u8];
  }
  if (n < 0x7F00) {
    return [((n >> 8) + 128) as u8, (n & 255) as u8];
  }
  return [255 as u8, (n - 0x7F00 & 255) as u8, (n - 0x7F00 >> 8) as u8];
}

function __code_of(bases: i32[], v: i32): i32 {
  let c: i32 = bases.len() - 1;
  while (c > 0 && bases[c] > v) {
    c = c - 1;
  }
  return c;
}

function __choose(codes: i32[], f: __Field): __Choice {
  let counts: i32[] = __zeros(f.max_sym + 1);
  let distinct: i32 = 0;
  for c in codes {
    if (counts[c] == 0) {
      distinct = distinct + 1;
    }
    counts = counts.with(c, counts[c] + 1);
  }
  let empty: u8[] = [];
  if (distinct == 1) {
    return __Choice { mode: 1, ct: __Ct { log: 0, state: [], dnb: [], dfs: [] }, desc: [codes[0] as u8] };
  }
  let predefined: __Choice = __Choice { mode: 0, ct: __ctable(f.probs, f.log), desc: empty };
  if (codes.len() < 64) {
    return predefined;
  }
  let log: i32 = cmp.max(cmp.max(5, __highbit(distinct) + 2), cmp.min(f.max_log, __highbit(codes.len()) - 1));
  match (__normalize(counts, codes.len(), cmp.min(log, f.max_log))) {
    Some(probs) => {
      let at: i32 = cmp.min(log, f.max_log);
      return __Choice { mode: 2, ct: __ctable(probs, at), desc: __write_ncount(probs, at) };
    },
    None => {
      return predefined;
    }
  }
  return predefined;
}
```

### `37 struct __Codes3`

```fern
<<37 struct __Codes3>>=
struct __Codes3 { lc: i32[], mc: i32[], oc: i32[] }

struct __Tables3 { ll: __Ct, of: __Ct, ml: __Ct }

struct __Enc { ll: i32, of: i32, ml: i32, acc: i64, nb: i32 }
```

### `38 function __seq_stream(own out: u8[], b: __Seqb, c: __Codes3, t: __Tables3, codes: __Codes): u8[]`

```fern
<<38 function __seq_stream(own out: u8[], b: __Seqb, c: __Codes3, t: __Tables3, codes: __Codes): u8[]>>=
function __seq_stream(own out: u8[], b: __Seqb, c: __Codes3, t: __Tables3, codes: __Codes): u8[] {
  let n: i32 = b.ll.len();
  let e: __Enc = __Enc { ll: __init_or_zero(t.ll, c.lc[n - 1]), of: __init_or_zero(t.of, c.oc[n - 1]), ml: __init_or_zero(t.ml, c.mc[n - 1]), acc: 0, nb: 0 };
  e = __extras(e, b, c, codes, n - 1);
  let i: i32 = n - 2;
  while (i >= 0) {
    e = __step(e, t, c, i);
    out = __flushed(out, e.acc, e.nb);
    e = __Enc { ...e, acc: e.acc >> (e.nb >> 3 << 3), nb: e.nb & 7 };
    e = __extras(e, b, c, codes, i);
    out = __flushed(out, e.acc, e.nb);
    e = __Enc { ...e, acc: e.acc >> (e.nb >> 3 << 3), nb: e.nb & 7 };
    i = i - 1;
  }
  let acc: i64 = __acc(e.acc, e.nb, e.ml as i64, t.ml.log);
  acc = __acc(acc, e.nb + t.ml.log, e.of as i64, t.of.log);
  acc = __acc(acc, e.nb + t.ml.log + t.of.log, e.ll as i64, t.ll.log);
  let nb: i32 = e.nb + t.ml.log + t.of.log + t.ll.log;
  out = __flushed(out, acc, nb);
  return __closed(out, acc >> (nb >> 3 << 3), nb & 7);
}

function __init_or_zero(ct: __Ct, sym: i32): i32 {
  if (ct.log == 0) {
    return 0;
  }
  return __ct_init(ct, sym);
}

function __step(e: __Enc, t: __Tables3, c: __Codes3, i: i32): __Enc {
  let acc: i64 = e.acc;
  let nb: i32 = e.nb;
  let of: i32 = e.of;
  let ml: i32 = e.ml;
  let ll: i32 = e.ll;
  if (t.of.log > 0) {
    let bits: i32 = __ct_bits(t.of, of, c.oc[i]);
    acc = __acc(acc, nb, of as i64, bits);
    nb = nb + bits;
    of = __ct_next(t.of, of, c.oc[i]);
  }
  if (t.ml.log > 0) {
    let bits: i32 = __ct_bits(t.ml, ml, c.mc[i]);
    acc = __acc(acc, nb, ml as i64, bits);
    nb = nb + bits;
    ml = __ct_next(t.ml, ml, c.mc[i]);
  }
  if (t.ll.log > 0) {
    let bits: i32 = __ct_bits(t.ll, ll, c.lc[i]);
    acc = __acc(acc, nb, ll as i64, bits);
    nb = nb + bits;
    ll = __ct_next(t.ll, ll, c.lc[i]);
  }
  return __Enc { ll: ll, of: of, ml: ml, acc: acc, nb: nb };
}

function __extras(e: __Enc, b: __Seqb, c: __Codes3, codes: __Codes, i: i32): __Enc {
  let lx: i32 = codes.llx[c.lc[i]];
  let mx: i32 = codes.mlx[c.mc[i]];
  let ox: i32 = c.oc[i];
  let acc: i64 = __acc(e.acc, e.nb, (b.ll[i] - codes.llb[c.lc[i]]) as i64, lx);
  acc = __acc(acc, e.nb + lx, (b.ml[i] - codes.mlb[c.mc[i]]) as i64, mx);
  acc = __acc(acc, e.nb + lx + mx, (b.of[i] - (1 << ox)) as i64, ox);
  return __Enc { ...e, acc: acc, nb: e.nb + lx + mx + ox };
}

function __rep_value(off: i32, ll: i32, reps: i32[]): i32 {
  let k: i32 = 0;
  while (k < 3) {
    let v: i32 = k + 1;
    if (ll == 0) {
      v = k;
    }
    if (v >= 1 && off == reps[k]) {
      return v;
    }
    k = k + 1;
  }
  if (ll == 0 && off == reps[0] - 1) {
    return 3;
  }
  return off + 3;
}
```
