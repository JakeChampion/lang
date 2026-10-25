# Number parsers and integer formats

Status: [reference] Typed numeric conversion and integer formats.

Import the numeric module that owns the result type.
Every parser accepts a borrowed `str` view. An owned string borrows automatically.
The parsers do not trim whitespace or accept digit separators.

| Module | Decimal parser | Radix parser |
|---|---|---|
| `std/i32` | `parse_i32`, `parse_u8` | `parse_i32_radix`, `parse_u8_radix` |
| `std/i64` | `parse_i64` | `parse_i64_radix` |
| `std/u32` | `parse_u32` | `parse_u32_radix` |
| `std/u64` | `parse_u64`, `parse_usize` | `parse_u64_radix`, `parse_usize_radix` |
| `std/f32` | `parse_f32` | — |
| `std/f64` | `parse_f64` | — |

The return type is `Result[T, number_parse.ParseNumberError]`.
Import `std/number_parse` to name the error variants.
The error implements `std/error.Error` and `core/cmp.Display`.

| Error | Meaning |
|---|---|
| `EmptyInput` | The input contains no bytes. |
| `InvalidDigit(offset)` | A byte is invalid, or a required digit is missing at the end offset. |
| `OutOfRange` | The finite value exceeds the result type's range. |
| `InvalidRadix(radix)` | The radix is outside 2 through 36. |

Integer parsers accept an initial plus sign.
Signed parsers also accept a minus sign. Unsigned parsers reject a minus sign. They also reject negative zero.

Radix digits are ASCII and case-insensitive. Prefixes such as `0x` are rejected.
The `usize` range follows the target: 32 bits on WASI and 64 bits on native targets.

Float parsers accept decimal digits, a decimal point, and an optional decimal exponent.
They return the nearest representable value with ties to even.
The f32 parser checks the exact decimal at exact midpoints between adjacent binary32 values.
Negative zero and negative underflow keep their sign.

Case-insensitive `NaN`, `inf`, and `infinity` accept either sign.
Finite overflow reports an error. Explicit infinity succeeds.

With `std/string`, `s.is_int()` is true exactly when `i32.parse_i32(s)` succeeds.
The `s.is_float()` predicate follows `f64.parse_f64(s)` in the same way.
Both predicates include the parser's range checks.

Every integer width provides `to_string_radix`, `to_hex`, `to_binary`, and `to_oct`.
The radix formatter accepts 2 through 36 and returns an empty string for an invalid radix.
Digits are lowercase. A negative value has an initial minus sign and magnitude digits.

The `to_string_padded(width)` formatter includes the sign in the width and inserts zeroes after it.
The `to_string_with_sep(separator)` formatter groups decimal digits in threes from the right.
It preserves all bytes of a Unicode separator.

The previous `string.parse_*` numeric methods are removed.
The duplicate `core/int.parse_int_radix` function is also removed.
Use a numeric module's parser and handle its typed result.

The separate `std/parse.int64` function and its error enum are removed.
Use `i64.parse_i64` and `number_parse.ParseNumberError`.
