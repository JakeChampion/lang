# UUID values and text

This module generates and validates UUIDs.

## Model

The module creates version 4 and version 7 UUID text and validates the standard textual form. The nil UUID has a named constant.

## Contracts

Parse errors retain their byte offset and kind. Random generation has host effects, while validation can operate on supplied text.

## Executable example

```fern test name=std-uuid-example
import "std/uuid";

function main(): i32 {
  if (!uuid.is_nil(uuid.NIL) || uuid.is_nil("00000000-0000-0000-0000-000000000001")) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub enum ParseErrorKind>>

<<3 pub function (e: ParseError) message(): string>>

<<4 function canonical(b: u8[]): string>>

<<5 pub function v4(): string>>

<<6 pub const NIL: string>>

<<7 pub function is_nil(s: string): boolean>>

<<8 function __problem(s: string): Option[ParseError]>>

<<9 pub function version(s: string): Result[i32, ParseError]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module generates and validates UUIDs.
import "std/hex";
import "std/i32";
import "std/error";
import "core/cmp";
```

### `2 pub enum ParseErrorKind`

```fern
<<2 pub enum ParseErrorKind>>=
@derive(cmp.Eq)
pub enum ParseErrorKind { InvalidLength, InvalidSeparator, InvalidByte(u8) }

@derive(cmp.Eq)
pub struct ParseError { offset: i32, kind: ParseErrorKind }

impl error.Error for ParseError {}

impl cmp.Display for ParseError {}
```

### `3 pub function (e: ParseError) message(): string`

```fern
<<3 pub function (e: ParseError) message(): string>>=
pub function (e: ParseError) message(): string {
  return e.to_string();
}

pub function (e: ParseError) to_string(): string {
  let reason: string = "expected 36 bytes";
  match (e.kind) {
    ParseErrorKind.InvalidLength => {},
    ParseErrorKind.InvalidSeparator => {
      reason = "expected a hyphen";
    },
    ParseErrorKind.InvalidByte(byte) => {
      reason = "invalid hexadecimal byte " + (byte as i32).to_string();
    }
  }
  return "uuid byte " + e.offset.to_string() + ": " + reason;
}
```

### `4 function canonical(b: u8[]): string`

```fern
<<4 function canonical(b: u8[]): string>>=
function canonical(b: u8[]): string {
  let h: string = hex.encode(b);
  return slice_unchecked(h, 0, 8) + "-" + slice_unchecked(h, 8, 12) + "-" + slice_unchecked(h, 12, 16)
    + "-" + slice_unchecked(h, 16, 20) + "-" + slice_unchecked(h, 20, 32);
}
```

### `5 pub function v4(): string`

```fern
<<5 pub function v4(): string>>=
pub function v4(): string {
  let b: u8[] = random_bytes(16);
  b = b.with(6, b[6] & 15 | 64);
  b = b.with(8, b[8] & 63 | 128);
  return canonical(b);
}

pub function v7(): string {
  let ms: i64 = now_unix_ms();
  let r: u8[] = random_bytes(10);
  let b: u8[] = [];
  let shift: i64 = 40;
  while (shift >= 0) {
    b = b.append((ms >> shift & 255) as u8);
    shift = shift - 8;
  }
  b = b.append(r[0] & 15 | 112);
  b = b.append(r[1]);
  b = b.append(r[2] & 63 | 128);
  let i: i32 = 3;
  while (i < 10) {
    b = b.append(r[i]);
    i = i + 1;
  }
  return canonical(b);
}
```

### `6 pub const NIL: string`

```fern
<<6 pub const NIL: string>>=
pub const NIL: string = "00000000-0000-0000-0000-000000000000";
```

### `7 pub function is_nil(s: string): boolean`

```fern
<<7 pub function is_nil(s: string): boolean>>=
pub function is_nil(s: string): boolean {
  return s == NIL;
}

pub function is_valid(s: string): boolean {
  match (__problem(s)) {
    None => {
      return true;
    },
    Some(_) => {
      return false;
    }
  }
}
```

### `8 function __problem(s: string): Option[ParseError]`

```fern
<<8 function __problem(s: string): Option[ParseError]>>=
function __problem(s: string): Option[ParseError] {
  if (s.len() != 36) {
    let offset: i32 = if (s.len() < 36) { s.len() } else { 36 };
    return Some(ParseError { offset: offset, kind: ParseErrorKind.InvalidLength });
  }
  for i in 0..36 {
    if (i == 8 || i == 13 || i == 18 || i == 23) {
      if (s[i] != 45) {
        return Some(ParseError { offset: i, kind: ParseErrorKind.InvalidSeparator });
      }
    } else if (!s[i].is_ascii_hex_digit()) {
      return Some(ParseError { offset: i, kind: ParseErrorKind.InvalidByte(s[i]) });
    }
  }
  return None;
}
```

### `9 pub function version(s: string): Result[i32, ParseError]`

```fern
<<9 pub function version(s: string): Result[i32, ParseError]>>=
// version checks the UUID format and returns its version nibble.
// It accepts uppercase and lowercase hexadecimal digits, with no restriction on version or variant nibbles.
pub function version(s: string): Result[i32, ParseError] {
  if let Some(problem) = __problem(s) {
    return Err(problem);
  }
  let byte: i32 = s[14] as i32;
  if (byte <= 57) {
    return Ok(byte - 48);
  }
  if (byte <= 70) {
    return Ok(byte - 55);
  }
  return Ok(byte - 87);
}
```
