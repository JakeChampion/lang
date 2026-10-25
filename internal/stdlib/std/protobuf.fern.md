# Protocol Buffers wire fields

This module reads and writes Protocol Buffers fields without a schema.

## Model

A decoded field retains its number, wire type, integer word, and byte payload. The representation does not require a message schema.

## Contracts

Application code supplies the schema rules and validates expected field types. Varint, fixed-width, and byte fields retain distinct wire representations. The decoder checks varint widths, field numbers, and lengths before shifts or index casts. Typed errors retain their byte offsets.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>
<<dependencies 1>>

<<2 pub const VARINT: i32>>

<<3 pub function decode(bytes: [u8]): Result[Field[], DecodeError]>>

<<4 function __value(bytes: [u8], at: i32, number: i32, wire: i32, tag_at: i32): Result[(Field, i32), DecodeError]>>

<<5 pub function varint(fields: Field[], number: i32): Option[i64]>>
<<typed decode errors>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
import "core/cmp";
import "std/error";
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

### `3 pub function decode(bytes: [u8]): Result[Field[], DecodeError]`

```fern
<<3 pub function decode(bytes: [u8]): Result[Field[], DecodeError]>>=
pub function decode(bytes: [u8]): Result[Field[], DecodeError] {
  let out: Field[] = [];
  let at: i32 = 0;
  while (at < bytes.len()) {
    let tag_at: i32 = at;
    match (__varint(bytes, at, 0)) {
      Ok(tag) => {
        at = tag.1;
        let raw_number: u64 = tag.0 as u64 >> 3;
        if (raw_number == 0 || raw_number > 536870911) {
          return Err(__error(InvalidFieldNumber(raw_number), tag_at, 0));
        }
        let number: i32 = raw_number as i32;
        let wire: i32 = (tag.0 & 7 as i64) as i32;
        match (__value(bytes, at, number, wire, tag_at)) {
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

### `4 function __value(bytes: [u8], at: i32, number: i32, wire: i32, tag_at: i32): Result[(Field, i32), DecodeError]`

```fern
<<4 function __value(bytes: [u8], at: i32, number: i32, wire: i32, tag_at: i32): Result[(Field, i32), DecodeError]>>=
function __value(bytes: [u8], at: i32, number: i32, wire: i32, tag_at: i32): Result[(Field, i32), DecodeError] {
  if (wire == VARINT) {
    match (__varint(bytes, at, number)) {
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
    return Err(__error(UnsupportedGroup(wire), tag_at, number));
  }
  return Err(__error(InvalidWireType(wire), tag_at, number));
}

function __fixed(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), DecodeError] {
  let n: i32 = 8;
  if (wire == FIXED32) {
    n = 4;
  }
  if (n > bytes.len() - at) {
    return Err(__error(FixedOutOfBounds(n, bytes.len() - at), at, number));
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

function __delimited(bytes: [u8], at: i32, number: i32): Result[(Field, i32), DecodeError] {
  match (__varint(bytes, at, number)) {
    Ok(len) => {
      let size: u64 = len.0 as u64;
      let remaining: i32 = bytes.len() - len.1;
      if (size > remaining as u64) {
        return Err(__error(LengthOutOfBounds(size, remaining), at, number));
      }
      let n: i32 = size as i32;
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
  return Err(__error(UnexpectedEnd, bytes.len(), number));
}

function __varint(bytes: [u8], at: i32, field: i32): Result[(i64, i32), DecodeError] {
  let value: i64 = 0 as i64;
  let shift: i32 = 0;
  let i: i32 = at;
  while (i < bytes.len()) {
    let b: i32 = bytes[i] as i32;
    if (shift == 63 && b > 1) {
      return Err(__error(VarintOverflow, i, field));
    }
    value = value | (b & 127) as i64 << shift;
    i = i + 1;
    if (b < 128) {
      return Ok((value, i));
    }
    shift = shift + 7;
  }
  return Err(__error(UnexpectedEnd, i, field));
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

### Typed decode errors

```fern
<<typed decode errors>>=
pub enum DecodeErrorKind {
  UnexpectedEnd,
  VarintOverflow,
  InvalidFieldNumber(u64),
  UnsupportedGroup(i32),
  InvalidWireType(i32),
  LengthOutOfBounds(u64, i32),
  FixedOutOfBounds(i32, i32),
}

function __error(kind: DecodeErrorKind, offset: i32, field: i32): DecodeError {
  return DecodeError { kind: kind, offset: offset, field: field };
}

pub function (e: DecodeError) message(): string {
  return e.to_string();
}

pub function (e: DecodeError) to_string(): string {
  let prefix: string = "byte " + e.offset.to_string() + ": ";
  if (e.field != 0) {
    prefix = prefix + "field " + e.field.to_string() + ": ";
  }
  match (e.kind) {
    UnexpectedEnd => {
      return prefix + "unexpected end of varint";
    },
    VarintOverflow => {
      return prefix + "varint exceeds 64 bits";
    },
    InvalidFieldNumber(n) => {
      return prefix + "invalid field number " + n.to_string();
    },
    UnsupportedGroup(w) => {
      return prefix + "unsupported group wire type " + w.to_string();
    },
    InvalidWireType(w) => {
      return prefix + "invalid wire type " + w.to_string();
    },
    LengthOutOfBounds(n, left) => {
      return prefix + "length " + n.to_string() + " exceeds " + left.to_string() + " remaining bytes";
    },
    FixedOutOfBounds(n, left) => {
      return prefix + "fixed value needs " + n.to_string() + " bytes, got " + left.to_string();
    }
  }
  return prefix;
}

impl cmp.Display for DecodeError {}

impl error.Error for DecodeError {}

pub struct DecodeError { kind: DecodeErrorKind, offset: i32, field: i32 }
```

### Module introduction

```fern
<<module introduction>>=
// Schema-independent Protocol Buffers fields with typed decode errors.
```
