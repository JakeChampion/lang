# BSD sum

This module calculates the BSD `sum` checksum.

## Model

A `BsdSum` state accepts borrowed byte views and returns an updated state. The state counts the bytes for the block count.

## Contracts

`finish` returns the 16-bit rotating sum. `blocks` counts 1024-byte blocks, rounded up. The checksum does not provide message authentication.

## Executable example

```fern test name=std-hash-bsd-sum-example
import "std/hash/bsd_sum";
import "std/string";

function main(): i32 {
  let s: bsd_sum.BsdSum = bsd_sum.new().update("abc".as_bytes());
  if (s.byte_count() != 3 as u64 || s.blocks() != 1 as u64) { return 1; }
  if (s.finish() != bsd_sum.of("abc".as_bytes())) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub struct BsdSum>>

<<3 pub function new(): BsdSum>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates the BSD sum checksum.
```

### `2 pub struct BsdSum`

```fern
<<2 pub struct BsdSum>>=
pub struct BsdSum {
  sum: u32,
  len: u64,
}
```

### `3 pub function new(): BsdSum`

```fern
<<3 pub function new(): BsdSum>>=
pub function new(): BsdSum {
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

pub function of(s: [u8]): u32 {
  return new().update(s).finish();
}
```
