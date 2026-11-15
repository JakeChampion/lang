# Hexadecimal text and bytes

This module encodes and decodes hexadecimal data.

## Model

Encoders borrow a byte view and choose lowercase or uppercase digits. An encoder reads 16 bytes in one step, as lanes. Decode accepts either hexadecimal case and returns an owned byte array. Decode reads 32 digits in one step, as lanes, until a step finds a byte that is not a digit.

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
  let bytes = __alloc_u8(input.len() / 2);
  let i = 0;
  if (input.len() % 2 == 0) {
    let lanes = __hex_unlanes(input.as_bytes(), bytes);
    bytes = lanes.bytes;
    i = lanes.at;
  }
  let first = i;
  while (i < input.len()) {
    if (__hex_value(input[i] as i32) < 0) {
      return Err(codec.error("hex", i, codec.InvalidByte(input[i])));
    }
    i += 1;
  }
  if (input.len() % 2 != 0) {
    return Err(codec.error("hex", input.len(), codec.InvalidLength));
  }
  i = first / 2;
  while (i < bytes.len()) {
    let hi = __hex_value(input[i * 2] as i32);
    let lo = __hex_value(input[i * 2 + 1] as i32);
    bytes = bytes.with(i, (hi << 4 | lo) as u8);
    i += 1;
  }
  return Ok(bytes);
}

struct __HexDecoded { bytes: u8[], at: i32 }

// Writes the bytes of each group of 32 digits of `digits` to `bytes`, up to
// the first group with a byte that is not a digit. `at` is the index of the
// first digit that it does not decode.
function __hex_unlanes(digits: [u8], own bytes: u8[]): __HexDecoded {
  let none = 255 as u8;
  let even_a = __u8x16_of(0 as u8, 2 as u8, 4 as u8, 6 as u8, 8 as u8, 10 as u8, 12 as u8, 14 as u8, none, none, none, none, none, none, none, none);
  let even_b = __u8x16_of(none, none, none, none, none, none, none, none, 0 as u8, 2 as u8, 4 as u8, 6 as u8, 8 as u8, 10 as u8, 12 as u8, 14 as u8);
  let odd_a = __u8x16_of(1 as u8, 3 as u8, 5 as u8, 7 as u8, 9 as u8, 11 as u8, 13 as u8, 15 as u8, none, none, none, none, none, none, none, none);
  let odd_b = __u8x16_of(none, none, none, none, none, none, none, none, 1 as u8, 3 as u8, 5 as u8, 7 as u8, 9 as u8, 11 as u8, 13 as u8, 15 as u8);
  let i = 0;
  while (i + 32 <= digits.len()) {
    let x = __u8x16_load_view(digits, i);
    let y = __u8x16_load_view(digits, i + 16);
    if (__u8x16_bitmask(__u8x16_and(__hex_valid(x), __hex_valid(y))) != 65535) {
      return __HexDecoded { bytes: bytes, at: i };
    }
    let xv = __hex_nibbles(x);
    let yv = __hex_nibbles(y);
    let hi = __u8x16_or(__u8x16_swizzle(xv, even_a), __u8x16_swizzle(yv, even_b));
    let lo = __u8x16_or(__u8x16_swizzle(xv, odd_a), __u8x16_swizzle(yv, odd_b));
    bytes = __u8x16_with(bytes, i / 2, __u8x16_or(__u8x16_shl(hi, 4), lo));
    i += 32;
  }
  return __HexDecoded { bytes: bytes, at: i };
}

// A lane of ones for each byte of `v` that is a digit of either case.
function __hex_valid(v: u8x16): u8x16 {
  let digit = __u8x16_lt(v - __u8x16_splat(48 as u8), __u8x16_splat(10 as u8));
  let letter = __u8x16_lt(__u8x16_or(v, __u8x16_splat(32 as u8)) - __u8x16_splat(97 as u8), __u8x16_splat(6 as u8));
  return __u8x16_or(digit, letter);
}

// The value of each digit of `v`. A byte that is not a digit gives a value
// that the caller does not use.
function __hex_nibbles(v: u8x16): u8x16 {
  let digit = v - __u8x16_splat(48 as u8);
  let letter = __u8x16_or(v, __u8x16_splat(32 as u8)) - __u8x16_splat(87 as u8);
  return __u8x16_select(digit, letter, __u8x16_lt(digit, __u8x16_splat(10 as u8)));
}
```
