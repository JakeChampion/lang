# Text format helpers

This module formats values as text.

## Model

Format helpers substitute values through their display traits. Byte-count and duration helpers provide domain-specific text forms.

## Contracts

The numbered helpers retain each argument type through a generic bound. The duration parser returns an option because input text can be invalid. A width or a precision in a format specification saturates at one million, so a hostile specification cannot make a format allocate without bound.

## Executable example

```fern test name=std-format-example
import "std/format";

function main(): i32 {
  if (format.format("Hello, {}", ["Ada"]) != "Hello, Ada") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function format(fmt: string, args: string[]): string>>

<<4 function __format_apply_spec(s: string, spec: string): string>>

<<5 function __round_to_places(s: string, places: i32): string>>

<<6 pub function bytes(n: i64): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module formats values as text.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i64";
import "std/string";
import "std/time";
import "std/utf8" as utf8;
import "core/cmp" as cmp;
import "core/error";
```

### `3 pub function format(fmt: string, args: string[]): string`

```fern
<<3 pub function format(fmt: string, args: string[]): string>>=
pub function format(fmt: string, args: string[]): string {
  let n = fmt.len();
  let out: u8[] = [];
  let i = 0;
  let argi = 0;
  while (i < n) {
    if (i + 1 < n && fmt[i] == 123 && fmt[i + 1] == 123) {
      out = out.append(123 as u8);
      i += 2;
    } else if (i + 1 < n && fmt[i] == 125 && fmt[i + 1] == 125) {
      out = out.append(125 as u8);
      i += 2;
    } else if (fmt[i] == 123) {
      let j = i + 1;
      while (j < n && fmt[j] != 125) {
        j += 1;
      }
      let isPlaceholder = false;
      let spec: str = "";
      if (j < n) {
        spec = slice_unchecked(fmt, i + 1, j);
        if (spec.len() == 0) {
          isPlaceholder = true;
        } else if (spec[0] == 58) {
          isPlaceholder = true;
        }
      }
      if (isPlaceholder) {
        if (argi < args.len()) {
          let piece = __format_apply_spec(args[argi], spec);
          let pl = piece.len();
          let pk = 0;
          while (pk < pl) {
            out = out.append(piece[pk] as u8);
            pk += 1;
          }
          argi += 1;
        } else {
          out = out.append(123 as u8);
          let sl = spec.len();
          let sk = 0;
          while (sk < sl) {
            out = out.append(spec[sk] as u8);
            sk += 1;
          }
          out = out.append(125 as u8);
        }
        i = j + 1;
      } else {
        out = out.append(fmt[i] as u8);
        i += 1;
      }
    } else {
      out = out.append(fmt[i] as u8);
      i += 1;
    }
  }
  return string_from_bytes_unchecked(out);
}

pub function values[T: cmp.Display](fmt: string, args: T[]): string {
  let strs: string[] = [];
  let i = 0;
  while (i < args.len()) {
    strs = strs.append(args[i].to_string());
    i += 1;
  }
  return format(fmt, strs);
}

pub function format1[A: cmp.Display](fmt: string, a: A): string {
  return format(fmt, [a.to_string()]);
}

pub function format2[A: cmp.Display, B: cmp.Display](fmt: string, a: A, b: B): string {
  return format(fmt, [a.to_string(), b.to_string()]);
}

pub function format3[A: cmp.Display, B: cmp.Display, C: cmp.Display](fmt: string, a: A, b: B, c: C): string {
  return format(fmt, [a.to_string(), b.to_string(), c.to_string()]);
}

pub function format4[A: cmp.Display, B: cmp.Display, C: cmp.Display, D: cmp.Display](fmt: string, a: A, b: B, c: C, d: D): string {
  return format(fmt, [a.to_string(), b.to_string(), c.to_string(), d.to_string()]);
}
```

### `4 function __format_apply_spec(s: string, spec: string): string`

```fern
<<4 function __format_apply_spec(s: string, spec: string): string>>=
function __format_apply_spec(s: string, spec: string): string {
  if (spec.len() == 0) {
    return s;
  }
  let m = spec.len();
  let p = 1;
  let fill: str = " ";
  let align = 1;
  let fw = 1;
  match (utf8.decode_at(spec, p)) {
    Some(pair) => {
      fw = pair.1;
    },
    None => {}
  }
  if (p + fw < m && __is_align(spec[p + fw] as i32)) {
    fill = slice_unchecked(spec, p, p + fw);
    align = __align_code(spec[p + fw] as i32);
    p = p + fw + 1;
  } else if (p < m && __is_align(spec[p] as i32)) {
    align = __align_code(spec[p] as i32);
    p += 1;
  }
  let plus = false;
  if (p < m && spec[p] == 43) {
    plus = true;
    p += 1;
  } else if (p < m && spec[p] == 45) {
    p += 1;
  }
  let zero = false;
  if (p < m && spec[p] == 48) {
    zero = true;
    p += 1;
  }
  let width = 0;
  while (p < m && spec[p] >= 48 && spec[p] <= 57) {
    width = __bounded_count(width, spec[p] as i32 - 48);
    p += 1;
  }
  let val = s;
  if (p < m && spec[p] == 46) {
    p += 1;
    let prec = 0;
    while (p < m && spec[p] >= 48 && spec[p] <= 57) {
      prec = __bounded_count(prec, spec[p] as i32 - 48);
      p += 1;
    }
    if (__is_decimal_numeral(val)) {
      val = __round_to_places(val, prec);
    } else if (val.len() > prec) {
      val = val.slice_snap(0, prec).to_owned();
    }
  }
  if (plus && val.len() > 0 && val[0] >= 48 && val[0] <= 57) {
    val = "+" + val;
  }
  let vlen = val.len();
  if (vlen >= width) {
    return val;
  }
  let pad = width - vlen;
  if (zero) {
    if (val.len() > 0 && (val[0] == 45 || val[0] == 43)) {
      return slice_unchecked(val, 0, 1) + __repeat_char("0", pad) + slice_unchecked(val, 1, val.len());
    }
    return __repeat_char("0", pad) + val;
  }
  if (align == 2) {
    return __repeat_char(fill, pad) + val;
  }
  if (align == 3) {
    let left = pad / 2;
    return __repeat_char(fill, left) + val + __repeat_char(fill, pad - left);
  }
  return val + __repeat_char(fill, pad);
}

function __is_decimal_numeral(s: string): boolean {
  let n = s.len();
  let i = 0;
  if (i < n && (s[i] == 43 || s[i] == 45)) {
    i += 1;
  }
  let whole = 0;
  while (i < n && s[i] >= 48 && s[i] <= 57) {
    i += 1;
    whole += 1;
  }
  if (whole == 0) {
    return false;
  }
  if (i == n) {
    return true;
  }
  if (s[i] != 46) {
    return false;
  }
  i += 1;
  let frac = 0;
  while (i < n && s[i] >= 48 && s[i] <= 57) {
    i += 1;
    frac += 1;
  }
  return i == n && frac > 0;
}
```

### `5 function __round_to_places(s: string, places: i32): string`

```fern
<<5 function __round_to_places(s: string, places: i32): string>>=
function __round_to_places(s: string, places: i32): string {
  let n = s.len();
  let i = 0;
  let sign = "";
  if (s[0] == 43 || s[0] == 45) {
    if (s[0] == 45) {
      sign = "-";
    }
    i = 1;
  }
  let dot = i;
  while (dot < n && s[dot] != 46) {
    dot += 1;
  }
  let intLen = dot - i;
  let digits: u8[] = [];
  let k = i;
  while (k < dot) {
    digits = digits.append(s[k] as u8);
    k += 1;
  }
  let f = 0;
  while (f < places) {
    let src = dot + 1 + f;
    if (src < n) {
      digits = digits.append(s[src] as u8);
    } else {
      digits = digits.append(48 as u8);
    }
    f += 1;
  }
  let next = dot + 1 + places;
  if (next < n && s[next] >= 53) {
    let c = digits.len() - 1;
    let carry = true;
    while (carry && c >= 0) {
      let d = digits[c] as i32 + 1;
      if (d > 57) {
        digits = digits.with(c, 48 as u8);
        c -= 1;
      } else {
        digits = digits.with(c, d as u8);
        carry = false;
      }
    }
    if (carry) {
      let grown: u8[] = [49 as u8];
      let g = 0;
      while (g < digits.len()) {
        grown = grown.append(digits[g]);
        g += 1;
      }
      digits = grown;
      intLen += 1;
    }
  }
  let head: u8[] = [];
  let j = 0;
  while (j < intLen) {
    head = head.append(digits[j]);
    j += 1;
  }
  let out = sign + string_from_bytes_unchecked(head);
  if (places > 0) {
    let tail: u8[] = [];
    while (j < digits.len()) {
      tail = tail.append(digits[j]);
      j += 1;
    }
    out = out + "." + string_from_bytes_unchecked(tail);
  }
  return out;
}

function __is_align(c: i32): boolean {
  return c == 60 || c == 62 || c == 94;
}

function __align_code(c: i32): i32 {
  if (c == 60) {
    return 1;
  }
  if (c == 62) {
    return 2;
  }
  return 3;
}

// A width or precision saturates at one million, so a hostile specification
// cannot make a format allocate without bound.
function __bounded_count(acc: i32, digit: i32): i32 {
  if (acc >= 1000000) {
    return 1000000;
  }
  let next = acc * 10 + digit;
  if (next > 1000000) {
    return 1000000;
  }
  return next;
}

function __repeat_char(s: string, count: i32): string {
  if (count <= 0) {
    return "";
  }
  let sl = s.len();
  if (sl == 0) {
    return "";
  }
  let buf = __alloc_u8(sl * count);
  let pos = 0;
  let k = 0;
  while (k < count) {
    let j = 0;
    while (j < sl) {
      buf = buf.with(pos, s[j] as u8);
      pos += 1;
      j += 1;
    }
    k += 1;
  }
  return string_from_bytes_unchecked(buf);
}
```

### `6 pub function bytes(n: i64): string`

```fern
<<6 pub function bytes(n: i64): string>>=
pub function bytes(n: i64): string {
  let units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let u = 0;
  let scale = 1;
  while (u + 1 < units.len() && (n / scale >= 1024 || n / scale <= -1024)) {
    scale *= 1024;
    u += 1;
  }
  return (n / scale).to_string() + " " + units[u];
}

// Hours, minutes, seconds, and milliseconds, such as `1h 1m 1s 1ms`.
pub function duration(d: Duration): string {
  let ms = d.as_millis();
  if (ms == 0) {
    return "0ms";
  }
  let neg = ms < 0;
  let mag = ms;
  if (neg) {
    mag = -ms;
  }
  let sign = "";
  if (neg) {
    sign = "-";
  }
  let h = mag / 3600000;
  let rem = mag - h * 3600000;
  let m = rem / 60000;
  rem -= m * 60000;
  let s = rem / 1000;
  let msPart = rem - s * 1000;
  let out = "";
  if (h > 0) {
    out = out + h.to_string() + "h";
  }
  if (m > 0) {
    if (out.len() > 0) {
      out += " ";
    }
    out = out + m.to_string() + "m";
  }
  if (s > 0) {
    if (out.len() > 0) {
      out += " ";
    }
    out = out + s.to_string() + "s";
  }
  if (msPart > 0) {
    if (out.len() > 0) {
      out += " ";
    }
    out = out + msPart.to_string() + "ms";
  }
  return sign + out;
}

// Reads numbers with the units `ms`, `s`, `m`, `h`, and `d`, such as `1h 30m`.
// The text `parse_duration` refused, and the byte where reading stopped.
pub struct DurationParseError { text: string, offset: i32 }

impl error.Error for DurationParseError {}

impl cmp.Display for DurationParseError {}

pub function (e: DurationParseError) message(): string {
  return "\"" + e.text + "\" is not a duration such as \"1h 30m\": stopped at byte " + e.offset.to_string();
}

pub function (e: DurationParseError) to_string(): string {
  return e.message();
}

pub function parse_duration(s: string): Result[Duration, DurationParseError] {
  let n = s.len();
  let i = 0;
  let total: i64 = 0;
  let saw = false;
  while (i < n) {
    if (s[i] == 32) {
      i += 1;
      continue;
    }
    let start = i;
    let num: i64 = 0;
    while (i < n && s[i] >= 48 && s[i] <= 57) {
      num = num * (10 as i64) + (s[i] - 48) as i64;
      i += 1;
    }
    if (i == start) {
      return Err(DurationParseError { text: s, offset: i });
    }
    let unit_ms: i64 = 0;
    if (i + 1 < n && s[i] == 109 && s[i + 1] == 115) {
      unit_ms = 1;
      i += 2;
    } else if (i < n && s[i] == 115) {
      unit_ms = 1000;
      i += 1;
    } else if (i < n && s[i] == 109) {
      unit_ms = 60000;
      i += 1;
    } else if (i < n && s[i] == 104) {
      unit_ms = 3600000;
      i += 1;
    } else if (i < n && s[i] == 100) {
      unit_ms = 86400000;
      i += 1;
    } else {
      return Err(DurationParseError { text: s, offset: i });
    }
    total = total + num * unit_ms;
    saw = true;
  }
  if (!saw) {
    return Err(DurationParseError { text: s, offset: i });
  }
  return Ok(time.duration_millis(total));
}
```
