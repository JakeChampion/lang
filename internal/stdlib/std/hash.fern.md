# Checksums and fast hashes

This module calculates CRC-32, BSD, and System V checksums.

## Model

Checksum states accept borrowed byte views and return updated states. Separate types preserve the CRC-32, POSIX, BSD, and System V rules.

## Contracts

The finish method applies the checksum-specific final step. Byte counts belong to the state. These checksums do not provide message authentication.

## Executable example

```fern test name=std-hash-example
import "std/hash";
import "std/string";

function main(): i32 {
  if (hash.crc32_of("123456789".as_bytes()) != 0xcbf43926 as u32) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub struct Cksum>>

<<3 function __cksum_tables(): u32[]>>

<<4 pub function cksum_new(): Cksum>>

<<5 pub struct Crc32>>

<<6 function __crc32b_tables(): u32[]>>

<<7 pub function crc32_new(): Crc32>>

<<8 function __crc32_absorb(c: Crc32, bs: [u8]): Crc32>>

<<9 pub function (c: Crc32) update(chunk: [u8]): Crc32>>

<<10 pub struct BsdSum>>

<<11 pub function bsd_sum_new(): BsdSum>>

<<12 pub struct SysvSum>>

<<13 pub function sysv_sum_new(): SysvSum>>

<<14 function __xxh_tail(bs: [u8], at: i32, h: u64): u64>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates CRC-32, BSD, and System V checksums.
```

### `2 pub struct Cksum`

```fern
<<2 pub struct Cksum>>=
pub struct Cksum {
  crc: u32,
  len: u64,
  table: u32[],
}
```

### `3 function __cksum_tables(): u32[]`

```fern
<<3 function __cksum_tables(): u32[]>>=
function __cksum_tables(): u32[] {
  let t: u32[] = [];
  let i: u32 = 0;
  while (i < 256) {
    let c: u32 = i << 24;
    let k: i32 = 0;
    while (k < 8) {
      if ((c & 0x80000000) != 0) {
        c = c << 1 ^ 0x04c11db7;
      } else {
        c = c << 1;
      }
      k = k + 1;
    }
    t = t.append(c);
    i = i + 1;
  }
  let j: i32 = 0;
  let p: u32 = 0;
  while (j < 1792) {
    p = t[j];
    t = t.append(p << 8 ^ t[(p >> 24 & 255) as i32]);
    j = j + 1;
  }
  return t;
}
```

### `4 pub function cksum_new(): Cksum`

```fern
<<4 pub function cksum_new(): Cksum>>=
pub function cksum_new(): Cksum {
  return Cksum { crc: 0, len: 0 as u64, table: __cksum_tables() };
}

pub function (c: Cksum) update(chunk: [u8]): Cksum {
  return Cksum {
    ...c, crc: __crc32_cksum_array(c.crc as i32, chunk) as u32,
    len: c.len + chunk.len() as u64,
  };
}

pub function (c: Cksum) byte_count(): u64 {
  return c.len;
}

pub function (c: Cksum) finish(): u32 {
  let t: u32[] = c.table;
  let crc: u32 = c.crc;
  let n: u64 = c.len;
  while (n != 0 as u64) {
    crc = crc << 8 ^ t[((crc >> 24 ^ (n & 255 as u64) as u32) & 255) as i32];
    n = n >> 8 as u64;
  }
  return crc ^ 0xffffffff;
}

pub function cksum_of(s: [u8]): u32 {
  return cksum_new().update(s).finish();
}
```

### `5 pub struct Crc32`

```fern
<<5 pub struct Crc32>>=
pub struct Crc32 {
  crc: u32,
  len: u64,
  table: u32[],
}
```

### `6 function __crc32b_tables(): u32[]`

```fern
<<6 function __crc32b_tables(): u32[]>>=
function __crc32b_tables(): u32[] {
  let t: u32[] = [];
  let i: u32 = 0;
  while (i < 256) {
    let c: u32 = i;
    let k: i32 = 0;
    while (k < 8) {
      if ((c & 1) != 0) {
        c = c >> 1 ^ 0xedb88320;
      } else {
        c = c >> 1;
      }
      k = k + 1;
    }
    t = t.append(c);
    i = i + 1;
  }
  let j: i32 = 0;
  let p: u32 = 0;
  while (j < 1792) {
    p = t[j];
    t = t.append(p >> 8 ^ t[(p & 255) as i32]);
    j = j + 1;
  }
  return t;
}
```

### `7 pub function crc32_new(): Crc32`

```fern
<<7 pub function crc32_new(): Crc32>>=
pub function crc32_new(): Crc32 {
  return Crc32 { crc: 0xffffffff, len: 0 as u64, table: __crc32b_tables() };
}
```

### `8 function __crc32_absorb(c: Crc32, bs: [u8]): Crc32`

```fern
<<8 function __crc32_absorb(c: Crc32, bs: [u8]): Crc32>>=
function __crc32_absorb(c: Crc32, bs: [u8]): Crc32 {
  let t: u32[] = c.table;
  let crc: u32 = c.crc;
  let n: i32 = bs.len();
  let i: i32 = 0;
  let lim: i32 = n - 8;
  let w0: u32 = 0;
  let w1: u32 = 0;
  while (i <= lim) {
    w0 = crc ^ (bs[i] as u32 | bs[i + 1] as u32 << 8 | bs[i + 2] as u32 << 16 | bs[i + 3] as u32 << 24);
    w1 = bs[i + 4] as u32 | bs[i + 5] as u32 << 8 | bs[i + 6] as u32 << 16 | bs[i + 7] as u32 << 24;
    crc = t[1792 + (w0 & 255) as i32] ^ t[1536 + (w0 >> 8 & 255) as i32] ^ t[1280 + (w0 >> 16 & 255) as i32] ^ t[1024 + (w0 >> 24 & 255) as i32] ^ t[768 + (w1 & 255) as i32] ^ t[512 + (w1 >> 8 & 255) as i32] ^ t[256 + (w1 >> 16 & 255) as i32] ^ t[(w1 >> 24 & 255) as i32];
    i = i + 8;
  }
  while (i < n) {
    crc = crc >> 8 ^ t[((crc ^ bs[i] as u32) & 255) as i32];
    i = i + 1;
  }
  return Crc32 { ...c, crc: crc, len: c.len + n as u64 };
}
```

### `9 pub function (c: Crc32) update(chunk: [u8]): Crc32`

```fern
<<9 pub function (c: Crc32) update(chunk: [u8]): Crc32>>=
pub function (c: Crc32) update(chunk: [u8]): Crc32 {
  c = __crc32_absorb(c, chunk);
  return c;
}

pub function (c: Crc32) byte_count(): u64 {
  return c.len;
}

pub function (c: Crc32) finish(): u32 {
  return c.crc ^ 0xffffffff;
}

pub function crc32_of(s: [u8]): u32 {
  return crc32_new().update(s).finish();
}
```

### `10 pub struct BsdSum`

```fern
<<10 pub struct BsdSum>>=
pub struct BsdSum {
  sum: u32,
  len: u64,
}
```

### `11 pub function bsd_sum_new(): BsdSum`

```fern
<<11 pub function bsd_sum_new(): BsdSum>>=
pub function bsd_sum_new(): BsdSum {
  return BsdSum { sum: 0, len: 0 as u64 };
}

pub function (s: BsdSum) update(chunk: [u8]): BsdSum {
  return BsdSum { sum: __bsd_sum_bytes(chunk, s.sum as i32) as u32, len: s.len + chunk.len() as u64 };
}

pub function (s: BsdSum) finish(): u32 {
  return s.sum;
}

pub function (s: BsdSum) byte_count(): u64 {
  return s.len;
}

pub function (s: BsdSum) blocks(): u64 {
  return (s.len + 1023 as u64) / (1024 as u64);
}

pub function bsd_sum_of(s: [u8]): u32 {
  return bsd_sum_new().update(s).finish();
}
```

### `12 pub struct SysvSum`

```fern
<<12 pub struct SysvSum>>=
pub struct SysvSum {
  sum: u32,
  len: u64,
}
```

### `13 pub function sysv_sum_new(): SysvSum`

```fern
<<13 pub function sysv_sum_new(): SysvSum>>=
pub function sysv_sum_new(): SysvSum {
  return SysvSum { sum: 0, len: 0 as u64 };
}

pub function (s: SysvSum) update(chunk: [u8]): SysvSum {
  return SysvSum { sum: s.sum + __sum_bytes_array(chunk) as u32, len: s.len + chunk.len() as u64 };
}

pub function (s: SysvSum) finish(): u32 {
  let r: u32 = (s.sum & 0xffff) + (s.sum >> 16);
  return (r & 0xffff) + (r >> 16);
}

pub function (s: SysvSum) byte_count(): u64 {
  return s.len;
}

pub function (s: SysvSum) blocks(): u64 {
  return (s.len + 511 as u64) / (512 as u64);
}

pub function sysv_sum_of(s: [u8]): u32 {
  return sysv_sum_new().update(s).finish();
}

pub function xxh64(bs: [u8], seed: u64): u64 {
  let n: i32 = bs.len();
  let at: i32 = 0;
  let h: u64 = seed + 0x27D4EB2F165667C5;
  if (n >= 32) {
    let v: u64[] = [seed + 0x9E3779B185EBCA87 + 0xC2B2AE3D27D4EB4F, seed + 0xC2B2AE3D27D4EB4F, seed, seed - 0x9E3779B185EBCA87];
    while (at + 32 <= n) {
      v = [__xxh_round(v[0], __le64(bs, at)), __xxh_round(v[1], __le64(bs, at + 8)), __xxh_round(v[2], __le64(bs, at + 16)), __xxh_round(v[3], __le64(bs, at + 24))];
      at = at + 32;
    }
    h = __rotl64(v[0], 1) + __rotl64(v[1], 7) + __rotl64(v[2], 12) + __rotl64(v[3], 18);
    for x in v {
      h = (h ^ __xxh_round(0, x)) * 0x9E3779B185EBCA87 + 0x85EBCA77C2B2AE63;
    }
  }
  return __xxh_tail(bs, at, h + n as u64);
}
```

### `14 function __xxh_tail(bs: [u8], at: i32, h: u64): u64`

```fern
<<14 function __xxh_tail(bs: [u8], at: i32, h: u64): u64>>=
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

function __xxh_round(acc: u64, input: u64): u64 {
  return __rotl64(acc + input * 0xC2B2AE3D27D4EB4F, 31) * 0x9E3779B185EBCA87;
}

function __rotl64(x: u64, r: u64): u64 {
  return x << r | x >> 64 - r;
}

function __le64(bs: [u8], at: i32): u64 {
  let v: u64 = 0;
  let i: i32 = 7;
  while (i >= 0) {
    v = v << 8 | bs[at + i] as u64;
    i = i - 1;
  }
  return v;
}
```
