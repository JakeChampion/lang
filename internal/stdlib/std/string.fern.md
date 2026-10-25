# String operations and numeric text

This module provides string methods.

## Model

String methods cover search, slices, transformations, byte views, and numeric text. UTF-8 text and raw byte access serve different purposes.

## Contracts

Byte offsets must follow each method contract. Optional conversions retain invalid-input outcomes. Numeric validation uses the shared numeric parser.

## Executable example

```fern test name=std-string-example
import "std/string";
import "std/option";

function main(): i32 {
  if (!"hello".starts_with("he") || "hello".index_of("ll").unwrap_or(-1) != 2) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub function (s: string) is_empty(): boolean>>

<<3 function __substr_eq(s: string, offset: i32, needle: string, nLen: i32): boolean>>

<<4 function __str_find_short(s: string, sLen: i32, needle: string, nLen: i32, from: i32): i32>>

<<5 pub function (s: string) contains(needle: string): boolean>>

<<6 function __str_index(s: string, needle: string): i32>>

<<7 pub function (s: string) last_index_of(needle: string): Option[i32]>>

<<8 function put_bytes(buf: u8[], pos: i32, src: string): u8[]>>

<<9 pub function (s: string) escape_html(): string>>

<<10 pub function (s: string) escape_c(): string>>

<<11 function __case_separator(s: string, sep_byte: i32): string>>

<<12 pub function (s: string) camel_case(): string>>

<<13 function __case_join(s: string, upper_first: boolean): string>>

<<14 pub function (s: string) slugify(): string>>

<<15 pub function (s: string) common_prefix(other: string): string>>

<<16 pub function (s: string) is_kebab_case(): boolean>>

<<17 function __is_medial_caps(s: string, upper_first: boolean): boolean>>

<<18 pub function (s: string) shift_byte(delta: i32): u8[]>>

<<19 function __char_in_set(cp: i32, set: string): boolean>>

<<20 pub function (s: string) without_chars(set: string): string>>
<<21 pub function (s: string) lines_non_empty(): string[]>>

<<22 pub function (s: string) is_blank(): boolean>>

<<23 pub function (s: string) trim_start(): string>>

<<24 pub function (s: string) bytes(): u8[]>>

<<25 function __string_case_fold(s: string, srcBase: i32, dstBase: i32): string>>

<<26 pub function (s: string) to_ascii_lower(): string>>

<<27 pub function (s: string) last(): Option[u8]>>

<<28 function __floor_boundary(s: string, i: i32): i32>>

<<29 pub function (s: string) take(n: i32): string>>

<<30 pub function (s: string) eq_ignore_ascii_case(other: string): boolean>>

<<31 function __fill_lead_len(b: i32): i32>>

<<32 pub function (s: string) pad_start(n: i32, ch: char): string>>

<<33 pub function (s: string) trim_start_matches(sub: string): string>>

<<34 function __str_index_ci(s: string, needle: string): i32>>

<<35 pub function (s: string) pad_start_str(width: i32, fill: string): string>>

<<36 function __split_scalars(s: string): string[]>>
<<41 pub function (s: string) reverse_bytes(): u8[]>>

<<42 pub function (s: string) between(start: string, end: string): Option[string]>>
<<43 pub function (s: string) parse_bool(): Option[boolean]>>







<<50 pub function repeat_char(ch: char, n: i32): string>>
```
### `dependencies 1`

```fern
<<dependencies 1>>=
// This module provides string methods.
import "std/number_parse";
import "core/int";
import "core/utf8_scalar";
import "core/bigint";
import "std/i32";
import "std/array";
```
### `2 pub function (s: string) is_empty(): boolean`

```fern
<<2 pub function (s: string) is_empty(): boolean>>=
pub function (s: string) is_empty(): boolean {
  return s.len() == 0;
}

pub function (s: string) to_string(): string {
  return s;
}

pub function (s: string) repeat(n: i32): string {
  if (n <= 0) {
    return "";
  }
  let sl: i32 = s.len();
  if (sl == 0) {
    return "";
  }
  let total: i64 = sl as i64 * (n as i64);
  let out: string = s;
  while (out.len() as i64 * (2 as i64) <= total) {
    out = out + out;
  }
  let rem: i32 = (total - out.len() as i64) as i32;
  if (rem > 0) {
    out = out + slice_unchecked(out, 0, rem);
  }
  return out;
}

// starts_with returns true when the string starts with `prefix`.
pub function (s: string) starts_with(prefix: string): boolean {
  let pLen: i32 = prefix.len();
  if (pLen > s.len()) {
    return false;
  }
  return __mismatch(s, 0, prefix, 0, pLen) == pLen;
}

pub function (s: string) ends_with(suffix: string): boolean {
  let sufLen: i32 = suffix.len();
  if (sufLen > s.len()) {
    return false;
  }
  return __mismatch(s, s.len() - sufLen, suffix, 0, sufLen) == sufLen;
}
```
### `3 function __substr_eq(s: string, offset: i32, needle: string, nLen: i32): boolean`

```fern
<<3 function __substr_eq(s: string, offset: i32, needle: string, nLen: i32): boolean>>=
function __substr_eq(s: string, offset: i32, needle: string, nLen: i32): boolean {
  let i: i32 = 0;
  while (i < nLen) {
    if (s[offset + i] != needle[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __str_maxsuf(x: string, m: i32, invert: boolean, origin: i32, step: i32): (i32, i32) {
  let ms: i32 = 0 - 1;
  let j: i32 = 0;
  let k: i32 = 1;
  let p: i32 = 1;
  while (j + k < m) {
    let a: i32 = x[origin + step * (j + k)] as i32;
    let b: i32 = x[origin + step * (ms + k)] as i32;
    if (invert) {
      let t: i32 = a;
      a = b;
      b = t;
    }
    if (a < b) {
      j = j + k;
      k = 1;
      p = j - ms;
    } else if (a == b) {
      if (k == p) {
        j = j + p;
        k = 1;
      } else {
        k = k + 1;
      }
    } else {
      ms = j;
      j = j + 1;
      k = 1;
      p = 1;
    }
  }
  return (ms, p);
}

function __str_critical_factorization(needle: string, m: i32, origin: i32, step: i32): (i32, i32) {
  if (m < 3) {
    return (m - 1, 1);
  }
  let fwd: (i32, i32) = __str_maxsuf(needle, m, false, origin, step);
  let rev: (i32, i32) = __str_maxsuf(needle, m, true, origin, step);
  if (rev.0 < fwd.0) {
    return (fwd.0 + 1, fwd.1);
  }
  return (rev.0 + 1, rev.1);
}

function __str_is_periodic(needle: string, origin: i32, crit: i32, period: i32, step: i32): boolean {
  let i: i32 = 0;
  while (i < crit) {
    if (needle[origin + step * (period + i)] != needle[origin + step * i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __str_two_way(s: string, sLen: i32, needle: string, m: i32, start: i32, sOrigin: i32, nOrigin: i32, step: i32): i32 {
  let cf: (i32, i32) = __str_critical_factorization(needle, m, nOrigin, step);
  let crit: i32 = cf.0;
  let period: i32 = cf.1;
  let last: i32 = sLen - m;
  let j: i32 = start;

  if (__str_is_periodic(needle, nOrigin, crit, period, step)) {
    let memory: i32 = 0;
    while (j <= last) {
      let i: i32 = crit;
      if (memory > i) {
        i = memory;
      }
      while (i < m && needle[nOrigin + step * i] == s[sOrigin + step * (i + j)]) {
        i = i + 1;
      }
      if (i >= m) {
        i = crit - 1;
        while (i >= memory && needle[nOrigin + step * i] == s[sOrigin + step * (i + j)]) {
          i = i - 1;
        }
        if (i < memory) {
          return j;
        }
        j = j + period;
        memory = m - period;
      } else {
        j = j + (i - crit) + 1;
        memory = 0;
      }
    }
    return 0 - 1;
  }

  let shift: i32 = crit;
  if (m - crit > shift) {
    shift = m - crit;
  }
  shift = shift + 1;
  while (j <= last) {
    let i: i32 = crit;
    while (i < m && needle[nOrigin + step * i] == s[sOrigin + step * (i + j)]) {
      i = i + 1;
    }
    if (i >= m) {
      i = crit - 1;
      while (i >= 0 && needle[nOrigin + step * i] == s[sOrigin + step * (i + j)]) {
        i = i - 1;
      }
      if (i < 0) {
        return j;
      }
      j = j + shift;
    } else {
      j = j + (i - crit) + 1;
    }
  }
  return 0 - 1;
}
```
### `4 function __str_find_short(s: string, sLen: i32, needle: string, nLen: i32, from: i32): i32`

```fern
<<4 function __str_find_short(s: string, sLen: i32, needle: string, nLen: i32, from: i32): i32>>=
function __str_find_short(s: string, sLen: i32, needle: string, nLen: i32, from: i32): i32 {
  let i: i32 = from;
  while (i <= sLen - nLen) {
    let k: i32 = 0;
    while (k < nLen && s[i + k] == needle[k]) {
      k = k + 1;
    }
    if (k == nLen) {
      return i;
    }
    i = i + 1;
  }
  return 0 - 1;
}

function __str_find_from(s: string, sLen: i32, needle: string, nLen: i32, start: i32): i32 {
  let from: i32 = start;
  if (from < 0) {
    from = 0;
  }
  if (nLen == 0) {
    if (from > sLen) {
      return sLen;
    }
    return from;
  }
  if (nLen > sLen - from) {
    return 0 - 1;
  }
  if (nLen == 1) {
    return __memchr(s, needle[0] as i32, from);
  }
  if (nLen <= 4) {
    return __str_find_short(s, sLen, needle, nLen, from);
  }
  return __str_two_way(s, sLen, needle, nLen, from, 0, 0, 1);
}
```
### `5 pub function (s: string) contains(needle: string): boolean`

```fern
<<5 pub function (s: string) contains(needle: string): boolean>>=
pub function (s: string) contains(needle: string): boolean {
  let nLen: i32 = needle.len();
  if (nLen == 0) {
    return true;
  }
  return __str_find_from(s, s.len(), needle, nLen, 0) >= 0;
}

pub function (s: string) index_of(needle: string): Option[i32] {
  return __str_some(__str_index(s, needle));
}
```
### `6 function __str_index(s: string, needle: string): i32`

```fern
<<6 function __str_index(s: string, needle: string): i32>>=
function __str_index(s: string, needle: string): i32 {
  let nLen: i32 = needle.len();
  if (nLen == 0) {
    return 0;
  }
  return __str_find_from(s, s.len(), needle, nLen, 0);
}

function __str_some(i: i32): Option[i32] {
  if (i < 0) {
    return None;
  }
  return Some(i);
}

function __str_rfind_budget(): i32 {
  return 4;
}

function __str_rfind_from(s: string, sLen: i32, needle: string, nLen: i32): i32 {
  if (nLen == 0) {
    return sLen;
  }
  if (nLen > sLen) {
    return 0 - 1;
  }
  if (nLen == 1) {
    return __rmemchr(s, needle[0] as i32, sLen - 1);
  }

  let spent: i32 = 0;
  let limit: i32 = sLen * __str_rfind_budget() + 16;
  let i: i32 = sLen - nLen;
  while (i >= 0 && spent <= limit) {
    let k: i32 = 0;
    let same: boolean = true;
    while (k < nLen) {
      spent = spent + 1;
      if (s[i + k] != needle[k]) {
        same = false;
        k = nLen;
      } else {
        k = k + 1;
      }
    }
    if (same) {
      return i;
    }
    i = i - 1;
  }
  if (i < 0) {
    return 0 - 1;
  }

  let r: i32 = __str_two_way(s, sLen, needle, nLen, 0, sLen - 1, nLen - 1, 0 - 1);
  if (r < 0) {
    return 0 - 1;
  }
  return sLen - r - nLen;
}
```
### `7 pub function (s: string) last_index_of(needle: string): Option[i32]`

```fern
<<7 pub function (s: string) last_index_of(needle: string): Option[i32]>>=
pub function (s: string) last_index_of(needle: string): Option[i32] {
  return __str_some(__str_rfind_from(s, s.len(), needle, needle.len()));
}

pub function (s: string) to_ascii_capitalize(): string {
  let n: i32 = s.len();
  if (n == 0) {
    return "";
  }
  let first: i32 = s[0].to_ascii_upper() as i32;
  if (first == s[0] as i32) {
    return s;
  }
  let buf: u8[] = __alloc_u8(1);
  buf = buf.with(0, first as u8);
  return string_from_bytes_unchecked(buf) + slice_unchecked(s, 1, n);
}

pub function (s: string) whitespace_word_count(): i32 {
  let n: i32 = s.len();
  let c: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    while (i < n && __is_ascii_ws(s[i] as i32)) {
      i = i + 1;
    }
    if (i >= n) {
      return c;
    }
    c = c + 1;
    while (i < n && !__is_ascii_ws(s[i] as i32)) {
      i = i + 1;
    }
  }
  return c;
}
```
### `8 function put_bytes(buf: u8[], pos: i32, src: string): u8[]`

```fern
<<8 function put_bytes(buf: u8[], pos: i32, src: string): u8[]>>=
function put_bytes(buf: u8[], pos: i32, src: string): u8[] {
  let b: u8[] = buf;
  let j: i32 = 0;
  while (j < src.len()) {
    b = b.with(pos + j, src[j] as u8);
    j = j + 1;
  }
  return b;
}
```
### `9 pub function (s: string) escape_html(): string`

```fern
<<9 pub function (s: string) escape_html(): string>>=
pub function (s: string) escape_html(): string {
  let n: i32 = s.len();
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 38 || b == 34 || b == 39) {
      total = total + 5;
    } else if (b == 60 || b == 62) {
      total = total + 4;
    } else {
      total = total + 1;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 38) {
      buf = put_bytes(buf, pos, "&amp;");
      pos = pos + 5;
    } else if (b == 60) {
      buf = put_bytes(buf, pos, "&lt;");
      pos = pos + 4;
    } else if (b == 62) {
      buf = put_bytes(buf, pos, "&gt;");
      pos = pos + 4;
    } else if (b == 34) {
      buf = put_bytes(buf, pos, "&#34;");
      pos = pos + 5;
    } else if (b == 39) {
      buf = put_bytes(buf, pos, "&#39;");
      pos = pos + 5;
    } else {
      buf = buf.with(pos, s[i] as u8);
      pos = pos + 1;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) strip_quotes(): Option[string] {
  let n: i32 = s.len();
  if (n < 2) {
    return None;
  }
  let first: i32 = s[0] as i32;
  let last: i32 = s[n - 1] as i32;
  if (first == 34 && last == 34 || first == 39 && last == 39) {
    return Some(slice_unchecked(s, 1, n - 1).to_owned());
  }
  return None;
}

pub function (s: string) leading_count(b: u8): i32 {
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n && s[i] as i32 == b as i32) {
    i = i + 1;
  }
  return i;
}

pub function (s: string) trailing_count(b: u8): i32 {
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n && s[n - 1 - i] as i32 == b as i32) {
    i = i + 1;
  }
  return i;
}

pub function (s: string) hash_fnv32(): u32 {
  let h: u32 = 2166136261u32;
  let prime: u32 = 16777619u32;
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n) {
    h = h ^ s[i] as u32;
    h = h * prime;
    i = i + 1;
  }
  return h;
}

pub function (s: string) hash_djb2(): u32 {
  let h: u32 = 5381 as u32;
  let mult: u32 = 33 as u32;
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n) {
    h = h * mult + s[i] as u32;
    i = i + 1;
  }
  return h;
}
```
### `10 pub function (s: string) escape_c(): string`

```fern
<<10 pub function (s: string) escape_c(): string>>=
pub function (s: string) escape_c(): string {
  let n: i32 = s.len();
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 92 || b == 34 || b == 10 || b == 9 || b == 13 || b == 0) {
      total = total + 2;
    } else {
      total = total + 1;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 92) {
      buf = put_bytes(buf, pos, "\\\\");
      pos = pos + 2;
    } else if (b == 34) {
      buf = put_bytes(buf, pos, "\\\"");
      pos = pos + 2;
    } else if (b == 10) {
      buf = put_bytes(buf, pos, "\\n");
      pos = pos + 2;
    } else if (b == 9) {
      buf = put_bytes(buf, pos, "\\t");
      pos = pos + 2;
    } else if (b == 13) {
      buf = put_bytes(buf, pos, "\\r");
      pos = pos + 2;
    } else if (b == 0) {
      buf = put_bytes(buf, pos, "\\0");
      pos = pos + 2;
    } else {
      buf = buf.with(pos, s[i] as u8);
      pos = pos + 1;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) escape_shell(): string {
  return "'" + s.replace("'", "'\\''") + "'";
}

pub function (s: string) snake_case(): string {
  return __case_separator(s, 95);
}

pub function (s: string) kebab_case(): string {
  return __case_separator(s, 45);
}
```
### `11 function __case_separator(s: string, sep_byte: i32): string`

```fern
<<11 function __case_separator(s: string, sep_byte: i32): string>>=
function __case_separator(s: string, sep_byte: i32): string {
  let n: i32 = s.len();
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let b: u8 = s[i];
    if (b.is_ascii_upper() && i > 0) {
      total = total + 2;
    } else {
      total = total + 1;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: u8 = s[i];
    if (b.is_ascii_upper() && i > 0) {
      buf = buf.with(pos, sep_byte as u8);
      pos = pos + 1;
    }
    if (b == 32) {
      buf = buf.with(pos, sep_byte as u8);
    } else {
      buf = buf.with(pos, b.to_ascii_lower());
    }
    pos = pos + 1;
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```
### `12 pub function (s: string) camel_case(): string`

```fern
<<12 pub function (s: string) camel_case(): string>>=
pub function (s: string) camel_case(): string {
  return __case_join(s, false);
}

pub function (s: string) pascal_case(): string {
  return __case_join(s, true);
}
```
### `13 function __case_join(s: string, upper_first: boolean): string`

```fern
<<13 function __case_join(s: string, upper_first: boolean): string>>=
function __case_join(s: string, upper_first: boolean): string {
  let n: i32 = s.len();
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let b: u8 = s[i];
    if (b != 95 && b != 45 && b != 32) {
      total = total + 1;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  let at_word_start: boolean = true;
  let emitted_any: boolean = false;
  i = 0;
  while (i < n) {
    let b: u8 = s[i];
    if (b == 95 || b == 45 || b == 32) {
      at_word_start = true;
      i = i + 1;
    } else {
      let out_b: u8 = b.to_ascii_lower();
      if (at_word_start) {
        if (!emitted_any) {
          if (upper_first) {
            out_b = b.to_ascii_upper();
          }
        } else {
          out_b = b.to_ascii_upper();
        }
      }
      buf = buf.with(pos, out_b);
      pos = pos + 1;
      at_word_start = false;
      emitted_any = true;
      i = i + 1;
    }
  }
  return string_from_bytes_unchecked(buf);
}
```
### `14 pub function (s: string) slugify(): string`

```fern
<<14 pub function (s: string) slugify(): string>>=
pub function (s: string) slugify(): string {
  let n: i32 = s.len();
  let out: u8[] = [];
  let pending: boolean = false;
  let i: i32 = 0;
  while (i < n) {
    let lc: i32 = s[i].to_ascii_lower() as i32;
    let alnum: boolean = lc >= 97 && lc <= 122 || lc >= 48 && lc <= 57;
    if (alnum) {
      if (pending && out.len() > 0) {
        out = out.append(45 as u8);
      }
      pending = false;
      out = out.append(lc as u8);
    } else {
      pending = true;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(out);
}

pub function (s: string) is_valid_identifier(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  let first: u8 = s[0];
  if (!first.is_ascii_letter() && first != 95) {
    return false;
  }
  let i: i32 = 1;
  while (i < n) {
    if (!s[i].is_ascii_alphanumeric() && s[i] != 95) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_ipv4(): boolean {
  let parts: string[] = s.split(".");
  if (parts.len() != 4) {
    return false;
  }
  let i: i32 = 0;
  while (i < 4) {
    let p: string = parts[i];
    let plen: i32 = p.len();
    if (plen == 0 || plen > 3) {
      return false;
    }
    let v: i32 = 0;
    let j: i32 = 0;
    while (j < plen) {
      let b: u8 = p[j];
      if (!b.is_ascii_digit()) {
        return false;
      }
      v = v * 10 + (b as i32 - 48);
      j = j + 1;
    }
    if (v < 0 || v > 255) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_email_like(): boolean {
  match (s.split_once("@")) {
    Some(p) => {
      let lo: string = p.0;
      let dom: string = p.1;
      if (lo.len() == 0) {
        return false;
      }
      if (dom.len() == 0) {
        return false;
      }
      return dom.contains(".");
    },
    None => {
      return false;
    }
  }
}

pub function (s: string) is_url_like(): boolean {
  let rest: string = "";
  if (s.starts_with("https://")) {
    rest = s.remove_prefix("https://");
  } else if (s.starts_with("http://")) {
    rest = s.remove_prefix("http://");
  } else {
    return false;
  }
  if (rest.len() == 0) {
    return false;
  }
  return rest.contains(".");
}

pub function (s: string) is_json_like(): boolean {
  let t: string = s.trim();
  let n: i32 = t.len();
  if (n < 2) {
    return false;
  }
  let first: i32 = t[0] as i32;
  let last: i32 = t[n - 1] as i32;
  if (first == 123 && last == 125) {
    return true;
  }
  if (first == 91 && last == 93) {
    return true;
  }
  return false;
}
```
### `15 pub function (s: string) common_prefix(other: string): string`

```fern
<<15 pub function (s: string) common_prefix(other: string): string>>=
pub function (s: string) common_prefix(other: string): string {
  let n: i32 = s.len();
  let m: i32 = other.len();
  let cap: i32 = n;
  if (m < cap) {
    cap = m;
  }
  let i: i32 = 0;
  while (i < cap) {
    if (s[i] != other[i]) {
      return s.take(i);
    }
    i = i + 1;
  }
  return s.take(cap);
}

pub function (s: string) common_suffix(other: string): string {
  let n: i32 = s.len();
  let m: i32 = other.len();
  let cap: i32 = n;
  if (m < cap) {
    cap = m;
  }
  let i: i32 = 0;
  while (i < cap) {
    if (s[n - 1 - i] != other[m - 1 - i]) {
      return s.drop(n - i);
    }
    i = i + 1;
  }
  return s.drop(n - cap);
}

pub function (s: string) starts_with_any(prefixes: string[]): boolean {
  let n: i32 = prefixes.len();
  let i: i32 = 0;
  while (i < n) {
    if (s.starts_with(prefixes[i])) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

pub function (s: string) ends_with_any(suffixes: string[]): boolean {
  let n: i32 = suffixes.len();
  let i: i32 = 0;
  while (i < n) {
    if (s.ends_with(suffixes[i])) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

pub function (s: string) replace_first(old: string, new: string): string {
  return s.replace_n(old, new, 1);
}

pub function (s: string) word_at(i: i32): Option[string] {
  if (i < 0) {
    return None;
  }
  let words: string[] = s.fields();
  if (i >= words.len()) {
    return None;
  }
  return Some(words[i]);
}

pub function (s: string) whitespace_word_count_min(min_len: i32): i32 {
  let words: string[] = s.fields();
  let n: i32 = words.len();
  let c: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (words[i].len() >= min_len) {
      c = c + 1;
    }
    i = i + 1;
  }
  return c;
}

pub function (s: string) longest_word(): Option[string] {
  let words: string[] = s.fields();
  let n: i32 = words.len();
  if (n == 0) {
    return None;
  }
  let best: string = words[0];
  let i: i32 = 1;
  while (i < n) {
    if (words[i].len() > best.len()) {
      best = words[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function (s: string) is_quoted(): boolean {
  let n: i32 = s.len();
  if (n < 2) {
    return false;
  }
  let first: i32 = s[0] as i32;
  let last: i32 = s[n - 1] as i32;
  if (first != last) {
    return false;
  }
  if (first == 34 || first == 39 || first == 96) {
    return true;
  }
  return false;
}
```
### `16 pub function (s: string) is_kebab_case(): boolean`

```fern
<<16 pub function (s: string) is_kebab_case(): boolean>>=
pub function (s: string) is_kebab_case(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  if (s[0] == 45 || s[n - 1] == 45) {
    return false;
  }
  let i: i32 = 0;
  let prev_hyphen: boolean = false;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 45) {
      if (prev_hyphen) {
        return false;
      }
      prev_hyphen = true;
    } else if (b >= 97 && b <= 122 || b >= 48 && b <= 57) {
      prev_hyphen = false;
    } else {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_snake_case(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  if (s[0] == 95 || s[n - 1] == 95) {
    return false;
  }
  let i: i32 = 0;
  let prev_under: boolean = false;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 95) {
      if (prev_under) {
        return false;
      }
      prev_under = true;
    } else if (b >= 97 && b <= 122 || b >= 48 && b <= 57) {
      prev_under = false;
    } else {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_camel_case(): boolean {
  return __is_medial_caps(s, false);
}

pub function (s: string) is_pascal_case(): boolean {
  return __is_medial_caps(s, true);
}
```
### `17 function __is_medial_caps(s: string, upper_first: boolean): boolean`

```fern
<<17 function __is_medial_caps(s: string, upper_first: boolean): boolean>>=
function __is_medial_caps(s: string, upper_first: boolean): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  let f: i32 = s[0] as i32;
  if (upper_first) {
    if (!(f >= 65 && f <= 90)) {
      return false;
    }
  } else {
    if (!(f >= 97 && f <= 122)) {
      return false;
    }
  }
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    let ok: boolean = b >= 97 && b <= 122 || b >= 65 && b <= 90 || b >= 48 && b <= 57;
    if (!ok) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```
### `18 pub function (s: string) shift_byte(delta: i32): u8[]`

```fern
<<18 pub function (s: string) shift_byte(delta: i32): u8[]>>=
pub function (s: string) shift_byte(delta: i32): u8[] {
  let n: i32 = s.len();
  let buf: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32 + delta;
    b = (b % 256 + 256) % 256;
    buf = buf.with(i, b as u8);
    i = i + 1;
  }
  return buf;
}
```
### `19 function __char_in_set(cp: i32, set: string): boolean`

```fern
<<19 function __char_in_set(cp: i32, set: string): boolean>>=
function __char_in_set(cp: i32, set: string): boolean {
  let j: i32 = 0;
  let setLen: i32 = set.len();
  while (j < setLen) {
    let step: i32 = utf8_scalar.step(set, j, setLen);
    if (utf8_scalar.decode_at(set, j, step) == cp) {
      return true;
    }
    j = j + step;
  }
  return false;
}
```
### `20 pub function (s: string) without_chars(set: string): string`

```fern
<<20 pub function (s: string) without_chars(set: string): string>>=
pub function (s: string) without_chars(set: string): string {
  let n: i32 = s.len();
  if (n == 0) {
    return s;
  }
  if (set.len() == 0) {
    return s;
  }
  let buf: u8[] = __alloc_u8(n);
  let pos: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let step: i32 = utf8_scalar.step(s, i, n);
    if (!__char_in_set(utf8_scalar.decode_at(s, i, step), set)) {
      let b: i32 = 0;
      while (b < step) {
        buf = buf.with(pos, s[i + b]);
        pos = pos + 1;
        b = b + 1;
      }
    }
    i = i + step;
  }
  let out: u8[] = __alloc_u8(pos);
  let k: i32 = 0;
  while (k < pos) {
    out = out.with(k, buf[k]);
    k = k + 1;
  }
  return string_from_bytes_unchecked(out);
}

pub function (s: string) contains_only(set: string): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return true;
  }
  if (set.len() == 0) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    let step: i32 = utf8_scalar.step(s, i, n);
    if (!__char_in_set(utf8_scalar.decode_at(s, i, step), set)) {
      return false;
    }
    i = i + step;
  }
  return true;
}

pub function (s: string) count_chars_in(set: string): i32 {
  let n: i32 = s.len();
  if (n == 0 || set.len() == 0) {
    return 0;
  }
  let c: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let step: i32 = utf8_scalar.step(s, i, n);
    if (__char_in_set(utf8_scalar.decode_at(s, i, step), set)) {
      c = c + 1;
    }
    i = i + step;
  }
  return c;
}

pub function (s: string) split_at(i: i32): (string, string) {
  let n: i32 = s.len();
  let k: i32 = __floor_boundary(s, i);
  return (slice_unchecked(s, 0, k).to_owned(), slice_unchecked(s, k, n).to_owned());
}

pub function (s: string) replace_byte(from: u8, to: u8): u8[] {
  let n: i32 = s.len();
  let buf: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == from as i32) {
      buf = buf.with(i, to as u8);
    } else {
      buf = buf.with(i, b as u8);
    }
    i = i + 1;
  }
  return buf;
}

pub function (s: string) to_acronym(): string {
  let toks: string[] = s.fields();
  let n: i32 = toks.len();
  let out: string = "";
  let i: i32 = 0;
  while (i < n) {
    let t: string = toks[i];
    if (t.len() > 0) {
      let b: u8 = t[0];
      out = out + b.to_ascii_upper().to_ascii_string();
    }
    i = i + 1;
  }
  return out;
}

pub function (s: string) to_ascii_title_case(): string {
  let n: i32 = s.len();
  if (n == 0) {
    return s;
  }
  let buf: u8[] = __alloc_u8(n);
  let prev_space: boolean = true;
  let i: i32 = 0;
  while (i < n) {
    let b: u8 = s[i];
    if (prev_space) {
      buf = buf.with(i, b.to_ascii_upper());
    } else {
      buf = buf.with(i, b);
    }
    prev_space = b == 32;
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```
### `21 pub function (s: string) lines_non_empty(): string[]`

```fern
<<21 pub function (s: string) lines_non_empty(): string[]>>=
pub function (s: string) lines_non_empty(): string[] {
  let raw: string[] = s.lines();
  let n: i32 = raw.len();
  let out: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    if (raw[i].len() != 0) {
      out = out.append(raw[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (s: string) is_ascii_only(): boolean {
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n) {
    if (s[i] > 127) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_ascii_digits(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    if (!s[i].is_ascii_digit()) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_ascii_letters(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    if (!s[i].is_ascii_letter()) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_ascii_alphanumeric(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    if (!s[i].is_ascii_alphanumeric()) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_int(): boolean {
  match (i32.parse_i32(s)) {
    Ok(_) => {
      return true;
    },
    Err(_) => {
      return false;
    }
  }
  return false;
}

pub function (s: string) is_float(): boolean {
  let (_, code) = number_parse._float64(s);
  return code == number_parse.SUCCESS;
}

pub function (s: string) wrap(prefix: string, suffix: string): string {
  return prefix + s + suffix;
}
```
### `22 pub function (s: string) is_blank(): boolean`

```fern
<<22 pub function (s: string) is_blank(): boolean>>=
pub function (s: string) is_blank(): boolean {
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n) {
    if (!__is_ascii_ws(s[i] as i32)) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) is_hex_string(): boolean {
  let n: i32 = s.len();
  if (n == 0) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    if (!s[i].is_ascii_hex_digit()) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) indent(prefix: string): string {
  let n: i32 = s.len();
  if (n == 0) {
    return "";
  }
  let pl: i32 = prefix.len();
  let nl: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (s[i] == 10 && i + 1 < n) {
      nl = nl + 1;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(pl * (1 + nl) + n);
  let pos: i32 = 0;
  buf = put_bytes(buf, pos, prefix);
  pos = pos + pl;
  i = 0;
  while (i < n) {
    buf = buf.with(pos, s[i] as u8);
    pos = pos + 1;
    if (s[i] == 10 && i + 1 < n) {
      buf = put_bytes(buf, pos, prefix);
      pos = pos + pl;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) dedent(): string {
  let lines: string[] = s.split("\n");
  let common: string = "";
  let have_common: boolean = false;
  let i: i32 = 0;
  while (i < lines.len()) {
    let line: string = lines[i];
    let ws: i32 = 0;
    while (ws < line.len() && (line[ws] == 32 || line[ws] == 9)) {
      ws = ws + 1;
    }
    if (ws < line.len()) {
      let lead: string = slice_unchecked(line, 0, ws) + "";
      if (!have_common) {
        common = lead;
        have_common = true;
      } else {
        let k: i32 = 0;
        let maxk: i32 = common.len();
        if (lead.len() < maxk) {
          maxk = lead.len();
        }
        while (k < maxk && common[k] == lead[k]) {
          k = k + 1;
        }
        common = slice_unchecked(common, 0, k) + "";
      }
    }
    i = i + 1;
  }
  let clen: i32 = common.len();
  let out: string[] = [];
  let j: i32 = 0;
  while (j < lines.len()) {
    let ln: string = lines[j];
    let wsj: i32 = 0;
    while (wsj < ln.len() && (ln[wsj] == 32 || ln[wsj] == 9)) {
      wsj = wsj + 1;
    }
    if (wsj == ln.len()) {
      out = out.append("");
    } else {
      out = out.append(slice_unchecked(ln, clen, ln.len()) + "");
    }
    j = j + 1;
  }
  return out.join("\n");
}

pub function (s: string) to_owned(): string {
  return s + "";
}

pub function (s: string) trim(): string {
  let n: i32 = s.len();
  let low: i32 = 0;
  let high: i32 = n;
  while (low < high) {
    if (!__is_ascii_ws(s[low] as i32)) {
      break;
    }
    low = low + 1;
  }
  while (high > low) {
    if (!__is_ascii_ws(s[high - 1] as i32)) {
      break;
    }
    high = high - 1;
  }
  return slice_unchecked(s, low, high) + "";
}
```
### `23 pub function (s: string) trim_start(): string`

```fern
<<23 pub function (s: string) trim_start(): string>>=
pub function (s: string) trim_start(): string {
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n && __is_ascii_ws(s[i] as i32)) {
    i = i + 1;
  }
  return slice_unchecked(s, i, n) + "";
}

pub function (s: string) trim_end(): string {
  let n: i32 = s.len();
  let end: i32 = n;
  while (end > 0 && __is_ascii_ws(s[end - 1] as i32)) {
    end = end - 1;
  }
  return slice_unchecked(s, 0, end) + "";
}

pub function (s: string) rstrip_newline(): string {
  let n: i32 = s.len();
  if (n == 0) {
    return s;
  }
  if (s[n - 1] == 10) {
    if (n >= 2 && s[n - 2] == 13) {
      return slice_unchecked(s, 0, n - 2) + "";
    }
    return slice_unchecked(s, 0, n - 1) + "";
  }
  return s;
}

pub function (s: string) trim_chars(chars: string): string {
  let n: i32 = s.len();
  if (chars.len() == 0) {
    return s;
  }
  let low: i32 = 0;
  let high: i32 = n;
  while (low < high) {
    let step: i32 = utf8_scalar.step(s, low, n);
    if (!__char_in_set(utf8_scalar.decode_at(s, low, step), chars)) {
      break;
    }
    low = low + step;
  }
  while (high > low) {
    let start: i32 = utf8_scalar.prev_unit(s, high);
    if (!__char_in_set(utf8_scalar.decode_at(s, start, high - start), chars)) {
      break;
    }
    high = start;
  }
  return slice_unchecked(s, low, high) + "";
}

pub function (s: string) trim_start_chars(chars: string): string {
  let n: i32 = s.len();
  if (chars.len() == 0) {
    return s;
  }
  let i: i32 = 0;
  while (i < n) {
    let step: i32 = utf8_scalar.step(s, i, n);
    if (!__char_in_set(utf8_scalar.decode_at(s, i, step), chars)) {
      break;
    }
    i = i + step;
  }
  return slice_unchecked(s, i, n) + "";
}

pub function (s: string) trim_end_chars(chars: string): string {
  let n: i32 = s.len();
  if (chars.len() == 0) {
    return s;
  }
  let end: i32 = n;
  while (end > 0) {
    let start: i32 = utf8_scalar.prev_unit(s, end);
    if (!__char_in_set(utf8_scalar.decode_at(s, start, end - start), chars)) {
      break;
    }
    end = start;
  }
  return slice_unchecked(s, 0, end) + "";
}

pub function (s: string) starts_with_ci(prefix: string): boolean {
  let sLen: i32 = s.len();
  let pLen: i32 = prefix.len();
  if (pLen > sLen) {
    return false;
  }
  let i: i32 = 0;
  while (i < pLen) {
    if (s[i].to_ascii_lower() != prefix[i].to_ascii_lower()) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) ends_with_ci(suffix: string): boolean {
  let sLen: i32 = s.len();
  let uLen: i32 = suffix.len();
  if (uLen > sLen) {
    return false;
  }
  let off: i32 = sLen - uLen;
  let i: i32 = 0;
  while (i < uLen) {
    if (s[off + i].to_ascii_lower() != suffix[i].to_ascii_lower()) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```
### `24 pub function (s: string) bytes(): u8[]`

```fern
<<24 pub function (s: string) bytes(): u8[]>>=
pub function (s: string) bytes(): u8[] {
  let n: i32 = s.len();
  let out: u8[] = __alloc_u8(n);
  let data: usize = __str_bytes(s, 0 as usize);
  if (data == 0 as usize) {
    let i: i32 = 0;
    while (i < n) {
      out = out.with(i, s[i]);
      i = i + 1;
    }
  } else {
    __memcpy(out as usize, data, n);
  }
  return out;
}
```
### `25 function __string_case_fold(s: string, srcBase: i32, dstBase: i32): string`

```fern
<<25 function __string_case_fold(s: string, srcBase: i32, dstBase: i32): string>>=
function __string_case_fold(s: string, srcBase: i32, dstBase: i32): string {
  let n: i32 = s.len();
  let buf: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b >= srcBase && b < srcBase + 26) {
      b = b + (dstBase - srcBase);
    }
    buf = buf.with(i, b as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```
### `26 pub function (s: string) to_ascii_lower(): string`

```fern
<<26 pub function (s: string) to_ascii_lower(): string>>=
pub function (s: string) to_ascii_lower(): string {
  return __string_case_fold(s, 65, 97);
}

pub function (s: string) to_ascii_upper(): string {
  return __string_case_fold(s, 97, 65);
}

pub function (s: string) to_ascii_swap_case(): string {
  let n: i32 = s.len();
  let buf: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b >= 65 && b <= 90) {
      b = b + 32;
    } else if (b >= 97 && b <= 122) {
      b = b - 32;
    }
    buf = buf.with(i, b as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) split(sep: string): string[] {
  let n: i32 = s.len();
  let sepLen: i32 = sep.len();
  let out: string[] = [];
  if (sepLen == 0) {
    return __split_scalars(s);
  }
  let start: i32 = 0;
  let hit: i32 = __str_find_from(s, n, sep, sepLen, start);
  while (hit >= 0) {
    out = out.append(slice_unchecked(s, start, hit) + "");
    start = hit + sepLen;
    hit = __str_find_from(s, n, sep, sepLen, start);
  }
  return out.append(slice_unchecked(s, start, n) + "");
}

pub function (s: string) rsplit(sep: string): string[] {
  let fwd: string[] = s.split(sep);
  let out: string[] = [];
  let i: i32 = fwd.len() - 1;
  while (i >= 0) {
    out = out.append(fwd[i]);
    i = i - 1;
  }
  return out;
}

pub function (s: string) splitn(sep: string, n: i32): string[] {
  let out: string[] = [];
  if (n <= 0) {
    return out;
  }
  if (n == 1) {
    return out.append(s);
  }
  let sepLen: i32 = sep.len();
  let sLen: i32 = s.len();
  let i: i32 = 0;
  let start: i32 = 0;
  if (sepLen == 0) {
    while (i < sLen && out.len() < n - 1) {
      let step: i32 = utf8_scalar.step(s, i, sLen);
      out = out.append(slice_unchecked(s, i, i + step) + "");
      i = i + step;
    }
    return out.append(slice_unchecked(s, i, sLen) + "");
  }
  let hit: i32 = __str_find_from(s, sLen, sep, sepLen, start);
  while (hit >= 0 && out.len() < n - 1) {
    out = out.append(slice_unchecked(s, start, hit) + "");
    start = hit + sepLen;
    hit = __str_find_from(s, sLen, sep, sepLen, start);
  }
  return out.append(slice_unchecked(s, start, sLen) + "");
}

pub function (s: string) rsplitn(sep: string, n: i32): string[] {
  let out: string[] = [];
  if (n <= 0) {
    return out;
  }
  let fwd: string[] = s.split(sep);
  let k: i32 = fwd.len();
  if (k <= n) {
    let a: i32 = k - 1;
    while (a >= 0) {
      out = out.append(fwd[a]);
      a = a - 1;
    }
    return out;
  }
  let i: i32 = k - 1;
  while (i >= k - (n - 1)) {
    out = out.append(fwd[i]);
    i = i - 1;
  }
  let head: string = fwd[0];
  let j: i32 = 1;
  while (j <= i) {
    head = head + sep + fwd[j];
    j = j + 1;
  }
  return out.append(head);
}

pub function (s: string) first(): Option[u8] {
  if (s.len() == 0) {
    return None;
  }
  return Some(s[0]);
}
```
### `27 pub function (s: string) last(): Option[u8]`

```fern
<<27 pub function (s: string) last(): Option[u8]>>=
pub function (s: string) last(): Option[u8] {
  let n: i32 = s.len();
  if (n == 0) {
    return None;
  }
  return Some(s[n - 1]);
}
```
### `28 function __floor_boundary(s: string, i: i32): i32`

```fern
<<28 function __floor_boundary(s: string, i: i32): i32>>=
function __floor_boundary(s: string, i: i32): i32 {
  let n: i32 = s.len();
  let k: i32 = i;
  if (k < 0) {
    k = 0;
  }
  if (k > n) {
    k = n;
  }
  while (k > 0 && k < n) {
    let c: i32 = s[k] as i32;
    if (c < 128 || c >= 192) {
      break;
    }
    k = k - 1;
  }
  return k;
}

function __ceil_boundary(s: string, i: i32): i32 {
  let n: i32 = s.len();
  let k: i32 = i;
  if (k < 0) {
    k = 0;
  }
  if (k > n) {
    k = n;
  }
  while (k < n) {
    let c: i32 = s[k] as i32;
    if (c < 128 || c >= 192) {
      break;
    }
    k = k + 1;
  }
  return k;
}
```
### `29 pub function (s: string) take(n: i32): string`

```fern
<<29 pub function (s: string) take(n: i32): string>>=
pub function (s: string) take(n: i32): string {
  if (n <= 0) {
    return "";
  }
  let sLen: i32 = s.len();
  if (n >= sLen) {
    return s;
  }
  return slice_unchecked(s, 0, __floor_boundary(s, n)) + "";
}

pub function (s: string) drop(n: i32): string {
  if (n <= 0) {
    return s;
  }
  let sLen: i32 = s.len();
  if (n >= sLen) {
    return "";
  }
  return slice_unchecked(s, __floor_boundary(s, n), sLen) + "";
}

pub function (s: string) slice_snap(a: i32, b: i32): string {
  let lo: i32 = __ceil_boundary(s, a);
  let hi: i32 = __floor_boundary(s, b);
  if (lo >= hi) {
    return "";
  }
  return slice_unchecked(s, lo, hi) + "";
}

pub function (s: string) chunks(size: i32): string[] {
  let sLen: i32 = s.len();
  let out: string[] = [];
  if (sLen == 0) {
    return out;
  }
  if (size <= 0) {
    return out.append(s);
  }
  let i: i32 = 0;
  while (i < sLen) {
    let end: i32 = i + size;
    if (end > sLen) {
      end = sLen;
    } else {
      end = __floor_boundary(s, end);
      if (end <= i) {
        end = __ceil_boundary(s, i + size);
      }
    }
    out = out.append(slice_unchecked(s, i, end) + "");
    i = end;
  }
  return out;
}

pub function (s: string) lines(): string[] {
  let n: i32 = s.len();
  let out: string[] = [];
  if (n == 0) {
    return out;
  }
  let start: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (s[i] == 10) {
      let end: i32 = i;
      if (end > start) {
        if (s[end - 1] == 13) {
          end = end - 1;
        }
      }
      out = out.append(slice_unchecked(s, start, end) + "");
      i = i + 1;
      start = i;
    } else {
      i = i + 1;
    }
  }
  if (start < n) {
    let endTail: i32 = n;
    if (endTail > start) {
      if (s[endTail - 1] == 13) {
        endTail = endTail - 1;
      }
    }
    out = out.append(slice_unchecked(s, start, endTail) + "");
  }
  return out;
}

pub function (s: string) fields(): string[] {
  let n: i32 = s.len();
  let out: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    while (i < n && __is_ascii_ws(s[i] as i32)) {
      i = i + 1;
    }
    if (i >= n) {
      break;
    }
    let start: i32 = i;
    while (i < n && !__is_ascii_ws(s[i] as i32)) {
      i = i + 1;
    }
    out = out.append(slice_unchecked(s, start, i) + "");
  }
  return out;
}
```
### `30 pub function (s: string) eq_ignore_ascii_case(other: string): boolean`

```fern
<<30 pub function (s: string) eq_ignore_ascii_case(other: string): boolean>>=
pub function (s: string) eq_ignore_ascii_case(other: string): boolean {
  let n: i32 = s.len();
  if (n != other.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    let a: i32 = s[i] as i32;
    let b: i32 = other[i] as i32;
    if (a >= 65 && a <= 90) {
      a = a + 32;
    }
    if (b >= 65 && b <= 90) {
      b = b + 32;
    }
    if (a != b) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: string) cmp_ignore_ascii_case(other: string): i32 {
  let n: i32 = s.len();
  let m: i32 = other.len();
  let min: i32 = n;
  if (m < n) {
    min = m;
  }
  let i: i32 = 0;
  while (i < min) {
    let ca: u8 = s[i].to_ascii_lower();
    let cb: u8 = other[i].to_ascii_lower();
    if (ca < cb) {
      return -1;
    }
    if (ca > cb) {
      return 1;
    }
    i = i + 1;
  }
  if (n < m) {
    return -1;
  }
  if (n > m) {
    return 1;
  }
  return 0;
}

pub function (s: string) strip_prefix(prefix: string): Option[string] {
  if (!s.starts_with(prefix)) {
    return None;
  }
  return Some(slice_unchecked(s, prefix.len(), s.len()).to_owned());
}

pub function (s: string) strip_suffix(suffix: string): Option[string] {
  if (!s.ends_with(suffix)) {
    return None;
  }
  let k: i32 = s.len() - suffix.len();
  return Some(slice_unchecked(s, 0, k).to_owned());
}

pub function (s: string) remove_prefix(prefix: string): string {
  if (!s.starts_with(prefix)) {
    return s;
  }
  return slice_unchecked(s, prefix.len(), s.len()) + "";
}

pub function (s: string) remove_suffix(suffix: string): string {
  if (!s.ends_with(suffix)) {
    return s;
  }
  let k: i32 = s.len() - suffix.len();
  return slice_unchecked(s, 0, k) + "";
}
```
### `31 function __fill_lead_len(b: i32): i32`

```fern
<<31 function __fill_lead_len(b: i32): i32>>=
function __fill_lead_len(b: i32): i32 {
  if (b < 128) {
    return 1;
  }
  if (b >= 240) {
    return 4;
  }
  if (b >= 224) {
    return 3;
  }
  if (b >= 192) {
    return 2;
  }
  return 1;
}

function __fill_bytes(ch: string, need: i32): u8[] {
  let clen: i32 = __fill_lead_len(ch[0] as i32);
  if (clen > ch.len()) {
    clen = ch.len();
  }
  let total: i32 = need / clen * clen;
  let buf: u8[] = __alloc_u8(total);
  let i: i32 = 0;
  while (i < total) {
    buf = buf.with(i, ch[i % clen]);
    i = i + 1;
  }
  return buf;
}
```
### `32 pub function (s: string) pad_start(n: i32, ch: char): string`

```fern
<<32 pub function (s: string) pad_start(n: i32, ch: char): string>>=
pub function (s: string) pad_start(n: i32, ch: char): string {
  let sl: i32 = s.len();
  if (sl >= n) {
    return s;
  }
  let fill: u8[] = __fill_bytes(utf8_scalar.from_codepoint(ch), n - sl);
  let fl: i32 = fill.len();
  let buf: u8[] = __alloc_u8(fl + sl);
  let i: i32 = 0;
  while (i < fl) {
    buf = buf.with(i, fill[i]);
    i = i + 1;
  }
  let j: i32 = 0;
  while (j < sl) {
    buf = buf.with(fl + j, s[j] as u8);
    j = j + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) pad_end(n: i32, ch: char): string {
  let sl: i32 = s.len();
  if (sl >= n) {
    return s;
  }
  let fill: u8[] = __fill_bytes(utf8_scalar.from_codepoint(ch), n - sl);
  let fl: i32 = fill.len();
  let buf: u8[] = __alloc_u8(sl + fl);
  let j: i32 = 0;
  while (j < sl) {
    buf = buf.with(j, s[j] as u8);
    j = j + 1;
  }
  let i: i32 = 0;
  while (i < fl) {
    buf = buf.with(sl + i, fill[i]);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) zfill(width: i32): string {
  let sl: i32 = s.len();
  if (sl >= width) {
    return s;
  }
  let has_sign: boolean = sl > 0 && (s[0] == 45 || s[0] == 43);
  let need: i32 = width - sl;
  let buf: u8[] = __alloc_u8(width);
  let pos: i32 = 0;
  let body_start: i32 = 0;
  if (has_sign) {
    buf = buf.with(0, s[0] as u8);
    pos = 1;
    body_start = 1;
  }
  let z: i32 = 0;
  while (z < need) {
    buf = buf.with(pos, 48 as u8);
    pos = pos + 1;
    z = z + 1;
  }
  let j: i32 = body_start;
  while (j < sl) {
    buf = buf.with(pos, s[j] as u8);
    pos = pos + 1;
    j = j + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) split_once(sep: string): Option[(string, string)] {
  if (sep.len() == 0) {
    return None;
  }
  let n: i32 = s.len();
  let sLen: i32 = sep.len();
  let hit: i32 = __str_find_from(s, n, sep, sLen, 0);
  if (hit < 0) {
    return None;
  }
  return Some((slice_unchecked(s, 0, hit).to_owned(), slice_unchecked(s, hit + sLen, n).to_owned()));
}

pub function (s: string) partition(sep: string): (string, string, string) {
  if (sep.len() == 0) {
    return (s.to_owned(), "", "");
  }
  let n: i32 = s.len();
  let sLen: i32 = sep.len();
  let hit: i32 = __str_find_from(s, n, sep, sLen, 0);
  if (hit < 0) {
    return (s.to_owned(), "", "");
  }
  return (slice_unchecked(s, 0, hit).to_owned(), sep.to_owned(), slice_unchecked(s, hit + sLen, n).to_owned());
}

pub function (s: string) rpartition(sep: string): (string, string, string) {
  if (sep.len() == 0) {
    return ("", "", s.to_owned());
  }
  let n: i32 = s.len();
  let sLen: i32 = sep.len();
  let i: i32 = __str_rfind_from(s, n, sep, sLen);
  if (i < 0) {
    return ("", "", s.to_owned());
  }
  return (slice_unchecked(s, 0, i).to_owned(), sep.to_owned(), slice_unchecked(s, i + sLen, n).to_owned());
}

pub function (s: string) rsplit_once(sep: string): Option[(string, string)] {
  if (sep.len() == 0) {
    return None;
  }
  let n: i32 = s.len();
  let sLen: i32 = sep.len();
  let i: i32 = __str_rfind_from(s, n, sep, sLen);
  if (i < 0) {
    return None;
  }
  return Some((slice_unchecked(s, 0, i).to_owned(), slice_unchecked(s, i + sLen, n).to_owned()));
}
```
### `33 pub function (s: string) trim_start_matches(sub: string): string`

```fern
<<33 pub function (s: string) trim_start_matches(sub: string): string>>=
pub function (s: string) trim_start_matches(sub: string): string {
  if (sub.len() == 0) {
    return s;
  }
  let n: i32 = s.len();
  let subLen: i32 = sub.len();
  let i: i32 = 0;
  while (i + subLen <= n && __substr_eq(s, i, sub, subLen)) {
    i = i + subLen;
  }
  return slice_unchecked(s, i, n) + "";
}

pub function (s: string) trim_end_matches(sub: string): string {
  if (sub.len() == 0) {
    return s;
  }
  let n: i32 = s.len();
  let subLen: i32 = sub.len();
  let end: i32 = n;
  while (end - subLen >= 0 && __substr_eq(s, end - subLen, sub, subLen)) {
    end = end - subLen;
  }
  return slice_unchecked(s, 0, end) + "";
}

pub function (s: string) contains_ci(needle: string): boolean {
  return __str_index_ci(s, needle) >= 0;
}

pub function (s: string) index_of_ci(needle: string): Option[i32] {
  return __str_some(__str_index_ci(s, needle));
}
```
### `34 function __str_index_ci(s: string, needle: string): i32`

```fern
<<34 function __str_index_ci(s: string, needle: string): i32>>=
function __str_index_ci(s: string, needle: string): i32 {
  let nLen: i32 = needle.len();
  if (nLen == 0) {
    return 0;
  }
  let sLen: i32 = s.len();
  if (nLen > sLen) {
    return 0 - 1;
  }
  let last: i32 = sLen - nLen;
  let i: i32 = 0;
  while (i <= last) {
    let j: i32 = 0;
    let ok: boolean = true;
    while (j < nLen) {
      if (s[i + j].to_ascii_lower() != needle[j].to_ascii_lower()) {
        ok = false;
        j = nLen;
      } else {
        j = j + 1;
      }
    }
    if (ok) {
      return i;
    }
    i = i + 1;
  }
  return 0 - 1;
}
```
### `35 pub function (s: string) pad_start_str(width: i32, fill: string): string`

```fern
<<35 pub function (s: string) pad_start_str(width: i32, fill: string): string>>=
pub function (s: string) pad_start_str(width: i32, fill: string): string {
  let sLen: i32 = s.len();
  if (sLen >= width || fill.len() == 0) {
    return s;
  }
  let pad: string = "";
  while (pad.len() + fill.len() <= width - sLen) {
    pad = pad + fill;
  }
  if (pad.len() < width - sLen) {
    pad = pad + fill.take(width - sLen - pad.len());
  }
  return pad + s;
}

pub function (s: string) pad_end_str(width: i32, fill: string): string {
  let sLen: i32 = s.len();
  if (sLen >= width || fill.len() == 0) {
    return s;
  }
  let pad: string = "";
  while (pad.len() + fill.len() <= width - sLen) {
    pad = pad + fill;
  }
  if (pad.len() < width - sLen) {
    pad = pad + fill.take(width - sLen - pad.len());
  }
  return s + pad;
}

pub function (s: string) center(width: i32, ch: char): string {
  let sLen: i32 = s.len();
  if (sLen >= width) {
    return s;
  }
  let total: i32 = width - sLen;
  let lpad: i32 = total / 2;
  let rpad: i32 = total - lpad;
  return string_from_bytes_unchecked(__fill_bytes(utf8_scalar.from_codepoint(ch), lpad)) + s + string_from_bytes_unchecked(__fill_bytes(utf8_scalar.from_codepoint(ch), rpad));
}

pub function (s: string) reverse_words(): string {
  let words: string[] = s.fields();
  return words.reverse().join(" ");
}

pub function (s: string) truncate(n: i32, ellipsis: string): string {
  if (n <= 0) {
    return "";
  }
  let sLen: i32 = s.len();
  if (sLen <= n) {
    return s;
  }
  let eLen: i32 = ellipsis.len();
  if (n <= eLen) {
    return s.slice_snap(0, n).to_owned();
  }
  return s.slice_snap(0, n - eLen) + ellipsis;
}

pub function (s: string) ellipsis(n: i32): string {
  if (s.len() <= n) {
    return s;
  }
  if (n < 3) {
    if (n <= 0) {
      return "";
    }
    return slice_unchecked("...", 0, n).to_owned();
  }
  if (n == 3) {
    return "...";
  }
  return s.slice_snap(0, n - 3) + "...";
}

pub function (s: string) first_line(): Option[string] {
  if (s.len() == 0) {
    return None;
  }
  let idx: i32 = __str_index(s, "\n");
  if (idx < 0) {
    return Some(s);
  }
  return Some(slice_unchecked(s, 0, idx).to_owned());
}

pub function (s: string) at(i: i32): Option[u8] {
  if (i < 0 || i >= s.len()) {
    return None;
  }
  return Some(s[i]);
}

pub function (s: string) to_array(): string[] {
  return __split_scalars(s);
}
```
### `36 function __split_scalars(s: string): string[]`

```fern
<<36 function __split_scalars(s: string): string[]>>=
function __split_scalars(s: string): string[] {
  let n: i32 = s.len();
  let out: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    let step: i32 = utf8_scalar.step(s, i, n);
    out = out.append(slice_unchecked(s, i, i + step) + "");
    i = i + step;
  }
  return out;
}
```
### `41 pub function (s: string) reverse_bytes(): u8[]`

```fern
<<41 pub function (s: string) reverse_bytes(): u8[]>>=
pub function (s: string) reverse_bytes(): u8[] {
  let n: i32 = s.len();
  let buf: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    buf = buf.with(i, s[n - 1 - i] as u8);
    i = i + 1;
  }
  return buf;
}

pub function (s: string) count(sub: string): i32 {
  let subLen: i32 = sub.len();
  if (subLen == 0) {
    return 0;
  }
  if (subLen == 1) {
    return __count_byte(s, sub[0] as i32);
  }
  let n: i32 = s.len();
  let count: i32 = 0;
  let hit: i32 = __str_find_from(s, n, sub, subLen, 0);
  while (hit >= 0) {
    count = count + 1;
    hit = __str_find_from(s, n, sub, subLen, hit + subLen);
  }
  return count;
}

pub function (s: string) find_all(sub: string): i32[] {
  let out: i32[] = [];
  if (sub.len() == 0) {
    return out;
  }
  let n: i32 = s.len();
  let subLen: i32 = sub.len();
  let hit: i32 = __str_find_from(s, n, sub, subLen, 0);
  while (hit >= 0) {
    out = out.append(hit);
    hit = __str_find_from(s, n, sub, subLen, hit + subLen);
  }
  return out;
}

pub function (s: string) count_byte(b: u8): i32 {
  return __count_byte(s, b as i32);
}

pub function (s: string) replace_n(old: string, new: string, limit: i32): string {
  let n: i32 = s.len();
  let oLen: i32 = old.len();
  if (oLen == 0 || limit <= 0) {
    return s;
  }
  let matches: i32 = 0;
  let hit: i32 = __str_find_from(s, n, old, oLen, 0);
  while (hit >= 0 && matches < limit) {
    matches = matches + 1;
    hit = __str_find_from(s, n, old, oLen, hit + oLen);
  }
  if (matches == 0) {
    return s;
  }
  let nLen: i32 = new.len();
  let out_len: i32 = n + matches * (nLen - oLen);
  if (out_len == 0) {
    return "";
  }
  let buf: u8[] = __alloc_u8(out_len);
  let pos: i32 = 0;
  let i: i32 = 0;
  hit = __str_find_from(s, n, old, oLen, 0);
  let done: i32 = 0;
  while (hit >= 0 && done < matches) {
    while (i < hit) {
      buf = buf.with(pos, s[i] as u8);
      pos = pos + 1;
      i = i + 1;
    }
    let k: i32 = 0;
    while (k < nLen) {
      buf = buf.with(pos, new[k] as u8);
      pos = pos + 1;
      k = k + 1;
    }
    i = hit + oLen;
    done = done + 1;
    hit = __str_find_from(s, n, old, oLen, i);
  }
  while (i < n) {
    buf = buf.with(pos, s[i] as u8);
    pos = pos + 1;
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) remove_all(needle: string): string {
  return s.replace(needle, "");
}

pub function (s: string) before(sep: string): string {
  let idx: i32 = __str_index(s, sep);
  if (idx < 0) {
    return s;
  }
  return slice_unchecked(s, 0, idx) + "";
}

pub function (s: string) after(sep: string): string {
  if (sep.len() == 0) {
    return s;
  }
  let idx: i32 = __str_index(s, sep);
  if (idx < 0) {
    return "";
  }
  return slice_unchecked(s, idx + sep.len(), s.len()) + "";
}
```
### `42 pub function (s: string) between(start: string, end: string): Option[string]`

```fern
<<42 pub function (s: string) between(start: string, end: string): Option[string]>>=
pub function (s: string) between(start: string, end: string): Option[string] {
  if (start.len() == 0 || end.len() == 0) {
    return None;
  }
  let i: i32 = __str_index(s, start);
  if (i < 0) {
    return None;
  }
  let begin: i32 = i + start.len();
  let n: i32 = s.len();
  let endLen: i32 = end.len();
  let j: i32 = begin;
  while (j + endLen <= n) {
    if (__substr_eq(s, j, end, endLen)) {
      return Some(slice_unchecked(s, begin, j).to_owned());
    }
    j = j + 1;
  }
  return None;
}

pub function (s: string) line_count(): i32 {
  let n: i32 = s.len();
  if (n == 0) {
    return 0;
  }
  let c: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (s[i] == 10) {
      c = c + 1;
    }
    i = i + 1;
  }
  if (s[n - 1] != 10) {
    c = c + 1;
  }
  return c;
}

pub function (s: string) repeat_with_sep(n: i32, sep: string): string {
  if (n <= 0) {
    return "";
  }
  let sl: i32 = s.len();
  let pl: i32 = sep.len();
  let total: i32 = sl * n + pl * (n - 1);
  if (total == 0) {
    return "";
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  let k: i32 = 0;
  while (k < n) {
    if (k > 0) {
      let pj: i32 = 0;
      while (pj < pl) {
        buf = buf.with(pos, sep[pj] as u8);
        pos = pos + 1;
        pj = pj + 1;
      }
    }
    let j: i32 = 0;
    while (j < sl) {
      buf = buf.with(pos, s[j] as u8);
      pos = pos + 1;
      j = j + 1;
    }
    k = k + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (s: string) replace(old: string, new: string): string {
  return s.replace_n(old, new, 2147483647);
}
```
### `43 pub function (s: string) parse_bool(): Option[boolean]`

```fern
<<43 pub function (s: string) parse_bool(): Option[boolean]>>=
pub function (s: string) parse_bool(): Option[boolean] {
  if (s == "true" || s == "1") {
    return Some(true);
  }
  if (s == "false" || s == "0") {
    return Some(false);
  }
  return None;
}

pub function (s: string) is_http_safe_method(): boolean {
  return s == "GET" || s == "HEAD" || s == "OPTIONS" || s == "TRACE";
}

pub function (s: string) is_http_idempotent_method(): boolean {
  if (s.is_http_safe_method()) {
    return true;
  }
  return s == "PUT" || s == "DELETE";
}
```

### `50 pub function repeat_char(ch: char, n: i32): string`

```fern
<<50 pub function repeat_char(ch: char, n: i32): string>>=
pub function repeat_char(ch: char, n: i32): string {
  if (n <= 0) {
    return "";
  }
  return utf8_scalar.from_codepoint(ch).repeat(n);
}

pub(package) function __is_ascii_ws(b: i32): boolean {
  return b == 32 || b >= 9 && b <= 13;
}

pub function (s: string) replace_at(i: i32, new: string): string {
  let n: i32 = s.len();
  if (i < 0 || i >= n) {
    return s;
  }
  if (__floor_boundary(s, i) != i) {
    return s;
  }
  let end: i32 = i + utf8_scalar.step(s, i, n);
  if (new.len() == 0) {
    return slice_unchecked(s, 0, i) + slice_unchecked(s, end, n);
  }
  let width: i32 = utf8_scalar.step(new, 0, new.len());
  return slice_unchecked(s, 0, i) + slice_unchecked(new, 0, width) + slice_unchecked(s, end, n);
}

pub function (s: string) without_byte(b: u8): u8[] {
  let n: i32 = s.len();
  let buf: u8[] = __alloc_u8(n);
  let pos: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (s[i] as i32 != b as i32) {
      buf = buf.with(pos, s[i] as u8);
      pos = pos + 1;
    }
    i = i + 1;
  }
  let out: u8[] = __alloc_u8(pos);
  let k: i32 = 0;
  while (k < pos) {
    out = out.with(k, buf[k]);
    k = k + 1;
  }
  return out;
}

pub function (s: string) repeat_to(target: i32): string {
  let sLen: i32 = s.len();
  if (sLen == 0 || target <= 0) {
    return "";
  }
  let copies: i32 = target / sLen;
  return s.repeat(copies);
}
```