# CRC-32

This module calculates the IEEE CRC-32 that zlib, gzip and PNG use.

## Model

A `Crc32` state accepts borrowed byte views and returns an updated state.

## Contracts

`finish` inverts the register, as the IEEE checksum requires. The checksum does not provide message authentication.

## Executable example

```fern test name=std-hash-crc32-example
import "std/hash/crc32";
import "std/string";

function main(): i32 {
  if (crc32.of("123456789".as_bytes()) != 0xcbf43926 as u32) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub struct Crc32>>

<<3 pub function new(): Crc32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates the IEEE CRC-32.
```

### `2 pub struct Crc32`

```fern
<<2 pub struct Crc32>>=
pub struct Crc32 {
  crc: u32,
  len: u64,
}
```

### `3 pub function new(): Crc32`

```fern
<<3 pub function new(): Crc32>>=
pub function new(): Crc32 {
  return Crc32 { crc: 0xffffffff, len: 0 as u64 };
}

pub function (c: Crc32) update(chunk: [u8]): Crc32 {
  return Crc32 {
    crc: __crc32_ieee_array(c.crc as i32, chunk) as u32,
    len: c.len + chunk.len() as u64,
  };
}

pub function (c: Crc32) byte_count(): u64 {
  return c.len;
}

pub function (c: Crc32) finish(): u32 {
  return c.crc ^ 0xffffffff;
}

pub function of(s: [u8]): u32 {
  return new().update(s).finish();
}
```
