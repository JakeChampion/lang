# UTF-8 code points and bytes

This module encodes, decodes, and validates UTF-8 code points.

## Model

UTF-8 helpers convert between encoded bytes and Unicode scalar values.
`decode_at(s, offset)` returns `Some((scalar, byte_width))` for a valid scalar at that byte offset.
The next byte position is `offset + byte_width`.
For example, `decode_at("Aé€", 3)` returns `Some((8364, 3))`, so the next position is 6.

## Contracts

Strict conversion rejects malformed UTF-8. Lossy conversion replaces invalid input. Byte positions and code-point counts measure different quantities.

The validator checks 16 bytes in one step, as lanes of `u8x16`. A step that finds an error stops, and a scalar check gives the offset of the first invalid sequence. The offset is the same as the offset of a check of one byte at a time.

## Executable example

```fern test name=std-utf8-example
import "std/utf8";

function main(): i32 {
  if (("Aé").codepoint_count() != 2 || !utf8.is_valid("Aé")) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub function decode_at(s: string, i: i32): Option[(i32, i32)]>>

<<3 pub function is_valid(s: string): boolean>>

<<4 pub function is_char_boundary(s: string, i: i32): boolean>>
```
### `dependencies 1`

```fern
<<dependencies 1>>=
// This module encodes, decodes, and validates UTF-8 code points.
import "core/cmp";
import "core/error";
import "std/string";
import "core/utf8_scalar";
```
### `2 pub function decode_at(s: string, i: i32): Option[(i32, i32)]`

```fern
<<2 pub function decode_at(s: string, i: i32): Option[(i32, i32)]>>=
pub function decode_at(s: string, i: i32): Option[(i32, i32)] {
  let n = s.len();
  if (i < 0 || i >= n) {
    return None;
  }
  let b0 = s[i] as i32;
  if (b0 < 128) {
    return Some((b0, 1));
  }
  if (b0 < 192) {
    return None;
  }
  if (b0 < 224) {
    if (i + 1 >= n) {
      return None;
    }
    let c1 = s[i + 1] as i32;
    if (c1 < 128 || c1 >= 192) {
      return None;
    }
    let cp2 = b0 - 192 << 6 | c1 - 128;
    if (cp2 < 128) {
      return None;
    }
    return Some((cp2, 2));
  }
  if (b0 < 240) {
    if (i + 2 >= n) {
      return None;
    }
    let d1 = s[i + 1] as i32;
    let d2 = s[i + 2] as i32;
    if (d1 < 128 || d1 >= 192) {
      return None;
    }
    if (d2 < 128 || d2 >= 192) {
      return None;
    }
    let cp3 = b0 - 224 << 12 | d1 - 128 << 6 | d2 - 128;
    if (cp3 < 2048) {
      return None;
    }
    if (cp3 >= 55296 && cp3 <= 57343) {
      return None;
    }
    return Some((cp3, 3));
  }
  if (b0 < 248) {
    if (i + 3 >= n) {
      return None;
    }
    let e1 = s[i + 1] as i32;
    let e2 = s[i + 2] as i32;
    let e3 = s[i + 3] as i32;
    if (e1 < 128 || e1 >= 192) {
      return None;
    }
    if (e2 < 128 || e2 >= 192) {
      return None;
    }
    if (e3 < 128 || e3 >= 192) {
      return None;
    }
    let cp4 = b0 - 240 << 18 | e1 - 128 << 12 | e2 - 128 << 6 | e3 - 128;
    if (cp4 < 65536) {
      return None;
    }
    if (cp4 > 1114111) {
      return None;
    }
    return Some((cp4, 4));
  }
  return None;
}

pub function char_from_i32(n: i32): Option[char] {
  if (n < 0 || n > 1114111) {
    return None;
  }
  if (n >= 55296 && n <= 57343) {
    return None;
  }
  return Some(n as char);
}

pub function encode(ch: char): string {
  return utf8_scalar.from_codepoint(ch);
}
```
### `3 pub function is_valid(s: string): boolean`

```fern
<<3 pub function is_valid(s: string): boolean>>=
pub function is_valid(s: string): boolean {
  return __valid_prefix(s) == s.len();
}

// The length of the longest valid prefix: the offset of the first invalid
// sequence, or the whole length.
function __valid_prefix(s: string): i32 {
  return __scalar_prefix(s, __lanes_prefix(s));
}

// Checks 16 bytes in each step, by the method of Keiser and Lemire,
// "Validating UTF-8 in less than one instruction per byte" (2021). It stops
// at the first step that finds an error, and answers the start of the last
// sequence before that step. Each error that the steps do not find starts
// there or later.
function __lanes_prefix(s: string): i32 {
  let b = s.as_bytes();
  let n = b.len();
  let at = 0;
  if (n < 16) {
    return 0;
  }
  // Each table gives the error classes that a nibble permits: an error is a
  // class that all three nibbles of a byte and the byte before it permit.
  let high1 = __u8x16_of(2 as u8, 2 as u8, 2 as u8, 2 as u8, 2 as u8, 2 as u8, 2 as u8, 2 as u8, 128 as u8, 128 as u8, 128 as u8, 128 as u8, 33 as u8, 1 as u8, 21 as u8, 73 as u8);
  let low1 = __u8x16_of(231 as u8, 163 as u8, 131 as u8, 131 as u8, 139 as u8, 203 as u8, 203 as u8, 203 as u8, 203 as u8, 203 as u8, 203 as u8, 203 as u8, 203 as u8, 219 as u8, 203 as u8, 203 as u8);
  let high2 = __u8x16_of(1 as u8, 1 as u8, 1 as u8, 1 as u8, 1 as u8, 1 as u8, 1 as u8, 1 as u8, 230 as u8, 174 as u8, 186 as u8, 186 as u8, 1 as u8, 1 as u8, 1 as u8, 1 as u8);
  // Lane k of `firstj` takes byte k - j of the first step, and 0 where that
  // is before the start.
  let first1 = __u8x16_of(255 as u8, 0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8);
  let first2 = __u8x16_of(255 as u8, 255 as u8, 0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8);
  let first3 = __u8x16_of(255 as u8, 255 as u8, 255 as u8, 0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8);
  let nibble = __u8x16_splat(15 as u8);
  let top = __u8x16_splat(128 as u8);
  let zero = __u8x16_splat(0 as u8);
  while (at + 16 <= n) {
    let cur = __u8x16_load_view(b, at);
    // Lane k of `pj` is the byte j places before lane k of `cur`.
    let p1 = zero;
    let p2 = zero;
    let p3 = zero;
    if (at > 0) {
      p1 = __u8x16_load_view(b, at - 1);
      p2 = __u8x16_load_view(b, at - 2);
      p3 = __u8x16_load_view(b, at - 3);
    } else {
      p1 = __u8x16_swizzle(cur, first1);
      p2 = __u8x16_swizzle(cur, first2);
      p3 = __u8x16_swizzle(cur, first3);
    }
    if (__u8x16_bitmask(__u8x16_or(cur, p3)) != 0) {
      let special = __u8x16_and(__u8x16_and(__u8x16_swizzle(high1, __u8x16_shr(p1, 4)), __u8x16_swizzle(low1, __u8x16_and(p1, nibble))), __u8x16_swizzle(high2, __u8x16_shr(cur, 4)));
      // A second continuation byte is correct only as the third or the
      // fourth byte of a sequence.
      let deep = __u8x16_and(__u8x16_or(__u8x16_sub_sat(p2, __u8x16_splat(96 as u8)), __u8x16_sub_sat(p3, __u8x16_splat(112 as u8))), top);
      if (__u8x16_bitmask(__u8x16_eq(__u8x16_xor(deep, special), zero)) != 65535) {
        return __sequence_start(b, at);
      }
      at += 16;
    } else {
      // The step is ASCII and no sequence continues into it. The next
      // step starts at the end of the run of ASCII bytes.
      at = __ascii_run(s, at + 16);
    }
  }
  return __sequence_start(b, at);
}

// The start of the last sequence before `at`, which can be incomplete, or
// `at` when the bytes before it end with an ASCII byte.
function __sequence_start(b: [u8], at: i32): i32 {
  let j = at - 1;
  while (j >= 0 && j >= at - 4) {
    let c = b[j] as i32;
    if (c >= 192) {
      return j;
    }
    if (c < 128) {
      return j + 1;
    }
    j -= 1;
  }
  return at;
}

// The length of the longest valid prefix of `s`. `from` is the start of a
// sequence, and the bytes before it are valid.
function __scalar_prefix(s: string, from: i32): i32 {
  let n = s.len();
  let i = from;
  while (i < n) {
    let b0 = s[i] as i32;
    if (b0 < 128) {
      i = __ascii_run(s, i);
      if (i >= n) {
        return n;
      }
      b0 = s[i] as i32;
    }
    if (b0 < 192) {
      return i;
    } else if (b0 < 224) {
      if (i + 1 >= n) {
        return i;
      }
      let c1 = s[i + 1] as i32;
      if (c1 < 128 || c1 >= 192) {
        return i;
      }
      if ((b0 - 192 << 6 | c1 - 128) < 128) {
        return i;
      }
      i += 2;
    } else if (b0 < 240) {
      if (i + 2 >= n) {
        return i;
      }
      let d1 = s[i + 1] as i32;
      let d2 = s[i + 2] as i32;
      if (d1 < 128 || d1 >= 192) {
        return i;
      }
      if (d2 < 128 || d2 >= 192) {
        return i;
      }
      let cp3 = b0 - 224 << 12 | d1 - 128 << 6 | d2 - 128;
      if (cp3 < 2048) {
        return i;
      }
      if (cp3 >= 55296 && cp3 <= 57343) {
        return i;
      }
      i += 3;
    } else if (b0 < 248) {
      if (i + 3 >= n) {
        return i;
      }
      let e1 = s[i + 1] as i32;
      let e2 = s[i + 2] as i32;
      let e3 = s[i + 3] as i32;
      if (e1 < 128 || e1 >= 192) {
        return i;
      }
      if (e2 < 128 || e2 >= 192) {
        return i;
      }
      if (e3 < 128 || e3 >= 192) {
        return i;
      }
      let cp4 = b0 - 240 << 18 | e1 - 128 << 12 | e2 - 128 << 6 | e3 - 128;
      if (cp4 < 65536) {
        return i;
      }
      if (cp4 > 1114111) {
        return i;
      }
      i += 4;
    } else {
      return i;
    }
  }
  return n;
}

// The byte offset of the first sequence that is not valid UTF-8.
pub struct Utf8Error { offset: i32 }

impl error.Error for Utf8Error {}

impl cmp.Display for Utf8Error {}

pub function (e: Utf8Error) message(): string {
  return "invalid UTF-8 at byte " + e.offset.to_string();
}

pub function (e: Utf8Error) to_string(): string {
  return e.message();
}

pub function from_bytes(b: [u8]): Result[string, Utf8Error] {
  let s = "";
  unsafe {
    s = string_from_bytes_unchecked(__bytes_owned(b));
  }
  let valid = __valid_prefix(s);
  if (valid == s.len()) {
    return Ok(s);
  }
  return Err(Utf8Error { offset: valid });
}

pub function from_bytes_lossy(b: [u8]): string {
  let s = "";
  unsafe {
    s = string_from_bytes_unchecked(__bytes_owned(b));
  }
  if (is_valid(s)) {
    return s;
  }
  return encode_all(s.chars());
}

pub function encode_all(cps: char[]): string {
  return utf8_scalar.from_codepoints(cps);
}

pub function codepoint_at(s: string, byte_index: i32): Option[char] {
  match (decode_at(s, byte_index)) {
    Some(pair) => {
      return Some(pair.0 as char);
    },
    None => {
      return None;
    }
  }
}

pub function char_at(s: string, index: i32): Option[string] {
  if (index < 0) {
    return None;
  }
  let n = s.len();
  let i = 0;
  let cp_idx = 0;
  while (i < n) {
    let start = i;
    i += utf8_scalar.step(s, i, n);
    if (cp_idx == index) {
      if (is_valid(slice_unchecked(s, start, i))) {
        return Some(slice_unchecked(s, start, i).to_owned());
      }
      return Some(encode(65533 as char));
    }
    cp_idx += 1;
  }
  return None;
}
```
### `4 pub function is_char_boundary(s: string, i: i32): boolean`

```fern
<<4 pub function is_char_boundary(s: string, i: i32): boolean>>=
pub function is_char_boundary(s: string, i: i32): boolean {
  let n = s.len();
  if (i < 0 || i > n) {
    return false;
  }
  if (i == 0 || i == n) {
    return true;
  }
  let b = s[i] as i32;
  return b < 128 || b >= 192;
}

pub function floor_char_boundary(s: string, i: i32): i32 {
  let n = s.len();
  if (i <= 0) {
    return 0;
  }
  if (i >= n) {
    return n;
  }
  let j = i;
  while (j > 0) {
    let b = s[j] as i32;
    if (b < 128 || b >= 192) {
      return j;
    }
    j -= 1;
  }
  return 0;
}

pub function ceil_char_boundary(s: string, i: i32): i32 {
  let n = s.len();
  if (i <= 0) {
    return 0;
  }
  if (i >= n) {
    return n;
  }
  let j = i;
  while (j < n) {
    let b = s[j] as i32;
    if (b < 128 || b >= 192) {
      return j;
    }
    j += 1;
  }
  return n;
}

pub function substring(s: string, start: i32, end: i32): string {
  let n = s.len();
  let lo = start;
  if (lo < 0) {
    lo = 0;
  }
  let i = 0;
  let cp_idx = 0;
  let byte_start = -1;
  let byte_end = n;
  while (i < n) {
    if (cp_idx == lo) {
      byte_start = i;
    }
    if (cp_idx == end) {
      byte_end = i;
    }
    i += utf8_scalar.step(s, i, n);
    cp_idx += 1;
  }
  if (cp_idx == lo) {
    byte_start = i;
  }
  if (cp_idx == end) {
    byte_end = i;
  }
  if (byte_start < 0) {
    return "";
  }
  if (byte_end < byte_start) {
    return "";
  }
  return slice_unchecked(s, byte_start, byte_end) + "";
}

pub function (s: string) chars(): char[] {
  let n = s.len();
  let out: char[] = [];
  let i = 0;
  while (i < n) {
    let step = utf8_scalar.step(s, i, n);
    out = out.append(utf8_scalar.decode_at(s, i, step) as char);
    i += step;
  }
  return out;
}
```
