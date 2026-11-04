# Multidimensional array views

This module provides multidimensional arrays with flat storage.

## Model

An array combines flat storage with a shape, strides, and an offset. These fields describe the relation between logical coordinates and stored elements.

## Contracts

A view can have different strides from a packed row-major array. Operations that require packed data must use the layout contract rather than shape alone.

## Executable example

```fern test name=std-ndarray-example
import "std/ndarray";

function main(): i32 {
  let a = ndarray.from_flat([1, 2, 3, 4], [2, 2]);
  if (a.rank() != 2 || a.len() != 4 || !a.is_row_major()) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<2 pub struct NdArray[T]>>

<<3 function shape_error(): void>>

<<4 pub function from_flat[T](data: T[], shape: i32[]): NdArray[T]>>

<<5 function addr[T](a: NdArray[T], idx: i32[]): i32>>

<<6 pub function (a: NdArray[T]) get(idx: i32[]): Option[T]>>

<<7 function check_axis[T](a: NdArray[T], axis: i32): void>>

<<8 pub function (a: NdArray[T]) transpose(): NdArray[T]>>

<<9 function addr_of[T](a: NdArray[T], idx: i32[]): i32>>

<<10 pub function (a: NdArray[T]) map[U](f: (T)>>

<<11 function packed_map[T, U](data: T[], f: (T)>>

<<12 pub function broadcast_shape(x: i32[], y: i32[]): i32[]>>

<<13 function packed_zip[T, U, V](x: T[], y: U[], f: (T, U)>>

<<14 pub function (a: NdArray[T]) outer[U, V](b: NdArray[U], f: (T, U)>>

<<15 function packed_outer[T, U, V](a: T[], b: U[], shape: i32[], f: (T, U)>>

<<16 pub function (a: NdArray[T]) inner[A](b: NdArray[T], init: A, mul: (T, T)>>

<<17 function inner_shape(a: i32[], b: i32[]): i32[]>>

<<18 pub function (a: NdArray[T]) fold_all[A](init: A, f: (A, T)>>

<<19 function packed_fold[T, A](data: T[], init: A, f: (A, T)>>

<<20 pub function (a: NdArray[T]) reduce_axis[A](axis: i32, init: A, f: (A, T)>>

<<21 function copy_flat[T](a: NdArray[T]): T[]>>

<<22 cmp impls>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides multidimensional arrays with flat storage.
```

### `dependencies`

```fern
<<dependencies>>=
import "core/cmp";
```

### `2 pub struct NdArray[T]`

```fern
<<2 pub struct NdArray[T]>>=
pub struct NdArray[T] { data: T[], shape: i32[], strides: i32[], offset: i32 }
```

### `3 function shape_error(): void`

```fern
<<3 function shape_error(): void>>=
function shape_error(): void {
  exit(134);
}

function check_shape(shape: i32[]): void {
  let n = 1;
  let k = 0;
  while (k < shape.len()) {
    if (shape[k] < 0) {
      shape_error();
    }
    if (shape[k] != 0 && n > 2147483647 / shape[k]) {
      shape_error();
    }
    n *= shape[k];
    k += 1;
  }
}

function count_of(shape: i32[]): i32 {
  check_shape(shape);
  let n = 1;
  let k = 0;
  while (k < shape.len()) {
    n *= shape[k];
    k += 1;
  }
  return n;
}

function row_major(shape: i32[]): i32[] {
  let out: i32[] = [];
  let k = 0;
  while (k < shape.len()) {
    out = out.append(0);
    k += 1;
  }
  let s = 1;
  k = shape.len() - 1;
  while (k >= 0) {
    out = out.with(k, s);
    s *= shape[k];
    k -= 1;
  }
  return out;
}
```

### `4 pub function from_flat[T](data: T[], shape: i32[]): NdArray[T]`

```fern
<<4 pub function from_flat[T](data: T[], shape: i32[]): NdArray[T]>>=
pub function from_flat[T](data: T[], shape: i32[]): NdArray[T] {
  if (count_of(shape) != data.len()) {
    shape_error();
  }
  return NdArray { data: data, shape: shape, strides: row_major(shape), offset: 0 };
}

pub function (a: NdArray[T]) shape(): i32[] {
  return a.shape;
}

pub function (a: NdArray[T]) strides(): i32[] {
  return a.strides;
}

pub function (a: NdArray[T]) rank(): i32 {
  return a.shape.len();
}

pub function (a: NdArray[T]) len(): i32 {
  return count_of(a.shape);
}

pub function (a: NdArray[T]) is_row_major(): boolean {
  let rm = row_major(a.shape);
  let k = 0;
  while (k < a.shape.len()) {
    if (a.shape[k] != 1 && a.strides[k] != rm[k]) {
      return false;
    }
    k += 1;
  }
  return true;
}

pub function (a: NdArray[T]) is_packed(): boolean {
  return a.is_row_major() && a.offset == 0 && a.data.len() == a.len();
}
```

### `5 function addr[T](a: NdArray[T], idx: i32[]): i32`

```fern
<<5 function addr[T](a: NdArray[T], idx: i32[]): i32>>=
function addr[T](a: NdArray[T], idx: i32[]): i32 {
  if (idx.len() != a.shape.len()) {
    return -1;
  }
  let at = a.offset;
  let k = 0;
  while (k < idx.len()) {
    if (idx[k] < 0 || idx[k] >= a.shape[k]) {
      return -1;
    }
    at = at + idx[k] * a.strides[k];
    k += 1;
  }
  return at;
}
```

### `6 pub function (a: NdArray[T]) get(idx: i32[]): Option[T]`

```fern
<<6 pub function (a: NdArray[T]) get(idx: i32[]): Option[T]>>=
pub function (a: NdArray[T]) get(idx: i32[]): Option[T] {
  let at = addr(a, idx);
  if (at < 0) {
    return None;
  }
  return Some(a.data[at]);
}

pub function (a: NdArray[T]) get_unchecked(idx: i32[]): T {
  let at = addr(a, idx);
  if (at < 0) {
    shape_error();
  }
  return a.data[at];
}
```

### `7 function check_axis[T](a: NdArray[T], axis: i32): void`

```fern
<<7 function check_axis[T](a: NdArray[T], axis: i32): void>>=
function check_axis[T](a: NdArray[T], axis: i32): void {
  if (axis < 0 || axis >= a.shape.len()) {
    shape_error();
  }
}
```

### `8 pub function (a: NdArray[T]) transpose(): NdArray[T]`

```fern
<<8 pub function (a: NdArray[T]) transpose(): NdArray[T]>>=
pub function (a: NdArray[T]) transpose(): NdArray[T] {
  let axes: i32[] = [];
  let k = a.shape.len() - 1;
  while (k >= 0) {
    axes = axes.append(k);
    k -= 1;
  }
  return a.permute(axes);
}

pub function (a: NdArray[T]) permute(axes: i32[]): NdArray[T] {
  if (axes.len() != a.shape.len()) {
    shape_error();
  }
  let seen: boolean[] = [];
  let shape: i32[] = [];
  let strides: i32[] = [];
  let k = 0;
  while (k < axes.len()) {
    seen = seen.append(false);
    k += 1;
  }
  k = 0;
  while (k < axes.len()) {
    check_axis(a, axes[k]);
    if (seen[axes[k]]) {
      shape_error();
    }
    seen = seen.with(axes[k], true);
    shape = shape.append(a.shape[axes[k]]);
    strides = strides.append(a.strides[axes[k]]);
    k += 1;
  }
  return NdArray { data: a.data, shape: shape, strides: strides, offset: a.offset };
}

pub function (a: NdArray[T]) reverse(axis: i32): NdArray[T] {
  check_axis(a, axis);
  let n = a.shape[axis];
  let s = a.strides[axis];
  let offset = a.offset;
  if (n > 0) {
    offset = offset + (n - 1) * s;
  }
  return NdArray { data: a.data, shape: a.shape, strides: a.strides.with(axis, 0 - s), offset: offset };
}

pub function (a: NdArray[T]) slice(axis: i32, lo: i32, hi: i32): NdArray[T] {
  check_axis(a, axis);
  if (lo < 0 || lo > hi || hi > a.shape[axis]) {
    shape_error();
  }
  let offset = a.offset + lo * a.strides[axis];
  return NdArray { data: a.data, shape: a.shape.with(axis, hi - lo), strides: a.strides, offset: offset };
}

pub function (a: NdArray[T]) select(axis: i32, i: i32): NdArray[T] {
  check_axis(a, axis);
  if (i < 0 || i >= a.shape[axis]) {
    shape_error();
  }
  let shape: i32[] = [];
  let strides: i32[] = [];
  let k = 0;
  while (k < a.shape.len()) {
    if (k != axis) {
      shape = shape.append(a.shape[k]);
      strides = strides.append(a.strides[k]);
    }
    k += 1;
  }
  return NdArray { data: a.data, shape: shape, strides: strides, offset: a.offset + i * a.strides[axis] };
}

pub function (a: NdArray[T]) reshape(shape: i32[]): NdArray[T] {
  if (count_of(shape) != a.len()) {
    shape_error();
  }
  if (a.is_row_major()) {
    return NdArray { data: a.data, shape: shape, strides: row_major(shape), offset: a.offset };
  }
  return from_flat(copy_flat(a), shape);
}

pub function (a: NdArray[T]) packed(): NdArray[T] {
  if (a.is_packed()) {
    return a;
  }
  return from_flat(copy_flat(a), a.shape);
}

pub function (a: NdArray[T]) to_flat(): T[] {
  if (a.is_packed()) {
    return a.data;
  }
  return copy_flat(a);
}
```

### `9 function addr_of[T](a: NdArray[T], idx: i32[]): i32`

```fern
<<9 function addr_of[T](a: NdArray[T], idx: i32[]): i32>>=
function addr_of[T](a: NdArray[T], idx: i32[]): i32 {
  let at = a.offset;
  let k = 0;
  while (k < idx.len()) {
    at = at + idx[k] * a.strides[k];
    k += 1;
  }
  return at;
}

function zeros(rank: i32): i32[] {
  let idx: i32[] = [];
  let k = 0;
  while (k < rank) {
    idx = idx.append(0);
    k += 1;
  }
  return idx;
}
```

### `10 pub function (a: NdArray[T]) map[U](f: (T)`

```fern
<<10 pub function (a: NdArray[T]) map[U](f: (T)>>=
pub function (a: NdArray[T]) map[U](f: (T) => U): NdArray[U] {
  if (a.is_packed()) {
    return from_flat(packed_map(a.data, f), a.shape);
  }
  let out: U[] = [];
  let n = a.len();
  let idx = zeros(a.shape.len());
  let i = 0;
  while (i < n) {
    out = out.append(f(a.data[addr_of(a, idx)]));
    idx = bump(idx, a.shape);
    i += 1;
  }
  return from_flat(out, a.shape);
}
```

### `11 function packed_map[T, U](data: T[], f: (T)`

```fern
<<11 function packed_map[T, U](data: T[], f: (T)>>=
function packed_map[T, U](data: T[], f: (T) => U): U[] {
  let out: U[] = [];
  let n = data.len();
  let i = 0;
  while (i < n) {
    out = out.append(f(data[i]));
    i += 1;
  }
  return out;
}

function same_shape(x: i32[], y: i32[]): boolean {
  if (x.len() != y.len()) {
    return false;
  }
  let k = 0;
  while (k < x.len()) {
    if (x[k] != y[k]) {
      return false;
    }
    k += 1;
  }
  return true;
}
```

### `12 pub function broadcast_shape(x: i32[], y: i32[]): i32[]`

```fern
<<12 pub function broadcast_shape(x: i32[], y: i32[]): i32[]>>=
pub function broadcast_shape(x: i32[], y: i32[]): i32[] {
  check_shape(x);
  check_shape(y);
  let rank = x.len();
  if (y.len() > rank) {
    rank = y.len();
  }
  let out = zeros(rank);
  let k = 0;
  while (k < rank) {
    let ex = 1;
    let ey = 1;
    if (k >= rank - x.len()) {
      ex = x[k - (rank - x.len())];
    }
    if (k >= rank - y.len()) {
      ey = y[k - (rank - y.len())];
    }
    if (ex == ey || ey == 1) {
      out = out.with(k, ex);
    } else if (ex == 1) {
      out = out.with(k, ey);
    } else {
      shape_error();
    }
    k += 1;
  }
  check_shape(out);
  return out;
}

pub function (a: NdArray[T]) broadcast_to(shape: i32[]): NdArray[T] {
  check_shape(shape);
  let pad = shape.len() - a.shape.len();
  if (pad < 0) {
    shape_error();
  }
  let strides = zeros(shape.len());
  let k = 0;
  while (k < a.shape.len()) {
    if (a.shape[k] == shape[pad + k]) {
      strides = strides.with(pad + k, a.strides[k]);
    } else if (a.shape[k] != 1) {
      shape_error();
    }
    k += 1;
  }
  return NdArray { data: a.data, shape: shape, strides: strides, offset: a.offset };
}

pub function (a: NdArray[T]) zip_with[U, V](b: NdArray[U], f: (T, U) => V): NdArray[V] {
  let shape = broadcast_shape(a.shape, b.shape);
  let x = a;
  let y = b;
  if (!same_shape(a.shape, shape)) {
    x = a.broadcast_to(shape);
  }
  if (!same_shape(b.shape, shape)) {
    y = b.broadcast_to(shape);
  }
  if (x.is_packed() && y.is_packed()) {
    return from_flat(packed_zip(x.data, y.data, f), shape);
  }
  let out: V[] = [];
  let n = count_of(shape);
  let idx = zeros(shape.len());
  let i = 0;
  while (i < n) {
    out = out.append(f(x.data[addr_of(x, idx)], y.data[addr_of(y, idx)]));
    idx = bump(idx, shape);
    i += 1;
  }
  return from_flat(out, shape);
}
```

### `13 function packed_zip[T, U, V](x: T[], y: U[], f: (T, U)`

```fern
<<13 function packed_zip[T, U, V](x: T[], y: U[], f: (T, U)>>=
function packed_zip[T, U, V](x: T[], y: U[], f: (T, U) => V): V[] {
  let out: V[] = [];
  let n = x.len();
  let i = 0;
  while (i < n) {
    out = out.append(f(x[i], y[i]));
    i += 1;
  }
  return out;
}
```

### `14 pub function (a: NdArray[T]) outer[U, V](b: NdArray[U], f: (T, U)`

```fern
<<14 pub function (a: NdArray[T]) outer[U, V](b: NdArray[U], f: (T, U)>>=
pub function (a: NdArray[T]) outer[U, V](b: NdArray[U], f: (T, U) => V): NdArray[V] {
  let shape = join(a.shape, b.shape);
  check_shape(shape);
  if (a.is_packed() && b.is_packed()) {
    return packed_outer(a.data, b.data, shape, f);
  }
  let ra = a.shape.len();
  let rb = b.shape.len();
  let x = NdArray { data: a.data, shape: join(a.shape, ones(rb)), strides: join(a.strides, zeros(rb)), offset: a.offset };
  let y = NdArray { data: b.data, shape: join(ones(ra), b.shape), strides: join(zeros(ra), b.strides), offset: b.offset };
  return x.zip_with(y, f);
}
```

### `15 function packed_outer[T, U, V](a: T[], b: U[], shape: i32[], f: (T, U)`

```fern
<<15 function packed_outer[T, U, V](a: T[], b: U[], shape: i32[], f: (T, U)>>=
function packed_outer[T, U, V](a: T[], b: U[], shape: i32[], f: (T, U) => V): NdArray[V] {
  let out: V[] = [];
  if (b.len() == 0) {
    return from_flat(out, shape);
  }
  let i = 0;
  while (i < a.len()) {
    let j = 0;
    while (j < b.len()) {
      out = out.append(f(a[i], b[j]));
      j += 1;
    }
    i += 1;
  }
  return from_flat(out, shape);
}

function join(x: i32[], y: i32[]): i32[] {
  let out: i32[] = [];
  let k = 0;
  while (k < x.len()) {
    out = out.append(x[k]);
    k += 1;
  }
  k = 0;
  while (k < y.len()) {
    out = out.append(y[k]);
    k += 1;
  }
  return out;
}

function ones(rank: i32): i32[] {
  let out: i32[] = [];
  let k = 0;
  while (k < rank) {
    out = out.append(1);
    k += 1;
  }
  return out;
}

function base_of[T](a: NdArray[T], idx: i32[], idx_from: i32, axis_from: i32, count: i32): i32 {
  let at = a.offset;
  let k = 0;
  while (k < count) {
    at = at + idx[idx_from + k] * a.strides[axis_from + k];
    k += 1;
  }
  return at;
}
```

### `16 pub function (a: NdArray[T]) inner[A](b: NdArray[T], init: A, mul: (T, T)`

```fern
<<16 pub function (a: NdArray[T]) inner[A](b: NdArray[T], init: A, mul: (T, T)>>=
pub function (a: NdArray[T]) inner[A](b: NdArray[T], init: A, mul: (T, T) => A, add: (A, A) => A): NdArray[A] {
  let ra = a.shape.len();
  let rb = b.shape.len();
  let shape = inner_shape(a.shape, b.shape);
  let n = a.shape[ra - 1];
  let sa = a.strides[ra - 1];
  let sb = b.strides[0];
  let out: A[] = [];
  let total = count_of(shape);
  if (total == 0) {
    return from_flat(out, shape);
  }
  if (a.is_packed() && b.is_packed()) {
    return from_flat(packed_inner(a.data, b.data, extent_before(a.shape, ra - 1), n, extent_after(b.shape, 0), init, mul, add), shape);
  }
  let idx = zeros(shape.len());
  let i = 0;
  while (i < total) {
    let pa = base_of(a, idx, 0, 0, ra - 1);
    let pb = base_of(b, idx, ra - 1, 1, rb - 1);
    let acc = init;
    let k = 0;
    while (k < n) {
      acc = add(acc, mul(a.data[pa + k * sa], b.data[pb + k * sb]));
      k += 1;
    }
    out = out.append(acc);
    idx = bump(idx, shape);
    i += 1;
  }
  return from_flat(out, shape);
}
```

### `17 function inner_shape(a: i32[], b: i32[]): i32[]`

```fern
<<17 function inner_shape(a: i32[], b: i32[]): i32[]>>=
function inner_shape(a: i32[], b: i32[]): i32[] {
  if (a.len() < 1 || b.len() < 1 || a[a.len() - 1] != b[0]) {
    shape_error();
  }
  return join(drop_axis(a, a.len() - 1), drop_axis(b, 0));
}

function inner_kernel(a: NdArray[f64], b: NdArray[f64], init: f64): NdArray[f64] {
  let shape = inner_shape(a.shape, b.shape);
  let total = count_of(shape);
  if (total == 0) {
    return from_flat([], shape);
  }
  let last = a.shape.len() - 1;
  let data = __inner_mul_add_f64(a.data, b.data, extent_before(a.shape, last), a.shape[last], extent_after(b.shape, 0), init);
  return from_flat(data, shape);
}

function packed_inner[T, A](xs: T[], ys: T[], rows: i32, extent: i32, columns: i32, init: A, mul: (T, T) => A, add: (A, A) => A): A[] {
  let out: A[] = [];
  let i = 0;
  while (i < rows) {
    let j = 0;
    while (j < columns) {
      let acc: A = init;
      let k = 0;
      while (k < extent) {
        acc = add(acc, mul(xs[i * extent + k], ys[k * columns + j]));
        k += 1;
      }
      out = out.append(acc);
      j += 1;
    }
    i += 1;
  }
  return out;
}
```

### `18 pub function (a: NdArray[T]) fold_all[A](init: A, f: (A, T)`

```fern
<<18 pub function (a: NdArray[T]) fold_all[A](init: A, f: (A, T)>>=
pub function (a: NdArray[T]) fold_all[A](init: A, f: (A, T) => A): A {
  if (a.is_packed()) {
    return packed_fold(a.data, init, f);
  }
  let acc = init;
  let n = a.len();
  let idx = zeros(a.shape.len());
  let i = 0;
  while (i < n) {
    acc = f(acc, a.data[addr_of(a, idx)]);
    idx = bump(idx, a.shape);
    i += 1;
  }
  return acc;
}
```

### `19 function packed_fold[T, A](data: T[], init: A, f: (A, T)`

```fern
<<19 function packed_fold[T, A](data: T[], init: A, f: (A, T)>>=
function packed_fold[T, A](data: T[], init: A, f: (A, T) => A): A {
  let acc: A = init;
  let n = data.len();
  let i = 0;
  while (i < n) {
    acc = f(acc, data[i]);
    i += 1;
  }
  return acc;
}

function drop_axis(shape: i32[], axis: i32): i32[] {
  let out: i32[] = [];
  let k = 0;
  while (k < shape.len()) {
    if (k != axis) {
      out = out.append(shape[k]);
    }
    k += 1;
  }
  return out;
}

function lane_of(idx: i32[], axis: i32, lane_strides: i32[]): i32 {
  let lane = 0;
  let k = 0;
  let j = 0;
  while (k < idx.len()) {
    if (k != axis) {
      lane = lane + idx[k] * lane_strides[j];
      j += 1;
    }
    k += 1;
  }
  return lane;
}

function lanes[A](count: i32, init: A): A[] {
  let accs: A[] = [];
  let i = 0;
  while (i < count) {
    accs = accs.append(init);
    i += 1;
  }
  return accs;
}

function extent_after(shape: i32[], axis: i32): i32 {
  let s = 1;
  let k = axis + 1;
  while (k < shape.len()) {
    s *= shape[k];
    k += 1;
  }
  return s;
}

function extent_before(shape: i32[], axis: i32): i32 {
  let s = 1;
  let k = 0;
  while (k < axis) {
    s *= shape[k];
    k += 1;
  }
  return s;
}

function packed_reduce[T, A](data: T[], outer: i32, extent: i32, inner: i32, init: A, f: (A, T) => A): A[] {
  let accs = lanes(outer * inner, init);
  let i = 0;
  let h = 0;
  while (h < outer) {
    let base = h * inner;
    let t = 0;
    while (t < extent) {
      let l = 0;
      while (l < inner) {
        let lane = base + l;
        accs = accs.with(lane, f(accs[lane], data[i]));
        i += 1;
        l += 1;
      }
      t += 1;
    }
    h += 1;
  }
  return accs;
}

function packed_scan[T, A](data: T[], outer: i32, extent: i32, inner: i32, init: A, f: (A, T) => A): A[] {
  let accs = lanes(outer * inner, init);
  let out: A[] = [];
  let i = 0;
  let h = 0;
  while (h < outer) {
    let base = h * inner;
    let t = 0;
    while (t < extent) {
      let l = 0;
      while (l < inner) {
        let lane = base + l;
        let acc = f(accs[lane], data[i]);
        accs = accs.with(lane, acc);
        out = out.append(acc);
        i += 1;
        l += 1;
      }
      t += 1;
    }
    h += 1;
  }
  return out;
}
```

### `20 pub function (a: NdArray[T]) reduce_axis[A](axis: i32, init: A, f: (A, T)`

```fern
<<20 pub function (a: NdArray[T]) reduce_axis[A](axis: i32, init: A, f: (A, T)>>=
pub function (a: NdArray[T]) reduce_axis[A](axis: i32, init: A, f: (A, T) => A): NdArray[A] {
  check_axis(a, axis);
  let rshape = drop_axis(a.shape, axis);
  let lane_strides = row_major(rshape);
  if (a.is_packed()) {
    return from_flat(
      packed_reduce(
        a.data, extent_before(a.shape, axis), a.shape[axis],
        extent_after(a.shape, axis), init, f,
      ),
      rshape,
    );
  }
  let accs = lanes(count_of(rshape), init);
  let n = a.len();
  let idx = zeros(a.shape.len());
  let i = 0;
  while (i < n) {
    let lane = lane_of(idx, axis, lane_strides);
    accs = accs.with(lane, f(accs[lane], a.data[addr_of(a, idx)]));
    idx = bump(idx, a.shape);
    i += 1;
  }
  return from_flat(accs, rshape);
}

pub function (a: NdArray[T]) scan_axis[A](axis: i32, init: A, f: (A, T) => A): NdArray[A] {
  check_axis(a, axis);
  let rshape = drop_axis(a.shape, axis);
  let lane_strides = row_major(rshape);
  if (a.is_packed()) {
    return from_flat(
      packed_scan(
        a.data, extent_before(a.shape, axis), a.shape[axis],
        extent_after(a.shape, axis), init, f,
      ),
      a.shape,
    );
  }
  let accs = lanes(count_of(rshape), init);
  let out: A[] = [];
  let n = a.len();
  let idx = zeros(a.shape.len());
  let i = 0;
  while (i < n) {
    let lane = lane_of(idx, axis, lane_strides);
    let acc = f(accs[lane], a.data[addr_of(a, idx)]);
    accs = accs.with(lane, acc);
    out = out.append(acc);
    idx = bump(idx, a.shape);
    i += 1;
  }
  return from_flat(out, a.shape);
}

pub function (a: NdArray[T]) map_rank[U](k: i32, f: (NdArray[T]) => NdArray[U]): NdArray[U] {
  if (k < 0 || k > a.shape.len()) {
    shape_error();
  }
  let fr = a.shape.len() - k;
  let frame: i32[] = [];
  let j = 0;
  while (j < fr) {
    frame = frame.append(a.shape[j]);
    j += 1;
  }
  let cells = count_of(frame);
  let out: U[] = [];
  let cshape: i32[] = [];
  let idx = zeros(fr);
  let packed = a.is_packed();
  let cshape_in: i32[] = [];
  let cstrides: i32[] = [];
  let q = fr;
  while (q < a.shape.len()) {
    cshape_in = cshape_in.append(a.shape[q]);
    cstrides = cstrides.append(a.strides[q]);
    q += 1;
  }
  let csize = count_of(cshape_in);
  let i = 0;
  while (i < cells) {
    let cell = a;
    if (packed) {
      cell = NdArray { data: a.data, shape: cshape_in, strides: cstrides, offset: i * csize };
    } else {
      let d = 0;
      while (d < fr) {
        cell = cell.select(0, idx[d]);
        d += 1;
      }
      idx = bump(idx, frame);
    }
    let r = f(cell);
    if (i == 0) {
      cshape = r.shape();
    } else {
      if (!same_shape(r.shape(), cshape)) {
        shape_error();
      }
    }
    let flat = r.to_flat();
    let m = 0;
    while (m < flat.len()) {
      out = out.append(flat[m]);
      m += 1;
    }
    i += 1;
  }
  return from_flat(out, join(frame, cshape));
}
```

### `21 function copy_flat[T](a: NdArray[T]): T[]`

```fern
<<21 function copy_flat[T](a: NdArray[T]): T[]>>=
function copy_flat[T](a: NdArray[T]): T[] {
  let out: T[] = [];
  let n = a.len();
  let idx = zeros(a.shape.len());
  let i = 0;
  while (i < n) {
    out = out.append(a.data[addr_of(a, idx)]);
    idx = bump(idx, a.shape);
    i += 1;
  }
  return out;
}

function bump(own idx: i32[], shape: i32[]): i32[] {
  let k = shape.len() - 1;
  while (k >= 0) {
    if (idx[k] + 1 < shape[k]) {
      return idx.with(k, idx[k] + 1);
    }
    idx = idx.with(k, 0);
    k -= 1;
  }
  return idx;
}
```

### `22 cmp impls`

Two arrays are equal when they have the same shape and the same elements in row-major order. The strides and the offset of a view do not take part.

```fern
<<22 cmp impls>>=
impl[T: cmp.Eq] cmp.Eq for NdArray[T] {
  function eq(self: NdArray[T], other: NdArray[T]): boolean {
    return self.shape == other.shape && self.to_flat() == other.to_flat();
  }
}

impl[T: cmp.Hash] cmp.Hash for NdArray[T] {
  function hash(self: NdArray[T]): i32 {
    let h = 17;
    let k = 0;
    while (k < self.shape.len()) {
      h = h * 31 + self.shape[k];
      k += 1;
    }
    return self.fold_all(h, (acc: i32, x: T) => acc * 31 + x.hash());
  }
}

// The text nests one bracket pair for each axis, so a 2 by 2 array gives [[1, 2], [3, 4]].
impl[T: cmp.Display] cmp.Display for NdArray[T] {
  function to_string(self: NdArray[T]): string {
    return display_axis(self.to_flat(), self.shape, 0, 0);
  }
}

function display_axis[T: cmp.Display](xs: T[], shape: i32[], axis: i32, at: i32): string {
  if (axis == shape.len()) {
    return xs[at].to_string();
  }
  let step = 1;
  let k = axis + 1;
  while (k < shape.len()) {
    step *= shape[k];
    k += 1;
  }
  let out = "[";
  let i = 0;
  while (i < shape[axis]) {
    if (i > 0) {
      out += ", ";
    }
    out += display_axis(xs, shape, axis + 1, at + i * step);
    i += 1;
  }
  return out + "]";
}
```
