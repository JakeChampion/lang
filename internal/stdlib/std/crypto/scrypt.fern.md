# scrypt key derivation

This module derives keys from passwords with scrypt under RFC 7914.

## Model

`key` runs PBKDF2-HMAC-SHA256, then scryptROMix on each of the p blocks, then PBKDF2-HMAC-SHA256 again. The cost N sets the memory and the time: one ROMix holds N blocks of 128r bytes. An `i64` slot holds two 32-bit words of a block, so the memory is 128r(N + 2) bytes.

## Contracts

N is a power of 2 that is more than 1. The block size r and the parallelization p are 1 or more. Parameters that need more than 2^31 - 1 slots give `Memory`. scrypt reads its memory at an index that depends on the password. That index is the only value that the module makes public under the constant-time gate.

## Executable example

```fern test name=std-crypto-scrypt-example
import "std/crypto/scrypt";
import "std/hex";

function main(): i32 {
  match (scrypt.key("password".as_bytes(), "salt".as_bytes(), 16, 1, 1, 16)) {
    Ok(k) => {
      if (hex.encode(k) != "45133c3dfba48c82235df51a53499241") { return 1; }
    },
    Err(_) => { return 2; }
  }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum ScryptError>>

<<4 the Salsa20/8 core>>

<<5 BlockMix and ROMix>>

<<6 byte order>>

<<7 pub function key>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module derives keys from passwords with scrypt under RFC 7914.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
import "core/error";
import "core/cmp";
```

### `3 pub enum ScryptError`

Each parameter error keeps the value that the caller gave.

```fern
<<3 pub enum ScryptError>>=
pub enum ScryptError {
  Cost(i32),
  BlockSize(i32),
  Parallelism(i32),
  Memory,
}

impl error.Error for ScryptError {}

impl cmp.Display for ScryptError {}

pub function (e: ScryptError) to_string(): string {
  return e.message();
}

pub function (e: ScryptError) message(): string {
  match (e) {
    Cost(n) => {
      return "scrypt: the cost N is " + n.to_string() + ", want a power of 2 that is more than 1";
    },
    BlockSize(r) => {
      return "scrypt: the block size r is " + r.to_string() + ", want 1 or more";
    },
    Parallelism(p) => {
      return "scrypt: the parallelization p is " + p.to_string() + ", want 1 or more";
    },
    Memory => {
      return "scrypt: the parameters need more than 2^31 - 1 slots of memory";
    }
  }
  return "";
}
```

### `4 the Salsa20/8 core`

The core keeps the 16 words in locals. It reads two slots for each pair of words, and it writes the result over the output block.

```fern
<<4 the Salsa20/8 core>>=
// The Salsa20/8 core of b[prev:prev + 8] ^ b[src:src + 8], written to
// b[dst:dst + 8]. Each slot holds two words, the first in the low half.
function __salsa_xor(own b: i64[], prev: i32, src: i32, dst: i32): i64[] {
  let p0 = b[prev] ^ b[src];
  let p1 = b[prev + 1] ^ b[src + 1];
  let p2 = b[prev + 2] ^ b[src + 2];
  let p3 = b[prev + 3] ^ b[src + 3];
  let p4 = b[prev + 4] ^ b[src + 4];
  let p5 = b[prev + 5] ^ b[src + 5];
  let p6 = b[prev + 6] ^ b[src + 6];
  let p7 = b[prev + 7] ^ b[src + 7];
  let w0 = p0 as u32;
  let w1 = (p0 >> 32) as u32;
  let w2 = p1 as u32;
  let w3 = (p1 >> 32) as u32;
  let w4 = p2 as u32;
  let w5 = (p2 >> 32) as u32;
  let w6 = p3 as u32;
  let w7 = (p3 >> 32) as u32;
  let w8 = p4 as u32;
  let w9 = (p4 >> 32) as u32;
  let w10 = p5 as u32;
  let w11 = (p5 >> 32) as u32;
  let w12 = p6 as u32;
  let w13 = (p6 >> 32) as u32;
  let w14 = p7 as u32;
  let w15 = (p7 >> 32) as u32;
  let x0 = w0;
  let x1 = w1;
  let x2 = w2;
  let x3 = w3;
  let x4 = w4;
  let x5 = w5;
  let x6 = w6;
  let x7 = w7;
  let x8 = w8;
  let x9 = w9;
  let x10 = w10;
  let x11 = w11;
  let x12 = w12;
  let x13 = w13;
  let x14 = w14;
  let x15 = w15;
  let i = 0;
  while (i < 4) {
    let t = x0 + x12;
    x4 ^= t << 7 | t >> 25;
    t = x4 + x0;
    x8 ^= t << 9 | t >> 23;
    t = x8 + x4;
    x12 ^= t << 13 | t >> 19;
    t = x12 + x8;
    x0 ^= t << 18 | t >> 14;
    t = x5 + x1;
    x9 ^= t << 7 | t >> 25;
    t = x9 + x5;
    x13 ^= t << 9 | t >> 23;
    t = x13 + x9;
    x1 ^= t << 13 | t >> 19;
    t = x1 + x13;
    x5 ^= t << 18 | t >> 14;
    t = x10 + x6;
    x14 ^= t << 7 | t >> 25;
    t = x14 + x10;
    x2 ^= t << 9 | t >> 23;
    t = x2 + x14;
    x6 ^= t << 13 | t >> 19;
    t = x6 + x2;
    x10 ^= t << 18 | t >> 14;
    t = x15 + x11;
    x3 ^= t << 7 | t >> 25;
    t = x3 + x15;
    x7 ^= t << 9 | t >> 23;
    t = x7 + x3;
    x11 ^= t << 13 | t >> 19;
    t = x11 + x7;
    x15 ^= t << 18 | t >> 14;
    t = x0 + x3;
    x1 ^= t << 7 | t >> 25;
    t = x1 + x0;
    x2 ^= t << 9 | t >> 23;
    t = x2 + x1;
    x3 ^= t << 13 | t >> 19;
    t = x3 + x2;
    x0 ^= t << 18 | t >> 14;
    t = x5 + x4;
    x6 ^= t << 7 | t >> 25;
    t = x6 + x5;
    x7 ^= t << 9 | t >> 23;
    t = x7 + x6;
    x4 ^= t << 13 | t >> 19;
    t = x4 + x7;
    x5 ^= t << 18 | t >> 14;
    t = x10 + x9;
    x11 ^= t << 7 | t >> 25;
    t = x11 + x10;
    x8 ^= t << 9 | t >> 23;
    t = x8 + x11;
    x9 ^= t << 13 | t >> 19;
    t = x9 + x8;
    x10 ^= t << 18 | t >> 14;
    t = x15 + x14;
    x12 ^= t << 7 | t >> 25;
    t = x12 + x15;
    x13 ^= t << 9 | t >> 23;
    t = x13 + x12;
    x14 ^= t << 13 | t >> 19;
    t = x14 + x13;
    x15 ^= t << 18 | t >> 14;
    i += 1;
  }
  b = b.with(dst, ((x0 + w0) as u64 | (x1 + w1) as u64 << 32) as i64);
  b = b.with(dst + 1, ((x2 + w2) as u64 | (x3 + w3) as u64 << 32) as i64);
  b = b.with(dst + 2, ((x4 + w4) as u64 | (x5 + w5) as u64 << 32) as i64);
  b = b.with(dst + 3, ((x6 + w6) as u64 | (x7 + w7) as u64 << 32) as i64);
  b = b.with(dst + 4, ((x8 + w8) as u64 | (x9 + w9) as u64 << 32) as i64);
  b = b.with(dst + 5, ((x10 + w10) as u64 | (x11 + w11) as u64 << 32) as i64);
  b = b.with(dst + 6, ((x12 + w12) as u64 | (x13 + w13) as u64 << 32) as i64);
  b = b.with(dst + 7, ((x14 + w14) as u64 | (x15 + w15) as u64 << 32) as i64);
  return b;
}
```

### `5 BlockMix and ROMix`

BlockMix writes the even outputs to the first half of the output block and the odd outputs to the second half. Thus no output needs a copy. The first loop of ROMix writes each block of V directly after the block before it.

```fern
<<5 BlockMix and ROMix>>=
// BlockMix of the block of 16r slots at b[from:] into b[to:].
function __block_mix(own b: i64[], from: i32, to: i32, r: i32): i64[] {
  let prev = from + (2 * r - 1) * 8;
  let i = 0;
  while (i < r) {
    let even = to + i * 8;
    b = __salsa_xor(b, prev, from + i * 16, even);
    let odd = to + (r + i) * 8;
    b = __salsa_xor(b, even, from + i * 16 + 8, odd);
    prev = odd;
    i += 1;
  }
  return b;
}

// XORs block j of V, the first n blocks of b, into the block at b[at:]. j
// is the first word of the last 64 bytes of that block, modulo n. scrypt
// reads V at an index that depends on the password, so the index is public.
function __mix_in(own b: i64[], at: i32, h: i32, n: i32, own seen: u8[]): (i64[], u8[]) {
  let j = b[at + h - 8] as i32 & n - 1;
  seen = seen.with(0, (j & 255) as u8).with(1, (j >> 8 & 255) as u8).with(2, (j >> 16 & 255) as u8).with(3, (j >> 24 & 255) as u8);
  __ct_public(seen);
  let from = (seen[0] as i32 | seen[1] as i32 << 8 | seen[2] as i32 << 16 | seen[3] as i32 << 24) * h;
  let i = 0;
  while (i < h) {
    b = b.with(at + i, b[at + i] ^ b[from + i]);
    i += 1;
  }
  return (b, seen);
}

// scryptROMix. b holds n + 2 blocks of h = 16r slots: V, then X and Y. X
// is in block 0 at the start and in block n at the end.
function __ro_mix(own b: i64[], r: i32, n: i32): i64[] {
  let h = 16 * r;
  let i = 0;
  while (i < n) {
    b = __block_mix(b, i * h, (i + 1) * h, r);
    i += 1;
  }
  let x = n * h;
  let y = x + h;
  let seen = __alloc_u8(4);
  i = 0;
  while (i < n) {
    let (b1, s1) = __mix_in(b, x, h, n, seen);
    b = __block_mix(b1, x, y, r);
    let (b2, s2) = __mix_in(b, y, h, n, s1);
    b = __block_mix(b2, y, x, r);
    seen = s2;
    i += 2;
  }
  return b;
}
```

### `6 byte order`

RFC 7914 reads each 32-bit word in little-endian order. Two words make one slot.

```fern
<<6 byte order>>=
function __get_le64(b: [u8], at: i32): i64 {
  let v = 0u64;
  let i = 7;
  while (i >= 0) {
    v = v << 8 | b[at + i] as u64;
    i -= 1;
  }
  return v as i64;
}

function __put_le64(own d: u8[], at: i32, v: i64): u8[] {
  let i = 0;
  while (i < 8) {
    d = d.with(at + i, (v >> (8 * i) as i64 & 255i64) as u8);
    i += 1;
  }
  return d;
}
```

### `7 pub function key`

```fern
<<7 pub function key>>=
function __refusal(n: i32, r: i32, p: i32): Option[ScryptError] {
  if (n < 2 || (n & n - 1) != 0) {
    return Some(Cost(n));
  }
  if (r < 1) {
    return Some(BlockSize(r));
  }
  if (p < 1) {
    return Some(Parallelism(p));
  }
  if (r as i64 * (p as i64) >= 1073741824i64 || 128i64 * (r as i64) * (p as i64) > 2147483647i64 || 16i64 * (r as i64) * (n as i64 + 2i64) > 2147483647i64) {
    return Some(Memory);
  }
  return None;
}

// scrypt (RFC 7914) of password and salt with cost n, block size r and
// parallelization p.
pub function key(password: [u8], salt: [u8], n: i32, r: i32, p: i32, key_len: i32): Result[u8[], ScryptError] {
  if let Some(e) = __refusal(n, r, p) {
    return Err(e);
  }
  let h = 16 * r;
  let blocks = crypto.pbkdf2_sha256(password, salt, 1, 128 * r * p);
  let b = __alloc_i64(h * (n + 2));
  let k = 0;
  while (k < p) {
    let i = 0;
    while (i < h) {
      b = b.with(i, __get_le64(blocks, 128 * r * k + 8 * i));
      i += 1;
    }
    b = __ro_mix(b, r, n);
    i = 0;
    while (i < h) {
      blocks = __put_le64(blocks, 128 * r * k + 8 * i, b[n * h + i]);
      i += 1;
    }
    k += 1;
  }
  crypto.erase(b);
  let out = crypto.pbkdf2_sha256(password, blocks, 1, key_len);
  crypto.erase(blocks);
  return Ok(out);
}
```
