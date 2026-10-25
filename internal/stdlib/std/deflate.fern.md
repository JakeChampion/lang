# DEFLATE, zlib, and gzip

This module decodes DEFLATE, zlib, and gzip data.

## Model

The DEFLATE decoder interprets bit fields, Huffman codes, and history references. The zlib and gzip wrappers add their frame and checksum rules.

## Contracts

Output limits bound decompressed data. `Inflated` retains both the output and the consumed input count. Error values distinguish malformed data from incomplete data and output limits.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub enum InflateError>>

<<3 pub function (e: InflateError) message(): string>>

<<4 pub struct Inflated>>

<<5 pub function inflate(input: [u8], max_out: i32): Result[Inflated, InflateError]>>

<<6 function __inflated(input: [u8], from: i32, history: u8[], max_out: i32): Result[Inflated, InflateError]>>

<<7 struct __State>>

<<8 function __fail(own s: __State, e: InflateError): __State>>

<<9 struct __Huff>>

<<10 function __construct(lengths: i32[], n: i32): __Huff>>

<<11 const LENGTH_BASE: i32[]>>

<<12 function __table(h: __Huff, bits: i32, lit: boolean): i32[]>>

<<13 function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff): __State>>

<<14 function __fixed(own s: __State, data: [u8]): __State>>

<<15 function __repeated(own lengths: i32[], times: i32, value: i32): i32[]>>

<<16 pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError]>>

<<17 function __joined(own out: u8[], more: [u8]): u8[]>>

<<18 pub function zlib_decode(input: [u8], max_out: i32): Result[u8[], InflateError]>>

<<19 function __put_le32(own out: u8[], v: u32): u8[]>>

<<20 pub function deflate_flushed(input: [u8], window_bits: i32): u8[]>>

<<21 struct __Bits>>

<<22 function __put(w: __Bits, value: i32, bits: i32): __Bits>>

<<23 struct __Code>>

<<24 function __put_sym(w: __Bits, c: __Code, sym: i32): __Bits>>

<<25 function __put_match(w: __Bits, length: i32, distance: i32, lit: __Code, dist: __Code): __Bits>>

<<26 struct __Span>>

<<27 function __span(toks: i32[], at: i32, end: i32): i32>>

<<28 function __two_used(freq: i32[]): i32[]>>

<<29 pub function huffman_lengths(freq: i32[], limit: i32): i32[]>>

<<30 function __assigned(keys: i32[], counts: i32[], size: i32): i32[]>>

<<31 struct __Header>>

<<32 function __cl_order(): i32[]>>

<<33 function __fast_tokens(input: [u8]): i32[]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module decodes DEFLATE, zlib, and gzip data.
import "std/hash";
import "std/array";
import "core/cmp";
import "core/int";
import "std/error";
```

### `2 pub enum InflateError`

```fern
<<2 pub enum InflateError>>=
pub enum InflateError {
  Truncated,
  Malformed(string),
  OutputLimit,
  BadChecksum,
  BadHeader(string),
}

impl error.Error for InflateError {}

impl cmp.Display for InflateError {}
```

### `3 pub function (e: InflateError) message(): string`

```fern
<<3 pub function (e: InflateError) message(): string>>=
pub function (e: InflateError) message(): string {
  match (e) {
    Truncated => {
      return "the compressed data ends early";
    },
    Malformed(what) => {
      return "malformed compressed data: " + what;
    },
    OutputLimit => {
      return "the decompressed data is past its limit";
    },
    BadChecksum => {
      return "the checksum does not match the decompressed data";
    },
    BadHeader(what) => {
      return "unsupported compressed data: " + what;
    }
  }
  return "";
}

pub function (e: InflateError) to_string(): string {
  return e.message();
}
```

### `4 pub struct Inflated`

```fern
<<4 pub struct Inflated>>=
pub struct Inflated { out: u8[], consumed: i32 }
```

### `5 pub function inflate(input: [u8], max_out: i32): Result[Inflated, InflateError]`

```fern
<<5 pub function inflate(input: [u8], max_out: i32): Result[Inflated, InflateError]>>=
pub function inflate(input: [u8], max_out: i32): Result[Inflated, InflateError] {
  return inflate_from(input, 0, max_out);
}

pub function inflate_from(input: [u8], from: i32, max_out: i32): Result[Inflated, InflateError] {
  return __inflated(input, from, [], max_out);
}

pub function inflate_after(history: [u8], input: [u8], max_out: i32): Result[Inflated, InflateError] {
  match (__inflated(input, 0, __bytes_owned(history), max_out)) {
    Ok(got) => {
      return Ok(Inflated { ...got, out: array.drop(got.out, history.len()) });
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err(Truncated);
}
```

### `6 function __inflated(input: [u8], from: i32, history: u8[], max_out: i32): Result[Inflated, InflateError]`

```fern
<<6 function __inflated(input: [u8], from: i32, history: u8[], max_out: i32): Result[Inflated, InflateError]>>=
function __inflated(input: [u8], from: i32, history: u8[], max_out: i32): Result[Inflated, InflateError] {
  let s: __State = __State { pos: from, bitbuf: 0, bitcnt: 0, out: history, max_out: max_out + history.len(), last: 0, distance: 0, failed: false, error: Truncated, lengths: [] };
  let done: boolean = false;
  while (!done && !s.failed) {
    s = __bits(s, input, 1);
    done = s.last == 1;
    s = __bits(s, input, 2);
    let kind: i32 = s.last;
    s = __block(s, input, kind);
  }
  if (s.failed) {
    return Err(s.error);
  }
  return Ok(Inflated { out: s.out, consumed: s.pos - from });
}
```

### `7 struct __State`

```fern
<<7 struct __State>>=
struct __State { pos: i32, bitbuf: i32, bitcnt: i32, out: u8[], max_out: i32, last: i32, distance: i32, failed: boolean, error: InflateError, lengths: i32[] }
```

### `8 function __fail(own s: __State, e: InflateError): __State`

```fern
<<8 function __fail(own s: __State, e: InflateError): __State>>=
function __fail(own s: __State, e: InflateError): __State {
  if (s.failed) {
    return s;
  }
  return __State { ...s, failed: true, error: e, last: 0, distance: 0 };
}

function __block(own s: __State, data: [u8], kind: i32): __State {
  if (kind == 0) {
    return __stored(s, data);
  }
  if (kind == 1) {
    return __fixed(s, data);
  }
  if (kind == 2) {
    return __dynamic(s, data);
  }
  return __fail(s, Malformed("a reserved block type"));
}

function __bits(own s: __State, data: [u8], need: i32): __State {
  if (s.failed) {
    return s;
  }
  let val: i32 = s.bitbuf;
  let cnt: i32 = s.bitcnt;
  let pos: i32 = s.pos;
  while (cnt < need) {
    if (pos >= data.len()) {
      return __fail(s, Truncated);
    }
    val = val | data[pos] as i32 << cnt;
    pos = pos + 1;
    cnt = cnt + 8;
  }
  let mask: i32 = (1 << need) - 1;
  return __State { ...s, pos: pos, bitbuf: val >> need, bitcnt: cnt - need, last: val & mask };
}

function __stored(own s: __State, data: [u8]): __State {
  s = __State { ...s, bitbuf: 0, bitcnt: 0 };
  if (s.pos + 4 > data.len()) {
    return __fail(s, Truncated);
  }
  let len: i32 = data[s.pos] as i32 | data[s.pos + 1] as i32 << 8;
  let nlen: i32 = data[s.pos + 2] as i32 | data[s.pos + 3] as i32 << 8;
  if (len != (nlen ^ 65535)) {
    return __fail(s, Malformed("a stored block's length and its complement disagree"));
  }
  let at: i32 = s.pos + 4;
  if (at + len > data.len()) {
    return __fail(s, Truncated);
  }
  if (s.out.len() + len > s.max_out) {
    return __fail(s, OutputLimit);
  }
  let out: u8[] = s.out;
  s = __State { ...s, out: [] };
  let i: i32 = 0;
  while (i < len) {
    out = out.append(data[at + i]);
    i = i + 1;
  }
  return __State { ...s, pos: at + len, out: out };
}
```

### `9 struct __Huff`

```fern
<<9 struct __Huff>>=
struct __Huff { count: i32[], symbol: i32[], left: i32 }
```

### `10 function __construct(lengths: i32[], n: i32): __Huff`

```fern
<<10 function __construct(lengths: i32[], n: i32): __Huff>>=
function __construct(lengths: i32[], n: i32): __Huff {
  let count: i32[] = __zeros(16);
  let sym: i32 = 0;
  while (sym < n) {
    count = count.with(lengths[sym], count[lengths[sym]] + 1);
    sym = sym + 1;
  }
  let left: i32 = 1;
  let len: i32 = 1;
  while (len <= 15) {
    left = left << 1;
    left = left - count[len];
    if (left < 0) {
      return __Huff { count: count, symbol: [], left: 0 - 1 };
    }
    len = len + 1;
  }
  let offs: i32[] = __zeros(16);
  len = 1;
  while (len < 15) {
    offs = offs.with(len + 1, offs[len] + count[len]);
    len = len + 1;
  }
  let symbols: i32[] = __zeros(n);
  sym = 0;
  while (sym < n) {
    if (lengths[sym] != 0) {
      symbols = symbols.with(offs[lengths[sym]], sym);
      offs = offs.with(lengths[sym], offs[lengths[sym]] + 1);
    }
    sym = sym + 1;
  }
  return __Huff { count: count, symbol: symbols, left: left };
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

function __decode(own s: __State, data: [u8], h: __Huff): __State {
  let code: i32 = 0;
  let first: i32 = 0;
  let index: i32 = 0;
  let len: i32 = 1;
  while (len <= 15) {
    s = __bits(s, data, 1);
    if (s.failed) {
      return s;
    }
    code = code | s.last;
    let count: i32 = h.count[len];
    if (code - count < first) {
      return __State { ...s, last: h.symbol[index + (code - first)] };
    }
    index = index + count;
    first = first + count << 1;
    code = code << 1;
    len = len + 1;
  }
  return __fail(s, Malformed("a bit pattern that is no code"));
}
```

### `11 const LENGTH_BASE: i32[]`

```fern
<<11 const LENGTH_BASE: i32[]>>=
const LENGTH_BASE: i32[] = [3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258];

const LENGTH_EXTRA: i32[] = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0];

const DIST_BASE: i32[] = [1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577];

const DIST_EXTRA: i32[] = [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13];

const LIT_BITS: i32 = 10;

const DIST_BITS: i32 = 8;

const LIT_MASK: u64 = 1023u64;

const DIST_MASK: u64 = 255u64;
```

### `12 function __table(h: __Huff, bits: i32, lit: boolean): i32[]`

```fern
<<12 function __table(h: __Huff, bits: i32, lit: boolean): i32[]>>=
// An entry holds the code length in bits 0-3, the extra bit count in bits
// 4-7 and the literal, length base or distance base from bit 16. Zero is a
// code longer than the table, which __walk reads.
const E_LITERAL: i32 = 256;

const E_END: i32 = 512;

const E_NO_CODE: i32 = 1024;

const E_PAST: i32 = 2048;

function __table(h: __Huff, bits: i32, lit: boolean): i32[] {
  let table: i32[] = __zeros(1 << bits);
  let code: i32 = 0;
  let index: i32 = 0;
  let len: i32 = 1;
  while (len <= bits) {
    let k: i32 = 0;
    while (k < h.count[len]) {
      let entry: i32 = __entry(h.symbol[index], len, lit);
      let at: i32 = __reversed(code, len);
      while (at < table.len()) {
        table = table.with(at, entry);
        at = at + (1 << len);
      }
      code = code + 1;
      index = index + 1;
      k = k + 1;
    }
    code = code << 1;
    len = len + 1;
  }
  return table;
}

function __entry(sym: i32, len: i32, lit: boolean): i32 {
  if (!lit) {
    if (sym >= 30) {
      return E_PAST | len;
    }
    return DIST_BASE[sym] << 16 | DIST_EXTRA[sym] << 4 | len;
  }
  if (sym < 256) {
    return sym << 16 | E_LITERAL | len;
  }
  if (sym == 256) {
    return E_END | len;
  }
  if (sym - 257 >= 29) {
    return E_PAST | len;
  }
  return LENGTH_BASE[sym - 257] << 16 | LENGTH_EXTRA[sym - 257] << 4 | len;
}

// The entry of a code longer than the table: zero when `avail` bits do not
// hold it, E_NO_CODE when no code matches.
function __walk(h: __Huff, bits: u64, avail: i32, lit: boolean): i32 {
  let code: i32 = 0;
  let first: i32 = 0;
  let index: i32 = 0;
  let len: i32 = 1;
  while (len <= 15) {
    if (len > avail) {
      return 0;
    }
    code = code | (bits >> (len - 1) as u64 & 1u64) as i32;
    let count: i32 = h.count[len];
    if (code - count < first) {
      return __entry(h.symbol[index + (code - first)], len, lit);
    }
    index = index + count;
    first = first + count << 1;
    code = code << 1;
    len = len + 1;
  }
  return E_NO_CODE;
}

@inline
function __le64(data: [u8], at: i32): u64 {
  return data[at] as u64 | data[at + 1] as u64 << 8 | data[at + 2] as u64 << 16 | data[at + 3] as u64 << 24
    | data[at + 4] as u64 << 32 | data[at + 5] as u64 << 40 | data[at + 6] as u64 << 48 | data[at + 7] as u64 << 56;
}
```

### `13 function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff): __State`

`__fast` runs while eight input bytes and 258 output bytes are left. One
word refill leaves at least 56 bits, and a length with its distance needs at
most 48, so it reads a whole symbol pair with no bit count check. The careful
loop in `__codes` decodes what is left one byte at a time. The two loops are
in separate functions so that the fast one has the registers to itself.

```fern
<<13 function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff): __State>>=
// fern-lint: allow cyclomatic-complexity
function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff): __State {
  let lit: i32[] = __table(lencode, LIT_BITS, true);
  let dtab: i32[] = __table(distcode, DIST_BITS, false);
  let lmask: u64 = (1 << LIT_BITS) as u64 - 1u64;
  let dmask: u64 = (1 << DIST_BITS) as u64 - 1u64;
  let out: u8[] = s.out;
  let max_out: i32 = s.max_out;
  let pos: i32 = s.pos;
  let bitbuf: u64 = s.bitbuf as u64;
  let bitcnt: i32 = s.bitcnt;
  let n: i32 = data.len();
  s = __State { ...s, out: [], lengths: [] };
  let error: Option[InflateError] = None;
  let done: boolean = false;
  let fast: __Fast = __fast(out, data, pos, bitbuf, bitcnt, max_out, lit, dtab, lencode, distcode);
  out = fast.out;
  pos = fast.pos;
  bitbuf = fast.bitbuf;
  bitcnt = fast.bitcnt;
  if (fast.status != F_ROOM) {
    error = __fast_error(fast.status);
    done = true;
  }
  while (!done) {
    while (bitcnt <= 48 && pos < n) {
      bitbuf = bitbuf | data[pos] as u64 << bitcnt as u64;
      pos = pos + 1;
      bitcnt = bitcnt + 8;
    }
    let e: i32 = lit[(bitbuf & lmask) as i32];
    if (e == 0) {
      e = __walk(lencode, bitbuf, bitcnt, true);
    }
    let used: i32 = e & 15;
    if (e == 0 || used > bitcnt) {
      error = Some(Truncated);
      done = true;
    } else {
      bitbuf = bitbuf >> used as u64;
      bitcnt = bitcnt - used;
      if ((e & E_LITERAL) != 0) {
        if (out.len() >= max_out) {
          error = Some(OutputLimit);
          done = true;
        } else {
          out = out.append((e >> 16) as u8);
        }
      } else if ((e & (E_END | E_NO_CODE | E_PAST)) != 0) {
        error = __symbol_error(e, true);
        done = true;
      } else {
        let lx: i32 = e >> 4 & 15;
        let len: i32 = (e >> 16) + (bitbuf & (1u64 << lx as u64) - 1u64) as i32;
        if (lx > bitcnt) {
          error = Some(Truncated);
          done = true;
        } else {
          bitbuf = bitbuf >> lx as u64;
          bitcnt = bitcnt - lx;
          while (bitcnt <= 48 && pos < n) {
            bitbuf = bitbuf | data[pos] as u64 << bitcnt as u64;
            pos = pos + 1;
            bitcnt = bitcnt + 8;
          }
          let d: i32 = dtab[(bitbuf & dmask) as i32];
          if (d == 0) {
            d = __walk(distcode, bitbuf, bitcnt, false);
          }
          let dused: i32 = d & 15;
          if (d == 0 || dused > bitcnt) {
            error = Some(Truncated);
            done = true;
          } else {
            bitbuf = bitbuf >> dused as u64;
            bitcnt = bitcnt - dused;
            let dx: i32 = d >> 4 & 15;
            if ((d & (E_NO_CODE | E_PAST)) != 0) {
              error = __symbol_error(d, false);
              done = true;
            } else if (dx > bitcnt) {
              error = Some(Truncated);
              done = true;
            } else {
              let dist: i32 = (d >> 16) + (bitbuf & (1u64 << dx as u64) - 1u64) as i32;
              bitbuf = bitbuf >> dx as u64;
              bitcnt = bitcnt - dx;
              if (dist > out.len()) {
                error = Some(Malformed("a distance before the start of the output"));
                done = true;
              } else if (out.len() + len > max_out) {
                error = Some(OutputLimit);
                done = true;
              } else {
                out = __copy(out, len, dist);
              }
            }
          }
        }
      }
    }
  }
  let back: i32 = bitcnt >> 3;
  bitcnt = bitcnt & 7;
  s = __State { ...s, out: out, pos: pos - back, bitbuf: (bitbuf & (1u64 << bitcnt as u64) - 1u64) as i32, bitcnt: bitcnt };
  match (error) {
    Some(err) => {
      return __fail(s, err);
    },
    None => {
      return s;
    }
  }
  return s;
}

struct __Fast { out: u8[], pos: i32, bitbuf: u64, bitcnt: i32, status: i32 }

const F_ROOM: i32 = 0;

const F_END: i32 = 1;

const F_NO_CODE: i32 = 2;

const F_LENGTH_PAST: i32 = 3;

const F_DISTANCE_PAST: i32 = 4;

const F_TOO_FAR: i32 = 5;

// Decodes while eight input bytes and 258 output bytes are left, and stops
// with F_ROOM when they are not. After a literal at least 41 bits are left,
// so the next entry is read before the refill, and a second literal is
// decoded with no refill. After a match `e` is -1 and is read after it. The guard at the top is what lets the
// bounds prover drop the checks on the input words and the two tables.
function __fast(own out: u8[], data: [u8], pos: i32, bitbuf: u64, bitcnt: i32, max_out: i32, lit: i32[], dtab: i32[], lencode: __Huff, distcode: __Huff): __Fast {
  let fast_in: i32 = data.len() - 8;
  let fast_out: i32 = max_out - 258;
  let status: i32 = F_ROOM;
  if (pos < 0 || lit.len() < 1 << LIT_BITS || dtab.len() < 1 << DIST_BITS) {
    return __Fast { out: out, pos: pos, bitbuf: bitbuf, bitcnt: bitcnt, status: status };
  }
  let e: i32 = 0 - 1;
  while (pos <= fast_in && out.len() <= fast_out) {
    bitbuf = bitbuf | __le64(data, pos) << bitcnt as u64;
    pos = pos + ((bitcnt >> 3 ^ 7) & 7);
    bitcnt = bitcnt | 56;
    if (e < 0) {
      e = lit[(bitbuf & LIT_MASK) as i32];
    }
    if (e == 0) {
      e = __walk(lencode, bitbuf, bitcnt, true);
    }
    let used: i32 = e & 15;
    bitbuf = bitbuf >> used as u64;
    bitcnt = bitcnt - used;
    if ((e & E_LITERAL) != 0) {
      out = out.append((e >> 16) as u8);
      e = lit[(bitbuf & LIT_MASK) as i32];
      if ((e & E_LITERAL) != 0) {
        bitbuf = bitbuf >> (e & 15) as u64;
        bitcnt = bitcnt - (e & 15);
        out = out.append((e >> 16) as u8);
        e = lit[(bitbuf & LIT_MASK) as i32];
      }
      continue;
    }
    if ((e & (E_END | E_NO_CODE | E_PAST)) != 0) {
      status = __fast_status(e, F_LENGTH_PAST);
      break;
    }
    let lx: i32 = e >> 4 & 15;
    let len: i32 = (e >> 16) + (bitbuf & (1u64 << lx as u64) - 1u64) as i32;
    bitbuf = bitbuf >> lx as u64;
    bitcnt = bitcnt - lx;
    let d: i32 = dtab[(bitbuf & DIST_MASK) as i32];
    if (d == 0) {
      d = __walk(distcode, bitbuf, bitcnt, false);
    }
    let dused: i32 = d & 15;
    bitbuf = bitbuf >> dused as u64;
    bitcnt = bitcnt - dused;
    if ((d & (E_NO_CODE | E_PAST)) != 0) {
      status = __fast_status(d, F_DISTANCE_PAST);
      break;
    }
    let dx: i32 = d >> 4 & 15;
    let dist: i32 = (d >> 16) + (bitbuf & (1u64 << dx as u64) - 1u64) as i32;
    bitbuf = bitbuf >> dx as u64;
    bitcnt = bitcnt - dx;
    if (dist > out.len()) {
      status = F_TOO_FAR;
      break;
    }
    out = __copy(out, len, dist);
    e = 0 - 1;
  }
  return __Fast { out: out, pos: pos, bitbuf: bitbuf, bitcnt: bitcnt, status: status };
}

function __fast_status(e: i32, past: i32): i32 {
  if ((e & E_END) != 0) {
    return F_END;
  }
  if ((e & E_NO_CODE) != 0) {
    return F_NO_CODE;
  }
  return past;
}

function __fast_error(status: i32): Option[InflateError] {
  if (status == F_NO_CODE) {
    return Some(Malformed("a bit pattern that is no code"));
  }
  if (status == F_LENGTH_PAST) {
    return Some(Malformed("a length code past 285"));
  }
  if (status == F_DISTANCE_PAST) {
    return Some(Malformed("a distance code past 29"));
  }
  if (status == F_TOO_FAR) {
    return Some(Malformed("a distance before the start of the output"));
  }
  return None;
}

// None for the end-of-block code, the error for the others.
function __symbol_error(e: i32, lit: boolean): Option[InflateError] {
  if ((e & E_END) != 0) {
    return None;
  }
  if ((e & E_NO_CODE) != 0) {
    return Some(Malformed("a bit pattern that is no code"));
  }
  if (lit) {
    return Some(Malformed("a length code past 285"));
  }
  return Some(Malformed("a distance code past 29"));
}

function __copy(own out: u8[], len: i32, dist: i32): u8[] {
  let i: i32 = 0;
  while (i < len) {
    out = out.append(out[out.len() - dist]);
    i = i + 1;
  }
  return out;
}
```

### `14 function __fixed(own s: __State, data: [u8]): __State`

```fern
<<14 function __fixed(own s: __State, data: [u8]): __State>>=
function __fixed(own s: __State, data: [u8]): __State {
  let lengths: i32[] = [];
  let sym: i32 = 0;
  while (sym < 288) {
    lengths = lengths.append(__fixed_length(sym));
    sym = sym + 1;
  }
  let dlengths: i32[] = [];
  sym = 0;
  while (sym < 30) {
    dlengths = dlengths.append(5);
    sym = sym + 1;
  }
  return __codes(s, data, __construct(lengths, 288), __construct(dlengths, 30));
}

function __fixed_length(sym: i32): i32 {
  if (sym < 144) {
    return 8;
  }
  if (sym < 256) {
    return 9;
  }
  if (sym < 280) {
    return 7;
  }
  return 8;
}

function __code_length_order(): i32[] {
  return [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];
}

function __dynamic(own s: __State, data: [u8]): __State {
  s = __bits(s, data, 5);
  let nlen: i32 = s.last + 257;
  s = __bits(s, data, 5);
  let ndist: i32 = s.last + 1;
  s = __bits(s, data, 4);
  let ncode: i32 = s.last + 4;
  if (nlen > 286 || ndist > 30) {
    return __fail(s, Malformed("too many length or distance codes"));
  }
  let order: i32[] = __code_length_order();
  let lengths: i32[] = __zeros(19);
  let i: i32 = 0;
  while (i < ncode) {
    s = __bits(s, data, 3);
    lengths = lengths.with(order[i], s.last);
    i = i + 1;
  }
  let lencode: __Huff = __construct(lengths, 19);
  if (lencode.left != 0) {
    return __fail(s, Malformed("an incomplete or over-subscribed code length code"));
  }
  s = __code_lengths(s, data, lencode, nlen + ndist);
  if (s.failed) {
    return s;
  }
  return __dynamic_codes(s, data, nlen, ndist);
}

function __dynamic_codes(own s: __State, data: [u8], nlen: i32, ndist: i32): __State {
  let all: i32[] = s.lengths;
  if (all[256] == 0) {
    return __fail(s, Malformed("no end-of-block code"));
  }
  let lit: __Huff = __construct(all, nlen);
  if (lit.left < 0 || lit.left > 0 && nlen - lit.count[0] != 1) {
    return __fail(s, Malformed("an incomplete or over-subscribed literal/length code"));
  }
  let dist: __Huff = __construct(__tail(all, nlen, nlen + ndist), ndist);
  if (dist.left < 0 || dist.left > 0 && ndist - dist.count[0] != 1) {
    return __fail(s, Malformed("an incomplete or over-subscribed distance code"));
  }
  return __codes(s, data, lit, dist);
}

function __tail(xs: i32[], from: i32, end: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = from;
  while (i < end) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

function __code_lengths(own s: __State, data: [u8], code: __Huff, n: i32): __State {
  let lengths: i32[] = [];
  while (lengths.len() < n && !s.failed) {
    s = __decode(s, data, code);
    let sym: i32 = s.last;
    if (sym < 16) {
      lengths = lengths.append(sym);
    } else {
      s = __repeat(s, data, sym, lengths);
      if (lengths.len() + s.last > n) {
        return __fail(s, Malformed("more code lengths than codes"));
      }
      let value: i32 = __repeat_value(sym, lengths);
      lengths = __repeated(lengths, s.last, value);
    }
  }
  return __State { ...s, lengths: lengths };
}

function __repeat(own s: __State, data: [u8], sym: i32, lengths: i32[]): __State {
  if (sym == 16) {
    if (lengths.len() == 0) {
      return __fail(s, Malformed("a repeat with no length before it"));
    }
    s = __bits(s, data, 2);
    return __State { ...s, last: 3 + s.last };
  }
  if (sym == 17) {
    s = __bits(s, data, 3);
    return __State { ...s, last: 3 + s.last };
  }
  s = __bits(s, data, 7);
  return __State { ...s, last: 11 + s.last };
}

function __repeat_value(sym: i32, lengths: i32[]): i32 {
  if (sym == 16 && lengths.len() > 0) {
    return lengths[lengths.len() - 1];
  }
  return 0;
}
```

### `15 function __repeated(own lengths: i32[], times: i32, value: i32): i32[]`

```fern
<<15 function __repeated(own lengths: i32[], times: i32, value: i32): i32[]>>=
function __repeated(own lengths: i32[], times: i32, value: i32): i32[] {
  let k: i32 = 0;
  while (k < times) {
    lengths = lengths.append(value);
    k = k + 1;
  }
  return lengths;
}
```

### `16 pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError]`

```fern
<<16 pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError]>>=
pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError] {
  let out: u8[] = [];
  let at: i32 = 0;
  while (at < input.len()) {
    let member: Inflated = __gzip_member(input, at, max_out - out.len())?;
    out = __joined(out, member.out);
    at = at + member.consumed;
  }
  if (input.len() == 0) {
    return Err(BadHeader("empty"));
  }
  return Ok(out);
}
```

### `17 function __joined(own out: u8[], more: [u8]): u8[]`

```fern
<<17 function __joined(own out: u8[], more: [u8]): u8[]>>=
function __joined(own out: u8[], more: [u8]): u8[] {
  let i: i32 = 0;
  while (i < more.len()) {
    out = out.append(more[i]);
    i = i + 1;
  }
  return out;
}

function __gzip_member(input: [u8], from: i32, max_out: i32): Result[Inflated, InflateError] {
  let body: i32 = __gzip_header(input, from)?;
  let inflated: Inflated = inflate_from(input, body, max_out)?;
  let trailer: i32 = body + inflated.consumed;
  if (trailer + 8 > input.len()) {
    return Err(Truncated);
  }
  let crc: u32 = __le32(input, trailer);
  let isize: u32 = __le32(input, trailer + 4);
  if (crc != hash.crc32_new().update(inflated.out[:]).finish() || isize != inflated.out.len() as u32) {
    return Err(BadChecksum);
  }
  return Ok(Inflated { out: inflated.out, consumed: trailer + 8 - from });
}

function __gzip_header(input: [u8], from: i32): Result[i32, InflateError] {
  if (from + 2 > input.len()) {
    return Err(Truncated);
  }
  if (input[from] != 31 as u8 || input[from + 1] != 139 as u8) {
    return Err(BadHeader("not gzip"));
  }
  if (from + 10 > input.len()) {
    return Err(Truncated);
  }
  if (input[from + 2] != 8 as u8) {
    return Err(BadHeader("a gzip compression method other than deflate"));
  }
  let flags: i32 = input[from + 3] as i32;
  if ((flags & 224) != 0) {
    return Err(BadHeader("reserved gzip flags"));
  }
  return __gzip_optional(input, from + 10, flags);
}

function __gzip_optional(input: [u8], at: i32, flags: i32): Result[i32, InflateError] {
  if ((flags & 4) != 0) {
    if (at + 2 > input.len()) {
      return Err(Truncated);
    }
    at = at + 2 + (input[at] as i32 | input[at + 1] as i32 << 8);
  }
  if ((flags & 8) != 0) {
    at = __past_nul(input, at)?;
  }
  if ((flags & 16) != 0) {
    at = __past_nul(input, at)?;
  }
  if ((flags & 2) != 0) {
    at = at + 2;
  }
  if (at > input.len()) {
    return Err(Truncated);
  }
  return Ok(at);
}

function __past_nul(input: [u8], at: i32): Result[i32, InflateError] {
  let i: i32 = at;
  while (i < input.len()) {
    if (input[i] == 0 as u8) {
      return Ok(i + 1);
    }
    i = i + 1;
  }
  return Err(Truncated);
}

function __le32(input: [u8], at: i32): u32 {
  return input[at] as u32 | input[at + 1] as u32 << 8 | input[at + 2] as u32 << 16 | input[at + 3] as u32 << 24;
}
```

### `18 pub function zlib_decode(input: [u8], max_out: i32): Result[u8[], InflateError]`

```fern
<<18 pub function zlib_decode(input: [u8], max_out: i32): Result[u8[], InflateError]>>=
pub function zlib_decode(input: [u8], max_out: i32): Result[u8[], InflateError] {
  if (input.len() < 2) {
    return Err(Truncated);
  }
  let cmf: i32 = input[0] as i32;
  let flg: i32 = input[1] as i32;
  if ((cmf & 15) != 8 || cmf >> 4 > 7) {
    return Err(BadHeader("a zlib compression method or window this decoder does not read"));
  }
  if ((cmf * 256 + flg) % 31 != 0) {
    return Err(BadHeader("a zlib header whose check fails"));
  }
  if ((flg & 32) != 0) {
    return Err(BadHeader("a zlib preset dictionary"));
  }
  let inflated: Inflated = inflate_from(input, 2, max_out)?;
  let trailer: i32 = 2 + inflated.consumed;
  if (trailer + 4 > input.len()) {
    return Err(Truncated);
  }
  let want: u32 = input[trailer] as u32 << 24 | input[trailer + 1] as u32 << 16 | input[trailer + 2] as u32 << 8 | input[trailer + 3] as u32;
  if (want != adler32(inflated.out)) {
    return Err(BadChecksum);
  }
  return Ok(inflated.out);
}

pub function adler32(bs: [u8]): u32 {
  let a: u32 = 1;
  let b: u32 = 0;
  let i: i32 = 0;
  while (i < bs.len()) {
    a = (a + bs[i] as u32) % 65521;
    b = (b + a) % 65521;
    i = i + 1;
  }
  return b << 16 | a;
}

pub function deflate(input: [u8]): u8[] {
  return __deflated(input, 15, true);
}

pub function deflate_fast(input: [u8]): u8[] {
  return __deflated_tokens(input, __fast_tokens(input), true);
}

pub function gzip(input: [u8]): u8[] {
  let crc: u32 = hash.crc32_new().update(input).finish();
  let head: u8[] = [31 as u8, 139 as u8, 8 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 255 as u8];
  return __put_le32(__put_le32(__joined(head, deflate(input)), crc), input.len() as u32);
}
```

### `19 function __put_le32(own out: u8[], v: u32): u8[]`

```fern
<<19 function __put_le32(own out: u8[], v: u32): u8[]>>=
function __put_le32(own out: u8[], v: u32): u8[] {
  return out.append((v & 255) as u8).append((v >> 8 & 255) as u8).append((v >> 16 & 255) as u8).append((v >> 24) as u8);
}
```

### `20 pub function deflate_flushed(input: [u8], window_bits: i32): u8[]`

```fern
<<20 pub function deflate_flushed(input: [u8], window_bits: i32): u8[]>>=
pub function deflate_flushed(input: [u8], window_bits: i32): u8[] {
  return __deflated(input, window_bits, false);
}
```

### `21 struct __Bits`

```fern
<<21 struct __Bits>>=
struct __Bits { out: u8[], acc: i32, n: i32 }
```

### `22 function __put(w: __Bits, value: i32, bits: i32): __Bits`

```fern
<<22 function __put(w: __Bits, value: i32, bits: i32): __Bits>>=
function __put(w: __Bits, value: i32, bits: i32): __Bits {
  let acc: i32 = w.acc | value << w.n;
  let n: i32 = w.n + bits;
  let out: u8[] = w.out;
  while (n >= 8) {
    out = out.append((acc & 255) as u8);
    acc = acc >> 8;
    n = n - 8;
  }
  return __Bits { out: out, acc: acc, n: n };
}

function __flush_byte(w: __Bits): __Bits {
  if (w.n == 0) {
    return w;
  }
  return __Bits { out: w.out.append((w.acc & 255) as u8), acc: 0, n: 0 };
}
```

### `23 struct __Code`

```fern
<<23 struct __Code>>=
struct __Code { lens: i32[], codes: i32[] }
```

### `24 function __put_sym(w: __Bits, c: __Code, sym: i32): __Bits`

```fern
<<24 function __put_sym(w: __Bits, c: __Code, sym: i32): __Bits>>=
function __put_sym(w: __Bits, c: __Code, sym: i32): __Bits {
  return __put(w, c.codes[sym], c.lens[sym]);
}

function __canonical(lens: i32[]): __Code {
  let count: i32[] = __filled(16, 0);
  for l in lens {
    if (l > 0) {
      count = __bump(count, l);
    }
  }
  let next: i32[] = __filled(16, 0);
  let code: i32 = 0;
  let b: i32 = 1;
  while (b < 16) {
    code = code + count[b - 1] << 1;
    next = next.with(b, code);
    b = b + 1;
  }
  let codes: i32[] = [];
  for l in lens {
    codes = codes.append(__reversed(next[l], l));
    next = __bump(next, l);
  }
  return __Code { lens: lens, codes: codes };
}

function __reversed(code: i32, bits: i32): i32 {
  let rev: i32 = 0;
  let i: i32 = 0;
  while (i < bits) {
    rev = rev << 1 | code >> i & 1;
    i = i + 1;
  }
  return rev;
}

function __bump(own xs: i32[], i: i32): i32[] {
  return xs.with(i, xs[i] + 1);
}

function __fixed_lit(): __Code {
  let lens: i32[] = [];
  let s: i32 = 0;
  while (s < 288) {
    lens = lens.append(__fixed_len(s));
    s = s + 1;
  }
  return __canonical(lens);
}

function __fixed_len(sym: i32): i32 {
  if (sym < 144) {
    return 8;
  }
  if (sym < 256) {
    return 9;
  }
  if (sym < 280) {
    return 7;
  }
  return 8;
}

function __fixed_dist(): __Code {
  return __canonical(__filled(30, 5));
}

function __len_sym(len: i32): i32 {
  if (len == 258) {
    return 28;
  }
  let x: i32 = len - 3;
  if (x < 8) {
    return x;
  }
  let k: i32 = __log2(x);
  return 4 * (k - 1) + (x >> k - 2 & 3);
}

function __len_base_of(li: i32): i32 {
  if (li < 8) {
    return 3 + li;
  }
  if (li == 28) {
    return 258;
  }
  return 3 + ((4 | li & 3) << (li >> 2) - 1);
}

function __len_extra_of(li: i32): i32 {
  if (li < 8 || li == 28) {
    return 0;
  }
  return (li >> 2) - 1;
}

function __dist_sym(d: i32): i32 {
  let x: i32 = d - 1;
  if (x < 4) {
    return x;
  }
  let k: i32 = __log2(x);
  return 2 * k + (x >> k - 1 & 1);
}

function __dist_base_of(di: i32): i32 {
  if (di < 4) {
    return di + 1;
  }
  return 1 + ((2 | di & 1) << (di >> 1) - 1);
}

function __dist_extra_of(di: i32): i32 {
  if (di < 4) {
    return 0;
  }
  return (di >> 1) - 1;
}

function __log2(x: i32): i32 {
  let k: i32 = 0;
  while (x > 1) {
    x = x >> 1;
    k = k + 1;
  }
  return k;
}
```

### `25 function __put_match(w: __Bits, length: i32, distance: i32, lit: __Code, dist: __Code): __Bits`

```fern
<<25 function __put_match(w: __Bits, length: i32, distance: i32, lit: __Code, dist: __Code): __Bits>>=
function __put_match(w: __Bits, length: i32, distance: i32, lit: __Code, dist: __Code): __Bits {
  let li: i32 = __len_sym(length);
  w = __put_sym(w, lit, 257 + li);
  w = __put(w, length - __len_base_of(li), __len_extra_of(li));
  let di: i32 = __dist_sym(distance);
  w = __put_sym(w, dist, di);
  return __put(w, distance - __dist_base_of(di), __dist_extra_of(di));
}

function __deflated(input: [u8], window_bits: i32, final: boolean): u8[] {
  return __deflated_tokens(input, __tokens(input, 1 << window_bits), final);
}

function __deflated_tokens(input: [u8], toks: i32[], final: boolean): u8[] {
  let fixed: (__Code, __Code) = (__fixed_lit(), __fixed_dist());
  let empty: u8[] = [];
  let w: __Bits = __Bits { out: empty, acc: 0, n: 0 };
  let s: __Span = __Span { from: 0, len: 0, at: 0, end: 0, last: false };
  while (s.end < toks.len() || s.at == 0 && s.end == 0) {
    let end: i32 = s.end + 16384;
    if (end > toks.len()) {
      end = toks.len();
    }
    s = __Span { from: s.from + s.len, len: __span(toks, s.end, end), at: s.end, end: end, last: final && end == toks.len() };
    w = __put_block(w, input, toks, s, fixed);
    if (end == 0) {
      s = __Span { ...s, at: 1 };
    }
  }
  if (final) {
    return __flush_byte(w).out;
  }
  w = __put(w, 0, 3);
  w = __flush_byte(w);
  return w.out.append(0 as u8).append(0 as u8).append(255 as u8).append(255 as u8);
}
```

### `26 struct __Span`

```fern
<<26 struct __Span>>=
struct __Span { from: i32, len: i32, at: i32, end: i32, last: boolean }
```

### `27 function __span(toks: i32[], at: i32, end: i32): i32`

```fern
<<27 function __span(toks: i32[], at: i32, end: i32): i32>>=
function __span(toks: i32[], at: i32, end: i32): i32 {
  let n: i32 = 0;
  let i: i32 = at;
  while (i < end) {
    if (toks[i] < 256) {
      n = n + 1;
    } else {
      n = n + (toks[i] >> 16);
    }
    i = i + 1;
  }
  return n;
}

function __put_block(w: __Bits, input: [u8], toks: i32[], s: __Span, fixed: (__Code, __Code)): __Bits {
  let f: (i32[], i32[]) = __freqs(toks, s.at, s.end);
  let lit: __Code = __canonical(huffman_lengths(__two_used(f.0), 15));
  let dist: __Code = __canonical(huffman_lengths(__two_used(f.1), 15));
  let h: __Header = __header(lit.lens, dist.lens);
  let extra: i32 = __extra_cost(f.0, f.1);
  let own_cost: i32 = h.bits + __weighted(f.0, lit.lens) + __weighted(f.1, dist.lens);
  let fixed_cost: i32 = __weighted(f.0, fixed.0.lens) + __weighted(f.1, fixed.1.lens);
  if (__stored_cost(s.len) < 3 + extra + cmp.min(own_cost, fixed_cost)) {
    return __put_stored(w, input, s);
  }
  let fin: i32 = 0;
  if (s.last) {
    fin = 1;
  }
  w = __put(w, fin, 1);
  if (own_cost < fixed_cost) {
    w = __put_header(__put(w, 2, 2), h);
    return __put_tokens(w, toks, s, lit, dist);
  }
  return __put_tokens(__put(w, 1, 2), toks, s, fixed.0, fixed.1);
}

function __freqs(toks: i32[], at: i32, end: i32): (i32[], i32[]) {
  let lit: i32[] = __filled(286, 0);
  let dist: i32[] = __filled(30, 0);
  let i: i32 = at;
  while (i < end) {
    let t: i32 = toks[i];
    if (t < 256) {
      lit = lit.with(t, lit[t] + 1);
    } else {
      let ls: i32 = 257 + __len_sym(t >> 16);
      let ds: i32 = __dist_sym(t & 65535);
      lit = lit.with(ls, lit[ls] + 1);
      dist = dist.with(ds, dist[ds] + 1);
    }
    i = i + 1;
  }
  return (__bump(lit, 256), dist);
}

function __extra_cost(lit: i32[], dist: i32[]): i32 {
  let bits: i32 = 0;
  let i: i32 = 0;
  while (i < 29) {
    bits = bits + lit[257 + i] * __len_extra_of(i);
    i = i + 1;
  }
  i = 0;
  while (i < 30) {
    bits = bits + dist[i] * __dist_extra_of(i);
    i = i + 1;
  }
  return bits;
}

function __weighted(freq: i32[], lens: i32[]): i32 {
  let bits: i32 = 0;
  let i: i32 = 0;
  while (i < freq.len()) {
    bits = bits + freq[i] * lens[i];
    i = i + 1;
  }
  return bits;
}

function __stored_cost(len: i32): i32 {
  return (len / 65535 + 1) * 42 + len * 8;
}

function __put_stored(w: __Bits, input: [u8], s: __Span): __Bits {
  let at: i32 = s.from;
  let end: i32 = s.from + s.len;
  let first: boolean = true;
  while (first || at < end) {
    let n: i32 = cmp.min(end - at, 65535);
    let fin: i32 = 0;
    if (s.last && at + n == end) {
      fin = 1;
    }
    w = __flush_byte(__put(__put(w, fin, 1), 0, 2));
    w = __put(__put(w, n, 16), n ^ 65535, 16);
    w = __Bits { ...w, out: __joined_range(w.out, input, at, at + n) };
    at = at + n;
    first = false;
  }
  return w;
}

function __joined_range(out: u8[], more: [u8], from: i32, to: i32): u8[] {
  let i: i32 = from;
  while (i < to) {
    out = out.append(more[i]);
    i = i + 1;
  }
  return out;
}

function __put_tokens(w: __Bits, toks: i32[], s: __Span, lit: __Code, dist: __Code): __Bits {
  let i: i32 = s.at;
  while (i < s.end) {
    let t: i32 = toks[i];
    if (t < 256) {
      w = __put_sym(w, lit, t);
    } else {
      w = __put_match(w, t >> 16, t & 65535, lit, dist);
    }
    i = i + 1;
  }
  return __put_sym(w, lit, 256);
}
```

### `28 function __two_used(freq: i32[]): i32[]`

```fern
<<28 function __two_used(freq: i32[]): i32[]>>=
function __two_used(freq: i32[]): i32[] {
  let used: i32 = 0;
  for f in freq {
    if (f > 0) {
      used = used + 1;
    }
  }
  let s: i32 = 0;
  while (used < 2) {
    if (freq[s] == 0) {
      freq = freq.with(s, 1);
      used = used + 1;
    }
    s = s + 1;
  }
  return freq;
}
```

### `29 pub function huffman_lengths(freq: i32[], limit: i32): i32[]`

```fern
<<29 pub function huffman_lengths(freq: i32[], limit: i32): i32[]>>=
pub function huffman_lengths(freq: i32[], limit: i32): i32[] {
  let keys: i32[] = [];
  let s: i32 = 0;
  while (s < freq.len()) {
    if (freq[s] > 0) {
      keys = keys.append(freq[s] << 9 | s);
    }
    s = s + 1;
  }
  keys = cmp.sort(keys);
  let weights: i32[] = [];
  for k in keys {
    weights = weights.append(k >> 9);
  }
  return __assigned(keys, __limited(__moffat(weights), limit), freq.len());
}
```

### `30 function __assigned(keys: i32[], counts: i32[], size: i32): i32[]`

```fern
<<30 function __assigned(keys: i32[], counts: i32[], size: i32): i32[]>>=
function __assigned(keys: i32[], counts: i32[], size: i32): i32[] {
  let lens: i32[] = __filled(size, 0);
  let j: i32 = keys.len();
  let l: i32 = 1;
  while (l < counts.len()) {
    let c: i32 = counts[l];
    while (c > 0) {
      j = j - 1;
      lens = lens.with(keys[j] & 511, l);
      c = c - 1;
    }
    l = l + 1;
  }
  return lens;
}

function __moffat(own a: i32[]): i32[] {
  let n: i32 = a.len();
  if (n == 1) {
    return a.with(0, 1);
  }
  if (n == 0) {
    return a;
  }
  return __moffat_depths(__moffat_parents(a));
}

function __moffat_parents(own a: i32[]): i32[] {
  let n: i32 = a.len();
  a = a.with(0, a[0] + a[1]);
  let root: i32 = 0;
  let leaf: i32 = 2;
  let next: i32 = 1;
  while (next < n - 1) {
    if (leaf >= n || a[root] < a[leaf]) {
      a = a.with(next, a[root]).with(root, next);
      root = root + 1;
    } else {
      a = a.with(next, a[leaf]);
      leaf = leaf + 1;
    }
    if (leaf >= n || root < next && a[root] < a[leaf]) {
      a = a.with(next, a[next] + a[root]).with(root, next);
      root = root + 1;
    } else {
      a = a.with(next, a[next] + a[leaf]);
      leaf = leaf + 1;
    }
    next = next + 1;
  }
  return a;
}

function __moffat_depths(own a: i32[]): i32[] {
  let n: i32 = a.len();
  a = a.with(n - 2, 0);
  let next: i32 = n - 3;
  while (next >= 0) {
    a = a.with(next, a[a[next]] + 1);
    next = next - 1;
  }
  let avail: i32 = 1;
  let depth: i32 = 0;
  let root: i32 = n - 2;
  next = n - 1;
  while (avail > 0) {
    let used: i32 = 0;
    while (root >= 0 && a[root] == depth) {
      used = used + 1;
      root = root - 1;
    }
    while (avail > used) {
      a = a.with(next, depth);
      next = next - 1;
      avail = avail - 1;
    }
    avail = 2 * used;
    depth = depth + 1;
  }
  return a;
}

function __limited(depths: i32[], limit: i32): i32[] {
  let counts: i32[] = __filled(limit + 1, 0);
  for d in depths {
    counts = __bump(counts, cmp.min(d, limit));
  }
  let total: i32 = 0;
  let l: i32 = 1;
  while (l <= limit) {
    total = total + (counts[l] << limit - l);
    l = l + 1;
  }
  while (total > 1 << limit) {
    counts = counts.with(limit, counts[limit] - 1);
    let i: i32 = limit - 1;
    while (counts[i] == 0) {
      i = i - 1;
    }
    counts = counts.with(i, counts[i] - 1).with(i + 1, counts[i + 1] + 2);
    total = total - 1;
  }
  return counts;
}
```

### `31 struct __Header`

```fern
<<31 struct __Header>>=
struct __Header { hlit: i32, hdist: i32, rle: i32[], cl: __Code, hclen: i32, bits: i32 }
```

### `32 function __cl_order(): i32[]`

```fern
<<32 function __cl_order(): i32[]>>=
function __cl_order(): i32[] {
  return [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];
}

function __header(lit: i32[], dist: i32[]): __Header {
  let hlit: i32 = __sent(lit, 257);
  let hdist: i32 = __sent(dist, 1);
  let lens: i32[] = array.slice(lit, 0, hlit).concat(array.slice(dist, 0, hdist));
  let rle: i32[] = __rle(lens);
  let freq: i32[] = __filled(19, 0);
  for r in rle {
    freq = __bump(freq, r & 31);
  }
  let cl: __Code = __canonical(huffman_lengths(__two_used(freq), 7));
  let order: i32[] = __cl_order();
  let hclen: i32 = 19;
  while (hclen > 4 && cl.lens[order[hclen - 1]] == 0) {
    hclen = hclen - 1;
  }
  let bits: i32 = 14 + 3 * hclen;
  for r in rle {
    bits = bits + cl.lens[r & 31] + __cl_extra(r & 31);
  }
  return __Header { hlit: hlit, hdist: hdist, rle: rle, cl: cl, hclen: hclen, bits: bits };
}

function __sent(lens: i32[], least: i32): i32 {
  let n: i32 = lens.len();
  while (n > least && lens[n - 1] == 0) {
    n = n - 1;
  }
  return n;
}

function __cl_extra(sym: i32): i32 {
  if (sym == 16) {
    return 2;
  }
  if (sym == 17) {
    return 3;
  }
  if (sym == 18) {
    return 7;
  }
  return 0;
}

function __rle(lens: i32[]): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < lens.len()) {
    let step: (i32, i32) = __rle_step(lens, i);
    out = out.append(step.0);
    i = i + step.1;
  }
  return out;
}

function __rle_step(lens: i32[], i: i32): (i32, i32) {
  let v: i32 = lens[i];
  let run: i32 = __run(lens, i);
  if (v == 0 && run >= 11) {
    return (18 | run - 11 << 5, run);
  }
  if (v == 0 && run >= 3) {
    let r: i32 = cmp.min(run, 10);
    return (17 | r - 3 << 5, r);
  }
  if (v != 0 && i > 0 && lens[i - 1] == v && run >= 3) {
    let r: i32 = cmp.min(run, 6);
    return (16 | r - 3 << 5, r);
  }
  return (v, 1);
}

function __run(lens: i32[], i: i32): i32 {
  let run: i32 = 1;
  while (run < 138 && i + run < lens.len() && lens[i + run] == lens[i]) {
    run = run + 1;
  }
  return run;
}

function __put_header(w: __Bits, h: __Header): __Bits {
  w = __put(w, h.hlit - 257, 5);
  w = __put(w, h.hdist - 1, 5);
  w = __put(w, h.hclen - 4, 4);
  let order: i32[] = __cl_order();
  let k: i32 = 0;
  while (k < h.hclen) {
    w = __put(w, h.cl.lens[order[k]], 3);
    k = k + 1;
  }
  for r in h.rle {
    w = __put(__put_sym(w, h.cl, r & 31), r >> 5, __cl_extra(r & 31));
  }
  return w;
}

function __tokens(input: [u8], window: i32): i32[] {
  let n: i32 = input.len();
  let head: i32[] = __filled(32768, 0 - 1);
  let prev: i32[] = __filled(window, 0 - 1);
  let out: i32[] = [];
  let held: (i32, i32) = (0 - 1, 0);
  let skip: i32 = 0;
  let pos: i32 = 0;
  while (pos < n) {
    let cur: (i32, i32) = __match_at(input, pos, skip, head, prev, window);
    if (pos + 2 < n) {
      let h: i32 = __hash3(input, pos);
      prev = prev.with(pos & window - 1, head[h]);
      head = head.with(h, pos);
    }
    if (pos >= skip && held.0 >= 3 && cur.0 <= held.0) {
      out = out.append(held.0 << 16 | held.1);
      skip = pos - 1 + held.0;
      held = (0 - 1, 0);
    } else if (pos >= skip) {
      if (held.0 >= 0) {
        out = out.append(input[pos - 1] as i32);
      }
      held = cur;
    }
    pos = pos + 1;
  }
  if (held.0 >= 3) {
    return out.append(held.0 << 16 | held.1);
  }
  if (held.0 >= 0) {
    return out.append(input[n - 1] as i32);
  }
  return out;
}
```

### `33 function __fast_tokens(input: [u8]): i32[]`

```fern
<<33 function __fast_tokens(input: [u8]): i32[]>>=
function __fast_tokens(input: [u8]): i32[] {
  let head: i32[] = __filled(32768, -1);
  let out: i32[] = [];
  let pos: i32 = 0;
  while (pos < input.len()) {
    let length: i32 = 0;
    let distance: i32 = 0;
    if (pos + 2 < input.len()) {
      let candidate: i32 = head[__hash3(input, pos)];
      if (candidate >= 0 && pos - candidate <= 32768) {
        length = __common(input, candidate, pos);
        distance = pos - candidate;
      }
    }
    if (length >= 3) {
      out = out.append(length << 16 | distance);
    } else {
      out = out.append(input[pos] as i32);
      length = 1;
    }
    let end: i32 = pos + length;
    while (pos < end) {
      if (pos + 2 < input.len()) {
        head = head.with(__hash3(input, pos), pos);
      }
      pos = pos + 1;
    }
  }
  return out;
}

function __match_at(input: [u8], pos: i32, skip: i32, head: i32[], prev: i32[], window: i32): (i32, i32) {
  if (pos < skip || pos + 2 >= input.len()) {
    return (0, 0);
  }
  return __longest(input, pos, head[__hash3(input, pos)], prev, window);
}

function __filled(n: i32, v: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(v);
    i = i + 1;
  }
  return out;
}

function __hash3(input: [u8], pos: i32): i32 {
  if (pos + 2 >= input.len()) {
    return 0;
  }
  let h: i32 = input[pos] as i32 << 10 ^ input[pos + 1] as i32 << 5 ^ input[pos + 2] as i32;
  return h * 2057 & 32767;
}

function __common(input: [u8], from: i32, pos: i32): i32 {
  return __mismatch_bytes(input, from, input, pos, 258);
}

function __longest(input: [u8], pos: i32, cand: i32, prev: i32[], window: i32): (i32, i32) {
  let n: i32 = input.len();
  let best_len: i32 = 0;
  let best_dist: i32 = 0;
  let depth: i32 = 0;
  while (cand >= 0 && pos - cand < window && depth < 32) {
    let len: i32 = 0;
    if (pos + best_len >= n || input[cand + best_len] == input[pos + best_len]) {
      len = __common(input, cand, pos);
    }
    if (len > best_len) {
      best_len = len;
      best_dist = pos - cand;
    }
    if (len == 258) {
      return (best_len, best_dist);
    }
    cand = prev[cand & window - 1];
    depth = depth + 1;
  }
  return (best_len, best_dist);
}
```
