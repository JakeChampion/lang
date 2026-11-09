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

// Each step maps the text positions that the pattern so far can reach
// to the positions after one more pattern token.
function __glob_match(p: string, t: string): boolean {
  let cur: boolean[] = [true];
  for k in 0..t.len() {
    cur = cur.append(false);
  }
  let pi = 0;
  while (pi < p.len()) {
    let pc = p[pi] as i32;
    if (pc == 42 && pi + 1 < p.len() && p[pi + 1] == 42) {
      while (pi < p.len() && p[pi] == 42) {
        pi += 1;
      }
      let slash = pi < p.len() && p[pi] == 47;
      cur = __glob_globstar(cur, t, slash);
      if (slash) {
        pi += 1;
      }
    } else if (pc == 42) {
      cur = __glob_star(cur, t);
      pi += 1;
    } else {
      cur = __glob_one(cur, p, pi, t);
      pi = pi + 1;
      if (pc == 91) {
        pi = __glob_class(p, pi - 1, 0).1;
      }
    }
    if (!__glob_any(cur)) {
      return false;
    }
  }
  return cur[t.len()];
}

function __glob_any(cur: boolean[]): boolean {
  for v in cur {
    if (v) {
      return true;
    }
  }
  return false;
}

function __glob_globstar(cur: boolean[], t: string, slash: boolean): boolean[] {
  let next: boolean[] = [];
  let seen = false;
  for k in 0..cur.len() {
    if (slash) {
      next = next.append(cur[k] || seen && t[k - 1] == 47);
    } else {
      next = next.append(cur[k] || seen);
    }
    seen = seen || cur[k];
  }
  return next;
}

function __glob_star(cur: boolean[], t: string): boolean[] {
  let next: boolean[] = [];
  let carry = false;
  for k in 0..cur.len() {
    let v = cur[k] || carry;
    carry = v && k < t.len() && t[k] != 47;
    next = next.append(v);
  }
  return next;
}

function __glob_one(cur: boolean[], p: string, pi: i32, t: string): boolean[] {
  let pc = p[pi] as i32;
  let next: boolean[] = [false];
  for k in 0..t.len() {
    let ch = t[k] as i32;
    if (!cur[k]) {
      next = next.append(false);
    } else if (pc == 63) {
      next = next.append(ch != 47);
    } else if (pc == 91) {
      next = next.append(ch != 47 && __glob_class(p, pi, ch).0);
    } else {
      next = next.append(ch == pc);
    }
  }
  return next;
}
```

### `3 pub function is_match(pattern: string, text: string): boolean`

```fern
<<3 pub function is_match(pattern: string, text: string): boolean>>=
pub function is_match(pattern: string, text: string): boolean {
  return __glob_match(pattern, text);
}
```
