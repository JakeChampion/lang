# Persistent hash sets

This module provides persistent hash sets.

## Model

A persistent set stores each value as a key in a persistent hash map. Updates preserve earlier versions of the set.

## Contracts

Values require compatible hash and equality implementations. Set membership checks equality even when several values share a hash.

## Executable example

```fern test name=std-pset-example
import "std/pset";

function main(): i32 {
  let s = pset.pset_of([1, 2, 1]);
  if (s.len() != 2 || s.contains(3)) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct PSet[T]>>

<<4 pub function pset_new[T](): PSet[T]>>

<<5 function __ps_insert_all[T: cmp.Hash + cmp.Eq](s: PSet[T], xs: T[]): PSet[T]>>

<<6 pub function (s: PSet[T]) intersection[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides persistent hash sets.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "core/iter";
import "std/pmap";
```

### `3 pub struct PSet[T]`

```fern
<<3 pub struct PSet[T]>>=
pub struct PSet[T] { root: pmap.PMap[T, i32] }
```

### `4 pub function pset_new[T](): PSet[T]`

```fern
<<4 pub function pset_new[T](): PSet[T]>>=
pub function pset_new[T](): PSet[T] {
  let e: pmap.PMap[T, i32] = pmap.pmap_new();
  return PSet { root: e };
}

pub function pset_of[T: cmp.Hash + cmp.Eq](xs: T[]): PSet[T] {
  let s: PSet[T] = pset_new();
  let i: i32 = 0;
  while (i < xs.len()) {
    s = s.insert(xs[i]);
    i = i + 1;
  }
  return s;
}

pub function (s: PSet[T]) len(): i32 {
  return s.root.len();
}

pub function (s: PSet[T]) is_empty(): boolean {
  return s.root.is_empty();
}

pub function (s: PSet[T]) insert[T: cmp.Hash + cmp.Eq](x: T): PSet[T] {
  return PSet { root: s.root.insert(x, 0) };
}

pub function (s: PSet[T]) remove[T: cmp.Hash + cmp.Eq](x: T): PSet[T] {
  return PSet { root: s.root.remove(x) };
}

pub function (s: PSet[T]) contains[T: cmp.Hash + cmp.Eq](x: T): boolean {
  return s.root.contains(x);
}

pub function (s: PSet[T]) iter(): iter.ArrayIter[T] {
  return iter.of(s.to_array());
}

pub function (s: PSet[T]) to_array(): T[] {
  return s.root.keys();
}

pub function (s: PSet[T]) union[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T] {
  if (s.len() >= other.len()) {
    return __ps_insert_all(s, other.to_array());
  }
  return __ps_insert_all(other, s.to_array());
}
```

### `5 function __ps_insert_all[T: cmp.Hash + cmp.Eq](s: PSet[T], xs: T[]): PSet[T]`

```fern
<<5 function __ps_insert_all[T: cmp.Hash + cmp.Eq](s: PSet[T], xs: T[]): PSet[T]>>=
function __ps_insert_all[T: cmp.Hash + cmp.Eq](s: PSet[T], xs: T[]): PSet[T] {
  let out: PSet[T] = s;
  let i: i32 = 0;
  while (i < xs.len()) {
    out = out.insert(xs[i]);
    i = i + 1;
  }
  return out;
}
```

### `6 pub function (s: PSet[T]) intersection[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T]`

```fern
<<6 pub function (s: PSet[T]) intersection[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T]>>=
pub function (s: PSet[T]) intersection[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T] {
  let out: PSet[T] = pset_new();
  let xs: T[] = s.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    if (other.contains(xs[i])) {
      out = out.insert(xs[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (s: PSet[T]) difference[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T] {
  let out: PSet[T] = s;
  let xs: T[] = other.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    out = out.remove(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function (s: PSet[T]) is_subset[T: cmp.Hash + cmp.Eq](other: PSet[T]): boolean {
  if (s.len() > other.len()) {
    return false;
  }
  let xs: T[] = s.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    if (!other.contains(xs[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function (s: PSet[T]) equals[T: cmp.Hash + cmp.Eq](other: PSet[T]): boolean {
  return s.len() == other.len() && s.is_subset(other);
}

pub function (s: PSet[T]) filter[T: cmp.Hash + cmp.Eq](pred: (T) => boolean): PSet[T] {
  let out: PSet[T] = pset_new();
  let xs: T[] = s.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    if (pred(xs[i])) {
      out = out.insert(xs[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (s: PSet[T]) fold[T, A](init: A, f: (A, T) => A): A {
  return s.root.fold(init, (a: A, k: T, v: i32) => f(a, k));
}

pub function (s: PSet[T]) for_each(f: (T) => void): void {
  s.root.for_each((k: T, v: i32) => f(k));
}

pub function (s: PSet[T]) is_valid(): boolean {
  return s.root.is_valid();
}

pub function (s: PSet[T]) is_superset[T: cmp.Hash + cmp.Eq](other: PSet[T]): boolean {
  return other.is_subset(s);
}

pub function (s: PSet[T]) is_disjoint[T: cmp.Hash + cmp.Eq](other: PSet[T]): boolean {
  return s.intersection(other).is_empty();
}

pub function (s: PSet[T]) symmetric_difference[T: cmp.Hash + cmp.Eq](other: PSet[T]): PSet[T] {
  return s.difference(other).union(other.difference(s));
}
```
