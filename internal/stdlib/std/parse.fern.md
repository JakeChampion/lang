# Strict decimal integer parsing

This module converts decimal strings to integers. A sign is optional. Whitespace and non-ASCII digits are invalid.

## Model

The decimal parser accepts an optional sign and ASCII decimal digits. Its result retains either a signed 64-bit integer or a concrete parse error.

## Contracts

Whitespace and non-ASCII digits are invalid. Overflow at either signed integer boundary is an error.

## Executable example

```fern test name=std-parse-example
import "std/parse";

function main(): i32 {
  let Ok(n) = parse.int64("-42") else { return 1; };
  if (n != -42) { return 2; }
  let Err(_) = parse.int64(" 42") else { return 3; };
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum IntError>>

<<4 pub function (e: IntError) message(): string>>

<<5 impl error.Error for IntError>>

<<6 pub function int64(text: string): Result[i64, IntError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module converts decimal strings to integers. A sign is optional. Whitespace and non-ASCII digits are invalid.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "std/error";
import "std/i32";
```

### `3 pub enum IntError`

```fern
<<3 pub enum IntError>>=
pub enum IntError {
  Empty,
  InvalidDigit(i32),
  Overflow,
}
```

### `4 pub function (e: IntError) message(): string`

```fern
<<4 pub function (e: IntError) message(): string>>=
pub function (e: IntError) message(): string {
  match (e) {
    Empty => {
      return "expected decimal digits";
    },
    InvalidDigit(offset) => {
      return "invalid decimal digit at byte " + offset.to_string();
    },
    Overflow => {
      return "decimal integer is outside the signed 64-bit range";
    }
  }
}

pub function (e: IntError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for IntError`

```fern
<<5 impl error.Error for IntError>>=
impl error.Error for IntError {}

impl cmp.Display for IntError {}
```

### `6 pub function int64(text: string): Result[i64, IntError]`

```fern
<<6 pub function int64(text: string): Result[i64, IntError]>>=
pub function int64(text: string): Result[i64, IntError] {
  if (text.len() == 0) {
    return Err(Empty);
  }
  let at: i32 = 0;
  let negative: boolean = text[0] == b'-';
  if (negative || text[0] == b'+') {
    at = 1;
  }
  if (at == text.len()) {
    return Err(Empty);
  }
  let limit: i64 = -9223372036854775807;
  if (negative) {
    limit = limit - 1;
  }
  let cutoff: i64 = limit / 10;
  let value: i64 = 0;
  while (at < text.len()) {
    let digit: i64 = text[at] as i64 - 48;
    if (digit < 0 || digit > 9) {
      return Err(InvalidDigit(at));
    }
    if (value < cutoff) {
      return Err(Overflow);
    }
    value = value * 10;
    if (value < limit + digit) {
      return Err(Overflow);
    }
    value = value - digit;
    at = at + 1;
  }
  if (negative) {
    return Ok(value);
  }
  return Ok(0 - value);
}
```
