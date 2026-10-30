# System V sum

This module calculates the System V `sum -s` checksum.

## Model

A `SysvSum` state accepts borrowed byte views and returns an updated state. The state counts the bytes for the block count.

## Contracts

`finish` folds the 32-bit byte sum to 16 bits. `blocks` counts 512-byte blocks, rounded up. The checksum does not provide message authentication.

## Executable example

```fern test name=std-hash-sysv-sum-example
import "std/hash/sysv_sum";
import "std/string";

function main(): i32 {
  if (sysv_sum.of("abc".as_bytes()) != 294 as u32) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub struct SysvSum>>

<<3 pub function new(): SysvSum>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates the System V sum checksum.
```

### `2 pub struct SysvSum`

```fern
<<2 pub struct SysvSum>>=
pub struct SysvSum {
  sum: u32,
  len: u64,
}
```

### `3 pub function new(): SysvSum`

```fern
<<3 pub function new(): SysvSum>>=
pub function new(): SysvSum {
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

pub function of(s: [u8]): u32 {
  return new().update(s).finish();
}
```
