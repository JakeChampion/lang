# Shared codec errors

This module defines shared errors for byte encodings.

## Model

A decode error retains the codec name, the byte offset, and the error kind. The display and error interfaces expose the same diagnostic text.

## Contracts

Offsets count bytes from zero. Error kinds distinguish invalid bytes, invalid lengths, invalid pad bytes, and nonzero unused bits.

## Executable example

```fern test name=std-codec-example
import "std/codec";
import "std/string";

function main(): i32 {
  let problem = codec.error("hex", 1, codec.DecodeErrorKind.InvalidLength);
  if (!problem.message().contains("hex byte 1")) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum DecodeErrorKind>>

<<4 pub function (e: DecodeError) message(): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines shared errors for byte encodings.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "std/error";
import "std/i32";
```

### `3 pub enum DecodeErrorKind`

```fern
<<3 pub enum DecodeErrorKind>>=
@derive(cmp.Eq)
pub enum DecodeErrorKind {
  InvalidByte(u8),
  InvalidLength,
  InvalidPadding,
  NonZeroTrailingBits,
}

@derive(cmp.Eq)
pub struct DecodeError { encoding: string, offset: i32, kind: DecodeErrorKind }

impl error.Error for DecodeError {}

impl cmp.Display for DecodeError {}
```

### `4 pub function (e: DecodeError) message(): string`

```fern
<<4 pub function (e: DecodeError) message(): string>>=
pub function (e: DecodeError) message(): string {
  return e.to_string();
}

pub function (e: DecodeError) to_string(): string {
  let reason: string = "invalid encoded input";
  match (e.kind) {
    DecodeErrorKind.InvalidByte(byte) => {
      reason = "invalid byte " + (byte as i32).to_string();
    },
    DecodeErrorKind.InvalidLength => {
      reason = "invalid input length";
    },
    DecodeErrorKind.InvalidPadding => {
      reason = "invalid padding";
    },
    DecodeErrorKind.NonZeroTrailingBits => {
      reason = "nonzero trailing bits";
    }
  }
  return e.encoding + " byte " + e.offset.to_string() + ": " + reason;
}

pub function error(encoding: string, offset: i32, kind: DecodeErrorKind): DecodeError {
  return DecodeError { encoding: encoding, offset: offset, kind: kind };
}

// A missing error means that the pad bytes are valid.
pub(package) function padding_error(input: string, data_end: i32, max_pad: i32, group: i32, encoding: string): Option[DecodeError] {
  let i: i32 = data_end;
  while (i < input.len()) {
    if (input[i] != 61) {
      return Some(error(encoding, i, DecodeErrorKind.InvalidPadding));
    }
    i = i + 1;
  }
  let count: i32 = input.len() - data_end;
  if (count > max_pad) {
    return Some(error(encoding, data_end + max_pad, DecodeErrorKind.InvalidPadding));
  }
  if (count > 0 && (data_end == 0 || input.len() % group != 0)) {
    return Some(error(encoding, data_end, DecodeErrorKind.InvalidPadding));
  }
  return None;
}
```
