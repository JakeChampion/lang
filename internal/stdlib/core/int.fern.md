# Integer text and radix conversion

This module converts integers to strings.

## Model

Decimal and radix conversion share digit helpers. The conversion code separates a negative sign from the unsigned magnitude.

## Contracts

Typed numeric parsers belong to the scalar modules. This module contains shared integer format kernels.

## Executable example

```fern test name=core-int-example
import "core/int";
import "std/i32";

function main(): i32 {
  if (int.int_to_string(42) != "42") { return 1; }
  match (i32.parse_i32_radix("2a", 16)) { Ok(n) => { if (n != 42) { return 2; } }, Err(_) => { return 3; } }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 const SMALL_INTS: string[]>>

<<3 function __int_pair_table(): string>>

<<4 pub function int_to_string(n: i32): string>>


<<6 pub function int_to_string_radix(n: i32, base: i32): string>>
<<numeric parser and format additions>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module converts integers to strings.
```

### `2 const SMALL_INTS: string[]`

```fern
<<2 const SMALL_INTS: string[]>>=
const SMALL_INTS: string[] = [
  "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "21", "22", "23", "24", "25", "26", "27", "28", "29", "30", "31", "32", "33", "34", "35", "36", "37", "38", "39", "40", "41", "42", "43", "44", "45", "46", "47", "48", "49", "50", "51", "52", "53", "54", "55", "56", "57", "58", "59", "60", "61", "62", "63", "64", "65", "66", "67", "68", "69", "70", "71", "72", "73", "74", "75", "76", "77", "78", "79", "80", "81", "82", "83", "84", "85", "86", "87", "88", "89", "90", "91", "92", "93", "94", "95", "96", "97", "98", "99",
];
```

### `3 function __int_pair_table(): string`

```fern
<<3 function __int_pair_table(): string>>=
function __int_pair_table(): string {
  return "00010203040506070809101112131415161718192021222324252627282930313233343536373839404142434445464748495051525354555657585960616263646566676869707172737475767778798081828384858687888990919293949596979899";
}

function __int_u32_digits(u: u32): i32 {
  if (u < 10 as u32) {
    return 1;
  }
  if (u < 100 as u32) {
    return 2;
  }
  if (u < 1000 as u32) {
    return 3;
  }
  if (u < 10000 as u32) {
    return 4;
  }
  if (u < 100000 as u32) {
    return 5;
  }
  if (u < 1000000 as u32) {
    return 6;
  }
  if (u < 10000000 as u32) {
    return 7;
  }
  if (u < 100000000 as u32) {
    return 8;
  }
  if (u < 1000000000 as u32) {
    return 9;
  }
  return 10;
}
```

### `4 pub function int_to_string(n: i32): string`

```fern
<<4 pub function int_to_string(n: i32): string>>=
pub function int_to_string(n: i32): string {
  if (n >= 0 && n < 100) {
    return SMALL_INTS[n];
  }
  let neg: boolean = false;
  if (n < 0) {
    neg = true;
    n = 0 - n;
  }
  let u: u32 = n as u32;
  let ndig: i32 = __int_u32_digits(u);
  let total: i32 = ndig;
  if (neg) {
    total = total + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let tbl: string = __int_pair_table();
  let w: i32 = total;
  while (u >= 100 as u32) {
    let r: i32 = (u % (100 as u32)) as i32;
    u = u / (100 as u32);
    w = w - 2;
    buf = buf.with(w, tbl[r * 2] as u8);
    buf = buf.with(w + 1, tbl[r * 2 + 1] as u8);
  }
  let last: i32 = u as i32;
  if (u >= 10 as u32) {
    w = w - 2;
    buf = buf.with(w, tbl[last * 2] as u8);
    buf = buf.with(w + 1, tbl[last * 2 + 1] as u8);
  } else {
    w = w - 1;
    buf = buf.with(w, (last + 48) as u8);
  }
  if (neg) {
    buf = buf.with(0, 45 as u8);
  }
  return string_from_bytes_unchecked(buf);
}

pub(package) function __int_to_string_u64(mag: i64, neg: i32): string {
  let m: u64 = mag as u64;
  let ndig: i32 = 1;
  let probe: u64 = m;
  while (probe >= 100 as u64) {
    ndig = ndig + 2;
    probe = probe / (100 as u64);
  }
  if (probe >= 10 as u64) {
    ndig = ndig + 1;
  }
  let total: i32 = ndig;
  if (neg != 0) {
    total = total + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let tbl: string = __int_pair_table();
  let w: i32 = total;
  while (m >= 100 as u64) {
    let r: i32 = (m % (100 as u64)) as i32;
    m = m / (100 as u64);
    w = w - 2;
    buf = buf.with(w, tbl[r * 2] as u8);
    buf = buf.with(w + 1, tbl[r * 2 + 1] as u8);
  }
  let last: i32 = m as i32;
  if (m >= 10 as u64) {
    w = w - 2;
    buf = buf.with(w, tbl[last * 2] as u8);
    buf = buf.with(w + 1, tbl[last * 2 + 1] as u8);
  } else {
    w = w - 1;
    buf = buf.with(w, (last + 48) as u8);
  }
  if (neg != 0) {
    buf = buf.with(0, 45 as u8);
  }
  return string_from_bytes_unchecked(buf);
}

pub(package) function __radix_digit(c: i32): i32 {
  if (c >= 48 && c <= 57) {
    return c as i32 - 48;
  }
  if (c >= 97 && c <= 122) {
    return c as i32 - 87;
  }
  if (c >= 65 && c <= 90) {
    return c as i32 - 55;
  }
  return 0 - 1;
}

pub(package) function __radix_char(d: i32): i32 {
  if (d < 10) {
    return d + 48;
  }
  return d + 87;
}
```


### `6 pub function int_to_string_radix(n: i32, base: i32): string`

```fern
<<6 pub function int_to_string_radix(n: i32, base: i32): string>>=
pub function int_to_string_radix(n: i32, base: i32): string {
  if (base == 10) {
    return int_to_string(n);
  }
  return _format_integer(_magnitude(n as i64), n < 0, base);
}
```

### Numeric parsers and formats

```fern
<<numeric parser and format additions>>=
pub(package) function _format_integer(magnitude: u64, negative: boolean, base: i32): string {
  if (base < 2 || base > 36) {
    return "";
  }
  if (base == 10) {
    let sign: i32 = 0;
    if (negative) {
      sign = 1;
    }
    return __int_to_string_u64(magnitude as i64, sign);
  }
  if (magnitude == 0 as u64) {
    return "0";
  }
  let mag: u64 = magnitude;
  let radix: u64 = base as u64;
  let digits: u8[] = __alloc_u8(65);
  let count: i32 = 0;
  while (mag > 0 as u64) {
    digits = digits.with(count, __radix_char((mag % radix) as i32) as u8);
    count = count + 1;
    mag = mag / radix;
  }
  let sign_bytes: i32 = 0;
  if (negative) {
    sign_bytes = 1;
  }
  let buf: u8[] = __alloc_u8(count + sign_bytes);
  if (negative) {
    buf = buf.with(0, 45 as u8);
  }
  let i: i32 = 0;
  while (i < count) {
    buf = buf.with(i + sign_bytes, digits[count - 1 - i]);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub(package) function _group_integer(text: string, separator: string): string {
  let start: i32 = 0;
  if (text.len() > 0 && text[0] == 45) {
    start = 1;
  }
  let digits: i32 = text.len() - start;
  if (digits <= 3 || separator.len() == 0) {
    return text;
  }
  let groups: i32 = (digits - 1) / 3;
  let total: i64 = text.len() as i64 + groups as i64 * (separator.len() as i64);
  if (total > 2147483647 as i64) {
    exit(1);
    return "";
  }
  let buf: u8[] = __alloc_u8(total as i32);
  let pos: i32 = 0;
  if (start == 1) {
    buf = buf.with(0, 45 as u8);
    pos = 1;
  }
  let i: i32 = start;
  while (i < text.len()) {
    if (i > start && (text.len() - i) % 3 == 0) {
      let j: i32 = 0;
      while (j < separator.len()) {
        buf = buf.with(pos, separator[j]);
        pos = pos + 1;
        j = j + 1;
      }
    }
    buf = buf.with(pos, text[i]);
    pos = pos + 1;
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub(package) function _magnitude(n: i64): u64 {
  let raw: u64 = n as u64;
  if (n < 0 as i64) {
    return 0 as u64 - raw;
  }
  return raw;
}

pub(package) function _pad_integer(text: string, width: i32): string {
  let n: i32 = text.len();
  if (n >= width) {
    return text;
  }
  let start: i32 = 0;
  let buf: u8[] = __alloc_u8(width);
  if (n > 0 && text[0] == 45) {
    start = 1;
    buf = buf.with(0, 45 as u8);
  }
  let gap: i32 = width - n;
  let i: i32 = start;
  while (i < start + gap) {
    buf = buf.with(i, 48 as u8);
    i = i + 1;
  }
  let j: i32 = start;
  while (j < n) {
    buf = buf.with(i, text[j]);
    i = i + 1;
    j = j + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```
