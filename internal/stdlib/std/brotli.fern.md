# Brotli compression and decompression

This module encodes and decodes Brotli data under RFC 7932.

## Model

The decoder reads the stream through a 64-bit bit buffer. It decodes each prefix code with a lookup table. The decoder writes literals, copies and dictionary words directly into the output. Input is a borrowed byte view.

## Contracts

`max_out` bounds the output size. Truncated input, malformed streams, and output limits produce distinct errors. A successful result owns its decompressed bytes.

A stream that ends before the decoder has the bits it needs is truncated. This rule also applies when the missing bits make the rest of the stream look malformed.

## Module assembly

```fern
<<*>>=
// This module decodes Brotli data under RFC 7932.
import "std/brotli/dictionary";
import "std/brotli/encoder";
import "core/cmp";
import "std/error";

<<Compress a stream>>

<<Errors>>

<<Decompress a stream>>

<<Read bits>>

<<Read meta-block headers>>

<<Build prefix code tables>>

<<Read prefix codes>>

<<Read the meta-block codes>>

<<Decode commands>>

<<Expand dictionary words>>

<<Constant tables>>
```

## Errors

```fern
<<Errors>>=
pub enum BrotliError { BrotliTruncated, BrotliMalformed(string), BrotliTooLarge }

pub function (e: BrotliError) to_string(): string {
  return e.message();
}

impl error.Error for BrotliError {}

impl cmp.Display for BrotliError {}

pub function (e: BrotliError) message(): string {
  match (e) {
    BrotliTruncated => {
      return "brotli: truncated";
    },
    BrotliMalformed(what) => {
      return "brotli: malformed: " + what;
    },
    BrotliTooLarge => {
      return "brotli: output past the limit";
    }
  }
  return "brotli";
}
```

## Decompress a stream

The decoder state `__D` holds the bit reader, the output and the four last distances. It also holds the codes of the current meta-block. The functions that read headers take the state and give it back. A function that reads a value puts it in `v`. A failure sets `failed`, and each later step then gives the state back unchanged.

Before a meta-block, `__reserve` gives the output room for all of its bytes. `pos` counts the input bytes that the bit buffer took. Past the end of the input the buffer takes zero bytes, and `pos` continues to count them. Thus `__past` tells when the decoder used bits that the input does not have.

```fern
<<Decompress a stream>>=
pub function decompress(input: [u8], max_out: i32): Result[u8[], BrotliError] {
  let d: __D = __window(__start(), input);
  let window: i32 = d.v;
  while (!d.failed && !d.last) {
    d = __mb_header(d, input);
    if (!d.failed && __past(d.pos, d.cnt, input.len())) {
      return Err(BrotliTruncated);
    }
    if (!d.failed && d.out.len() + d.v > max_out) {
      return Err(BrotliTooLarge);
    }
    d = __mb_body(d, input, window);
  }
  if (d.failed) {
    return Err(__error(d, input));
  }
  if (__past(d.pos, d.cnt, input.len())) {
    return Err(BrotliTruncated);
  }
  return Ok(d.out);
}

struct __D { pos: i32, buf: u64, cnt: i32, v: i32, failed: boolean, what: string, last: boolean, raw: boolean, lens: i32[], tab: i32[], offs: i32[], cat: __Cat, m: __Mb, out: u8[], cap: i32, r0: i32, r1: i32, r2: i32, r3: i32 }

function __start(): __D {
  let none: u8[] = [];
  let c: __Cat = __Cat { n: 1, types: 0, lens: 0, left: 0 };
  let m: __Mb = __Mb { lit: c, cmd: c, dist: c, btab: [], postfix: 0, direct: 0, modes: [], fixed: [], lmap: [], dmap: [], ltab: [], ctab: [], coff: [], dtab: [], dcodes: [] };
  return __D { pos: 0, buf: 0u64, cnt: 0, v: 0, failed: false, what: "", last: false, raw: false, lens: [], tab: [], offs: [], cat: c, m: m, out: none, cap: 0, r0: 4, r1: 11, r2: 15, r3: 16 };
}

function __fail(own d: __D, what: string): __D {
  if (d.failed) {
    return d;
  }
  return __D { ...d, failed: true, what: what };
}

function __error(d: __D, input: [u8]): BrotliError {
  if (__past(d.pos, d.cnt, input.len())) {
    return BrotliTruncated;
  }
  return BrotliMalformed(d.what);
}

// True when the reader used more bits than the input holds.
@inline
function __past(pos: i32, cnt: i32, n: i32): boolean {
  return pos > n && (pos - n) * 8 > cnt;
}

function __mb_body(own d: __D, input: [u8], window: i32): __D {
  if (d.failed || d.v == 0) {
    return d;
  }
  let end: i32 = d.out.len() + d.v;
  d = __reserve(d, end);
  if (d.raw) {
    return __raw(d, input);
  }
  d = __mb_codes(d, input);
  if (d.failed) {
    return d;
  }
  let m: __Mb = d.m;
  return __commands(d, input, m, end, window);
}

function __raw(own d: __D, input: [u8]): __D {
  let len: i32 = d.v;
  d = __align(d, input);
  if (d.failed) {
    return d;
  }
  let at: i32 = d.pos;
  if (at + len > input.len()) {
    return __fail(__D { ...d, pos: at + len }, "truncated");
  }
  let out: u8[] = d.out;
  d = __D { ...d, out: [] };
  out = __joined(out, __bytes_range(input, at, at + len));
  return __D { ...d, out: out, pos: at + len };
}

// Gives the output room for `n` bytes, so that the appends do not copy it.
// The room at least doubles. Thus a stream of many small meta-blocks copies
// the output a few times only. The copy of a match writes eight bytes at a
// time, so the room has 16 bytes more.
function __reserve(own d: __D, n: i32): __D {
  if (n <= d.cap) {
    return d;
  }
  let want: i32 = cmp.max(n + 16, d.cap * 2);
  let room: u8[] = __alloc_u8(want);
  __arr_set_len(room, 0);
  let old: u8[] = d.out;
  d = __D { ...d, out: [] };
  return __D { ...d, out: __joined(room, old), cap: want };
}

function __joined(own out: u8[], more: u8[]): u8[] {
  let i: i32 = 0;
  while (i < more.len()) {
    out = out.append(more[i]);
    i = i + 1;
  }
  return out;
}
```

## Read bits

The bit buffer holds `cnt` unread bits, the least significant bit first. Bits above `cnt` are zero or are the next bits of the input. A refill reads eight bytes at once while eight bytes are left. Near the end `__tail` reads one byte at a time, and past the end it reads zero bytes. Each refill leaves at least 56 bits.

The refill functions are inline, and the compiler keeps their result in registers. Thus the decoder loop can use them without an allocation.

```fern
<<Read bits>>=
struct __Fill { pos: i32, buf: u64, cnt: i32 }

// Refills the bit buffer. `fast` is the last position that has eight input
// bytes after it. The test of `pos >= 0` lets the compiler drop the bounds
// check of the eight-byte read.
@inline
function __refill(data: [u8], pos: i32, buf: u64, cnt: i32, fast: i32): __Fill {
  if (pos >= 0 && pos <= fast) {
    return __Fill { pos: pos + (63 - cnt >> 3), buf: buf | __le64(data, pos) << cnt as u64, cnt: cnt | 56 };
  }
  let t: __Fill = __tail(data, pos, buf, cnt);
  return __Fill { pos: t.pos, buf: t.buf, cnt: t.cnt };
}

// Refills the bit buffer when it holds fewer than `k` bits.
@inline
function __ensure(data: [u8], pos: i32, buf: u64, cnt: i32, fast: i32, k: i32): __Fill {
  if (cnt >= k) {
    return __Fill { pos: pos, buf: buf, cnt: cnt };
  }
  return __refill(data, pos, buf, cnt, fast);
}

function __tail(data: [u8], pos: i32, buf: u64, cnt: i32): __Fill {
  let n: i32 = data.len();
  while (cnt < 56) {
    if (pos < n) {
      buf = buf | data[pos] as u64 << cnt as u64;
    }
    pos = pos + 1;
    cnt = cnt + 8;
  }
  return __Fill { pos: pos, buf: buf, cnt: cnt };
}

@inline
function __le64(data: [u8], at: i32): u64 {
  return data[at] as u64 | data[at + 1] as u64 << 8 | data[at + 2] as u64 << 16 | data[at + 3] as u64 << 24
    | data[at + 4] as u64 << 32 | data[at + 5] as u64 << 40 | data[at + 6] as u64 << 48 | data[at + 7] as u64 << 56;
}

function __need(own d: __D, data: [u8]): __D {
  if (d.cnt >= 32) {
    return d;
  }
  let f: __Fill = __refill(data, d.pos, d.buf, d.cnt, data.len() - 8);
  return __D { ...d, pos: f.pos, buf: f.buf, cnt: f.cnt };
}

// Reads `k` bits, at most 32, into `v`.
function __get(own d: __D, data: [u8], k: i32): __D {
  d = __need(d, data);
  let v: i32 = (d.buf & (1u64 << k as u64) - 1u64) as i32;
  return __D { ...d, buf: d.buf >> k as u64, cnt: d.cnt - k, v: v };
}

// Reads one symbol of the code at `at` in `tab` into `v`.
function __sym(own d: __D, data: [u8], tab: i32[], at: i32): __D {
  d = __need(d, data);
  let e: i32 = __entry(tab, at, d.buf);
  return __D { ...d, buf: d.buf >> (e & 15) as u64, cnt: d.cnt - (e & 15), v: e >> 16 };
}

// Drops the bits up to the next byte boundary, which must be zero. The
// buffer starts at a byte boundary, so those bits are `cnt & 7`.
function __align(own d: __D, data: [u8]): __D {
  let pad: i32 = d.cnt & 7;
  d = __get(d, data, pad);
  if (d.v != 0) {
    return __fail(d, "padding");
  }
  return __D { ...d, pos: d.pos - (d.cnt >> 3), buf: 0u64, cnt: 0 };
}
```

## Read meta-block headers

`__window` gives the size of the history window. `__mb_header` sets `last` and `raw`, and puts the meta-block length in `v`. A metadata block has length zero. The header function skips its bytes.

```fern
<<Read meta-block headers>>=
function __window(own d: __D, data: [u8]): __D {
  d = __get(d, data, 1);
  let bits: i32 = 16;
  if (d.v == 1) {
    d = __get(d, data, 3);
    bits = 17 + d.v;
    if (d.v == 0) {
      d = __get(d, data, 3);
      bits = 8 + d.v;
      if (d.v == 0) {
        bits = 17;
      }
      if (d.v == 1) {
        return __fail(d, "window size");
      }
    }
  }
  return __D { ...d, v: (1 << bits) - 16 };
}

function __mb_header(own d: __D, data: [u8]): __D {
  d = __get(d, data, 1);
  let last: boolean = d.v == 1;
  if (last) {
    d = __get(d, data, 1);
    if (d.v == 1) {
      return __D { ...d, last: true, raw: false, v: 0 };
    }
  }
  d = __get(d, data, 2);
  let nibbles: i32 = [4, 5, 6, 0][d.v];
  if (nibbles == 0) {
    return __metadata(d, data, last);
  }
  d = __get(d, data, nibbles * 4);
  let len: i32 = d.v + 1;
  if (nibbles > 4 && len - 1 >> (nibbles - 1) * 4 == 0) {
    return __fail(d, "meta-block length");
  }
  let raw: boolean = false;
  if (!last) {
    d = __get(d, data, 1);
    raw = d.v == 1;
  }
  return __D { ...d, last: last, raw: raw, v: len };
}

function __metadata(own d: __D, data: [u8], last: boolean): __D {
  d = __get(d, data, 1);
  if (last || d.v != 0) {
    return __fail(d, "metadata block");
  }
  d = __get(d, data, 2);
  let bytes: i32 = d.v;
  let skip: i32 = 0;
  if (bytes > 0) {
    d = __get(d, data, bytes * 8);
    skip = d.v + 1;
    if (bytes > 1 && skip - 1 >> (bytes - 1) * 8 == 0) {
      return __fail(d, "metadata length");
    }
  }
  d = __align(d, data);
  return __D { ...d, pos: d.pos + skip, last: false, raw: false, v: 0 };
}

function __varlen(own d: __D, data: [u8]): __D {
  d = __get(d, data, 1);
  if (d.v == 0) {
    return d;
  }
  d = __get(d, data, 3);
  let n: i32 = d.v;
  if (n == 0) {
    return __D { ...d, v: 1 };
  }
  d = __get(d, data, n);
  return __D { ...d, v: d.v + (1 << n) };
}
```

## Build prefix code tables

A code has a root table of 256 entries that the next eight bits index. The code-length code has no code longer than five bits, so its root table has 32 entries and no links.

An entry holds the code length in bits 0 to 3 and the symbol from bit 16. A code longer than eight bits has a link in the root table. The link holds eight plus the bit count of its second-level table. It also holds the offset of that table from the root. The second-level entries hold the code length less eight.

A code with one symbol uses no bits. The callers check that all other codes are complete before they build a table, so each entry is set. When no code is longer than `n` bits, the root table repeats every `1 << n` entries. The builder thus writes the first `1 << n` entries and copies them.

```fern
<<Build prefix code tables>>=
// Appends the table of the code with the lengths `lens` to `tab`. Its root
// has `1 << root` entries, and only a root of 8 bits has links.
function __table(lens: i32[], own tab: i32[], root: i32): i32[] {
  let at: i32 = tab.len();
  let count: i32[] = __alloc_i32(16);
  let only: i32 = 0;
  let used: i32 = 0;
  let longest: i32 = 0;
  let s: i32 = 0;
  while (s < lens.len()) {
    let l: i32 = lens[s];
    if (l > 0) {
      count = count.with(l, count[l] + 1);
      only = s;
      used = used + 1;
      longest = cmp.max(longest, l);
    }
    s = s + 1;
  }
  if (used <= 1) {
    return tab.concat(__doubled([only << 16], 1 << root));
  }
  let sorted: i32[] = __sorted(lens, count, used);
  let fill: i32 = cmp.min(longest, root);
  tab = tab.concat(__doubled(__root(__alloc_i32(1 << fill), count, sorted, fill), 1 << root));
  if (fill < 8) {
    return tab;
  }
  return __second(tab, at, count, sorted);
}

// Repeats the entries of `r` until it has `n` of them. A root table built
// for codes of at most `fill` bits repeats every `1 << fill` entries.
function __doubled(own r: i32[], n: i32): i32[] {
  while (r.len() < n) {
    r = r.concat(r);
  }
  return r;
}

// The symbols with a code, ordered by code length and then by symbol.
function __sorted(lens: i32[], count: i32[], used: i32): i32[] {
  let next: i32[] = __alloc_i32(16);
  let len: i32 = 1;
  while (len < 15) {
    next = next.with(len + 1, next[len] + count[len]);
    len = len + 1;
  }
  let sorted: i32[] = __alloc_i32(used);
  let s: i32 = 0;
  while (s < lens.len()) {
    let l: i32 = lens[s];
    if (l > 0) {
      sorted = sorted.with(next[l], s);
      next = next.with(l, next[l] + 1);
    }
    s = s + 1;
  }
  return sorted;
}

function __root(own tab: i32[], count: i32[], sorted: i32[], root: i32): i32[] {
  let code: i32 = 0;
  let k: i32 = 0;
  let len: i32 = 1;
  while (len <= root) {
    let i: i32 = 0;
    while (i < count[len]) {
      let entry: i32 = sorted[k] << 16 | len;
      let j: i32 = encoder.reverse(code, len);
      while (j < 1 << root) {
        tab = tab.with(j, entry);
        j = j + (1 << len);
      }
      code = code + 1;
      k = k + 1;
      i = i + 1;
    }
    code = code << 1;
    len = len + 1;
  }
  return tab;
}

// Adds the second-level tables. Codes with the same first eight bits are
// next to each other in canonical order, so they share one table.
function __second(own tab: i32[], at: i32, count: i32[], sorted: i32[]): i32[] {
  let left: i32[] = count;
  let code: i32 = 0;
  let k: i32 = 0;
  let len: i32 = 1;
  while (len <= 8) {
    code = code + count[len] << 1;
    k = k + count[len];
    len = len + 1;
  }
  let low: i32 = 0 - 1;
  let sub: i32 = 0;
  let bits: i32 = 0;
  while (len <= 15) {
    while (left[len] > 0) {
      let rev: i32 = encoder.reverse(code, len);
      if ((rev & 255) != low) {
        low = rev & 255;
        bits = __sub_bits(left, len);
        sub = tab.len();
        tab = tab.concat(__alloc_i32(1 << bits));
        tab = tab.with(at + low, sub - at << 16 | 8 + bits);
      }
      let entry: i32 = sorted[k] << 16 | len - 8;
      let j: i32 = rev >> 8;
      while (j < 1 << bits) {
        tab = tab.with(sub + j, entry);
        j = j + (1 << len - 8);
      }
      left = left.with(len, left[len] - 1);
      code = code + 1;
      k = k + 1;
    }
    code = code << 1;
    len = len + 1;
  }
  return tab;
}

// The bit count of the second-level table that starts with a code of
// length `len`. `left` counts the codes that are not yet in a table.
function __sub_bits(left: i32[], len: i32): i32 {
  let room: i32 = 1 << len - 8;
  while (len < 15) {
    room = room - left[len];
    if (room <= 0) {
      return len - 8;
    }
    len = len + 1;
    room = room << 1;
  }
  return len - 8;
}

// The entry for the next bits in `buf`. Its length field is the full code
// length, also for a code with a second-level entry.
@inline
function __entry(tab: i32[], at: i32, buf: u64): i32 {
  let e: i32 = tab[at + (buf & 255u64) as i32];
  if ((e & 15) <= 8) {
    return e;
  }
  let mask: u64 = (1u64 << ((e & 15) - 8) as u64) - 1u64;
  return tab[at + (e >> 16) + (buf >> 8u64 & mask) as i32] + 8;
}
```

## Read prefix codes

`__read_lens` reads one prefix code and puts the code length of each symbol in `lens`. A simple code lists up to four symbols. A complex code first gives the code lengths of the code-length alphabet. Then it gives the code lengths of the symbols with that code. The reader refuses a complex code that is not complete.

```fern
<<Read prefix codes>>=
function __read_lens(own d: __D, data: [u8], size: i32): __D {
  d = __get(d, data, 2);
  let hskip: i32 = d.v;
  if (hskip == 1) {
    return __simple(d, data, size);
  }
  return __complex(d, data, hskip, size);
}

function __simple(own d: __D, data: [u8], size: i32): __D {
  d = __get(d, data, 2);
  let n: i32 = d.v + 1;
  let width: i32 = 32 - __clz32((size - 1) as u32);
  let syms: i32[] = [];
  while (syms.len() < n) {
    d = __get(d, data, width);
    if (d.v >= size || __has(syms, d.v)) {
      return __fail(d, "simple prefix code");
    }
    syms = syms.append(d.v);
  }
  let shape: i32[] = [[1], [1, 1], [1, 2, 2], [2, 2, 2, 2]][n - 1];
  if (n == 4) {
    d = __get(d, data, 1);
    if (d.v == 1) {
      shape = [1, 2, 3, 3];
    }
  }
  let lens: i32[] = __alloc_i32(size);
  let k: i32 = 0;
  while (k < n) {
    lens = lens.with(syms[k], shape[k]);
    k = k + 1;
  }
  return __D { ...d, lens: lens };
}

function __has(xs: i32[], x: i32): boolean {
  for y in xs {
    if (y == x) {
      return true;
    }
  }
  return false;
}

function __complex(own d: __D, data: [u8], hskip: i32, size: i32): __D {
  let lens: i32[] = __alloc_i32(18);
  let space: i32 = 32;
  let used: i32 = 0;
  let i: i32 = hskip;
  while (i < 18 && space > 0) {
    d = __need(d, data);
    let v: i32 = CL_FIXED[(d.buf & 15u64) as i32];
    d = __get(d, data, v >> 16);
    let len: i32 = v & 0xFFFF;
    lens = lens.with(CL_ORDER[i], len);
    if (len != 0) {
      space = space - (32 >> len);
      used = used + 1;
    }
    i = i + 1;
  }
  if (space != 0 && used != 1) {
    return __fail(d, "code length code");
  }
  return __lengths(d, data, __table(lens, [], 5), size);
}

// Reads the code lengths of `size` symbols with the code-length code `clc`.
// That code has no code longer than five bits.
function __lengths(own d: __D, data: [u8], clc: i32[], size: i32): __D {
  let lens: i32[] = __alloc_i32(size);
  let k: i32 = 0;
  let prev: i32 = 8;
  let repeat: i32 = 0;
  let rlen: i32 = 0;
  let space: i32 = 32768;
  while (k < size && space > 0) {
    d = __need(d, data);
    let e: i32 = clc[(d.buf & 31u64) as i32];
    d = __D { ...d, buf: d.buf >> (e & 15) as u64, cnt: d.cnt - (e & 15) };
    let c: i32 = e >> 16;
    if (c < 16) {
      if (c != 0) {
        lens = lens.with(k, c);
        prev = c;
        space = space - (32768 >> c);
      }
      k = k + 1;
      repeat = 0;
    } else {
      let extra: i32 = c - 14;
      let len: i32 = __repeat_value(c, prev);
      if (rlen != len) {
        repeat = 0;
      }
      let old: i32 = repeat;
      if (repeat > 0) {
        repeat = repeat - 2 << extra;
      }
      d = __get(d, data, extra);
      repeat = repeat + d.v + 3;
      let more: i32 = repeat - old;
      if (k + more > size) {
        return __fail(d, "code lengths");
      }
      lens = __set_run(lens, k, more, len);
      k = k + more;
      if (len != 0) {
        space = space - (more << 15 - len);
      }
      rlen = len;
    }
  }
  if (space != 0) {
    return __fail(d, "incomplete prefix code");
  }
  return __D { ...d, lens: lens };
}

// Code 16 repeats the last length that is not zero, and code 17 repeats zero.
@inline
function __repeat_value(c: i32, prev: i32): i32 {
  if (c == 16) {
    return prev;
  }
  return 0;
}

function __set_run(own lens: i32[], at: i32, n: i32, value: i32): i32[] {
  if (value == 0) {
    return lens;
  }
  let i: i32 = 0;
  while (i < n) {
    lens = lens.with(at + i, value);
    i = i + 1;
  }
  return lens;
}
```

## Read the meta-block codes

A compressed meta-block gives the block types and block lengths of the three categories: literals, commands and distances. Then it gives the context modes, the two context maps and the prefix codes of each category.

The codes of one category share one table. A context map entry holds the offset of its code in that table, so a literal needs no other lookup. The block-switch codes of all three categories share the table `btab`. The table `dcodes` gives the base distance and the extra bit count of each distance code from 16. These values depend only on the meta-block header.

```fern
<<Read the meta-block codes>>=
struct __Cat { n: i32, types: i32, lens: i32, left: i32 }

struct __Mb { lit: __Cat, cmd: __Cat, dist: __Cat, btab: i32[], postfix: i32, direct: i32, modes: i32[], fixed: i32[], lmap: i32[], dmap: i32[], ltab: i32[], ctab: i32[], coff: i32[], dtab: i32[], dcodes: i64[] }

function __mb_codes(own d: __D, data: [u8]): __D {
  d = __D { ...d, tab: [] };
  d = __cat(d, data);
  let lit: __Cat = d.cat;
  d = __cat(d, data);
  let cmd: __Cat = d.cat;
  d = __cat(d, data);
  let dist: __Cat = d.cat;
  let btab: i32[] = d.tab;
  d = __get(d, data, 2);
  let postfix: i32 = d.v;
  d = __get(d, data, 4);
  let direct: i32 = d.v << postfix;
  let modes: i32[] = [];
  while (modes.len() < lit.n) {
    d = __get(d, data, 2);
    modes = modes.append(d.v << 9);
  }
  d = __cmap(d, data, 64 * lit.n);
  let lmap: i32[] = d.lens;
  let ltrees: i32 = d.v;
  d = __cmap(d, data, 4 * dist.n);
  let dmap: i32[] = d.lens;
  let dtrees: i32 = d.v;
  d = __group(d, data, ltrees, 256);
  let ltab: i32[] = d.tab;
  let loff: i32[] = d.offs;
  d = __group(d, data, cmd.n, 704);
  let ctab: i32[] = d.tab;
  let coff: i32[] = d.offs;
  let dsize: i32 = 16 + direct + (48 << postfix);
  d = __group(d, data, dtrees, dsize);
  let dtab: i32[] = d.tab;
  if (d.failed) {
    return d;
  }
  lmap = __mapped(lmap, loff);
  dmap = __mapped(dmap, d.offs);
  let m: __Mb = __Mb { lit: lit, cmd: cmd, dist: dist, btab: btab, postfix: postfix, direct: direct, modes: modes, fixed: __fixed_trees(lmap, lit.n), lmap: lmap, dmap: dmap, ltab: ltab, ctab: ctab, coff: coff, dtab: dtab, dcodes: __dist_codes(direct, postfix, dsize) };
  return __D { ...d, lens: [], tab: [], offs: [], m: m };
}

// Reads the block types of one category, with the codes of its block
// switches added to `d.tab`.
function __cat(own d: __D, data: [u8]): __D {
  d = __varlen(d, data);
  let n: i32 = d.v + 1;
  if (d.failed || n == 1) {
    return __D { ...d, cat: __Cat { n: 1, types: 0, lens: 0, left: 1 << 28 } };
  }
  let tab: i32[] = d.tab;
  d = __D { ...d, tab: [] };
  d = __read_lens(d, data, n + 2);
  let types: i32 = tab.len();
  tab = __built(tab, d);
  d = __read_lens(d, data, 26);
  let lens: i32 = tab.len();
  tab = __built(tab, d);
  if (d.failed) {
    return __D { ...d, cat: __Cat { n: 1, types: 0, lens: 0, left: 1 << 28 } };
  }
  d = __sym(d, data, tab, lens);
  let s: i32 = d.v;
  d = __get(d, data, BL_EXTRA[s]);
  return __D { ...d, tab: tab, cat: __Cat { n: n, types: types, lens: lens, left: BL_OFFSET[s] + d.v } };
}

// Adds the table of the code that `d.lens` holds, unless a read failed.
function __built(own tab: i32[], d: __D): i32[] {
  if (d.failed) {
    return tab;
  }
  return __table(d.lens, tab, 8);
}

// Reads `n` codes of `size` symbols into `d.tab`, with the offset of each code in `d.offs`.
function __group(own d: __D, data: [u8], n: i32, size: i32): __D {
  let tab: i32[] = [];
  let offs: i32[] = [];
  d = __D { ...d, tab: [], offs: [] };
  while (offs.len() < n && !d.failed) {
    d = __read_lens(d, data, size);
    offs = offs.append(tab.len());
    tab = __built(tab, d);
  }
  return __D { ...d, tab: tab, offs: offs };
}

// Reads a context map of `size` entries into `d.lens`, and its number of
// codes into `v`. Each entry is less than that number.
function __cmap(own d: __D, data: [u8], size: i32): __D {
  d = __varlen(d, data);
  let trees: i32 = d.v + 1;
  if (trees == 1) {
    return __D { ...d, lens: __alloc_i32(size), v: 1 };
  }
  d = __get(d, data, 1);
  let rle: i32 = 0;
  if (d.v == 1) {
    d = __get(d, data, 4);
    rle = d.v + 1;
  }
  d = __read_lens(d, data, trees + rle);
  if (d.failed) {
    return d;
  }
  let code: i32[] = __table(d.lens, [], 8);
  let map: i32[] = __alloc_i32(size);
  let k: i32 = 0;
  while (k < size) {
    d = __sym(d, data, code, 0);
    let s: i32 = d.v;
    if (s == 0 || s > rle) {
      map = map.with(k, cmp.max(s - rle, 0));
      k = k + 1;
    } else {
      d = __get(d, data, s);
      k = k + (1 << s) + d.v;
      if (k > size) {
        return __fail(d, "context map");
      }
    }
  }
  d = __get(d, data, 1);
  if (d.v == 1) {
    map = __inverse_mtf(map);
  }
  return __D { ...d, lens: map, v: trees };
}

// For each literal block type, the code of all its contexts, or -1 when
// the contexts use more than one code.
function __fixed_trees(lmap: i32[], n: i32): i32[] {
  let out: i32[] = __alloc_i32(n);
  let t: i32 = 0;
  while (t < n) {
    let code: i32 = lmap[t << 6];
    let i: i32 = 1;
    while (i < 64 && lmap[(t << 6) + i] == code) {
      i = i + 1;
    }
    if (i < 64) {
      code = 0 - 1;
    }
    out = out.with(t, code);
    t = t + 1;
  }
  return out;
}

function __mapped(own map: i32[], offs: i32[]): i32[] {
  let i: i32 = 0;
  while (i < map.len()) {
    map = map.with(i, offs[map[i]]);
    i = i + 1;
  }
  return map;
}

function __inverse_mtf(own v: i32[]): i32[] {
  let mtf: i32[] = __alloc_i32(256);
  let i: i32 = 0;
  while (i < 256) {
    mtf = mtf.with(i, i);
    i = i + 1;
  }
  i = 0;
  while (i < v.len()) {
    let idx: i32 = v[i];
    let value: i32 = mtf[idx];
    v = v.with(i, value);
    let k: i32 = idx;
    while (k > 0) {
      mtf = mtf.with(k, mtf[k - 1]);
      k = k - 1;
    }
    mtf = mtf.with(0, value);
    i = i + 1;
  }
  return v;
}
```

## Decode commands

`__commands` decodes the commands of one compressed meta-block. It keeps the bit buffer, the block state of the three categories and the last distances in local values. A block switch is rare, so `__switch` does it in a separate function.

A command symbol selects the base and the extra bit count of its insert length and of its copy length. The table `COMMAND` holds all four for each symbol. The decoder then decodes the literals into the output, and then the distance. A distance past the history selects a dictionary word.

```fern
<<Decode commands>>=
// The bit buffer and the block state of one category after a block switch.
struct __Sw { buf: u64, cnt: i32, cur: i32, prev: i32, left: i32 }

function __commands(own d: __D, data: [u8], m: __Mb, end: i32, window: i32): __D {
  let out: u8[] = d.out;
  let pos: i32 = d.pos;
  let buf: u64 = d.buf;
  let cnt: i32 = d.cnt;
  let r0: i32 = d.r0;
  let r1: i32 = d.r1;
  let r2: i32 = d.r2;
  let r3: i32 = d.r3;
  d = __D { ...d, out: [] };
  let n: i32 = data.len();
  let fast: i32 = n - 8;
  let ltab: i32[] = m.ltab;
  let lmap: i32[] = m.lmap;
  let ctab: i32[] = m.ctab;
  let dtab: i32[] = m.dtab;
  let dmap: i32[] = m.dmap;
  let dcodes: i64[] = m.dcodes;
  let postfix: i32 = m.postfix;
  let lt: i32 = 0;
  let lp: i32 = 1;
  let ll: i32 = m.lit.left;
  let lbase: i32 = 0;
  let lmode: i32 = m.modes[0];
  let lfix: i32 = m.fixed[0];
  let ct: i32 = 0;
  let cp: i32 = 1;
  let cl: i32 = m.cmd.left;
  let dt: i32 = 0;
  let dp: i32 = 1;
  let dl: i32 = m.dist.left;
  let p1: i32 = __back(out, 1);
  let p2: i32 = __back(out, 2);
  let bad: i32 = 0;
  while (__more(out.len(), end, pos, cnt, n)) {
    let f: __Fill = __refill(data, pos, buf, cnt, fast);
    let cs: __Sw = __next_block(f.buf, f.cnt, m.btab, m.cmd, ct, cp, cl);
    ct = cs.cur;
    cp = cs.prev;
    cl = cs.left;
    f = __ensure(data, f.pos, cs.buf, cs.cnt, fast, 39);
    let e: i32 = __entry(ctab, m.coff[ct], f.buf);
    buf = f.buf >> (e & 15) as u64;
    cnt = f.cnt - (e & 15);
    let sym: i32 = e >> 16;
    let c: i64 = COMMAND[sym];
    let ix: i32 = (c >> 24 & 255i64) as i32;
    let insert: i32 = (c & 16777215i64) as i32 + (buf & (1u64 << ix as u64) - 1u64) as i32;
    f = __ensure(data, f.pos, buf >> ix as u64, cnt - ix, fast, 24);
    pos = f.pos;
    let cx: i32 = (c >> 56) as i32;
    let copy: i32 = (c >> 32 & 16777215i64) as i32 + (f.buf & (1u64 << cx as u64) - 1u64) as i32;
    buf = f.buf >> cx as u64;
    cnt = f.cnt - cx;
    if (out.len() + insert > end) {
      bad = 1;
      break;
    }
    let k: i32 = insert;
    while (k > 0) {
      f = __ensure(data, pos, buf, cnt, fast, 15);
      if (ll == 0) {
        f = __refill(data, f.pos, f.buf, f.cnt, fast);
        let ls: __Sw = __switch(f.buf, f.cnt, m.btab, m.lit, lt, lp);
        f = __ensure(data, f.pos, ls.buf, ls.cnt, fast, 15);
        lp = ls.prev;
        lt = ls.cur;
        ll = ls.left;
        lbase = lt << 6;
        lmode = m.modes[lt];
        lfix = m.fixed[lt];
      }
      ll = ll - 1;
      let le: i32 = __entry(ltab, __literal_code(lfix, lmap, lbase, lmode, p1, p2), f.buf);
      pos = f.pos;
      buf = f.buf >> (le & 15) as u64;
      cnt = f.cnt - (le & 15);
      p2 = p1;
      p1 = le >> 16;
      out = out.append(p1 as u8);
      k = k - 1;
    }
    if (out.len() >= end) {
      break;
    }
    let code: i32 = 0;
    let dist: i32 = r0;
    if (sym >= 128) {
      f = __refill(data, pos, buf, cnt, fast);
      let ds: __Sw = __next_block(f.buf, f.cnt, m.btab, m.dist, dt, dp, dl);
      dt = ds.cur;
      dp = ds.prev;
      dl = ds.left;
      f = __ensure(data, f.pos, ds.buf, ds.cnt, fast, 39);
      let de: i32 = __entry(dtab, dmap[(dt << 2) + __dist_context(copy)], f.buf);
      pos = f.pos;
      buf = f.buf >> (de & 15) as u64;
      cnt = f.cnt - (de & 15);
      code = de >> 16;
      let nb: i32 = __extra_bits(code, dcodes);
      dist = __distance(code, dcodes, (buf & (1u64 << nb as u64) - 1u64) as i32 << postfix, r0, r1, r2, r3);
      buf = buf >> nb as u64;
      cnt = cnt - nb;
      if (dist <= 0) {
        bad = 2;
        break;
      }
    }
    let max: i32 = __min(out.len(), window);
    if (dist > max) {
      bad = __word_check(dist - max - 1, copy, end - out.len());
      if (bad != 0) {
        break;
      }
      out = __word(out, dist - max - 1, copy);
      p1 = __back(out, 1);
      p2 = __back(out, 2);
    } else {
      if (out.len() + copy > end) {
        bad = 1;
        break;
      }
      out = __copy(out, copy, dist);
      p1 = out[out.len() - 1] as i32;
      p2 = out[out.len() - 2] as i32;
      if (code != 0) {
        r3 = r2;
        r2 = r1;
        r1 = r0;
        r0 = dist;
      }
    }
  }
  return __ended(__D { ...d, out: out, pos: pos, buf: buf, cnt: cnt, r0: r0, r1: r1, r2: r2, r3: r3 }, bad);
}

// True while the meta-block has bytes to decode and the reader has not
// used bits past the input. A stream that ends early stops here, and the
// next check of `__past` reports it.
@inline
function __more(len: i32, end: i32, pos: i32, cnt: i32, n: i32): boolean {
  return len < end && !__past(pos, cnt, n);
}

function __ended(own d: __D, bad: i32): __D {
  if (bad == 0) {
    return d;
  }
  return __fail(d, ["", "command past the meta-block", "distance", "dictionary word length", "dictionary transform"][bad]);
}

// The offset of the code of the next literal. A block type whose contexts
// all use one code needs no context.
@inline
function __literal_code(fixed: i32, lmap: i32[], lbase: i32, lmode: i32, p1: i32, p2: i32): i32 {
  if (fixed >= 0) {
    return fixed;
  }
  return lmap[lbase + (CONTEXT[lmode + p1] as i32 | CONTEXT[lmode + 256 + p2] as i32)];
}

@inline
function __min(a: i32, b: i32): i32 {
  if (a < b) {
    return a;
  }
  return b;
}

// Counts down the block of a category, and decodes a block switch when the
// block ends. `buf` holds at least 54 bits.
@inline
function __next_block(buf: u64, cnt: i32, btab: i32[], c: __Cat, cur: i32, prev: i32, left: i32): __Sw {
  if (left > 0) {
    return __Sw { buf: buf, cnt: cnt, cur: cur, prev: prev, left: left - 1 };
  }
  let s: __Sw = __switch(buf, cnt, btab, c, cur, prev);
  return __Sw { buf: s.buf, cnt: s.cnt, cur: s.cur, prev: s.prev, left: s.left - 1 };
}

// Decodes a block switch: the new block type and the length of its block.
// The two symbols and the extra bits take at most 54 bits of `buf`.
function __switch(buf: u64, cnt: i32, btab: i32[], c: __Cat, cur: i32, prev: i32): __Sw {
  let e: i32 = __entry(btab, c.types, buf);
  let b: u64 = buf >> (e & 15) as u64;
  let k: i32 = cnt - (e & 15);
  let t: i32 = (e >> 16) - 2;
  if (e >> 16 == 0) {
    t = prev;
  } else if (e >> 16 == 1) {
    t = cur + 1;
  }
  if (t >= c.n) {
    t = t - c.n;
  }
  let l: i32 = __entry(btab, c.lens, b);
  b = b >> (l & 15) as u64;
  k = k - (l & 15);
  let x: i32 = BL_EXTRA[l >> 16];
  let left: i32 = BL_OFFSET[l >> 16] + (b & (1u64 << x as u64) - 1u64) as i32;
  return __Sw { buf: b >> x as u64, cnt: k - x, cur: t, prev: cur, left: left };
}

@inline
function __dist_context(copy: i32): i32 {
  if (copy > 4) {
    return 3;
  }
  return copy - 2;
}

@inline
function __extra_bits(code: i32, dcodes: i64[]): i32 {
  if (code < 16) {
    return 0;
  }
  return (dcodes[code] & 31i64) as i32;
}

// The distance of `code`. `extra` is its extra bits, shifted to their place.
// A code below 16 gives a change to one of the four last distances.
@inline
function __distance(code: i32, dcodes: i64[], extra: i32, r0: i32, r1: i32, r2: i32, r3: i32): i32 {
  if (code >= 16) {
    return (dcodes[code] >> 5) as i32 + extra;
  }
  let last: i32 = r0;
  let idx: i32 = DIST_RING[code];
  if (idx == 1) {
    last = r1;
  } else if (idx == 2) {
    last = r2;
  } else if (idx == 3) {
    last = r3;
  }
  return last + DIST_DELTA[code] - 3;
}

// For each distance code from 16: the distance with zero extra bits from
// bit 5, and the number of extra bits in bits 0 to 4.
function __dist_codes(direct: i32, postfix: i32, size: i32): i64[] {
  let t: i64[] = __alloc_i64(size);
  let code: i32 = 16;
  while (code < size) {
    let x: i32 = code - direct - 16;
    let base: i32 = code - 15;
    let nb: i32 = 0;
    if (x >= 0) {
      let h: i32 = x >> postfix;
      nb = 1 + (h >> 1);
      base = ((2 + (h & 1) << nb) - 4 << postfix) + (x & (1 << postfix) - 1) + direct + 1;
    }
    t = t.with(code, base as i64 << 5 | nb as i64);
    code = code + 1;
  }
  return t;
}

function __back(out: u8[], k: i32): i32 {
  if (out.len() < k) {
    return 0;
  }
  return out[out.len() - k] as i32;
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

## Expand dictionary words

A distance past the history selects word `id` of length `len` in the static dictionary. The low bits of `id` select the word, and the high bits select one of 121 transforms. A transform adds a prefix and a suffix. It can also remove bytes from either end of the word, or change letters to upper case.

```fern
<<Expand dictionary words>>=
// Zero for a word that exists and fits in `room` bytes, or the code of the
// failure.
function __word_check(id: i32, len: i32, room: i32): i32 {
  if (len < 4 || len > 24) {
    return 3;
  }
  let tr: i32 = id >> DICT_BITS[len];
  if (tr >= 121) {
    return 4;
  }
  let kind: i32 = TRANSFORM_KIND[tr];
  if (AFFIX_AT[2 * tr + 2] - AFFIX_AT[2 * tr] + len - __head_cut(kind, len) - __tail_cut(kind, len) > room) {
    return 1;
  }
  return 0;
}

// The number of bytes that transform `kind` removes from the start of a word.
@inline
function __head_cut(kind: i32, len: i32): i32 {
  if (kind >= 12) {
    return cmp.min(kind - 11, len);
  }
  return 0;
}

// The number of bytes that transform `kind` removes from the end of a word.
@inline
function __tail_cut(kind: i32, len: i32): i32 {
  if (kind >= 1 && kind <= 9) {
    return cmp.min(kind, len);
  }
  return 0;
}

function __word(own out: u8[], id: i32, len: i32): u8[] {
  let bits: i32 = DICT_BITS[len];
  let tr: i32 = id >> bits;
  let at: i32 = DICT_OFFSET[len] + (id & (1 << bits) - 1) * len;
  out = __affix(out, 2 * tr);
  let kind: i32 = TRANSFORM_KIND[tr];
  let from: i32 = at + __head_cut(kind, len);
  let to: i32 = at + len - __tail_cut(kind, len);
  let start: i32 = out.len();
  let i: i32 = from;
  while (i < to) {
    out = out.append(dictionary.WORDS[i]);
    i = i + 1;
  }
  if (kind == 10 || kind == 11) {
    out = __upper(out, start, kind == 10);
  }
  return __affix(out, 2 * tr + 1);
}

function __affix(own out: u8[], k: i32): u8[] {
  let i: i32 = AFFIX_AT[k];
  while (i < AFFIX_AT[k + 1]) {
    out = out.append(AFFIX[i]);
    i = i + 1;
  }
  return out;
}

// Changes the letters from `at` to upper case, or only the first when `first` is true.
function __upper(own w: u8[], at: i32, first: boolean): u8[] {
  let i: i32 = at;
  while (i < w.len()) {
    let b: i32 = w[i] as i32;
    let step: i32 = 3;
    if (b < 0xC0) {
      step = 1;
      if (b >= 97 && b <= 122) {
        w = w.with(i, (b ^ 32) as u8);
      }
    } else if (b < 0xE0) {
      step = 2;
      if (i + 1 < w.len()) {
        w = w.with(i + 1, w[i + 1] ^ 32 as u8);
      }
    } else if (i + 2 < w.len()) {
      w = w.with(i + 2, w[i + 2] ^ 5 as u8);
    }
    i = i + step;
    if (first) {
      i = w.len();
    }
  }
  return w;
}
```

## Constant tables

```fern
<<Constant tables>>=
// For each command symbol: the insert length with no extra bits in bits 0
// to 23, and its extra bit count from bit 24. Bit 32 starts the same two
// values for the copy length.
const COMMAND: i64[] = [8589934592, 12884901888, 17179869184, 21474836480, 25769803776, 30064771072,
  34359738368, 38654705664, 8589934593, 12884901889, 17179869185, 21474836481, 25769803777,
  30064771073, 34359738369, 38654705665, 8589934594, 12884901890, 17179869186, 21474836482,
  25769803778, 30064771074, 34359738370, 38654705666, 8589934595, 12884901891, 17179869187,
  21474836483, 25769803779, 30064771075, 34359738371, 38654705667, 8589934596, 12884901892,
  17179869188, 21474836484, 25769803780, 30064771076, 34359738372, 38654705668, 8589934597,
  12884901893, 17179869189, 21474836485, 25769803781, 30064771077, 34359738373, 38654705669,
  8606711814, 12901679110, 17196646406, 21491613702, 25786580998, 30081548294, 34376515590,
  38671482886, 8606711816, 12901679112, 17196646408, 21491613704, 25786581000, 30081548296,
  34376515592, 38671482888, 72057636987600896, 72057645577535488, 144115248205398016,
  144115265385267200, 216172876603064320, 216172910962802688, 288230539360468992,
  288230608079945728, 72057636987600897, 72057645577535489, 144115248205398017, 144115265385267201,
  216172876603064321, 216172910962802689, 288230539360468993, 288230608079945729, 72057636987600898,
  72057645577535490, 144115248205398018, 144115265385267202, 216172876603064322, 216172910962802690,
  288230539360468994, 288230608079945730, 72057636987600899, 72057645577535491, 144115248205398019,
  144115265385267203, 216172876603064323, 216172910962802691, 288230539360468995,
  288230608079945731, 72057636987600900, 72057645577535492, 144115248205398020, 144115265385267204,
  216172876603064324, 216172910962802692, 288230539360468996, 288230608079945732, 72057636987600901,
  72057645577535493, 144115248205398021, 144115265385267205, 216172876603064325, 216172910962802693,
  288230539360468997, 288230608079945733, 72057637004378118, 72057645594312710, 144115248222175238,
  144115265402044422, 216172876619841542, 216172910979579910, 288230539377246214,
  288230608096722950, 72057637004378120, 72057645594312712, 144115248222175240, 144115265402044424,
  216172876619841544, 216172910979579912, 288230539377246216, 288230608096722952, 8589934592,
  12884901888, 17179869184, 21474836480, 25769803776, 30064771072, 34359738368, 38654705664,
  8589934593, 12884901889, 17179869185, 21474836481, 25769803777, 30064771073, 34359738369,
  38654705665, 8589934594, 12884901890, 17179869186, 21474836482, 25769803778, 30064771074,
  34359738370, 38654705666, 8589934595, 12884901891, 17179869187, 21474836483, 25769803779,
  30064771075, 34359738371, 38654705667, 8589934596, 12884901892, 17179869188, 21474836484,
  25769803780, 30064771076, 34359738372, 38654705668, 8589934597, 12884901893, 17179869189,
  21474836485, 25769803781, 30064771077, 34359738373, 38654705669, 8606711814, 12901679110,
  17196646406, 21491613702, 25786580998, 30081548294, 34376515590, 38671482886, 8606711816,
  12901679112, 17196646408, 21491613704, 25786581000, 30081548296, 34376515592, 38671482888,
  72057636987600896, 72057645577535488, 144115248205398016, 144115265385267200, 216172876603064320,
  216172910962802688, 288230539360468992, 288230608079945728, 72057636987600897, 72057645577535489,
  144115248205398017, 144115265385267201, 216172876603064321, 216172910962802689,
  288230539360468993, 288230608079945729, 72057636987600898, 72057645577535490, 144115248205398018,
  144115265385267202, 216172876603064322, 216172910962802690, 288230539360468994,
  288230608079945730, 72057636987600899, 72057645577535491, 144115248205398019, 144115265385267203,
  216172876603064323, 216172910962802691, 288230539360468995, 288230608079945731, 72057636987600900,
  72057645577535492, 144115248205398020, 144115265385267204, 216172876603064324, 216172910962802692,
  288230539360468996, 288230608079945732, 72057636987600901, 72057645577535493, 144115248205398021,
  144115265385267205, 216172876603064325, 216172910962802693, 288230539360468997,
  288230608079945733, 72057637004378118, 72057645594312710, 144115248222175238, 144115265402044422,
  216172876619841542, 216172910979579910, 288230539377246214, 288230608096722950, 72057637004378120,
  72057645594312712, 144115248222175240, 144115265402044424, 216172876619841544, 216172910979579912,
  288230539377246216, 288230608096722952, 8623489034, 12918456330, 17213423626, 21508390922,
  25803358218, 30098325514, 34393292810, 38688260106, 8623489038, 12918456334, 17213423630,
  21508390926, 25803358222, 30098325518, 34393292814, 38688260110, 8640266258, 12935233554,
  17230200850, 21525168146, 25820135442, 30115102738, 34410070034, 38705037330, 8640266266,
  12935233562, 17230200858, 21525168154, 25820135450, 30115102746, 34410070042, 38705037338,
  8657043490, 12952010786, 17246978082, 21541945378, 25836912674, 30131879970, 34426847266,
  38721814562, 8657043506, 12952010802, 17246978098, 21541945394, 25836912690, 30131879986,
  34426847282, 38721814578, 8673820738, 12968788034, 17263755330, 21558722626, 25853689922,
  30148657218, 34443624514, 38738591810, 8673820770, 12968788066, 17263755362, 21558722658,
  25853689954, 30148657250, 34443624546, 38738591842, 72057637021155338, 72057645611089930,
  144115248238952458, 144115265418821642, 216172876636618762, 216172910996357130,
  288230539394023434, 288230608113500170, 72057637021155342, 72057645611089934, 144115248238952462,
  144115265418821646, 216172876636618766, 216172910996357134, 288230539394023438,
  288230608113500174, 72057637037932562, 72057645627867154, 144115248255729682, 144115265435598866,
  216172876653395986, 216172911013134354, 288230539410800658, 288230608130277394, 72057637037932570,
  72057645627867162, 144115248255729690, 144115265435598874, 216172876653395994, 216172911013134362,
  288230539410800666, 288230608130277402, 72057637054709794, 72057645644644386, 144115248272506914,
  144115265452376098, 216172876670173218, 216172911029911586, 288230539427577890,
  288230608147054626, 72057637054709810, 72057645644644402, 144115248272506930, 144115265452376114,
  216172876670173234, 216172911029911602, 288230539427577906, 288230608147054642, 72057637071487042,
  72057645661421634, 144115248289284162, 144115265469153346, 216172876686950466, 216172911046688834,
  288230539444355138, 288230608163831874, 72057637071487074, 72057645661421666, 144115248289284194,
  144115265469153378, 216172876686950498, 216172911046688866, 288230539444355170,
  288230608163831906, 360288270837350400, 360288408276303872, 432346139753185280,
  504404008669020160, 576462152462761984, 648520846012317696, 720580639073501184,
  1729391353651003392, 360288270837350401, 360288408276303873, 432346139753185281,
  504404008669020161, 576462152462761985, 648520846012317697, 720580639073501185,
  1729391353651003393, 360288270837350402, 360288408276303874, 432346139753185282,
  504404008669020162, 576462152462761986, 648520846012317698, 720580639073501186,
  1729391353651003394, 360288270837350403, 360288408276303875, 432346139753185283,
  504404008669020163, 576462152462761987, 648520846012317699, 720580639073501187,
  1729391353651003395, 360288270837350404, 360288408276303876, 432346139753185284,
  504404008669020164, 576462152462761988, 648520846012317700, 720580639073501188,
  1729391353651003396, 360288270837350405, 360288408276303877, 432346139753185285,
  504404008669020165, 576462152462761989, 648520846012317701, 720580639073501189,
  1729391353651003397, 360288270854127622, 360288408293081094, 432346139769962502,
  504404008685797382, 576462152479539206, 648520846029094918, 720580639090278406,
  1729391353667780614, 360288270854127624, 360288408293081096, 432346139769962504,
  504404008685797384, 576462152479539208, 648520846029094920, 720580639090278408,
  1729391353667780616, 8690598018, 12985565314, 17280532610, 21575499906, 25870467202, 30165434498,
  34460401794, 38755369090, 8707375298, 13002342594, 17297309890, 21592277186, 25887244482,
  30182211778, 34477179074, 38772146370, 8724152642, 13019119938, 17314087234, 21609054530,
  25904021826, 30198989122, 34493956418, 38788923714, 8740930114, 13035897410, 17330864706,
  21625832002, 25920799298, 30215766594, 34510733890, 38805701186, 8757707842, 13052675138,
  17347642434, 21642609730, 25937577026, 30232544322, 34527511618, 38822478914, 8791263298,
  13086230594, 17381197890, 21676165186, 25971132482, 30266099778, 34561067074, 38856034370,
  8824821826, 13119789122, 17414756418, 21709723714, 26004691010, 30299658306, 34594625602,
  38889592898, 8992610370, 13287577666, 17582544962, 21877512258, 26172479554, 30467446850,
  34762414146, 39057381442, 360288270870904842, 360288408309858314, 432346139786739722,
  504404008702574602, 576462152496316426, 648520846045872138, 720580639107055626,
  1729391353684557834, 360288270870904846, 360288408309858318, 432346139786739726,
  504404008702574606, 576462152496316430, 648520846045872142, 720580639107055630,
  1729391353684557838, 360288270887682066, 360288408326635538, 432346139803516946,
  504404008719351826, 576462152513093650, 648520846062649362, 720580639123832850,
  1729391353701335058, 360288270887682074, 360288408326635546, 432346139803516954,
  504404008719351834, 576462152513093658, 648520846062649370, 720580639123832858,
  1729391353701335066, 360288270904459298, 360288408343412770, 432346139820294178,
  504404008736129058, 576462152529870882, 648520846079426594, 720580639140610082,
  1729391353718112290, 360288270904459314, 360288408343412786, 432346139820294194,
  504404008736129074, 576462152529870898, 648520846079426610, 720580639140610098,
  1729391353718112306, 360288270921236546, 360288408360190018, 432346139837071426,
  504404008752906306, 576462152546648130, 648520846096203842, 720580639157387330,
  1729391353734889538, 360288270921236578, 360288408360190050, 432346139837071458,
  504404008752906338, 576462152546648162, 648520846096203874, 720580639157387362,
  1729391353734889570, 72057637088264322, 72057645678198914, 144115248306061442, 144115265485930626,
  216172876703727746, 216172911063466114, 288230539461132418, 288230608180609154, 72057637105041602,
  72057645694976194, 144115248322838722, 144115265502707906, 216172876720505026, 216172911080243394,
  288230539477909698, 288230608197386434, 72057637121818946, 72057645711753538, 144115248339616066,
  144115265519485250, 216172876737282370, 216172911097020738, 288230539494687042,
  288230608214163778, 72057637138596418, 72057645728531010, 144115248356393538, 144115265536262722,
  216172876754059842, 216172911113798210, 288230539511464514, 288230608230941250, 72057637155374146,
  72057645745308738, 144115248373171266, 144115265553040450, 216172876770837570, 216172911130575938,
  288230539528242242, 288230608247718978, 72057637188929602, 72057645778864194, 144115248406726722,
  144115265586595906, 216172876804393026, 216172911164131394, 288230539561797698,
  288230608281274434, 72057637222488130, 72057645812422722, 144115248440285250, 144115265620154434,
  216172876837951554, 216172911197689922, 288230539595356226, 288230608314832962, 72057637390276674,
  72057645980211266, 144115248608073794, 144115265787942978, 216172877005740098, 216172911365478466,
  288230539763144770, 288230608482621506, 360288270938013826, 360288408376967298,
  432346139853848706, 504404008769683586, 576462152563425410, 648520846112981122,
  720580639174164610, 1729391353751666818, 360288270954791106, 360288408393744578,
  432346139870625986, 504404008786460866, 576462152580202690, 648520846129758402,
  720580639190941890, 1729391353768444098, 360288270971568450, 360288408410521922,
  432346139887403330, 504404008803238210, 576462152596980034, 648520846146535746,
  720580639207719234, 1729391353785221442, 360288270988345922, 360288408427299394,
  432346139904180802, 504404008820015682, 576462152613757506, 648520846163313218,
  720580639224496706, 1729391353801998914, 360288271005123650, 360288408444077122,
  432346139920958530, 504404008836793410, 576462152630535234, 648520846180090946,
  720580639241274434, 1729391353818776642, 360288271038679106, 360288408477632578,
  432346139954513986, 504404008870348866, 576462152664090690, 648520846213646402,
  720580639274829890, 1729391353852332098, 360288271072237634, 360288408511191106,
  432346139988072514, 504404008903907394, 576462152697649218, 648520846247204930,
  720580639308388418, 1729391353885890626, 360288271240026178, 360288408678979650,
  432346140155861058, 504404009071695938, 576462152865437762, 648520846414993474,
  720580639476176962, 1729391354053679170];

const BL_OFFSET: i32[] = [1, 5, 9, 13, 17, 25, 33, 41, 49, 65, 81, 97, 113, 145, 177, 209, 241, 305, 369, 497, 753, 1265, 2289, 4337, 8433, 16625];

const BL_EXTRA: i32[] = [2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 7, 8, 9, 10, 11, 12, 13, 24];

const CL_ORDER: i32[] = [1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15];

// Bit count in bits 16 and up, code length below, for the next four bits.
const CL_FIXED: i32[] = [0x020000, 0x020004, 0x020003, 0x030002, 0x020000, 0x020004, 0x020003, 0x040001, 0x020000, 0x020004, 0x020003, 0x030002, 0x020000, 0x020004, 0x020003, 0x040005];

// The last distance that distance codes 0 to 15 change, and the change plus three.
const DIST_RING: i32[] = [0, 1, 2, 3, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1];

const DIST_DELTA: i32[] = [3, 3, 3, 3, 2, 4, 1, 5, 0, 6, 2, 4, 1, 5, 0, 6];

const DICT_BITS: i32[] = [0, 0, 0, 0, 10, 10, 11, 11, 10, 10, 10, 10, 10, 9, 9, 8, 7, 7, 8, 7, 7, 6, 6, 5, 5];

const DICT_OFFSET: i32[] = [0, 0, 0, 0, 0, 4096, 9216, 21504, 35840, 44032, 53248, 63488, 74752, 87040, 93696, 100864, 104704, 106752, 108928, 113536, 115968, 118528, 119872, 121280, 122016];

<<Transform tables>>

<<Context table>>
```

The transforms come from Appendix B of RFC 7932.

```fern
<<Transform tables>>=
// The prefix of transform t is affix 2t and its suffix is affix 2t + 1.
// Affix k is AFFIX[AFFIX_AT[k]] up to AFFIX[AFFIX_AT[k + 1]].
const AFFIX: u8[] = [
  32, 32, 32, 32, 32, 116, 104, 101, 32, 32, 115, 32, 32, 32, 111, 102, 32, 32,
  97, 110, 100, 32, 44, 32, 32, 44, 32, 32, 32, 32, 105, 110, 32, 32, 116, 111, 32, 101, 32, 32, 34,
  46, 34, 62, 10, 93, 32, 102, 111, 114, 32, 32, 97, 32, 32, 116, 104, 97, 116, 32, 32, 46, 32, 46,
  32, 44, 32, 32, 119, 105, 116, 104, 32, 39, 32, 102, 114, 111, 109, 32, 32, 98, 121, 32, 32, 116,
  104, 101, 32, 46, 32, 84, 104, 101, 32, 32, 111, 110, 32, 32, 97, 115, 32, 32, 105, 115, 32, 105,
  110, 103, 32, 10, 9, 58, 32, 46, 32, 101, 100, 32, 40, 44, 32, 32, 97, 116, 32, 108, 121, 32, 32,
  116, 104, 101, 32, 32, 111, 102, 32, 32, 44, 32, 34, 46, 40, 32, 34, 62, 61, 34, 32, 46, 46, 99,
  111, 109, 47, 32, 116, 104, 101, 32, 32, 111, 102, 32, 116, 104, 101, 32, 39, 46, 32, 84, 104,
  105, 115, 32, 44, 46, 32, 40, 46, 32, 110, 111, 116, 32, 32, 61, 34, 101, 114, 32, 32, 32, 97,
  108, 32, 32, 61, 39, 34, 46, 32, 32, 40, 102, 117, 108, 32, 32, 46, 32, 105, 118, 101, 32, 108,
  101, 115, 115, 32, 39, 101, 115, 116, 32, 32, 46, 34, 62, 32, 61, 39, 44, 105, 122, 101, 32, 46,
  194, 160, 32, 44, 61, 34, 61, 34, 111, 117, 115, 32, 44, 32, 61, 39, 32, 44, 32, 61, 34, 32, 44,
  32, 44, 40, 46, 32, 32, 46, 61, 39, 32, 46, 32, 32, 61, 34, 32, 61, 39, 32, 61, 39,
];

const AFFIX_AT: i32[] = [
  0, 0, 0, 0, 1, 2, 3, 3, 3, 3, 4, 4, 9, 10, 10, 12, 13, 13, 17, 17, 17, 17,
  22, 22, 22, 22, 22, 24, 25, 25, 27, 28, 29, 29, 33, 33, 37, 39, 40, 40, 41, 41, 42, 42, 44, 44,
  45, 45, 45, 45, 46, 46, 51, 51, 51, 51, 51, 51, 54, 54, 60, 61, 61, 61, 63, 64, 64, 65, 67, 67,
  67, 67, 73, 73, 74, 74, 80, 80, 84, 84, 84, 84, 84, 89, 89, 89, 89, 89, 95, 95, 95, 95, 99, 99,
  103, 103, 107, 107, 107, 107, 111, 111, 113, 113, 114, 115, 117, 117, 120, 120, 120, 120, 120,
  120, 120, 120, 121, 121, 123, 123, 123, 123, 127, 127, 130, 135, 139, 139, 139, 139, 139, 140,
  142, 142, 143, 144, 145, 145, 146, 146, 148, 148, 150, 151, 152, 157, 157, 162, 170, 170, 171,
  171, 178, 178, 179, 180, 181, 181, 182, 182, 183, 183, 188, 189, 191, 191, 194, 195, 196, 196,
  199, 200, 200, 200, 202, 202, 203, 203, 205, 206, 207, 207, 211, 212, 214, 214, 218, 218, 223,
  223, 224, 224, 228, 229, 230, 230, 232, 233, 235, 235, 236, 236, 240, 240, 241, 243, 243, 244,
  245, 245, 247, 247, 249, 249, 253, 253, 255, 255, 257, 258, 259, 260, 262, 263, 265, 265, 266,
  266, 267, 267, 269, 270, 271, 271, 273, 274, 276, 277, 279, 280, 282, 283, 285,
];

// Kind 0 keeps the word. Kinds 1 to 9 remove that many last bytes. Kind 10
// changes the first letter to upper case, and kind 11 changes all letters.
// Kinds 12 to 20 remove 1 to 9 first bytes.
const TRANSFORM_KIND: i32[] = [
  0, 0, 0, 12, 10, 0, 0, 0, 0, 10, 0, 13, 1, 0, 0, 10, 0, 0, 0, 0, 0,
  0, 0, 3, 0, 0, 14, 2, 0, 0, 10, 0, 0, 0, 15, 0, 0, 0, 0, 16, 17, 0, 4, 0, 11, 0, 0, 0, 7, 1, 0, 0,
  0, 0, 20, 18, 6, 0, 10, 8, 0, 0, 0, 5, 9, 10, 10, 0, 11, 10, 0, 0, 0, 0, 10, 0, 0, 0, 10, 10, 0,
  0, 0, 11, 0, 11, 0, 11, 10, 0, 0, 10, 0, 0, 11, 0, 10, 11, 0, 10, 0, 11, 0, 0, 10, 11, 0, 11, 10,
  10, 11, 11, 11, 11, 11, 11, 11, 11, 10, 11, 10,
];
```

The context table gives the context ID of a literal from its two last bytes. Section 7.1 of RFC 7932 describes it.

```fern
<<Context table>>=
// For each context mode, 256 entries for the last byte and 256 for the byte before it.
const CONTEXT: u8[] = [
  0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21,
  22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45,
  46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 0, 1, 2, 3, 4, 5, 6, 7, 8,
  9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33,
  34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57,
  58, 59, 60, 61, 62, 63, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
  21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44,
  45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 0, 1, 2, 3, 4, 5, 6,
  7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
  32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55,
  56, 57, 58, 59, 60, 61, 62, 63, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 7, 7,
  7, 7, 8, 8, 8, 8, 9, 9, 9, 9, 10, 10, 10, 10, 11, 11, 11, 11, 12, 12, 12, 12, 13, 13, 13, 13, 14,
  14, 14, 14, 15, 15, 15, 15, 16, 16, 16, 16, 17, 17, 17, 17, 18, 18, 18, 18, 19, 19, 19, 19, 20,
  20, 20, 20, 21, 21, 21, 21, 22, 22, 22, 22, 23, 23, 23, 23, 24, 24, 24, 24, 25, 25, 25, 25, 26,
  26, 26, 26, 27, 27, 27, 27, 28, 28, 28, 28, 29, 29, 29, 29, 30, 30, 30, 30, 31, 31, 31, 31, 32,
  32, 32, 32, 33, 33, 33, 33, 34, 34, 34, 34, 35, 35, 35, 35, 36, 36, 36, 36, 37, 37, 37, 37, 38,
  38, 38, 38, 39, 39, 39, 39, 40, 40, 40, 40, 41, 41, 41, 41, 42, 42, 42, 42, 43, 43, 43, 43, 44,
  44, 44, 44, 45, 45, 45, 45, 46, 46, 46, 46, 47, 47, 47, 47, 48, 48, 48, 48, 49, 49, 49, 49, 50,
  50, 50, 50, 51, 51, 51, 51, 52, 52, 52, 52, 53, 53, 53, 53, 54, 54, 54, 54, 55, 55, 55, 55, 56,
  56, 56, 56, 57, 57, 57, 57, 58, 58, 58, 58, 59, 59, 59, 59, 60, 60, 60, 60, 61, 61, 61, 61, 62,
  62, 62, 62, 63, 63, 63, 63, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 8, 12, 16, 12, 12, 20, 12, 16, 24, 28, 12, 12, 32, 12, 36, 12, 44, 44, 44, 44, 44, 44, 44, 44,
  44, 44, 32, 32, 24, 40, 28, 12, 12, 48, 52, 52, 52, 48, 52, 52, 52, 48, 52, 52, 52, 52, 52, 48,
  52, 52, 52, 52, 52, 48, 52, 52, 52, 52, 52, 24, 12, 28, 12, 12, 12, 56, 60, 60, 60, 56, 60, 60,
  60, 56, 60, 60, 60, 60, 60, 56, 60, 60, 60, 60, 60, 56, 60, 60, 60, 60, 60, 24, 12, 28, 12, 0, 0,
  1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1,
  0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 2, 3, 2,
  3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3,
  2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1,
  1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2,
  2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 3, 3, 3, 3, 3, 3, 3,
  3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
  2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 0, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
  8, 8, 8, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16,
  16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16,
  16, 16, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
  24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
  24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 32, 32, 32, 32, 32, 32,
  32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32,
  32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32,
  32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40,
  40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40,
  40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 48, 48, 48, 48, 48, 48, 48, 48, 48, 48, 48, 48, 48, 48,
  48, 56, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
  2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
  2, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
  3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 4,
  4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
  4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 5,
  5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
  5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 7,
];
```

## Compress a stream

The fast encoder uses LZ77 matches and fixed Huffman trees. Its match history has a fixed size.

```fern
<<Compress a stream>>=
pub function compress(input: u8[]): u8[] {
  return encoder.compress(input);
}
```
