# Array operations and statistics

This module provides array methods.

## Model

Receiver methods extend arrays with statistics, transforms, and set operations. Specialized integer, float, and string methods retain their element types.

## Contracts

Operations that require an element use options for empty input. Integer averages retain integer arithmetic. Collection results must replace the caller value when an update is required.

## Executable example

```fern test name=std-array-example
import "std/array";
import "std/option";

function main(): i32 {
  let values: i32[] = [2, 4, 6];
  if (values.avg().unwrap_or(0) != 4) { return 1; }
  let empty: i32[] = [];
  if (empty.avg().is_some()) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function (arr: i32[]) avg(): Option[i32]>>

<<4 pub function (arr: string[]) join(sep: string): string>>

<<5 pub function (arr: string[]) all_starts_with(prefix: string): boolean>>

<<6 pub function variance_f64(arr: f64[]): Option[f64]>>

<<7 pub function first[T](xs: T[]): Option[T]>>

<<8 pub function min_by_i32_key[T](xs: T[], key: (T)>>

<<9 pub function rposition[T](xs: T[], pred: (T)>>

<<10 pub function drop_last[T](xs: T[], n: i32): T[]>>

<<11 pub function scan[T, A](xs: T[], init: A, f: (A, T)>>

<<12 pub function difference[T: cmp.Eq](a: T[], b: T[]): T[]>>

<<13 pub function (xs: T[]) rotate_left(n: i32): T[]>>

<<14 pub function (xs: T[]) count[T: cmp.Eq](target: T): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides array methods.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "std/i32";
import "std/string";
import "std/num";
```

### `3 pub function (arr: i32[]) avg(): Option[i32]`

```fern
<<3 pub function (arr: i32[]) avg(): Option[i32]>>=
// avg returns the integer mean, or None for an empty array.
pub function (arr: i32[]) avg(): Option[i32] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let s: i64 = 0;
  let i: i32 = 0;
  while (i < n) {
    s = s + arr[i] as i64;
    i = i + 1;
  }
  return Some((s / (n as i64)) as i32);
}

pub function (arr: i32[]) gcd_all(): Option[i32] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let g: i32 = arr[0].abs();
  let i: i32 = 1;
  while (i < n) {
    g = g.gcd(arr[i].abs());
    i = i + 1;
  }
  return Some(g);
}

pub function (arr: i32[]) lcm_all(): Option[i32] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let l: i32 = arr[0].abs();
  let i: i32 = 1;
  while (i < n) {
    l = l.lcm(arr[i].abs());
    i = i + 1;
  }
  return Some(l);
}

pub function (arr: i32[]) abs_each(): i32[] {
  let n: i32 = arr.len();
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(arr[i].abs());
    i = i + 1;
  }
  return out;
}

pub function (arr: i32[]) min_max(): Option[(i32, i32)] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let lo: i32 = arr[0];
  let hi: i32 = arr[0];
  let i: i32 = 1;
  while (i < n) {
    if (arr[i] < lo) {
      lo = arr[i];
    }
    if (arr[i] > hi) {
      hi = arr[i];
    }
    i = i + 1;
  }
  return Some((lo, hi));
}

pub function (arr: i32[]) every_positive(): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (arr[i] <= 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (arr: i32[]) sum_squared(): i32 {
  let n: i32 = arr.len();
  let s: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    s = s + arr[i] * arr[i];
    i = i + 1;
  }
  return s;
}

pub function (arr: i32[]) median(): Option[i32] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let sorted: i32[] = cmp.sort(arr);
  return Some(sorted[(n - 1) / 2]);
}

pub function (arr: i32[]) mode(): Option[i32] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let best: i32 = arr[0];
  let best_count: i32 = 1;
  let i: i32 = 0;
  while (i < n) {
    let c: i32 = arr.count(arr[i]);
    if (c > best_count) {
      best = arr[i];
      best_count = c;
    }
    i = i + 1;
  }
  return Some(best);
}
```

### `4 pub function (arr: string[]) join(sep: string): string`

```fern
<<4 pub function (arr: string[]) join(sep: string): string>>=
pub function (arr: string[]) join(sep: string): string {
  let n: i32 = arr.len();
  if (n == 0) {
    return "";
  }
  if (n == 1) {
    return arr[0];
  }
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    total = total + arr[i].len();
    i = i + 1;
  }
  total = total + sep.len() * (n - 1);
  let b: usize = buf_new(total);
  i = 0;
  while (i < n) {
    if (i > 0) {
      buf_push(b, sep);
    }
    buf_push(b, arr[i]);
    i = i + 1;
  }
  let out: string = buf_take(b);
  buf_free(b);
  return out;
}

pub function (arr: string[]) filter_non_empty(): string[] {
  let n: i32 = arr.len();
  let out: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    if (arr[i].len() > 0) {
      out = out.append(arr[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (arr: string[]) count_non_empty(): i32 {
  let n: i32 = arr.len();
  let c: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (arr[i].len() > 0) {
      c = c + 1;
    }
    i = i + 1;
  }
  return c;
}

pub function (arr: string[]) distinct_count(): i32 {
  let n: i32 = arr.len();
  let seen: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    if (!cmp.contains(seen, arr[i])) {
      seen = seen.append(arr[i]);
    }
    i = i + 1;
  }
  return seen.len();
}

pub function (arr: string[]) max_by_len(): Option[string] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let best: string = arr[0];
  let i: i32 = 1;
  while (i < n) {
    if (arr[i].len() > best.len()) {
      best = arr[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function (arr: string[]) min_by_len(): Option[string] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let best: string = arr[0];
  let i: i32 = 1;
  while (i < n) {
    if (arr[i].len() < best.len()) {
      best = arr[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function (arr: string[]) sum_lens(): i32 {
  let n: i32 = arr.len();
  let s: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    s = s + arr[i].len();
    i = i + 1;
  }
  return s;
}

pub function (arr: string[]) all_non_empty(): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (arr[i].len() == 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (arr: string[]) any_contains(needle: string): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (arr[i].contains(needle)) {
      return true;
    }
    i = i + 1;
  }
  return false;
}
```

### `5 pub function (arr: string[]) all_starts_with(prefix: string): boolean`

```fern
<<5 pub function (arr: string[]) all_starts_with(prefix: string): boolean>>=
pub function (arr: string[]) all_starts_with(prefix: string): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (!arr[i].starts_with(prefix)) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (arr: string[]) all_ends_with(suffix: string): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (!arr[i].ends_with(suffix)) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (arr: string[]) join_with_last(sep: string, last_sep: string): string {
  let n: i32 = arr.len();
  if (n == 0) {
    return "";
  }
  if (n == 1) {
    return arr[0];
  }
  if (n == 2) {
    return arr[0] + last_sep + arr[1];
  }
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    total = total + arr[i].len();
    i = i + 1;
  }
  total = total + sep.len() * (n - 2) + last_sep.len();
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    if (i > 0) {
      let sp: string = sep;
      if (i == n - 1) {
        sp = last_sep;
      }
      let spl: i32 = sp.len();
      let j: i32 = 0;
      while (j < spl) {
        buf = buf.with(pos, sp[j] as u8);
        pos = pos + 1;
        j = j + 1;
      }
    }
    let e: string = arr[i];
    let el: i32 = e.len();
    let k: i32 = 0;
    while (k < el) {
      buf = buf.with(pos, e[k] as u8);
      pos = pos + 1;
      k = k + 1;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function (arr: i32[]) sum_abs(): i32 {
  let n: i32 = arr.len();
  let s: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    s = s + arr[i].abs();
    i = i + 1;
  }
  return s;
}

pub function (arr: i32[]) all_zero(): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (arr[i] != 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (arr: string[]) all_eq_str(target: string): boolean {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (arr[i] != target) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function avg_i64(arr: i64[]): Option[i64] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let total: i64 = 0;
  let i: i32 = 0;
  while (i < n) {
    total = total + arr[i];
    i = i + 1;
  }
  return Some(total / (n as i64));
}

pub function avg_f64(arr: f64[]): Option[f64] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let total: f64 = 0.0;
  let i: i32 = 0;
  while (i < n) {
    total = total + arr[i];
    i = i + 1;
  }
  return Some(total / (n as f64));
}
```

### `6 pub function variance_f64(arr: f64[]): Option[f64]`

```fern
<<6 pub function variance_f64(arr: f64[]): Option[f64]>>=
pub function variance_f64(arr: f64[]): Option[f64] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let msum: f64 = 0.0;
  let mi: i32 = 0;
  while (mi < n) {
    msum = msum + arr[mi];
    mi = mi + 1;
  }
  let mean: f64 = msum / (n as f64);
  let acc: f64 = 0.0;
  let i: i32 = 0;
  while (i < n) {
    let d: f64 = arr[i] - mean;
    acc = acc + d * d;
    i = i + 1;
  }
  return Some(acc / (n as f64));
}

pub function stddev_f64(arr: f64[]): Option[f64] {
  match (variance_f64(arr)) {
    Some(v) => {
      return Some(__sqrt_f64(v));
    },
    None => {
      return None;
    }
  }
}

pub function median_f64(arr: f64[]): Option[f64] {
  let n: i32 = arr.len();
  if (n == 0) {
    return None;
  }
  let sorted: f64[] = cmp.sort(arr);
  if (n % 2 == 1) {
    return Some(sorted[n / 2]);
  }
  let hi: i32 = n / 2;
  return Some((sorted[hi - 1] + sorted[hi]) * 0.5);
}

pub function dot_f64(a: f64[], b: f64[]): f64 {
  let n: i32 = a.len();
  let m: i32 = b.len();
  if (m < n) {
    n = m;
  }
  let s: f64 = 0.0;
  let i: i32 = 0;
  while (i < n) {
    s = s + a[i] * b[i];
    i = i + 1;
  }
  return s;
}

pub function norm_f64(arr: f64[]): f64 {
  let n: i32 = arr.len();
  let s: f64 = 0.0;
  let i: i32 = 0;
  while (i < n) {
    s = s + arr[i] * arr[i];
    i = i + 1;
  }
  return __sqrt_f64(s);
}

pub function distance_f64(a: f64[], b: f64[]): f64 {
  let n: i32 = a.len();
  let m: i32 = b.len();
  if (m < n) {
    n = m;
  }
  let s: f64 = 0.0;
  let i: i32 = 0;
  while (i < n) {
    let d: f64 = a[i] - b[i];
    s = s + d * d;
    i = i + 1;
  }
  return __sqrt_f64(s);
}

pub function normalize_f64(arr: f64[]): f64[] {
  let mag: f64 = norm_f64(arr);
  if (mag == 0.0) {
    return arr;
  }
  let out: f64[] = [];
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    out = out.append(arr[i] / mag);
    i = i + 1;
  }
  return out;
}

pub function scale_f64(arr: f64[], k: f64): f64[] {
  return __scale_f64(arr, k);
}

pub function outer_mul_f64(a: f64[], b: f64[]): f64[] {
  return __outer_mul_f64(a, b);
}

pub function inner_mul_add_f64(a: f64[], b: f64[], rows: i32, extent: i32, columns: i32, init: f64): f64[] {
  return __inner_mul_add_f64(a, b, rows, extent, columns, init);
}

pub function add_f64(a: f64[], b: f64[]): f64[] {
  let n: i32 = a.len();
  let m: i32 = b.len();
  if (m < n) {
    n = m;
  }
  let out: f64[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(a[i] + b[i]);
    i = i + 1;
  }
  return out;
}

pub function is_empty[T](xs: T[]): boolean {
  return xs.len() == 0;
}
```

### `7 pub function first[T](xs: T[]): Option[T]`

```fern
<<7 pub function first[T](xs: T[]): Option[T]>>=
pub function first[T](xs: T[]): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  return Some(xs[0]);
}

pub function last[T](xs: T[]): Option[T] {
  let n: i32 = xs.len();
  if (n == 0) {
    return None;
  }
  return Some(xs[n - 1]);
}

pub function get[T](xs: T[], i: i32): Option[T] {
  if (i < 0 || i >= xs.len()) {
    return None;
  }
  return Some(xs[i]);
}

pub function map[T, U](xs: T[], f: (T) => U): U[] {
  let out: U[] = [];
  for x in xs {
    out = out.append(f(x));
  }
  return out;
}

pub function filter[T](xs: T[], keep: (T) => boolean): T[] {
  let out: T[] = [];
  for x in xs {
    if (keep(x)) {
      out = out.append(x);
    }
  }
  return out;
}

pub function fold[T, A](xs: T[], init: A, f: (A, T) => A): A {
  let acc: A = init;
  for x in xs {
    acc = f(acc, x);
  }
  return acc;
}

pub function any[T](xs: T[], pred: (T) => boolean): boolean {
  for x in xs {
    if (pred(x)) {
      return true;
    }
  }
  return false;
}

pub function all[T](xs: T[], pred: (T) => boolean): boolean {
  for x in xs {
    if (!pred(x)) {
      return false;
    }
  }
  return true;
}

pub function none[T](xs: T[], pred: (T) => boolean): boolean {
  for x in xs {
    if (pred(x)) {
      return false;
    }
  }
  return true;
}

pub function find[T](xs: T[], pred: (T) => boolean): Option[T] {
  for x in xs {
    if (pred(x)) {
      return Some(x);
    }
  }
  return None;
}

pub function find_last[T](xs: T[], pred: (T) => boolean): Option[T] {
  let i: i32 = xs.len() - 1;
  while (i >= 0) {
    if (pred(xs[i])) {
      return Some(xs[i]);
    }
    i = i - 1;
  }
  return None;
}

pub function find_map[T, U](xs: T[], f: (T) => Option[U]): Option[U] {
  for x in xs {
    match (f(x)) {
      Some(v) => {
        return Some(v);
      },
      None => {}
    }
  }
  return None;
}

pub function max_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let best: T = xs[0];
  let best_k: i32 = key(best);
  let i: i32 = 1;
  while (i < xs.len()) {
    let k: i32 = key(xs[i]);
    if (k > best_k) {
      best = xs[i];
      best_k = k;
    }
    i = i + 1;
  }
  return Some(best);
}
```

### `8 pub function min_by_i32_key[T](xs: T[], key: (T)`

```fern
<<8 pub function min_by_i32_key[T](xs: T[], key: (T)>>=
pub function min_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let best: T = xs[0];
  let best_k: i32 = key(best);
  let i: i32 = 1;
  while (i < xs.len()) {
    let k: i32 = key(xs[i]);
    if (k < best_k) {
      best = xs[i];
      best_k = k;
    }
    i = i + 1;
  }
  return Some(best);
}

pub function max_index[T: cmp.Ord](xs: T[]): Option[i32] {
  if (xs.len() == 0) {
    return None;
  }
  let best: i32 = 0;
  let i: i32 = 1;
  while (i < xs.len()) {
    if (xs[i].cmp(xs[best]) > 0) {
      best = i;
    }
    i = i + 1;
  }
  return Some(best);
}

pub function min_index[T: cmp.Ord](xs: T[]): Option[i32] {
  if (xs.len() == 0) {
    return None;
  }
  let best: i32 = 0;
  let i: i32 = 1;
  while (i < xs.len()) {
    if (xs[i].cmp(xs[best]) < 0) {
      best = i;
    }
    i = i + 1;
  }
  return Some(best);
}

pub function running_max[T: cmp.Ord](xs: T[]): T[] {
  let out: T[] = [];
  if (xs.len() == 0) {
    return out;
  }
  let best: T = xs[0];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i].cmp(best) > 0) {
      best = xs[i];
    }
    out = out.append(best);
    i = i + 1;
  }
  return out;
}

pub function running_min[T: cmp.Ord](xs: T[]): T[] {
  let out: T[] = [];
  if (xs.len() == 0) {
    return out;
  }
  let best: T = xs[0];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i].cmp(best) < 0) {
      best = xs[i];
    }
    out = out.append(best);
    i = i + 1;
  }
  return out;
}

pub function max_by[T](xs: T[], cmp: (T, T) => i32): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let best: T = xs[0];
  let i: i32 = 1;
  while (i < xs.len()) {
    if (cmp(xs[i], best) > 0) {
      best = xs[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function min_by[T](xs: T[], cmp: (T, T) => i32): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let best: T = xs[0];
  let i: i32 = 1;
  while (i < xs.len()) {
    if (cmp(xs[i], best) < 0) {
      best = xs[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function position[T](xs: T[], pred: (T) => boolean): Option[i32] {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (pred(xs[i])) {
      return Some(i);
    }
    i = i + 1;
  }
  return None;
}

pub function find_indices[T](xs: T[], pred: (T) => boolean): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (pred(xs[i])) {
      out = out.append(i);
    }
    i = i + 1;
  }
  return out;
}
```

### `9 pub function rposition[T](xs: T[], pred: (T)`

```fern
<<9 pub function rposition[T](xs: T[], pred: (T)>>=
pub function rposition[T](xs: T[], pred: (T) => boolean): Option[i32] {
  let i: i32 = xs.len() - 1;
  while (i >= 0) {
    if (pred(xs[i])) {
      return Some(i);
    }
    i = i - 1;
  }
  return None;
}

pub function count_where[T](xs: T[], pred: (T) => boolean): i32 {
  let c: i32 = 0;
  let i: i32 = 0;
  while (i < xs.len()) {
    if (pred(xs[i])) {
      c = c + 1;
    }
    i = i + 1;
  }
  return c;
}

pub function sum_by[T](xs: T[], key: (T) => i32): i32 {
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < xs.len()) {
    total = total + key(xs[i]);
    i = i + 1;
  }
  return total;
}

pub function take_while[T](xs: T[], pred: (T) => boolean): T[] {
  let out: T[] = [];
  let i: i32 = 0;
  while (i < xs.len() && pred(xs[i])) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function drop_while[T](xs: T[], pred: (T) => boolean): T[] {
  let i: i32 = 0;
  while (i < xs.len() && pred(xs[i])) {
    i = i + 1;
  }
  let out: T[] = [];
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function enumerate[T](xs: T[]): (i32, T)[] {
  let out: (i32, T)[] = [];
  let i: i32 = 0;
  for x in xs {
    out = out.append((i, x));
    i = i + 1;
  }
  return out;
}

pub function reverse[T](xs: T[]): T[] {
  let out: T[] = [];
  let i: i32 = xs.len() - 1;
  while (i >= 0) {
    out = out.append(xs[i]);
    i = i - 1;
  }
  return out;
}

pub function rotate_left[T](xs: T[], n: i32): T[] {
  let len: i32 = xs.len();
  let out: T[] = [];
  if (len == 0) {
    return out;
  }
  let sh: i32 = (n % len + len) % len;
  let i: i32 = 0;
  while (i < len) {
    out = out.append(xs[(i + sh) % len]);
    i = i + 1;
  }
  return out;
}

pub function rotate_right[T](xs: T[], n: i32): T[] {
  return rotate_left(xs, 0 - n);
}

pub function take[T](xs: T[], n: i32): T[] {
  let out: T[] = [];
  let i: i32 = 0;
  while (i < n && i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function drop[T](xs: T[], n: i32): T[] {
  let out: T[] = [];
  let i: i32 = n;
  if (i < 0) {
    i = 0;
  }
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function take_last[T](xs: T[], n: i32): T[] {
  if (n <= 0) {
    return [];
  }
  let start: i32 = xs.len() - n;
  if (start < 0) {
    start = 0;
  }
  return slice(xs, start, xs.len());
}
```

### `10 pub function drop_last[T](xs: T[], n: i32): T[]`

```fern
<<10 pub function drop_last[T](xs: T[], n: i32): T[]>>=
pub function drop_last[T](xs: T[], n: i32): T[] {
  if (n <= 0) {
    return slice(xs, 0, xs.len());
  }
  let end: i32 = xs.len() - n;
  if (end < 0) {
    end = 0;
  }
  return slice(xs, 0, end);
}

pub function slice[T](xs: T[], start: i32, end: i32): T[] {
  let lo: i32 = start;
  let hi: i32 = end;
  if (lo < 0) {
    lo = 0;
  }
  if (hi > xs.len()) {
    hi = xs.len();
  }
  let out: T[] = [];
  let i: i32 = lo;
  while (i < hi) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function chunks[T](xs: T[], size: i32): T[][] {
  let out: T[][] = [];
  if (size <= 0) {
    return out;
  }
  let n: i32 = xs.len();
  let i: i32 = 0;
  while (i < n) {
    let hi: i32 = i + size;
    if (hi > n) {
      hi = n;
    }
    out = out.append(slice(xs, i, hi));
    i = i + size;
  }
  return out;
}

pub function chunks_exact[T](xs: T[], size: i32): T[][] {
  let out: T[][] = [];
  if (size <= 0) {
    return out;
  }
  let n: i32 = xs.len();
  let i: i32 = 0;
  while (i + size <= n) {
    out = out.append(slice(xs, i, i + size));
    i = i + size;
  }
  return out;
}

pub function windows[T](xs: T[], size: i32): T[][] {
  let out: T[][] = [];
  if (size <= 0) {
    return out;
  }
  let n: i32 = xs.len();
  let i: i32 = 0;
  while (i + size <= n) {
    out = out.append(slice(xs, i, i + size));
    i = i + 1;
  }
  return out;
}

pub function concat[T](a: T[], b: T[]): T[] {
  let out: T[] = [];
  for x in a {
    out = out.append(x);
  }
  for y in b {
    out = out.append(y);
  }
  return out;
}

pub function zip[T, U](a: T[], b: U[]): (T, U)[] {
  let out: (T, U)[] = [];
  let n: i32 = a.len();
  if (b.len() < n) {
    n = b.len();
  }
  let i: i32 = 0;
  while (i < n) {
    out = out.append((a[i], b[i]));
    i = i + 1;
  }
  return out;
}

pub function flat_map[T, U](xs: T[], f: (T) => U[]): U[] {
  let out: U[] = [];
  for x in xs {
    for y in f(x) {
      out = out.append(y);
    }
  }
  return out;
}

pub function flatten[T](xss: T[][]): T[] {
  let out: T[] = [];
  for xs in xss {
    for x in xs {
      out = out.append(x);
    }
  }
  return out;
}

pub function partition[T](xs: T[], pred: (T) => boolean): (T[], T[]) {
  let kept: T[] = [];
  let rejected: T[] = [];
  for x in xs {
    if (pred(x)) {
      kept = kept.append(x);
    } else {
      rejected = rejected.append(x);
    }
  }
  return (kept, rejected);
}
```

### `11 pub function scan[T, A](xs: T[], init: A, f: (A, T)`

```fern
<<11 pub function scan[T, A](xs: T[], init: A, f: (A, T)>>=
pub function scan[T, A](xs: T[], init: A, f: (A, T) => A): A[] {
  let out: A[] = [];
  let acc: A = init;
  for x in xs {
    acc = f(acc, x);
    out = out.append(acc);
  }
  return out;
}

pub function intersperse[T](xs: T[], sep: T): T[] {
  let out: T[] = [];
  let i: i32 = 0;
  let n: i32 = xs.len();
  while (i < n) {
    if (i > 0) {
      out = out.append(sep);
    }
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function step_by[T](xs: T[], n: i32): T[] {
  let out: T[] = [];
  if (n <= 0) {
    return out;
  }
  let i: i32 = 0;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + n;
  }
  return out;
}

pub function reduce[T](xs: T[], f: (T, T) => T): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let acc: T = xs[0];
  let i: i32 = 1;
  while (i < xs.len()) {
    acc = f(acc, xs[i]);
    i = i + 1;
  }
  return Some(acc);
}

pub function binary_search[T: cmp.Ord](xs: T[], target: T): Option[i32] {
  let lo: i32 = 0;
  let hi: i32 = xs.len();
  while (lo < hi) {
    let mid: i32 = lo + (hi - lo) / 2;
    let c: i32 = xs[mid].cmp(target);
    if (c == 0) {
      return Some(mid);
    }
    if (c < 0) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return None;
}

pub function all_equal[T: cmp.Eq](xs: T[]): boolean {
  let n: i32 = xs.len();
  if (n < 2) {
    return true;
  }
  let i: i32 = 1;
  while (i < n) {
    if (!xs[i].eq(xs[0])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function dedup[T: cmp.Eq](xs: T[]): T[] {
  let out: T[] = [];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (i == 0 || !xs[i].eq(xs[i - 1])) {
      out = out.append(xs[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function count[T: cmp.Eq](xs: T[], target: T): i32 {
  let c: i32 = 0;
  for x in xs {
    if (x.eq(target)) {
      c = c + 1;
    }
  }
  return c;
}

pub function union[T: cmp.Eq](a: T[], b: T[]): T[] {
  let out: T[] = [];
  for x in a {
    if (!cmp.contains(out, x)) {
      out = out.append(x);
    }
  }
  for y in b {
    if (!cmp.contains(out, y)) {
      out = out.append(y);
    }
  }
  return out;
}

pub function intersection[T: cmp.Eq](a: T[], b: T[]): T[] {
  let out: T[] = [];
  for x in a {
    if (cmp.contains(b, x) && !cmp.contains(out, x)) {
      out = out.append(x);
    }
  }
  return out;
}
```

### `12 pub function difference[T: cmp.Eq](a: T[], b: T[]): T[]`

```fern
<<12 pub function difference[T: cmp.Eq](a: T[], b: T[]): T[]>>=
pub function difference[T: cmp.Eq](a: T[], b: T[]): T[] {
  let out: T[] = [];
  for x in a {
    if (!cmp.contains(b, x) && !cmp.contains(out, x)) {
      out = out.append(x);
    }
  }
  return out;
}

pub function index_of_last[T: cmp.Eq](xs: T[], target: T): Option[i32] {
  let i: i32 = xs.len() - 1;
  while (i >= 0) {
    if (xs[i].eq(target)) {
      return Some(i);
    }
    i = i - 1;
  }
  return None;
}

pub function starts_with[T: cmp.Eq](xs: T[], prefix: T[]): boolean {
  if (prefix.len() > xs.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < prefix.len()) {
    if (!xs[i].eq(prefix[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function ends_with[T: cmp.Eq](xs: T[], suffix: T[]): boolean {
  let n: i32 = xs.len();
  let m: i32 = suffix.len();
  if (m > n) {
    return false;
  }
  let i: i32 = 0;
  while (i < m) {
    if (!xs[n - m + i].eq(suffix[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (xs: T[]) is_empty(): boolean {
  return is_empty(xs);
}

pub function (xs: T[]) first(): Option[T] {
  return first(xs);
}

pub function (xs: T[]) last(): Option[T] {
  return last(xs);
}

pub function (xs: T[]) get(i: i32): Option[T] {
  return get(xs, i);
}

pub function (xs: T[]) map[U](f: (T) => U): U[] {
  return map(xs, f);
}

pub function (xs: T[]) filter(keep: (T) => boolean): T[] {
  return filter(xs, keep);
}

pub function (xs: T[]) fold[A](init: A, f: (A, T) => A): A {
  return fold(xs, init, f);
}

pub function (xs: T[]) any(pred: (T) => boolean): boolean {
  return any(xs, pred);
}

pub function (xs: T[]) all(pred: (T) => boolean): boolean {
  return all(xs, pred);
}

pub function (xs: T[]) none(pred: (T) => boolean): boolean {
  return none(xs, pred);
}

pub function (xs: T[]) find(pred: (T) => boolean): Option[T] {
  return find(xs, pred);
}

pub function (xs: T[]) find_last(pred: (T) => boolean): Option[T] {
  return find_last(xs, pred);
}

pub function (xs: T[]) rposition(pred: (T) => boolean): Option[i32] {
  return rposition(xs, pred);
}

pub function (xs: T[]) count_where(pred: (T) => boolean): i32 {
  return count_where(xs, pred);
}

pub function (xs: T[]) sum_by(key: (T) => i32): i32 {
  return sum_by(xs, key);
}

pub function (xs: T[]) enumerate(): (i32, T)[] {
  return enumerate(xs);
}

pub function (xs: T[]) concat(other: T[]): T[] {
  return concat(xs, other);
}

pub function (xs: T[]) zip[U](other: U[]): (T, U)[] {
  return zip(xs, other);
}

pub function (xs: T[]) step_by(n: i32): T[] {
  return step_by(xs, n);
}

pub function (xs: T[]) flat_map[U](f: (T) => U[]): U[] {
  return flat_map(xs, f);
}

pub function (xs: T[]) intersperse(sep: T): T[] {
  return intersperse(xs, sep);
}
```

### `13 pub function (xs: T[]) rotate_left(n: i32): T[]`

```fern
<<13 pub function (xs: T[]) rotate_left(n: i32): T[]>>=
pub function (xs: T[]) rotate_left(n: i32): T[] {
  return rotate_left(xs, n);
}

pub function (xs: T[]) rotate_right(n: i32): T[] {
  return rotate_right(xs, n);
}

pub function (xs: T[]) reduce(f: (T, T) => T): Option[T] {
  return reduce(xs, f);
}

pub function (xs: T[]) max_by(cmp: (T, T) => i32): Option[T] {
  return max_by(xs, cmp);
}

pub function (xs: T[]) min_by(cmp: (T, T) => i32): Option[T] {
  return min_by(xs, cmp);
}

pub function (xs: T[]) is_sorted[T: cmp.Ord](): boolean {
  return cmp.is_sorted(xs);
}

pub function (xs: T[]) binary_search[T: cmp.Ord](target: T): Option[i32] {
  return binary_search(xs, target);
}

pub function (xs: T[]) max_index[T: cmp.Ord](): Option[i32] {
  return max_index(xs);
}

pub function (xs: T[]) min_index[T: cmp.Ord](): Option[i32] {
  return min_index(xs);
}

pub function (xs: T[]) equal[T: cmp.Eq](other: T[]): boolean {
  return cmp.eq_arrays(xs, other);
}

pub function (xs: T[]) dedup[T: cmp.Eq](): T[] {
  return dedup(xs);
}

pub function (xs: T[]) all_equal[T: cmp.Eq](): boolean {
  return all_equal(xs);
}

pub function (xs: T[]) starts_with[T: cmp.Eq](prefix: T[]): boolean {
  return starts_with(xs, prefix);
}

pub function (xs: T[]) ends_with[T: cmp.Eq](suffix: T[]): boolean {
  return ends_with(xs, suffix);
}

pub function (xs: T[]) index_of[T: cmp.Eq](target: T): Option[i32] {
  return cmp.index_of(xs, target);
}

pub function (xs: T[]) contains[T: cmp.Eq](target: T): boolean {
  return cmp.contains(xs, target);
}

pub function (xs: T[]) index_of_last[T: cmp.Eq](target: T): Option[i32] {
  return index_of_last(xs, target);
}

pub function (xs: T[]) partition[T](pred: (T) => boolean): (T[], T[]) {
  return partition(xs, pred);
}

pub function (xs: T[]) scan[T, A](init: A, f: (A, T) => A): A[] {
  return scan(xs, init, f);
}

pub function (xs: T[]) chunks[T](size: i32): T[][] {
  return chunks(xs, size);
}

pub function (xs: T[]) chunks_exact[T](size: i32): T[][] {
  return chunks_exact(xs, size);
}

pub function (xs: T[]) windows[T](size: i32): T[][] {
  return windows(xs, size);
}

pub function (xs: T[]) sum[T: num.Add + num.Zero](): T {
  return num.sum(xs);
}

pub function (xs: T[]) product[T: num.Mul + num.One](): T {
  return num.product(xs);
}

pub function (xs: T[]) cumsum[T: num.Add + num.Zero](): T[] {
  return num.cumsum(xs);
}

pub function (xs: T[]) cumproduct[T: num.Mul + num.One](): T[] {
  return num.cumproduct(xs);
}

pub function (xs: T[]) adjacent_diff[T: num.Sub](): T[] {
  return num.adjacent_diff(xs);
}

pub function (xs: T[]) max[T: cmp.Ord](): Option[T] {
  return cmp.max_of(xs);
}

pub function (xs: T[]) min[T: cmp.Ord](): Option[T] {
  return cmp.min_of(xs);
}

pub function (xs: T[]) range[T: cmp.Ord + num.Sub](): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let lo: T = xs[0];
  let hi: T = xs[0];
  for x in xs {
    if (x.cmp(lo) < 0) {
      lo = x;
    }
    if (x.cmp(hi) > 0) {
      hi = x;
    }
  }
  return Some(hi.sub(lo));
}
```

### `14 pub function (xs: T[]) count[T: cmp.Eq](target: T): i32`

```fern
<<14 pub function (xs: T[]) count[T: cmp.Eq](target: T): i32>>=
pub function (xs: T[]) count[T: cmp.Eq](target: T): i32 {
  return count(xs, target);
}

pub function (xs: T[]) reverse(): T[] {
  return reverse(xs);
}

pub function (xs: T[]) take(n: i32): T[] {
  return take(xs, n);
}

pub function (xs: T[]) drop(n: i32): T[] {
  return drop(xs, n);
}

pub function (xs: T[]) distinct[T: cmp.Eq](): T[] {
  return cmp.distinct(xs);
}

pub function (xs: T[]) position(pred: (T) => boolean): Option[i32] {
  return position(xs, pred);
}
```
