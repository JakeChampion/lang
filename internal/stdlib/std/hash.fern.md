# Fast hashes

This module calculates the XXH64 hash. The checksums are in their own modules: `std/hash/cksum`, `std/hash/crc32`, `std/hash/bsd_sum` and `std/hash/sysv_sum`.

## Model

`xxh64` hashes a borrowed byte view with a seed and returns the 64-bit digest.

## Contracts

The digest agrees with the reference XXH64. It is not a cryptographic hash and does not provide message authentication.

## Executable example

```fern test name=std-hash-example
import "std/hash";
import "std/string";

function main(): i32 {
  if (hash.xxh64("".as_bytes(), 0 as u64) != 0xEF46DB3751D8E999 as u64) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function xxh64(bs: [u8], seed: u64): u64>>

<<3 function __xxh_tail(bs: [u8], at: i32, h: u64): u64>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates the XXH64 hash.
```

### `2 pub function xxh64(bs: [u8], seed: u64): u64`

```fern
<<2 pub function xxh64(bs: [u8], seed: u64): u64>>=
pub function xxh64(bs: [u8], seed: u64): u64 {
  let n: i32 = bs.len();
  let at: i32 = 0;
  let h: u64 = seed + 0x27D4EB2F165667C5;
  if (n >= 32) {
    let v1: u64 = seed + 0x9E3779B185EBCA87 + 0xC2B2AE3D27D4EB4F;
    let v2: u64 = seed + 0xC2B2AE3D27D4EB4F;
    let v3: u64 = seed;
    let v4: u64 = seed - 0x9E3779B185EBCA87;
    while (at + 32 <= n) {
      v1 = __xxh_round(v1, __le64(bs, at));
      v2 = __xxh_round(v2, __le64(bs, at + 8));
      v3 = __xxh_round(v3, __le64(bs, at + 16));
      v4 = __xxh_round(v4, __le64(bs, at + 24));
      at = at + 32;
    }
    h = __rotl64(v1, 1) + __rotl64(v2, 7) + __rotl64(v3, 12) + __rotl64(v4, 18);
    h = __xxh_merge(__xxh_merge(__xxh_merge(__xxh_merge(h, v1), v2), v3), v4);
  }
  return __xxh_tail(bs, at, h + n as u64);
}
```

### `3 function __xxh_tail(bs: [u8], at: i32, h: u64): u64`

```fern
<<3 function __xxh_tail(bs: [u8], at: i32, h: u64): u64>>=
function __xxh_tail(bs: [u8], at: i32, h: u64): u64 {
  let n: i32 = bs.len();
  while (at + 8 <= n) {
    h = __rotl64(h ^ __xxh_round(0, __le64(bs, at)), 27) * 0x9E3779B185EBCA87 + 0x85EBCA77C2B2AE63;
    at = at + 8;
  }
  if (at + 4 <= n) {
    let w: u64 = bs[at] as u64 | bs[at + 1] as u64 << 8 | bs[at + 2] as u64 << 16 | bs[at + 3] as u64 << 24;
    h = __rotl64(h ^ w * 0x9E3779B185EBCA87, 23) * 0xC2B2AE3D27D4EB4F + 0x165667B19E3779F9;
    at = at + 4;
  }
  while (at < n) {
    h = __rotl64(h ^ bs[at] as u64 * 0x27D4EB2F165667C5, 11) * 0x9E3779B185EBCA87;
    at = at + 1;
  }
  h = (h ^ h >> 33) * 0xC2B2AE3D27D4EB4F;
  h = (h ^ h >> 29) * 0x165667B19E3779F9;
  return h ^ h >> 32;
}

@inline
function __xxh_round(acc: u64, input: u64): u64 {
  return __rotl64(acc + input * 0xC2B2AE3D27D4EB4F, 31) * 0x9E3779B185EBCA87;
}

@inline
function __xxh_merge(h: u64, v: u64): u64 {
  return (h ^ __xxh_round(0, v)) * 0x9E3779B185EBCA87 + 0x85EBCA77C2B2AE63;
}

@inline
function __rotl64(x: u64, r: u64): u64 {
  return x << r | x >> 64 - r;
}

@inline
function __le64(bs: [u8], at: i32): u64 {
  return bs[at] as u64 | bs[at + 1] as u64 << 8 | bs[at + 2] as u64 << 16 | bs[at + 3] as u64 << 24
    | bs[at + 4] as u64 << 32 | bs[at + 5] as u64 << 40 | bs[at + 6] as u64 << 48 | bs[at + 7] as u64 << 56;
}
```
