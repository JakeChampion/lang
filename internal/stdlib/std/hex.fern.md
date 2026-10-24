# Hexadecimal text and bytes

This module encodes and decodes hexadecimal data.

## Model

Encoders borrow a byte view and choose lowercase or uppercase digits. Decode accepts either hexadecimal case and returns an owned byte array.

## Contracts

Decode requires complete pairs of hexadecimal digits. Invalid bytes and odd lengths return a codec error with a byte offset.

## Executable example

```fern test name=std-hex-example
import "std/hex";
import "std/string";

function main(): i32 {
  if (hex.encode("AB".as_bytes()) != "4142") { return 1; }
  let Ok(bytes) = hex.decode("fF") else { return 2; };
  if (bytes.len() != 1 || bytes[0] != 255 as u8) { return 3; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __hex_lower_char(d: i32): i32>>

<<4 pub function encode(bs: [u8]): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes and decodes hexadecimal data.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/codec";
```

### `3 function __hex_lower_char(d: i32): i32`

```fern
<<3 function __hex_lower_char(d: i32): i32>>=
function __hex_lower_char(d: i32): i32 {
  if (d < 10) {
    return d + 48;
  }
  return d + 87;
}

function __hex_upper_char(d: i32): i32 {
  if (d < 10) {
    return d + 48;
  }
  return d + 55;
}

function __hex_value(c: i32): i32 {
  if (c >= 48 && c < 58) {
    return c - 48;
  }
  if (c >= 97 && c < 103) {
    return c - 87;
  }
  if (c >= 65 && c < 71) {
    return c - 55;
  }
  return 0 - 1;
}
```

### `4 pub function encode(bs: [u8]): string`

```fern
<<4 pub function encode(bs: [u8]): string>>=
pub function encode(bs: [u8]): string {
  let n: i32 = bs.len();
  if (n == 0) {
    return "";
  }
  let buf: u8[] = __alloc_u8(n * 2);
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = bs[i] as i32;
    buf = buf.with(i * 2, __hex_lower_char(b >> 4 & 15) as u8);
    buf = buf.with(i * 2 + 1, __hex_lower_char(b & 15) as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function encode_upper(bs: [u8]): string {
  let n: i32 = bs.len();
  if (n == 0) {
    return "";
  }
  let buf: u8[] = __alloc_u8(n * 2);
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = bs[i] as i32;
    buf = buf.with(i * 2, __hex_upper_char(b >> 4 & 15) as u8);
    buf = buf.with(i * 2 + 1, __hex_upper_char(b & 15) as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function decode(input: string): Result[u8[], codec.DecodeError] {
  let i: i32 = 0;
  while (i < input.len()) {
    if (__hex_value(input[i] as i32) < 0) {
      return Err(codec.error("hex", i, codec.InvalidByte(input[i])));
    }
    i = i + 1;
  }
  if (input.len() % 2 != 0) {
    return Err(codec.error("hex", input.len(), codec.InvalidLength));
  }
  let bytes: u8[] = __alloc_u8(input.len() / 2);
  i = 0;
  while (i < bytes.len()) {
    let hi: i32 = __hex_value(input[i * 2] as i32);
    let lo: i32 = __hex_value(input[i * 2 + 1] as i32);
    bytes = bytes.with(i, (hi << 4 | lo) as u8);
    i = i + 1;
  }
  return Ok(bytes);
}
```
