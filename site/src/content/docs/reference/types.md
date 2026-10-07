---
title: Type system
description: Built-in types, integer arithmetic, strings, arrays, generics, unions, Cell, Option and Result.
sidebar:
  order: 2
---

## Built-in types

| Category   | Members                                              |
| ---------- | ---------------------------------------------------- |
| Integers   | `i32` `i64` `u8` `u32` `u64` `usize`                  |
| Floats     | `f32` `f64` (`float` is an alias for `f64`)          |
| Text       | `string` (owned UTF-8), `str` (a borrowed string slice), `char` (a Unicode scalar value) |
| Other      | `boolean` `void` (its value is `()`)                 |
| Composite  | `T[]` (array), `[T]` (slice), `(T, U, ...)` (tuple), `Map[K, V]`, `Cell[T]` |
| Function   | `(T1, T2) => R`                                      |
| Trait object | `dyn Trait` — see [Traits](../traits/#runtime-dispatch--dyn-trait) |

`usize` is target-aware: 4 bytes on wasm32, 8 on arm64 and x86-64.
Use it for "size of a thing in memory" semantics.

There is no `i8`, `i16`, `u16` or `isize`: `i32` and `u32` cover that
ground, and `u8` is the byte type.

## Integer arithmetic

Integer operations never trap, and they give the same result on every
target:

- `+` `-` `*` `<<` **wrap** at the operand's width (two's complement).
- `/` and `%` are defined for every operand: `x / 0` is `0`, `x % 0` is
  `x`, and `MIN / -1` is `MIN`.
- A shift count is masked to the width (`& 31` or `& 63`). `>>` is
  arithmetic on signed types and logical on unsigned ones.
- `as` between integer types truncates or extends; between a float and
  an integer it truncates toward zero.

When wrapping is not what you want, two operator families say so at the
operation:

| Family | Operators | Result on overflow |
| ------ | --------- | ------------------ |
| saturating | `+\|` `-\|` `*\|` `<<\|` | clamps to the type's `MIN` / `MAX` |
| checked | `+?` `-?` `*?` `/?` `%?` `<<?` `>>?` | `None`; otherwise `Some(result)` |

```fern
import "std/i32";

function main(): i32 {
    let big: i32 = 2147483647;
    print((big + 1).to_string());    // -2147483648: wraps
    print((big +| 1).to_string());   // 2147483647: saturates
    match (big +? 1) {
        Some(n) => { print(n.to_string()); },
        None    => { print("overflow"); },   // this arm runs
    }
    print((7 / 0).to_string());      // 0
    return 0;
}
```

`/?` and `%?` also answer `None` for a zero divisor and for `MIN / -1`,
and `<<?` / `>>?` for a shift count outside the width. Both families are
integer-only, and the saturating ones reject `usize` (`E009`) because its
bounds depend on the target. The full contract is
[`docs/INTEGER-SEMANTICS.md`](https://github.com/JakeChampion/lang/blob/main/docs/INTEGER-SEMANTICS.md).

## Strings, characters and bytes

A `string` is UTF-8 text, and its `len()` counts bytes. Indexing gives a
**byte**: `s[i]` is a `u8`, which compares with a byte literal (`b'x'`),
not a character literal. `for b in s` walks the bytes.

```fern
import "std/string";

function main(): i32 {
    let s: string = "héllo";
    print(s.len().to_string());           // 6: é is two bytes
    if (s[0] == b'h') { print("starts with h"); }
    match (s[0:3]) {
        Some(v) => { print(v.to_string()); },  // hé
        None    => { print("out of range"); },
    }
    match (s[0:2]) {
        Some(v) => { print(v.to_string()); },
        None    => { print("splits é"); },     // this arm runs
    }
    let cs: char[] = s.chars();
    print(cs.len().to_string());          // 5 characters
    return 0;
}
```

- `s[a:b]` is `Option[str]`: `None` when a bound is out of range or
  falls inside a multi-byte character. A `str` is a view into the
  original string, not a copy. `slice_unchecked(s, a, b)` is the
  byte-indexed form that gives a `str` directly and aborts instead.
- `char` is a Unicode scalar value. `s.chars()` (in `std/string`) splits
  a string into them, and `c as i32` gives the code point.
- Strings compare with `==` and order with `<` / `>` byte-wise; `+`
  concatenates.

## Arrays and slices

`T[]` is an array. Its elements are **read-only after construction**:
`xs[i] = v` is a compile error (`E056`). The update methods return a new
array, and you rebind the name to it:

```fern
import "std/i32";

function total(xs: [i32]): i32 {
    let t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}

function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4, 5];
    xs = xs.with(0, 10).append(6);    // [10, 2, 3, 4, 5, 6]
    let mid: [i32] = xs[1:4];         // a view of 2, 3, 4
    print(total(mid).to_string());    // 9
    print(total(xs).to_string());     // 30: an array passes as a slice
    return 0;
}
```

`[T]` is a slice: a borrowed window onto an array, made with `xs[a:b]`
(either bound may be left out). It copies nothing, and a function that
takes `[T]` accepts a `T[]` as well. Reading outside an array or slice
aborts the program. The old array is not changed by `with` or `append` as
far as any other holder can see, but when the binding is the only
reference the update happens in place, so the rebinding idiom costs no
copy.

## Structs, enums and tuples

Struct fields are read-only after construction too (`E048`). A
struct-update literal builds the changed copy:

```fern
struct Point { x: i32, y: i32 }

function main(): i32 {
    let p: Point = Point { x: 1, y: 2 };
    let q: Point = Point { ...p, x: 5 };   // y is copied from p
    return q.x + q.y - 7;
}
```

An enum variant carries nothing, positional payloads, or named fields:

```fern
import "std/float";

enum Shape {
    Circle { r: f64 },
    Rect { w: f64, h: f64 },
    Pair(f64, f64),
    Empty,
}

function area(s: Shape): f64 {
    return match (s) {
        Circle { r } => 3.14159 * r * r,
        Rect { w, h } => w * h,
        Pair(a, b) => a * b,
        Empty => 0.0,
    };
}

function main(): i32 {
    print(area(Rect(3.0, 4.0)).to_string());   // 12
    return 0;
}
```

A named-field variant is matched with `{ }`, binding every field by name,
in any order. It is constructed positionally, arguments in declaration
order: `Rect(3.0, 4.0)` sets `w` then `h`.

A tuple `(T, U)` reads its elements with `.0`, `.1`, … and destructures
with `let (a, b) = t;`. The empty tuple `()` is the unit value, below.

## The unit type

`void` is the type of "no interesting value", and `()` is its sole
value — the **unit value**. Write `()` when a generic needs a type
argument but there is nothing to carry:

```fern
function ensure_readable(path: string): Result[(), IoError] {
    read_file(path)?;   // the contents don't matter, only that it worked
    return Ok(());
}
```

`()` is also accepted as a type, so `Result[(), IoError]` and
`Result[void, IoError]` are the same type spelled two ways — much as
`float` is an alias for `f64`. `fern -fmt` normalises the type spelling
to `void`; the value is always `()`.

A void-returning *call* is not a value: `Ok(log_it())` is an error, not
a unit value (`fern -explain E072`). Call it as a statement, then return
`Ok(())`.

## Maps

`Map[K, V]` is built into the language, with a literal syntax — but its
operations live in `core/map`, so the module has to be imported:

```fern
import "core/map";
import "std/i32";

let stock: Map[string, i32] = Map { "frond": 3, "spore": 7 };
stock = stock.insert("rhizome", 1);

match (stock.get("frond")) {
    Some(n) => { print(n.to_string()); },
    None    => { print("(none)"); },
}
```

Keys are integers, strings, or a type that implements `cmp.Eq` and
`cmp.Hash` (`@derive(cmp.Eq, cmp.Hash)` is enough). Iteration order is
insertion order and is part of the contract, not an accident — see the
[`for (k, v) in m` form](../language-features/#iteration--for--in).
Forget the `core/map` import and the checker says so directly rather than
failing on an unknown method.

| Operation             | Returns              |
| --------------------- | -------------------- |
| `len()`               | `i32`                |
| `has(k)`              | `boolean`            |
| `get(k)`              | `Option[V]`          |
| `get_or(k, fallback)` | `V`                  |
| `keys()` / `values()` | `K[]` / `V[]`        |
| `insert(k, v)`        | the updated map      |
| `without(k)`          | `(Map[K, V], boolean)` — the map, and whether the key was there |
| `cleared()`           | an empty map         |

The last three return the updated map rather than mutating in place *as
an expression*, but they may reuse the original's storage when it is
uniquely referenced. Rebind the result — `stock = stock.insert(…)` — and
treat the old binding as spent instead of expecting two independent maps.

## Implicit conversions

There aren't any between numeric widths. Casts are explicit:

```fern
let a: i32 = 7;
let b: i64 = a as i64;
```

The one exception is the polymorphic numeric literal: `1` types as
whatever integer the context demands.

## Generics

Functions and structs/enums take type parameters in `[...]`:

```fern
function id[T](x: T): T {
    return x;
}

struct Pair[A, B] { first: A, second: B }
enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }

function (p: Pair[A, B]) swap[A, B](): Pair[B, A] {
    return Pair { first: p.second, second: p.first };
}
```

Generic calls infer `T` from the argument types when possible; write the
type arguments at the call when they cannot be inferred or you want to
say them: `id[i32](7)`, `id[Point](p)`. The compiler monomorphises every
distinct instantiation before codegen, so there's no runtime cost.

A type parameter can carry a **trait bound** (`[T: Display]`) to
constrain it to types implementing a trait — see [Traits](../traits/).

## Union types

A union is a closed sum over struct types:

```fern
struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
type Expr = Add | Mul;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
    }
}
```

A struct value converts to the union implicitly (`eval(Add { l: 1, r: 2 })`),
and each match arm names a member struct. A union lists at least two
structs; `type` declares nothing else, so there is no single-type alias.

The checker desugars unions to synthetic enums with one variant per
member; everything downstream (IR / codegen) treats them as
ordinary enums.

## Shared mutable state — `Cell[T]`

Struct fields, array elements and reference-typed closure captures are
all read-only, which keeps reference cycles unconstructible. `Cell[T]` is
the one sanctioned mutable slot:

```fern
import "std/i32";

function main(): i32 {
    let hits: Cell[i32] = cell_new(0);
    let record = (): void => { hits.set(hits.get() + 1); };
    record();
    record();
    print(hits.get().to_string());   // 2
    return 0;
}
```

`T` must be a scalar, a `string`, or an array of scalars. Anything that
could hold a reference back to the cell — a struct, an enum, another
cell — is rejected (`E057`).

## Built-in `Option` and `Result`

```fern
// Built in; shown for reference.
enum Option[T] { Some(T), None }
enum Result[T, E] { Ok(T), Err(E) }
```

Both are built into the language — always in scope, no import
required. The names are reserved: declaring your own `Option` or `Result`
is an error (`E010`).

The postfix `?` operator unwraps the success variant and early-
returns the failure variant:

```fern
function parse(s: string): Result[i32, string] {
    // ...
}

function double(s: string): Result[i32, string] {
    let n: i32 = parse(s)?;          // bails on Err
    return Ok(n * 2);
}
```

`?` also works on your own two-variant enums marked `@try`, and can
convert one error type into another on the way out — see
[Error handling](../error-handling/).
