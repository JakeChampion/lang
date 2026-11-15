# DEFLATE, zlib, and gzip

This module decodes DEFLATE, zlib, and gzip data.

## Model

The DEFLATE decoder interprets bit fields, Huffman codes, and history references. The zlib and gzip wrappers add their frame and checksum rules.

## Contracts

Output limits bound decompressed data. `Inflated` retains both the output and the consumed input count. Error values distinguish malformed data from incomplete data and output limits.

`zlib_compress` writes the bytes that zlib writes, at each level of zlib from 0 to 9. It is for a program that must write the same bytes as a program that uses zlib. At level 0, the blocks of zlib depend on the space for output that each call to `deflate` of zlib gets: `zlib_compress_in` takes that space, and `zlib_compress` gives all the output space in one call, as `compress2` of zlib does.

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

<<9 decode table entries>>

<<10 the decode table of a code>>

<<11 table bits>>

<<12 the read of one symbol>>

<<13 the decode loops>>

<<14 function __fixed(own s: __State, data: [u8]): __State>>

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

<<35 the encoder of zlib>>
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

The output buffer is longer than the output. `n` counts the bytes that the
decoder wrote, and the decoder writes at an index. The length of the buffer
goes down to `n` at the end.

```fern
<<6 function __inflated(input: [u8], from: i32, history: u8[], max_out: i32): Result[Inflated, InflateError]>>=
function __inflated(input: [u8], from: i32, history: u8[], max_out: i32): Result[Inflated, InflateError] {
  if (from < 0) {
    return Err(Malformed("an offset below zero"));
  }
  let out = history;
  let n = history.len();
  if (n == 0) {
    out = __room(input.len() - from, max_out);
  }
  let s = __State { pos: from, bitbuf: 0, bitcnt: 0, out: out, n: n, max_out: max_out + n, last: 0, failed: false, error: Truncated, lengths: [], counts: [] };
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
  return Ok(Inflated { out: __produced(s.out, s.n), consumed: s.pos - from });
}

// The bytes past the output that the fast loop can write: three literals,
// or a match of 258 bytes and the 15 bytes that a copy of 16 bytes can pass
// its end. It is more than the 511 + 31 that the masks of a length in
// `__fast` allow, so the bounds prover finds that the output index does not
// overflow. The masks of a distance keep it from being negative.
const SLACK: i32 = 600;

// Room for four times the input, at least 4 KiB and at most the output
// limit, and SLACK - 3 more, so the output rarely grows and copies. With at
// most SLACK - 3 bytes past the limit, the fast loop can write three
// literals wherever it has room for SLACK bytes.
function __room(avail: i32, max_out: i32): u8[] {
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
  return __alloc_u8(__capped(want + (SLACK - 3) as i64));
}

function __capped(want: i64): i32 {
  if (want > 2147483647i64) {
    return 2147483647;
  }
  return want as i32;
}

// A buffer with the first `n` bytes of `out` and room for `need` more,
// at least twice as long as `out`, and at most SLACK - 3 past the output
// limit.
function __grown(own out: u8[], n: i32, need: i32, max_out: i32): u8[] {
  let want = cmp.max(out.len() as i64 * 2i64, n as i64 + need as i64 + 2i64 * (SLACK as i64));
  want = cmp.min(want, max_out as i64 + (SLACK - 3) as i64);
  want = cmp.max(want, n as i64 + need as i64);
  let bigger = __alloc_u8(__capped(want));
  let i = 0;
  while (i < n) {
    bigger = bigger.with(i, out[i]);
    i += 1;
  }
  return bigger;
}

function __produced(own out: u8[], n: i32): u8[] {
  unsafe {
    __arr_set_len(out, n);
  }
  return out;
}
```

### `7 struct __State`

```fern
<<7 struct __State>>=
struct __State { pos: i32, bitbuf: i32, bitcnt: i32, out: u8[], n: i32, max_out: i32, last: i32, failed: boolean, error: InflateError, lengths: i32[], counts: i32[] }
```

### `8 function __fail(own s: __State, e: InflateError): __State`

```fern
<<8 function __fail(own s: __State, e: InflateError): __State>>=
function __fail(own s: __State, e: InflateError): __State {
  if (s.failed) {
    return s;
  }
  return __State { ...s, failed: true, error: e, last: 0 };
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
  if (s.n + len > s.max_out) {
    return __fail(s, OutputLimit);
  }
  let out = s.out;
  let n = s.n;
  s = __State { ...s, out: [] };
  if (n + len > out.len()) {
    out = __grown(out, n, len, s.max_out);
  }
  let i = 0;
  while (i < len) {
    out = out.with(n + i, data[at + i]);
    i += 1;
  }
  return __State { ...s, pos: at + len, out: out, n: n + len };
}
```

### `9 decode table entries`

A decode table has one entry for each pattern of the first `bits` input
bits. After these entries, it has a subtable for each pattern that starts
a longer code.

Bits 0-7 of an entry hold the count of bits that the entry takes from the
input: the code, and the extra bits of a length or a distance. Bits 8-11
hold the length of the code, so the extra bits are the bits above it. A
subtable entry holds the part of the code after the first `bits` bits.

Bits 12-15 are flags. Bits 16-30 hold the literal, the base of the length
or the distance, or the start of a subtable. A literal also has bit 31, so
a literal entry is below zero.

An entry with E_SUB takes the first `bits` bits, and bits 8-11 give the
index bits of its subtable. An entry with E_NO_CODE is a pattern of an
incomplete code that is no code. It takes as many bits as the longest
code, so a decoder with fewer bits finds the input short.

```fern
<<9 decode table entries>>=
const E_END: i32 = 4096;

const E_SUB: i32 = 8192;

const E_NO_CODE: i32 = 16384;

const E_PAST: i32 = 32768;

const E_FLAGS: i32 = 61440;

const E_LITERAL: i32 = -2147483647 - 1;

struct __Tables { state: __State, literal: i32[], distance: i32[] }

function __empty_tables(s: __State): __Tables {
  return __Tables { state: s, literal: [], distance: [] };
}

// The count of codes that the code does not use: zero for a complete code,
// below zero for an over-subscribed code. `count[at + len]` counts the
// codes of `len` bits.
function __left(count: i32[], at: i32): i32 {
  let left = 1;
  let len = 1;
  while (len <= 15) {
    left = (left << 1) - count[at + len];
    if (left < 0) {
      return -1;
    }
    len += 1;
  }
  return left;
}

// RFC 1951 lets an incomplete code have one code of one bit, and lets a
// distance code have no codes.
function __invalid(count: i32[], at: i32, left: i32, symbols: i32, empty: boolean): boolean {
  if (left <= 0) {
    return left < 0;
  }
  let used = symbols - count[at];
  return !(empty && used == 0 || used == 1 && count[at + 1] == 1);
}

// The entry of each symbol with no code length: the literal, or the base of
// a length or a distance and its count of extra bits from RFC 1951 section
// 3.2.5. A code of `len` bits adds 257 * len, so bits 0-7 also count the
// extra bits.
const LIT_VALUES: i32[] = [-2147483648, -2147418112, -2147352576, -2147287040, -2147221504, -2147155968, -2147090432, -2147024896, -2146959360, -2146893824, -2146828288, -2146762752, -2146697216, -2146631680, -2146566144, -2146500608, -2146435072, -2146369536, -2146304000, -2146238464, -2146172928, -2146107392, -2146041856, -2145976320, -2145910784, -2145845248, -2145779712, -2145714176, -2145648640, -2145583104, -2145517568, -2145452032, -2145386496, -2145320960, -2145255424, -2145189888, -2145124352, -2145058816, -2144993280, -2144927744, -2144862208, -2144796672, -2144731136, -2144665600, -2144600064, -2144534528, -2144468992, -2144403456, -2144337920, -2144272384, -2144206848, -2144141312, -2144075776, -2144010240, -2143944704, -2143879168, -2143813632, -2143748096, -2143682560, -2143617024, -2143551488, -2143485952, -2143420416, -2143354880, -2143289344, -2143223808, -2143158272, -2143092736, -2143027200, -2142961664, -2142896128, -2142830592, -2142765056, -2142699520, -2142633984, -2142568448, -2142502912, -2142437376, -2142371840, -2142306304, -2142240768, -2142175232, -2142109696, -2142044160, -2141978624, -2141913088, -2141847552, -2141782016, -2141716480, -2141650944, -2141585408, -2141519872, -2141454336, -2141388800, -2141323264, -2141257728, -2141192192, -2141126656, -2141061120, -2140995584, -2140930048, -2140864512, -2140798976, -2140733440, -2140667904, -2140602368, -2140536832, -2140471296, -2140405760, -2140340224, -2140274688, -2140209152, -2140143616, -2140078080, -2140012544, -2139947008, -2139881472, -2139815936, -2139750400, -2139684864, -2139619328, -2139553792, -2139488256, -2139422720, -2139357184, -2139291648, -2139226112, -2139160576, -2139095040, -2139029504, -2138963968, -2138898432, -2138832896, -2138767360, -2138701824, -2138636288, -2138570752, -2138505216, -2138439680, -2138374144, -2138308608, -2138243072, -2138177536, -2138112000, -2138046464, -2137980928, -2137915392, -2137849856, -2137784320, -2137718784, -2137653248, -2137587712, -2137522176, -2137456640, -2137391104, -2137325568, -2137260032, -2137194496, -2137128960, -2137063424, -2136997888, -2136932352, -2136866816, -2136801280, -2136735744, -2136670208, -2136604672, -2136539136, -2136473600, -2136408064, -2136342528, -2136276992, -2136211456, -2136145920, -2136080384, -2136014848, -2135949312, -2135883776, -2135818240, -2135752704, -2135687168, -2135621632, -2135556096, -2135490560, -2135425024, -2135359488, -2135293952, -2135228416, -2135162880, -2135097344, -2135031808, -2134966272, -2134900736, -2134835200, -2134769664, -2134704128, -2134638592, -2134573056, -2134507520, -2134441984, -2134376448, -2134310912, -2134245376, -2134179840, -2134114304, -2134048768, -2133983232, -2133917696, -2133852160, -2133786624, -2133721088, -2133655552, -2133590016, -2133524480, -2133458944, -2133393408, -2133327872, -2133262336, -2133196800, -2133131264, -2133065728, -2133000192, -2132934656, -2132869120, -2132803584, -2132738048, -2132672512, -2132606976, -2132541440, -2132475904, -2132410368, -2132344832, -2132279296, -2132213760, -2132148224, -2132082688, -2132017152, -2131951616, -2131886080, -2131820544, -2131755008, -2131689472, -2131623936, -2131558400, -2131492864, -2131427328, -2131361792, -2131296256, -2131230720, -2131165184, -2131099648, -2131034112, -2130968576, -2130903040, -2130837504, -2130771968, 4096, 196608, 262144, 327680, 393216, 458752, 524288, 589824, 655360, 720897, 851969, 983041, 1114113, 1245186, 1507330, 1769474, 2031618, 2293763, 2818051, 3342339, 3866627, 4390916, 5439492, 6488068, 7536644, 8585221, 10682373, 12779525, 14876677, 16908288, 32768, 32768];

const DIST_VALUES: i32[] = [65536, 131072, 196608, 262144, 327681, 458753, 589826, 851970, 1114115, 1638403, 2162692, 3211268, 4259845, 6356997, 8454150, 12648454, 16842759, 25231367, 33619976, 50397192, 67174409, 100728841, 134283274, 201392138, 268501003, 402718731, 536936460, 805371916, 1073807373, 1610678285];

const LENGTH_VALUES: i32[] = [0, 65536, 131072, 196608, 262144, 327680, 393216, 458752, 524288, 589824, 655360, 720896, 786432, 851968, 917504, 983040, 1048576, 1114112, 1179648];
```

### `10 the decode table of a code`

`__build` sorts the symbols by the length of their code, so the codes come
in their canonical order. `rev` is the next code with its bits reversed, as
the input gives them. So `rev` is also the index of the code in the table.

The table starts with an entry for each pattern of the shortest code. It
doubles before each longer length up to `bits`. The copy of the shorter
codes fills the entries that their codes also match, so each code is
written once, as libdeflate does it.

The codes longer than `bits` go in subtables after the main entries. The
codes with the same first `bits` bits are next to each other. Their
subtable is as large as the longest of them needs, as zlib computes it.

```fern
<<10 the decode table of a code>>=
// The decode table of the `n` code lengths from `from`. `count[at + len]`
// counts the codes of `len` bits, and `left` is their __left. `values` has
// the entry of each symbol with no code length.
// fern-lint: allow cyclomatic-complexity
function __build(lengths: i32[], from: i32, n: i32, count: i32[], at: i32, left: i32, bits: i32, values: i32[]): i32[] {
  let offs = __alloc_i32(16);
  let len = 1;
  while (len < 15) {
    offs = offs.with(len + 1, offs[len] + count[at + len]);
    len += 1;
  }
  let sorted = __alloc_i32(n);
  let sym = 0;
  while (sym < n) {
    len = lengths[from + sym];
    if (len != 0) {
      sorted = sorted.with(offs[len], sym);
      offs = offs.with(len, offs[len] + 1);
    }
    sym += 1;
  }
  let longest = 15;
  while (longest > 0 && count[at + longest] == 0) {
    longest -= 1;
  }
  let none = E_NO_CODE | longest << 8 | longest;
  let first = 1;
  while (first < bits && count[at + first] == 0) {
    first += 1;
  }
  let table = if (left != 0) { __filled(1 << bits, none) } else { __alloc_i32(1 << bits) };
  table = table.truncate(1 << first);
  let rev = 0;
  let i = 0;
  len = first;
  while (len <= bits) {
    if (len > first) {
      table = table.concat(table);
    }
    let k = count[at + len];
    let code = len * 257;
    while (k > 0) {
      table = table.with(rev, values[sorted[i]] + code);
      rev = __next_code(rev, len);
      i += 1;
      k -= 1;
    }
    len += 1;
  }
  let mask = (1 << bits) - 1;
  let low = -1;
  let start = 0;
  let sub = 0;
  while (len <= longest) {
    let k = count[at + len];
    while (k > 0) {
      if ((rev & mask) != low) {
        low = rev & mask;
        sub = len - bits;
        let room = (1 << sub) - k;
        while (sub + bits < longest && room > 0) {
          sub += 1;
          room = (room << 1) - count[at + sub + bits];
        }
        start = table.len();
        let j = 0;
        while (j < 1 << sub) {
          table = table.append(none);
          j += 1;
        }
        table = table.with(low, E_SUB | start << 16 | sub << 8 | bits);
      }
      let entry = values[sorted[i]] + (len - bits) * 257;
      let at = rev >> bits;
      while (at < 1 << sub) {
        table = table.with(start + at, entry);
        at += 1 << len - bits;
      }
      rev = __next_code(rev, len);
      i += 1;
      k -= 1;
    }
    len += 1;
  }
  return table;
}

// The code after `rev` in a code of `len` bits, both with their bits
// reversed.
function __next_code(rev: i32, len: i32): i32 {
  let free = (rev ^ (1 << len) - 1) as u32;
  if (free == 0u32) {
    return 0;
  }
  let bit = 1 << 31 - __clz32(free);
  return rev & bit - 1 | bit;
}
```

### `11 table bits`

The main part of the literal/length table has an entry for each pattern of
10 bits. The main part of the distance table has one for each pattern of 8
bits.

```fern
<<11 table bits>>=
const LIT_BITS: i32 = 10;

const DIST_BITS: i32 = 8;

const LIT_MASK: u64 = 1023u64;

const DIST_MASK: u64 = 255u64;
```

### `12 the read of one symbol`

The resumable reader reads one symbol at a time with `__symbol`. It takes
the bits of the code and leaves the extra bits in the input. The whole bytes
that stay in the bit buffer go back to the input, as `__bits` keeps them.

```fern
<<12 the read of one symbol>>=
// The entry of the next code of `table` in `last`. `name` names the code
// in an error.
function __symbol(own s: __State, data: [u8], table: i32[], bits: i32, name: string): __State {
  if (s.failed) {
    return s;
  }
  let buf = s.bitbuf;
  let cnt = s.bitcnt;
  let pos = s.pos;
  while (cnt < 16 && pos < data.len()) {
    buf |= data[pos] as i32 << cnt;
    pos += 1;
    cnt += 8;
  }
  let e = table[buf & (1 << bits) - 1];
  if ((e & E_SUB) != 0) {
    if (bits > cnt) {
      return __fail(s, Truncated);
    }
    buf >>= bits;
    cnt -= bits;
    e = table[(e >> 16) + (buf & (1 << (e >> 8 & 15)) - 1)];
  }
  let used = e >> 8 & 15;
  if (used > cnt) {
    return __fail(s, Truncated);
  }
  if ((e & E_NO_CODE) != 0) {
    return __fail(s, Malformed("a bit pattern that is no " + name + " code"));
  }
  buf >>= used;
  cnt -= used;
  return __State { ...s, pos: pos - (cnt >> 3), bitbuf: buf & (1 << (cnt & 7)) - 1, bitcnt: cnt & 7, last: e };
}

@inline
function __le64(data: [u8], at: i32): u64 {
  return data[at] as u64 | data[at + 1] as u64 << 8 | data[at + 2] as u64 << 16 | data[at + 3] as u64 << 24
    | data[at + 4] as u64 << 32 | data[at + 5] as u64 << 40 | data[at + 6] as u64 << 48 | data[at + 7] as u64 << 56;
}

// The low `n` bits of `x`.
@inline
function __low(x: u64, n: i32): u64 {
  return x & (1u64 << n as u64) - 1u64;
}
```

### `13 the decode loops`

`__fast` runs while eight input bytes and SLACK output bytes are left. One
word refill leaves at least 56 bits. A length with its distance needs at
most 48, so the loop reads a whole symbol pair with no bit count check.
Three literals need at most 45 bits. So after a literal, the loop decodes
up to two more. Then it finds the entry of the code after them.

The careful loop in `__codes` decodes what is left one byte at a time. The
two loops are in separate functions so that the fast one has the registers
to itself.

```fern
<<13 the decode loops>>=
// fern-lint: allow cyclomatic-complexity
function __codes(own s: __State, data: [u8], lit: i32[], dtab: i32[]): __State {
  let out = s.out;
  let n = s.n;
  let max_out = s.max_out;
  let pos = s.pos;
  let bitbuf = s.bitbuf as u64;
  let bitcnt = s.bitcnt;
  let end = data.len();
  s = __State { ...s, out: [], lengths: [] };
  let status = F_ROOM;
  while (true) {
    let fast = __fast(out, n, data, pos, bitbuf, bitcnt, max_out, lit, dtab);
    out = fast.out;
    n = fast.n;
    pos = fast.pos;
    bitbuf = fast.bitbuf;
    bitcnt = fast.bitcnt;
    status = fast.status;
    if (status != F_ROOM || pos > end - 8 || n > max_out - 3 || n <= out.len() - SLACK) {
      break;
    }
    let before = out.len();
    out = __grown(out, n, SLACK, max_out);
    if (out.len() == before) {
      break;
    }
  }
  while (status == F_ROOM) {
    while (bitcnt <= 56 && pos < end) {
      bitbuf |= data[pos] as u64 << bitcnt as u64;
      pos += 1;
      bitcnt += 8;
    }
    let e = lit[(bitbuf & LIT_MASK) as i32];
    if ((e & E_SUB) != 0) {
      if (LIT_BITS > bitcnt) {
        status = F_SHORT;
        break;
      }
      bitbuf >>= LIT_BITS as u64;
      bitcnt -= LIT_BITS;
      e = lit[(e >> 16) + __low(bitbuf, e >> 8 & 15) as i32];
    }
    let used = e & 255;
    if (used > bitcnt) {
      status = F_SHORT;
      break;
    }
    let saved = bitbuf;
    bitbuf >>= used as u64;
    bitcnt -= used;
    if (e < 0) {
      if (n >= max_out) {
        status = F_LIMIT;
        break;
      }
      if (n >= out.len()) {
        out = __grown(out, n, 1, max_out);
      }
      out = out.with(n, (e >> 16) as u8);
      n += 1;
      continue;
    }
    if ((e & E_FLAGS) != 0) {
      status = __end_status(e);
      break;
    }
    let len = (e >> 16) + (__low(saved, used) >> (e >> 8 & 15) as u64) as i32;
    while (bitcnt <= 56 && pos < end) {
      bitbuf |= data[pos] as u64 << bitcnt as u64;
      pos += 1;
      bitcnt += 8;
    }
    let d = dtab[(bitbuf & DIST_MASK) as i32];
    if ((d & E_SUB) != 0) {
      if (DIST_BITS > bitcnt) {
        status = F_SHORT;
        break;
      }
      bitbuf >>= DIST_BITS as u64;
      bitcnt -= DIST_BITS;
      d = dtab[(d >> 16) + __low(bitbuf, d >> 8 & 15) as i32];
    }
    used = d & 255;
    if (used > bitcnt) {
      status = F_SHORT;
      break;
    }
    if ((d & E_FLAGS) != 0) {
      status = F_NO_DISTANCE;
      break;
    }
    let dist = (d >> 16) + (__low(bitbuf, used) >> (d >> 8 & 15) as u64) as i32;
    bitbuf >>= used as u64;
    bitcnt -= used;
    if (dist > n) {
      status = F_TOO_FAR;
      break;
    }
    if (n + len > max_out) {
      status = F_LIMIT;
      break;
    }
    if (n + len > out.len()) {
      out = __grown(out, n, len, max_out);
    }
    let k = 0;
    while (k < len) {
      out = out.with(n + k, out[n + k - dist]);
      k += 1;
    }
    n += len;
  }
  let back = bitcnt >> 3;
  bitcnt &= 7;
  s = __State { ...s, out: out, n: n, pos: pos - back, bitbuf: __low(bitbuf, bitcnt) as i32, bitcnt: bitcnt };
  match (__status_error(status)) {
    Some(err) => {
      return __fail(s, err);
    },
    None => {
      return s;
    }
  }
  return s;
}

struct __Fast { out: u8[], n: i32, pos: i32, bitbuf: u64, bitcnt: i32, status: i32 }

const F_ROOM: i32 = 0;

const F_END: i32 = 1;

const F_NO_CODE: i32 = 2;

const F_LENGTH_PAST: i32 = 3;

const F_TOO_FAR: i32 = 4;

const F_NO_DISTANCE: i32 = 5;

const F_LIMIT: i32 = 6;

const F_SHORT: i32 = 7;

// Decodes while eight input bytes and SLACK output bytes are left, and
// stops with F_ROOM when they are not. The guard at the top is what lets
// the bounds prover drop the checks on the input words and the two main
// tables. `e` is the entry of the next code when the bits of the last
// iteration found it, and zero when the next iteration reads it.
// fern-lint: allow cyclomatic-complexity
function __fast(own out: u8[], n: i32, data: [u8], pos: i32, bitbuf: u64, bitcnt: i32, max_out: i32, lit: i32[], dtab: i32[]): __Fast {
  let fast_in = data.len() - 8;
  let room = out.len() - SLACK;
  let status = F_ROOM;
  if (pos < 0 || n < 0 || room > max_out - 3 || lit.len() < 1 << LIT_BITS || dtab.len() < 1 << DIST_BITS) {
    return __Fast { out: out, n: n, pos: pos, bitbuf: bitbuf, bitcnt: bitcnt, status: status };
  }
  let cnt = bitcnt as u64;
  let e = 0;
  while (pos <= fast_in && n <= room) {
    bitbuf |= __le64(data, pos) << cnt;
    pos += ((cnt >> 3u64 ^ 7u64) & 7u64) as i32;
    cnt |= 56u64;
    if (e == 0) {
      e = lit[(bitbuf & LIT_MASK) as i32];
    }
    let saved = bitbuf;
    let used = (e & 255) as u64;
    bitbuf >>= used;
    cnt -= used;
    if (e < 0) {
      out = out.with(n, (e >> 16) as u8);
      e = lit[(bitbuf & LIT_MASK) as i32];
      if (e >= 0) {
        n += 1;
        continue;
      }
      used = (e & 255) as u64;
      bitbuf >>= used;
      cnt -= used;
      out = out.with(n + 1, (e >> 16) as u8);
      e = lit[(bitbuf & LIT_MASK) as i32];
      if (e >= 0) {
        n += 2;
        continue;
      }
      used = (e & 255) as u64;
      bitbuf >>= used;
      cnt -= used;
      out = out.with(n + 2, (e >> 16) as u8);
      n += 3;
      e = lit[(bitbuf & LIT_MASK) as i32];
      continue;
    }
    if ((e & E_FLAGS) != 0) {
      if ((e & E_SUB) == 0) {
        status = __end_status(e);
        break;
      }
      e = lit[(e >> 16) + __low(bitbuf, e >> 8 & 15) as i32];
      saved = bitbuf;
      used = (e & 255) as u64;
      bitbuf >>= used;
      cnt -= used;
      if (e < 0) {
        out = out.with(n, (e >> 16) as u8);
        n += 1;
        e = 0;
        continue;
      }
      if ((e & E_FLAGS) != 0) {
        status = __end_status(e);
        break;
      }
    }
    let len = (e >> 16 & 511) + ((__low(saved, e & 255) >> (e >> 8 & 15) as u64) as i32 & 31);
    let d = dtab[(bitbuf & DIST_MASK) as i32];
    saved = bitbuf;
    used = (d & 255) as u64;
    bitbuf >>= used;
    cnt -= used;
    if ((d & E_FLAGS) != 0) {
      if ((d & E_SUB) == 0) {
        status = F_NO_DISTANCE;
        break;
      }
      d = dtab[(d >> 16) + __low(bitbuf, d >> 8 & 15) as i32];
      saved = bitbuf;
      used = (d & 255) as u64;
      bitbuf >>= used;
      cnt -= used;
      if ((d & E_FLAGS) != 0) {
        status = F_NO_DISTANCE;
        break;
      }
    }
    let dist = (d >> 16 & 32767) + ((__low(saved, d & 255) >> (d >> 8 & 15) as u64) as i32 & 8191);
    if (dist > n) {
      status = F_TOO_FAR;
      break;
    }
    if (n + len > max_out) {
      status = F_LIMIT;
      break;
    }
    out = __copy(out, n, dist, len);
    n += len;
    e = 0;
  }
  return __Fast { out: out, n: n, pos: pos, bitbuf: bitbuf, bitcnt: cnt as i32, status: status };
}

// Copies `len` bytes from `dist` back to `n`, 16 at a time. It can write
// 15 bytes past the copy. Most copies are at most 32 bytes, so the first two
// blocks need no loop. When `dist` is below 16, the first bytes are copied
// one at a time, up to a multiple of `dist` that is at least 16, and the
// blocks of 16 after them come from that far back.
@inline
function __copy(own out: u8[], n: i32, dist: i32, len: i32): u8[] {
  if (dist >= 16) {
    let from = n - dist;
    let v = __u8x16_load(out, from);
    out = __u8x16_with(out, n, v);
    if (len <= 16) {
      return out;
    }
    let w = __u8x16_load(out, from + 16);
    out = __u8x16_with(out, n + 16, w);
    return __copy_rest(out, n, dist, len, 32);
  }
  if (dist == 1) {
    let v = __u8x16_splat(out[n - 1]);
    out = __u8x16_with(out, n, v);
    let k = 16;
    while (k < len) {
      out = __u8x16_with(out, n + k, v);
      k += 16;
    }
    return out;
  }
  let back = dist;
  while (back < 16) {
    back += dist;
  }
  let stop = cmp.min(len, back);
  let k = 0;
  while (k < stop) {
    out = out.with(n + k, out[n + k - dist]);
    k += 1;
  }
  return __copy_rest(out, n, back, len, k);
}

// The blocks of a copy from `k` on, `back` bytes back.
@inline
function __copy_rest(own out: u8[], n: i32, back: i32, len: i32, k: i32): u8[] {
  while (k < len) {
    let v = __u8x16_load(out, n + k - back);
    out = __u8x16_with(out, n + k, v);
    k += 16;
  }
  return out;
}

// The status of an entry with E_END, E_NO_CODE or E_PAST.
function __end_status(e: i32): i32 {
  if ((e & E_END) != 0) {
    return F_END;
  }
  if ((e & E_NO_CODE) != 0) {
    return F_NO_CODE;
  }
  return F_LENGTH_PAST;
}

function __status_error(status: i32): Option[InflateError] {
  if (status == F_NO_CODE) {
    return Some(Malformed("a bit pattern that is no literal/length code"));
  }
  if (status == F_NO_DISTANCE) {
    return Some(Malformed("a bit pattern that is no distance code"));
  }
  if (status == F_LENGTH_PAST) {
    return Some(Malformed("a length code past 285"));
  }
  if (status == F_TOO_FAR) {
    return Some(Malformed("a distance before the start of the output"));
  }
  if (status == F_LIMIT) {
    return Some(OutputLimit);
  }
  if (status == F_SHORT) {
    return Some(Truncated);
  }
  return None;
}
```

### `14 function __fixed(own s: __State, data: [u8]): __State`

```fern
<<14 function __fixed(own s: __State, data: [u8]): __State>>=
// The decode tables of the fixed codes of RFC 1951 section 3.2.6, as
// __build gives them. The literal/length code has 288 codes: 0-143 of eight
// bits, 144-255 of nine, 256-279 of seven and 280-287 of eight. The distance
// code has 30 codes of five bits, so the other two patterns are no code.
const FIXED_LIT_TABLE: i32[] = [5895, -2142238712, -2146433016, 7538700, 2033417, -2140141560, -2144335864, -2134898423, 657159, -2141190136, -2145384440, -2136995575, -2147481592, -2139092984, -2143287288, -2132801271, 395015, -2141714424, -2145908728, -2138044151, 3868426, -2139617272, -2143811576, -2133849847, 1115912, -2140665848, -2144860152, -2135946999, -2146957304, -2138568696, -2142763000, -2131752695, 263943, -2141976568, -2146170872, 14878733, 2819850, -2139879416, -2144073720, -2134374135, 853768, -2140927992, -2145122296, -2136471287, -2147219448, -2138830840, -2143025144, -2132276983, 526087, -2141452280, -2145646584, -2137519863, 5441291, -2139355128, -2143549432, -2133325559, 1509129, -2140403704, -2144598008, -2135422711, -2146695160, -2138306552, -2142500856, -2131228407, 198407, -2142107640, -2146301944, 10684429, 2295562, -2140010488, -2144204792, -2134636279, 722696, -2141059064, -2145253368, -2136733431, -2147350520, -2138961912, -2143156216, -2132539127, 460551, -2141583352, -2145777656, -2137782007, 4392715, -2139486200, -2143680504, -2133587703, 1246985, -2140534776, -2144729080, -2135684855, -2146826232, -2138437624, -2142631928, -2131490551, 329479, -2141845496, -2146039800, 34824, 3344138, -2139748344, -2143942648, -2134111991, 984840, -2140796920, -2144991224, -2136209143, -2147088376, -2138699768, -2142894072, -2132014839, 591623, -2141321208, -2145515512, -2137257719, 6489867, -2139224056, -2143418360, -2133063415, 1771273, -2140272632, -2144466936, -2135160567, -2146564088, -2138175480, -2142369784, -2130966263, 5895, -2142173176, -2146367480, 8587277, 2033417, -2140076024, -2144270328, -2134767351, 657159, -2141124600, -2145318904, -2136864503, -2147416056, -2139027448, -2143221752, -2132670199, 395015, -2141648888, -2145843192, -2137913079, 3868426, -2139551736, -2143746040, -2133718775, 1115912, -2140600312, -2144794616, -2135815927, -2146891768, -2138503160, -2142697464, -2131621623, 263943, -2141911032, -2146105336, 16910344, 2819850, -2139813880, -2144008184, -2134243063, 853768, -2140862456, -2145056760, -2136340215, -2147153912, -2138765304, -2142959608, -2132145911, 526087, -2141386744, -2145581048, -2137388791, 5441291, -2139289592, -2143483896, -2133194487, 1509129, -2140338168, -2144532472, -2135291639, -2146629624, -2138241016, -2142435320, -2131097335, 198407, -2142042104, -2146236408, 12781581, 2295562, -2139944952, -2144139256, -2134505207, 722696, -2140993528, -2145187832, -2136602359, -2147284984, -2138896376, -2143090680, -2132408055, 460551, -2141517816, -2145712120, -2137650935, 4392715, -2139420664, -2143614968, -2133456631, 1246985, -2140469240, -2144663544, -2135553783, -2146760696, -2138372088, -2142566392, -2131359479, 329479, -2141779960, -2145974264, 34824, 3344138, -2139682808, -2143877112, -2133980919, 984840, -2140731384, -2144925688, -2136078071, -2147022840, -2138634232, -2142828536, -2131883767, 591623, -2141255672, -2145449976, -2137126647, 6489867, -2139158520, -2143352824, -2132932343, 1771273, -2140207096, -2144401400, -2135029495, -2146498552, -2138109944, -2142304248, -2130835191, 5895, -2142238712, -2146433016, 7538700, 2033417, -2140141560, -2144335864, -2134832887, 657159, -2141190136, -2145384440, -2136930039, -2147481592, -2139092984, -2143287288, -2132735735, 395015, -2141714424, -2145908728, -2137978615, 3868426, -2139617272, -2143811576, -2133784311, 1115912, -2140665848, -2144860152, -2135881463, -2146957304, -2138568696, -2142763000, -2131687159, 263943, -2141976568, -2146170872, 14878733, 2819850, -2139879416, -2144073720, -2134308599, 853768, -2140927992, -2145122296, -2136405751, -2147219448, -2138830840, -2143025144, -2132211447, 526087, -2141452280, -2145646584, -2137454327, 5441291, -2139355128, -2143549432, -2133260023, 1509129, -2140403704, -2144598008, -2135357175, -2146695160, -2138306552, -2142500856, -2131162871, 198407, -2142107640, -2146301944, 10684429, 2295562, -2140010488, -2144204792, -2134570743, 722696, -2141059064, -2145253368, -2136667895, -2147350520, -2138961912, -2143156216, -2132473591, 460551, -2141583352, -2145777656, -2137716471, 4392715, -2139486200, -2143680504, -2133522167, 1246985, -2140534776, -2144729080, -2135619319, -2146826232, -2138437624, -2142631928, -2131425015, 329479, -2141845496, -2146039800, 34824, 3344138, -2139748344, -2143942648, -2134046455, 984840, -2140796920, -2144991224, -2136143607, -2147088376, -2138699768, -2142894072, -2131949303, 591623, -2141321208, -2145515512, -2137192183, 6489867, -2139224056, -2143418360, -2132997879, 1771273, -2140272632, -2144466936, -2135095031, -2146564088, -2138175480, -2142369784, -2130900727, 5895, -2142173176, -2146367480, 8587277, 2033417, -2140076024, -2144270328, -2134701815, 657159, -2141124600, -2145318904, -2136798967, -2147416056, -2139027448, -2143221752, -2132604663, 395015, -2141648888, -2145843192, -2137847543, 3868426, -2139551736, -2143746040, -2133653239, 1115912, -2140600312, -2144794616, -2135750391, -2146891768, -2138503160, -2142697464, -2131556087, 263943, -2141911032, -2146105336, 16910344, 2819850, -2139813880, -2144008184, -2134177527, 853768, -2140862456, -2145056760, -2136274679, -2147153912, -2138765304, -2142959608, -2132080375, 526087, -2141386744, -2145581048, -2137323255, 5441291, -2139289592, -2143483896, -2133128951, 1509129, -2140338168, -2144532472, -2135226103, -2146629624, -2138241016, -2142435320, -2131031799, 198407, -2142042104, -2146236408, 12781581, 2295562, -2139944952, -2144139256, -2134439671, 722696, -2140993528, -2145187832, -2136536823, -2147284984, -2138896376, -2143090680, -2132342519, 460551, -2141517816, -2145712120, -2137585399, 4392715, -2139420664, -2143614968, -2133391095, 1246985, -2140469240, -2144663544, -2135488247, -2146760696, -2138372088, -2142566392, -2131293943, 329479, -2141779960, -2145974264, 34824, 3344138, -2139682808, -2143877112, -2133915383, 984840, -2140731384, -2144925688, -2136012535, -2147022840, -2138634232, -2142828536, -2131818231, 591623, -2141255672, -2145449976, -2137061111, 6489867, -2139158520, -2143352824, -2132866807, 1771273, -2140207096, -2144401400, -2134963959, -2146498552, -2138109944, -2142304248, -2130769655, 5895, -2142238712, -2146433016, 7538700, 2033417, -2140141560, -2144335864, -2134898423, 657159, -2141190136, -2145384440, -2136995575, -2147481592, -2139092984, -2143287288, -2132801271, 395015, -2141714424, -2145908728, -2138044151, 3868426, -2139617272, -2143811576, -2133849847, 1115912, -2140665848, -2144860152, -2135946999, -2146957304, -2138568696, -2142763000, -2131752695, 263943, -2141976568, -2146170872, 14878733, 2819850, -2139879416, -2144073720, -2134374135, 853768, -2140927992, -2145122296, -2136471287, -2147219448, -2138830840, -2143025144, -2132276983, 526087, -2141452280, -2145646584, -2137519863, 5441291, -2139355128, -2143549432, -2133325559, 1509129, -2140403704, -2144598008, -2135422711, -2146695160, -2138306552, -2142500856, -2131228407, 198407, -2142107640, -2146301944, 10684429, 2295562, -2140010488, -2144204792, -2134636279, 722696, -2141059064, -2145253368, -2136733431, -2147350520, -2138961912, -2143156216, -2132539127, 460551, -2141583352, -2145777656, -2137782007, 4392715, -2139486200, -2143680504, -2133587703, 1246985, -2140534776, -2144729080, -2135684855, -2146826232, -2138437624, -2142631928, -2131490551, 329479, -2141845496, -2146039800, 34824, 3344138, -2139748344, -2143942648, -2134111991, 984840, -2140796920, -2144991224, -2136209143, -2147088376, -2138699768, -2142894072, -2132014839, 591623, -2141321208, -2145515512, -2137257719, 6489867, -2139224056, -2143418360, -2133063415, 1771273, -2140272632, -2144466936, -2135160567, -2146564088, -2138175480, -2142369784, -2130966263, 5895, -2142173176, -2146367480, 8587277, 2033417, -2140076024, -2144270328, -2134767351, 657159, -2141124600, -2145318904, -2136864503, -2147416056, -2139027448, -2143221752, -2132670199, 395015, -2141648888, -2145843192, -2137913079, 3868426, -2139551736, -2143746040, -2133718775, 1115912, -2140600312, -2144794616, -2135815927, -2146891768, -2138503160, -2142697464, -2131621623, 263943, -2141911032, -2146105336, 16910344, 2819850, -2139813880, -2144008184, -2134243063, 853768, -2140862456, -2145056760, -2136340215, -2147153912, -2138765304, -2142959608, -2132145911, 526087, -2141386744, -2145581048, -2137388791, 5441291, -2139289592, -2143483896, -2133194487, 1509129, -2140338168, -2144532472, -2135291639, -2146629624, -2138241016, -2142435320, -2131097335, 198407, -2142042104, -2146236408, 12781581, 2295562, -2139944952, -2144139256, -2134505207, 722696, -2140993528, -2145187832, -2136602359, -2147284984, -2138896376, -2143090680, -2132408055, 460551, -2141517816, -2145712120, -2137650935, 4392715, -2139420664, -2143614968, -2133456631, 1246985, -2140469240, -2144663544, -2135553783, -2146760696, -2138372088, -2142566392, -2131359479, 329479, -2141779960, -2145974264, 34824, 3344138, -2139682808, -2143877112, -2133980919, 984840, -2140731384, -2144925688, -2136078071, -2147022840, -2138634232, -2142828536, -2131883767, 591623, -2141255672, -2145449976, -2137126647, 6489867, -2139158520, -2143352824, -2132932343, 1771273, -2140207096, -2144401400, -2135029495, -2146498552, -2138109944, -2142304248, -2130835191, 5895, -2142238712, -2146433016, 7538700, 2033417, -2140141560, -2144335864, -2134832887, 657159, -2141190136, -2145384440, -2136930039, -2147481592, -2139092984, -2143287288, -2132735735, 395015, -2141714424, -2145908728, -2137978615, 3868426, -2139617272, -2143811576, -2133784311, 1115912, -2140665848, -2144860152, -2135881463, -2146957304, -2138568696, -2142763000, -2131687159, 263943, -2141976568, -2146170872, 14878733, 2819850, -2139879416, -2144073720, -2134308599, 853768, -2140927992, -2145122296, -2136405751, -2147219448, -2138830840, -2143025144, -2132211447, 526087, -2141452280, -2145646584, -2137454327, 5441291, -2139355128, -2143549432, -2133260023, 1509129, -2140403704, -2144598008, -2135357175, -2146695160, -2138306552, -2142500856, -2131162871, 198407, -2142107640, -2146301944, 10684429, 2295562, -2140010488, -2144204792, -2134570743, 722696, -2141059064, -2145253368, -2136667895, -2147350520, -2138961912, -2143156216, -2132473591, 460551, -2141583352, -2145777656, -2137716471, 4392715, -2139486200, -2143680504, -2133522167, 1246985, -2140534776, -2144729080, -2135619319, -2146826232, -2138437624, -2142631928, -2131425015, 329479, -2141845496, -2146039800, 34824, 3344138, -2139748344, -2143942648, -2134046455, 984840, -2140796920, -2144991224, -2136143607, -2147088376, -2138699768, -2142894072, -2131949303, 591623, -2141321208, -2145515512, -2137192183, 6489867, -2139224056, -2143418360, -2132997879, 1771273, -2140272632, -2144466936, -2135095031, -2146564088, -2138175480, -2142369784, -2130900727, 5895, -2142173176, -2146367480, 8587277, 2033417, -2140076024, -2144270328, -2134701815, 657159, -2141124600, -2145318904, -2136798967, -2147416056, -2139027448, -2143221752, -2132604663, 395015, -2141648888, -2145843192, -2137847543, 3868426, -2139551736, -2143746040, -2133653239, 1115912, -2140600312, -2144794616, -2135750391, -2146891768, -2138503160, -2142697464, -2131556087, 263943, -2141911032, -2146105336, 16910344, 2819850, -2139813880, -2144008184, -2134177527, 853768, -2140862456, -2145056760, -2136274679, -2147153912, -2138765304, -2142959608, -2132080375, 526087, -2141386744, -2145581048, -2137323255, 5441291, -2139289592, -2143483896, -2133128951, 1509129, -2140338168, -2144532472, -2135226103, -2146629624, -2138241016, -2142435320, -2131031799, 198407, -2142042104, -2146236408, 12781581, 2295562, -2139944952, -2144139256, -2134439671, 722696, -2140993528, -2145187832, -2136536823, -2147284984, -2138896376, -2143090680, -2132342519, 460551, -2141517816, -2145712120, -2137585399, 4392715, -2139420664, -2143614968, -2133391095, 1246985, -2140469240, -2144663544, -2135488247, -2146760696, -2138372088, -2142566392, -2131293943, 329479, -2141779960, -2145974264, 34824, 3344138, -2139682808, -2143877112, -2133915383, 984840, -2140731384, -2144925688, -2136012535, -2147022840, -2138634232, -2142828536, -2131818231, 591623, -2141255672, -2145449976, -2137061111, 6489867, -2139158520, -2143352824, -2132866807, 1771273, -2140207096, -2144401400, -2134963959, -2146498552, -2138109944, -2142304248, -2130769655];

const FIXED_DIST_TABLE: i32[] = [66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669, 66821, 16844044, 1115400, 268502288, 328966, 67175694, 4261130, 1073808658, 197893, 33621261, 2163977, 536937745, 591111, 134284559, 8455435, 17669, 132357, 25232652, 1639688, 402720016, 460038, 100730126, 6358282, 1610679570, 263429, 50398477, 3212553, 805373201, 853255, 201393423, 12649739, 17669];

function __fixed(own s: __State, data: [u8]): __State {
  return __codes(s, data, FIXED_LIT_TABLE, FIXED_DIST_TABLE);
}

function __fixed_tables(s: __State): __Tables {
  return __Tables { state: s, literal: FIXED_LIT_TABLE, distance: FIXED_DIST_TABLE };
}

function __code_length_order(): i32[] {
  return [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];
}

function __dynamic(own s: __State, data: [u8]): __State {
  let tables = __dynamic_tables(s, data);
  if (tables.state.failed) {
    return tables.state;
  }
  return __codes(__State { ...tables.state }, data, tables.literal, tables.distance);
}

function __dynamic_tables(own s: __State, data: [u8]): __Tables {
  s = __bits(s, data, 14);
  let nlen = (s.last & 31) + 257;
  let ndist = (s.last >> 5 & 31) + 1;
  let ncode = (s.last >> 10) + 4;
  if (nlen > 286 || ndist > 30) {
    return __empty_tables(__fail(s, Malformed("too many length or distance codes")));
  }
  let order = __code_length_order();
  let lengths = __alloc_i32(19);
  let count = __alloc_i32(16);
  let i = 0;
  while (i < ncode) {
    let k = cmp.min(ncode - i, 7);
    s = __bits(s, data, 3 * k);
    let three = s.last;
    let stop = i + k;
    while (i < stop) {
      let len = three & 7;
      lengths = lengths.with(order[i], len);
      count = count.with(len, count[len] + 1);
      three >>= 3;
      i += 1;
    }
  }
  let left = __left(count, 0);
  if (left != 0) {
    return __empty_tables(__fail(s, Malformed("an incomplete or over-subscribed code length code")));
  }
  s = __code_lengths(s, data, __build(lengths, 0, 19, count, 0, left, 7, LENGTH_VALUES), nlen, ndist);
  if (s.failed) {
    return __empty_tables(s);
  }
  return __dynamic_codes(s, nlen, ndist);
}

// `s.counts` holds the count of the codes of each length of the
// literal/length code, then of the distance code.
function __dynamic_codes(own s: __State, nlen: i32, ndist: i32): __Tables {
  let all = s.lengths;
  let counts = s.counts;
  s = __State { ...s, lengths: [], counts: [] };
  if (all[256] == 0) {
    return __empty_tables(__fail(s, Malformed("no end-of-block code")));
  }
  let ll = __left(counts, 0);
  if (__invalid(counts, 0, ll, nlen, false)) {
    return __empty_tables(__fail(s, Malformed("an incomplete or over-subscribed literal/length code")));
  }
  let dl = __left(counts, 16);
  if (__invalid(counts, 16, dl, ndist, true)) {
    return __empty_tables(__fail(s, Malformed("an incomplete or over-subscribed distance code")));
  }
  let lit = __build(all, 0, nlen, counts, 0, ll, LIT_BITS, LIT_VALUES);
  let dist = __build(all, nlen, ndist, counts, 16, dl, DIST_BITS, DIST_VALUES);
  return __Tables { state: s, literal: lit, distance: dist };
}

// The count of extra bits of a repeat symbol, and the count that the extra
// bits add to.
const REPEAT_EXTRA: i32[] = [2, 3, 7];

const REPEAT_BASE: i32[] = [3, 3, 11];

// Reads `nlen` code lengths of the literal/length code and `ndist` of the
// distance code with the code of `table`, and counts the codes of each
// length of the two codes. A symbol with its extra bits needs 14 bits at
// most, so one refill serves each symbol. The whole bytes that stay in the
// bit buffer go back to the input at the end.
// fern-lint: allow cyclomatic-complexity
function __code_lengths(own s: __State, data: [u8], table: i32[], nlen: i32, ndist: i32): __State {
  let n = nlen + ndist;
  let lengths = __alloc_i32(n);
  let counts = __alloc_i32(32);
  let i = 0;
  let pos = s.pos;
  let buf = s.bitbuf as u64;
  let cnt = s.bitcnt;
  let end = data.len();
  let error: Option[InflateError] = None;
  if (table.len() < 128) {
    return __fail(s, Truncated);
  }
  while (i < n) {
    if (cnt < 14) {
      if (pos <= end - 8) {
        buf |= __le64(data, pos) << cnt as u64;
        pos += (cnt >> 3 ^ 7) & 7;
        cnt |= 56;
      } else {
        while (cnt <= 56 && pos < end) {
          buf |= data[pos] as u64 << cnt as u64;
          pos += 1;
          cnt += 8;
        }
      }
    }
    let entry = table[(buf & 127u64) as i32];
    let used = entry & 255;
    if (used > cnt) {
      error = Some(Truncated);
      break;
    }
    buf >>= used as u64;
    cnt -= used;
    let sym = entry >> 16;
    if (sym < 16) {
      lengths = lengths.with(i, sym);
      let c = if (i < nlen) { sym } else { sym + 16 };
      counts = counts.with(c, counts[c] + 1);
      i += 1;
      continue;
    }
    if (sym == 16 && i == 0) {
      error = Some(Malformed("a repeat with no length before it"));
      break;
    }
    let extra = REPEAT_EXTRA[sym - 16];
    if (extra > cnt) {
      error = Some(Truncated);
      break;
    }
    let times = REPEAT_BASE[sym - 16] + __low(buf, extra) as i32;
    buf >>= extra as u64;
    cnt -= extra;
    if (i + times > n) {
      error = Some(Malformed("more code lengths than codes"));
      break;
    }
    let value = if (sym == 16) { lengths[i - 1] } else { 0 };
    let stop = i + times;
    let first = cmp.max(cmp.min(stop, nlen) - i, 0);
    counts = counts.with(value, counts[value] + first);
    counts = counts.with(value + 16, counts[value + 16] + times - first);
    if (value != 0) {
      while (i < stop) {
        lengths = lengths.with(i, value);
        i += 1;
      }
    }
    i = stop;
  }
  pos -= cnt >> 3;
  cnt &= 7;
  s = __State { ...s, pos: pos, bitbuf: __low(buf, cnt) as i32, bitcnt: cnt, lengths: lengths, counts: counts };
  return match (error) {
    Some(e) => __fail(s, e),
    None => s,
  };
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

// The deflate data of the gzip member at `from`, with no check of its
// trailer. `consumed` counts the bytes of the member with its trailer.
pub function gzip_member_data(input: [u8], from: i32, max_out: i32): Result[Inflated, InflateError] {
  let body = __gzip_header(input, from)?;
  let inflated = inflate_from(input, body, max_out)?;
  return Ok(Inflated { out: inflated.out, consumed: body + inflated.consumed + 8 - from });
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
  if (from < 0) {
    return Err(BadHeader("an offset below zero"));
  }
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
// most bytes after which b cannot pass 2^32. A word of eight bytes holds
// four lanes of 16 bits for the even bytes and four for the odd bytes.
// Each lane counts at most eight words before the lanes are added up, so
// no lane passes 16 bits. In a chunk of `len` bytes, b gets `len` times
// the a before the chunk, eight times the bytes before each word, and
// 8 - k times byte k of each word.
pub function adler32(bs: [u8]): u32 {
  let a = 1u64;
  let b = 0u64;
  let n = bs.len();
  let last = n - 16;
  let i = 0;
  while (i <= last) {
    let stop = i + cmp.min(n - i >> 4, 347) * 16;
    let len = (stop - i) as u64;
    let sum = 0u64;
    let before = 0u64;
    let weighted = 0u64;
    while (i < stop) {
      let run = cmp.min(stop, i + 64);
      let even = 0u64;
      let odd = 0u64;
      let r = 0u64;
      let words = 0u64;
      while (i <= last) {
        let w = __le64(bs, i);
        let v = __le64(bs, i + 8);
        r += even + odd;
        even += w & BYTE_LANES;
        odd += w >> 8 & BYTE_LANES;
        r += even + odd;
        even += v & BYTE_LANES;
        odd += v >> 8 & BYTE_LANES;
        i += 16;
        words += 2u64;
        if (i >= run) {
          break;
        }
      }
      before += words * sum + (r * LANE_SUM >> 48);
      sum += (even + odd) * LANE_SUM >> 48;
      weighted += (even * EVEN_WEIGHTS >> 48) + (odd * ODD_WEIGHTS >> 48);
    }
    b = (b + len * a + 8u64 * before + weighted) % 65521u64;
    a = (a + sum) % 65521u64;
  }
  while (i < n) {
    a += bs[i] as u64;
    b += a;
    i += 1;
  }
  a %= 65521u64;
  b %= 65521u64;
  return (b << 16 | a) as u32;
}

// The low byte of each 16-bit lane.
const BYTE_LANES: u64 = 71777214294589695u64;

// A product with it adds the four lanes in the top lane.
const LANE_SUM: u64 = 281479271743489u64;

// Products with these put 8, 6, 4 and 2 times the even lanes, and 7, 5, 3
// and 1 times the odd lanes, in the top lane.
const EVEN_WEIGHTS: u64 = 2251825583751170u64;

const ODD_WEIGHTS: u64 = 1970346312007681u64;

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
  literal: i32[],
  distance_code: i32[],
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
  let bits = __State { pos: 0, bitbuf: 0, bitcnt: 0, out: [], n: 0, max_out: max_out, last: 0, failed: false, error: Truncated, lengths: [], counts: [] };
  let initial = __ReadCursor {
    state: bits, literal: [], distance_code: [], history: [], total: 0, phase: ReadHeader,
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
  let state = __symbol(__State { ...cursor.state }, input, cursor.literal, LIT_BITS, "literal/length");
  cursor = __ReadCursor { ...cursor, state: state };
  if (state.failed) {
    return cursor;
  }
  let e = state.last;
  if (e < 0) {
    return __read_emit(cursor, (e >> 16) as u8);
  }
  if ((e & E_END) != 0) {
    return __read_block_end(cursor);
  }
  if ((e & E_PAST) != 0) {
    return __ReadCursor { ...cursor, state: __fail(state, Malformed("a length code past 285")) };
  }
  return __read_match(cursor, input, e);
}

// `e` is the entry of a length. The extra bits of an entry are the bits it
// takes past its code.
function __read_match(own cursor: __ReadCursor, input: [u8], e: i32): __ReadCursor {
  let state = __bits(__State { ...cursor.state }, input, (e & 255) - (e >> 8 & 15));
  let length = (e >> 16) + state.last;
  state = __symbol(state, input, cursor.distance_code, DIST_BITS, "distance");
  if (state.failed) {
    return __ReadCursor { ...cursor, state: state };
  }
  let d = state.last;
  state = __bits(state, input, (d & 255) - (d >> 8 & 15));
  let distance = (d >> 16) + state.last;
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

### The encoder of zlib

`zlib_compress` follows `deflate.c` and `trees.c` of zlib 1.2 and 1.3 with a window of 32 KiB, a memory level of 8 and the default strategy. Each step and each choice is that of zlib: the hash chains, the lazy matches of the levels 4 to 9, the blocks of 16383 symbols, the Huffman trees with the order of the heap of zlib, and the choice of a stored, a static or a dynamic block. The bytes after the end of the input do not change the output of zlib, so the matches stop at the end of the input.

Level 0 follows `deflate_stored` of zlib 1.2.12 and later, and the calls of `deflate` with `Z_FINISH` that a program makes until the stream ends. Each call has the same space for output. The space changes where each stored block ends, because zlib writes a block directly to the output only when the block fits in that space.

```fern
<<35 the encoder of zlib>>=
// The zlib stream of `input` at a level of 0 to 9, with the bytes that
// zlib 1.2 and 1.3 write with a window of 32 KiB, a memory level of 8 and
// the default strategy (deflate.c and trees.c of zlib). A level below 0 is
// level 6, as in zlib.
pub function zlib_compress(input: [u8], level: i32): u8[] {
  return zlib_compress_in(input, level, 2147483647);
}

// The zlib stream of `input` that zlib writes when each call to `deflate`
// has `room` bytes for output. Only level 0 uses `room`.
pub function zlib_compress_in(input: [u8], level_in: i32, room: i32): u8[] {
  if (level_in == 0) {
    return __zstored_stream(input, cmp.max(room, 1));
  }
  let level = if (level_in < 0) { 6 } else { cmp.min(level_in, 9) };
  let flags = if (level < 2) { 0 } else if (level < 6) { 1 } else if (level == 6) { 2 } else { 3 };
  let header = 8 + (7 << 4) << 8 | flags << 6;
  header += 31 - header % 31;
  let out = buf_new(input.len() / 2 + 64);
  buf_push_byte(out, header >> 8);
  buf_push_byte(out, header & 255);
  let w = __zdeflate(input, level, __ZBits { out: out, acc: 0, n: 0 });
  let a = adler32(input);
  buf_push_byte(w.out, (a >> 24) as i32);
  buf_push_byte(w.out, (a >> 16 & 255u32) as i32);
  buf_push_byte(w.out, (a >> 8 & 255u32) as i32);
  buf_push_byte(w.out, (a & 255u32) as i32);
  let bytes = buf_take_bytes(w.out);
  buf_free(w.out);
  return bytes;
}

// The state of level 0: the positions of `deflate_stored` in the input,
// the bytes that wait for space (`pending` from `pend_at`), and the space
// of the call. The window holds the input from `wbase`.
struct __ZS { input: [u8], nin: i32, wbase: i32, strstart: i32, block_start: i32, pend: u8[], pend_at: i32, room: i32, out: usize }

const __ZS_WINDOW: i32 = 65536;

// The stream of level 0: the calls to `deflate` with `Z_FINISH`, each with
// `room` bytes of space, until the end of the stream.
function __zstored_stream(input: [u8], room: i32): u8[] {
  let empty: u8[] = [];
  let s = __ZS { input: input, nin: 0, wbase: 0, strstart: 0, block_start: 0, pend: empty, pend_at: 0, room: 0, out: buf_new(input.len() + input.len() / 4096 * 5 + 64) };
  let status = 0;
  let trailer = false;
  while (true) {
    s = __zs_flush(__ZS { ...s, room: room });
    if (s.room == 0) {
      continue;
    }
    if (status == 0) {
      status = 1;
      s = __zs_flush(__zs_pend(s, [120u8, 1u8]));
      if (s.pend.len() > s.pend_at) {
        continue;
      }
    }
    if (status == 1) {
      let (t, state) = __zs_deflate(s);
      s = t;
      status = if (state > 0) { 2 } else { 1 };
      if (state < 2) {
        continue;
      }
    }
    if (trailer) {
      break;
    }
    trailer = true;
    let a = adler32(input);
    s = __zs_flush(__zs_pend(s, [(a >> 24) as u8, (a >> 16 & 255u32) as u8, (a >> 8 & 255u32) as u8, (a & 255u32) as u8]));
    if (s.pend.len() == s.pend_at) {
      break;
    }
  }
  let bytes = buf_take_bytes(s.out);
  buf_free(s.out);
  return bytes;
}

// `bytes` at the end of the pending bytes.
function __zs_pend(s: __ZS, bytes: u8[]): __ZS {
  return __ZS { ...s, pend: s.pend.concat(bytes) };
}

// The pending bytes that fit in the space: `flush_pending` of zlib.
function __zs_flush(s: __ZS): __ZS {
  let n = cmp.min(s.pend.len() - s.pend_at, s.room);
  for i in s.pend_at..s.pend_at + n {
    buf_push_byte(s.out, s.pend[i] as i32);
  }
  if (s.pend_at + n == s.pend.len()) {
    let empty: u8[] = [];
    return __ZS { ...s, pend: empty, pend_at: 0, room: s.room - n };
  }
  return __ZS { ...s, pend_at: s.pend_at + n, room: s.room - n };
}

// The header of a stored block of `len` bytes, the last one if `last`.
function __zs_header(len: i32, last: boolean): u8[] {
  let first: u8 = if (last) { 1 } else { 0 };
  return [first, (len & 255) as u8, (len >> 8 & 255) as u8, (~len & 255) as u8, (~len >> 8 & 255) as u8];
}

// The input bytes from `from` to `to` at the end of the output.
function __zs_copy(s: __ZS, from: i32, to: i32): __ZS {
  for i in from..to {
    buf_push_byte(s.out, s.input[i] as i32);
  }
  return __ZS { ...s, room: s.room - (to - from) };
}

// One call of `deflate_stored` with `Z_FINISH`: 0 is `need_more`, 1 is
// `finish_started` and 2 is `finish_done`.
function __zs_deflate(s_in: __ZS): (__ZS, i32) {
  let (s, last) = __zs_direct(s_in);
  if (last) {
    return (s, 2);
  }
  let avail = s.input.len() - s.nin;
  let have = __ZS_WINDOW - s.strstart;
  if (avail > have && s.block_start >= __Z_WSIZE) {
    s = __ZS { ...s, block_start: s.block_start - __Z_WSIZE, strstart: s.strstart - __Z_WSIZE, wbase: s.wbase + __Z_WSIZE };
    have += __Z_WSIZE;
  }
  have = cmp.min(have, avail);
  s = __ZS { ...s, nin: s.nin + have, strstart: s.strstart + have };
  avail -= have;
  let left = s.strstart - s.block_start;
  if (left < __Z_WSIZE && (avail != 0 || left > 65531)) {
    return (s, 0);
  }
  let len = cmp.min(left, 65531);
  let done = avail == 0 && len == left;
  let start = s.wbase + s.block_start;
  s = __zs_pend(s, __zs_header(len, done));
  s = __ZS { ...s, pend: s.pend.concat(s.input[start:start + len]), block_start: s.block_start + len };
  return (__zs_flush(s), if (done) { 1 } else { 0 });
}

// The stored blocks that `deflate_stored` writes directly to the output,
// and the window after them. True if the last block is written.
function __zs_direct(s_in: __ZS): (__ZS, boolean) {
  let s = s_in;
  let first = s.nin;
  let last = false;
  while (!last && s.room >= 5) {
    let left = s.strstart - s.block_start;
    let rest = left + (s.input.len() - s.nin);
    let len = cmp.min(cmp.min(65535, rest), s.room - 5);
    if (len < __Z_WSIZE && len != rest) {
      break;
    }
    last = len == rest;
    s = __zs_flush(__zs_pend(s, __zs_header(len, last)));
    let from_window = cmp.min(left, len);
    s = __zs_copy(s, s.wbase + s.block_start, s.wbase + s.block_start + from_window);
    s = __ZS { ...s, block_start: s.block_start + from_window };
    s = __zs_copy(s, s.nin, s.nin + len - from_window);
    s = __ZS { ...s, nin: s.nin + len - from_window };
  }
  let used = s.nin - first;
  if (used >= __Z_WSIZE) {
    s = __ZS { ...s, wbase: s.nin - __Z_WSIZE, strstart: __Z_WSIZE, block_start: __Z_WSIZE };
  } else if (used > 0) {
    if (__ZS_WINDOW - s.strstart <= used) {
      s = __ZS { ...s, strstart: s.strstart - __Z_WSIZE, wbase: s.wbase + __Z_WSIZE };
    }
    s = __ZS { ...s, strstart: s.strstart + used, block_start: s.strstart + used };
  }
  return (s, last);
}

// The bits that the encoder writes: a builder and the bits not yet in a
// byte, the first bit lowest.
struct __ZBits { out: usize, acc: i64, n: i32 }

function __zput(w: __ZBits, value: i32, bits: i32): __ZBits {
  let acc = w.acc | (value as i64 & (1 as i64 << bits) - 1) << w.n;
  let n = w.n + bits;
  while (n >= 8) {
    buf_push_byte(w.out, (acc & 255) as i32);
    acc >>= 8;
    n -= 8;
  }
  return __ZBits { out: w.out, acc: acc, n: n };
}

// The bits up to the next byte: `bi_windup` of zlib.
function __zalign(w: __ZBits): __ZBits {
  if (w.n > 0) {
    buf_push_byte(w.out, (w.acc & 255) as i32);
  }
  return __ZBits { out: w.out, acc: 0, n: 0 };
}

// The settings of a level: `configuration_table` of zlib.
struct __ZLevel { good: i32, lazy: i32, nice: i32, chain: i32, slow: boolean }

function __zlevel(level: i32): __ZLevel {
  let goods = [4, 4, 4, 4, 8, 8, 8, 32, 32];
  let lazies = [4, 5, 6, 4, 16, 16, 32, 128, 258];
  let nices = [8, 16, 32, 16, 32, 128, 128, 258, 258];
  let chains = [4, 8, 32, 16, 32, 128, 256, 1024, 4096];
  let k = level - 1;
  return __ZLevel { good: goods[k], lazy: lazies[k], nice: nices[k], chain: chains[k], slow: level >= 4 };
}

// The state of the encoder. Positions are in the window of zlib: the byte
// at window position `p` is the input byte `offset + p`, and a position of
// 0 in `head` and `prev` is no position, as in zlib.
struct __Z { input: [u8], cfg: __ZLevel, offset: i32, strstart: i32, lookahead: i32, avail: i32, block_start: i32, match_start: i32, match_length: i32, prev_length: i32, prev_match: i32, match_available: boolean, head: i32[], prev: i32[], dists: i32[], lcs: i32[], w: __ZBits }

const __Z_WSIZE: i32 = 32768;

const __Z_MAX_DIST: i32 = 32506;

const __Z_MIN_LOOKAHEAD: i32 = 262;

// The window after `fill_window` of zlib: the move of the upper half of
// the window down when the position is high, with the slide of the hash
// chains, and the input that fits.
function __zfill(z_in: __Z): __Z {
  let z = z_in;
  while (true) {
    let more = 2 * __Z_WSIZE - z.lookahead - z.strstart;
    if (z.strstart >= __Z_WSIZE + __Z_MAX_DIST) {
      z = __Z { ...z, offset: z.offset + __Z_WSIZE, strstart: z.strstart - __Z_WSIZE, match_start: z.match_start - __Z_WSIZE, block_start: z.block_start - __Z_WSIZE, head: __zslide(z.head), prev: __zslide(z.prev) };
      more += __Z_WSIZE;
    }
    if (z.avail == 0) {
      return z;
    }
    let n = cmp.min(more, z.avail);
    z = __Z { ...z, lookahead: z.lookahead + n, avail: z.avail - n };
    if (z.lookahead >= __Z_MIN_LOOKAHEAD || z.avail == 0) {
      return z;
    }
  }
  return z;
}

function __zslide(own t: i32[]): i32[] {
  let i = 0;
  while (i < t.len()) {
    let m = t[i];
    t = t.with(i, if (m >= __Z_WSIZE) { m - __Z_WSIZE } else { 0 });
    i += 1;
  }
  return t;
}

function __zbyte(z: __Z, p: i32): i32 {
  return z.input[z.offset + p] as i32;
}

// Puts the string at `p` in the hash chains and gives the last position
// with its hash: INSERT_STRING of zlib.
function __zinsert(z: __Z, p: i32): (__Z, i32) {
  let h = (__zbyte(z, p) << 10 ^ __zbyte(z, p + 1) << 5 ^ __zbyte(z, p + 2)) & 32767;
  let last = z.head[h];
  return (__Z { ...z, prev: z.prev.with(p & 32767, last), head: z.head.with(h, p) }, last);
}

// The length of the common start of the strings at `a` and `b`, at most
// `limit`.
function __zcommon(z: __Z, a: i32, b: i32, limit: i32): i32 {
  return __mismatch_bytes(z.input, z.offset + a, z.input, z.offset + b, limit);
}

// The longest match at the position before `cur`: `longest_match` of
// zlib. It gives the length and sets `match_start`. The bytes after the
// input do not change the result, so the length stops at the input.
function __zlongest(z: __Z, cur_in: i32): (__Z, i32) {
  let cur = cur_in;
  let chain = if (z.prev_length >= z.cfg.good) { z.cfg.chain >> 2 } else { z.cfg.chain };
  let best = z.prev_length;
  let start = z.match_start;
  let nice = cmp.min(z.cfg.nice, z.lookahead);
  let limit = if (z.strstart > __Z_MAX_DIST) { z.strstart - __Z_MAX_DIST } else { 0 };
  let max = cmp.min(258, z.lookahead);
  while (true) {
    let len = __zcommon(z, cur, z.strstart, max);
    if (len > best) {
      start = cur;
      best = len;
      if (len >= nice) {
        break;
      }
    }
    cur = z.prev[cur & 32767];
    chain -= 1;
    if (cur <= limit || chain == 0) {
      break;
    }
  }
  return (__Z { ...z, match_start: start }, cmp.min(best, z.lookahead));
}

// The input as one stream of blocks: `deflate_fast` for the levels 1 to 3
// and `deflate_slow` for the others.
function __zdeflate(input: [u8], level: i32, w: __ZBits): __ZBits {
  let none: i32[] = [];
  let z = __Z { input: input, cfg: __zlevel(level), offset: 0, strstart: 0, lookahead: 0, avail: input.len(), block_start: 0, match_start: 0, match_length: 2, prev_length: 2, prev_match: 0, match_available: false, head: __alloc_i32(32768), prev: __alloc_i32(32768), dists: none, lcs: none, w: w };
  z = if (z.cfg.slow) { __zslow(z) } else { __zfast(z) };
  return __zalign(__zflush(z, true).w);
}

// A literal or a match, and the block that a full buffer of symbols ends:
// `_tr_tally` of zlib, whose buffer holds 16383 symbols.
function __ztally(z: __Z, dist: i32, lc: i32): (__Z, boolean) {
  let t = __Z { ...z, dists: z.dists.append(dist), lcs: z.lcs.append(lc) };
  return (t, t.dists.len() == 16383);
}

// `deflate_fast` of zlib.
// fern-lint: allow cyclomatic-complexity
function __zfast(z_in: __Z): __Z {
  let z = z_in;
  while (true) {
    if (z.lookahead < __Z_MIN_LOOKAHEAD) {
      z = __zfill(z);
      if (z.lookahead == 0) {
        return z;
      }
    }
    let head = 0;
    if (z.lookahead >= 3) {
      let (z1, h) = __zinsert(z, z.strstart);
      z = z1;
      head = h;
    }
    if (head != 0 && z.strstart - head <= __Z_MAX_DIST) {
      let (z2, len) = __zlongest(z, head);
      z = __Z { ...z2, match_length: len };
    }
    let flush = false;
    if (z.match_length >= 3) {
      let (z3, f) = __ztally(z, z.strstart - z.match_start, z.match_length - 3);
      z = __Z { ...z3, lookahead: z3.lookahead - z3.match_length };
      flush = f;
      if (z.match_length <= z.cfg.lazy && z.lookahead >= 3) {
        let left = z.match_length - 1;
        while (left > 0) {
          z = __Z { ...z, strstart: z.strstart + 1 };
          let (z4, _) = __zinsert(z, z.strstart);
          z = z4;
          left -= 1;
        }
        z = __Z { ...z, strstart: z.strstart + 1, match_length: 0 };
      } else {
        z = __Z { ...z, strstart: z.strstart + z.match_length, match_length: 0 };
      }
    } else {
      let (z5, f) = __ztally(z, 0, __zbyte(z, z.strstart));
      z = __Z { ...z5, lookahead: z5.lookahead - 1, strstart: z5.strstart + 1 };
      flush = f;
    }
    if (flush) {
      z = __zflush(z, false);
    }
  }
  return z;
}

// `deflate_slow` of zlib: a match waits one position for a longer one.
// fern-lint: allow cyclomatic-complexity
function __zslow(z_in: __Z): __Z {
  let z = z_in;
  while (true) {
    if (z.lookahead < __Z_MIN_LOOKAHEAD) {
      z = __zfill(z);
      if (z.lookahead == 0) {
        break;
      }
    }
    let head = 0;
    if (z.lookahead >= 3) {
      let (z1, h) = __zinsert(z, z.strstart);
      z = z1;
      head = h;
    }
    z = __Z { ...z, prev_length: z.match_length, prev_match: z.match_start, match_length: 2 };
    if (head != 0 && z.prev_length < z.cfg.lazy && z.strstart - head <= __Z_MAX_DIST) {
      let (z2, len) = __zlongest(z, head);
      z = __Z { ...z2, match_length: len };
      if (len == 3 && z.strstart - z.match_start > 4096) {
        z = __Z { ...z, match_length: 2 };
      }
    }
    if (z.prev_length >= 3 && z.match_length <= z.prev_length) {
      let max_insert = z.strstart + z.lookahead - 3;
      let (z3, flush) = __ztally(z, z.strstart - 1 - z.prev_match, z.prev_length - 3);
      z = __Z { ...z3, lookahead: z3.lookahead - (z3.prev_length - 1) };
      let left = z.prev_length - 2;
      while (left > 0) {
        z = __Z { ...z, strstart: z.strstart + 1 };
        if (z.strstart <= max_insert) {
          let (z4, _) = __zinsert(z, z.strstart);
          z = z4;
        }
        left -= 1;
      }
      z = __Z { ...z, match_available: false, match_length: 2, strstart: z.strstart + 1 };
      if (flush) {
        z = __zflush(z, false);
      }
    } else if (z.match_available) {
      let (z5, flush) = __ztally(z, 0, __zbyte(z, z.strstart - 1));
      z = z5;
      if (flush) {
        z = __zflush(z, false);
      }
      z = __Z { ...z, strstart: z.strstart + 1, lookahead: z.lookahead - 1 };
    } else {
      z = __Z { ...z, match_available: true, strstart: z.strstart + 1, lookahead: z.lookahead - 1 };
    }
  }
  if (z.match_available) {
    let (z6, _) = __ztally(z, 0, __zbyte(z, z.strstart - 1));
    z = __Z { ...z6, match_available: false };
  }
  return z;
}

// ---- The blocks (trees.c of zlib) ----

const __Z_EXTRA_LBITS: i32[] = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0];

const __Z_EXTRA_DBITS: i32[] = [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13];

const __Z_EXTRA_BLBITS: i32[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 3, 7];

const __Z_BL_ORDER: i32[] = [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];

// The first match length of each length code (`base_length`).
function __zbase_length(code: i32): i32 {
  if (code == 28) {
    return 0;
  }
  let base = 0;
  for c in 0..code {
    base += 1 << __Z_EXTRA_LBITS[c];
  }
  return base;
}

// The length code of a match length less 3 (`_length_code`).
function __zlength_code(lc: i32): i32 {
  if (lc == 255) {
    return 28;
  }
  let code = 0;
  let base = 0;
  while (base + (1 << __Z_EXTRA_LBITS[code]) <= lc) {
    base += 1 << __Z_EXTRA_LBITS[code];
    code += 1;
  }
  return code;
}

// The first distance less 1 of each distance code (`base_dist`).
function __zbase_dist(code: i32): i32 {
  let base = 0;
  for c in 0..code {
    base += 1 << __Z_EXTRA_DBITS[c];
  }
  return base;
}

// The distance code of a distance less 1 (`d_code`).
function __zdist_code(dist: i32): i32 {
  let code = 0;
  while (code < 29 && __zbase_dist(code + 1) <= dist) {
    code += 1;
  }
  return code;
}

// A Huffman tree of zlib: the frequency, the parent and the length of each
// node, the code of each leaf, and the largest code in use.
struct __ZTree { freq: i32[], dad: i32[], len: i32[], code: i32[], max_code: i32 }

// The heap of `build_tree`, with the depth of each node.
struct __ZHeap { heap: i32[], depth: i32[], n: i32, max: i32 }

function __zsmaller(t: __ZTree, h: __ZHeap, a: i32, b: i32): boolean {
  return t.freq[a] < t.freq[b] || t.freq[a] == t.freq[b] && h.depth[a] <= h.depth[b];
}

// `pqdownheap` of zlib.
function __zdown(t: __ZTree, h_in: __ZHeap, k_in: i32): __ZHeap {
  let h = h_in;
  let k = k_in;
  let v = h.heap[k];
  let j = k << 1;
  while (j <= h.n) {
    if (j < h.n && __zsmaller(t, h, h.heap[j + 1], h.heap[j])) {
      j += 1;
    }
    if (__zsmaller(t, h, v, h.heap[j])) {
      break;
    }
    h = __ZHeap { ...h, heap: h.heap.with(k, h.heap[j]) };
    k = j;
    j <<= 1;
  }
  return __ZHeap { ...h, heap: h.heap.with(k, v) };
}

// The costs that a tree adds to a block: the bits of the block with this
// tree and with the static tree.
struct __ZCost { opt: i64, stat: i64 }

// The tree of the frequencies `freq` of `elems` codes, its lengths at most
// `max_length`, and the costs: `build_tree`, `gen_bitlen` and `gen_codes`
// of zlib. `stat` holds the lengths of the static tree, or none.
function __zbuild(freq_in: i32[], elems: i32, stat: i32[], extra: i32[], base: i32, max_length: i32): (__ZTree, __ZCost) {
  let size = 2 * elems + 1;
  let t = __ZTree { freq: __zgrow(freq_in, size), dad: __alloc_i32(size), len: __alloc_i32(size), code: __alloc_i32(size), max_code: -1 };
  let h = __ZHeap { heap: __alloc_i32(2 * 286 + 1), depth: __alloc_i32(size), n: 0, max: 2 * 286 + 1 };
  let cost = __ZCost { opt: 0, stat: 0 };
  for n in 0..elems {
    if (t.freq[n] != 0) {
      h = __ZHeap { ...h, n: h.n + 1 };
      h = __ZHeap { ...h, heap: h.heap.with(h.n, n) };
      t = __ZTree { ...t, max_code: n };
    }
  }
  while (h.n < 2) {
    let node = if (t.max_code < 2) { t.max_code + 1 } else { 0 };
    if (t.max_code < 2) {
      t = __ZTree { ...t, max_code: t.max_code + 1 };
    }
    h = __ZHeap { ...h, n: h.n + 1 };
    h = __ZHeap { ...h, heap: h.heap.with(h.n, node) };
    t = __ZTree { ...t, freq: t.freq.with(node, 1) };
    cost = __ZCost { opt: cost.opt - 1, stat: cost.stat - (if (stat.len() > 0) { stat[node] as i64 } else { 0 }) };
  }
  let k = h.n / 2;
  while (k >= 1) {
    h = __zdown(t, h, k);
    k -= 1;
  }
  let (t2, h2) = __zcombine(t, h, elems);
  return __zbitlen(t2, h2, cost, stat, extra, base, max_length);
}

function __zgrow(freq: i32[], size: i32): i32[] {
  let out = __alloc_i32(size);
  for i in 0..freq.len() {
    out = out.with(i, freq[i]);
  }
  return out;
}

// The tree made from the heap: the two least frequent nodes become the
// children of a new node, until one node is left.
function __zcombine(t_in: __ZTree, h_in: __ZHeap, elems: i32): (__ZTree, __ZHeap) {
  let t = t_in;
  let h = h_in;
  let node = elems;
  while (true) {
    let n = h.heap[1];
    h = __ZHeap { ...h, heap: h.heap.with(1, h.heap[h.n]), n: h.n - 1 };
    h = __zdown(t, h, 1);
    let m = h.heap[1];
    h = __ZHeap { ...h, max: h.max - 2 };
    h = __ZHeap { ...h, heap: h.heap.with(h.max + 1, n).with(h.max, m) };
    t = __ZTree { ...t, freq: t.freq.with(node, t.freq[n] + t.freq[m]), dad: t.dad.with(n, node).with(m, node) };
    h = __ZHeap { ...h, depth: h.depth.with(node, cmp.max(h.depth[n], h.depth[m]) + 1), heap: h.heap.with(1, node) };
    node += 1;
    h = __zdown(t, h, 1);
    if (h.n < 2) {
      break;
    }
  }
  h = __ZHeap { ...h, max: h.max - 1 };
  return (t, __ZHeap { ...h, heap: h.heap.with(h.max, h.heap[1]) });
}

// The lengths of the codes of the tree, at most `max_length`, the costs,
// and the codes: `gen_bitlen` and `gen_codes` of zlib.
// fern-lint: allow cyclomatic-complexity
function __zbitlen(t_in: __ZTree, h: __ZHeap, cost_in: __ZCost, stat: i32[], extra: i32[], base: i32, max_length: i32): (__ZTree, __ZCost) {
  let t = t_in;
  let cost = cost_in;
  let counts = __alloc_i32(16);
  let overflow = 0;
  t = __ZTree { ...t, len: t.len.with(h.heap[h.max], 0) };
  let top = 2 * 286 + 1;
  for k in h.max + 1..top {
    let n = h.heap[k];
    let bits = t.len[t.dad[n]] + 1;
    if (bits > max_length) {
      bits = max_length;
      overflow += 1;
    }
    t = __ZTree { ...t, len: t.len.with(n, bits) };
    if (n <= t.max_code) {
      counts = counts.with(bits, counts[bits] + 1);
      let xbits = if (n >= base) { extra[n - base] } else { 0 };
      let f = t.freq[n] as i64;
      cost = __ZCost { opt: cost.opt + f * ((bits + xbits) as i64), stat: cost.stat + (if (stat.len() > 0) { f * ((stat[n] + xbits) as i64) } else { 0 }) };
    }
  }
  if (overflow > 0) {
    while (overflow > 0) {
      let bits = max_length - 1;
      while (counts[bits] == 0) {
        bits -= 1;
      }
      counts = counts.with(bits, counts[bits] - 1).with(bits + 1, counts[bits + 1] + 2);
      counts = counts.with(max_length, counts[max_length] - 1);
      overflow -= 2;
    }
    let k = top;
    let bits = max_length;
    while (bits != 0) {
      let left = counts[bits];
      while (left != 0) {
        k -= 1;
        let m = h.heap[k];
        if (m <= t.max_code) {
          if (t.len[m] != bits) {
            cost = __ZCost { ...cost, opt: cost.opt + (bits - t.len[m]) as i64 * (t.freq[m] as i64) };
            t = __ZTree { ...t, len: t.len.with(m, bits) };
          }
          left -= 1;
        }
      }
      bits -= 1;
    }
  }
  return (__ZTree { ...t, code: __zcodes(t.len, t.max_code, counts) }, cost);
}

// The codes of the lengths `len`, with the bits reversed: `gen_codes`.
function __zcodes(len: i32[], max_code: i32, counts: i32[]): i32[] {
  let next = __alloc_i32(16);
  let code = 0;
  for bits in 1..16 {
    code = code + counts[bits - 1] << 1;
    next = next.with(bits, code);
  }
  let out = __alloc_i32(len.len());
  for n in 0..max_code + 1 {
    let l = len[n];
    if (l != 0) {
      out = out.with(n, __zreverse(next[l], l));
      next = next.with(l, next[l] + 1);
    }
  }
  return out;
}

function __zreverse(code_in: i32, len: i32): i32 {
  let code = code_in;
  let res = 0;
  for k in 0..len {
    res = res << 1 | code & 1;
    code >>= 1;
  }
  return res;
}

// The lengths of the static trees.
function __zstatic_llen(): i32[] {
  let out = __alloc_i32(288);
  for n in 0..288 {
    out = out.with(n, if (n < 144) { 8 } else if (n < 256) { 9 } else if (n < 280) { 7 } else { 8 });
  }
  return out;
}

function __zstatic_codes(len: i32[]): i32[] {
  let counts = __alloc_i32(16);
  for l in len {
    counts = counts.with(l, counts[l] + 1);
  }
  return __zcodes(len, len.len() - 1, counts);
}

// The frequencies of the symbols of a block, with the end of the block.
function __zfreqs(z: __Z): (i32[], i32[]) {
  let lf = __alloc_i32(286);
  let df = __alloc_i32(30);
  for i in 0..z.dists.len() {
    if (z.dists[i] == 0) {
      lf = lf.with(z.lcs[i], lf[z.lcs[i]] + 1);
    } else {
      let lcode = __zlength_code(z.lcs[i]) + 257;
      lf = lf.with(lcode, lf[lcode] + 1);
      let dcode = __zdist_code(z.dists[i] - 1);
      df = df.with(dcode, df[dcode] + 1);
    }
  }
  return (lf.with(256, 1), df);
}

// The frequencies of the codes of the lengths of a tree, as the run
// lengths of `send_tree` give them: `scan_tree` of zlib.
function __zscan(own bl: i32[], len: i32[], max_code: i32): i32[] {
  let runs = __zruns(len, max_code);
  for r in runs {
    let code = r >> 16;
    bl = bl.with(code, bl[code] + __zrun_count(r));
  }
  return bl;
}

// The codes of the lengths `len` up to `max_code`, in runs: each item is a
// code of the tree of the lengths (0 to 18) and, in its low 16 bits, how
// many times it goes, or the count of a repeat less its base (in the bits
// 8 and up of the low part) for the codes 16 to 18 (`send_tree` of zlib).
function __zruns(len: i32[], max_code: i32): i32[] {
  let out: i32[] = [];
  let prevlen = -1;
  let nextlen = len[0];
  let count = 0;
  let (max_count, min_count) = __zrun_limits(-1, nextlen);
  for n in 0..max_code + 1 {
    let curlen = nextlen;
    nextlen = if (n + 1 <= max_code) { len[n + 1] } else { 65535 };
    count += 1;
    if (count < max_count && curlen == nextlen) {
      continue;
    }
    out = __zrun_items(out, curlen, prevlen, count, min_count);
    count = 0;
    prevlen = curlen;
    let (mx, mn) = __zrun_limits(curlen, nextlen);
    max_count = mx;
    min_count = mn;
  }
  return out;
}

// The longest run and the shortest repeat after the length `curlen` when
// the next length is `nextlen`.
function __zrun_limits(curlen: i32, nextlen: i32): (i32, i32) {
  if (nextlen == 0) {
    return (138, 3);
  }
  if (curlen == nextlen) {
    return (6, 3);
  }
  return (7, 4);
}

// The items of one run of `count` lengths `curlen` after the length
// `prevlen`.
function __zrun_items(own out: i32[], curlen: i32, prevlen: i32, count_in: i32, min_count: i32): i32[] {
  let count = count_in;
  if (count < min_count) {
    return out.append(curlen << 16 | count);
  }
  if (curlen != 0) {
    if (curlen != prevlen) {
      out = out.append(curlen << 16 | 1);
      count -= 1;
    }
    return out.append(16 << 16 | 1 | count - 3 << 8);
  }
  if (count <= 10) {
    return out.append(17 << 16 | 1 | count - 3 << 8);
  }
  return out.append(18 << 16 | 1 | count - 11 << 8);
}

// The count of the code of a run: its low byte.
function __zrun_count(r: i32): i32 {
  return r & 255;
}

// Writes the runs of the lengths of a tree with the codes `bt`.
function __zsend_runs(w_in: __ZBits, runs: i32[], bt: __ZTree): __ZBits {
  let w = w_in;
  for r in runs {
    let code = r >> 16;
    let extra = r >> 8 & 255;
    let times = __zrun_count(r);
    for k in 0..times {
      w = __zput(w, bt.code[code], bt.len[code]);
    }
    if (code == 16) {
      w = __zput(w, extra, 2);
    } else if (code == 17) {
      w = __zput(w, extra, 3);
    } else if (code == 18) {
      w = __zput(w, extra, 7);
    }
  }
  return w;
}

// Writes the symbols of the block with the codes of the trees `lt` and
// `dt`, and the end of the block: `compress_block` of zlib.
function __zcompress(w_in: __ZBits, z: __Z, lcode: i32[], llen: i32[], dcode: i32[], dlen: i32[]): __ZBits {
  let w = w_in;
  for i in 0..z.dists.len() {
    let lc = z.lcs[i];
    if (z.dists[i] == 0) {
      w = __zput(w, lcode[lc], llen[lc]);
      continue;
    }
    let code = __zlength_code(lc);
    w = __zput(w, lcode[code + 257], llen[code + 257]);
    if (__Z_EXTRA_LBITS[code] != 0) {
      w = __zput(w, lc - __zbase_length(code), __Z_EXTRA_LBITS[code]);
    }
    let dist = z.dists[i] - 1;
    let dc = __zdist_code(dist);
    w = __zput(w, dcode[dc], dlen[dc]);
    if (__Z_EXTRA_DBITS[dc] != 0) {
      w = __zput(w, dist - __zbase_dist(dc), __Z_EXTRA_DBITS[dc]);
    }
  }
  return __zput(w, lcode[256], llen[256]);
}

// Writes the block of the symbols since `block_start`, as a stored block, a
// block with the static trees or a block with its own trees, whichever is
// the smallest: `_tr_flush_block` of zlib.
function __zflush(z: __Z, last: boolean): __Z {
  let (lf, df) = __zfreqs(z);
  let slen = __zstatic_llen();
  let dstat = __alloc_i32(30).map((v: i32): i32 => 5);
  let (lt, lcost) = __zbuild(lf, 286, slen, __Z_EXTRA_LBITS, 257, 15);
  let (dt, dcost) = __zbuild(df, 30, dstat, __Z_EXTRA_DBITS, 0, 15);
  let bl = __zscan(__zscan(__alloc_i32(19), lt.len, lt.max_code), dt.len, dt.max_code);
  let none_stat: i32[] = [];
  let (bt, bcost) = __zbuild(bl, 19, none_stat, __Z_EXTRA_BLBITS, 0, 7);
  let max_blindex = 18;
  while (max_blindex >= 3 && bt.len[__Z_BL_ORDER[max_blindex]] == 0) {
    max_blindex -= 1;
  }
  let opt = lcost.opt + dcost.opt + bcost.opt + 3 * (max_blindex as i64 + 1) + 14;
  let stat = lcost.stat + dcost.stat;
  let opt_lenb = opt + 3 + 7 >> 3;
  let static_lenb = stat + 3 + 7 >> 3;
  if (static_lenb <= opt_lenb) {
    opt_lenb = static_lenb;
  }
  let w = z.w;
  let lastbit = if (last) { 1 } else { 0 };
  if ((z.strstart - z.block_start) as i64 + 4 <= opt_lenb && z.block_start >= 0) {
    w = __zstored(w, z, lastbit);
  } else if (static_lenb == opt_lenb) {
    w = __zstatic_block(w, z, lastbit, slen, dstat);
  } else {
    w = __zput(__zput(__zput(__zput(w, 4 + lastbit, 3), lt.max_code + 1 - 257, 5), dt.max_code, 5), max_blindex + 1 - 4, 4);
    for rank in 0..max_blindex + 1 {
      w = __zput(w, bt.len[__Z_BL_ORDER[rank]], 3);
    }
    w = __zsend_runs(w, __zruns(lt.len, lt.max_code), bt);
    w = __zsend_runs(w, __zruns(dt.len, dt.max_code), bt);
    w = __zcompress(w, z, lt.code, lt.len, dt.code, dt.len);
  }
  let none: i32[] = [];
  return __Z { ...z, w: w, block_start: z.strstart, dists: none, lcs: none };
}

// A stored block of the bytes since `block_start`: `_tr_stored_block`.
function __zstored(w_in: __ZBits, z: __Z, lastbit: i32): __ZBits {
  let stored_len = z.strstart - z.block_start;
  let w = __zalign(__zput(w_in, lastbit, 3));
  buf_push_byte(w.out, stored_len & 255);
  buf_push_byte(w.out, stored_len >> 8 & 255);
  buf_push_byte(w.out, ~stored_len & 255);
  buf_push_byte(w.out, ~stored_len >> 8 & 255);
  for i in z.offset + z.block_start..z.offset + z.strstart {
    buf_push_byte(w.out, z.input[i] as i32);
  }
  return w;
}

// A block with the static trees.
function __zstatic_block(w_in: __ZBits, z: __Z, lastbit: i32, slen: i32[], dstat: i32[]): __ZBits {
  let w = __zput(w_in, 2 + lastbit, 3);
  let dc = __alloc_i32(30);
  for k in 0..30 {
    dc = dc.with(k, __zreverse(k, 5));
  }
  return __zcompress(w, z, __zstatic_codes(slen), slen, dc, dstat);
}
```
