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
  let ca: char[] = a.chars();
  let cb: char[] = b.chars();
  let n: i32 = ca.len();
  let m: i32 = cb.len();
  if (n == 0) {
    return m;
  }
  if (m == 0) {
    return n;
  }
  let prev: i32[] = [];
  let j: i32 = 0;
  while (j <= m) {
    prev = prev.append(j);
    j = j + 1;
  }
  let i: i32 = 1;
  while (i <= n) {
    let cur: i32[] = [];
    cur = cur.append(i);
    let k: i32 = 1;
    while (k <= m) {
      let cost: i32 = 1;
      if (ca[i - 1] == cb[k - 1]) {
        cost = 0;
      }
      let del: i32 = prev[k] + 1;
      let ins: i32 = cur[k - 1] + 1;
      let sub: i32 = prev[k - 1] + cost;
      let best: i32 = del;
      if (ins < best) {
        best = ins;
      }
      if (sub < best) {
        best = sub;
      }
      cur = cur.append(best);
      k = k + 1;
    }
    prev = cur;
    i = i + 1;
  }
  return prev[m];
}

pub function similarity(a: string, b: string): f64 {
  let la: i32 = a.codepoint_count();
  let lb: i32 = b.codepoint_count();
  let longer: i32 = la;
  if (lb > longer) {
    longer = lb;
  }
  if (longer == 0) {
    return 1.0;
  }
  let d: i32 = levenshtein(a, b);
  return 1.0 - d as f64 / (longer as f64);
}
```
