# ANSI terminal styles

This module adds ANSI terminal colors and text styles.

## Model

Style functions surround text with ANSI SGR sequences. The module constructs text and does not inspect the terminal or environment.

## Contracts

A caller decides whether terminal output permits these sequences. The CLI module provides environment-aware color policy.

## Executable example

```fern test name=std-ansi-example
import "std/ansi";
import "std/string";

function main(): i32 {
  let text = ansi.red("error");
  if (!text.contains("error") || text.len() <= 5) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 function __esc(): string>>

<<3 pub function sgr(code: string, s: string): string>>

<<4 pub function fg_rgb(r: i32, g: i32, b: i32, s: string): string>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module adds ANSI terminal colors and text styles.
import "std/i32";
```

### `2 function __esc(): string`

```fern
<<2 function __esc(): string>>=
function __esc(): string {
  let b: u8[] = [27 as u8];
  return string_from_bytes_unchecked(b);
}
```

### `3 pub function sgr(code: string, s: string): string`

```fern
<<3 pub function sgr(code: string, s: string): string>>=
pub function sgr(code: string, s: string): string {
  let e: string = __esc();
  return e + "[" + code + "m" + s + e + "[0m";
}

pub function black(s: string): string {
  return sgr("30", s);
}

pub function red(s: string): string {
  return sgr("31", s);
}

pub function green(s: string): string {
  return sgr("32", s);
}

pub function yellow(s: string): string {
  return sgr("33", s);
}

pub function blue(s: string): string {
  return sgr("34", s);
}

pub function magenta(s: string): string {
  return sgr("35", s);
}

pub function cyan(s: string): string {
  return sgr("36", s);
}

pub function white(s: string): string {
  return sgr("37", s);
}

pub function bright_black(s: string): string {
  return sgr("90", s);
}

pub function bright_red(s: string): string {
  return sgr("91", s);
}

pub function bright_green(s: string): string {
  return sgr("92", s);
}

pub function bright_yellow(s: string): string {
  return sgr("93", s);
}

pub function bright_blue(s: string): string {
  return sgr("94", s);
}

pub function bright_magenta(s: string): string {
  return sgr("95", s);
}

pub function bright_cyan(s: string): string {
  return sgr("96", s);
}

pub function bright_white(s: string): string {
  return sgr("97", s);
}

pub function bg_black(s: string): string {
  return sgr("40", s);
}

pub function bg_red(s: string): string {
  return sgr("41", s);
}

pub function bg_green(s: string): string {
  return sgr("42", s);
}

pub function bg_yellow(s: string): string {
  return sgr("43", s);
}

pub function bg_blue(s: string): string {
  return sgr("44", s);
}

pub function bg_magenta(s: string): string {
  return sgr("45", s);
}

pub function bg_cyan(s: string): string {
  return sgr("46", s);
}

pub function bg_white(s: string): string {
  return sgr("47", s);
}

pub function bold(s: string): string {
  return sgr("1", s);
}

pub function dim(s: string): string {
  return sgr("2", s);
}

pub function italic(s: string): string {
  return sgr("3", s);
}

pub function underline(s: string): string {
  return sgr("4", s);
}

pub function reverse(s: string): string {
  return sgr("7", s);
}

pub function strikethrough(s: string): string {
  return sgr("9", s);
}

pub function fg_256(n: i32, s: string): string {
  return sgr("38;5;" + n.to_string(), s);
}

pub function bg_256(n: i32, s: string): string {
  return sgr("48;5;" + n.to_string(), s);
}
```

### `4 pub function fg_rgb(r: i32, g: i32, b: i32, s: string): string`

```fern
<<4 pub function fg_rgb(r: i32, g: i32, b: i32, s: string): string>>=
pub function fg_rgb(r: i32, g: i32, b: i32, s: string): string {
  return sgr("38;2;" + r.to_string() + ";" + g.to_string() + ";" + b.to_string(), s);
}

pub function bg_rgb(r: i32, g: i32, b: i32, s: string): string {
  return sgr("48;2;" + r.to_string() + ";" + g.to_string() + ";" + b.to_string(), s);
}

pub function strip(s: string): string {
  let n: i32 = s.len();
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    if (s[i] == 27 && i + 1 < n && s[i + 1] == 91) {
      i = i + 2;
      while (i < n && s[i] != 109) {
        i = i + 1;
      }
      if (i < n) {
        i = i + 1;
      }
    } else {
      out = out.append(s[i] as u8);
      i = i + 1;
    }
  }
  return string_from_bytes_unchecked(out);
}
```
