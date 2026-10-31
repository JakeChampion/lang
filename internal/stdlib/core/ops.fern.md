# Operator traits

This module defines the traits behind the arithmetic and bit operators.

## Model

Each binary operator has one trait with one method of the same name. `+`, `-`, `*`, `/` and `%` use `Add`, `Sub`, `Mul`, `Div` and `Rem`. `&`, `|`, `^`, `<<` and `>>` use `BitAnd`, `BitOr`, `BitXor`, `Shl` and `Shr`. Unary `-` uses `Neg`. `Zero` and `One` supply the identity values, and `Num` combines the four arithmetic traits.

## Contracts

A trait bound selects the operators available to a generic function. It does not change the overflow or precision rules of the concrete number type. The integer types implement every trait. The float and vector types implement the arithmetic traits and `Neg`, but not `Rem` or the bit traits.

## Executable example

```fern test name=core-ops-example
import "core/ops";

struct Mod7 { v: i32 }

impl ops.Add for Mod7 {
  function add(self: Mod7, o: Mod7): Mod7 { return Mod7 { v: (self.v + o.v) % 7 }; }
}

impl ops.Rem for Mod7 {
  function rem(self: Mod7, o: Mod7): Mod7 { return Mod7 { v: self.v % o.v }; }
}

function wrap[T: ops.Rem](a: T, m: T): T {
  return a % m;
}

function main(): i32 {
  let s: Mod7 = Mod7 { v: 5 } + Mod7 { v: 4 };
  if (s.v != 2) { return 1; }
  if ((Mod7 { v: 6 } % Mod7 { v: 4 }).v != 2) { return 2; }
  if (wrap(17, 5) != 2 || (12 & 10) != 8) { return 3; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<operator traits>>

<<impls for i32>>

<<impls for i64>>

<<impls for f64>>

<<impls for f32>>

<<impls for f64x2>>

<<impls for f32x4>>

<<impls for u32>>

<<impls for u64>>

<<impls for u8>>

<<impls for usize>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines the traits behind the arithmetic and bit operators.
```

### `operator traits`

```fern
<<operator traits>>=
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

pub trait Rem {
  function rem(self: Self, o: Self): Self;
}

pub trait Neg {
  function neg(self: Self): Self;
}

pub trait BitAnd {
  function bitand(self: Self, o: Self): Self;
}

pub trait BitOr {
  function bitor(self: Self, o: Self): Self;
}

pub trait BitXor {
  function bitxor(self: Self, o: Self): Self;
}

pub trait Shl {
  function shl(self: Self, o: Self): Self;
}

pub trait Shr {
  function shr(self: Self, o: Self): Self;
}

pub trait Zero {
  function zero(): Self;
}

pub trait One {
  function one(): Self;
}

// The greatest common divisor and least common multiple of two integers.
pub trait Gcd {
  function gcd(self: Self, o: Self): Self;
  function lcm(self: Self, o: Self): Self;
}

pub trait Num: Add + Sub + Mul + Div {}
```

### `impls for i32`

```fern
<<impls for i32>>=
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

impl Rem for i32 {
  function rem(self: i32, o: i32): i32 {
    return self % o;
  }
}

impl BitAnd for i32 {
  function bitand(self: i32, o: i32): i32 {
    return self & o;
  }
}

impl BitOr for i32 {
  function bitor(self: i32, o: i32): i32 {
    return self | o;
  }
}

impl BitXor for i32 {
  function bitxor(self: i32, o: i32): i32 {
    return self ^ o;
  }
}

impl Shl for i32 {
  function shl(self: i32, o: i32): i32 {
    return self << o;
  }
}

impl Shr for i32 {
  function shr(self: i32, o: i32): i32 {
    return self >> o;
  }
}

impl Num for i32 {}
```

### `impls for i64`

```fern
<<impls for i64>>=
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

impl Rem for i64 {
  function rem(self: i64, o: i64): i64 {
    return self % o;
  }
}

impl BitAnd for i64 {
  function bitand(self: i64, o: i64): i64 {
    return self & o;
  }
}

impl BitOr for i64 {
  function bitor(self: i64, o: i64): i64 {
    return self | o;
  }
}

impl BitXor for i64 {
  function bitxor(self: i64, o: i64): i64 {
    return self ^ o;
  }
}

impl Shl for i64 {
  function shl(self: i64, o: i64): i64 {
    return self << o;
  }
}

impl Shr for i64 {
  function shr(self: i64, o: i64): i64 {
    return self >> o;
  }
}

impl Num for i64 {}
```

### `impls for f64`

```fern
<<impls for f64>>=
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

impl Div for f64 {
  function div(self: f64, o: f64): f64 {
    return self / o;
  }
}

impl Neg for f64 {
  function neg(self: f64): f64 {
    return -self;
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
```

### `impls for f32`

```fern
<<impls for f32>>=
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
    return -self;
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
```

### `impls for f64x2`

```fern
<<impls for f64x2>>=
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
```

### `impls for f32x4`

```fern
<<impls for f32x4>>=
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
```

### `impls for u32`

```fern
<<impls for u32>>=
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

impl Rem for u32 {
  function rem(self: u32, o: u32): u32 {
    return self % o;
  }
}

impl BitAnd for u32 {
  function bitand(self: u32, o: u32): u32 {
    return self & o;
  }
}

impl BitOr for u32 {
  function bitor(self: u32, o: u32): u32 {
    return self | o;
  }
}

impl BitXor for u32 {
  function bitxor(self: u32, o: u32): u32 {
    return self ^ o;
  }
}

impl Shl for u32 {
  function shl(self: u32, o: u32): u32 {
    return self << o;
  }
}

impl Shr for u32 {
  function shr(self: u32, o: u32): u32 {
    return self >> o;
  }
}

impl Num for u32 {}
```

### `impls for u64`

```fern
<<impls for u64>>=
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

impl Rem for u64 {
  function rem(self: u64, o: u64): u64 {
    return self % o;
  }
}

impl BitAnd for u64 {
  function bitand(self: u64, o: u64): u64 {
    return self & o;
  }
}

impl BitOr for u64 {
  function bitor(self: u64, o: u64): u64 {
    return self | o;
  }
}

impl BitXor for u64 {
  function bitxor(self: u64, o: u64): u64 {
    return self ^ o;
  }
}

impl Shl for u64 {
  function shl(self: u64, o: u64): u64 {
    return self << o;
  }
}

impl Shr for u64 {
  function shr(self: u64, o: u64): u64 {
    return self >> o;
  }
}

impl Num for u64 {}
```

### `impls for u8`

```fern
<<impls for u8>>=
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

impl Rem for u8 {
  function rem(self: u8, o: u8): u8 {
    return self % o;
  }
}

impl BitAnd for u8 {
  function bitand(self: u8, o: u8): u8 {
    return self & o;
  }
}

impl BitOr for u8 {
  function bitor(self: u8, o: u8): u8 {
    return self | o;
  }
}

impl BitXor for u8 {
  function bitxor(self: u8, o: u8): u8 {
    return self ^ o;
  }
}

impl Shl for u8 {
  function shl(self: u8, o: u8): u8 {
    return self << o;
  }
}

impl Shr for u8 {
  function shr(self: u8, o: u8): u8 {
    return self >> o;
  }
}

impl Num for u8 {}
```

### `impls for usize`

```fern
<<impls for usize>>=
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

impl Rem for usize {
  function rem(self: usize, o: usize): usize {
    return self % o;
  }
}

impl BitAnd for usize {
  function bitand(self: usize, o: usize): usize {
    return self & o;
  }
}

impl BitOr for usize {
  function bitor(self: usize, o: usize): usize {
    return self | o;
  }
}

impl BitXor for usize {
  function bitxor(self: usize, o: usize): usize {
    return self ^ o;
  }
}

impl Shl for usize {
  function shl(self: usize, o: usize): usize {
    return self << o;
  }
}

impl Shr for usize {
  function shr(self: usize, o: usize): usize {
    return self >> o;
  }
}

impl Num for usize {}

function __abs_i32(x: i32): i32 {
  if (x < 0) {
    return 0 - x;
  }
  return x;
}

function __abs_i64(x: i64): i64 {
  if (x < 0 as i64) {
    return 0 as i64 - x;
  }
  return x;
}

impl Gcd for i32 {
  function gcd(self: i32, other: i32): i32 {
    let a: i32 = __abs_i32(self);
    let b: i32 = __abs_i32(other);
    while (b != 0) {
      let t: i32 = b;
      b = a % b;
      a = t;
    }
    return a;
  }

  function lcm(self: i32, other: i32): i32 {
    if (self == 0 || other == 0) {
      return 0;
    }
    let g: i32 = self.gcd(other);
    return __abs_i32(self / g) * __abs_i32(other);
  }
}

impl Gcd for i64 {
  function gcd(self: i64, other: i64): i64 {
    let a: i64 = __abs_i64(self);
    let b: i64 = __abs_i64(other);
    while (b != 0 as i64) {
      let t: i64 = b;
      b = a % b;
      a = t;
    }
    return a;
  }

  function lcm(self: i64, other: i64): i64 {
    if (self == 0 as i64 || other == 0 as i64) {
      return 0 as i64;
    }
    let g: i64 = self.gcd(other);
    return __abs_i64(self / g) * __abs_i64(other);
  }
}

impl Gcd for u32 {
  function gcd(self: u32, other: u32): u32 {
    let a: u32 = self;
    let b: u32 = other;
    while (b != 0 as u32) {
      let t: u32 = b;
      b = a % b;
      a = t;
    }
    return a;
  }

  function lcm(self: u32, other: u32): u32 {
    if (self == 0 as u32 || other == 0 as u32) {
      return 0 as u32;
    }
    return self / self.gcd(other) * other;
  }
}

impl Gcd for u64 {
  function gcd(self: u64, other: u64): u64 {
    let a: u64 = self;
    let b: u64 = other;
    while (b != 0 as u64) {
      let t: u64 = b;
      b = a % b;
      a = t;
    }
    return a;
  }

  function lcm(self: u64, other: u64): u64 {
    if (self == 0 as u64 || other == 0 as u64) {
      return 0 as u64;
    }
    return self / self.gcd(other) * other;
  }
}
```
