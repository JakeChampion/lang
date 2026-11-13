# Pointer-sized unsigned integers

This module provides the `usize` functions. It parses a `usize` from text and writes one in a radix.

## Contracts

The width of `usize` follows the compilation target. `parse_usize` refuses a value that the target cannot hold.

## Executable example

```fern test name=std-usize-example
import "std/usize";

function main(): i32 {
  let Ok(n) = usize.parse_usize("255") else { return 1; };
  if (n.to_hex() != "ff" || n.to_string_radix(2) != "11111111") { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunk below contains the complete implementation.

```fern
<<*>>=
<<dependencies>>

<<parsers and formats>>
```

### `dependencies`

```fern
<<dependencies>>=
// This module provides the usize functions.

import "std/number_parse";
import "core/int";
```

### Parsers and formats

```fern
<<parsers and formats>>=
pub function parse_usize(s: str): Result[usize, number_parse.ParseNumberError] {
  return parse_usize_radix(s, 10);
}

pub function parse_usize_radix(s: str, radix: i32): Result[usize, number_parse.ParseNumberError] {
  let (bits, code) = number_parse._integer(s, radix, false, (0 as usize - 1 as usize) as u64, 0 as u64);
  if (code != number_parse.SUCCESS) {
    return Err(number_parse._error(code, radix));
  }
  return Ok(bits as usize);
}

pub function (n: usize) to_binary(): string {
  return n.to_string_radix(2);
}

pub function (n: usize) to_hex(): string {
  return n.to_string_radix(16);
}

pub function (n: usize) to_oct(): string {
  return n.to_string_radix(8);
}

pub function (n: usize) to_string_padded(width: i32): string {
  return int._pad_integer(n.to_string(), width);
}

pub function (n: usize) to_string_radix(base: i32): string {
  if (base == 10) {
    return n.to_string();
  }
  return int._format_integer(n as u64, false, base);
}

pub function (n: usize) to_string_with_sep(sep: string): string {
  return int._group_integer(n.to_string(), sep);
}
```
