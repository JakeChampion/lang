# scrypt key derivation

This module derives keys from passwords with scrypt under RFC 7914.

## Model

`key` runs PBKDF2-HMAC-SHA256, then scryptROMix on each of the p blocks, then PBKDF2-HMAC-SHA256 again. The cost N sets the memory and the time: one ROMix holds N blocks of 128r bytes. An `i64` slot holds two 32-bit words of a block. ROMix also holds X, Y and a block of zeros, so the memory is 128r(N + 3) bytes.

## Contracts

N is a power of 2 that is more than 1. The block size r and the parallelization p are 1 or more. Parameters that need more than 2^31 - 1 slots give `Memory`. `key` caps the variable-sized working buffers at 32 MiB. Use `key_with_memory_limit` to supply a byte limit. The check counts the ROMix array and parallel blocks together, and the PBKDF2 output and salt copy while each derivation runs. It excludes caller-owned inputs, fixed-size hash state and allocator overhead. scrypt reads its memory at an index that depends on the password. That index is the only value that the module makes public under the constant-time gate.

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

<<4 the word order>>

<<5 ROMix>>

<<6 pub function key>>
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
  MemoryLimit(i64),
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
    MemoryLimit(limit) => {
      return f"scrypt: working memory exceeds the limit of {limit} bytes";
    },
    Memory => {
      return "scrypt: the parameters need more than 2^31 - 1 slots of memory";
    }
  }
  return "";
}
```

### `4 the word order`

The compiler kernel `__scrypt_block_mix` runs BlockMix. It keeps the 16 words of each 64-byte block in the order of `ORDER`. In this order, each row of 4 words is a diagonal of the Salsa20 matrix. The kernel reads and writes this order only. Thus the module changes the order when it reads the PBKDF2 output, and it changes it back for the output.

ROMix reads its index from the first word of a block. That word stays in the first position.

```fern
<<4 the word order>>=
// Position k of a block holds word ORDER[k]. Slot k holds positions 2k and
// 2k + 1, the first in the low half.
const ORDER: i32[] = [0, 5, 10, 15, 4, 9, 14, 3, 8, 13, 2, 7, 12, 1, 6, 11];

// The little-endian word at bytes[at:at + 4].
function __get_le32(bytes: [u8], at: i32): u64 {
  return bytes[at] as u64 | bytes[at + 1] as u64 << 8 | bytes[at + 2] as u64 << 16 | bytes[at + 3] as u64 << 24;
}

function __put_le32(own d: u8[], at: i32, v: u64): u8[] {
  let i = 0;
  while (i < 4) {
    d = d.with(at + i, (v >> (8 * i) as u64 & 255u64) as u8);
    i += 1;
  }
  return d;
}

// Reads the 64-byte block at bytes[at:] into the 8 slots at b[to:].
function __get_block(own b: i64[], to: i32, bytes: [u8], at: i32): i64[] {
  let k = 0;
  while (k < 8) {
    let lo = __get_le32(bytes, at + 4 * ORDER[2 * k]);
    let hi = __get_le32(bytes, at + 4 * ORDER[2 * k + 1]);
    b = b.with(to + k, (lo | hi << 32) as i64);
    k += 1;
  }
  return b;
}

// Writes the 8 slots at b[from:] to the 64-byte block at bytes[at:].
function __put_block(own bytes: u8[], at: i32, b: [i64], from: i32): u8[] {
  let k = 0;
  while (k < 8) {
    let v = b[from + k] as u64;
    bytes = __put_le32(bytes, at + 4 * ORDER[2 * k], v);
    bytes = __put_le32(bytes, at + 4 * ORDER[2 * k + 1], v >> 32);
    k += 1;
  }
  return bytes;
}
```

### `5 ROMix`

The first loop of ROMix writes each block of V directly after the block before it. It mixes in a block of zeros, because the kernel always mixes in a block. The second loop mixes in a block of V as part of each BlockMix, so it writes no intermediate block.

```fern
<<5 ROMix>>=
// The index of the block of V that ROMix mixes in next: the first word of
// the last 64 bytes of the block at b[at:], modulo n. scrypt reads V at an
// index that depends on the password, so the index is public.
function __index(b: [i64], at: i32, h: i32, n: i32, own seen: u8[]): (i32, u8[]) {
  let j = b[at + h - 8] as i32 & n - 1;
  seen = seen.with(0, (j & 255) as u8).with(1, (j >> 8 & 255) as u8).with(2, (j >> 16 & 255) as u8).with(3, (j >> 24 & 255) as u8);
  __ct_public(seen);
  return (seen[0] as i32 | seen[1] as i32 << 8 | seen[2] as i32 << 16 | seen[3] as i32 << 24, seen);
}

// scryptROMix. b holds n + 3 blocks of h = 16r slots: V, then X, Y and a
// block of zeros. X is in block 0 at the start and in block n at the end.
function __ro_mix(own b: i64[], r: i32, n: i32): i64[] {
  let h = 16 * r;
  let zeros = (n + 2) * h;
  let i = 0;
  while (i < n) {
    b = __scrypt_block_mix(b, i * h, zeros, (i + 1) * h, r);
    i += 1;
  }
  let x = n * h;
  let y = x + h;
  let seen = __alloc_u8(4);
  i = 0;
  while (i < n) {
    let (j, s1) = __index(b, x, h, n, seen);
    b = __scrypt_block_mix(b, x, j * h, y, r);
    let (k, s2) = __index(b, y, h, n, s1);
    b = __scrypt_block_mix(b, y, k * h, x, r);
    seen = s2;
    i += 2;
  }
  return b;
}
```

### `6 pub function key`

```fern
<<6 pub function key>>=
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
  let block_bytes = 128i64 * (r as i64);
  // Divide before multiplying so even the largest i32 inputs cannot wrap.
  if (p as i64 > 2147483647i64 / block_bytes || n as i64 + 3i64 > 2147483647i64 / (16i64 * (r as i64))) {
    return Some(Memory);
  }
  return None;
}

// scrypt (RFC 7914) with a 32 MiB working memory limit.
pub function key(password: [u8], salt: [u8], n: i32, r: i32, p: i32, key_len: i32): Result[u8[], ScryptError] {
  return key_with_memory_limit(password, salt, n, r, p, key_len, 33554432i64);
}

// Check the peak variable-sized buffers before the first allocation.
pub function key_with_memory_limit(password: [u8], salt: [u8], n: i32, r: i32, p: i32, key_len: i32, max_bytes: i64): Result[u8[], ScryptError] {
  if let Some(e) = __refusal(n, r, p) {
    return Err(e);
  }
  let block_bytes = 128i64 * (r as i64);
  let blocks_bytes = block_bytes * (p as i64);
  let romix_bytes = block_bytes * (n as i64 + 3i64);
  let initial_bytes = blocks_bytes + salt.len() as i64 + 4i64;
  let final_bytes = 2i64 * blocks_bytes + 4i64 + key_len as i64;
  if (blocks_bytes + romix_bytes > max_bytes || initial_bytes > max_bytes || key_len > 0 && final_bytes > max_bytes) {
    return Err(MemoryLimit(max_bytes));
  }
  let h = 16 * r;
  let blocks = crypto.pbkdf2_sha256(password, salt, 1, 128 * r * p);
  let b = __alloc_i64(h * (n + 3));
  let k = 0;
  while (k < p) {
    let i = 0;
    while (i < 2 * r) {
      b = __get_block(b, 8 * i, blocks, 128 * r * k + 64 * i);
      i += 1;
    }
    b = __ro_mix(b, r, n);
    i = 0;
    while (i < 2 * r) {
      blocks = __put_block(blocks, 128 * r * k + 64 * i, b, n * h + 8 * i);
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
