# Hexadecimal text and bytes

This module encodes and decodes hexadecimal data.

## Model

Encoders borrow a byte view and choose lowercase or uppercase digits. An encoder reads 16 bytes in one step, as lanes. Decode accepts either hexadecimal case and returns an owned byte array.

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

<<3 function __hex_value(c: i32): i32>>

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

### `3 function __hex_value(c: i32): i32`

```fern
<<3 function __hex_value(c: i32): i32>>=
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
  return -1;
}
```

### `4 pub function encode(bs: [u8]): string`

```fern
<<4 pub function encode(bs: [u8]): string>>=
// `letter` is the code that digit 10 has, less 10.
function __hex_encode(bs: [u8], letter: i32): string {
  let n = bs.len();
  if (n == 0) {
    return "";
  }
  let buf = __alloc_u8(n * 2);
  let i = 0;
  if (n >= 16) {
    buf = __hex_lanes(bs, buf, letter);
    i = n - n % 16;
  }
  while (i < n) {
    let b = bs[i] as i32;
    buf = buf.with(i * 2, __hex_digit(b >> 4 & 15, letter) as u8);
    buf = buf.with(i * 2 + 1, __hex_digit(b & 15, letter) as u8);
    i += 1;
  }
  unsafe {
    return string_from_bytes_unchecked(buf);
  }
}

function __hex_digit(d: i32, letter: i32): i32 {
  if (d < 10) {
    return d + 48;
  }
  return d + letter;
}

// Writes the digits of each whole group of 16 bytes of `bs` to `buf`.
// 16 bytes give 32 digits. A lane of 16 bits holds the two digits of one
// byte, with the digit of the high half in its low byte.
function __hex_lanes(bs: [u8], own buf: u8[], letter: i32): u8[] {
  let a = (letter + 10) as u8;
  let digits = __u8x16_of(48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, a, a + 1 as u8, a + 2 as u8, a + 3 as u8, a + 4 as u8, a + 5 as u8);
  let low = __u8x16_splat(15 as u8);
  let i = 0;
  while (i + 16 <= bs.len()) {
    let v = __u8x16_load_view(bs, i);
    let hi = __u8x16_swizzle(digits, __u8x16_shr(v, 4));
    let lo = __u8x16_swizzle(digits, __u8x16_and(v, low));
    buf = __u16x8_with(buf, i * 2, __u16x8_or(__u8x16_widen_lo(hi), __u16x8_shl(__u8x16_widen_lo(lo), 8)));
    buf = __u16x8_with(buf, i * 2 + 16, __u16x8_or(__u8x16_widen_hi(hi), __u16x8_shl(__u8x16_widen_hi(lo), 8)));
    i += 16;
  }
  return buf;
}

pub function encode(bs: [u8]): string {
  return __hex_encode(bs, 87);
}

pub function encode_upper(bs: [u8]): string {
  return __hex_encode(bs, 55);
}

pub function decode(input: string): Result[u8[], codec.DecodeError] {
  let i = 0;
  while (i < input.len()) {
    if (__hex_value(input[i] as i32) < 0) {
      return Err(codec.error("hex", i, codec.InvalidByte(input[i])));
    }
    i += 1;
  }
  if (input.len() % 2 != 0) {
    return Err(codec.error("hex", input.len(), codec.InvalidLength));
  }
  let bytes = __alloc_u8(input.len() / 2);
  i = 0;
  while (i < bytes.len()) {
    let hi = __hex_value(input[i * 2] as i32);
    let lo = __hex_value(input[i * 2 + 1] as i32);
    bytes = bytes.with(i, (hi << 4 | lo) as u8);
    i += 1;
  }
  return Ok(bytes);
}
```
