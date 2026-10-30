# POSIX cksum

This module calculates the POSIX `cksum` CRC.

## Model

A `Cksum` state accepts borrowed byte views and returns an updated state. The state counts the bytes, because the final step mixes in the length.

## Contracts

`finish` applies the POSIX final step: it mixes in the byte count and inverts the result. The checksum does not provide message authentication.

## Executable example

```fern test name=std-hash-cksum-example
import "std/hash/cksum";
import "std/string";

function main(): i32 {
  if (cksum.of("123456789".as_bytes()) != 930766865 as u32) { return 1; }
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

<<4 pub function new(): Cksum>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates the POSIX cksum CRC.
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

### `4 pub function new(): Cksum`

```fern
<<4 pub function new(): Cksum>>=
pub function new(): Cksum {
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

pub function of(s: [u8]): u32 {
  return new().update(s).finish();
}
```
