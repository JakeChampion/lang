# Shell-style pattern matches

This module compares strings with shell glob patterns.

## Model

The matcher interprets shell glob syntax against a supplied string. Pattern evaluation does not access the filesystem.

## Contracts

A match tests the pattern against the complete input. Callers that require filesystem expansion must enumerate paths separately.

## Executable example

```fern test name=std-glob-example
import "std/glob";

function main(): i32 {
  if (!glob.is_match("*.fern", "main.fern")) { return 1; }
  if (glob.is_match("a?c", "ac")) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 function __glob_class(p: string, start: i32, ch: i32): (boolean, i32)>>

<<3 pub function is_match(pattern: string, text: string): boolean>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module compares strings with shell glob patterns.
```

### `2 function __glob_class(p: string, start: i32, ch: i32): (boolean, i32)`

```fern
<<2 function __glob_class(p: string, start: i32, ch: i32): (boolean, i32)>>=
function __glob_class(p: string, start: i32, ch: i32): (boolean, i32) {
  let i = start + 1;
  let negate = false;
  if (i < p.len() && (p[i] == 33 || p[i] == 94)) {
    negate = true;
    i += 1;
  }
  let matched = false;
  let first = true;
  while (i < p.len() && (p[i] != 93 || first)) {
    first = false;
    if (i + 2 < p.len() && p[i + 1] == 45 && p[i + 2] != 93) {
      if (ch >= p[i] as i32 && ch <= p[i + 2] as i32) {
        matched = true;
      }
      i += 3;
    } else {
      if (ch == p[i] as i32) {
        matched = true;
      }
      i += 1;
    }
  }
  let next = i;
  if (i < p.len()) {
    next = i + 1;
  }
  if (negate) {
    return (!matched, next);
  }
  return (matched, next);
}

function __glob_match(p: string, i: i32, t: string, j: i32): boolean {
  let pi = i;
  let tj = j;
  while (pi < p.len()) {
    let pc = p[pi] as i32;
    if (pc == 42) {
      if (pi + 1 < p.len() && p[pi + 1] == 42) {
        let ni = pi;
        while (ni < p.len() && p[ni] == 42) {
          ni += 1;
        }
        if (ni < p.len() && p[ni] == 47) {
          if (__glob_match(p, ni + 1, t, tj)) {
            return true;
          }
        }
        let k = tj;
        while (k <= t.len()) {
          if (__glob_match(p, ni, t, k)) {
            return true;
          }
          k += 1;
        }
        return false;
      }
      let m = tj;
      let cont = true;
      while (cont) {
        if (__glob_match(p, pi + 1, t, m)) {
          return true;
        }
        if (m >= t.len() || t[m] == 47) {
          cont = false;
        } else {
          m += 1;
        }
      }
      return false;
    } else if (pc == 63) {
      if (tj >= t.len() || t[tj] == 47) {
        return false;
      }
      pi += 1;
      tj += 1;
    } else if (pc == 91) {
      if (tj >= t.len() || t[tj] == 47) {
        return false;
      }
      let res = __glob_class(p, pi, t[tj] as i32);
      if (!res.0) {
        return false;
      }
      pi = res.1;
      tj += 1;
    } else {
      if (tj >= t.len() || t[tj] as i32 != pc) {
        return false;
      }
      pi += 1;
      tj += 1;
    }
  }
  return tj == t.len();
}
```

### `3 pub function is_match(pattern: string, text: string): boolean`

```fern
<<3 pub function is_match(pattern: string, text: string): boolean>>=
pub function is_match(pattern: string, text: string): boolean {
  return __glob_match(pattern, 0, text, 0);
}
```
