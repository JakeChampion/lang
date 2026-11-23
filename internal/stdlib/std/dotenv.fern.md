# Environment-file values

This module parses environment files into maps of names and values.

## Model

The parser turns environment-file text into a string map. It handles assignment syntax and quoted values without a process-environment update.

Each assignment is `NAME=value`, with an optional `export ` before it. White space around the name and the value is not part of them. A line that starts with `#` is a comment. A line with no `=` or with no name is ignored. The last assignment of a name wins.

- An unquoted value ends at the end of its line. A `#` at the start of the value or after white space starts a comment, so `URL=http://host/#top` keeps its fragment.
- A value in single quotes is literal.
- A value in double quotes reads the escapes `\n`, `\t`, `\r`, `\\`, `\"` and `\'`. Other backslash pairs stay as they are.
- A quoted value can continue across lines. Only white space and a comment can follow the quote that closes it.
- A value whose quote does not close in this way is an unquoted value, quotes included.

## Contracts

The returned map owns the parsed names and values. A caller decides how these values interact with the actual process environment.

## Executable example

```fern test name=std-dotenv-example
import "std/dotenv";
import "core/map";
import "std/option";

function main(): i32 {
  let values = dotenv.parse("NAME=Ada\nMODE=test\n");
  if (values.get("NAME").unwrap_or("") != "Ada") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __de_is_space(c: i32): boolean>>

<<4 pub function parse(s: string): Map[string, string]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module parses environment files into maps of names and values.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/map";
import "std/string";
```

### `3 function __de_is_space(c: i32): boolean`

```fern
<<3 function __de_is_space(c: i32): boolean>>=
function __de_is_space(c: i32): boolean {
  return c == 32 || c == 9 || c == 13;
}

// The index of the "\n" that ends the line of `i`, or the length of `s`.
function __de_line_end(s: string, i: i32): i32 {
  let n = s.len();
  while (i < n && s[i] != 10) {
    i += 1;
  }
  return i;
}

function __de_skip_space(s: string, a: i32, b: i32): i32 {
  while (a < b && __de_is_space(s[a] as i32)) {
    a += 1;
  }
  return a;
}

function __de_trimmed(s: string, a: i32, b: i32): string {
  a = __de_skip_space(s, a, b);
  while (b > a && __de_is_space(s[b - 1] as i32)) {
    b -= 1;
  }
  return slice_unchecked(s, a, b) + "";
}

// The index of the quote that closes a value, or -1. A backslash in a
// double-quoted value makes the next byte a literal.
function __de_close(s: string, i: i32, quote: u8): i32 {
  let n = s.len();
  while (i < n) {
    if (s[i] == quote) {
      return i;
    }
    if (quote == 34 && s[i] == 92) {
      i += 1;
    }
    i += 1;
  }
  return -1;
}

// The end of an unquoted value: the start of its comment, or `b`. A "#"
// starts a comment only at the start of the value or after white space.
function __de_comment(s: string, a: i32, b: i32): i32 {
  let i = a;
  while (i < b) {
    if (s[i] == 35 && (i == a || __de_is_space(s[i - 1] as i32))) {
      return i;
    }
    i += 1;
  }
  return b;
}

function __de_unescape(s: string): string {
  let out: u8[] = [];
  let i = 0;
  while (i < s.len()) {
    if (s[i] == 92 && i + 1 < s.len()) {
      let n = s[i + 1] as i32;
      if (n == 110) {
        out = out.append(10 as u8);
      } else if (n == 116) {
        out = out.append(9 as u8);
      } else if (n == 114) {
        out = out.append(13 as u8);
      } else if (n == 92) {
        out = out.append(92 as u8);
      } else if (n == 34) {
        out = out.append(34 as u8);
      } else if (n == 39) {
        out = out.append(39 as u8);
      } else {
        out = out.append(92 as u8);
        out = out.append(n as u8);
      }
      i += 2;
    } else {
      out = out.append(s[i] as u8);
      i += 1;
    }
  }
  return unsafe { string_from_bytes_unchecked(out) };
}

// The value that starts at `a`, and the index of the line after it. `eol`
// is the end of the line of `a`.
function __de_value(s: string, a: i32, eol: i32): (string, i32) {
  a = __de_skip_space(s, a, eol);
  if (a < eol && (s[a] == 34 || s[a] == 39)) {
    let close = __de_close(s, a + 1, s[a]);
    if (close >= 0) {
      let end = __de_line_end(s, close);
      let rest = __de_skip_space(s, close + 1, end);
      if (rest == end || s[rest] == 35) {
        let body = slice_unchecked(s, a + 1, close) + "";
        if (s[a] == 34) {
          return (__de_unescape(body), end + 1);
        }
        return (body, end + 1);
      }
    }
  }
  return (__de_trimmed(s, a, __de_comment(s, a, eol)), eol + 1);
}
```

### `4 pub function parse(s: string): Map[string, string]`

```fern
<<4 pub function parse(s: string): Map[string, string]>>=
pub function parse(s: string): Map[string, string] {
  let m: Map[string, string] = map_new(8);
  let n = s.len();
  let i = 0;
  while (i < n) {
    let eol = __de_line_end(s, i);
    let a = __de_skip_space(s, i, eol);
    i = eol + 1;
    if (a == eol || s[a] == 35) {
      continue;
    }
    if (slice_unchecked(s, a, eol).starts_with("export ")) {
      a = __de_skip_space(s, a + 7, eol);
    }
    let eq = a;
    while (eq < eol && s[eq] != 61) {
      eq += 1;
    }
    if (eq == eol) {
      continue;
    }
    let key = __de_trimmed(s, a, eq);
    if (key.len() == 0) {
      continue;
    }
    let (val, next) = __de_value(s, eq + 1, eol);
    m = m.insert(key, val);
    i = next;
  }
  return m;
}
```
