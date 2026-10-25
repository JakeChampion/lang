# Default primitive display

This internal module provides default display bodies for primitive values.

## Contracts

The compiler loads these bodies as runtime dependencies. They define no receiver methods and expose no source import edges. Explicit user methods retain precedence.

## Executable example

```fern test name=core-primitive-text-example
function main(): i32 { let n: i64 = 42; if (n.to_string() != "42") { return 1; } return 0; }
```

## Module assembly

The root chunk preserves declaration order.

```fern
<<*>>=
<<module introduction>>

<<1 pub(package) function __primitive_i32_to_string(n: i32): string>>

<<2 pub(package) function __primitive_u8_to_string(n: u8): string>>

<<3 pub(package) function __primitive_i64_to_string(n: i64): string>>

<<4 pub(package) function __primitive_u32_to_string(n: u32): string>>

<<5 pub(package) function __primitive_u64_to_string(n: u64): string>>

<<6 pub(package) function __primitive_usize_to_string(n: usize): string>>

<<7 pub(package) function __primitive_char_to_string(c: char): string>>

<<8 pub(package) function __primitive_string_to_string(s: string): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// Default primitive display bodies. These functions define no receiver methods.
import "core/int";
import "core/f64_text";
import "core/utf8_scalar";
```

### `1 pub(package) function __primitive_i32_to_string(n: i32): string`

```fern
<<1 pub(package) function __primitive_i32_to_string(n: i32): string>>=
pub(package) function __primitive_i32_to_string(n: i32): string {
  return int.int_to_string(n);
}
```

### `2 pub(package) function __primitive_u8_to_string(n: u8): string`

```fern
<<2 pub(package) function __primitive_u8_to_string(n: u8): string>>=
pub(package) function __primitive_u8_to_string(n: u8): string {
  return int.int_to_string(n as i32);
}
```

### `3 pub(package) function __primitive_i64_to_string(n: i64): string`

```fern
<<3 pub(package) function __primitive_i64_to_string(n: i64): string>>=
pub(package) function __primitive_i64_to_string(n: i64): string {
  if (n < 0 as i64) {
    return int.__int_to_string_u64(0 as i64 - n, 1);
  }
  return int.__int_to_string_u64(n, 0);
}
```

### `4 pub(package) function __primitive_u32_to_string(n: u32): string`

```fern
<<4 pub(package) function __primitive_u32_to_string(n: u32): string>>=
pub(package) function __primitive_u32_to_string(n: u32): string {
  return int.__int_to_string_u64(n as i64, 0);
}
```

### `5 pub(package) function __primitive_u64_to_string(n: u64): string`

```fern
<<5 pub(package) function __primitive_u64_to_string(n: u64): string>>=
pub(package) function __primitive_u64_to_string(n: u64): string {
  return int.__int_to_string_u64(n as i64, 0);
}
```

### `6 pub(package) function __primitive_usize_to_string(n: usize): string`

```fern
<<6 pub(package) function __primitive_usize_to_string(n: usize): string>>=
pub(package) function __primitive_usize_to_string(n: usize): string {
  return int.__int_to_string_u64(n as i64, 0);
}
```

### `7 pub(package) function __primitive_char_to_string(c: char): string`

```fern
<<7 pub(package) function __primitive_char_to_string(c: char): string>>=
pub(package) function __primitive_char_to_string(c: char): string {
  return utf8_scalar.from_codepoint(c);
}
```

### `8 pub(package) function __primitive_string_to_string(s: string): string`

```fern
<<8 pub(package) function __primitive_string_to_string(s: string): string>>=
pub(package) function __primitive_string_to_string(s: string): string {
  return s;
}
```
