# DEFLATE, zlib, and gzip

This module decodes DEFLATE, zlib, and gzip data.

## Model

The DEFLATE decoder interprets bit fields, Huffman codes, and history references. The zlib and gzip wrappers add their frame and checksum rules.

## Contracts

Output limits bound decompressed data. `Inflated` retains both the output and the consumed input count. Error values distinguish malformed data from incomplete data and output limits.

The encoder has compression levels 1 to 9, as gzip has. `deflate_level` and `gzip_level` take the level. Level 1 is `deflate_fast`, and level 6 is `deflate` and `gzip`. A higher level visits more candidates at each position and gives smaller output.

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

<<13 function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff, lit: i32[], dtab: i32[]): __State>>

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

<<25 function __deflated(input: [u8], window_bits: i32, final: boolean): u8[]>>

<<26 struct __Span>>

<<27 function __span(toks: i32[], at: i32, end: i32): i32>>

<<28 function __two_used(freq: i32[]): i32[]>>

<<29 pub function huffman_lengths(freq: i32[], limit: i32): i32[]>>

<<30 function __assigned(keys: i32[], counts: i32[], size: i32): i32[]>>

<<31 struct __Header>>

<<32 function __cl_order(): i32[]>>

<<33 function __fast_tokens(input: [u8], start: i32): i32[]>>

<<34 resumable output>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module decodes DEFLATE, zlib, and gzip data.
import "std/hash/crc32";
import "std/array";
import "core/cmp";
import "core/int";
import "core/error";
```

### `2 pub enum InflateError`

```fern
<<2 pub enum InflateError>>=
pub enum InflateError {
  Truncated,
  Malformed(string),
  OutputLimit,
  BadChecksum,
  BadLength,
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
    BadLength => {
      return "the stored length does not match the decompressed data";
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
  let out = history;
  if (history.len() == 0) {
    out = __room(input.len() - from, max_out);
  }
  let s = __State { pos: from, bitbuf: 0, bitcnt: 0, out: out, max_out: max_out + history.len(), last: 0, distance: 0, failed: false, error: Truncated, lengths: [] };
  let done = false;
  while (!done && !s.failed) {
    s = __bits(s, input, 1);
    done = s.last == 1;
    s = __bits(s, input, 2);
    let kind = s.last;
    s = __block(s, input, kind);
  }
  if (s.failed) {
    return Err(s.error);
  }
  return Ok(Inflated { out: s.out, consumed: s.pos - from });
}

// Room for four times the input, at least 4 KiB and at most the output
// limit, so the output rarely grows and copies.
function __room(avail: i32, max_out: i32): u8[] {
  unsafe {
    let want = avail as i64 * 4i64;
    if (want < 4096i64) {
      want = 4096i64;
    }
    if (want > max_out as i64) {
      want = max_out as i64;
    }
    if (want < 0i64) {
      want = 0i64;
    }
    let out = __alloc_u8(want as i32);
    __arr_set_len(out, 0);
    return out;
  }
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
  let val = s.bitbuf;
  let cnt = s.bitcnt;
  let pos = s.pos;
  while (cnt < need) {
    if (pos >= data.len()) {
      return __fail(s, Truncated);
    }
    val |= data[pos] as i32 << cnt;
    pos += 1;
    cnt += 8;
  }
  let mask = (1 << need) - 1;
  return __State { ...s, pos: pos, bitbuf: val >> need, bitcnt: cnt - need, last: val & mask };
}

function __stored(own s: __State, data: [u8]): __State {
  s = __State { ...s, bitbuf: 0, bitcnt: 0 };
  if (s.pos + 4 > data.len()) {
    return __fail(s, Truncated);
  }
  let len = data[s.pos] as i32 | data[s.pos + 1] as i32 << 8;
  let nlen = data[s.pos + 2] as i32 | data[s.pos + 3] as i32 << 8;
  if (len != (nlen ^ 65535)) {
    return __fail(s, Malformed("a stored block's length and its complement disagree"));
  }
  let at = s.pos + 4;
  if (at + len > data.len()) {
    return __fail(s, Truncated);
  }
  if (s.out.len() + len > s.max_out) {
    return __fail(s, OutputLimit);
  }
  let out = s.out;
  s = __State { ...s, out: [] };
  let i = 0;
  while (i < len) {
    out = out.append(data[at + i]);
    i += 1;
  }
  return __State { ...s, pos: at + len, out: out };
}
```

### `9 struct __Huff`

```fern
<<9 struct __Huff>>=
struct __Huff { count: i32[], symbol: i32[], left: i32 }

struct __Tables { state: __State, literal: __Huff, distance: __Huff }

function __empty_tables(s: __State): __Tables {
  let empty = __Huff { count: [], symbol: [], left: 0 };
  return __Tables { state: s, literal: empty, distance: empty };
}
```

### `10 function __construct(lengths: i32[], n: i32): __Huff`

```fern
<<10 function __construct(lengths: i32[], n: i32): __Huff>>=
function __construct(lengths: i32[], n: i32): __Huff {
  let count = __zeros(16);
  let sym = 0;
  while (sym < n) {
    count = count.with(lengths[sym], count[lengths[sym]] + 1);
    sym += 1;
  }
  let left = 1;
  let len = 1;
  while (len <= 15) {
    left <<= 1;
    left -= count[len];
    if (left < 0) {
      return __Huff { count: count, symbol: [], left: -1 };
    }
    len += 1;
  }
  let offs = __zeros(16);
  len = 1;
  while (len < 15) {
    offs = offs.with(len + 1, offs[len] + count[len]);
    len += 1;
  }
  let symbols = __zeros(n);
  sym = 0;
  while (sym < n) {
    if (lengths[sym] != 0) {
      symbols = symbols.with(offs[lengths[sym]], sym);
      offs = offs.with(lengths[sym], offs[lengths[sym]] + 1);
    }
    sym += 1;
  }
  return __Huff { count: count, symbol: symbols, left: left };
}

function __zeros(n: i32): i32[] {
  return __alloc_i32(n);
}

// Reads one symbol of the code `h`, one bit for each step. The bits stay in
// locals, so a symbol makes one new state.
function __decode(own s: __State, data: [u8], h: __Huff): __State {
  if (s.failed) {
    return s;
  }
  let buf = s.bitbuf;
  let cnt = s.bitcnt;
  let pos = s.pos;
  let code = 0;
  let first = 0;
  let index = 0;
  let len = 1;
  while (len <= 15) {
    if (cnt == 0) {
      if (pos >= data.len()) {
        return __fail(s, Truncated);
      }
      buf = data[pos] as i32;
      pos += 1;
      cnt = 8;
    }
    code |= buf & 1;
    buf >>= 1;
    cnt -= 1;
    let count = h.count[len];
    if (code - count < first) {
      return __State { ...s, pos: pos, bitbuf: buf, bitcnt: cnt, last: h.symbol[index + (code - first)] };
    }
    index += count;
    first = first + count << 1;
    code <<= 1;
    len += 1;
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
  let table = __zeros(1 << bits);
  let code = 0;
  let index = 0;
  let len = 1;
  while (len <= bits) {
    let k = 0;
    while (k < h.count[len]) {
      let entry = __entry(h.symbol[index], len, lit);
      let at = __reversed(code, len);
      while (at < table.len()) {
        table = table.with(at, entry);
        at += 1 << len;
      }
      code += 1;
      index += 1;
      k += 1;
    }
    code <<= 1;
    len += 1;
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
  let code = 0;
  let first = 0;
  let index = 0;
  let len = 1;
  while (len <= 15) {
    if (len > avail) {
      return 0;
    }
    code |= (bits >> (len - 1) as u64 & 1u64) as i32;
    let count = h.count[len];
    if (code - count < first) {
      return __entry(h.symbol[index + (code - first)], len, lit);
    }
    index += count;
    first = first + count << 1;
    code <<= 1;
    len += 1;
  }
  return E_NO_CODE;
}

@inline
function __le64(data: [u8], at: i32): u64 {
  return data[at] as u64 | data[at + 1] as u64 << 8 | data[at + 2] as u64 << 16 | data[at + 3] as u64 << 24
    | data[at + 4] as u64 << 32 | data[at + 5] as u64 << 40 | data[at + 6] as u64 << 48 | data[at + 7] as u64 << 56;
}
```

### `13 function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff, lit: i32[], dtab: i32[]): __State`

`__fast` runs while eight input bytes and 258 output bytes are left. One
word refill leaves at least 56 bits, and a length with its distance needs at
most 48, so it reads a whole symbol pair with no bit count check. The careful
loop in `__codes` decodes what is left one byte at a time. The two loops are
in separate functions so that the fast one has the registers to itself.

```fern
<<13 function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff, lit: i32[], dtab: i32[]): __State>>=
// fern-lint: allow cyclomatic-complexity
function __codes(own s: __State, data: [u8], lencode: __Huff, distcode: __Huff, lit: i32[], dtab: i32[]): __State {
  let lmask = (1 << LIT_BITS) as u64 - 1u64;
  let dmask = (1 << DIST_BITS) as u64 - 1u64;
  let out = s.out;
  let max_out = s.max_out;
  let pos = s.pos;
  let bitbuf = s.bitbuf as u64;
  let bitcnt = s.bitcnt;
  let n = data.len();
  s = __State { ...s, out: [], lengths: [] };
  let error: Option[InflateError] = None;
  let done = false;
  let fast = __fast(out, data, pos, bitbuf, bitcnt, max_out, lit, dtab, lencode, distcode);
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
      bitbuf |= data[pos] as u64 << bitcnt as u64;
      pos += 1;
      bitcnt += 8;
    }
    let e = lit[(bitbuf & lmask) as i32];
    if (e == 0) {
      e = __walk(lencode, bitbuf, bitcnt, true);
    }
    let used = e & 15;
    if (e == 0 || used > bitcnt) {
      error = Some(Truncated);
      done = true;
    } else {
      bitbuf >>= used as u64;
      bitcnt -= used;
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
        let lx = e >> 4 & 15;
        let len = (e >> 16) + (bitbuf & (1u64 << lx as u64) - 1u64) as i32;
        if (lx > bitcnt) {
          error = Some(Truncated);
          done = true;
        } else {
          bitbuf >>= lx as u64;
          bitcnt -= lx;
          while (bitcnt <= 48 && pos < n) {
            bitbuf |= data[pos] as u64 << bitcnt as u64;
            pos += 1;
            bitcnt += 8;
          }
          let d = dtab[(bitbuf & dmask) as i32];
          if (d == 0) {
            d = __walk(distcode, bitbuf, bitcnt, false);
          }
          let dused = d & 15;
          if (d == 0 || dused > bitcnt) {
            error = Some(Truncated);
            done = true;
          } else {
            bitbuf >>= dused as u64;
            bitcnt -= dused;
            let dx = d >> 4 & 15;
            if ((d & (E_NO_CODE | E_PAST)) != 0) {
              error = __symbol_error(d, false);
              done = true;
            } else if (dx > bitcnt) {
              error = Some(Truncated);
              done = true;
            } else {
              let dist = (d >> 16) + (bitbuf & (1u64 << dx as u64) - 1u64) as i32;
              bitbuf >>= dx as u64;
              bitcnt -= dx;
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
  let back = bitcnt >> 3;
  bitcnt &= 7;
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
  let fast_in = data.len() - 8;
  let fast_out = max_out - 258;
  let status = F_ROOM;
  if (pos < 0 || lit.len() < 1 << LIT_BITS || dtab.len() < 1 << DIST_BITS) {
    return __Fast { out: out, pos: pos, bitbuf: bitbuf, bitcnt: bitcnt, status: status };
  }
  let e = -1;
  while (pos <= fast_in && out.len() <= fast_out) {
    bitbuf |= __le64(data, pos) << bitcnt as u64;
    pos += (bitcnt >> 3 ^ 7) & 7;
    bitcnt |= 56;
    if (e < 0) {
      e = lit[(bitbuf & LIT_MASK) as i32];
    }
    if (e == 0) {
      e = __walk(lencode, bitbuf, bitcnt, true);
    }
    let used = e & 15;
    bitbuf >>= used as u64;
    bitcnt -= used;
    if ((e & E_LITERAL) != 0) {
      out = out.append((e >> 16) as u8);
      e = lit[(bitbuf & LIT_MASK) as i32];
      if ((e & E_LITERAL) != 0) {
        bitbuf >>= (e & 15) as u64;
        bitcnt -= e & 15;
        out = out.append((e >> 16) as u8);
        e = lit[(bitbuf & LIT_MASK) as i32];
      }
      continue;
    }
    if ((e & (E_END | E_NO_CODE | E_PAST)) != 0) {
      status = __fast_status(e, F_LENGTH_PAST);
      break;
    }
    let lx = e >> 4 & 15;
    let len = (e >> 16) + (bitbuf & (1u64 << lx as u64) - 1u64) as i32;
    bitbuf >>= lx as u64;
    bitcnt -= lx;
    let d = dtab[(bitbuf & DIST_MASK) as i32];
    if (d == 0) {
      d = __walk(distcode, bitbuf, bitcnt, false);
    }
    let dused = d & 15;
    bitbuf >>= dused as u64;
    bitcnt -= dused;
    if ((d & (E_NO_CODE | E_PAST)) != 0) {
      status = __fast_status(d, F_DISTANCE_PAST);
      break;
    }
    let dx = d >> 4 & 15;
    let dist = (d >> 16) + (bitbuf & (1u64 << dx as u64) - 1u64) as i32;
    bitbuf >>= dx as u64;
    bitcnt -= dx;
    if (dist > out.len()) {
      status = F_TOO_FAR;
      break;
    }
    out = __copy(out, len, dist);
    e = -1;
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
  let i = 0;
  while (i < len) {
    out = out.append(out[out.len() - dist]);
    i += 1;
  }
  return out;
}
```

### `14 function __fixed(own s: __State, data: [u8]): __State`

```fern
<<14 function __fixed(own s: __State, data: [u8]): __State>>=
// The fixed codes of RFC 1951 section 3.2.6, in the form that __construct
// and __table give for them.
const FIXED_LIT_COUNT: i32[] = [0, 0, 0, 0, 0, 0, 0, 24, 152, 112, 0, 0, 0, 0, 0, 0];

const FIXED_LIT_SYMBOL: i32[] = [256, 257, 258, 259, 260, 261, 262, 263, 264, 265, 266, 267, 268, 269, 270, 271, 272, 273, 274, 275, 276, 277, 278, 279, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128, 129, 130, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142, 143, 280, 281, 282, 283, 284, 285, 286, 287, 144, 145, 146, 147, 148, 149, 150, 151, 152, 153, 154, 155, 156, 157, 158, 159, 160, 161, 162, 163, 164, 165, 166, 167, 168, 169, 170, 171, 172, 173, 174, 175, 176, 177, 178, 179, 180, 181, 182, 183, 184, 185, 186, 187, 188, 189, 190, 191, 192, 193, 194, 195, 196, 197, 198, 199, 200, 201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211, 212, 213, 214, 215, 216, 217, 218, 219, 220, 221, 222, 223, 224, 225, 226, 227, 228, 229, 230, 231, 232, 233, 234, 235, 236, 237, 238, 239, 240, 241, 242, 243, 244, 245, 246, 247, 248, 249, 250, 251, 252, 253, 254, 255];

const FIXED_DIST_COUNT: i32[] = [0, 0, 0, 0, 0, 30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];

const FIXED_DIST_SYMBOL: i32[] = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29];

const FIXED_LIT_TABLE: i32[] = [519, 5243144, 1048840, 7536712, 2031655, 7340296, 3145992, 12583177, 655367, 6291720, 2097416, 10486025, 264, 8388872, 4194568, 14680329, 393223, 5767432, 1573128, 9437449, 3866679, 7864584, 3670280, 13631753, 1114135, 6816008, 2621704, 11534601, 524552, 8913160, 4718856, 15728905, 262151, 5505288, 1310984, 14876760, 2818103, 7602440, 3408136, 13107465, 851991, 6553864, 2359560, 11010313, 262408, 8651016, 4456712, 15204617, 524295, 6029576, 1835272, 9961737, 5439559, 8126728, 3932424, 14156041, 1507367, 7078152, 2883848, 12058889, 786696, 9175304, 4981000, 16253193, 196615, 5374216, 1179912, 10682456, 2293815, 7471368, 3277064, 12845321, 720919, 6422792, 2228488, 10748169, 131336, 8519944, 4325640, 14942473, 458759, 5898504, 1704200, 9699593, 4390983, 7995656, 3801352, 13893897, 1245223, 6947080, 2752776, 11796745, 655624, 9044232, 4849928, 15991049, 327687, 5636360, 1442056, 2056, 3342391, 7733512, 3539208, 13369609, 983063, 6684936, 2490632, 11272457, 393480, 8782088, 4587784, 15466761, 589831, 6160648, 1966344, 10223881, 6488135, 8257800, 4063496, 14418185, 1769511, 7209224, 3014920, 12321033, 917768, 9306376, 5112072, 16515337, 519, 5308680, 1114376, 8585304, 2031655, 7405832, 3211528, 12714249, 655367, 6357256, 2162952, 10617097, 65800, 8454408, 4260104, 14811401, 393223, 5832968, 1638664, 9568521, 3866679, 7930120, 3735816, 13762825, 1114135, 6881544, 2687240, 11665673, 590088, 8978696, 4784392, 15859977, 262151, 5570824, 1376520, 16908296, 2818103, 7667976, 3473672, 13238537, 851991, 6619400, 2425096, 11141385, 327944, 8716552, 4522248, 15335689, 524295, 6095112, 1900808, 10092809, 5439559, 8192264, 3997960, 14287113, 1507367, 7143688, 2949384, 12189961, 852232, 9240840, 5046536, 16384265, 196615, 5439752, 1245448, 12779608, 2293815, 7536904, 3342600, 12976393, 720919, 6488328, 2294024, 10879241, 196872, 8585480, 4391176, 15073545, 458759, 5964040, 1769736, 9830665, 4390983, 8061192, 3866888, 14024969, 1245223, 7012616, 2818312, 11927817, 721160, 9109768, 4915464, 16122121, 327687, 5701896, 1507592, 2056, 3342391, 7799048, 3604744, 13500681, 983063, 6750472, 2556168, 11403529, 459016, 8847624, 4653320, 15597833, 589831, 6226184, 2031880, 10354953, 6488135, 8323336, 4129032, 14549257, 1769511, 7274760, 3080456, 12452105, 983304, 9371912, 5177608, 16646409, 519, 5243144, 1048840, 7536712, 2031655, 7340296, 3145992, 12648713, 655367, 6291720, 2097416, 10551561, 264, 8388872, 4194568, 14745865, 393223, 5767432, 1573128, 9502985, 3866679, 7864584, 3670280, 13697289, 1114135, 6816008, 2621704, 11600137, 524552, 8913160, 4718856, 15794441, 262151, 5505288, 1310984, 14876760, 2818103, 7602440, 3408136, 13173001, 851991, 6553864, 2359560, 11075849, 262408, 8651016, 4456712, 15270153, 524295, 6029576, 1835272, 10027273, 5439559, 8126728, 3932424, 14221577, 1507367, 7078152, 2883848, 12124425, 786696, 9175304, 4981000, 16318729, 196615, 5374216, 1179912, 10682456, 2293815, 7471368, 3277064, 12910857, 720919, 6422792, 2228488, 10813705, 131336, 8519944, 4325640, 15008009, 458759, 5898504, 1704200, 9765129, 4390983, 7995656, 3801352, 13959433, 1245223, 6947080, 2752776, 11862281, 655624, 9044232, 4849928, 16056585, 327687, 5636360, 1442056, 2056, 3342391, 7733512, 3539208, 13435145, 983063, 6684936, 2490632, 11337993, 393480, 8782088, 4587784, 15532297, 589831, 6160648, 1966344, 10289417, 6488135, 8257800, 4063496, 14483721, 1769511, 7209224, 3014920, 12386569, 917768, 9306376, 5112072, 16580873, 519, 5308680, 1114376, 8585304, 2031655, 7405832, 3211528, 12779785, 655367, 6357256, 2162952, 10682633, 65800, 8454408, 4260104, 14876937, 393223, 5832968, 1638664, 9634057, 3866679, 7930120, 3735816, 13828361, 1114135, 6881544, 2687240, 11731209, 590088, 8978696, 4784392, 15925513, 262151, 5570824, 1376520, 16908296, 2818103, 7667976, 3473672, 13304073, 851991, 6619400, 2425096, 11206921, 327944, 8716552, 4522248, 15401225, 524295, 6095112, 1900808, 10158345, 5439559, 8192264, 3997960, 14352649, 1507367, 7143688, 2949384, 12255497, 852232, 9240840, 5046536, 16449801, 196615, 5439752, 1245448, 12779608, 2293815, 7536904, 3342600, 13041929, 720919, 6488328, 2294024, 10944777, 196872, 8585480, 4391176, 15139081, 458759, 5964040, 1769736, 9896201, 4390983, 8061192, 3866888, 14090505, 1245223, 7012616, 2818312, 11993353, 721160, 9109768, 4915464, 16187657, 327687, 5701896, 1507592, 2056, 3342391, 7799048, 3604744, 13566217, 983063, 6750472, 2556168, 11469065, 459016, 8847624, 4653320, 15663369, 589831, 6226184, 2031880, 10420489, 6488135, 8323336, 4129032, 14614793, 1769511, 7274760, 3080456, 12517641, 983304, 9371912, 5177608, 16711945, 519, 5243144, 1048840, 7536712, 2031655, 7340296, 3145992, 12583177, 655367, 6291720, 2097416, 10486025, 264, 8388872, 4194568, 14680329, 393223, 5767432, 1573128, 9437449, 3866679, 7864584, 3670280, 13631753, 1114135, 6816008, 2621704, 11534601, 524552, 8913160, 4718856, 15728905, 262151, 5505288, 1310984, 14876760, 2818103, 7602440, 3408136, 13107465, 851991, 6553864, 2359560, 11010313, 262408, 8651016, 4456712, 15204617, 524295, 6029576, 1835272, 9961737, 5439559, 8126728, 3932424, 14156041, 1507367, 7078152, 2883848, 12058889, 786696, 9175304, 4981000, 16253193, 196615, 5374216, 1179912, 10682456, 2293815, 7471368, 3277064, 12845321, 720919, 6422792, 2228488, 10748169, 131336, 8519944, 4325640, 14942473, 458759, 5898504, 1704200, 9699593, 4390983, 7995656, 3801352, 13893897, 1245223, 6947080, 2752776, 11796745, 655624, 9044232, 4849928, 15991049, 327687, 5636360, 1442056, 2056, 3342391, 7733512, 3539208, 13369609, 983063, 6684936, 2490632, 11272457, 393480, 8782088, 4587784, 15466761, 589831, 6160648, 1966344, 10223881, 6488135, 8257800, 4063496, 14418185, 1769511, 7209224, 3014920, 12321033, 917768, 9306376, 5112072, 16515337, 519, 5308680, 1114376, 8585304, 2031655, 7405832, 3211528, 12714249, 655367, 6357256, 2162952, 10617097, 65800, 8454408, 4260104, 14811401, 393223, 5832968, 1638664, 9568521, 3866679, 7930120, 3735816, 13762825, 1114135, 6881544, 2687240, 11665673, 590088, 8978696, 4784392, 15859977, 262151, 5570824, 1376520, 16908296, 2818103, 7667976, 3473672, 13238537, 851991, 6619400, 2425096, 11141385, 327944, 8716552, 4522248, 15335689, 524295, 6095112, 1900808, 10092809, 5439559, 8192264, 3997960, 14287113, 1507367, 7143688, 2949384, 12189961, 852232, 9240840, 5046536, 16384265, 196615, 5439752, 1245448, 12779608, 2293815, 7536904, 3342600, 12976393, 720919, 6488328, 2294024, 10879241, 196872, 8585480, 4391176, 15073545, 458759, 5964040, 1769736, 9830665, 4390983, 8061192, 3866888, 14024969, 1245223, 7012616, 2818312, 11927817, 721160, 9109768, 4915464, 16122121, 327687, 5701896, 1507592, 2056, 3342391, 7799048, 3604744, 13500681, 983063, 6750472, 2556168, 11403529, 459016, 8847624, 4653320, 15597833, 589831, 6226184, 2031880, 10354953, 6488135, 8323336, 4129032, 14549257, 1769511, 7274760, 3080456, 12452105, 983304, 9371912, 5177608, 16646409, 519, 5243144, 1048840, 7536712, 2031655, 7340296, 3145992, 12648713, 655367, 6291720, 2097416, 10551561, 264, 8388872, 4194568, 14745865, 393223, 5767432, 1573128, 9502985, 3866679, 7864584, 3670280, 13697289, 1114135, 6816008, 2621704, 11600137, 524552, 8913160, 4718856, 15794441, 262151, 5505288, 1310984, 14876760, 2818103, 7602440, 3408136, 13173001, 851991, 6553864, 2359560, 11075849, 262408, 8651016, 4456712, 15270153, 524295, 6029576, 1835272, 10027273, 5439559, 8126728, 3932424, 14221577, 1507367, 7078152, 2883848, 12124425, 786696, 9175304, 4981000, 16318729, 196615, 5374216, 1179912, 10682456, 2293815, 7471368, 3277064, 12910857, 720919, 6422792, 2228488, 10813705, 131336, 8519944, 4325640, 15008009, 458759, 5898504, 1704200, 9765129, 4390983, 7995656, 3801352, 13959433, 1245223, 6947080, 2752776, 11862281, 655624, 9044232, 4849928, 16056585, 327687, 5636360, 1442056, 2056, 3342391, 7733512, 3539208, 13435145, 983063, 6684936, 2490632, 11337993, 393480, 8782088, 4587784, 15532297, 589831, 6160648, 1966344, 10289417, 6488135, 8257800, 4063496, 14483721, 1769511, 7209224, 3014920, 12386569, 917768, 9306376, 5112072, 16580873, 519, 5308680, 1114376, 8585304, 2031655, 7405832, 3211528, 12779785, 655367, 6357256, 2162952, 10682633, 65800, 8454408, 4260104, 14876937, 393223, 5832968, 1638664, 9634057, 3866679, 7930120, 3735816, 13828361, 1114135, 6881544, 2687240, 11731209, 590088, 8978696, 4784392, 15925513, 262151, 5570824, 1376520, 16908296, 2818103, 7667976, 3473672, 13304073, 851991, 6619400, 2425096, 11206921, 327944, 8716552, 4522248, 15401225, 524295, 6095112, 1900808, 10158345, 5439559, 8192264, 3997960, 14352649, 1507367, 7143688, 2949384, 12255497, 852232, 9240840, 5046536, 16449801, 196615, 5439752, 1245448, 12779608, 2293815, 7536904, 3342600, 13041929, 720919, 6488328, 2294024, 10944777, 196872, 8585480, 4391176, 15139081, 458759, 5964040, 1769736, 9896201, 4390983, 8061192, 3866888, 14090505, 1245223, 7012616, 2818312, 11993353, 721160, 9109768, 4915464, 16187657, 327687, 5701896, 1507592, 2056, 3342391, 7799048, 3604744, 13566217, 983063, 6750472, 2556168, 11469065, 459016, 8847624, 4653320, 15663369, 589831, 6226184, 2031880, 10420489, 6488135, 8323336, 4129032, 14614793, 1769511, 7274760, 3080456, 12517641, 983304, 9371912, 5177608, 16711945];

const FIXED_DIST_TABLE: i32[] = [65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0, 65541, 16842869, 1114165, 268501173, 327701, 67174549, 4259925, 1073807573, 196613, 33620101, 2162757, 536936645, 589861, 134283429, 8454245, 0, 131077, 25231477, 1638453, 402718901, 458773, 100728981, 6357077, 1610678485, 262149, 50397317, 3211333, 805372101, 852005, 201392293, 12648549, 0];

function __fixed(own s: __State, data: [u8]): __State {
  let tables = __fixed_tables(s);
  return __codes(__State { ...tables.state }, data, tables.literal, tables.distance, FIXED_LIT_TABLE, FIXED_DIST_TABLE);
}

function __fixed_tables(s: __State): __Tables {
  return __Tables {
    state: s,
    literal: __Huff { count: FIXED_LIT_COUNT, symbol: FIXED_LIT_SYMBOL, left: 0 },
    distance: __Huff { count: FIXED_DIST_COUNT, symbol: FIXED_DIST_SYMBOL, left: 2048 },
  };
}

function __code_length_order(): i32[] {
  return [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];
}

function __dynamic(own s: __State, data: [u8]): __State {
  let tables = __dynamic_tables(s, data);
  if (tables.state.failed) {
    return tables.state;
  }
  let lit = __table(tables.literal, LIT_BITS, true);
  let dtab = __table(tables.distance, DIST_BITS, false);
  return __codes(__State { ...tables.state }, data, tables.literal, tables.distance, lit, dtab);
}

function __dynamic_tables(own s: __State, data: [u8]): __Tables {
  s = __bits(s, data, 5);
  let nlen = s.last + 257;
  s = __bits(s, data, 5);
  let ndist = s.last + 1;
  s = __bits(s, data, 4);
  let ncode = s.last + 4;
  if (nlen > 286 || ndist > 30) {
    return __empty_tables(__fail(s, Malformed("too many length or distance codes")));
  }
  let order = __code_length_order();
  let lengths = __zeros(19);
  let i = 0;
  while (i < ncode) {
    s = __bits(s, data, 3);
    lengths = lengths.with(order[i], s.last);
    i += 1;
  }
  let lencode = __construct(lengths, 19);
  if (lencode.left != 0) {
    return __empty_tables(__fail(s, Malformed("an incomplete or over-subscribed code length code")));
  }
  s = __code_lengths(s, data, lencode, nlen + ndist);
  if (s.failed) {
    return __empty_tables(s);
  }
  return __dynamic_codes(s, nlen, ndist);
}

function __dynamic_codes(own s: __State, nlen: i32, ndist: i32): __Tables {
  let all = s.lengths;
  if (all[256] == 0) {
    return __empty_tables(__fail(s, Malformed("no end-of-block code")));
  }
  let lit = __construct(all, nlen);
  if (__invalid_tree(lit, nlen, false)) {
    return __empty_tables(__fail(s, Malformed("an incomplete or over-subscribed literal/length code")));
  }
  let dist = __construct(__tail(all, nlen, nlen + ndist), ndist);
  if (__invalid_tree(dist, ndist, true)) {
    return __empty_tables(__fail(s, Malformed("an incomplete or over-subscribed distance code")));
  }
  return __Tables { state: s, literal: lit, distance: dist };
}

function __invalid_tree(tree: __Huff, symbols: i32, empty: boolean): boolean {
  if (tree.left <= 0) {
    return tree.left < 0;
  }
  let used = symbols - tree.count[0];
  return !(empty && used == 0 || used == 1 && tree.count[1] == 1);
}

function __tail(xs: i32[], from: i32, end: i32): i32[] {
  let out: i32[] = [];
  let i = from;
  while (i < end) {
    out = out.append(xs[i]);
    i += 1;
  }
  return out;
}

// The entry of each seven-bit pattern for the code of the code lengths:
// the symbol from bit 4 and the code length in bits 0-3.
function __length_table(code: __Huff): i32[] {
  let table = __zeros(128);
  let next = 0;
  let index = 0;
  let len = 1;
  while (len <= 7) {
    let k = 0;
    while (k < code.count[len]) {
      let entry = code.symbol[index] << 4 | len;
      let at = __reversed(next, len);
      while (at < 128) {
        table = table.with(at, entry);
        at += 1 << len;
      }
      next += 1;
      index += 1;
      k += 1;
    }
    next <<= 1;
    len += 1;
  }
  return table;
}

// The count of extra bits of a repeat symbol, and the count that the extra
// bits add to.
const REPEAT_EXTRA: i32[] = [2, 3, 7];

const REPEAT_BASE: i32[] = [3, 3, 11];

// Reads `n` code lengths. A symbol with its extra bits needs 14 bits at
// most, so one refill serves each symbol. The whole bytes that stay in the
// bit buffer go back to the input at the end.
function __code_lengths(own s: __State, data: [u8], code: __Huff, n: i32): __State {
  let table = __length_table(code);
  let lengths: i32[] = [];
  let pos = s.pos;
  let buf = s.bitbuf;
  let cnt = s.bitcnt;
  let error: Option[InflateError] = None;
  while (lengths.len() < n) {
    while (cnt < 14 && pos < data.len()) {
      buf |= data[pos] as i32 << cnt;
      pos += 1;
      cnt += 8;
    }
    let entry = table[buf & 127];
    let used = entry & 15;
    if (used == 0) {
      error = Some(Malformed("a bit pattern that is no code"));
      break;
    }
    if (used > cnt) {
      error = Some(Truncated);
      break;
    }
    buf >>= used;
    cnt -= used;
    let sym = entry >> 4;
    if (sym < 16) {
      lengths = lengths.append(sym);
      continue;
    }
    if (sym == 16 && lengths.len() == 0) {
      error = Some(Malformed("a repeat with no length before it"));
      break;
    }
    let extra = REPEAT_EXTRA[sym - 16];
    if (extra > cnt) {
      error = Some(Truncated);
      break;
    }
    let times = REPEAT_BASE[sym - 16] + (buf & (1 << extra) - 1);
    buf >>= extra;
    cnt -= extra;
    if (lengths.len() + times > n) {
      error = Some(Malformed("more code lengths than codes"));
      break;
    }
    let value = if (sym == 16) { lengths[lengths.len() - 1] } else { 0 };
    lengths = __repeated(lengths, times, value);
  }
  pos -= cnt >> 3;
  cnt &= 7;
  s = __State { ...s, pos: pos, bitbuf: buf & (1 << cnt) - 1, bitcnt: cnt, lengths: lengths };
  return match (error) {
    Some(e) => __fail(s, e),
    None => s,
  };
}
```

### `15 function __repeated(own lengths: i32[], times: i32, value: i32): i32[]`

```fern
<<15 function __repeated(own lengths: i32[], times: i32, value: i32): i32[]>>=
function __repeated(own lengths: i32[], times: i32, value: i32): i32[] {
  let k = 0;
  while (k < times) {
    lengths = lengths.append(value);
    k += 1;
  }
  return lengths;
}
```

### `16 pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError]`

```fern
<<16 pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError]>>=
pub function gunzip(input: [u8], max_out: i32): Result[u8[], InflateError] {
  let out: u8[] = [];
  let at = 0;
  while (at < input.len()) {
    let member = gzip_member(input, at, max_out - out.len())?;
    out = __joined(out, member.out);
    at += member.consumed;
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
  let i = 0;
  while (i < more.len()) {
    out = out.append(more[i]);
    i += 1;
  }
  return out;
}

// One gzip member at `from`: its header, its deflate data and its trailer.
// `consumed` counts the bytes of the member.
pub function gzip_member(input: [u8], from: i32, max_out: i32): Result[Inflated, InflateError] {
  let body = __gzip_header(input, from)?;
  let inflated = inflate_from(input, body, max_out)?;
  let trailer = body + inflated.consumed;
  if (trailer + 8 > input.len()) {
    return Err(Truncated);
  }
  let crc = __le32(input, trailer);
  let isize = __le32(input, trailer + 4);
  if (crc != crc32.new().update(inflated.out[:]).finish()) {
    return Err(BadChecksum);
  }
  if (isize != inflated.out.len() as u32) {
    return Err(BadLength);
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
  let flags = input[from + 3] as i32;
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
    at += 2;
  }
  if (at > input.len()) {
    return Err(Truncated);
  }
  return Ok(at);
}

function __past_nul(input: [u8], at: i32): Result[i32, InflateError] {
  let i = at;
  while (i < input.len()) {
    if (input[i] == 0 as u8) {
      return Ok(i + 1);
    }
    i += 1;
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
  let cmf = input[0] as i32;
  let flg = input[1] as i32;
  if ((cmf & 15) != 8 || cmf >> 4 > 7) {
    return Err(BadHeader("a zlib compression method or window this decoder does not read"));
  }
  if ((cmf * 256 + flg) % 31 != 0) {
    return Err(BadHeader("a zlib header whose check fails"));
  }
  if ((flg & 32) != 0) {
    return Err(BadHeader("a zlib preset dictionary"));
  }
  let inflated = inflate_from(input, 2, max_out)?;
  let trailer = 2 + inflated.consumed;
  if (trailer + 4 > input.len()) {
    return Err(Truncated);
  }
  let want = input[trailer] as u32 << 24 | input[trailer + 1] as u32 << 16 | input[trailer + 2] as u32 << 8 | input[trailer + 3] as u32;
  if (want != adler32(inflated.out)) {
    return Err(BadChecksum);
  }
  return Ok(inflated.out);
}

// The sums take their modulus once for each 5552 bytes, zlib's NMAX: the
// most bytes after which b cannot pass 2^32.
pub function adler32(bs: [u8]): u32 {
  let a = 1;
  let b = 0;
  let n = bs.len();
  let i = 0;
  while (i < n) {
    let end = cmp.min(n, i + 5552);
    while (i + 4 <= end) {
      a += bs[i] as u32;
      b += a;
      a += bs[i + 1] as u32;
      b += a;
      a += bs[i + 2] as u32;
      b += a;
      a += bs[i + 3] as u32;
      b += a;
      i += 4;
    }
    while (i < end) {
      a += bs[i] as u32;
      b += a;
      i += 1;
    }
    a %= 65521;
    b %= 65521;
  }
  return b << 16 | a;
}

pub function deflate(input: [u8]): u8[] {
  return __deflated(input, 15, true);
}

pub function deflate_fast(input: [u8]): u8[] {
  return __deflated_tokens(input, __fast_tokens(input, 0), true, 0);
}

// Level 1 is deflate_fast and level 6 is deflate. A level outside 1 to 9 is
// the nearest of the two ends.
pub function deflate_level(input: [u8], level: i32): u8[] {
  if (level <= 1) {
    return deflate_fast(input);
  }
  let e = __effort(level);
  return __deflated_tokens(input, __tokens(input, 32768, e.depth, e.nice, 0), true, 0);
}

// One block of a stream that workers compress in parts: the deflate data of
// `input`, whose matches can reach into the last 32 KiB of `dict`. A block
// that is not the last ends on a byte boundary with an empty stored block,
// so the next block can follow it directly.
pub function deflate_block(dict: [u8], input: [u8], level: i32, last: boolean): u8[] {
  let keep = cmp.min(dict.len(), 32768);
  let none: u8[] = [];
  let buf = __joined(__joined(none, dict[dict.len() - keep:dict.len()]), input);
  if (level <= 1) {
    return __deflated_tokens(buf, __fast_tokens(buf, keep), last, keep);
  }
  let e = __effort(level);
  return __deflated_tokens(buf, __tokens(buf, 32768, e.depth, e.nice, keep), last, keep);
}

pub function gzip(input: [u8]): u8[] {
  return gzip_level(input, 6);
}

pub function gzip_level(input: [u8], level: i32): u8[] {
  let crc = crc32.new().update(input).finish();
  let xfl = 0;
  if (level <= 1) {
    xfl = 4;
  } else if (level >= 9) {
    xfl = 2;
  }
  let head = [31 as u8, 139 as u8, 8 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, xfl as u8, 255 as u8];
  return __put_le32(__put_le32(__joined(head, deflate_level(input, level)), crc), input.len() as u32);
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
  let acc = w.acc | value << w.n;
  let n = w.n + bits;
  let out = w.out;
  while (n >= 8) {
    out = out.append((acc & 255) as u8);
    acc >>= 8;
    n -= 8;
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
  let count = __filled(16, 0);
  for l in lens {
    if (l > 0) {
      count = __bump(count, l);
    }
  }
  let next = __filled(16, 0);
  let code = 0;
  let b = 1;
  while (b < 16) {
    code = code + count[b - 1] << 1;
    next = next.with(b, code);
    b += 1;
  }
  let codes: i32[] = [];
  for l in lens {
    codes = codes.append(__reversed(next[l], l));
    next = __bump(next, l);
  }
  return __Code { lens: lens, codes: codes };
}

function __reversed(code: i32, bits: i32): i32 {
  let rev = 0;
  let i = 0;
  while (i < bits) {
    rev = rev << 1 | code >> i & 1;
    i += 1;
  }
  return rev;
}

function __bump(own xs: i32[], i: i32): i32[] {
  return xs.with(i, xs[i] + 1);
}

function __fixed_lit(): __Code {
  let lens: i32[] = [];
  let s = 0;
  while (s < 288) {
    lens = lens.append(__fixed_len(s));
    s += 1;
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
  let x = len - 3;
  if (x < 8) {
    return x;
  }
  let k = __log2(x);
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
  let x = d - 1;
  if (x < 4) {
    return x;
  }
  let k = __log2(x);
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
  return 31 - __clz32(x as u32);
}
```

### `25 function __deflated(input: [u8], window_bits: i32, final: boolean): u8[]`

```fern
<<25 function __deflated(input: [u8], window_bits: i32, final: boolean): u8[]>>=
function __deflated(input: [u8], window_bits: i32, final: boolean): u8[] {
  let e = __effort(6);
  return __deflated_tokens(input, __tokens(input, 1 << window_bits, e.depth, e.nice, 0), final, 0);
}

// The tokens of input[from:], whose matches can reach back before `from`.
function __deflated_tokens(input: [u8], toks: i32[], final: boolean, from: i32): u8[] {
  let fixed = (__fixed_lit(), __fixed_dist());
  let empty: u8[] = [];
  let w = __Bits { out: empty, acc: 0, n: 0 };
  let s = __Span { from: from, len: 0, at: 0, end: 0, last: false };
  while (s.end < toks.len() || s.at == 0 && s.end == 0) {
    let end = s.end + 16384;
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
  let n = 0;
  let i = at;
  while (i < end) {
    if (toks[i] < 256) {
      n += 1;
    } else {
      n += toks[i] >> 16;
    }
    i += 1;
  }
  return n;
}

function __put_block(w: __Bits, input: [u8], toks: i32[], s: __Span, fixed: (__Code, __Code)): __Bits {
  let f = __freqs(toks, s.at, s.end);
  let lit = __canonical(huffman_lengths(__two_used(f.0), 15));
  let dist = __canonical(huffman_lengths(__two_used(f.1), 15));
  let h = __header(lit.lens, dist.lens);
  let extra = __extra_cost(f.0, f.1);
  let own_cost = h.bits + __weighted(f.0, lit.lens) + __weighted(f.1, dist.lens);
  let fixed_cost = __weighted(f.0, fixed.0.lens) + __weighted(f.1, fixed.1.lens);
  if (__stored_cost(s.len) < 3 + extra + cmp.min(own_cost, fixed_cost)) {
    return __put_stored(w, input, s);
  }
  let fin = 0;
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
  let lit = __filled(286, 0);
  let dist = __filled(30, 0);
  let i = at;
  while (i < end) {
    let t = toks[i];
    if (t < 256) {
      lit = lit.with(t, lit[t] + 1);
    } else {
      let ls = 257 + __len_sym(t >> 16);
      let ds = __dist_sym(t & 65535);
      lit = lit.with(ls, lit[ls] + 1);
      dist = dist.with(ds, dist[ds] + 1);
    }
    i += 1;
  }
  return (__bump(lit, 256), dist);
}

function __extra_cost(lit: i32[], dist: i32[]): i32 {
  let bits = 0;
  let i = 0;
  while (i < 29) {
    bits = bits + lit[257 + i] * __len_extra_of(i);
    i += 1;
  }
  i = 0;
  while (i < 30) {
    bits = bits + dist[i] * __dist_extra_of(i);
    i += 1;
  }
  return bits;
}

function __weighted(freq: i32[], lens: i32[]): i32 {
  let bits = 0;
  let i = 0;
  while (i < freq.len()) {
    bits = bits + freq[i] * lens[i];
    i += 1;
  }
  return bits;
}

function __stored_cost(len: i32): i32 {
  return (len / 65535 + 1) * 42 + len * 8;
}

function __put_stored(w: __Bits, input: [u8], s: __Span): __Bits {
  let at = s.from;
  let end = s.from + s.len;
  let first = true;
  while (first || at < end) {
    let n = cmp.min(end - at, 65535);
    let fin = 0;
    if (s.last && at + n == end) {
      fin = 1;
    }
    w = __flush_byte(__put(__put(w, fin, 1), 0, 2));
    w = __put(__put(w, n, 16), n ^ 65535, 16);
    w = __Bits { ...w, out: __joined_range(w.out, input, at, at + n) };
    at += n;
    first = false;
  }
  return w;
}

function __joined_range(out: u8[], more: [u8], from: i32, to: i32): u8[] {
  let i = from;
  while (i < to) {
    out = out.append(more[i]);
    i += 1;
  }
  return out;
}

// A token adds at most 48 bits to fewer than 8 held ones, so one flush of
// whole bytes after each token keeps the 64-bit buffer from overflowing.
function __put_tokens(w: __Bits, toks: i32[], s: __Span, lit: __Code, dist: __Code): __Bits {
  let words = __length_words(lit);
  let lcodes = lit.codes;
  let llens = lit.lens;
  let dcodes = dist.codes;
  let dlens = dist.lens;
  let out = w.out;
  let acc = w.acc as u64;
  let n = w.n;
  let i = s.at;
  while (i < s.end) {
    let t = toks[i];
    if (t < 256) {
      acc |= lcodes[t] as u64 << n as u64;
      n += llens[t];
    } else {
      let e = words[t >> 16];
      acc |= (e >> 8) as u64 << n as u64;
      n += (e & 255i64) as i32;
      let x = (t & 65535) - 1;
      let di = x;
      let nb = 0;
      if (x >= 4) {
        let k = __log2(x);
        di = 2 * k + (x >> k - 1 & 1);
        nb = k - 1;
      }
      acc |= (dcodes[di] | (x & (1 << nb) - 1) << dlens[di]) as u64 << n as u64;
      n = n + dlens[di] + nb;
    }
    while (n >= 8) {
      out = out.append((acc & 255u64) as u8);
      acc >>= 8u64;
      n -= 8;
    }
    i += 1;
  }
  return __put_sym(__Bits { out: out, acc: acc as i32, n: n }, lit, 256);
}

// For each match length, its symbol's code and extra bits as one word, the
// bits above bit 8 and their count below.
function __length_words(lit: __Code): i64[] {
  let out = __alloc_i64(259);
  let length = 3;
  while (length <= 258) {
    let li = __len_sym(length);
    let sym = 257 + li;
    let bits = (lit.codes[sym] | length - __len_base_of(li) << lit.lens[sym]) as i64;
    out = out.with(length, bits << 8 | (lit.lens[sym] + __len_extra_of(li)) as i64);
    length += 1;
  }
  return out;
}
```

### `28 function __two_used(freq: i32[]): i32[]`

```fern
<<28 function __two_used(freq: i32[]): i32[]>>=
function __two_used(freq: i32[]): i32[] {
  let used = 0;
  for f in freq {
    if (f > 0) {
      used += 1;
    }
  }
  let s = 0;
  while (used < 2) {
    if (freq[s] == 0) {
      freq = freq.with(s, 1);
      used += 1;
    }
    s += 1;
  }
  return freq;
}
```

### `29 pub function huffman_lengths(freq: i32[], limit: i32): i32[]`

```fern
<<29 pub function huffman_lengths(freq: i32[], limit: i32): i32[]>>=
pub function huffman_lengths(freq: i32[], limit: i32): i32[] {
  let keys: i32[] = [];
  let s = 0;
  while (s < freq.len()) {
    if (freq[s] > 0) {
      keys = keys.append(freq[s] << 9 | s);
    }
    s += 1;
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
  let lens = __filled(size, 0);
  let j = keys.len();
  let l = 1;
  while (l < counts.len()) {
    let c = counts[l];
    while (c > 0) {
      j -= 1;
      lens = lens.with(keys[j] & 511, l);
      c -= 1;
    }
    l += 1;
  }
  return lens;
}

function __moffat(own a: i32[]): i32[] {
  let n = a.len();
  if (n == 1) {
    return a.with(0, 1);
  }
  if (n == 0) {
    return a;
  }
  return __moffat_depths(__moffat_parents(a));
}

function __moffat_parents(own a: i32[]): i32[] {
  let n = a.len();
  a = a.with(0, a[0] + a[1]);
  let root = 0;
  let leaf = 2;
  let next = 1;
  while (next < n - 1) {
    if (leaf >= n || a[root] < a[leaf]) {
      a = a.with(next, a[root]).with(root, next);
      root += 1;
    } else {
      a = a.with(next, a[leaf]);
      leaf += 1;
    }
    if (leaf >= n || root < next && a[root] < a[leaf]) {
      a = a.with(next, a[next] + a[root]).with(root, next);
      root += 1;
    } else {
      a = a.with(next, a[next] + a[leaf]);
      leaf += 1;
    }
    next += 1;
  }
  return a;
}

function __moffat_depths(own a: i32[]): i32[] {
  let n = a.len();
  a = a.with(n - 2, 0);
  let next = n - 3;
  while (next >= 0) {
    a = a.with(next, a[a[next]] + 1);
    next -= 1;
  }
  let avail = 1;
  let depth = 0;
  let root = n - 2;
  next = n - 1;
  while (avail > 0) {
    let used = 0;
    while (root >= 0 && a[root] == depth) {
      used += 1;
      root -= 1;
    }
    while (avail > used) {
      a = a.with(next, depth);
      next -= 1;
      avail -= 1;
    }
    avail = 2 * used;
    depth += 1;
  }
  return a;
}

function __limited(depths: i32[], limit: i32): i32[] {
  let counts = __filled(limit + 1, 0);
  for d in depths {
    counts = __bump(counts, cmp.min(d, limit));
  }
  let total = 0;
  let l = 1;
  while (l <= limit) {
    total += counts[l] << limit - l;
    l += 1;
  }
  while (total > 1 << limit) {
    counts = counts.with(limit, counts[limit] - 1);
    let i = limit - 1;
    while (counts[i] == 0) {
      i -= 1;
    }
    counts = counts.with(i, counts[i] - 1).with(i + 1, counts[i + 1] + 2);
    total -= 1;
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
  let hlit = __sent(lit, 257);
  let hdist = __sent(dist, 1);
  let lens = array.slice(lit, 0, hlit).concat(array.slice(dist, 0, hdist));
  let rle = __rle(lens);
  let freq = __filled(19, 0);
  for r in rle {
    freq = __bump(freq, r & 31);
  }
  let cl = __canonical(huffman_lengths(__two_used(freq), 7));
  let order = __cl_order();
  let hclen = 19;
  while (hclen > 4 && cl.lens[order[hclen - 1]] == 0) {
    hclen -= 1;
  }
  let bits = 14 + 3 * hclen;
  for r in rle {
    bits = bits + cl.lens[r & 31] + __cl_extra(r & 31);
  }
  return __Header { hlit: hlit, hdist: hdist, rle: rle, cl: cl, hclen: hclen, bits: bits };
}

function __sent(lens: i32[], least: i32): i32 {
  let n = lens.len();
  while (n > least && lens[n - 1] == 0) {
    n -= 1;
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
  let i = 0;
  while (i < lens.len()) {
    let step: (i32, i32) = __rle_step(lens, i);
    out = out.append(step.0);
    i += step.1;
  }
  return out;
}

function __rle_step(lens: i32[], i: i32): (i32, i32) {
  let v = lens[i];
  let run = __run(lens, i);
  if (v == 0 && run >= 11) {
    return (18 | run - 11 << 5, run);
  }
  if (v == 0 && run >= 3) {
    let r = cmp.min(run, 10);
    return (17 | r - 3 << 5, r);
  }
  if (v != 0 && i > 0 && lens[i - 1] == v && run >= 3) {
    let r = cmp.min(run, 6);
    return (16 | r - 3 << 5, r);
  }
  return (v, 1);
}

function __run(lens: i32[], i: i32): i32 {
  let run = 1;
  while (run < 138 && i + run < lens.len() && lens[i + run] == lens[i]) {
    run += 1;
  }
  return run;
}

function __put_header(w: __Bits, h: __Header): __Bits {
  w = __put(w, h.hlit - 257, 5);
  w = __put(w, h.hdist - 1, 5);
  w = __put(w, h.hclen - 4, 4);
  let order = __cl_order();
  let k = 0;
  while (k < h.hclen) {
    w = __put(w, h.cl.lens[order[k]], 3);
    k += 1;
  }
  for r in h.rle {
    w = __put(__put_sym(w, h.cl, r & 31), r >> 5, __cl_extra(r & 31));
  }
  return w;
}

// How far the search of a compression level looks at one position: the
// candidates it visits and the match length that ends it.
struct __Effort { depth: i32, nice: i32 }

function __effort(level: i32): __Effort {
  if (level <= 2) {
    return __Effort { depth: 8, nice: 16 };
  }
  if (level == 3) {
    return __Effort { depth: 16, nice: 32 };
  }
  if (level == 4) {
    return __Effort { depth: 24, nice: 48 };
  }
  if (level == 5) {
    return __Effort { depth: 30, nice: 58 };
  }
  if (level == 6) {
    return __Effort { depth: 35, nice: 65 };
  }
  if (level == 7) {
    return __Effort { depth: 64, nice: 128 };
  }
  if (level == 8) {
    return __Effort { depth: 256, nice: 258 };
  }
  return __Effort { depth: 1024, nice: 258 };
}

// Both hashes have bounded chains. Each ring entry stores the four-byte
// link in its low word and the three-byte link in its high word. Heads
// refer only to positions whose ring entries have already been written.
// Positions before `start` are a dictionary: they enter the hashes, and they
// give no token.
function __tokens(input: [u8], window: i32, depth: i32, nice: i32, start: i32): i32[] {
  let n = input.len();
  let head3 = __filled(32768, -1);
  let head4 = __filled(65536, -1);
  let prev = __alloc_i64(window);
  let out: i32[] = [];
  let held = (-1, 0);
  let skip = 0;
  let pos = 0;
  let primed = cmp.min(start, n - 3);
  while (pos < primed) {
    let w = __le32(input, pos);
    let h3 = ((w << 8) * 506832829u32 >> 17) as i32;
    let h4 = (w * 506832829u32 >> 16) as i32;
    prev = prev.with(pos & window - 1, head3[h3] as i64 << 32 | (head4[h4] as u32) as i64);
    head3 = head3.with(h3, pos);
    head4 = head4.with(h4, pos);
    pos += 1;
  }
  pos = start;
  while (pos < n) {
    let c3 = -1;
    let c4 = -1;
    if (pos + 3 < n) {
      let w = __le32(input, pos);
      let h3 = ((w << 8) * 506832829u32 >> 17) as i32;
      let h4 = (w * 506832829u32 >> 16) as i32;
      c3 = head3[h3];
      c4 = head4[h4];
      head3 = head3.with(h3, pos);
      head4 = head4.with(h4, pos);
      prev = prev.with(pos & window - 1, c3 as i64 << 32 | (c4 as u32) as i64);
    }
    let cur = (0, 0);
    if (pos >= skip && held.0 < nice) {
      cur = __longest(input, pos, c3, c4, prev, window, held.0, depth, nice);
    }
    if (pos >= skip && held.0 >= 3 && cur.0 <= held.0) {
      out = out.append(held.0 << 16 | held.1);
      skip = pos - 1 + held.0;
      held = (-1, 0);
    } else if (pos >= skip) {
      if (held.0 >= 0) {
        out = out.append(input[pos - 1] as i32);
      }
      held = cur;
    }
    pos += 1;
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

### `33 function __fast_tokens(input: [u8], start: i32): i32[]`

```fern
<<33 function __fast_tokens(input: [u8], start: i32): i32[]>>=
function __fast_tokens(input: [u8], start: i32): i32[] {
  let head = __filled(32768, -1);
  let out: i32[] = [];
  let pos = 0;
  while (pos < start) {
    if (pos + 2 < input.len()) {
      head = head.with(__hash3(input, pos), pos);
    }
    pos += 1;
  }
  while (pos < input.len()) {
    let length = 0;
    let distance = 0;
    if (pos + 2 < input.len()) {
      let candidate = head[__hash3(input, pos)];
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
    let end = pos + length;
    while (pos < end) {
      if (pos + 2 < input.len()) {
        head = head.with(__hash3(input, pos), pos);
      }
      pos += 1;
    }
  }
  return out;
}

function __filled(n: i32, v: i32): i32[] {
  let out = __alloc_i32(cmp.max(n, 0));
  let i = 0;
  while (i < n) {
    out = out.with(i, v);
    i += 1;
  }
  return out;
}

function __hash3(input: [u8], pos: i32): i32 {
  if (pos + 2 >= input.len()) {
    return 0;
  }
  let h = input[pos] as i32 << 10 ^ input[pos + 1] as i32 << 5 ^ input[pos + 2] as i32;
  return h * 2057 & 32767;
}

function __common(input: [u8], from: i32, pos: i32): i32 {
  return __mismatch_bytes(input, from, input, pos, 258);
}

// The longest match at pos, longer than a held match of 3 or more bytes. A
// search visits at most chain candidates and stops at a match of longest
// bytes. The search for the lazy step visits half as many. Three-byte matches use
// the requested window. If the newest three-byte hash entry is a collision
// and no longer match exists, its chain supplies an earlier matching entry.
@inline
function __longest(input: [u8], pos: i32, c3: i32, cand: i32, prev: i64[], window: i32, held: i32, chain: i32, longest: i32): (i32, i32) {
  let max = cmp.min(258, input.len() - pos);
  let nice = cmp.min(longest, max);
  if (max < 4 || held >= nice) {
    return (0, 0);
  }
  let first = __le32(input, pos);
  let best_len = 0;
  let best_dist = 0;
  let depth = chain;
  if (held >= 3) {
    best_len = held;
    depth = chain >> 1;
  } else if (c3 >= 0 && pos - c3 <= window && (__le32(input, c3) ^ first) << 8 == 0u32) {
    best_len = 3;
    best_dist = pos - c3;
  }
  while (cand >= 0 && pos - cand < window && depth > 0) {
    if (input[cand + best_len] == input[pos + best_len] && __le32(input, cand) == first) {
      let len = __extend(input, cand, pos, max);
      if (len > best_len) {
        best_len = len;
        best_dist = pos - cand;
        if (len >= nice) {
          return (best_len, best_dist);
        }
      }
    }
    cand = prev[cand & window - 1] as i32;
    depth -= 1;
  }
  if (best_len < 3) {
    let distance = __short_match(input, pos, c3, prev, window, first);
    if (distance > 0) {
      return (3, distance);
    }
  }
  return (best_len, best_dist);
}

function __short_match(input: [u8], pos: i32, cand: i32, prev: i64[], window: i32, first: u32): i32 {
  let depth = 35;
  while (cand >= 0 && pos - cand < window && depth > 0) {
    if ((__le32(input, cand) ^ first) << 8 == 0u32) {
      return pos - cand;
    }
    cand = (prev[cand & window - 1] >> 32) as i32;
    depth -= 1;
  }
  return 0;
}

// The length of the match past its first four bytes, eight bytes at a time.
// A call here would keep the chain walk's values out of registers.
@inline
function __extend(input: [u8], cand: i32, pos: i32, max: i32): i32 {
  let len = 4;
  while (len + 8 <= max) {
    let x = __le64(input, cand + len) ^ __le64(input, pos + len);
    if (x != 0u64) {
      return len + (__ctz64(x) >> 3);
    }
    len += 8;
  }
  while (len < max && input[cand + len] == input[pos + len]) {
    len += 1;
  }
  return len;
}
```

### Resumable output

The reader retains encoded input and at most 32768 history bytes.
Each request returns at most 32768 owned output bytes.
Earlier chunks can precede a later stream error. Callers must wait for `done` before they trust a complete stream.

```fern
<<34 resumable output>>=
pub enum InflateReadError { ReadCodec(InflateError), ReadCancelled, ReadRequest, ReadBusy }

impl error.Error for InflateReadError {}

impl cmp.Display for InflateReadError {}

pub function (e: InflateReadError) message(): string {
  return match (e) { ReadCodec(cause) => cause.message(), ReadCancelled => "the decompression request was cancelled", ReadRequest => "the decompression request size is invalid", ReadBusy => "the decompression reader is already active" };
}

pub function (e: InflateReadError) to_string(): string {
  return e.message();
}

pub struct InflateChunk { bytes: u8[], done: boolean, consumed: i32, total: i32, history_bytes: i32 }

pub struct InflateReader { read: (i32, () => boolean) => Result[InflateChunk, InflateReadError] }

@derive(cmp.Eq)
enum __ReadPhase { ReadHeader, ReadStored, ReadCodes, ReadCopy, ReadEnd }

struct __ReadCursor {
  state: __State,
  literal: __Huff,
  distance_code: __Huff,
  history: u8[],
  total: i32,
  phase: __ReadPhase,
  final_block: boolean,
  remaining: i32,
  distance: i32,
  byte: i32,
  cancelled: boolean,
}

pub function inflate_reader(input: u8[], max_out: i32): Result[InflateReader, InflateReadError] {
  if (max_out < 0) {
    return Err(ReadRequest);
  }
  let bits = __State { pos: 0, bitbuf: 0, bitcnt: 0, out: [], max_out: max_out, last: 0, distance: 0, failed: false, error: Truncated, lengths: [] };
  let empty = __Huff { count: [], symbol: [], left: 0 };
  let initial = __ReadCursor {
    state: bits, literal: empty, distance_code: empty, history: [], total: 0, phase: ReadHeader,
    final_block: false, remaining: 0, distance: 0, byte: -1, cancelled: false,
  };
  let state = cell_new(Some(initial));
  let read = (count: i32, cancelled: () => boolean): Result[InflateChunk, InflateReadError] => __read_request(state, input, count, cancelled);
  return Ok(InflateReader { read: read });
}

function __read_request(state: Cell[Option[__ReadCursor]], input: [u8], count: i32, cancelled: () => boolean): Result[InflateChunk, InflateReadError] {
  if (count <= 0 || count > 32768) {
    return Err(ReadRequest);
  }
  let cursor = match (state.get()) { Some(v) => v, None => { return Err(ReadBusy); } };
  state.set(None);
  let result = __read_run(__ReadCursor { ...cursor }, input, count, cancelled);
  let answer = __read_answer(result.0, result.1);
  state.set(Some(result.0));
  return answer;
}

function __read_answer(cursor: __ReadCursor, bytes: u8[]): Result[InflateChunk, InflateReadError] {
  if (cursor.cancelled) {
    return Err(ReadCancelled);
  }
  if (cursor.state.failed) {
    return Err(ReadCodec(cursor.state.error));
  }
  return Ok(InflateChunk { bytes: bytes, done: cursor.phase == ReadEnd, consumed: cursor.state.pos, total: cursor.total, history_bytes: cursor.history.len() });
}

function __read_run(own cursor: __ReadCursor, input: [u8], count: i32, cancelled: () => boolean): (__ReadCursor, u8[]) {
  let output: u8[] = [];
  let steps = 0;
  while (output.len() < count && cursor.phase != ReadEnd && !cursor.state.failed && !cursor.cancelled) {
    if (steps % 256 == 0 && cancelled()) {
      cursor = __ReadCursor { ...cursor, cancelled: true };
    } else {
      cursor = __read_step(cursor, input);
      if (cursor.byte >= 0) {
        output = output.append(cursor.byte as u8);
      }
    }
    steps += 1;
    if (steps == 256) {
      steps = 0;
    }
  }
  return (cursor, output);
}

function __read_step(own cursor: __ReadCursor, input: [u8]): __ReadCursor {
  cursor = __ReadCursor { ...cursor, byte: -1 };
  let phase = cursor.phase;
  return match (phase) { ReadHeader => __read_header(cursor, input), ReadStored => __read_stored_byte(cursor, input), ReadCodes => __read_code(cursor, input), ReadCopy => __read_copy_byte(cursor), ReadEnd => cursor };
}

function __read_header(own cursor: __ReadCursor, input: [u8]): __ReadCursor {
  let state = __bits(__State { ...cursor.state }, input, 1);
  let final_block = state.last == 1;
  state = __bits(state, input, 2);
  cursor = __ReadCursor { ...cursor, state: state, final_block: final_block };
  if (state.failed) {
    return cursor;
  }
  if (state.last == 0) {
    return __read_stored_header(cursor, input);
  }
  if (state.last == 3) {
    return __ReadCursor { ...cursor, state: __fail(state, Malformed("a reserved block type")) };
  }
  let tables = if (state.last == 1) { __fixed_tables(state) } else { __dynamic_tables(state, input) };
  return __ReadCursor { ...cursor, state: tables.state, literal: tables.literal, distance_code: tables.distance, phase: ReadCodes };
}

function __read_stored_header(own cursor: __ReadCursor, input: [u8]): __ReadCursor {
  let state = __State { ...cursor.state, bitbuf: 0, bitcnt: 0 };
  if (state.pos > input.len() - 4) {
    return __ReadCursor { ...cursor, state: __fail(state, Truncated) };
  }
  let length = input[state.pos] as i32 | input[state.pos + 1] as i32 << 8;
  let complement = input[state.pos + 2] as i32 | input[state.pos + 3] as i32 << 8;
  if (length != (complement ^ 65535)) {
    return __ReadCursor { ...cursor, state: __fail(state, Malformed("a stored block's length and its complement disagree")) };
  }
  state = __State { ...state, pos: state.pos + 4 };
  if (length > input.len() - state.pos) {
    return __ReadCursor { ...cursor, state: __fail(state, Truncated) };
  }
  cursor = __ReadCursor { ...cursor, state: state, remaining: length, phase: ReadStored };
  if (length == 0) {
    return __read_block_end(cursor);
  }
  return cursor;
}

function __read_block_end(own cursor: __ReadCursor): __ReadCursor {
  return __ReadCursor { ...cursor, phase: if (cursor.final_block) { ReadEnd } else { ReadHeader } };
}

function __read_stored_byte(own cursor: __ReadCursor, input: [u8]): __ReadCursor {
  let byte = input[cursor.state.pos];
  cursor = __read_emit(cursor, byte);
  cursor = __ReadCursor { ...cursor, state: __State { ...cursor.state, pos: cursor.state.pos + 1 }, remaining: cursor.remaining - 1 };
  if (cursor.remaining == 0) {
    return __read_block_end(cursor);
  }
  return cursor;
}

function __read_emit(own cursor: __ReadCursor, byte: u8): __ReadCursor {
  if (cursor.total >= cursor.state.max_out) {
    return __ReadCursor { ...cursor, state: __fail(cursor.state, OutputLimit) };
  }
  let history = cursor.history;
  cursor = __ReadCursor { ...cursor, history: [] };
  if (history.len() < 32768) {
    history = history.append(byte);
  } else {
    history = history.with(cursor.total & 32767, byte);
  }
  return __ReadCursor { ...cursor, history: history, total: cursor.total + 1, byte: byte as i32 };
}

function __read_code(own cursor: __ReadCursor, input: [u8]): __ReadCursor {
  let state = __decode(__State { ...cursor.state }, input, cursor.literal);
  cursor = __ReadCursor { ...cursor, state: state };
  if (state.failed) {
    return cursor;
  }
  if (state.last < 256) {
    return __read_emit(cursor, state.last as u8);
  }
  if (state.last == 256) {
    return __read_block_end(cursor);
  }
  if (state.last > 285) {
    return __ReadCursor { ...cursor, state: __fail(state, Malformed("a length code past 285")) };
  }
  return __read_match(cursor, input, state.last - 257);
}

function __read_match(own cursor: __ReadCursor, input: [u8], symbol: i32): __ReadCursor {
  let state = __bits(__State { ...cursor.state }, input, LENGTH_EXTRA[symbol]);
  let length = LENGTH_BASE[symbol] + state.last;
  state = __decode(state, input, cursor.distance_code);
  if (state.failed) {
    return __ReadCursor { ...cursor, state: state };
  }
  if (state.last >= 30) {
    return __ReadCursor { ...cursor, state: __fail(state, Malformed("a distance code past 29")) };
  }
  let distance_symbol = state.last;
  state = __bits(state, input, DIST_EXTRA[distance_symbol]);
  let distance = DIST_BASE[distance_symbol] + state.last;
  if (distance > cmp.min(cursor.total, 32768)) {
    state = __fail(state, Malformed("a distance before the available history"));
  }
  return __ReadCursor { ...cursor, state: state, phase: ReadCopy, remaining: length, distance: distance };
}

function __read_copy_byte(own cursor: __ReadCursor): __ReadCursor {
  let byte = cursor.history[cursor.total - cursor.distance & 32767];
  cursor = __read_emit(cursor, byte);
  cursor = __ReadCursor { ...cursor, remaining: cursor.remaining - 1 };
  if (cursor.remaining == 0) {
    return __ReadCursor { ...cursor, phase: ReadCodes };
  }
  return cursor;
}
```
