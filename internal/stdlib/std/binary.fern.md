# Network-order integer fields

This module reads and writes fixed-width integers in network byte order.

## Model

The byte append helpers construct owned buffers. Integer fields use network byte order, with the most significant byte first.

## Contracts

Readers borrow a byte view and validate the requested offset. An incomplete field returns `OutOfBounds` rather than a partial integer.

## Executable example

```fern test name=std-binary-example
import "std/binary";

function main(): i32 {
  let bytes = binary.append_u32_be([], 0x01020304 as u32);
  let Ok(value) = binary.read_u32_be(bytes, 0) else { return 1; };
  if (value != 0x01020304 as u32 || bytes[0] != 1 as u8) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum DecodeError>>

<<4 pub function (e: DecodeError) message(): string>>

<<5 impl error.Error for DecodeError>>

<<6 pub function append_bytes(own output: u8[], bytes: [u8]): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads and writes fixed-width integers in network byte order.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "std/error";
```

### `3 pub enum DecodeError`

```fern
<<3 pub enum DecodeError>>=
pub enum DecodeError { OutOfBounds }
```

### `4 pub function (e: DecodeError) message(): string`

```fern
<<4 pub function (e: DecodeError) message(): string>>=
pub function (e: DecodeError) message(): string {
  return "integer field is outside the byte view";
}

pub function (e: DecodeError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for DecodeError`

```fern
<<5 impl error.Error for DecodeError>>=
impl error.Error for DecodeError {}

impl cmp.Display for DecodeError {}
```

### `6 pub function append_bytes(own output: u8[], bytes: [u8]): u8[]`

```fern
<<6 pub function append_bytes(own output: u8[], bytes: [u8]): u8[]>>=
pub function append_bytes(own output: u8[], bytes: [u8]): u8[] {
  let index: i32 = 0;
  while (index < bytes.len()) {
    output = output.append(bytes[index]);
    index = index + 1;
  }
  return output;
}

pub function append_u32_be(own output: u8[], value: u32): u8[] {
  output = output.append((value >> 24) as u8);
  output = output.append((value >> 16 & 255) as u8);
  output = output.append((value >> 8 & 255) as u8);
  return output.append((value & 255) as u8);
}

pub function append_u64_be(own output: u8[], value: u64): u8[] {
  output = append_u32_be(output, (value >> 32) as u32);
  return append_u32_be(output, value as u32);
}

pub function read_u32_be(input: [u8], offset: i32): Result[u32, DecodeError] {
  if (offset < 0 || input.len() < 4 || offset > input.len() - 4) {
    return Err(OutOfBounds);
  }
  return Ok(input[offset] as u32 << 24 | input[offset + 1] as u32 << 16
    | input[offset + 2] as u32 << 8 | input[offset + 3] as u32);
}

pub function read_u64_be(input: [u8], offset: i32): Result[u64, DecodeError] {
  if (offset < 0 || input.len() < 8 || offset > input.len() - 8) {
    return Err(OutOfBounds);
  }
  let high: u32 = read_u32_be(input, offset)?;
  let low: u32 = read_u32_be(input, offset + 4)?;
  return Ok(high as u64 << 32 | low as u64);
}
```
