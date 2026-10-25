# Unicode scalar encoding kernels

This internal module provides the scalar encoder shared by string and UTF-8 functions.

## Contracts

Invalid unchecked scalar values become U+FFFD. A valid scalar emits one to four bytes. The byte count and write helper share the same scalar representation.

## Executable example

```fern test name=core-utf8-scalar-example
import "std/utf8";
function main(): i32 { if (utf8.encode(233 as char) != "é") { return 1; } return 0; }
```

## Module assembly

The root chunk preserves declaration order.

```fern
<<*>>=
<<1 pub(package) function encoded_len(c: i32): i32>>

<<2 pub(package) function sanitise(c: i32): i32>>

<<3 pub(package) function put(buf_in: u8[], pos: i32, c: i32): u8[]>>

<<4 pub(package) function from_codepoint(ch: char): string>>
```

### `1 pub(package) function encoded_len(c: i32): i32`

```fern
<<1 pub(package) function encoded_len(c: i32): i32>>=
// Shared Unicode scalar encoding.

pub(package) function encoded_len(c: i32): i32 {
  if (c < 128) {
    return 1;
  }
  if (c < 2048) {
    return 2;
  }
  if (c < 65536) {
    return 3;
  }
  return 4;
}

// `sanitise(c)` — map a value that is negative, past U+10FFFF, or a
// UTF-16 surrogate to U+FFFD; pass every other scalar through. Keeps the
// encoders total, mirroring the decoder's substitution policy.
```

### `2 pub(package) function sanitise(c: i32): i32`

```fern
<<2 pub(package) function sanitise(c: i32): i32>>=
pub(package) function sanitise(c: i32): i32 {
  if (c < 0 || c > 1114111 || c >= 55296 && c <= 57343) {
    return 65533;
  }
  return c;
}

// `put(buf, pos, c)` — write the UTF-8 bytes of sanitised codepoint
// `c` at `pos`, returning the (possibly grown) buffer. Caller sizes `buf`
// via `encoded_len` and advances `pos` by the same.
```

### `3 pub(package) function put(buf_in: u8[], pos: i32, c: i32): u8[]`

```fern
<<3 pub(package) function put(buf_in: u8[], pos: i32, c: i32): u8[]>>=
pub(package) function put(buf_in: u8[], pos: i32, c: i32): u8[] {
  let buf: u8[] = buf_in;
  if (c < 128) {
    buf = buf.with(pos, c as u8);
  } else if (c < 2048) {
    buf = buf.with(pos, (192 | c >> 6) as u8);
    buf = buf.with(pos + 1, (128 | c & 63) as u8);
  } else if (c < 65536) {
    buf = buf.with(pos, (224 | c >> 12) as u8);
    buf = buf.with(pos + 1, (128 | c >> 6 & 63) as u8);
    buf = buf.with(pos + 2, (128 | c & 63) as u8);
  } else {
    buf = buf.with(pos, (240 | c >> 18) as u8);
    buf = buf.with(pos + 1, (128 | c >> 12 & 63) as u8);
    buf = buf.with(pos + 2, (128 | c >> 6 & 63) as u8);
    buf = buf.with(pos + 3, (128 | c & 63) as u8);
  }
  return buf;
}

// `from_codepoint(cp)` — the single-codepoint UTF-8 string for
// scalar `cp` (out-of-range / surrogate → U+FFFD). The inverse of indexing
// `chars()`; the way to build non-ASCII text from a numeric scalar.
```

### `4 pub(package) function from_codepoint(ch: char): string`

```fern
<<4 pub(package) function from_codepoint(ch: char): string>>=
pub(package) function from_codepoint(ch: char): string {
  let c: i32 = ch as i32;
  if (c < 0 || c > 1114111 || c >= 55296 && c <= 57343) {  // U+FFFD
    c = 65533;
  }
  if (c < 128) {
    let one: u8[] = [c as u8];
    return string_from_bytes_unchecked(one);
  }
  if (c < 2048) {
    let two: u8[] = [(192 + (c >> 6)) as u8, (128 + (c & 63)) as u8];
    return string_from_bytes_unchecked(two);
  }
  if (c < 65536) {
    let three: u8[] = [(224 + (c >> 12)) as u8, (128 + (c >> 6 & 63)) as u8, (128 + (c & 63)) as u8];
    return string_from_bytes_unchecked(three);
  }
  let four: u8[] = [(240 + (c >> 18)) as u8, (128 + (c >> 12 & 63)) as u8, (128 + (c >> 6 & 63)) as u8, (128 + (c & 63)) as u8];
  return string_from_bytes_unchecked(four);
}

// Encode scalars into one buffer whose size is known before writing.
pub(package) function from_codepoints(cps: char[]): string {
  let m: i32 = cps.len();
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < m) {
    total = total + encoded_len(sanitise(cps[i] as i32));
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < m) {
    let c: i32 = sanitise(cps[i] as i32);
    buf = put(buf, pos, c);
    pos = pos + encoded_len(c);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

// Maximal-subpart decoding shared by byte-safe text operations and UTF-8.
pub(package) function is_cont(b: i32): boolean {
  return b >= 128 && b < 192;
}

pub(package) function expect(b0: i32): i32 {
  if (b0 < 128) {
    return 1;
  }
  if (b0 < 194) {
    return 0;
  }
  if (b0 < 224) {
    return 2;
  }
  if (b0 < 240) {
    return 3;
  }
  if (b0 < 245) {
    return 4;
  }
  return 0;
}

pub(package) function cont_lo(b0: i32): i32 {
  if (b0 == 224) {
    return 160;
  }
  if (b0 == 240) {
    return 144;
  }
  return 128;
}

pub(package) function cont_hi(b0: i32): i32 {
  if (b0 == 237) {
    return 159;
  }
  if (b0 == 244) {
    return 143;
  }
  return 191;
}

pub(package) function step(s: string, i: i32, n: i32): i32 {
  let b0: i32 = s[i] as i32;
  let want: i32 = expect(b0);
  if (want <= 1) {
    return 1;
  }
  if (i + 1 >= n) {
    return 1;
  }
  let c1: i32 = s[i + 1] as i32;
  if (c1 < cont_lo(b0) || c1 > cont_hi(b0)) {
    return 1;
  }
  let k: i32 = 2;
  while (k < want) {
    if (i + k >= n) {
      return k;
    }
    if (!is_cont(s[i + k] as i32)) {
      return k;
    }
    k = k + 1;
  }
  return want;
}

pub(package) function prev_unit(s: string, end: i32): i32 {
  let start: i32 = end - 1;
  while (start > 0 && end - start < 4 && is_cont(s[start] as i32)) {
    start = start - 1;
  }
  if (start + step(s, start, end) == end) {
    return start;
  }
  return end - 1;
}

pub(package) function decode_at(s: string, i: i32, step: i32): i32 {
  let b0: i32 = s[i] as i32;
  if (b0 < 128) {
    return b0;
  }
  if (step != expect(b0)) {
    return 65533;
  }
  if (step == 2) {
    return (b0 & 31) << 6 | s[i + 1] as i32 & 63;
  }
  if (step == 3) {
    return (b0 & 15) << 12 | (s[i + 1] as i32 & 63) << 6 | s[i + 2] as i32 & 63;
  }
  return (b0 & 7) << 18 | (s[i + 1] as i32 & 63) << 12 | (s[i + 2] as i32 & 63) << 6 | s[i + 3] as i32 & 63;
}
```
