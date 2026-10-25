# Arithmetic trait contracts

This module defines arithmetic traits for numeric types.

## Model

Small traits describe addition, subtraction, multiplication, division, negation, zero, and one. Composite traits combine these requirements for generic numeric code.

## Contracts

A trait bound selects the operations available to a generic function. It does not change the overflow or precision rules of the concrete number type.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub trait Add>>

<<3 impl Div for f64>>

<<4 impl Div for f32x4>>

<<5 pub function sum_with[T: Add](xs: T[], zero: T): T>>
```
### `module introduction`

```fern
<<module introduction>>=
// This module defines arithmetic traits for numeric types.
```
### `2 pub trait Add`

```fern
<<2 pub trait Add>>=
pub trait Add {
  function add(self: Self, o: Self): Self;
}

pub trait Sub {
  function sub(self: Self, o: Self): Self;
}

pub trait Mul {
  function mul(self: Self, o: Self): Self;
}

pub trait Div {
  function div(self: Self, o: Self): Self;
}

pub trait Neg {
  function neg(self: Self): Self;
}

pub trait Zero {
  function zero(): Self;
}

pub trait One {
  function one(): Self;
}

pub trait Num: Add + Sub + Mul + Div {}

impl Add for i32 {
  function add(self: i32, o: i32): i32 {
    return self + o;
  }
}

impl Sub for i32 {
  function sub(self: i32, o: i32): i32 {
    return self - o;
  }
}

impl Mul for i32 {
  function mul(self: i32, o: i32): i32 {
    return self * o;
  }
}

impl Div for i32 {
  function div(self: i32, o: i32): i32 {
    return self / o;
  }
}

impl Neg for i32 {
  function neg(self: i32): i32 {
    return 0 - self;
  }
}

impl Zero for i32 {
  function zero(): i32 {
    return 0;
  }
}

impl One for i32 {
  function one(): i32 {
    return 1;
  }
}

impl Num for i32 {}

impl Add for i64 {
  function add(self: i64, o: i64): i64 {
    return self + o;
  }
}

impl Sub for i64 {
  function sub(self: i64, o: i64): i64 {
    return self - o;
  }
}

impl Mul for i64 {
  function mul(self: i64, o: i64): i64 {
    return self * o;
  }
}

impl Div for i64 {
  function div(self: i64, o: i64): i64 {
    return self / o;
  }
}

impl Neg for i64 {
  function neg(self: i64): i64 {
    return 0 - self;
  }
}

impl Zero for i64 {
  function zero(): i64 {
    return 0;
  }
}

impl One for i64 {
  function one(): i64 {
    return 1;
  }
}

impl Num for i64 {}

impl Add for f64 {
  function add(self: f64, o: f64): f64 {
    return self + o;
  }
}

impl Sub for f64 {
  function sub(self: f64, o: f64): f64 {
    return self - o;
  }
}

impl Mul for f64 {
  function mul(self: f64, o: f64): f64 {
    return self * o;
  }
}
```
### `3 impl Div for f64`

```fern
<<3 impl Div for f64>>=
impl Div for f64 {
  function div(self: f64, o: f64): f64 {
    return self / o;
  }
}

impl Neg for f64 {
  function neg(self: f64): f64 {
    return 0.0 - self;
  }
}

impl Zero for f64 {
  function zero(): f64 {
    return 0.0;
  }
}

impl One for f64 {
  function one(): f64 {
    return 1.0;
  }
}

impl Num for f64 {}

impl Add for f32 {
  function add(self: f32, o: f32): f32 {
    return self + o;
  }
}

impl Sub for f32 {
  function sub(self: f32, o: f32): f32 {
    return self - o;
  }
}

impl Mul for f32 {
  function mul(self: f32, o: f32): f32 {
    return self * o;
  }
}

impl Div for f32 {
  function div(self: f32, o: f32): f32 {
    return self / o;
  }
}

impl Neg for f32 {
  function neg(self: f32): f32 {
    return 0.0 - self;
  }
}

impl Zero for f32 {
  function zero(): f32 {
    return 0.0;
  }
}

impl One for f32 {
  function one(): f32 {
    return 1.0;
  }
}

impl Num for f32 {}

impl Add for f64x2 {
  function add(self: f64x2, o: f64x2): f64x2 {
    return self + o;
  }
}

impl Sub for f64x2 {
  function sub(self: f64x2, o: f64x2): f64x2 {
    return self - o;
  }
}

impl Mul for f64x2 {
  function mul(self: f64x2, o: f64x2): f64x2 {
    return self * o;
  }
}

impl Div for f64x2 {
  function div(self: f64x2, o: f64x2): f64x2 {
    return self / o;
  }
}

impl Neg for f64x2 {
  function neg(self: f64x2): f64x2 {
    return -self;
  }
}

impl Zero for f64x2 {
  function zero(): f64x2 {
    return __f64x2_splat(0.0);
  }
}

impl One for f64x2 {
  function one(): f64x2 {
    return __f64x2_splat(1.0);
  }
}

impl Num for f64x2 {}

impl Add for f32x4 {
  function add(self: f32x4, o: f32x4): f32x4 {
    return self + o;
  }
}

impl Sub for f32x4 {
  function sub(self: f32x4, o: f32x4): f32x4 {
    return self - o;
  }
}

impl Mul for f32x4 {
  function mul(self: f32x4, o: f32x4): f32x4 {
    return self * o;
  }
}
```
### `4 impl Div for f32x4`

```fern
<<4 impl Div for f32x4>>=
impl Div for f32x4 {
  function div(self: f32x4, o: f32x4): f32x4 {
    return self / o;
  }
}

impl Neg for f32x4 {
  function neg(self: f32x4): f32x4 {
    return -self;
  }
}

impl Zero for f32x4 {
  function zero(): f32x4 {
    return __f32x4_splat(0.0);
  }
}

impl One for f32x4 {
  function one(): f32x4 {
    return __f32x4_splat(1.0);
  }
}

impl Num for f32x4 {}

impl Add for u32 {
  function add(self: u32, o: u32): u32 {
    return self + o;
  }
}

impl Sub for u32 {
  function sub(self: u32, o: u32): u32 {
    return self - o;
  }
}

impl Mul for u32 {
  function mul(self: u32, o: u32): u32 {
    return self * o;
  }
}

impl Div for u32 {
  function div(self: u32, o: u32): u32 {
    return self / o;
  }
}

impl Zero for u32 {
  function zero(): u32 {
    return 0;
  }
}

impl One for u32 {
  function one(): u32 {
    return 1;
  }
}

impl Num for u32 {}

impl Add for u64 {
  function add(self: u64, o: u64): u64 {
    return self + o;
  }
}

impl Sub for u64 {
  function sub(self: u64, o: u64): u64 {
    return self - o;
  }
}

impl Mul for u64 {
  function mul(self: u64, o: u64): u64 {
    return self * o;
  }
}

impl Div for u64 {
  function div(self: u64, o: u64): u64 {
    return self / o;
  }
}

impl Zero for u64 {
  function zero(): u64 {
    return 0;
  }
}

impl One for u64 {
  function one(): u64 {
    return 1;
  }
}

impl Num for u64 {}
```
### `5 pub function sum_with[T: Add](xs: T[], zero: T): T`

```fern
<<5 pub function sum_with[T: Add](xs: T[], zero: T): T>>=
pub function sum_with[T: Add](xs: T[], zero: T): T {
  let acc = zero;
  for x in xs {
    acc = acc.add(x);
  }
  return acc;
}

pub function product_with[T: Mul](xs: T[], one: T): T {
  let acc = one;
  for x in xs {
    acc = acc.mul(x);
  }
  return acc;
}

pub function sum[T: Add + Zero](xs: T[]): T {
  let acc: T = T.zero();
  for x in xs {
    acc = acc.add(x);
  }
  return acc;
}

pub function product[T: Mul + One](xs: T[]): T {
  let acc: T = T.one();
  for x in xs {
    acc = acc.mul(x);
  }
  return acc;
}

pub function cumsum[T: Add + Zero](xs: T[]): T[] {
  let out: T[] = [];
  let run: T = T.zero();
  for x in xs {
    run = run.add(x);
    out = out.append(run);
  }
  return out;
}

pub function cumproduct[T: Mul + One](xs: T[]): T[] {
  let out: T[] = [];
  let run: T = T.one();
  for x in xs {
    run = run.mul(x);
    out = out.append(run);
  }
  return out;
}

pub function adjacent_diff[T: Sub](xs: T[]): T[] {
  let out: T[] = [];
  let i: i32 = 1;
  while (i < xs.len()) {
    out = out.append(xs[i].sub(xs[i - 1]));
    i = i + 1;
  }
  return out;
}

// ---- u8 ----
impl Add for u8 {
  function add(self: u8, o: u8): u8 {
    return self + o;
  }
}

impl Sub for u8 {
  function sub(self: u8, o: u8): u8 {
    return self - o;
  }
}

impl Mul for u8 {
  function mul(self: u8, o: u8): u8 {
    return self * o;
  }
}

impl Div for u8 {
  function div(self: u8, o: u8): u8 {
    return self / o;
  }
}

impl Zero for u8 {
  function zero(): u8 {
    return 0 as u8;
  }
}

impl One for u8 {
  function one(): u8 {
    return 1 as u8;
  }
}

impl Num for u8 {}

// ---- usize ----
impl Add for usize {
  function add(self: usize, o: usize): usize {
    return self + o;
  }
}

impl Sub for usize {
  function sub(self: usize, o: usize): usize {
    return self - o;
  }
}

impl Mul for usize {
  function mul(self: usize, o: usize): usize {
    return self * o;
  }
}

impl Div for usize {
  function div(self: usize, o: usize): usize {
    return self / o;
  }
}

impl Zero for usize {
  function zero(): usize {
    return 0 as usize;
  }
}

impl One for usize {
  function one(): usize {
    return 1 as usize;
  }
}

impl Num for usize {}
```