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
  let s = ordset.ordset_of([3, 1, 3]);
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

<<4 pub function ordset_new[T](): OrdSet[T]>>
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

### `4 pub function ordset_new[T](): OrdSet[T]`

```fern
<<4 pub function ordset_new[T](): OrdSet[T]>>=
pub function ordset_new[T](): OrdSet[T] {
  let e: ordmap.OrdMap[T, i32] = ordmap.ordmap_new();
  return OrdSet { root: e };
}

pub function ordset_of[T: cmp.Ord](xs: T[]): OrdSet[T] {
  let s: OrdSet[T] = ordset_new();
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

pub function (s: OrdSet[T]) insert[T: cmp.Ord](x: T): OrdSet[T] {
  return OrdSet { root: s.root.insert(x, 0) };
}

pub function (s: OrdSet[T]) remove[T: cmp.Ord](x: T): OrdSet[T] {
  return OrdSet { root: s.root.remove(x) };
}

pub function (s: OrdSet[T]) contains[T: cmp.Ord](x: T): boolean {
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

pub function (s: OrdSet[T]) index_of[T: cmp.Ord](x: T): Option[i32] {
  return s.root.index_of(x);
}

pub function (s: OrdSet[T]) iter(): iter.ArrayIter[T] {
  return iter.of(s.to_array());
}

pub function (s: OrdSet[T]) to_array(): T[] {
  return s.root.keys();
}

pub function (s: OrdSet[T]) union[T: cmp.Ord](other: OrdSet[T]): OrdSet[T] {
  return OrdSet { root: s.root.merge(other.root) };
}

pub function (s: OrdSet[T]) intersection[T: cmp.Ord](other: OrdSet[T]): OrdSet[T] {
  return OrdSet { root: s.root.intersection(other.root) };
}

pub function (s: OrdSet[T]) difference[T: cmp.Ord](other: OrdSet[T]): OrdSet[T] {
  return OrdSet { root: s.root.difference(other.root) };
}

pub function (s: OrdSet[T]) is_subset[T: cmp.Ord](other: OrdSet[T]): boolean {
  if (s.len() > other.len()) {
    return false;
  }
  return s.root.difference(other.root).is_empty();
}

pub function (s: OrdSet[T]) equals[T: cmp.Ord](other: OrdSet[T]): boolean {
  return s.len() == other.len() && s.is_subset(other);
}

pub function (s: OrdSet[T]) filter(pred: (T) => boolean): OrdSet[T] {
  return OrdSet { root: s.root.filter((k: T, v: i32) => pred(k)) };
}

pub function (s: OrdSet[T]) fold[T, A](init: A, f: (A, T) => A): A {
  return s.root.fold(init, (a: A, k: T, v: i32) => f(a, k));
}

pub function (s: OrdSet[T]) for_each(f: (T) => void): void {
  s.root.for_each((k: T, v: i32) => f(k));
}

pub function (s: OrdSet[T]) is_valid[T: cmp.Ord](): boolean {
  return s.root.is_valid();
}

pub function (s: OrdSet[T]) is_superset[T: cmp.Ord](other: OrdSet[T]): boolean {
  return other.is_subset(s);
}

pub function (s: OrdSet[T]) is_disjoint[T: cmp.Ord](other: OrdSet[T]): boolean {
  return s.intersection(other).is_empty();
}

pub function (s: OrdSet[T]) symmetric_difference[T: cmp.Ord](other: OrdSet[T]): OrdSet[T] {
  return s.difference(other).union(other.difference(s));
}
```
