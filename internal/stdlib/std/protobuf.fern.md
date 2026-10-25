# Protocol Buffers wire fields

This module reads and writes Protocol Buffers fields without a schema.

## Model

A decoded field retains its number, wire type, integer word, and byte payload. The representation does not require a message schema.

## Contracts

Application code supplies the schema rules and validates expected field types. Varint, fixed-width, and byte fields retain distinct wire representations. A malformed message is a `ProtobufError` that holds the byte offset where the bad element starts.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub const VARINT: i32>>

<<3 pub function decode(bytes: [u8]): Result[Field[], ProtobufError]>>

<<4 function __value(bytes: [u8], tag_at: i32, at: i32, number: i32, wire: i32): Result[(Field, i32), ProtobufError]>>

<<5 pub function varint(fields: Field[], number: i32): Option[i64]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module reads and writes Protocol Buffers fields without a schema.
import "std/string";
import "std/error";
import "core/cmp";
```

### `2 pub const VARINT: i32`

```fern
<<2 pub const VARINT: i32>>=
pub const VARINT: i32 = 0;

pub const FIXED64: i32 = 1;

pub const BYTES: i32 = 2;

pub const FIXED32: i32 = 5;

pub struct Field { number: i32, wire: i32, word: i64, bytes: u8[] }

@derive(cmp.Eq)
pub enum ProtobufErrorKind {
  VarintOverflow,
  Truncated,
  FieldNumberOutOfRange,
  GroupWireType,
  UnknownWireType(i32),
}

// field is 0 when the error is in a tag.
@derive(cmp.Eq)
pub struct ProtobufError { offset: i32, field: i32, kind: ProtobufErrorKind }

impl error.Error for ProtobufError {}

impl cmp.Display for ProtobufError {}

pub function (e: ProtobufError) message(): string {
  let at: string = " at byte " + e.offset.to_string();
  let field: string = "field " + e.field.to_string();
  match (e.kind) {
    VarintOverflow => {
      return "protobuf: varint over 64 bits" + at;
    },
    Truncated => {
      if (e.field == 0) {
        return "protobuf: input ends inside a tag" + at;
      }
      return "protobuf: input ends inside " + field + at;
    },
    FieldNumberOutOfRange => {
      return "protobuf: field number out of range" + at;
    },
    GroupWireType => {
      return "protobuf: " + field + " is a group" + at;
    },
    UnknownWireType(wire) => {
      return "protobuf: " + field + " has wire type " + wire.to_string() + at;
    }
  }
  return "";
}

pub function (e: ProtobufError) to_string(): string {
  return e.message();
}
```

### `3 pub function decode(bytes: [u8]): Result[Field[], ProtobufError]`

```fern
<<3 pub function decode(bytes: [u8]): Result[Field[], ProtobufError]>>=
pub function decode(bytes: [u8]): Result[Field[], ProtobufError] {
  let out: Field[] = [];
  let at: i32 = 0;
  while (at < bytes.len()) {
    match (__varint(bytes, at, 0)) {
      Ok(tag) => {
        let field: i64 = tag.0 >> 3;
        if (field < 1 as i64 || field > 536870911 as i64) {
          return Err(ProtobufError { offset: at, field: 0, kind: FieldNumberOutOfRange });
        }
        let number: i32 = field as i32;
        let wire: i32 = (tag.0 & 7 as i64) as i32;
        match (__value(bytes, at, tag.1, number, wire)) {
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

### `4 function __value(bytes: [u8], tag_at: i32, at: i32, number: i32, wire: i32): Result[(Field, i32), ProtobufError]`

```fern
<<4 function __value(bytes: [u8], tag_at: i32, at: i32, number: i32, wire: i32): Result[(Field, i32), ProtobufError]>>=
function __value(bytes: [u8], tag_at: i32, at: i32, number: i32, wire: i32): Result[(Field, i32), ProtobufError] {
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
    return Err(ProtobufError { offset: tag_at, field: number, kind: GroupWireType });
  }
  return Err(ProtobufError { offset: tag_at, field: number, kind: UnknownWireType(wire) });
}

function __fixed(bytes: [u8], at: i32, number: i32, wire: i32): Result[(Field, i32), ProtobufError] {
  let n: i32 = 8;
  if (wire == FIXED32) {
    n = 4;
  }
  if (at + n > bytes.len()) {
    return Err(ProtobufError { offset: at, field: number, kind: Truncated });
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

function __delimited(bytes: [u8], at: i32, number: i32): Result[(Field, i32), ProtobufError] {
  match (__varint(bytes, at, number)) {
    Ok(len) => {
      if (len.0 < 0 as i64 || len.0 > (bytes.len() - len.1) as i64) {
        return Err(ProtobufError { offset: at, field: number, kind: Truncated });
      }
      let n: i32 = len.0 as i32;
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
  return Err(ProtobufError { offset: at, field: number, kind: Truncated });
}

// The tenth byte holds bit 63 only, so a value above 1 there overflows 64 bits.
function __varint(bytes: [u8], at: i32, field: i32): Result[(i64, i32), ProtobufError] {
  let value: i64 = 0 as i64;
  let shift: i32 = 0;
  let i: i32 = at;
  while (i < bytes.len()) {
    let b: i32 = bytes[i] as i32;
    if (shift == 63 && b > 1) {
      return Err(ProtobufError { offset: at, field: field, kind: VarintOverflow });
    }
    value = value | (b & 127) as i64 << shift;
    i = i + 1;
    if (b < 128) {
      return Ok((value, i));
    }
    shift = shift + 7;
  }
  return Err(ProtobufError { offset: at, field: field, kind: Truncated });
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
