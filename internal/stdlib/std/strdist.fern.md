# String edit distance

This module calculates distances between strings.

## Model

Levenshtein distance counts the minimum edits between two strings. Similarity provides a normalized comparison derived from the distance.

## Contracts

Distance is zero for equal strings. The numeric result measures text difference and does not establish semantic similarity.

## Executable example

```fern test name=std-strdist-example
import "std/strdist";

function main(): i32 {
  if (strdist.levenshtein("kitten", "sitting") != 3 || strdist.levenshtein("a", "a") != 0) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function levenshtein(a: string, b: string): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calculates distances between strings.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/utf8";
```

### `3 pub function levenshtein(a: string, b: string): i32`

```fern
<<3 pub function levenshtein(a: string, b: string): i32>>=
pub function levenshtein(a: string, b: string): i32 {
  let ca = a.chars();
  let cb = b.chars();
  let n = ca.len();
  let m = cb.len();
  if (n == 0) {
    return m;
  }
  if (m == 0) {
    return n;
  }
  let prev: i32[] = [];
  let j = 0;
  while (j <= m) {
    prev = prev.append(j);
    j += 1;
  }
  let i = 1;
  while (i <= n) {
    let cur: i32[] = [];
    cur = cur.append(i);
    let k = 1;
    while (k <= m) {
      let cost = 1;
      if (ca[i - 1] == cb[k - 1]) {
        cost = 0;
      }
      let del = prev[k] + 1;
      let ins = cur[k - 1] + 1;
      let sub = prev[k - 1] + cost;
      let best = del;
      if (ins < best) {
        best = ins;
      }
      if (sub < best) {
        best = sub;
      }
      cur = cur.append(best);
      k += 1;
    }
    prev = cur;
    i += 1;
  }
  return prev[m];
}

pub function similarity(a: string, b: string): f64 {
  let la = a.codepoint_count();
  let lb = b.codepoint_count();
  let longer = la;
  if (lb > longer) {
    longer = lb;
  }
  if (longer == 0) {
    return 1.0;
  }
  let d = levenshtein(a, b);
  return 1.0 - d as f64 / (longer as f64);
}
```
