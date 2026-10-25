# UTF-8 code points and bytes

This module encodes, decodes, and validates UTF-8 code points.

## Model

UTF-8 helpers convert between encoded bytes and Unicode scalar values. A decode-at operation returns both a scalar and the next byte position.

## Contracts

Strict conversion rejects malformed UTF-8. Lossy conversion replaces invalid input. Byte positions and code-point counts measure different quantities.

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

// This module encodes, decodes, and validates UTF-8 code points.
import "std/string";
import "core/utf8_scalar";
```
### `2 pub function decode_at(s: string, i: i32): Option[(i32, i32)]`

```fern
<<2 pub function decode_at(s: string, i: i32): Option[(i32, i32)]>>=
pub function decode_at(s: string, i: i32): Option[(i32, i32)] {
  let n: i32 = s.len();
  if (i < 0 || i >= n) {
    return None;
  }
  let b0: i32 = s[i] as i32;
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
    let c1: i32 = s[i + 1] as i32;
    if (c1 < 128 || c1 >= 192) {
      return None;
    }
    let cp2: i32 = b0 - 192 << 6 | c1 - 128;
    if (cp2 < 128) {
      return None;
    }
    return Some((cp2, 2));
  }
  if (b0 < 240) {
    if (i + 2 >= n) {
      return None;
    }
    let d1: i32 = s[i + 1] as i32;
    let d2: i32 = s[i + 2] as i32;
    if (d1 < 128 || d1 >= 192) {
      return None;
    }
    if (d2 < 128 || d2 >= 192) {
      return None;
    }
    let cp3: i32 = b0 - 224 << 12 | d1 - 128 << 6 | d2 - 128;
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
    let e1: i32 = s[i + 1] as i32;
    let e2: i32 = s[i + 2] as i32;
    let e3: i32 = s[i + 3] as i32;
    if (e1 < 128 || e1 >= 192) {
      return None;
    }
    if (e2 < 128 || e2 >= 192) {
      return None;
    }
    if (e3 < 128 || e3 >= 192) {
      return None;
    }
    let cp4: i32 = b0 - 240 << 18 | e1 - 128 << 12 | e2 - 128 << 6 | e3 - 128;
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
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n) {
    let b0: i32 = s[i] as i32;
    if (b0 < 128) {
      i = __ascii_run(s, i);
      if (i >= n) {
        return true;
      }
      b0 = s[i] as i32;
    }
    if (b0 < 192) {
      return false;
    } else if (b0 < 224) {
      if (i + 1 >= n) {
        return false;
      }
      let c1: i32 = s[i + 1] as i32;
      if (c1 < 128 || c1 >= 192) {
        return false;
      }
      if ((b0 - 192 << 6 | c1 - 128) < 128) {
        return false;
      }
      i = i + 2;
    } else if (b0 < 240) {
      if (i + 2 >= n) {
        return false;
      }
      let d1: i32 = s[i + 1] as i32;
      let d2: i32 = s[i + 2] as i32;
      if (d1 < 128 || d1 >= 192) {
        return false;
      }
      if (d2 < 128 || d2 >= 192) {
        return false;
      }
      let cp3: i32 = b0 - 224 << 12 | d1 - 128 << 6 | d2 - 128;
      if (cp3 < 2048) {
        return false;
      }
      if (cp3 >= 55296 && cp3 <= 57343) {
        return false;
      }
      i = i + 3;
    } else if (b0 < 248) {
      if (i + 3 >= n) {
        return false;
      }
      let e1: i32 = s[i + 1] as i32;
      let e2: i32 = s[i + 2] as i32;
      let e3: i32 = s[i + 3] as i32;
      if (e1 < 128 || e1 >= 192) {
        return false;
      }
      if (e2 < 128 || e2 >= 192) {
        return false;
      }
      if (e3 < 128 || e3 >= 192) {
        return false;
      }
      let cp4: i32 = b0 - 240 << 18 | e1 - 128 << 12 | e2 - 128 << 6 | e3 - 128;
      if (cp4 < 65536) {
        return false;
      }
      if (cp4 > 1114111) {
        return false;
      }
      i = i + 4;
    } else {
      return false;
    }
  }
  return true;
}

pub function from_bytes(b: [u8]): Option[string] {
  let s: string = string_from_bytes_unchecked(__bytes_owned(b));
  if (is_valid(s)) {
    return Some(s);
  }
  return None;
}

pub function from_bytes_lossy(b: [u8]): string {
  let s: string = string_from_bytes_unchecked(__bytes_owned(b));
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
  let n: i32 = s.len();
  let i: i32 = 0;
  let cp_idx: i32 = 0;
  while (i < n) {
    let start: i32 = i;
    i = i + utf8_scalar.step(s, i, n);
    if (cp_idx == index) {
      if (is_valid(slice_unchecked(s, start, i))) {
        return Some(slice_unchecked(s, start, i).to_owned());
      }
      return Some(encode(65533 as char));
    }
    cp_idx = cp_idx + 1;
  }
  return None;
}
```
### `4 pub function is_char_boundary(s: string, i: i32): boolean`

```fern
<<4 pub function is_char_boundary(s: string, i: i32): boolean>>=
pub function is_char_boundary(s: string, i: i32): boolean {
  let n: i32 = s.len();
  if (i < 0 || i > n) {
    return false;
  }
  if (i == 0 || i == n) {
    return true;
  }
  let b: i32 = s[i] as i32;
  return b < 128 || b >= 192;
}

pub function floor_char_boundary(s: string, i: i32): i32 {
  let n: i32 = s.len();
  if (i <= 0) {
    return 0;
  }
  if (i >= n) {
    return n;
  }
  let j: i32 = i;
  while (j > 0) {
    let b: i32 = s[j] as i32;
    if (b < 128 || b >= 192) {
      return j;
    }
    j = j - 1;
  }
  return 0;
}

pub function ceil_char_boundary(s: string, i: i32): i32 {
  let n: i32 = s.len();
  if (i <= 0) {
    return 0;
  }
  if (i >= n) {
    return n;
  }
  let j: i32 = i;
  while (j < n) {
    let b: i32 = s[j] as i32;
    if (b < 128 || b >= 192) {
      return j;
    }
    j = j + 1;
  }
  return n;
}

pub function substring(s: string, start: i32, end: i32): string {
  let n: i32 = s.len();
  let lo: i32 = start;
  if (lo < 0) {
    lo = 0;
  }
  let i: i32 = 0;
  let cp_idx: i32 = 0;
  let byte_start: i32 = 0 - 1;
  let byte_end: i32 = n;
  while (i < n) {
    if (cp_idx == lo) {
      byte_start = i;
    }
    if (cp_idx == end) {
      byte_end = i;
    }
    i = i + utf8_scalar.step(s, i, n);
    cp_idx = cp_idx + 1;
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
  let n: i32 = s.len();
  let out: char[] = [];
  let i: i32 = 0;
  while (i < n) {
    let step: i32 = utf8_scalar.step(s, i, n);
    out = out.append(utf8_scalar.decode_at(s, i, step) as char);
    i = i + step;
  }
  return out;
}

pub function (s: string) codepoint_count(): i32 {
  let n: i32 = s.len();
  let count: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    i = i + utf8_scalar.step(s, i, n);
    count = count + 1;
  }
  return count;
}
```