# Persistent indexed vectors

This module provides persistent vectors.

## Model

A vector combines a tree of element blocks with a short tail. The stored length and tree shift describe indexed access.

## Contracts

Updates return a new vector and retain previous versions. Optional access distinguishes an absent index from an element with a default-like value.

## Executable example

```fern test name=std-pvec-example
import "std/pvec";

function main(): i32 {
  let v = pvec.from_array([10, 20, 30]);
  if (v.len() != 3 || v.get_or(1, 0) != 20 || v.get_or(4, 9) != 9) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum VNode[T]>>

<<4 pub function pvec_new[T](): PVec[T]>>

<<5 function __pv_tail_off(len: i32): i32>>

<<6 function __pv_into[T](node: VNode[T], own acc: T[]): T[]>>

<<7 pub function (v: PVec[T]) len(): i32>>

<<8 function __pv_shrink_root[T](len: i32, root: VNode[T], shift: i32, tail: T[]): PVec[T]>>

<<9 pub function (v: PVec[T]) pop(): Option[(PVec[T], T)]>>

<<10 function __pv_without_last[T](v: PVec[T]): PVec[T]>>

<<11 pub function (v: PVec[T]) iter(): iter.ArrayIter[T]>>

<<12 function __pv_valid_root[T](root: VNode[T], shift: i32, off: i32): boolean>>

<<13 pub function (v: PVec[T]) is_valid(): boolean>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides persistent vectors.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/iter";
```

### `3 pub enum VNode[T]`

```fern
<<3 pub enum VNode[T]>>=
pub enum VNode[T] { Empty, Leaf(T[]), Branch(VNode[T][]) }

pub struct PVec[T] { len: i32, shift: i32, root: VNode[T], tail: T[] }
```

### `4 pub function pvec_new[T](): PVec[T]`

```fern
<<4 pub function pvec_new[T](): PVec[T]>>=
pub function pvec_new[T](): PVec[T] {
  let e: VNode[T] = Empty;
  let t: T[] = [];
  return PVec { len: 0, shift: 0, root: e, tail: t };
}

pub function from_array[T](xs: T[]): PVec[T] {
  let v: PVec[T] = pvec_new();
  let i: i32 = 0;
  while (i < xs.len()) {
    v = v.append(xs[i]);
    i = i + 1;
  }
  return v;
}
```

### `5 function __pv_tail_off(len: i32): i32`

```fern
<<5 function __pv_tail_off(len: i32): i32>>=
function __pv_tail_off(len: i32): i32 {
  if (len < 32) {
    return 0;
  }
  return len - 1 >> 5 << 5;
}

function __pv_path[T](shift: i32, leaf: VNode[T]): VNode[T] {
  if (shift == 0) {
    return leaf;
  }
  let inner: VNode[T] = __pv_path(shift - 5, leaf);
  return Branch([inner]);
}

function __pv_push_tail[T](node: VNode[T], shift: i32, len: i32, leaf: VNode[T]): VNode[T] {
  let sub: i32 = len - 1 >> shift & 31;
  match (node) {
    Branch(kids) => {
      if (shift == 5) {
        return Branch(kids.append(leaf));
      }
      if (sub < kids.len()) {
        let e: VNode[T] = Empty;
        let child: VNode[T] = kids[sub];
        let rest: VNode[T][] = kids.with(sub, e);
        child = __pv_push_tail(child, shift - 5, len, leaf);
        return Branch(rest.with(sub, child));
      }
      let path: VNode[T] = __pv_path(shift - 5, leaf);
      return Branch(kids.append(path));
    },
    Leaf(xs) => {
      return Leaf(xs);
    },
    Empty => {
      let e: VNode[T] = Empty;
      return e;
    }
  }
}

function __pv_leaf_for[T](root: VNode[T], shift: i32, i: i32): T[] {
  let cur: VNode[T] = root;
  let level: i32 = shift;
  let descending: boolean = true;
  while (descending) {
    match (cur) {
      Branch(kids) => {
        cur = kids[i >> level & 31];
        level = level - 5;
      },
      Leaf(xs) => {
        return xs;
      },
      Empty => {
        descending = false;
      }
    }
  }
  let none: T[] = [];
  return none;
}

function __pv_with_in[T](node: VNode[T], shift: i32, i: i32, x: T): VNode[T] {
  match (node) {
    Branch(kids) => {
      let sub: i32 = i >> shift & 31;
      let e: VNode[T] = Empty;
      let child: VNode[T] = kids[sub];
      let rest: VNode[T][] = kids.with(sub, e);
      child = __pv_with_in(child, shift - 5, i, x);
      return Branch(rest.with(sub, child));
    },
    Leaf(xs) => {
      let nx: T[] = xs.with(i & 31, x);
      return Leaf(nx);
    },
    Empty => {
      let e: VNode[T] = Empty;
      return e;
    }
  }
}

function __pv_pop_tail[T](node: VNode[T], shift: i32, len: i32): VNode[T] {
  let sub: i32 = len - 2 >> shift & 31;
  match (node) {
    Branch(kids) => {
      if (shift > 5) {
        let child: VNode[T] = __pv_pop_tail(kids[sub], shift - 5, len);
        match (child) {
          Empty => {
            if (sub == 0) {
              return child;
            }
            return Branch(__pv_drop_last(kids));
          },
          Branch(ck) => {
            return Branch(kids.with(sub, child));
          },
          Leaf(cx) => {
            return Branch(kids.with(sub, child));
          }
        }
      }
      if (sub == 0) {
        let e: VNode[T] = Empty;
        return e;
      }
      return Branch(__pv_drop_last(kids));
    },
    Leaf(xs) => {
      return node;
    },
    Empty => {
      return node;
    }
  }
}

function __pv_drop_last[T](xs: VNode[T][]): VNode[T][] {
  let out: VNode[T][] = [];
  let i: i32 = 0;
  while (i + 1 < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}
```

### `6 function __pv_into[T](node: VNode[T], own acc: T[]): T[]`

```fern
<<6 function __pv_into[T](node: VNode[T], own acc: T[]): T[]>>=
function __pv_into[T](node: VNode[T], own acc: T[]): T[] {
  match (node) {
    Empty => {
      return acc;
    },
    Leaf(xs) => {
      acc = __pv_into_elems(xs, 0, acc);
      return acc;
    },
    Branch(kids) => {
      acc = __pv_into_kids(kids, 0, acc);
      return acc;
    }
  }
}

function __pv_into_elems[T](xs: T[], i: i32, own acc: T[]): T[] {
  if (i >= xs.len()) {
    return acc;
  }
  acc = acc.append(xs[i]);
  acc = __pv_into_elems(xs, i + 1, acc);
  return acc;
}

function __pv_into_kids[T](kids: VNode[T][], i: i32, own acc: T[]): T[] {
  if (i >= kids.len()) {
    return acc;
  }
  acc = __pv_into(kids[i], acc);
  acc = __pv_into_kids(kids, i + 1, acc);
  return acc;
}

function __pv_fold[T, A](node: VNode[T], init: A, f: (A, T) => A): A {
  match (node) {
    Empty => {
      return init;
    },
    Leaf(xs) => {
      let a: A = init;
      let i: i32 = 0;
      while (i < xs.len()) {
        a = f(a, xs[i]);
        i = i + 1;
      }
      return a;
    },
    Branch(kids) => {
      let a: A = init;
      let i: i32 = 0;
      while (i < kids.len()) {
        a = __pv_fold(kids[i], a, f);
        i = i + 1;
      }
      return a;
    }
  }
}

function __pv_for_each[T](node: VNode[T], f: (T) => void): void {
  match (node) {
    Empty => {
      return;
    },
    Leaf(xs) => {
      let i: i32 = 0;
      while (i < xs.len()) {
        f(xs[i]);
        i = i + 1;
      }
    },
    Branch(kids) => {
      let i: i32 = 0;
      while (i < kids.len()) {
        __pv_for_each(kids[i], f);
        i = i + 1;
      }
    }
  }
}

function __pv_valid_branch[T](kids: VNode[T][], shift: i32, count: i32): boolean {
  if (shift == 0 || kids.len() == 0 || kids.len() > 32) {
    return false;
  }
  let per: i32 = 1 << shift;
  let left: i32 = count;
  let i: i32 = 0;
  while (i < kids.len()) {
    let take: i32 = per;
    if (left < per) {
      take = left;
    }
    if (take <= 0 || !__pv_valid(kids[i], shift - 5, take)) {
      return false;
    }
    left = left - take;
    i = i + 1;
  }
  return left == 0;
}

function __pv_valid[T](node: VNode[T], shift: i32, count: i32): boolean {
  match (node) {
    Empty => {
      return count == 0;
    },
    Leaf(xs) => {
      return shift == 0 && xs.len() == 32 && count == 32;
    },
    Branch(kids) => {
      return __pv_valid_branch(kids, shift, count);
    }
  }
}
```

### `7 pub function (v: PVec[T]) len(): i32`

```fern
<<7 pub function (v: PVec[T]) len(): i32>>=
pub function (v: PVec[T]) len(): i32 {
  return v.len;
}

pub function (v: PVec[T]) is_empty(): boolean {
  return v.len == 0;
}

pub function (v: PVec[T]) get(i: i32): Option[T] {
  if (i < 0 || i >= v.len) {
    return None;
  }
  if (i >= __pv_tail_off(v.len)) {
    return Some(v.tail[i - __pv_tail_off(v.len)]);
  }
  let leaf: T[] = __pv_leaf_for(v.root, v.shift, i);
  return Some(leaf[i & 31]);
}

pub function (v: PVec[T]) get_or(i: i32, fallback: T): T {
  if (i < 0 || i >= v.len) {
    return fallback;
  }
  if (i >= __pv_tail_off(v.len)) {
    return v.tail[i - __pv_tail_off(v.len)];
  }
  let leaf: T[] = __pv_leaf_for(v.root, v.shift, i);
  return leaf[i & 31];
}

pub function (v: PVec[T]) first(): Option[T] {
  return v.get(0);
}

pub function (v: PVec[T]) last(): Option[T] {
  if (v.len == 0) {
    return None;
  }
  return Some(v.tail[v.tail.len() - 1]);
}

pub function (v: PVec[T]) append(x: T): PVec[T] {
  if (v.len - __pv_tail_off(v.len) < 32) {
    return PVec { len: v.len + 1, shift: v.shift, root: v.root, tail: v.tail.append(x) };
  }
  let leaf: VNode[T] = Leaf(v.tail);
  let fresh: T[] = [x];
  let e: VNode[T] = Empty;
  let root: VNode[T] = v.root;
  v = PVec { ...v, root: e };
  match (root) {
    Empty => {
      return PVec { len: v.len + 1, shift: 0, root: leaf, tail: fresh };
    },
    Leaf(xs) => {
      return PVec { len: v.len + 1, shift: 5, root: Branch([root, leaf]), tail: fresh };
    },
    Branch(kids) => {
      if (v.len >> 5 > 1 << v.shift) {
        let path: VNode[T] = __pv_path(v.shift, leaf);
        return PVec { len: v.len + 1, shift: v.shift + 5, root: Branch([root, path]), tail: fresh };
      }
      root = __pv_push_tail(root, v.shift, v.len, leaf);
      return PVec { len: v.len + 1, shift: v.shift, root: root, tail: fresh };
    }
  }
}

pub function (v: PVec[T]) with(i: i32, x: T): PVec[T] {
  if (i < 0 || i >= v.len) {
    return v;
  }
  let off: i32 = __pv_tail_off(v.len);
  if (i >= off) {
    let tail: T[] = v.tail;
    v = PVec { ...v, tail: [] };
    return PVec { ...v, tail: tail.with(i - off, x) };
  }
  let e: VNode[T] = Empty;
  let root: VNode[T] = v.root;
  v = PVec { ...v, root: e };
  root = __pv_with_in(root, v.shift, i, x);
  return PVec { ...v, root: root };
}
```

### `8 function __pv_shrink_root[T](len: i32, root: VNode[T], shift: i32, tail: T[]): PVec[T]`

```fern
<<8 function __pv_shrink_root[T](len: i32, root: VNode[T], shift: i32, tail: T[]): PVec[T]>>=
function __pv_shrink_root[T](len: i32, root: VNode[T], shift: i32, tail: T[]): PVec[T] {
  match (root) {
    Branch(rk) => {
      if (rk.len() == 1 && shift > 0) {
        return __pv_shrink_root(len, rk[0], shift - 5, tail);
      }
      return PVec { len: len, shift: shift, root: root, tail: tail };
    },
    Leaf(rx) => {
      return PVec { len: len, shift: 0, root: root, tail: tail };
    },
    Empty => {
      return PVec { len: len, shift: 0, root: root, tail: tail };
    }
  }
}
```

### `9 pub function (v: PVec[T]) pop(): Option[(PVec[T], T)]`

```fern
<<9 pub function (v: PVec[T]) pop(): Option[(PVec[T], T)]>>=
pub function (v: PVec[T]) pop(): Option[(PVec[T], T)] {
  match (v.last()) {
    Some(x) => {
      return Some((__pv_without_last(v), x));
    },
    None => {
      return None;
    }
  }
}
```

### `10 function __pv_without_last[T](v: PVec[T]): PVec[T]`

```fern
<<10 function __pv_without_last[T](v: PVec[T]): PVec[T]>>=
function __pv_without_last[T](v: PVec[T]): PVec[T] {
  if (v.len == 1) {
    return pvec_new();
  }
  if (v.len - __pv_tail_off(v.len) > 1) {
    let t: T[] = [];
    let i: i32 = 0;
    while (i + 1 < v.tail.len()) {
      t = t.append(v.tail[i]);
      i = i + 1;
    }
    return PVec { len: v.len - 1, shift: v.shift, root: v.root, tail: t };
  }
  let newTail: T[] = __pv_leaf_for(v.root, v.shift, v.len - 2);
  match (v.root) {
    Leaf(xs) => {
      let e: VNode[T] = Empty;
      return PVec { len: v.len - 1, shift: 0, root: e, tail: newTail };
    },
    Branch(kids) => {
      let root: VNode[T] = __pv_pop_tail(v.root, v.shift, v.len);
      return __pv_shrink_root(v.len - 1, root, v.shift, newTail);
    },
    Empty => {
      return v;
    }
  }
}
```

### `11 pub function (v: PVec[T]) iter(): iter.ArrayIter[T]`

```fern
<<11 pub function (v: PVec[T]) iter(): iter.ArrayIter[T]>>=
pub function (v: PVec[T]) iter(): iter.ArrayIter[T] {
  return iter.of(v.to_array());
}

pub function (v: PVec[T]) to_array(): T[] {
  let acc: T[] = [];
  acc = __pv_into(v.root, acc);
  acc = __pv_into_elems(v.tail, 0, acc);
  return acc;
}

pub function (v: PVec[T]) fold[T, A](init: A, f: (A, T) => A): A {
  let a: A = __pv_fold(v.root, init, f);
  let i: i32 = 0;
  while (i < v.tail.len()) {
    a = f(a, v.tail[i]);
    i = i + 1;
  }
  return a;
}

pub function (v: PVec[T]) for_each(f: (T) => void): void {
  __pv_for_each(v.root, f);
  let i: i32 = 0;
  while (i < v.tail.len()) {
    f(v.tail[i]);
    i = i + 1;
  }
}

pub function (v: PVec[T]) map[T, U](f: (T) => U): PVec[U] {
  let out: PVec[U] = pvec_new();
  let xs: T[] = v.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    out = out.append(f(xs[i]));
    i = i + 1;
  }
  return out;
}

pub function (v: PVec[T]) filter(pred: (T) => boolean): PVec[T] {
  let out: PVec[T] = pvec_new();
  let xs: T[] = v.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    if (pred(xs[i])) {
      out = out.append(xs[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (v: PVec[T]) concat(other: PVec[T]): PVec[T] {
  let out: PVec[T] = v;
  let xs: T[] = other.to_array();
  let i: i32 = 0;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

pub function (v: PVec[T]) slice(lo: i32, hi: i32): PVec[T] {
  let out: PVec[T] = pvec_new();
  let i: i32 = lo;
  if (i < 0) {
    i = 0;
  }
  let end: i32 = hi;
  if (end > v.len) {
    end = v.len;
  }
  while (i < end) {
    out = out.append(v.get_or(i, v.tail[0]));
    i = i + 1;
  }
  return out;
}
```

### `12 function __pv_valid_root[T](root: VNode[T], shift: i32, off: i32): boolean`

```fern
<<12 function __pv_valid_root[T](root: VNode[T], shift: i32, off: i32): boolean>>=
function __pv_valid_root[T](root: VNode[T], shift: i32, off: i32): boolean {
  match (root) {
    Empty => {
      return off == 0 && shift == 0;
    },
    Leaf(xs) => {
      return shift == 0 && __pv_valid(root, 0, off);
    },
    Branch(kids) => {
      return shift > 0 && kids.len() >= 2 && __pv_valid(root, shift, off);
    }
  }
}
```

### `13 pub function (v: PVec[T]) is_valid(): boolean`

```fern
<<13 pub function (v: PVec[T]) is_valid(): boolean>>=
pub function (v: PVec[T]) is_valid(): boolean {
  let off: i32 = __pv_tail_off(v.len);
  if (v.tail.len() != v.len - off) {
    return false;
  }
  if (v.len > 0 && v.tail.len() == 0) {
    return false;
  }
  return __pv_valid_root(v.root, v.shift, off);
}
```
