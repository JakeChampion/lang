# Generic distinct-value sets

This module provides generic sets of distinct values.

## Model

A set retains each value at most once under its equality implementation. Set operations return set values with the requested membership.

## Contracts

Membership requires `cmp.Eq`. The equality contract determines duplicate removal and must remain consistent across insert, remove, and comparison operations.

## Executable example

```fern test name=std-set-example
import "std/set";

function main(): i32 {
  let s = set.set_of([2, 2, 3]);
  if (s.len() != 2 || !s.contains(3)) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Set[T]>>

<<4 pub function set_new[T](): Set[T]>>

<<5 pub function (s: Set[T]) is_disjoint[T: cmp.Eq](other: Set[T]): boolean>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides generic sets of distinct values.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "core/iter";
```

### `3 pub struct Set[T]`

```fern
<<3 pub struct Set[T]>>=
pub struct Set[T] {
  xs: T[],
}
```

### `4 pub function set_new[T](): Set[T]`

```fern
<<4 pub function set_new[T](): Set[T]>>=
pub function set_new[T](): Set[T] {
  return Set { xs: [] };
}

pub function set_of[T: cmp.Eq](xs: T[]): Set[T] {
  let s: Set[T] = set_new();
  let i: i32 = 0;
  while (i < xs.len()) {
    s = s.insert(xs[i]);
    i = i + 1;
  }
  return s;
}

pub function (s: Set[T]) contains[T: cmp.Eq](x: T): boolean {
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (s.xs[i].eq(x)) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

pub function (s: Set[T]) len(): i32 {
  return s.xs.len();
}

pub function (s: Set[T]) is_empty(): boolean {
  return s.xs.len() == 0;
}

pub function (s: Set[T]) insert[T: cmp.Eq](x: T): Set[T] {
  if (s.contains(x)) {
    return s;
  }
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    ys = ys.append(s.xs[i]);
    i = i + 1;
  }
  ys = ys.append(x);
  return Set { xs: ys };
}

pub function (s: Set[T]) remove[T: cmp.Eq](x: T): Set[T] {
  if (!s.contains(x)) {
    return s;
  }
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (!s.xs[i].eq(x)) {
      ys = ys.append(s.xs[i]);
    }
    i = i + 1;
  }
  return Set { xs: ys };
}

pub function (s: Set[T]) iter(): iter.ArrayIter[T] {
  return iter.of(s.xs);
}

pub function (s: Set[T]) to_array(): T[] {
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    ys = ys.append(s.xs[i]);
    i = i + 1;
  }
  return ys;
}

pub function (s: Set[T]) union[T: cmp.Eq](other: Set[T]): Set[T] {
  let out: Set[T] = s;
  let i: i32 = 0;
  while (i < other.xs.len()) {
    out = out.insert(other.xs[i]);
    i = i + 1;
  }
  return out;
}

pub function (s: Set[T]) intersection[T: cmp.Eq](other: Set[T]): Set[T] {
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (other.contains(s.xs[i])) {
      ys = ys.append(s.xs[i]);
    }
    i = i + 1;
  }
  return Set { xs: ys };
}

pub function (s: Set[T]) difference[T: cmp.Eq](other: Set[T]): Set[T] {
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (!other.contains(s.xs[i])) {
      ys = ys.append(s.xs[i]);
    }
    i = i + 1;
  }
  return Set { xs: ys };
}

pub function (s: Set[T]) is_subset[T: cmp.Eq](other: Set[T]): boolean {
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (!other.contains(s.xs[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: Set[T]) equals[T: cmp.Eq](other: Set[T]): boolean {
  if (s.xs.len() != other.xs.len()) {
    return false;
  }
  return s.is_subset(other);
}

pub function (s: Set[T]) is_superset[T: cmp.Eq](other: Set[T]): boolean {
  return other.is_subset(s);
}
```

### `5 pub function (s: Set[T]) is_disjoint[T: cmp.Eq](other: Set[T]): boolean`

```fern
<<5 pub function (s: Set[T]) is_disjoint[T: cmp.Eq](other: Set[T]): boolean>>=
pub function (s: Set[T]) is_disjoint[T: cmp.Eq](other: Set[T]): boolean {
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (other.contains(s.xs[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: Set[T]) symmetric_difference[T: cmp.Eq](other: Set[T]): Set[T] {
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (!other.contains(s.xs[i])) {
      ys = ys.append(s.xs[i]);
    }
    i = i + 1;
  }
  let j: i32 = 0;
  while (j < other.xs.len()) {
    if (!s.contains(other.xs[j])) {
      ys = ys.append(other.xs[j]);
    }
    j = j + 1;
  }
  return Set { xs: ys };
}

pub function (s: Set[T]) filter(pred: (T) => boolean): Set[T] {
  let ys: T[] = [];
  let i: i32 = 0;
  while (i < s.xs.len()) {
    if (pred(s.xs[i])) {
      ys = ys.append(s.xs[i]);
    }
    i = i + 1;
  }
  return Set { xs: ys };
}

pub function (s: Set[T]) fold[T, A](init: A, f: (A, T) => A): A {
  let acc: A = init;
  let i: i32 = 0;
  while (i < s.xs.len()) {
    acc = f(acc, s.xs[i]);
    i = i + 1;
  }
  return acc;
}

pub function (s: Set[T]) for_each(f: (T) => void): void {
  let i: i32 = 0;
  while (i < s.xs.len()) {
    f(s.xs[i]);
    i = i + 1;
  }
}
```
