# Base64 text and byte data

This module encodes and decodes Base64 data under RFC 4648.

## Model

Standard Base64 and URL-safe Base64 use separate alphabets. Encoders borrow byte views. Decoders return owned arrays and preserve arbitrary byte values. A decoder reads 16 characters in one step, as lanes, until a step finds a byte that is not in the alphabet.

## Contracts

Decode validates alphabet, length, pad bytes, and unused bits. The URL-safe decoder rejects characters from the standard alphabet when they differ.

## Executable example

```fern test name=std-base64-example
import "std/base64";
import "std/string";

function main(): i32 {
  if (base64.encode("foo".as_bytes()) != "Zm9v") { return 1; }
  let Ok(bytes) = base64.decode("Zm9v") else { return 2; };
  if (bytes.len() != 3 || bytes[2] != 111 as u8) { return 3; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __b64_alphabet(v: i32): i32>>

<<4 pub function encode(bs: [u8]): string>>

<<lanes>>

<<5 function __b64_std_to_url(s: string): string>>

<<6 pub function encode_url(input: [u8]): string>>

<<7 function __b64_decode(input: string, url: boolean): Result[u8[], codec.DecodeError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes and decodes Base64 data under RFC 4648.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/codec";
```

### `3 function __b64_alphabet(v: i32): i32`

```fern
<<3 function __b64_alphabet(v: i32): i32>>=
function __b64_alphabet(v: i32): i32 {
  if (v < 26) {
    return v + 65;
  }
  if (v < 52) {
    return v + 71;
  }
  if (v < 62) {
    return v - 4;
  }
  if (v == 62) {
    return 43;
  }
  return 47;
}

function __b64_decode_char(c: i32, url: boolean): i32 {
  if (c >= 65 && c < 91) {
    return c - 65;
  }
  if (c >= 97 && c < 123) {
    return c - 71;
  }
  if (c >= 48 && c < 58) {
    return c + 4;
  }
  if (c == (if (url) { 45 } else { 43 })) {
    return 62;
  }
  if (c == (if (url) { 95 } else { 47 })) {
    return 63;
  }
  return 0 - 1;
}
```

### `4 pub function encode(bs: [u8]): string`

```fern
<<4 pub function encode(bs: [u8]): string>>=
pub function encode(bs: [u8]): string {
  let n = bs.len();
  if (n == 0) {
    return "";
  }
  let out_len = (n + 2) / 3 * 4;
  let buf = __alloc_u8(out_len);
  let i = 0;
  let j = 0;
  let b0 = 0;
  let b1 = 0;
  let b2 = 0;
  if (n >= 16) {
    // __b64_lanes takes 12 bytes in each step while 16 bytes are left.
    let steps = (n - 4) / 12;
    buf = __b64_lanes(bs, buf);
    i = steps * 12;
    j = steps * 16;
  }
  while (i + 3 <= n) {
    b0 = bs[i] as i32;
    b1 = bs[i + 1] as i32;
    b2 = bs[i + 2] as i32;
    buf = buf.with(j, __b64_alphabet(b0 >> 2) as u8);
    buf = buf.with(j + 1, __b64_alphabet((b0 & 3) << 4 | b1 >> 4) as u8);
    buf = buf.with(j + 2, __b64_alphabet((b1 & 15) << 2 | b2 >> 6) as u8);
    buf = buf.with(j + 3, __b64_alphabet(b2 & 63) as u8);
    i += 3;
    j += 4;
  }
  let rem = n - i;
  if (rem == 1) {
    b0 = bs[i] as i32;
    buf = buf.with(j, __b64_alphabet(b0 >> 2) as u8);
    buf = buf.with(j + 1, __b64_alphabet((b0 & 3) << 4) as u8);
    buf = buf.with(j + 2, 61 as u8);
    buf = buf.with(j + 3, 61 as u8);
  } else if (rem == 2) {
    b0 = bs[i] as i32;
    b1 = bs[i + 1] as i32;
    buf = buf.with(j, __b64_alphabet(b0 >> 2) as u8);
    buf = buf.with(j + 1, __b64_alphabet((b0 & 3) << 4 | b1 >> 4) as u8);
    buf = buf.with(j + 2, __b64_alphabet((b1 & 15) << 2) as u8);
    buf = buf.with(j + 3, 61 as u8);
  }
  unsafe {
    return string_from_bytes_unchecked(buf);
  }
}
```

### `lanes`

One step reads 16 bytes, encodes the first 12 and writes 16 characters.
Each group of 4 lanes takes the 3 bytes of one group two times: `first` has
`b0 b0 b1 b2` and `second` has `0 b1 b2 0`, so each digit is one shift and
one mask of a lane. An index of 255 gives a lane of 0.

The character of a digit is the digit and an offset. A digit from 26 to 51
adds 71, a digit from 52 to 61 subtracts 4, 62 and 63 give `+` and `/`, and
a digit below 26 adds 65. The lanes wrap, so the offset 252 subtracts 4.

```fern
<<lanes>>=
function __b64_lanes(bs: [u8], own buf: u8[]): u8[] {
  let first = __u8x16_of(0 as u8, 0 as u8, 1 as u8, 2 as u8, 3 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 9 as u8, 10 as u8, 11 as u8);
  let second = __u8x16_of(255 as u8, 1 as u8, 2 as u8, 255 as u8, 255 as u8, 4 as u8, 5 as u8, 255 as u8, 255 as u8, 7 as u8, 8 as u8, 255 as u8, 255 as u8, 10 as u8, 11 as u8, 255 as u8);
  let offsets = __u8x16_of(71 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 252 as u8, 237 as u8, 240 as u8, 65 as u8, 0 as u8, 0 as u8);
  let m0 = __u8x16_of(63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8);
  let m1 = __u8x16_of(0 as u8, 3 as u8, 0 as u8, 0 as u8, 0 as u8, 3 as u8, 0 as u8, 0 as u8, 0 as u8, 3 as u8, 0 as u8, 0 as u8, 0 as u8, 3 as u8, 0 as u8, 0 as u8);
  let m2 = __u8x16_of(0 as u8, 0 as u8, 15 as u8, 0 as u8, 0 as u8, 0 as u8, 15 as u8, 0 as u8, 0 as u8, 0 as u8, 15 as u8, 0 as u8, 0 as u8, 0 as u8, 15 as u8, 0 as u8);
  let m3 = __u8x16_of(0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8);
  let m4 = __u8x16_of(0 as u8, 15 as u8, 0 as u8, 0 as u8, 0 as u8, 15 as u8, 0 as u8, 0 as u8, 0 as u8, 15 as u8, 0 as u8, 0 as u8, 0 as u8, 15 as u8, 0 as u8, 0 as u8);
  let m5 = __u8x16_of(0 as u8, 0 as u8, 3 as u8, 0 as u8, 0 as u8, 0 as u8, 3 as u8, 0 as u8, 0 as u8, 0 as u8, 3 as u8, 0 as u8, 0 as u8, 0 as u8, 3 as u8, 0 as u8);
  let k51 = __u8x16_splat(51 as u8);
  let k26 = __u8x16_splat(26 as u8);
  let k13 = __u8x16_splat(13 as u8);
  let i = 0;
  let j = 0;
  while (i + 16 <= bs.len()) {
    let v = __u8x16_load_view(bs, i);
    let a = __u8x16_swizzle(v, first);
    let b = __u8x16_swizzle(v, second);
    let digits = __u8x16_and(__u8x16_shr(a, 2), m0);
    digits = __u8x16_or(digits, __u8x16_shl(__u8x16_and(a, m1), 4));
    digits = __u8x16_or(digits, __u8x16_shl(__u8x16_and(a, m2), 2));
    digits = __u8x16_or(digits, __u8x16_and(a, m3));
    digits = __u8x16_or(digits, __u8x16_and(__u8x16_shr(b, 4), m4));
    digits = __u8x16_or(digits, __u8x16_and(__u8x16_shr(b, 6), m5));
    let high = __u8x16_sub_sat(digits, k51);
    let low = __u8x16_and(__u8x16_lt(digits, k26), k13);
    buf = __u8x16_with(buf, j, digits + __u8x16_swizzle(offsets, __u8x16_or(high, low)));
    i += 12;
    j += 16;
  }
  return buf;
}
```

### `5 function __b64_std_to_url(s: string): string`

```fern
<<5 function __b64_std_to_url(s: string): string>>=
function __b64_std_to_url(s: string): string {
  let out: u8[] = [];
  let i = 0;
  while (i < s.len()) {
    let c = s[i] as i32;
    if (c == 43) {
      out = out.append(45 as u8);
    } else if (c == 47) {
      out = out.append(95 as u8);
    } else if (c == 61) {} else {
      out = out.append(s[i] as u8);
    }
    i += 1;
  }
  unsafe {
    return string_from_bytes_unchecked(out);
  }
}
```

### `6 pub function encode_url(input: [u8]): string`

```fern
<<6 pub function encode_url(input: [u8]): string>>=
pub function encode_url(input: [u8]): string {
  return __b64_std_to_url(encode(input));
}

pub function decode(input: string): Result[u8[], codec.DecodeError] {
  return __b64_decode(input, false);
}

pub function decode_url(input: string): Result[u8[], codec.DecodeError] {
  return __b64_decode(input, true);
}
```

### `7 function __b64_decode(input: string, url: boolean): Result[u8[], codec.DecodeError]`

```fern
<<7 function __b64_decode(input: string, url: boolean): Result[u8[], codec.DecodeError]>>=
function __b64_decode(input: string, url: boolean): Result[u8[], codec.DecodeError] {
  let encoding = if (url) { "base64url" } else { "base64" };
  let output = __alloc_u8(input.len() / 4 * 3 + 2);
  let lanes = __b64_unlanes(input.as_bytes(), output, url);
  output = lanes.bytes;
  let first = lanes.at;
  let n = first;
  while (n < input.len() && input[n] != 61) {
    if (__b64_decode_char(input[n] as i32, url) < 0) {
      return Err(codec.error(encoding, n, codec.InvalidByte(input[n])));
    }
    n += 1;
  }
  if let Some(problem) = codec.padding_error(input, n, 2, 4, encoding) {
    return Err(problem);
  }
  let rem = n % 4;
  if (rem == 1) {
    return Err(codec.error(encoding, n, codec.InvalidLength));
  }
  let trailing = rem * 6 % 8;
  if (trailing > 0 && (__b64_decode_char(input[n - 1] as i32, url) & (1 << trailing) - 1) != 0) {
    return Err(codec.error(encoding, n - 1, codec.NonZeroTrailingBits));
  }
  let at = first / 4 * 3;
  let acc = 0;
  let bits = 0;
  for i in first..n {
    acc = acc << 6 | __b64_decode_char(input[i] as i32, url);
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      output = output.with(at, (acc >> bits & 255) as u8);
      at += 1;
    }
  }
  return Ok(output.truncate(n / 4 * 3 + rem * 6 / 8));
}

struct __B64Decoded { bytes: u8[], at: i32 }

// Writes the bytes of each group of 16 characters of `chars` to `bytes`, up
// to the first group with a byte that is not in the alphabet. 16 characters
// give 12 bytes. A step writes 16 bytes, so it runs only where they fit, and
// the next step writes over the last 4. `at` is the index of the first
// character that it does not decode, a multiple of 4.
function __b64_unlanes(chars: [u8], own bytes: u8[], url: boolean): __B64Decoded {
  let none = 255 as u8;
  // Output byte 3k + r takes sextets r and r + 1 of group k.
  let first = __u8x16_of(0 as u8, 1 as u8, 2 as u8, 4 as u8, 5 as u8, 6 as u8, 8 as u8, 9 as u8, 10 as u8, 12 as u8, 13 as u8, 14 as u8, none, none, none, none);
  let second = __u8x16_of(1 as u8, 2 as u8, 3 as u8, 5 as u8, 6 as u8, 7 as u8, 9 as u8, 10 as u8, 11 as u8, 13 as u8, 14 as u8, 15 as u8, none, none, none, none);
  let r0 = __u8x16_of(none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8);
  let r1 = __u8x16_of(0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8);
  let r2 = __u8x16_of(0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, none, 0 as u8, 0 as u8, 0 as u8, 0 as u8);
  let plus = if (url) { 45 as u8 } else { 43 as u8 };
  let slash = if (url) { 95 as u8 } else { 47 as u8 };
  let i = 0;
  while (i + 16 <= chars.len() && i / 4 * 3 + 16 <= bytes.len()) {
    let v = __u8x16_load_view(chars, i);
    if (__u8x16_bitmask(__b64_valid(v, plus, slash)) != 65535) {
      return __B64Decoded { bytes: bytes, at: i };
    }
    let six = __b64_sextets(v, plus, slash);
    let a = __u8x16_swizzle(six, first);
    let b = __u8x16_swizzle(six, second);
    let left = __u8x16_or(__u8x16_or(__u8x16_and(__u8x16_shl(a, 2), r0), __u8x16_and(__u8x16_shl(a, 4), r1)), __u8x16_and(__u8x16_shl(a, 6), r2));
    let right = __u8x16_or(__u8x16_or(__u8x16_and(__u8x16_shr(b, 4), r0), __u8x16_and(__u8x16_shr(b, 2), r1)), __u8x16_and(b, r2));
    bytes = __u8x16_with(bytes, i / 4 * 3, __u8x16_or(left, right));
    i += 16;
  }
  return __B64Decoded { bytes: bytes, at: i };
}

// A lane of ones for each byte of `v` that is in the alphabet with the two
// characters `plus` and `slash`.
function __b64_valid(v: u8x16, plus: u8, slash: u8): u8x16 {
  let upper = __u8x16_lt(v - __u8x16_splat(65 as u8), __u8x16_splat(26 as u8));
  let lower = __u8x16_lt(v - __u8x16_splat(97 as u8), __u8x16_splat(26 as u8));
  let digit = __u8x16_lt(v - __u8x16_splat(48 as u8), __u8x16_splat(10 as u8));
  let extra = __u8x16_or(__u8x16_eq(v, __u8x16_splat(plus)), __u8x16_eq(v, __u8x16_splat(slash)));
  return __u8x16_or(__u8x16_or(upper, lower), __u8x16_or(digit, extra));
}

// The 6-bit value of each character of `v`. A byte that is not in the
// alphabet gives a value that the caller does not use.
function __b64_sextets(v: u8x16, plus: u8, slash: u8): u8x16 {
  let upper = v - __u8x16_splat(65 as u8);
  let lower = v - __u8x16_splat(71 as u8);
  let digit = v + __u8x16_splat(4 as u8);
  let value = __u8x16_select(upper, lower, __u8x16_lt(upper, __u8x16_splat(26 as u8)));
  value = __u8x16_select(digit, value, __u8x16_lt(v - __u8x16_splat(48 as u8), __u8x16_splat(10 as u8)));
  value = __u8x16_select(__u8x16_splat(62 as u8), value, __u8x16_eq(v, __u8x16_splat(plus)));
  return __u8x16_select(__u8x16_splat(63 as u8), value, __u8x16_eq(v, __u8x16_splat(slash)));
}
```
