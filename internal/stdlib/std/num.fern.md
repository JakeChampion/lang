# Numeric reductions

This module provides generic reductions over arrays of numbers.

## Model

Each function takes the operator traits of `core/ops` as bounds. `sum` and `product` start from `Zero` and `One`, and `sum_with` and `product_with` take an explicit start value.

The `core/ops.Gcd` trait gives `gcd` and `lcm` to i32, i64, u32 and u64. The integer modules import `core/ops`, so `import "std/i32"` also makes `n.gcd(m)` available. `xs.gcd_all()` and `xs.lcm_all()` in `std/array` use this trait for each width.

## Contracts

A reduction does not change the overflow or precision rules of the element type. An integer sum wraps as the element type wraps. `avg` and `variance` return `None` for an empty array.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<reductions>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides generic reductions over arrays of numbers.
import "core/ops";
```

### `reductions`

```fern
<<reductions>>=
pub function sum_with[T: ops.Add](xs: T[], zero: T): T {
  let acc = zero;
  for x in xs {
    acc = acc.add(x);
  }
  return acc;
}

pub function product_with[T: ops.Mul](xs: T[], one: T): T {
  let acc = one;
  for x in xs {
    acc = acc.mul(x);
  }
  return acc;
}

pub function sum[T: ops.Add + ops.Zero](xs: T[]): T {
  let acc: T = T.zero();
  for x in xs {
    acc = acc.add(x);
  }
  return acc;
}

pub function product[T: ops.Mul + ops.One](xs: T[]): T {
  let acc: T = T.one();
  for x in xs {
    acc = acc.mul(x);
  }
  return acc;
}

pub function cumsum[T: ops.Add + ops.Zero](xs: T[]): T[] {
  let out: T[] = [];
  let run: T = T.zero();
  for x in xs {
    run = run.add(x);
    out = out.append(run);
  }
  return out;
}

pub function cumproduct[T: ops.Mul + ops.One](xs: T[]): T[] {
  let out: T[] = [];
  let run: T = T.one();
  for x in xs {
    run = run.mul(x);
    out = out.append(run);
  }
  return out;
}

pub function adjacent_diff[T: ops.Sub](xs: T[]): T[] {
  let out: T[] = [];
  let i: i32 = 1;
  while (i < xs.len()) {
    out = out.append(xs[i].sub(xs[i - 1]));
    i = i + 1;
  }
  return out;
}

// The element count as a T, built from One so no integer conversion is needed.
function __count[T: ops.Add + ops.Zero + ops.One](xs: T[]): T {
  let n: T = T.zero();
  for x in xs {
    n = n.add(T.one());
  }
  return n;
}

// The mean, summed in T as sum does, so an integer sum wraps; None when empty.
pub function avg[T: ops.Add + ops.Div + ops.Zero + ops.One](xs: T[]): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  return Some(sum(xs).div(__count(xs)));
}

// The population variance; None when empty.
pub function variance[T: ops.Num + ops.Zero + ops.One](xs: T[]): Option[T] {
  if (xs.len() == 0) {
    return None;
  }
  let n: T = __count(xs);
  let mean: T = sum(xs).div(n);
  let acc: T = T.zero();
  for x in xs {
    let d: T = x.sub(mean);
    acc = acc.add(d.mul(d));
  }
  return Some(acc.div(n));
}

// The dot product over the shorter of the two arrays.
pub function dot[T: ops.Add + ops.Mul + ops.Zero](a: T[], b: T[]): T {
  let n: i32 = a.len();
  if (b.len() < n) {
    n = b.len();
  }
  let acc: T = T.zero();
  let i: i32 = 0;
  while (i < n) {
    acc = acc.add(a[i].mul(b[i]));
    i = i + 1;
  }
  return acc;
}
```
