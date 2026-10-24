# Persistent hash maps

This module provides persistent hash maps.

## Model

A persistent map stores hash-directed branches, leaves, and collision nodes. Updates return a new value with shared structure.

## Contracts

Keys require both hash and equality traits. Equal keys must have compatible hashes. Hash collisions still require key equality checks.

## Executable example

```fern test name=std-pmap-example
import "std/pmap";
import "std/option";

function main(): i32 {
  let a = pmap.pmap_new[string, i32]();
  let b = a.insert("answer", 42);
  if (!a.is_empty() || b.get("answer").unwrap_or(0) != 42) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum PMap[K, V]>>

<<4 pub function pmap_new[K, V](): PMap[K, V]>>

<<5 function node_mix(x: i32): i32>>

<<6 function node_insert[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K, v: V): PMap[K, V]>>

<<7 function node_remove[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K): PMap[K, V]>>

<<8 function node_fold[K, V, A](n: PMap[K, V], init: A, f: (A, K, V)>>

<<9 function node_valid_branch[K, V](bm: i32, s: i32, kids: PMap[K, V][], h: i32, shift: i32): boolean>>

<<10 pub function (m: PMap[K, V]) len(): i32>>

<<11 pub function (m: PMap[K, V]) merge[K: cmp.Hash + cmp.Eq, V](other: PMap[K, V]): PMap[K, V]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides persistent hash maps.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "core/iter";
import "std/i32";
```

### `3 pub enum PMap[K, V]`

```fern
<<3 pub enum PMap[K, V]>>=
pub enum PMap[K, V] { Empty, Leaf(i32, K, V), Coll(i32, PMap[K, V][]), Branch(i32, i32, PMap[K, V][]) }
```

### `4 pub function pmap_new[K, V](): PMap[K, V]`

```fern
<<4 pub function pmap_new[K, V](): PMap[K, V]>>=
pub function pmap_new[K, V](): PMap[K, V] {
  let e: PMap[K, V] = Empty;
  return e;
}

pub function from[K: cmp.Hash + cmp.Eq, V](pairs: (K, V)[]): PMap[K, V] {
  let m: PMap[K, V] = pmap_new();
  for p in pairs {
    m = m.insert(p.0, p.1);
  }
  return m;
}
```

### `5 function node_mix(x: i32): i32`

```fern
<<5 function node_mix(x: i32): i32>>=
function node_mix(x: i32): i32 {
  let h: i32 = x;
  h = h ^ (h as u32 >> 16 as u32) as i32;
  h = h * (0 - 2048144789);
  h = h ^ (h as u32 >> 13 as u32) as i32;
  h = h * (0 - 1028477387);
  h = h ^ (h as u32 >> 16 as u32) as i32;
  return h;
}

function node_frag(h: i32, shift: i32): i32 {
  return (h as u32 >> shift as u32) as i32 & 31;
}

function node_slot(bitmap: i32, bit: i32): i32 {
  return (bitmap & bit - 1).count_ones();
}

function node_size[K, V](n: PMap[K, V]): i32 {
  match (n) {
    Empty => {
      return 0;
    },
    Leaf(h, k, v) => {
      return 1;
    },
    Coll(h, leaves) => {
      return leaves.len();
    },
    Branch(bm, s, kids) => {
      return s;
    }
  }
}

function node_insert_at[K, V](xs: PMap[K, V][], i: i32, x: PMap[K, V]): PMap[K, V][] {
  let out: PMap[K, V][] = [];
  let j: i32 = 0;
  while (j < i) {
    out = out.append(xs[j]);
    j = j + 1;
  }
  out = out.append(x);
  while (j < xs.len()) {
    out = out.append(xs[j]);
    j = j + 1;
  }
  return out;
}

function node_remove_at[K, V](xs: PMap[K, V][], i: i32): PMap[K, V][] {
  let out: PMap[K, V][] = [];
  let j: i32 = 0;
  while (j < xs.len()) {
    if (j != i) {
      out = out.append(xs[j]);
    }
    j = j + 1;
  }
  return out;
}

function node_pair[K, V](a: PMap[K, V], ah: i32, b: PMap[K, V], bh: i32, shift: i32): PMap[K, V] {
  if (ah == bh) {
    return Coll(ah, [a, b]);
  }
  if (shift >= 32) {
    return Coll(ah, [a, b]);
  }
  let fa: i32 = node_frag(ah, shift);
  let fb: i32 = node_frag(bh, shift);
  if (fa == fb) {
    let inner: PMap[K, V] = node_pair(a, ah, b, bh, shift + 5);
    return Branch(1 << fa, 2, [inner]);
  }
  if (fa < fb) {
    return Branch(1 << fa | 1 << fb, 2, [a, b]);
  }
  return Branch(1 << fa | 1 << fb, 2, [b, a]);
}

function node_coll_index[K: cmp.Hash + cmp.Eq, V](leaves: PMap[K, V][], k: K): i32 {
  let i: i32 = 0;
  while (i < leaves.len()) {
    match (leaves[i]) {
      Leaf(eh, ek, ev) => {
        if (ek.eq(k)) {
          return i;
        }
      },
      Empty => {},
      Coll(xh, xs) => {},
      Branch(xb, xs, xk) => {}
    }
    i = i + 1;
  }
  return 0 - 1;
}
```

### `6 function node_insert[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K, v: V): PMap[K, V]`

```fern
<<6 function node_insert[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K, v: V): PMap[K, V]>>=
function node_insert[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K, v: V): PMap[K, V] {
  match (n) {
    Empty => {
      return Leaf(h, k, v);
    },
    Leaf(lh, lk, lv) => {
      if (lh == h && lk.eq(k)) {
        return Leaf(h, k, v);
      }
      let fresh: PMap[K, V] = Leaf(h, k, v);
      return node_pair(Leaf(lh, lk, lv), lh, fresh, h, shift);
    },
    Coll(ch, leaves) => {
      if (ch != h) {
        let fresh: PMap[K, V] = Leaf(h, k, v);
        return node_pair(Coll(ch, leaves), ch, fresh, h, shift);
      }
      let i: i32 = node_coll_index(leaves, k);
      if (i >= 0) {
        return Coll(ch, leaves.with(i, Leaf(h, k, v)));
      }
      return Coll(ch, leaves.append(Leaf(h, k, v)));
    },
    Branch(bm, s, kids) => {
      let bit: i32 = 1 << node_frag(h, shift);
      let idx: i32 = node_slot(bm, bit);
      if ((bm & bit) == 0) {
        let fresh: PMap[K, V] = Leaf(h, k, v);
        let grown: PMap[K, V][] = node_insert_at(kids, idx, fresh);
        return Branch(bm | bit, s + 1, grown);
      }
      let e: PMap[K, V] = Empty;
      let child: PMap[K, V] = kids[idx];
      let rest: PMap[K, V][] = kids.with(idx, e);
      let old: i32 = node_size(child);
      child = node_insert(child, h, shift + 5, k, v);
      let added: i32 = node_size(child) - old;
      return Branch(bm, s + added, rest.with(idx, child));
    }
  }
}

function node_find[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, k: K): PMap[K, V] {
  let cur: PMap[K, V] = n;
  let shift: i32 = 0;
  let descending: boolean = true;
  while (descending) {
    match (cur) {
      Empty => {
        return cur;
      },
      Leaf(lh, lk, lv) => {
        if (lk.eq(k)) {
          return cur;
        }
        descending = false;
      },
      Coll(ch, leaves) => {
        let i: i32 = node_coll_index(leaves, k);
        if (i >= 0) {
          return leaves[i];
        }
        descending = false;
      },
      Branch(bm, s, kids) => {
        let bit: i32 = 1 << node_frag(h, shift);
        if ((bm & bit) == 0) {
          descending = false;
        } else {
          cur = kids[node_slot(bm, bit)];
          shift = shift + 5;
        }
      }
    }
  }
  let e: PMap[K, V] = Empty;
  return e;
}

function node_is_branch[K, V](n: PMap[K, V]): boolean {
  match (n) {
    Branch(bm, s, kids) => {
      return true;
    },
    Empty => {
      return true;
    },
    Leaf(h, k, v) => {
      return false;
    },
    Coll(h, xs) => {
      return false;
    }
  }
}

function node_remove_branch[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], bm: i32, s: i32, kids: PMap[K, V][], h: i32, shift: i32, k: K): PMap[K, V] {
  let bit: i32 = 1 << node_frag(h, shift);
  if ((bm & bit) == 0) {
    return n;
  }
  let idx: i32 = node_slot(bm, bit);
  let old: i32 = node_size(kids[idx]);
  let child: PMap[K, V] = node_remove(kids[idx], h, shift + 5, k);
  let now: i32 = node_size(child);
  if (now == old) {
    return n;
  }
  if (now > 0) {
    if (kids.len() == 1 && !node_is_branch(child)) {
      return child;
    }
    return Branch(bm, s - 1, kids.with(idx, child));
  }
  if (kids.len() == 1) {
    return child;
  }
  let rest: PMap[K, V][] = node_remove_at(kids, idx);
  if (rest.len() == 1 && !node_is_branch(rest[0])) {
    return rest[0];
  }
  return Branch(bm & 0 - 1 - bit, s - 1, rest);
}
```

### `7 function node_remove[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K): PMap[K, V]`

```fern
<<7 function node_remove[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K): PMap[K, V]>>=
function node_remove[K: cmp.Hash + cmp.Eq, V](n: PMap[K, V], h: i32, shift: i32, k: K): PMap[K, V] {
  match (n) {
    Empty => {
      return n;
    },
    Leaf(lh, lk, lv) => {
      if (lh == h && lk.eq(k)) {
        let e: PMap[K, V] = Empty;
        return e;
      }
      return n;
    },
    Coll(ch, leaves) => {
      if (ch != h) {
        return n;
      }
      let i: i32 = node_coll_index(leaves, k);
      if (i < 0) {
        return n;
      }
      if (leaves.len() == 2) {
        return leaves[1 - i];
      }
      return Coll(ch, node_remove_at(leaves, i));
    },
    Branch(bm, s, kids) => {
      return node_remove_branch(n, bm, s, kids, h, shift, k);
    }
  }
}

function node_keys_into[K, V](n: PMap[K, V], own acc: K[]): K[] {
  match (n) {
    Empty => {
      return acc;
    },
    Leaf(h, k, v) => {
      acc = acc.append(k);
      return acc;
    },
    Coll(h, leaves) => {
      acc = node_keys_kids(leaves, 0, acc);
      return acc;
    },
    Branch(bm, s, kids) => {
      acc = node_keys_kids(kids, 0, acc);
      return acc;
    }
  }
}

function node_keys_kids[K, V](kids: PMap[K, V][], i: i32, own acc: K[]): K[] {
  if (i >= kids.len()) {
    return acc;
  }
  acc = node_keys_into(kids[i], acc);
  acc = node_keys_kids(kids, i + 1, acc);
  return acc;
}

function node_values_into[K, V](n: PMap[K, V], own acc: V[]): V[] {
  match (n) {
    Empty => {
      return acc;
    },
    Leaf(h, k, v) => {
      acc = acc.append(v);
      return acc;
    },
    Coll(h, leaves) => {
      acc = node_values_kids(leaves, 0, acc);
      return acc;
    },
    Branch(bm, s, kids) => {
      acc = node_values_kids(kids, 0, acc);
      return acc;
    }
  }
}

function node_values_kids[K, V](kids: PMap[K, V][], i: i32, own acc: V[]): V[] {
  if (i >= kids.len()) {
    return acc;
  }
  acc = node_values_into(kids[i], acc);
  acc = node_values_kids(kids, i + 1, acc);
  return acc;
}

function node_entries_into[K, V](n: PMap[K, V], own acc: (K, V)[]): (K, V)[] {
  match (n) {
    Empty => {
      return acc;
    },
    Leaf(h, k, v) => {
      acc = acc.append((k, v));
      return acc;
    },
    Coll(h, leaves) => {
      acc = node_entries_kids(leaves, 0, acc);
      return acc;
    },
    Branch(bm, s, kids) => {
      acc = node_entries_kids(kids, 0, acc);
      return acc;
    }
  }
}

function node_entries_kids[K, V](kids: PMap[K, V][], i: i32, own acc: (K, V)[]): (K, V)[] {
  if (i >= kids.len()) {
    return acc;
  }
  acc = node_entries_into(kids[i], acc);
  acc = node_entries_kids(kids, i + 1, acc);
  return acc;
}
```

### `8 function node_fold[K, V, A](n: PMap[K, V], init: A, f: (A, K, V)`

```fern
<<8 function node_fold[K, V, A](n: PMap[K, V], init: A, f: (A, K, V)>>=
function node_fold[K, V, A](n: PMap[K, V], init: A, f: (A, K, V) => A): A {
  match (n) {
    Empty => {
      return init;
    },
    Leaf(h, k, v) => {
      return f(init, k, v);
    },
    Coll(h, leaves) => {
      let a: A = init;
      let i: i32 = 0;
      while (i < leaves.len()) {
        a = node_fold(leaves[i], a, f);
        i = i + 1;
      }
      return a;
    },
    Branch(bm, s, kids) => {
      let a: A = init;
      let i: i32 = 0;
      while (i < kids.len()) {
        a = node_fold(kids[i], a, f);
        i = i + 1;
      }
      return a;
    }
  }
}

function node_for_each[K, V](n: PMap[K, V], f: (K, V) => void): void {
  match (n) {
    Empty => {
      return;
    },
    Leaf(h, k, v) => {
      f(k, v);
    },
    Coll(h, leaves) => {
      let i: i32 = 0;
      while (i < leaves.len()) {
        node_for_each(leaves[i], f);
        i = i + 1;
      }
    },
    Branch(bm, s, kids) => {
      let i: i32 = 0;
      while (i < kids.len()) {
        node_for_each(kids[i], f);
        i = i + 1;
      }
    }
  }
}

function node_map_values[K, V, W](n: PMap[K, V], f: (V) => W): PMap[K, W] {
  match (n) {
    Empty => {
      let e: PMap[K, W] = Empty;
      return e;
    },
    Leaf(h, k, v) => {
      return Leaf(h, k, f(v));
    },
    Coll(h, leaves) => {
      let out: PMap[K, W][] = [];
      let i: i32 = 0;
      while (i < leaves.len()) {
        out = out.append(node_map_values(leaves[i], f));
        i = i + 1;
      }
      return Coll(h, out);
    },
    Branch(bm, s, kids) => {
      let out: PMap[K, W][] = [];
      let i: i32 = 0;
      while (i < kids.len()) {
        out = out.append(node_map_values(kids[i], f));
        i = i + 1;
      }
      return Branch(bm, s, out);
    }
  }
}

function node_prefix_ok(lh: i32, h: i32, shift: i32): boolean {
  if (shift == 0) {
    return true;
  }
  let keep: u32 = (32 - shift) as u32;
  return (lh as u32 << keep) as i32 == (h as u32 << keep) as i32;
}

function node_valid_coll[K, V](ch: i32, leaves: PMap[K, V][]): boolean {
  if (leaves.len() < 2) {
    return false;
  }
  let i: i32 = 0;
  while (i < leaves.len()) {
    match (leaves[i]) {
      Leaf(lh, lk, lv) => {
        if (lh != ch) {
          return false;
        }
      },
      Empty => {
        return false;
      },
      Coll(xh, xs) => {
        return false;
      },
      Branch(xb, xs, xk) => {
        return false;
      }
    }
    i = i + 1;
  }
  return true;
}

function node_child_hash[K, V](n: PMap[K, V]): i32 {
  match (n) {
    Leaf(lh, lk, lv) => {
      return lh;
    },
    Coll(ch, xs) => {
      return ch;
    },
    Branch(bm, s, kids) => {
      return 0 - 1;
    },
    Empty => {
      return 0 - 1;
    }
  }
}
```

### `9 function node_valid_branch[K, V](bm: i32, s: i32, kids: PMap[K, V][], h: i32, shift: i32): boolean`

```fern
<<9 function node_valid_branch[K, V](bm: i32, s: i32, kids: PMap[K, V][], h: i32, shift: i32): boolean>>=
function node_valid_branch[K, V](bm: i32, s: i32, kids: PMap[K, V][], h: i32, shift: i32): boolean {
  if (bm.count_ones() != kids.len() || kids.len() == 0) {
    return false;
  }
  if (kids.len() == 1 && !node_is_branch(kids[0])) {
    return false;
  }
  let total: i32 = 0;
  let i: i32 = 0;
  let frag: i32 = 0;
  while (frag < 32) {
    if ((bm & 1 << frag) != 0) {
      let child: PMap[K, V] = kids[i];
      let ch: i32 = node_child_hash(child);
      if (ch != 0 - 1 && node_frag(ch, shift) != frag) {
        return false;
      }
      if (!node_valid(child, h | frag << shift, shift + 5)) {
        return false;
      }
      total = total + node_size(child);
      i = i + 1;
    }
    frag = frag + 1;
  }
  return total == s;
}

function node_valid[K, V](n: PMap[K, V], h: i32, shift: i32): boolean {
  match (n) {
    Empty => {
      return shift == 0;
    },
    Leaf(lh, lk, lv) => {
      return node_prefix_ok(lh, h, shift);
    },
    Coll(ch, leaves) => {
      return node_prefix_ok(ch, h, shift) && node_valid_coll(ch, leaves);
    },
    Branch(bm, s, kids) => {
      return node_valid_branch(bm, s, kids, h, shift);
    }
  }
}
```

### `10 pub function (m: PMap[K, V]) len(): i32`

```fern
<<10 pub function (m: PMap[K, V]) len(): i32>>=
pub function (m: PMap[K, V]) len(): i32 {
  return node_size(m);
}

pub function (m: PMap[K, V]) is_empty(): boolean {
  return node_size(m) == 0;
}

pub function (m: PMap[K, V]) insert[K: cmp.Hash + cmp.Eq, V](k: K, v: V): PMap[K, V] {
  return node_insert(m, node_mix(k.hash()), 0, k, v);
}

pub function (m: PMap[K, V]) remove[K: cmp.Hash + cmp.Eq, V](k: K): PMap[K, V] {
  return node_remove(m, node_mix(k.hash()), 0, k);
}

pub function (m: PMap[K, V]) get[K: cmp.Hash + cmp.Eq, V](k: K): Option[V] {
  match (node_find(m, node_mix(k.hash()), k)) {
    Leaf(h, lk, lv) => {
      return Some(lv);
    },
    Empty => {
      return None;
    },
    Coll(h, xs) => {
      return None;
    },
    Branch(b, s, xs) => {
      return None;
    }
  }
}

pub function (m: PMap[K, V]) get_or[K: cmp.Hash + cmp.Eq, V](k: K, fallback: V): V {
  match (node_find(m, node_mix(k.hash()), k)) {
    Leaf(h, lk, lv) => {
      return lv;
    },
    Empty => {
      return fallback;
    },
    Coll(h, xs) => {
      return fallback;
    },
    Branch(b, s, xs) => {
      return fallback;
    }
  }
}

pub function (m: PMap[K, V]) contains[K: cmp.Hash + cmp.Eq, V](k: K): boolean {
  match (node_find(m, node_mix(k.hash()), k)) {
    Leaf(h, lk, lv) => {
      return true;
    },
    Empty => {
      return false;
    },
    Coll(h, xs) => {
      return false;
    },
    Branch(b, s, xs) => {
      return false;
    }
  }
}

pub function (m: PMap[K, V]) update[K: cmp.Hash + cmp.Eq, V](k: K, init: V, f: (V) => V): PMap[K, V] {
  let h: i32 = node_mix(k.hash());
  match (node_find(m, h, k)) {
    Leaf(lh, lk, lv) => {
      return node_insert(m, h, 0, k, f(lv));
    },
    _ => {
      return node_insert(m, h, 0, k, f(init));
    }
  }
}

pub function (m: PMap[K, V]) keys(): K[] {
  let acc: K[] = [];
  acc = node_keys_into(m, acc);
  return acc;
}

pub function (m: PMap[K, V]) values(): V[] {
  let acc: V[] = [];
  acc = node_values_into(m, acc);
  return acc;
}

pub function (m: PMap[K, V]) iter(): iter.ArrayIter[(K, V)] {
  let acc: (K, V)[] = [];
  acc = node_entries_into(m, acc);
  return iter.of(acc);
}

pub function (m: PMap[K, V]) fold[K, V, A](init: A, f: (A, K, V) => A): A {
  return node_fold(m, init, f);
}

pub function (m: PMap[K, V]) for_each(f: (K, V) => void): void {
  node_for_each(m, f);
}

pub function (m: PMap[K, V]) map_values[K, V, W](f: (V) => W): PMap[K, W] {
  return node_map_values(m, f);
}

pub function (m: PMap[K, V]) filter[K: cmp.Hash + cmp.Eq, V](pred: (K, V) => boolean): PMap[K, V] {
  let out: PMap[K, V] = pmap_new();
  let ks: K[] = m.keys();
  let vs: V[] = m.values();
  let i: i32 = 0;
  while (i < ks.len()) {
    if (pred(ks[i], vs[i])) {
      out = out.insert(ks[i], vs[i]);
    }
    i = i + 1;
  }
  return out;
}
```

### `11 pub function (m: PMap[K, V]) merge[K: cmp.Hash + cmp.Eq, V](other: PMap[K, V]): PMap[K, V]`

```fern
<<11 pub function (m: PMap[K, V]) merge[K: cmp.Hash + cmp.Eq, V](other: PMap[K, V]): PMap[K, V]>>=
pub function (m: PMap[K, V]) merge[K: cmp.Hash + cmp.Eq, V](other: PMap[K, V]): PMap[K, V] {
  if (m.len() >= other.len()) {
    let out: PMap[K, V] = m;
    let ks: K[] = other.keys();
    let vs: V[] = other.values();
    let i: i32 = 0;
    while (i < ks.len()) {
      out = out.insert(ks[i], vs[i]);
      i = i + 1;
    }
    return out;
  }
  let out: PMap[K, V] = other;
  let ks: K[] = m.keys();
  let vs: V[] = m.values();
  let i: i32 = 0;
  while (i < ks.len()) {
    if (!out.contains(ks[i])) {
      out = out.insert(ks[i], vs[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (m: PMap[K, V]) entries(): (K, V)[] {
  let out: (K, V)[] = [];
  let ks: K[] = m.keys();
  let vs: V[] = m.values();
  let i: i32 = 0;
  while (i < ks.len()) {
    out = out.append((ks[i], vs[i]));
    i = i + 1;
  }
  return out;
}

pub function (m: PMap[K, V]) intersection[K: cmp.Hash + cmp.Eq, V](other: PMap[K, V]): PMap[K, V] {
  let out: PMap[K, V] = pmap_new();
  let ks: K[] = m.keys();
  let vs: V[] = m.values();
  let i: i32 = 0;
  while (i < ks.len()) {
    if (other.contains(ks[i])) {
      out = out.insert(ks[i], vs[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function (m: PMap[K, V]) difference[K: cmp.Hash + cmp.Eq, V](other: PMap[K, V]): PMap[K, V] {
  let out: PMap[K, V] = m;
  let ks: K[] = other.keys();
  let i: i32 = 0;
  while (i < ks.len()) {
    out = out.remove(ks[i]);
    i = i + 1;
  }
  return out;
}

pub function (m: PMap[K, V]) is_valid(): boolean {
  return node_valid(m, 0, 0);
}
```
