# Persistent ordered sets

This module provides persistent ordered sets.

## Model

An ordered set stores its values as keys in an ordered map. A value occurs at most once.

## Contracts

Updates preserve the previous set value. Comparison follows `cmp.Ord`, and ordered operations use that same relation.

## Executable example

```fern test name=std-ordset-example
import "std/ordset";

function main(): i32 {
  let s = ordset.of([3, 1, 3]);
  if (s.len() != 2 || !s.contains(1)) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct OrdSet[T]>>

<<4 pub function new[T](): OrdSet[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides persistent ordered sets.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "core/iter";
import "std/ordmap";
```

### `3 pub struct OrdSet[T]`

```fern
<<3 pub struct OrdSet[T]>>=
pub struct OrdSet[T] { root: ordmap.OrdMap[T, i32] }
```

### `4 pub function new[T](): OrdSet[T]`

```fern
<<4 pub function new[T](): OrdSet[T]>>=
pub function new[T](): OrdSet[T] {
  let e: ordmap.OrdMap[T, i32] = ordmap.new();
  return OrdSet { root: e };
}

pub function of[T: cmp.Ord](xs: T[]): OrdSet[T] {
  let s: OrdSet[T] = new();
  let i: i32 = 0;
  while (i < xs.len()) {
    s = s.insert(xs[i]);
    i = i + 1;
  }
  return s;
}

pub function (s: OrdSet[T]) len(): i32 {
  return s.root.len();
}

pub function (s: OrdSet[T]) is_empty(): boolean {
  return s.root.is_empty();
}

pub function (s: OrdSet[T: cmp.Ord]) insert(x: T): OrdSet[T] {
  return OrdSet { root: s.root.insert(x, 0) };
}

pub function (s: OrdSet[T: cmp.Ord]) remove(x: T): OrdSet[T] {
  return OrdSet { root: s.root.remove(x) };
}

pub function (s: OrdSet[T: cmp.Ord]) contains(x: T): boolean {
  return s.root.contains(x);
}

pub function (s: OrdSet[T]) min(): Option[T] {
  return s.root.min_key();
}

pub function (s: OrdSet[T]) max(): Option[T] {
  return s.root.max_key();
}

pub function (s: OrdSet[T]) at(i: i32): Option[T] {
  return s.root.key_at(i);
}

pub function (s: OrdSet[T: cmp.Ord]) index_of(x: T): Option[i32] {
  return s.root.index_of(x);
}

pub function (s: OrdSet[T]) iter(): iter.Mapped[ordmap.OrdIter[T, i32], (T, i32), T] {
  return s.root.iter().map((e: (T, i32)): T => e.0);
}

pub function (s: OrdSet[T]) to_array(): T[] {
  return s.root.keys();
}

pub function (s: OrdSet[T: cmp.Ord]) union(other: OrdSet[T]): OrdSet[T] {
  return OrdSet { root: s.root.merge(other.root) };
}

pub function (s: OrdSet[T: cmp.Ord]) intersection(other: OrdSet[T]): OrdSet[T] {
  return OrdSet { root: s.root.intersection(other.root) };
}

pub function (s: OrdSet[T: cmp.Ord]) difference(other: OrdSet[T]): OrdSet[T] {
  return OrdSet { root: s.root.difference(other.root) };
}

pub function (s: OrdSet[T: cmp.Ord]) is_subset(other: OrdSet[T]): boolean {
  if (s.len() > other.len()) {
    return false;
  }
  return s.root.difference(other.root).is_empty();
}

pub function (s: OrdSet[T: cmp.Ord]) equals(other: OrdSet[T]): boolean {
  return s.len() == other.len() && s.is_subset(other);
}

pub function (s: OrdSet[T]) filter(pred: (T) => boolean): OrdSet[T] {
  return OrdSet { root: s.root.filter((k: T, v: i32) => pred(k)) };
}

pub function (s: OrdSet[T]) fold[A](init: A, f: (A, T) => A): A {
  return s.root.fold(init, (a: A, k: T, v: i32) => f(a, k));
}

pub function (s: OrdSet[T]) for_each(f: (T) => void): void {
  s.root.for_each((k: T, v: i32) => f(k));
}

pub function (s: OrdSet[T: cmp.Ord]) is_valid(): boolean {
  return s.root.is_valid();
}

pub function (s: OrdSet[T: cmp.Ord]) is_superset(other: OrdSet[T]): boolean {
  return other.is_subset(s);
}

pub function (s: OrdSet[T: cmp.Ord]) is_disjoint(other: OrdSet[T]): boolean {
  return s.intersection(other).is_empty();
}

pub function (s: OrdSet[T: cmp.Ord]) symmetric_difference(other: OrdSet[T]): OrdSet[T] {
  return s.difference(other).union(other.difference(s));
}
```
