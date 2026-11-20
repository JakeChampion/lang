# Argon2id password-based key derivation

This module implements Argon2id version 19 from RFC 9106. The default parameters follow its second recommendation for memory-constrained systems: 64 MiB, three passes, four lanes and a 32-byte tag. Use a fresh random 16-byte salt for each password.

`key` accepts a password, a salt and parameters. `derive` also accepts the optional secret and associated data. Store the public parameters and salt with the tag. Lanes run sequentially and produce the same tag as parallel execution.

`memory_kib` is the requested matrix size. `limit_kib` bounds the matrix and four scratch blocks before allocation. It also bounds the output length. The caller supplies input storage, and BLAKE2b uses additional fixed-size buffers.

```fern
<<*>>=
<<parameters>>

<<initial and variable-length hashes>>

<<block compression>>

<<matrix traversal>>

<<key derivation>>
```
### Parameters

```fern
<<parameters>>=
import "core/error";
import "std/binary";
import "std/result";
import "std/crypto";
import "std/u64";

pub struct Params { memory_kib: i32, passes: i32, lanes: i32, tag_len: i32, limit_kib: i32 }

pub function defaults(): Params {
  return Params { memory_kib: 65536, passes: 3, lanes: 4, tag_len: 32, limit_kib: 65540 };
}

pub enum ArgonError { Parameters, SaltLength, MemoryLimit }

impl error.Error for ArgonError {}

pub function (e: ArgonError) message(): string {
  return match (e) {
    Parameters => "argon2id: invalid memory, passes, lanes or tag length",
    SaltLength => "argon2id: the salt needs at least 8 bytes",
    MemoryLimit => "argon2id: the requested allocation exceeds the memory limit",
  };
}
```

### Initial and variable-length hashes

```fern
<<initial and variable-length hashes>>=
// Prefix each input to H0 with its byte length.
function field(h: crypto.Blake2b, data: [u8]): crypto.Blake2b {
  return h.update(binary.append_u32_le([], data.len() as u32)).update(data);
}

function initial(p: Params, password: [u8], salt: [u8], secret: [u8], data: [u8]): u8[] {
  let header: u8[] = [];
  for word in [p.lanes, p.tag_len, p.memory_kib, p.passes, 19, 2] {
    header = binary.append_u32_le(header, word as u32);
  }
  let h = crypto.blake2b_new(64).update(header);
  h = field(field(field(field(h, password), salt), secret), data);
  let digest = h.finish();
  crypto.erase(h.buf);
  return digest;
}

// Variable-length hash H' from RFC 9106 section 3.3.
function expand(data: [u8], length: i32): u8[] {
  let input = binary.append_u32_le([], length as u32).concat(data);
  if (length <= 64) {
    let out = crypto.blake2b(input, length);
    crypto.erase(input);
    return out;
  }
  let v = crypto.blake2b(input, 64);
  crypto.erase(input);
  let out = __alloc_u8(length);
  let at = 0;
  while (length - at > 64) {
    out = __bytes_into(out, at, v, 0, 32);
    at += 32;
    if (length - at > 64) {
      let next = crypto.blake2b(v, 64);
      crypto.erase(v);
      v = next;
    }
  }
  let tail = crypto.blake2b(v, length - at);
  crypto.erase(v);
  out = __bytes_into(out, at, tail, 0, tail.len());
  crypto.erase(tail);
  return out;
}
```

### Block compression

```fern
<<block compression>>=
function add(x: u64, y: u64): u64 {
  return x + y + 2u64 * (x & 0xffffffffu64) * (y & 0xffffffffu64);
}

function mix(own m: i64[], ai: i32, bi: i32, ci: i32, di: i32): i64[] {
  let a = m[ai] as u64;
  let b = m[bi] as u64;
  let c = m[ci] as u64;
  let d = m[di] as u64;
  a = add(a, b);
  d = (d ^ a).rotate_right(32);
  c = add(c, d);
  b = (b ^ c).rotate_right(24);
  a = add(a, b);
  d = (d ^ a).rotate_right(16);
  c = add(c, d);
  b = (b ^ c).rotate_right(63);
  return m.with(ai, a as i64).with(bi, b as i64).with(ci, c as i64).with(di, d as i64);
}

// Each round takes eight pairs of adjacent words. The pairs are two words
// apart across a row and sixteen words apart down a column.
function round(own m: i64[], base: i32, stride: i32): i64[] {
  let p0 = base;
  let p1 = base + stride;
  let p2 = base + 2 * stride;
  let p3 = base + 3 * stride;
  let p4 = base + 4 * stride;
  let p5 = base + 5 * stride;
  let p6 = base + 6 * stride;
  let p7 = base + 7 * stride;
  m = mix(m, p0, p2, p4, p6);
  m = mix(m, p0 + 1, p2 + 1, p4 + 1, p6 + 1);
  m = mix(m, p1, p3, p5, p7);
  m = mix(m, p1 + 1, p3 + 1, p5 + 1, p7 + 1);
  m = mix(m, p0, p2 + 1, p5, p7 + 1);
  m = mix(m, p0 + 1, p3, p5 + 1, p6);
  m = mix(m, p1, p3 + 1, p4, p6 + 1);
  m = mix(m, p1 + 1, p2, p4 + 1, p7);
  return m;
}

function compress(own m: i64[], left: i32, right: i32, dest: i32, tmp: i32, accumulate: boolean): i64[] {
  for i in 0..128 {
    m = m.with(tmp + i, m[left + i] ^ m[right + i]);
  }
  for i in 0..8 {
    m = round(m, tmp + 16 * i, 2);
  }
  for i in 0..8 {
    m = round(m, tmp + 2 * i, 16);
  }
  for i in 0..128 {
    let word = m[tmp + i] ^ m[left + i] ^ m[right + i];
    if (accumulate) {
      word ^= m[dest + i];
    }
    m = m.with(dest + i, word);
  }
  return m;
}
```

### Matrix traversal

```fern
<<matrix traversal>>=
// Select a reference within the completed part of the matrix (section 3.4).
function reference(j: u64, pass: i32, slice: i32, lane: i32, index: i32, segment: i32, lanes: i32): i32 {
  let other = ((j >> 32u64) % (lanes as u64)) as i32;
  if (pass == 0 && slice == 0) {
    other = lane;
  }
  let span = if (pass == 0) { slice * segment } else { 3 * segment };
  if (other == lane) {
    span += index - 1;
  } else if (index == 0) {
    span -= 1;
  }
  let low = j & 0xffffffffu64;
  let relative = span - 1 - (span as u64 * (low * low >> 32u64) >> 32u64) as i32;
  let start = if (pass == 0) { 0 } else { (slice + 1) % 4 * segment };
  let width = 4 * segment;
  return other * width + (start + relative) % width;
}

function fill_segment(own m: i64[], pass: i32, slice: i32, lane: i32, segment: i32, p: Params): i64[] {
  let width = 4 * segment;
  let blocks = width * p.lanes;
  let zero = blocks * 128;
  let input = zero + 128;
  let addr = input + 128;
  let tmp = addr + 128;
  let independent = pass == 0 && slice < 2;
  if (independent) {
    m = m.with(input, pass as i64).with(input + 1, lane as i64).with(input + 2, slice as i64).with(input + 3, blocks as i64).with(input + 4, p.passes as i64).with(input + 5, 2i64).with(input + 6, 0i64);
  }
  let first = if (pass == 0 && slice == 0) { 2 } else { 0 };
  for i in first..segment {
    let pos = slice * segment + i;
    let prev = lane * width + (pos + width - 1) % width;
    let j = m[prev * 128] as u64;
    if (independent) {
      if (i == first || i % 128 == 0) {
        m = m.with(input + 6, m[input + 6] + 1i64);
        m = compress(m, zero, input, addr, tmp, false);
        m = compress(m, zero, addr, addr, tmp, false);
      }
      j = m[addr + i % 128] as u64;
    }
    let ref_block = reference(j, pass, slice, lane, i, segment, p.lanes);
    m = compress(m, prev * 128, ref_block * 128, (lane * width + pos) * 128, tmp, pass != 0);
  }
  return m;
}
```

### Key derivation

```fern
<<key derivation>>=
pub function key(password: [u8], salt: [u8], p: Params): Result[u8[], ArgonError] {
  return derive(password, salt, [], [], p);
}

function fits_limit(blocks: i32, p: Params): boolean {
  let budget = p.limit_kib as i64 * 1024i64;
  let matrix = (blocks as i64 + 4i64) * 1024i64;
  // Fern array byte sizes and offsets must fit in i32, including the header.
  let largest = if (matrix > p.tag_len as i64) { matrix } else { p.tag_len as i64 };
  return largest <= budget && largest + 32i64 <= 2147483647i64;
}

function segment_length(p: Params, salt_len: i32): Result[i32, ArgonError] {
  if (p.lanes < 1 || p.lanes > 0xffffff || p.passes < 1 || p.tag_len < 4 || p.memory_kib < 8 * p.lanes) {
    return Err(Parameters);
  }
  if (salt_len < 8) {
    return Err(SaltLength);
  }
  let segment = p.memory_kib / (4 * p.lanes);
  let blocks = 4 * p.lanes * segment;
  if (!fits_limit(blocks, p)) {
    return Err(MemoryLimit);
  }
  return Ok(segment);
}

function initialize(own h0: u8[], segment: i32, lanes: i32): i64[] {
  let blocks = 4 * lanes * segment;
  let m = __alloc_i64((blocks + 4) * 128);
  // Address generation uses one zero block and a zero-padded input block.
  for i in 0..256 {
    m = m.with(blocks * 128 + i, 0i64);
  }
  for lane in 0..lanes {
    for i in 0..2 {
      let seed = h0.concat(binary.append_u32_le(binary.append_u32_le([], i as u32), lane as u32));
      let bytes = expand(seed, 1024);
      crypto.erase(seed);
      for j in 0..128 {
        m = m.with((lane * 4 * segment + i) * 128 + j, binary.read_u64_le(bytes, j * 8).unwrap_or(0u64) as i64);
      }
      crypto.erase(bytes);
    }
  }
  crypto.erase(h0);
  return m;
}

function finish(own m: i64[], p: Params, segment: i32): u8[] {
  let last: u8[] = [];
  for j in 0..128 {
    let word = 0i64;
    for lane in 0..p.lanes {
      word ^= m[((lane + 1) * 4 * segment - 1) * 128 + j];
    }
    last = binary.append_u64_le(last, word as u64);
  }
  crypto.erase(m);
  let tag = expand(last, p.tag_len);
  crypto.erase(last);
  return tag;
}

pub function derive(password: [u8], salt: [u8], secret: [u8], data: [u8], p: Params): Result[u8[], ArgonError] {
  let segment = segment_length(p, salt.len())?;
  let m = initialize(initial(p, password, salt, secret, data), segment, p.lanes);
  for pass in 0..p.passes {
    for slice in 0..4 {
      for lane in 0..p.lanes {
        m = fill_segment(m, pass, slice, lane, segment, p);
      }
    }
  }
  return Ok(finish(m, p, segment));
}
```
