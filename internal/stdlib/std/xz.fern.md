# xz and lzma files

This module decompresses the files of `xz`, and the `.lzma` files of LZMA Utils.

## Model

An xz file is one or more streams. A stream has a header, blocks, an index and a footer. Each block holds LZMA2 data, which is a list of chunks: an LZMA chunk, or a chunk of stored bytes. A `.lzma` file is a header of 13 bytes and one LZMA stream.

The LZMA decoder reads bits with a range decoder and adaptive probabilities. Each step gives a literal byte, or a copy of earlier bytes at one of four recent distances or at a new distance.

## Contracts

`decode_xz` and `decode_lzma` give the bytes before the first error, and the error, with the texts of the `xz` tool. A block can hold only the LZMA2 filter. The decoder verifies a check of CRC32, CRC64 or SHA-256, and it gives the bytes of a block whose check does not agree with the error. For data that ends early, the count of bytes before the error can differ from that of `xz`. `decompress_xz` and `decompress_lzma` give the bytes, or the error.

## Executable example

```fern test name=std-xz-example
import "std/xz";

function main(): i32 {
  let Err(e) = xz.decompress_xz([253u8, 55u8, 122u8]) else { return 1; };
  if (e.message() != "Unexpected end of input") { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module decompresses the files of `xz`, and the `.lzma` files of LZMA
// Utils.
import "core/cmp";
import "core/error";
import "std/crypto";
import "std/hash/crc32";

<<errors>>

<<range decoder>>

<<lzma>>

<<containers>>
```

### Errors

```fern
<<errors>>=
// The errors of xz and lzma data: an unknown format, a filter that the
// decoder does not have, data that is not valid, or data that ends early.
pub enum XzError { NotXz, Unsupported, Corrupt, Truncated }

pub function (e: XzError) message(): string {
  return match (e) {
    NotXz => "File format not recognized",
    Unsupported => "Unsupported options",
    Corrupt => "Compressed data is corrupt",
    Truncated => "Unexpected end of input",
  };
}

impl error.Error for XzError {}

impl cmp.Display for XzError {}

pub function (e: XzError) to_string(): string {
  return e.message();
}

// The bytes before an error, and the error.
pub struct Decoded { out: u8[], error: Option[XzError] }

function __xz_result(d: Decoded): Result[u8[], XzError] {
  return match (d.error) {
    Some(e) => Err(e),
    None => Ok(d.out),
  };
}
```

### Range decoder

The range decoder holds 32-bit values in `i64`. A probability is 11 bits, and it moves by a 32nd of the distance to its end after each bit. `pos` is -1 after the input ends.

```fern
<<range decoder>>=
struct __Rc { range: i64, code: i64, pos: i32, end: i32 }

function __rc_new(input: [u8], at: i32, end: i32): __Rc {
  if (at + 5 > end || input[at] != 0u8) {
    return __Rc { range: 0i64, code: 0i64, pos: -1, end: end };
  }
  let code = 0i64;
  for k in 1..5 {
    code = code << 8i64 | input[at + k] as i64;
  }
  return __Rc { range: 4294967295i64, code: code, pos: at + 5, end: end };
}

function __rc_norm(input: [u8], rc: __Rc): __Rc {
  if (rc.range >= 16777216i64) {
    return rc;
  }
  if (rc.pos < 0 || rc.pos >= rc.end) {
    return __Rc { ...rc, pos: -1 };
  }
  return __Rc { ...rc, range: rc.range << 8i64, code: (rc.code << 8i64 | input[rc.pos] as i64) & 4294967295i64, pos: rc.pos + 1 };
}

// One bit with the probability at `i` of `probs`.
function __rc_bit(input: [u8], rc: __Rc, own probs: i32[], i: i32): (__Rc, i32[], i32) {
  let p = probs[i];
  let bound = (rc.range >> 11i64) * (p as i64);
  if (rc.code < bound) {
    return (__rc_norm(input, __Rc { ...rc, range: bound }), probs.with(i, p + (2048 - p >> 5)), 0);
  }
  return (__rc_norm(input, __Rc { ...rc, range: rc.range - bound, code: rc.code - bound }), probs.with(i, p - (p >> 5)), 1);
}

// `n` bits with no probability, high bit first.
function __rc_direct(input: [u8], rc0: __Rc, n: i32): (__Rc, i32) {
  let rc = rc0;
  let v = 0;
  for _ in 0..n {
    let range = rc.range >> 1i64;
    let bit = if (rc.code >= range) { 1 } else { 0 };
    rc = __rc_norm(input, __Rc { ...rc, range: range, code: rc.code - (if (bit == 1) { range } else { 0i64 }) });
    v = v << 1 | bit;
  }
  return (rc, v);
}

// A tree of `n` bits from the probabilities at `base`, high bit first.
function __rc_tree(input: [u8], rc0: __Rc, own probs: i32[], base: i32, n: i32): (__Rc, i32[], i32) {
  let rc = rc0;
  let m = 1;
  for _ in 0..n {
    let (r, p, bit) = __rc_bit(input, rc, probs, base + m);
    rc = r;
    probs = p;
    m = m << 1 | bit;
  }
  return (rc, probs, m - (1 << n));
}

// A tree of `n` bits from the probabilities at `base`, low bit first.
function __rc_reverse(input: [u8], rc0: __Rc, own probs: i32[], base: i32, n: i32): (__Rc, i32[], i32) {
  let rc = rc0;
  let m = 1;
  let v = 0;
  for k in 0..n {
    let (r, p, bit) = __rc_bit(input, rc, probs, base + m);
    rc = r;
    probs = p;
    m = m << 1 | bit;
    v |= bit << k;
  }
  return (rc, probs, v);
}
```

### LZMA

All the probabilities are in one array, at the offsets of the constants below. The state of the decoder is the kind of the last steps (0 to 11), the four last distances, and the properties `lc`, `lp` and `pb`.

```fern
<<lzma>>=
const __XZ_MATCH: i32 = 0;

const __XZ_REP: i32 = 192;

const __XZ_REP_G0: i32 = 204;

const __XZ_REP_G1: i32 = 216;

const __XZ_REP_G2: i32 = 228;

const __XZ_REP0_LONG: i32 = 240;

const __XZ_SLOT: i32 = 432;

const __XZ_SPECIAL: i32 = 688;

const __XZ_ALIGN: i32 = 802;

const __XZ_LEN: i32 = 818;

const __XZ_REP_LEN: i32 = 1332;

const __XZ_LITERAL: i32 = 1846;

// The state of an LZMA decoder.
struct __Lz { probs: i32[], state: i32, reps: i32[], lc: i32, lp: i32, pb: i32, start: i32 }

function __lz_new(props: i32, start: i32): __Lz {
  let lc = props % 9;
  let lp = props / 9 % 5;
  let pb = props / 45;
  return __Lz { probs: __lz_probs(lc + lp), state: 0, reps: [0, 0, 0, 0], lc: lc, lp: lp, pb: pb, start: start };
}

function __lz_probs(bits: i32): i32[] {
  let n = __XZ_LITERAL + 768 * (1 << bits);
  let probs = __alloc_i32(n);
  for k in 0..n {
    probs = probs.with(k, 1024);
  }
  return probs;
}

// The state with new probabilities and the steps reset.
function __lz_reset(lz: __Lz): __Lz {
  return __Lz { ...lz, probs: __lz_probs(lz.lc + lz.lp), state: 0, reps: [0, 0, 0, 0] };
}

// The result of a run of the decoder: the output, the decoder, the range
// decoder, and 1 at an end mark, 0 at the end of the count, or -1 after an
// error with `error`.
struct __LzRun { out: u8[], lz: __Lz, rc: __Rc, end: i32, error: XzError }

// Decodes until the output holds `limit` bytes, or until an end mark when
// `limit` is -1.
function __lz_run(input: [u8], lz0: __Lz, rc0: __Rc, own out: u8[], limit: i32): __LzRun {
  let lz = lz0;
  let probs = lz0.probs;
  let rc = rc0;
  while (limit < 0 || out.len() < limit) {
    if (rc.pos < 0) {
      return __LzRun { out: out, lz: __Lz { ...lz, probs: probs }, rc: rc, end: -1, error: Truncated };
    }
    let pos_state = out.len() - lz.start & (1 << lz.pb) - 1;
    let (r, p, bit) = __rc_bit(input, rc, probs, __XZ_MATCH + (lz.state << 4) + pos_state);
    rc = r;
    probs = p;
    if (bit == 0) {
      let (r2, p2, state, b) = __lz_literal(input, rc, probs, lz, out);
      rc = r2;
      probs = p2;
      lz = __Lz { ...lz, state: state };
      out = out.append(b as u8);
      continue;
    }
    let step = __lz_match(input, rc, probs, lz, pos_state);
    rc = step.rc;
    probs = step.probs;
    lz = __Lz { ...lz, state: step.state, reps: step.reps };
    if (step.dist < 0 || step.dist >= out.len() - lz.start) {
      return __LzRun { out: out, lz: __Lz { ...lz, probs: probs }, rc: rc, end: if (step.dist == -2) { 1 } else { -1 }, error: Corrupt };
    }
    let n = if (limit >= 0 && out.len() + step.len > limit) { limit - out.len() } else { step.len };
    out = __lz_copy(out, step.dist, n);
  }
  return __LzRun { out: out, lz: __Lz { ...lz, probs: probs }, rc: rc, end: 0, error: Corrupt };
}

// The output with `n` bytes copied from `dist` plus one bytes back.
function __lz_copy(own out: u8[], dist: i32, n: i32): u8[] {
  let from = out.len() - dist - 1;
  for k in 0..n {
    out = out.append(out[from + k]);
  }
  return out;
}

// A literal byte. After a match, the byte at the last distance guides the
// first bits.
function __lz_literal(input: [u8], rc0: __Rc, own probs: i32[], lz: __Lz, out: u8[]): (__Rc, i32[], i32, i32) {
  let n = out.len() - lz.start;
  let prev = if (n > 0) { out[out.len() - 1] as i32 } else { 0 };
  let base = __XZ_LITERAL + 768 * (((n & (1 << lz.lp) - 1) << lz.lc) + (prev >> 8 - lz.lc));
  let rc = rc0;
  let sym = 1;
  if (lz.state >= 7 && lz.reps[0] < n) {
    let m = out[out.len() - lz.reps[0] - 1] as i32;
    while (sym < 256) {
      let mbit = m >> 7 & 1;
      m = m << 1;
      let (r, p, bit) = __rc_bit(input, rc, probs, base + (1 + mbit << 8) + sym);
      rc = r;
      probs = p;
      sym = sym << 1 | bit;
      if (mbit != bit) {
        break;
      }
    }
  }
  while (sym < 256) {
    let (r, p, bit) = __rc_bit(input, rc, probs, base + sym);
    rc = r;
    probs = p;
    sym = sym << 1 | bit;
  }
  let s = lz.state;
  return (rc, probs, if (s < 4) { 0 } else if (s < 10) { s - 3 } else { s - 6 }, sym - 256);
}

// A copy: its distance less one, or -2 for an end mark, its length, and the
// state of the decoder after it.
struct __LzMatch { rc: __Rc, probs: i32[], state: i32, reps: i32[], dist: i32, len: i32 }

function __lz_match(input: [u8], rc0: __Rc, own probs: i32[], lz: __Lz, pos_state: i32): __LzMatch {
  let (rc, p, rep) = __rc_bit(input, rc0, probs, __XZ_REP + lz.state);
  if (rep == 1) {
    return __lz_rep(input, rc, p, lz, pos_state);
  }
  let (r2, p2, len) = __lz_length(input, rc, p, __XZ_LEN, pos_state);
  let (r3, p3, dist) = __lz_distance(input, r2, p2, len);
  let reps = [dist, lz.reps[0], lz.reps[1], lz.reps[2]];
  return __LzMatch { rc: r3, probs: p3, state: if (lz.state < 7) { 7 } else { 10 }, reps: reps, dist: if (dist == -1) { -2 } else { dist }, len: len + 2 };
}

// A copy at one of the four last distances, or one byte at the last one.
function __lz_rep(input: [u8], rc0: __Rc, own probs: i32[], lz: __Lz, pos_state: i32): __LzMatch {
  let s = lz.state;
  let (rc, p, g0) = __rc_bit(input, rc0, probs, __XZ_REP_G0 + s);
  if (g0 == 0) {
    let (r2, p2, long) = __rc_bit(input, rc, p, __XZ_REP0_LONG + (s << 4) + pos_state);
    if (long == 0) {
      return __LzMatch { rc: r2, probs: p2, state: if (s < 7) { 9 } else { 11 }, reps: lz.reps, dist: lz.reps[0], len: 1 };
    }
    let (r3, p3, len) = __lz_length(input, r2, p2, __XZ_REP_LEN, pos_state);
    return __LzMatch { rc: r3, probs: p3, state: if (s < 7) { 8 } else { 11 }, reps: lz.reps, dist: lz.reps[0], len: len + 2 };
  }
  let (r2, p2, g1) = __rc_bit(input, rc, p, __XZ_REP_G1 + s);
  let (r3, p3, g2) = if (g1 == 0) { (r2, p2, 0) } else { __rc_bit(input, r2, p2, __XZ_REP_G2 + s) };
  let k = if (g1 == 0) { 1 } else { 2 + g2 };
  let reps = lz.reps;
  let dist = reps[k];
  let moved = if (k == 1) { [dist, reps[0], reps[2], reps[3]] } else if (k == 2) { [dist, reps[0], reps[1], reps[3]] } else { [dist, reps[0], reps[1], reps[2]] };
  let (r4, p4, len) = __lz_length(input, r3, p3, __XZ_REP_LEN, pos_state);
  return __LzMatch { rc: r4, probs: p4, state: if (s < 7) { 8 } else { 11 }, reps: moved, dist: dist, len: len + 2 };
}

// A length less two from the coder at `base`: 3 bits of the low or the
// middle tree of the position, or 8 bits of the high tree.
function __lz_length(input: [u8], rc0: __Rc, own probs: i32[], base: i32, pos_state: i32): (__Rc, i32[], i32) {
  let (rc, p, choice) = __rc_bit(input, rc0, probs, base);
  if (choice == 0) {
    return __rc_tree(input, rc, p, base + 2 + pos_state * 8, 3);
  }
  let (r2, p2, choice2) = __rc_bit(input, rc, p, base + 1);
  if (choice2 == 0) {
    let (r3, p3, v) = __rc_tree(input, r2, p2, base + 130 + pos_state * 8, 3);
    return (r3, p3, 8 + v);
  }
  let (r3, p3, v) = __rc_tree(input, r2, p2, base + 258, 8);
  return (r3, p3, 16 + v);
}

// A new distance less one, or -1 for an end mark.
function __lz_distance(input: [u8], rc0: __Rc, own probs: i32[], len: i32): (__Rc, i32[], i32) {
  let state = if (len < 3) { len } else { 3 };
  let (rc, p, slot) = __rc_tree(input, rc0, probs, __XZ_SLOT + state * 64, 6);
  if (slot < 4) {
    return (rc, p, slot);
  }
  let direct = (slot >> 1) - 1;
  let dist = (2 | slot & 1) << direct;
  if (slot < 14) {
    let (r2, p2, v) = __rc_reverse(input, rc, p, __XZ_SPECIAL + dist - slot - 1, direct);
    return (r2, p2, dist + v);
  }
  let (r2, high) = __rc_direct(input, rc, direct - 4);
  let (r3, p3, low) = __rc_reverse(input, r2, p, __XZ_ALIGN, 4);
  let total = dist as i64 + (high as i64 << 4i64) + low as i64;
  return (r3, p3, if (total >= 4294967295i64) { -1 } else if (total > 2147483647i64) { -3 } else { total as i32 });
}
```

### Containers

```fern
<<containers>>=
// decompress_xz gives the bytes of an xz file, or the error.
pub function decompress_xz(input: [u8]): Result[u8[], XzError] {
  return __xz_result(decode_xz(input));
}

// decompress_lzma gives the bytes of a `.lzma` file, or the error.
pub function decompress_lzma(input: [u8]): Result[u8[], XzError] {
  return __xz_result(decode_lzma(input));
}

function __xz_fail(own out: u8[], e: XzError): Decoded {
  return Decoded { out: out, error: Some(e) };
}

// decode_lzma gives the bytes of a `.lzma` file before the first error, and
// the error. A size of all ones means that an end mark ends the data.
pub function decode_lzma(input: [u8]): Decoded {
  let out: u8[] = [];
  if (input.len() < 13) {
    return __xz_fail(out, if (input.len() == 0) { NotXz } else { Truncated });
  }
  if (input[0] >= 225u8 || input[12] != 0u8 && input[12] != 255u8) {
    return __xz_fail(out, NotXz);
  }
  let known = input[12] == 0u8;
  let size = 0i64;
  for k in 0..8 {
    size |= input[5 + k] as i64 << 8i64 * (k as i64);
  }
  let rc = __rc_new(input, 13, input.len());
  if (rc.pos < 0) {
    return __xz_fail(out, Truncated);
  }
  let r = __lz_run(input, __lz_new(input[0] as i32, 0), rc, out, if (known) { size as i32 } else { -1 });
  if (r.end < 0) {
    return Decoded { out: r.out, error: Some(r.error) };
  }
  return Decoded { out: r.out, error: None };
}

// Reads a number of xz at `at`: seven bits in each byte, low bits first.
// It gives the number and the offset after it, or -1.
function __xz_varint(input: [u8], at: i32): (i64, i32) {
  let v = 0i64;
  let shift = 0i64;
  let pos = at;
  while (pos < input.len() && shift < 63i64) {
    let b = input[pos] as i64;
    v |= (b & 127i64) << shift;
    pos += 1;
    if (b < 128i64) {
      return (v, pos);
    }
    shift += 7i64;
  }
  return (0i64, -1);
}

// The size of the check of each kind of check.
function __xz_check_size(kind: i32): i32 {
  return if (kind == 0) { 0 } else { [4, 8, 16, 32, 64][(kind - 1) / 3] };
}

// decode_xz gives the bytes of the streams of an xz file before the first
// error, and the error.
pub function decode_xz(input: [u8]): Decoded {
  let out: u8[] = [];
  let at = 0;
  let first = true;
  while (at < input.len()) {
    if (!first && input[at] == 0u8) {
      at += 1;
      continue;
    }
    let r = __xz_stream(input, at, out, first);
    out = r.out;
    if (r.at < 0) {
      return __xz_fail(out, r.error);
    }
    at = r.at;
    first = false;
  }
  return Decoded { out: out, error: if (first) { Some(NotXz) } else { None } };
}

// The output after a stream or a block, the offset after it or -1, and the
// error for -1.
struct __XzRun { out: u8[], at: i32, error: XzError }

function __xz_stream(input: [u8], at: i32, own out: u8[], first: boolean): __XzRun {
  if let Some(e) = __xz_header(input, at, first) {
    return __XzRun { out: out, at: -1, error: e };
  }
  let check = input[at + 7] as i32;
  let pos = at + 12;
  while (pos < input.len() && input[pos] != 0u8) {
    let r = __xz_block(input, pos, out, check);
    out = r.out;
    if (r.at < 0) {
      return r;
    }
    pos = r.at;
  }
  if (pos >= input.len()) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  return __xz_index(input, pos, out);
}

// The error of the header of a stream at `at`, or None.
function __xz_header(input: [u8], at: i32, first: boolean): Option[XzError] {
  let magic = [253u8, 55u8, 122u8, 88u8, 90u8, 0u8];
  for k in 0..6 {
    if (at + k >= input.len()) {
      return Some(Truncated);
    }
    if (input[at + k] != magic[k]) {
      return Some(if (first) { NotXz } else { Corrupt });
    }
  }
  if (at + 12 > input.len()) {
    return Some(Truncated);
  }
  return if (input[at + 6] != 0u8 || input[at + 7] > 15u8) { Some(Unsupported) } else { None };
}

// The index from its indicator at `at`, and the footer after it.
function __xz_index(input: [u8], at: i32, own out: u8[]): __XzRun {
  let (count, p0) = __xz_varint(input, at + 1);
  let pos = p0;
  let k = 0i64;
  while (pos >= 0 && k < count * 2i64) {
    let (_, next) = __xz_varint(input, pos);
    pos = next;
    k += 1i64;
  }
  if (pos < 0) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  let sum = (pos - at + 3) / 4 * 4 + at;
  let end = sum + 4 + 12;
  if (end > input.len()) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  let valid = __xz_le(input, sum, 4) == crc32.of(input[at:sum]) as u64 && __xz_le(input, end - 12, 4) == crc32.of(input[end - 8:end - 2]) as u64;
  if (!valid || input[end - 2] != 89u8 || input[end - 1] != 90u8) {
    return __XzRun { out: out, at: -1, error: Corrupt };
  }
  return __XzRun { out: out, at: end, error: Corrupt };
}

// One block from its header at `at`.
function __xz_block(input: [u8], at: i32, own out: u8[], check: i32): __XzRun {
  if let Some(e) = __xz_block_header(input, at) {
    return __XzRun { out: out, at: -1, error: e };
  }
  let first = out.len();
  let r = __lzma2(input, at + (input[at] as i32 + 1) * 4, out);
  out = r.out;
  if (r.at < 0) {
    return r;
  }
  let sum = (r.at - at + 3) / 4 * 4 + at;
  let end = sum + __xz_check_size(check);
  if (end > input.len()) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  if (!__xz_checked(check, out, first, input, sum)) {
    return __XzRun { out: out, at: -1, error: Corrupt };
  }
  return __XzRun { out: out, at: end, error: Corrupt };
}

// The error of the header of a block at `at`, or None. The block must
// have one filter, LZMA2.
function __xz_block_header(input: [u8], at: i32): Option[XzError] {
  let head = (input[at] as i32 + 1) * 4;
  if (at + head > input.len()) {
    return Some(Truncated);
  }
  let flags = input[at + 1] as i32;
  let pos = at + 2;
  for bit in [64, 128] {
    if ((flags & bit) != 0 && pos >= 0) {
      let (_, next) = __xz_varint(input, pos);
      pos = next;
    }
  }
  return if ((flags & 3) != 0 || !__xz_lzma2(input, pos, at + head)) { Some(Unsupported) } else { None };
}

// Whether the check of the kind `kind` at `at` agrees with the bytes of
// `out` from `first`. A kind that the decoder does not know agrees.
function __xz_checked(kind: i32, out: u8[], first: i32, input: [u8], at: i32): boolean {
  let data = out[first:];
  if (kind == 1) {
    return __xz_le(input, at, 4) == crc32.of(data) as u64;
  }
  if (kind == 4) {
    return __xz_le(input, at, 8) == __xz_crc64(data);
  }
  if (kind != 10) {
    return true;
  }
  let digest = crypto.sha256(data);
  for k in 0..32 {
    if (digest[k] != input[at + k]) {
      return false;
    }
  }
  return true;
}

function __xz_le(input: [u8], at: i32, n: i32): u64 {
  let v = 0u64;
  for k in 0..n {
    v |= input[at + k] as u64 << (8 * k) as u64;
  }
  return v;
}

// The CRC64 of ECMA-182 that xz uses.
function __xz_crc64(data: [u8]): u64 {
  let table = __alloc_i64(256);
  for i in 0..256 {
    let c = i as u64;
    for _ in 0..8 {
      c = if ((c & 1u64) != 0u64) { c >> 1u64 ^ 14514072000185962306u64 } else { c >> 1u64 };
    }
    table = table.with(i, c as i64);
  }
  let crc = 18446744073709551615u64;
  for b in data {
    crc = table[((crc ^ b as u64) & 255u64) as i32] as u64 ^ crc >> 8u64;
  }
  return crc ^ 18446744073709551615u64;
}

// Whether the filter flags at `pos`, before `end`, name LZMA2 with a size
// of dictionary that is valid.
function __xz_lzma2(input: [u8], pos: i32, end: i32): boolean {
  return pos >= 0 && pos + 3 <= end && input[pos] == 33u8 && input[pos + 1] == 1u8 && input[pos + 2] <= 40u8;
}

// The chunks of LZMA2 data from `at`, up to the chunk 0.
function __lzma2(input: [u8], at: i32, own out: u8[]): __XzRun {
  let pos = at;
  let lz = __lz_new(0, out.len());
  let have_props = false;
  while (pos < input.len() && input[pos] != 0u8) {
    let c = input[pos] as i32;
    if (c == 1 || c == 2) {
      lz = if (c == 1) { __Lz { ...lz, start: out.len() } } else { lz };
      let r = __lzma2_stored(input, pos, out);
      out = r.out;
      if (r.at < 0) {
        return r;
      }
      pos = r.at;
      continue;
    }
    let r = __lzma2_chunk(input, pos, out, lz, have_props);
    out = r.run.out;
    if (r.run.at < 0) {
      return r.run;
    }
    lz = r.lz;
    have_props = true;
    pos = r.run.at;
  }
  if (pos >= input.len()) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  return __XzRun { out: out, at: pos + 1, error: Corrupt };
}

// A chunk of stored bytes from its control byte at `pos`.
function __lzma2_stored(input: [u8], pos: i32, own out: u8[]): __XzRun {
  if (pos + 3 > input.len()) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  let n = (input[pos + 1] as i32 << 8 | input[pos + 2] as i32) + 1;
  if (pos + 3 + n > input.len()) {
    return __XzRun { out: out, at: -1, error: Truncated };
  }
  for k in 0..n {
    out = out.append(input[pos + 3 + k]);
  }
  return __XzRun { out: out, at: pos + 3 + n, error: Corrupt };
}

struct __Lzma2Chunk { run: __XzRun, lz: __Lz }

// An LZMA chunk from its control byte at `pos`.
function __lzma2_chunk(input: [u8], pos: i32, own out: u8[], lz0: __Lz, have_props: boolean): __Lzma2Chunk {
  let c = input[pos] as i32;
  let reset = c >> 5 & 3;
  let head = if (reset >= 2) { 6 } else { 5 };
  if (c < 128 || pos + head > input.len()) {
    return __Lzma2Chunk { run: __XzRun { out: out, at: -1, error: if (c < 128) { Corrupt } else { Truncated } }, lz: lz0 };
  }
  let unpacked = ((c & 31) << 16 | input[pos + 1] as i32 << 8 | input[pos + 2] as i32) + 1;
  let packed = (input[pos + 3] as i32 << 8 | input[pos + 4] as i32) + 1;
  let lz = __lzma2_state(lz0, reset, if (reset >= 2) { input[pos + 5] as i32 } else { -1 }, out.len());
  if (lz.lc < 0 || reset < 2 && !have_props) {
    return __Lzma2Chunk { run: __XzRun { out: out, at: -1, error: Corrupt }, lz: lz0 };
  }
  return __lzma2_data(input, pos + head, packed, out, lz, unpacked);
}

// The LZMA data of a chunk at `start`: `packed` bytes that give `unpacked`
// bytes.
function __lzma2_data(input: [u8], start: i32, packed: i32, own out: u8[], lz: __Lz, unpacked: i32): __Lzma2Chunk {
  let rc = __rc_new(input, start, start + packed);
  if (start + packed > input.len() || rc.pos < 0) {
    return __Lzma2Chunk { run: __XzRun { out: out, at: -1, error: if (start + packed > input.len()) { Truncated } else { Corrupt } }, lz: lz };
  }
  let limit = out.len() + unpacked;
  let r = __lz_run(input, lz, rc, out, limit);
  if (r.end != 0) {
    return __Lzma2Chunk { run: __XzRun { out: r.out, at: -1, error: if (r.end < 0) { r.error } else { Corrupt } }, lz: r.lz };
  }
  return __Lzma2Chunk { run: __XzRun { out: r.out, at: start + packed, error: Corrupt }, lz: r.lz };
}

// The decoder after the reset of a chunk: 1 resets the state, 2 also takes
// the properties `props`, and 3 also resets the dictionary at `here`. A
// decoder with `lc` -1 marks properties that are not valid.
function __lzma2_state(lz: __Lz, reset: i32, props: i32, here: i32): __Lz {
  if (reset < 2) {
    return if (reset == 1) { __lz_reset(lz) } else { lz };
  }
  if (props >= 225 || props % 9 + props / 9 % 5 > 4) {
    return __Lz { ...lz, lc: -1 };
  }
  return __lz_new(props, if (reset == 3) { here } else { lz.start });
}
```
