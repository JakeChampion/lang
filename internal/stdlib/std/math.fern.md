# Numeric constants and RGB values

This module provides numeric constants and RGB operations.

## Model

Numeric helpers expose constants and angle conversion. RGB helpers use packed color values.

## Contracts

The text color parser returns an `RgbParseError` for invalid input.

## Executable example

```fern test name=std-math-example
import "std/math";

function main(): i32 {
  if (math.lerp(2.0, 4.0, 0.5) != 3.0) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 constants and conversions>>

<<3 function __hex_digit(b: i32): i32>>

<<4 pub function parse_rgb_hex(s: string): Result[i32, RgbParseError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides numeric constants and RGB operations.
import "core/cmp";
import "core/error";
```

### `2 constants and conversions`

```fern
<<2 constants and conversions>>=
pub const PI: f64 = 3.141592653589793;

pub const TAU: f64 = 6.283185307179586;

pub const E: f64 = 2.718281828459045;

pub function to_radians(deg: f64): f64 {
  return deg * (PI / 180.0);
}

pub function to_degrees(rad: f64): f64 {
  return rad * (180.0 / PI);
}

pub function lerp(a: f64, b: f64, t: f64): f64 {
  return a * (1.0 - t) + b * t;
}

pub function pack_rgb(r: i32, g: i32, b: i32): i32 {
  return (r & 255) << 16 | (g & 255) << 8 | b & 255;
}
```

### `3 function __hex_digit(b: i32): i32`

```fern
<<3 function __hex_digit(b: i32): i32>>=
function __hex_digit(b: i32): i32 {
  if (b >= 48 && b <= 57) {
    return b - 48;
  }
  if (b >= 97 && b <= 102) {
    return b - 97 + 10;
  }
  if (b >= 65 && b <= 70) {
    return b - 65 + 10;
  }
  return -1;
}
```

### `4 pub function parse_rgb_hex(s: string): Result[i32, RgbParseError]`

```fern
<<4 pub function parse_rgb_hex(s: string): Result[i32, RgbParseError]>>=
// The text `parse_rgb_hex` refused.
pub struct RgbParseError { text: string }

impl error.Error for RgbParseError {}

impl cmp.Display for RgbParseError {}

pub function (e: RgbParseError) message(): string {
  return "\"" + e.text + "\" is not a hex color such as #f80 or #ff8800";
}

pub function (e: RgbParseError) to_string(): string {
  return e.message();
}

pub function parse_rgb_hex(s: string): Result[i32, RgbParseError] {
  let start: i32 = 0;
  if (s.len() > 0 && s[0] == 35) {
    start = 1;
  }
  let body: i32 = s.len() - start;
  if (body == 6) {
    let v: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
      let d: i32 = __hex_digit(s[start + i] as i32);
      if (d < 0) {
        return Err(RgbParseError { text: s });
      }
      v = v << 4 | d;
      i = i + 1;
    }
    return Ok(v);
  }
  if (body == 3) {
    let r: i32 = __hex_digit(s[start] as i32);
    let g: i32 = __hex_digit(s[start + 1] as i32);
    let b: i32 = __hex_digit(s[start + 2] as i32);
    if (r < 0 || g < 0 || b < 0) {
      return Err(RgbParseError { text: s });
    }
    return Ok((r << 4 | r) << 16 | (g << 4 | g) << 8 | (b << 4 | b));
  }
  return Err(RgbParseError { text: s });
}

pub function rgb_luminance(rgb: i32): i32 {
  let r: i32 = rgb >> 16 & 255;
  let g: i32 = rgb >> 8 & 255;
  let b: i32 = rgb & 255;
  return (r * 299 + g * 587 + b * 114) / 1000;
}

pub function rgb_is_dark(rgb: i32): boolean {
  return rgb_luminance(rgb) < 128;
}
```
