# Comparison and display traits

This module defines comparison and display traits.

## Model

`Eq`, `Ord`, and `Hash` define the operations that generic collections require. `Display`, `Debug`, and `Default` define value presentation and defaults.

## Contracts

The comparison helpers use these traits rather than a representation-specific comparison. Array extrema return an option because an empty array has no element.

## Executable example

```fern test name=core-cmp-example
import "core/cmp";

function main(): i32 {
  if (cmp.min(7, 3) != 3 || cmp.max(7, 3) != 7) { return 1; }
  if (cmp.clamp(17, 0, 10) != 10) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub trait Display>>

<<4 impl Ord for i32>>

<<5 impl Ord for string>>

<<6 function __hash_mix_i32(x: i32): i32>>

<<7 impl Hash for i32>>

<<8 impl Debug for u64>>

<<9 pub function min[T: Ord](a: T, b: T): T>>

<<10 function __cmp_min_run(): i32>>

<<11 function __cmp_sort_adaptive[T: Ord](arr: T[], desc: boolean): T[]>>

<<12 pub function sort[T: Ord](arr: T[]): T[]>>

<<13 function __cmp_insertion_by[T](own a: T[], lo: i32, hi: i32, order: (T, T)>>

<<14 pub function is_sorted_by[T](arr: T[], order: (T, T)>>

<<15 function __cmp_heap_sort_ord[T: Ord](own a: T[]): T[]>>

<<16 pub function contains[T: Eq](xs: T[], target: T): boolean>>

<<17 impl Display for bigint.BigInt>>

<<18 pub function eq_arrays[T: Eq](a: T[], b: T[]): boolean>>
```
### `module introduction`

```fern
<<module introduction>>=
// This module defines comparison and display traits.
```
### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/utf8_scalar";
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/f64";
import "std/string";
import "core/bigint";
import "std/error";
```
### `3 pub trait Display`

```fern
<<3 pub trait Display>>=
pub trait Display {
  function to_string(self: Self): string;
}

pub trait Eq {
  function eq(self: Self, other: Self): boolean;
}

pub trait Ord {
  function cmp(self: Self, other: Self): i32;
}

pub trait Hash {
  function hash(self: Self): i32;
}

pub trait Default {
  function default(): Self;
}

pub trait Debug {
  function to_debug(self: Self): string;
}

impl Display for i32 {}

impl Display for i64 {}

impl Display for u32 {}

impl Display for u64 {}

impl Display for usize {}

impl Display for f64 {}

impl Display for f32 {}

impl Display for string {}

impl Display for IoError {}

impl Display for u8 {
  function to_string(self: u8): string {
    return (self as i32).to_string();
  }
}

impl Display for boolean {
  function to_string(self: boolean): string {
    if (self) {
      return "true";
    }
    return "false";
  }
}

impl Display for char {
  function to_string(self: char): string {
    return utf8_scalar.from_codepoint(self);
  }
}

// ---- Eq --------------------------------------------------------

impl Eq for i32 {
  function eq(self: i32, other: i32): boolean {
    return self == other;
  }
}

impl Eq for i64 {
  function eq(self: i64, other: i64): boolean {
    return self == other;
  }
}

impl Eq for u32 {
  function eq(self: u32, other: u32): boolean {
    return self == other;
  }
}

impl Eq for u64 {
  function eq(self: u64, other: u64): boolean {
    return self == other;
  }
}

impl Eq for usize {
  function eq(self: usize, other: usize): boolean {
    return self == other;
  }
}

impl Eq for u8 {
  function eq(self: u8, other: u8): boolean {
    return self == other;
  }
}

impl Eq for string {
  function eq(self: string, other: string): boolean {
    return self == other;
  }
}

impl Eq for boolean {
  function eq(self: boolean, other: boolean): boolean {
    return self == other;
  }
}

impl Eq for char {
  function eq(self: char, other: char): boolean {
    return self == other;
  }
}

impl Eq for f64 {
  function eq(self: f64, other: f64): boolean {
    if (self != self) {
      return other != other;
    }
    return self == other;
  }
}

impl Eq for f32 {
  function eq(self: f32, other: f32): boolean {
    if (self != self) {
      return other != other;
    }
    return self == other;
  }
}

// ---- Ord -------------------------------------------------------

impl Ord for boolean {
  function cmp(self: boolean, other: boolean): i32 {
    if (self == other) {
      return 0;
    }
    if (self) {
      return 1;
    }
    return -1;
  }
}
```
### `4 impl Ord for i32`

```fern
<<4 impl Ord for i32>>=
impl Ord for i32 {
  function cmp(self: i32, other: i32): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}

impl Ord for i64 {
  function cmp(self: i64, other: i64): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}

impl Ord for u32 {
  function cmp(self: u32, other: u32): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}

impl Ord for u64 {
  function cmp(self: u64, other: u64): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}

impl Ord for usize {
  function cmp(self: usize, other: usize): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}

impl Ord for u8 {
  function cmp(self: u8, other: u8): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}

impl Ord for char {
  function cmp(self: char, other: char): i32 {
    let a: i32 = self as i32;
    let b: i32 = other as i32;
    if (a < b) {
      return 0 - 1;
    }
    if (a > b) {
      return 1;
    }
    return 0;
  }
}

impl Ord for f64 {
  function cmp(self: f64, other: f64): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    if (self != self) {
      if (other != other) {
        return 0;
      }
      return 1;
    }
    if (other != other) {
      return 0 - 1;
    }
    return 0;
  }
}

impl Ord for f32 {
  function cmp(self: f32, other: f32): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    if (self != self) {
      if (other != other) {
        return 0;
      }
      return 1;
    }
    if (other != other) {
      return 0 - 1;
    }
    return 0;
  }
}
```
### `5 impl Ord for string`

```fern
<<5 impl Ord for string>>=
impl Ord for string {
  function cmp(self: string, other: string): i32 {
    if (self < other) {
      return 0 - 1;
    }
    if (self > other) {
      return 1;
    }
    return 0;
  }
}
```
### `6 function __hash_mix_i32(x: i32): i32`

```fern
<<6 function __hash_mix_i32(x: i32): i32>>=
function __hash_mix_i32(x: i32): i32 {
  let h: i32 = x ^ 61;
  h = h ^ (h as u32 >> 16 as u32) as i32;
  h = h + (h << 3);
  h = h ^ (h as u32 >> 4 as u32) as i32;
  h = h * 668265261;
  h = h ^ (h as u32 >> 15 as u32) as i32;
  return h;
}
```
### `7 impl Hash for i32`

```fern
<<7 impl Hash for i32>>=
impl Hash for i32 {
  function hash(self: i32): i32 {
    return __hash_mix_i32(self);
  }
}

impl Hash for u8 {
  function hash(self: u8): i32 {
    return __hash_mix_i32(self as i32);
  }
}

impl Hash for u32 {
  function hash(self: u32): i32 {
    return __hash_mix_i32(self as i32);
  }
}

impl Hash for char {
  function hash(self: char): i32 {
    return __hash_mix_i32(self as i32);
  }
}

impl Hash for i64 {
  function hash(self: i64): i32 {
    let lo: i32 = self as i32;
    let hi: i32 = (self >> 32 as i64) as i32;
    return __hash_mix_i32(lo ^ hi);
  }
}

impl Hash for u64 {
  function hash(self: u64): i32 {
    let lo: i32 = self as i32;
    let hi: i32 = (self >> 32 as u64) as i32;
    return __hash_mix_i32(lo ^ hi);
  }
}

// Float hashes follow Eq: both zeros share a hash, and all NaNs share a hash.
impl Hash for f64 {
  function hash(self: f64): i32 {
    if (self == 0.0) {
      return __hash_mix_i32(0);
    }
    if (self != self) {
      return __hash_mix_i32(0x7ff80000);
    }
    let bits: i64 = f64_bits(self);
    return __hash_mix_i32(bits as i32 ^ (bits >> 32 as i64) as i32);
  }
}

impl Hash for f32 {
  function hash(self: f32): i32 {
    if (self == 0.0 as f32) {
      return __hash_mix_i32(0);
    }
    if (self != self) {
      return __hash_mix_i32(0x7fc00000);
    }
    return __hash_mix_i32(f32_bits(self));
  }
}

// Folded through u64 so a 32-bit usize (wasm) never shifts by its own width.
impl Hash for usize {
  function hash(self: usize): i32 {
    let w: u64 = self as u64;
    let lo: i32 = w as i32;
    let hi: i32 = (w >> 32 as u64) as i32;
    return __hash_mix_i32(lo ^ hi);
  }
}

impl Hash for boolean {
  function hash(self: boolean): i32 {
    if (self) {
      return 1;
    }
    return 0;
  }
}

impl Hash for string {
  function hash(self: string): i32 {
    return self.hash_fnv32() as i32;
  }
}

impl Default for i32 {
  function default(): i32 {
    return 0;
  }
}

impl Default for i64 {
  function default(): i64 {
    return 0 as i64;
  }
}

impl Default for u32 {
  function default(): u32 {
    return 0 as u32;
  }
}

impl Default for u64 {
  function default(): u64 {
    return 0 as u64;
  }
}

impl Default for u8 {
  function default(): u8 {
    return 0 as u8;
  }
}

impl Default for string {
  function default(): string {
    return "";
  }
}

impl Default for boolean {
  function default(): boolean {
    return false;
  }
}

impl Default for f32 {
  function default(): f32 {
    return 0.0 as f32;
  }
}

impl Default for f64 {
  function default(): f64 {
    return 0.0 as f64;
  }
}

impl Debug for i32 {
  function to_debug(self: i32): string {
    return self.to_string();
  }
}

impl Debug for i64 {
  function to_debug(self: i64): string {
    return self.to_string();
  }
}

impl Debug for u32 {
  function to_debug(self: u32): string {
    return self.to_string();
  }
}
```
### `8 impl Debug for u64`

```fern
<<8 impl Debug for u64>>=
impl Debug for u64 {
  function to_debug(self: u64): string {
    return self.to_string();
  }
}

impl Debug for u8 {
  function to_debug(self: u8): string {
    return (self as i32).to_string();
  }
}

impl Debug for f64 {
  function to_debug(self: f64): string {
    return self.to_string();
  }
}

impl Debug for f32 {
  function to_debug(self: f32): string {
    return self.to_string();
  }
}

impl Debug for boolean {
  function to_debug(self: boolean): string {
    if (self) {
      return "true";
    }
    return "false";
  }
}

impl Debug for string {
  function to_debug(self: string): string {
    let out: u8[] = [];
    let i: i32 = 0;
    let n: i32 = self.len();
    while (i < n) {
      let b: i32 = self[i] as i32;
      if (b == 34) {
        out = out.append(92 as u8);
        out = out.append(34 as u8);
      } else {
        if (b == 92) {
          out = out.append(92 as u8);
          out = out.append(92 as u8);
        } else {
          if (b == 10) {
            out = out.append(92 as u8);
            out = out.append(110 as u8);
          } else {
            if (b == 9) {
              out = out.append(92 as u8);
              out = out.append(116 as u8);
            } else {
              out = out.append(self[i]);
            }
          }
        }
      }
      i = i + 1;
    }
    return "\"" + string_from_bytes_unchecked(out) + "\"";
  }
}
```
### `9 pub function min[T: Ord](a: T, b: T): T`

```fern
<<9 pub function min[T: Ord](a: T, b: T): T>>=
pub function min[T: Ord](a: T, b: T): T {
  if (b.cmp(a) < 0) {
    return b;
  }
  return a;
}

pub function max[T: Ord](a: T, b: T): T {
  if (a.cmp(b) < 0) {
    return b;
  }
  return a;
}

pub function clamp[T: Ord](x: T, lo: T, hi: T): T {
  if (x.cmp(lo) < 0) {
    return lo;
  }
  if (x.cmp(hi) > 0) {
    return hi;
  }
  return x;
}

pub function max_of[T: Ord](xs: T[]): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let best: T = xs[0];
  let i: i32 = 1;
  while (i < xs.len()) {
    if (xs[i].cmp(best) > 0) {
      best = xs[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function min_of[T: Ord](xs: T[]): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let best: T = xs[0];
  let i: i32 = 1;
  while (i < xs.len()) {
    if (xs[i].cmp(best) < 0) {
      best = xs[i];
    }
    i = i + 1;
  }
  return Some(best);
}

pub function lt[T: Ord](a: T, b: T): boolean {
  return a.cmp(b) < 0;
}

pub function lte[T: Ord](a: T, b: T): boolean {
  return a.cmp(b) <= 0;
}

pub function gt[T: Ord](a: T, b: T): boolean {
  return a.cmp(b) > 0;
}

pub function gte[T: Ord](a: T, b: T): boolean {
  return a.cmp(b) >= 0;
}

pub function is_sorted[T: Ord](arr: T[]): boolean {
  let i = 1;
  let n = arr.len();
  while (i < n) {
    if (arr[i].cmp(arr[i - 1]) < 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```
### `10 function __cmp_min_run(): i32`

```fern
<<10 function __cmp_min_run(): i32>>=
function __cmp_min_run(): i32 {
  return 32;
}

function __cmp_insertion[T: Ord](own a: T[], lo: i32, hi: i32, desc: boolean): T[] {
  let i: i32 = lo + 1;
  while (i < hi) {
    let v: T = a[i];
    let j: i32 = i - 1;
    let moving: boolean = j >= lo;
    while (moving) {
      let o: i32 = a[j].cmp(v);
      if (desc) {
        o = 0 - o;
      }
      if (o > 0) {
        a = a.with(j + 1, a[j]);
        j = j - 1;
        moving = j >= lo;
      } else {
        moving = false;
      }
    }
    a = a.with(j + 1, v);
    i = i + 1;
  }
  return a;
}
```
### `11 function __cmp_sort_adaptive[T: Ord](arr: T[], desc: boolean): T[]`

```fern
<<11 function __cmp_sort_adaptive[T: Ord](arr: T[], desc: boolean): T[]>>=
function __cmp_sort_adaptive[T: Ord](arr: T[], desc: boolean): T[] {
  let n: i32 = arr.len();
  if (n < 2) {
    return arr;
  }

  let a: T[] = [];
  let c: i32 = 0;
  while (c < n) {
    a = a.append(arr[c]);
    c = c + 1;
  }

  if (n <= 32) {
    a = __cmp_insertion(a, 0, n, desc);
    return a;
  }

  let bounds: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    bounds = bounds.append(i);
    let e: i32 = i + 1;
    if (e < n) {
      let head: i32 = a[i].cmp(a[e]);
      if (desc) {
        head = 0 - head;
      }
      if (head > 0) {
        let falling: boolean = true;
        while (falling) {
          e = e + 1;
          falling = false;
          if (e < n) {
            let od: i32 = a[e - 1].cmp(a[e]);
            if (desc) {
              od = 0 - od;
            }
            if (od > 0) {
              falling = true;
            }
          }
        }
        let lo: i32 = i;
        let hi: i32 = e - 1;
        while (lo < hi) {
          let t: T = a[lo];
          a = a.with(lo, a[hi]);
          a = a.with(hi, t);
          lo = lo + 1;
          hi = hi - 1;
        }
      } else {
        let rising: boolean = true;
        while (rising) {
          e = e + 1;
          rising = false;
          if (e < n) {
            let oa: i32 = a[e - 1].cmp(a[e]);
            if (desc) {
              oa = 0 - oa;
            }
            if (oa <= 0) {
              rising = true;
            }
          }
        }
      }
    }
    if (e - i < __cmp_min_run()) {
      let ext: i32 = i + __cmp_min_run();
      if (ext > n) {
        ext = n;
      }
      if (ext > e) {
        a = __cmp_insertion(a, i, ext, desc);
        e = ext;
      }
    }
    i = e;
  }
  bounds = bounds.append(n);

  while (bounds.len() > 2) {
    let out: T[] = [];
    let next: i32[] = [];
    let r: i32 = 0;
    while (r + 1 < bounds.len()) {
      let mlo: i32 = bounds[r];
      next = next.append(mlo);
      if (r + 2 < bounds.len()) {
        let mmid: i32 = bounds[r + 1];
        let mhi: i32 = bounds[r + 2];
        let p: i32 = mlo;
        let q: i32 = mmid;
        while (p < mmid && q < mhi) {
          let om: i32 = a[p].cmp(a[q]);
          if (desc) {
            om = 0 - om;
          }
          if (om > 0) {
            out = out.append(a[q]);
            q = q + 1;
          } else {
            out = out.append(a[p]);
            p = p + 1;
          }
        }
        while (p < mmid) {
          out = out.append(a[p]);
          p = p + 1;
        }
        while (q < mhi) {
          out = out.append(a[q]);
          q = q + 1;
        }
        r = r + 2;
      } else {
        let mend: i32 = bounds[r + 1];
        let mk: i32 = mlo;
        while (mk < mend) {
          out = out.append(a[mk]);
          mk = mk + 1;
        }
        r = r + 1;
      }
    }
    next = next.append(n);
    a = out;
    bounds = next;
  }
  return a;
}
```
### `12 pub function sort[T: Ord](arr: T[]): T[]`

```fern
<<12 pub function sort[T: Ord](arr: T[]): T[]>>=
pub function sort[T: Ord](arr: T[]): T[] {
  return __cmp_sort_adaptive(arr, false);
}

pub function sort_desc[T: Ord](arr: T[]): T[] {
  return __cmp_sort_adaptive(arr, true);
}

pub function sort_by[T](arr: T[], order: (T, T) => i32): T[] {
  let n: i32 = arr.len();
  if (n < 2) {
    return arr;
  }

  let a: T[] = [];
  let c: i32 = 0;
  while (c < n) {
    a = a.append(arr[c]);
    c = c + 1;
  }

  if (n <= 32) {
    a = __cmp_insertion_by(a, 0, n, order);
    return a;
  }

  let bounds: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    bounds = bounds.append(i);
    let e: i32 = i + 1;
    if (e < n) {
      if (order(a[i], a[e]) > 0) {
        let falling: boolean = true;
        while (falling) {
          e = e + 1;
          falling = false;
          if (e < n) {
            if (order(a[e - 1], a[e]) > 0) {
              falling = true;
            }
          }
        }
        let lo: i32 = i;
        let hi: i32 = e - 1;
        while (lo < hi) {
          let t: T = a[lo];
          a = a.with(lo, a[hi]);
          a = a.with(hi, t);
          lo = lo + 1;
          hi = hi - 1;
        }
      } else {
        let rising: boolean = true;
        while (rising) {
          e = e + 1;
          rising = false;
          if (e < n) {
            if (order(a[e - 1], a[e]) <= 0) {
              rising = true;
            }
          }
        }
      }
    }
    i = e;
  }
  bounds = bounds.append(n);

  while (bounds.len() > 2) {
    let out: T[] = [];
    let next: i32[] = [];
    let r: i32 = 0;
    while (r + 1 < bounds.len()) {
      let mlo: i32 = bounds[r];
      next = next.append(mlo);
      if (r + 2 < bounds.len()) {
        let mmid: i32 = bounds[r + 1];
        let mhi: i32 = bounds[r + 2];
        let p: i32 = mlo;
        let q: i32 = mmid;
        while (p < mmid && q < mhi) {
          if (order(a[p], a[q]) > 0) {
            out = out.append(a[q]);
            q = q + 1;
          } else {
            out = out.append(a[p]);
            p = p + 1;
          }
        }
        while (p < mmid) {
          out = out.append(a[p]);
          p = p + 1;
        }
        while (q < mhi) {
          out = out.append(a[q]);
          q = q + 1;
        }
        r = r + 2;
      } else {
        let mend: i32 = bounds[r + 1];
        let mk: i32 = mlo;
        while (mk < mend) {
          out = out.append(a[mk]);
          mk = mk + 1;
        }
        r = r + 1;
      }
    }
    next = next.append(n);
    a = out;
    bounds = next;
  }
  return a;
}
```
### `13 function __cmp_insertion_by[T](own a: T[], lo: i32, hi: i32, order: (T, T)`

```fern
<<13 function __cmp_insertion_by[T](own a: T[], lo: i32, hi: i32, order: (T, T)>>=
function __cmp_insertion_by[T](own a: T[], lo: i32, hi: i32, order: (T, T) => i32): T[] {
  let i: i32 = lo + 1;
  while (i < hi) {
    let v: T = a[i];
    let j: i32 = i - 1;
    let moving: boolean = j >= lo;
    while (moving) {
      if (order(a[j], v) > 0) {
        a = a.with(j + 1, a[j]);
        j = j - 1;
        moving = j >= lo;
      } else {
        moving = false;
      }
    }
    a = a.with(j + 1, v);
    i = i + 1;
  }
  return a;
}
```
### `14 pub function is_sorted_by[T](arr: T[], order: (T, T)`

```fern
<<14 pub function is_sorted_by[T](arr: T[], order: (T, T)>>=
pub function is_sorted_by[T](arr: T[], order: (T, T) => i32): boolean {
  let i: i32 = 1;
  let n: i32 = arr.len();
  while (i < n) {
    if (order(arr[i], arr[i - 1]) < 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function sort_by_key[T, K: Ord](arr: T[], key: (T) => K): T[] {
  let srcv: T[] = arr;
  let srck: K[] = [];
  for x in srcv {
    srck = srck.append(key(x));
  }
  let n: i32 = srcv.len();
  if (n < 2) {
    return srcv;
  }
  let width: i32 = 1;
  while (width < n) {
    let dstv: T[] = [];
    let dstk: K[] = [];
    let c: i32 = 0;
    while (c < n) {
      dstv = dstv.append(srcv[c]);
      dstk = dstk.append(srck[c]);
      c = c + 1;
    }
    let lo: i32 = 0;
    while (lo < n) {
      let mid: i32 = lo + width;
      if (mid > n) {
        mid = n;
      }
      let hi: i32 = lo + width + width;
      if (hi > n) {
        hi = n;
      }
      let i: i32 = lo;
      let j: i32 = mid;
      let k: i32 = lo;
      while (i < mid && j < hi) {
        if (srck[j].cmp(srck[i]) < 0) {
          dstv = dstv.with(k, srcv[j]);
          dstk = dstk.with(k, srck[j]);
          j = j + 1;
        } else {
          dstv = dstv.with(k, srcv[i]);
          dstk = dstk.with(k, srck[i]);
          i = i + 1;
        }
        k = k + 1;
      }
      while (i < mid) {
        dstv = dstv.with(k, srcv[i]);
        dstk = dstk.with(k, srck[i]);
        i = i + 1;
        k = k + 1;
      }
      while (j < hi) {
        dstv = dstv.with(k, srcv[j]);
        dstk = dstk.with(k, srck[j]);
        j = j + 1;
        k = k + 1;
      }
      lo = lo + width + width;
    }
    srcv = dstv;
    srck = dstk;
    width = width + width;
  }
  return srcv;
}

pub function sort_in_place[T: Ord](own arr: T[]): T[] {
  return __cmp_heap_sort_ord(arr);
}

pub function sort_desc_in_place[T: Ord](own arr: T[]): T[] {
  return __cmp_reverse_in_place(__cmp_heap_sort_ord(arr));
}

pub function sort_by_in_place[T](own arr: T[], order: (T, T) => i32): T[] {
  return __cmp_heap_sort(arr, order);
}

pub function sort_by_key_in_place[T, K: Ord](own arr: T[], key: (T) => K): T[] {
  return __cmp_heap_sort(arr, (a: T, b: T): i32 => key(a).cmp(key(b)));
}
```
### `15 function __cmp_heap_sort_ord[T: Ord](own a: T[]): T[]`

```fern
<<15 function __cmp_heap_sort_ord[T: Ord](own a: T[]): T[]>>=
function __cmp_heap_sort_ord[T: Ord](own a: T[]): T[] {
  let n: i32 = a.len();
  let start: i32 = n / 2 - 1;
  while (start >= 0) {
    a = __cmp_sift_down_ord(a, start, n);
    start = start - 1;
  }
  let end: i32 = n - 1;
  while (end > 0) {
    let t: T = a[0];
    a = a.with(0, a[end]);
    a = a.with(end, t);
    a = __cmp_sift_down_ord(a, 0, end);
    end = end - 1;
  }
  return a;
}

function __cmp_sift_down_ord[T: Ord](own a: T[], root: i32, n: i32): T[] {
  let r: i32 = root;
  while (2 * r + 1 < n) {
    let child: i32 = 2 * r + 1;
    if (child + 1 < n && a[child].cmp(a[child + 1]) < 0) {
      child = child + 1;
    }
    if (a[r].cmp(a[child]) >= 0) {
      return a;
    }
    let t: T = a[r];
    a = a.with(r, a[child]);
    a = a.with(child, t);
    r = child;
  }
  return a;
}

function __cmp_reverse_in_place[T](own a: T[]): T[] {
  let i: i32 = 0;
  let j: i32 = a.len() - 1;
  while (i < j) {
    let t: T = a[i];
    a = a.with(i, a[j]);
    a = a.with(j, t);
    i = i + 1;
    j = j - 1;
  }
  return a;
}

function __cmp_heap_sort[T](own a: T[], order: (T, T) => i32): T[] {
  let n: i32 = a.len();
  let start: i32 = n / 2 - 1;
  while (start >= 0) {
    a = __cmp_sift_down(a, start, n, order);
    start = start - 1;
  }
  let end: i32 = n - 1;
  while (end > 0) {
    let t: T = a[0];
    a = a.with(0, a[end]);
    a = a.with(end, t);
    a = __cmp_sift_down(a, 0, end, order);
    end = end - 1;
  }
  return a;
}

function __cmp_sift_down[T](own a: T[], root: i32, n: i32, order: (T, T) => i32): T[] {
  let r: i32 = root;
  while (2 * r + 1 < n) {
    let child: i32 = 2 * r + 1;
    if (child + 1 < n && order(a[child], a[child + 1]) < 0) {
      child = child + 1;
    }
    if (order(a[r], a[child]) >= 0) {
      return a;
    }
    let t: T = a[r];
    a = a.with(r, a[child]);
    a = a.with(child, t);
    r = child;
  }
  return a;
}
```
### `16 pub function contains[T: Eq](xs: T[], target: T): boolean`

```fern
<<16 pub function contains[T: Eq](xs: T[], target: T): boolean>>=
pub function contains[T: Eq](xs: T[], target: T): boolean {
  for x in xs {
    if (x.eq(target)) {
      return true;
    }
  }
  return false;
}

pub function index_of[T: Eq](xs: T[], target: T): Option[i32] {
  let i = 0;
  for x in xs {
    if (x.eq(target)) {
      return Some(i);
    }
    i = i + 1;
  }
  return None;
}

pub function distinct[T: Eq](xs: T[]): T[] {
  let out: T[] = [];
  for x in xs {
    if (!contains(out, x)) {
      out = out.append(x);
    }
  }
  return out;
}
```
### `17 impl Display for bigint.BigInt`

```fern
<<17 impl Display for bigint.BigInt>>=
impl Display for bigint.BigInt {}

impl Eq for bigint.BigInt {}

impl Ord for bigint.BigInt {}

impl Hash for bigint.BigInt {}

impl Default for bigint.BigInt {
  function default(): bigint.BigInt {
    return bigint.zero();
  }
}

impl Debug for bigint.BigInt {
  function to_debug(self: bigint.BigInt): string {
    return self.to_string();
  }
}
```
### `18 pub function eq_arrays[T: Eq](a: T[], b: T[]): boolean`

```fern
<<18 pub function eq_arrays[T: Eq](a: T[], b: T[]): boolean>>=
pub function eq_arrays[T: Eq](a: T[], b: T[]): boolean {
  let n = a.len();
  if (n != b.len()) {
    return false;
  }
  let i = 0;
  while (i < n) {
    if (!a[i].eq(b[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```