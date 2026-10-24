# Environment-file values

This module parses environment files into maps of names and values.

## Model

The parser turns environment-file text into a string map. It handles assignment syntax and quoted values without a process-environment update.

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

function __de_slice(s: string, a: i32, b: i32): string {
  let out: u8[] = [];
  let i: i32 = a;
  while (i < b) {
    out = out.append(s[i] as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(out);
}

function __de_trim(s: string): string {
  let a: i32 = 0;
  let b: i32 = s.len();
  while (a < b && __de_is_space(s[a] as i32)) {
    a = a + 1;
  }
  while (b > a && __de_is_space(s[b - 1] as i32)) {
    b = b - 1;
  }
  return __de_slice(s, a, b);
}

function __de_unescape(s: string): string {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < s.len()) {
    if (s[i] == 92 && i + 1 < s.len()) {
      let n: i32 = s[i + 1] as i32;
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
      i = i + 2;
    } else {
      out = out.append(s[i] as u8);
      i = i + 1;
    }
  }
  return string_from_bytes_unchecked(out);
}
```

### `4 pub function parse(s: string): Map[string, string]`

```fern
<<4 pub function parse(s: string): Map[string, string]>>=
pub function parse(s: string): Map[string, string] {
  let m: Map[string, string] = map_new(8);
  let lines: string[] = s.split("\n");
  let li: i32 = 0;
  while (li < lines.len()) {
    let line: string = __de_trim(lines[li]);
    li = li + 1;
    if (line.len() == 0) {
      continue;
    }
    if (line[0] == 35) {
      continue;
    }
    if (line.starts_with("export ")) {
      line = __de_trim(__de_slice(line, 7, line.len()));
    }
    let Some(eq) = line.index_of("=") else {
      continue;
    };
    let key: string = __de_trim(__de_slice(line, 0, eq));
    if (key.len() == 0) {
      continue;
    }
    let raw: string = __de_trim(__de_slice(line, eq + 1, line.len()));
    let val: string = raw;
    if (raw.len() >= 2 && raw[0] == 34 && raw[raw.len() - 1] == 34) {
      val = __de_unescape(__de_slice(raw, 1, raw.len() - 1));
    } else if (raw.len() >= 2 && raw[0] == 39 && raw[raw.len() - 1] == 39) {
      val = __de_slice(raw, 1, raw.len() - 1);
    }
    m = m.insert(key, val);
  }
  return m;
}
```
