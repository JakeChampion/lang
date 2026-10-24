# Integer text and radix conversion

This module converts integers to strings.

## Model

Decimal and radix conversion share digit helpers. The conversion code separates a negative sign from the unsigned magnitude.

## Contracts

`parse_int_radix` returns an option for invalid digits or an unsupported value. A radix determines the interpretation of each digit.

## Executable example

```fern test name=core-int-example
import "core/int";

function main(): i32 {
  if (int.int_to_string(42) != "42") { return 1; }
  match (int.parse_int_radix("2a", 16)) { Some(n) => { if (n != 42) { return 2; } }, None => { return 3; } }
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

<<5 function __radix_value(s: string, from: i32, base: i32, neg: boolean): Option[i32]>>

<<6 pub function int_to_string_radix(n: i32, base: i32): string>>
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

pub function parse_int_radix(s: string, base: i32): Option[i32] {
  if (base < 2 || base > 36) {
    return None;
  }
  let n: i32 = s.len();
  if (n == 0) {
    return None;
  }
  let neg: boolean = false;
  let i: i32 = 0;
  if (s[0] == 45) {
    neg = true;
    i = 1;
  } else if (s[0] == 43) {
    i = 1;
  }
  if (i >= n) {
    return None;
  }
  return __radix_value(s, i, base, neg);
}
```

### `5 function __radix_value(s: string, from: i32, base: i32, neg: boolean): Option[i32]`

```fern
<<5 function __radix_value(s: string, from: i32, base: i32, neg: boolean): Option[i32]>>=
function __radix_value(s: string, from: i32, base: i32, neg: boolean): Option[i32] {
  let limit: i64 = 2147483647 as i64;
  if (neg) {
    limit = limit + 1 as i64;
  }
  let v: i64 = 0 as i64;
  let i: i32 = from;
  while (i < s.len()) {
    let d: i32 = __radix_digit(s[i] as i32);
    if (d < 0 || d >= base) {
      return None;
    }
    v = v * (base as i64) + d as i64;
    if (v > limit) {
      return None;
    }
    i = i + 1;
  }
  if (neg) {
    v = 0 as i64 - v;
  }
  return Some(v as i32);
}
```

### `6 pub function int_to_string_radix(n: i32, base: i32): string`

```fern
<<6 pub function int_to_string_radix(n: i32, base: i32): string>>=
pub function int_to_string_radix(n: i32, base: i32): string {
  if (base < 2 || base > 36) {
    return "";
  }
  if (n == 0) {
    return "0";
  }
  let neg: boolean = n < 0;
  let mag: i64 = n as i64;
  if (neg) {
    mag = 0 - mag;
  }
  let digits: u8[] = __alloc_u8(33);
  let k: i32 = 0;
  let b64: i64 = base as i64;
  while (mag > 0 as i64) {
    let d: i32 = (mag % b64) as i32;
    digits = digits.with(k, __radix_char(d) as u8);
    k = k + 1;
    mag = mag / b64;
  }
  let out_len: i32 = k;
  if (neg) {
    out_len = k + 1;
  }
  let buf: u8[] = __alloc_u8(out_len);
  let bi: i32 = 0;
  if (neg) {
    buf = buf.with(0, 45 as u8);
    bi = 1;
  }
  let j: i32 = k - 1;
  while (j >= 0) {
    buf = buf.with(bi, digits[j]);
    bi = bi + 1;
    j = j - 1;
  }
  return string_from_bytes_unchecked(buf);
}
```
