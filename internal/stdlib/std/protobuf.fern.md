# Protocol Buffers wire fields

This module reads and writes Protocol Buffers fields without a schema.

## Model

A decoded field retains its number, wire type, integer word, and byte payload. The representation does not require a message schema.

## Contracts

Application code supplies the schema rules and validates expected field types. Varint, fixed-width, and byte fields retain distinct wire representations.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub const VARINT: i32>>

<<3 pub function decode(bytes: [u8]): Result[Field[], string]>>

<<4 function __value(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), string]>>

<<5 pub function varint(fields: Field[], number: i32): Option[i64]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module reads and writes Protocol Buffers fields without a schema.
import "std/string";
```

### `2 pub const VARINT: i32`

```fern
<<2 pub const VARINT: i32>>=
pub const VARINT: i32 = 0;

pub const FIXED64: i32 = 1;

pub const BYTES: i32 = 2;

pub const FIXED32: i32 = 5;

pub struct Field { number: i32, wire: i32, word: i64, bytes: u8[] }
```

### `3 pub function decode(bytes: [u8]): Result[Field[], string]`

```fern
<<3 pub function decode(bytes: [u8]): Result[Field[], string]>>=
pub function decode(bytes: [u8]): Result[Field[], string] {
  let out: Field[] = [];
  let at: i32 = 0;
  while (at < bytes.len()) {
    match (__varint(bytes, at)) {
      Ok(tag) => {
        at = tag.1;
        let number: i32 = (tag.0 >> 3) as i32;
        let wire: i32 = (tag.0 & 7 as i64) as i32;
        if (number <= 0) {
          return Err("field number 0 at byte " + at.to_string());
        }
        match (__value(bytes, at, number, wire)) {
          Ok(got) => {
            out = out.append(got.0);
            at = got.1;
          },
          Err(e) => {
            return Err(e);
          }
        }
      },
      Err(e) => {
        return Err(e);
      }
    }
  }
  return Ok(out);
}
```

### `4 function __value(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), string]`

```fern
<<4 function __value(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), string]>>=
function __value(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), string] {
  if (wire == VARINT) {
    match (__varint(bytes, at)) {
      Ok(v) => {
        let none: u8[] = [];
        return Ok((Field { number: number, wire: wire, word: v.0, bytes: none }, v.1));
      },
      Err(e) => {
        return Err(e);
      }
    }
  }
  if (wire == FIXED64 || wire == FIXED32) {
    return __fixed(bytes, at, number, wire);
  }
  if (wire == BYTES) {
    return __delimited(bytes, at, number);
  }
  if (wire == 3 || wire == 4) {
    return Err("field " + number.to_string() + " is a group");
  }
  return Err("field " + number.to_string() + " has wire type " + wire.to_string());
}

function __fixed(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), string] {
  let n: i32 = 8;
  if (wire == FIXED32) {
    n = 4;
  }
  if (at + n > bytes.len()) {
    return Err("field " + number.to_string() + " cut short");
  }
  let word: i64 = 0 as i64;
  let i: i32 = n - 1;
  while (i >= 0) {
    word = word << 8 | bytes[at + i] as i64;
    i = i - 1;
  }
  let none: u8[] = [];
  return Ok((Field { number: number, wire: wire, word: word, bytes: none }, at + n));
}

function __delimited(bytes: [u8], at: i32, number: i32): Result[(Field, i32), string] {
  match (__varint(bytes, at)) {
    Ok(len) => {
      let n: i32 = len.0 as i32;
      if (len.0 < 0 as i64 || n < 0 || len.1 + n > bytes.len()) {
        return Err("field " + number.to_string() + " length past the end");
      }
      let taken: u8[] = [];
      let i: i32 = len.1;
      while (i < len.1 + n) {
        taken = taken.append(bytes[i]);
        i = i + 1;
      }
      return Ok((Field { number: number, wire: BYTES, word: 0 as i64, bytes: taken }, len.1 + n));
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err("unreachable");
}

function __varint(bytes: [u8], at: i32): Result[(i64, i32), string] {
  let value: i64 = 0 as i64;
  let shift: i32 = 0;
  let i: i32 = at;
  while (i < bytes.len() && shift < 70) {
    let b: i32 = bytes[i] as i32;
    value = value | (b & 127) as i64 << shift;
    i = i + 1;
    if (b < 128) {
      return Ok((value, i));
    }
    shift = shift + 7;
  }
  if (shift >= 70) {
    return Err("varint over ten bytes at byte " + at.to_string());
  }
  return Err("varint cut short at byte " + at.to_string());
}
```

### `5 pub function varint(fields: Field[], number: i32): Option[i64]`

```fern
<<5 pub function varint(fields: Field[], number: i32): Option[i64]>>=
pub function varint(fields: Field[], number: i32): Option[i64] {
  let out: Option[i64] = None;
  for f in fields {
    if (f.number == number && f.wire == VARINT) {
      out = Some(f.word);
    }
  }
  return out;
}

pub function bytes(fields: Field[], number: i32): Option[u8[]] {
  let out: Option[u8[]] = None;
  for f in fields {
    if (f.number == number && f.wire == BYTES) {
      out = Some(f.bytes);
    }
  }
  return out;
}

pub function repeated(fields: Field[], number: i32): u8[][] {
  let out: u8[][] = [];
  for f in fields {
    if (f.number == number && f.wire == BYTES) {
      out = out.append(f.bytes);
    }
  }
  return out;
}

pub function varint_field(number: i32, value: i64): u8[] {
  return encode_varint(number as i64 << 3 | VARINT as i64).concat(encode_varint(value));
}

pub function bytes_field(number: i32, value: [u8]): u8[] {
  let out: u8[] = encode_varint(number as i64 << 3 | BYTES as i64).concat(encode_varint(value.len() as i64));
  for byte in value {
    out = out.append(byte);
  }
  return out;
}

pub function string_field(number: i32, value: string): u8[] {
  return bytes_field(number, value.as_bytes());
}

pub function encode_varint(value: i64): u8[] {
  let out: u8[] = [];
  let v: i64 = value;
  let left: i32 = 10;
  while (left > 0) {
    let low: i32 = (v & 127 as i64) as i32;
    v = v >> 7 & 144115188075855871 as i64;
    left = left - 1;
    if (v == 0 as i64 || left == 0) {
      out = out.append(low as u8);
      return out;
    }
    out = out.append((low | 128) as u8);
  }
  return out;
}

pub function zigzag(n: i64): i64 {
  return n << 1 ^ n >> 63;
}

pub function unzigzag(z: i64): i64 {
  return z >> 1 & 9223372036854775807 as i64 ^ 0 as i64 - (z & 1 as i64);
}
```
