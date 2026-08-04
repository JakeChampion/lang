---
title: Type system
description: Sized integers, generics, unions, error-handling.
sidebar:
  order: 2
---

## Built-in types

| Category   | Members                                              |
| ---------- | ---------------------------------------------------- |
| Integers   | `i32` `i64` `u8` `u32` `u64` `usize`                  |
| Floats     | `f32` `f64` (`float` is an alias for `f64`)          |
| Other      | `boolean` `string` `void`                            |
| Composite  | `T[]` (owned array), `[T]` (slice), `(T, U, ...)` (tuple), `Map[K, V]` |
| Function   | `(T1, T2) => R`                                      |

`usize` is target-aware: 4 bytes on wasm32, 8 on arm64 and x86-64.
Use it for "size of a thing in memory" semantics.

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

There is no `i8`, `i16` or `u16`: they cost a full set of per-stride
backend paths for a handful of call sites, so `i32` / `u32` cover that
ground instead. `u8` stays because bytes are genuinely a different thing.

## Maps

`Map[K, V]` is built into the language, with a literal syntax — but its
operations live in `core/map`, so the module has to be imported:

```fern
import "core/map";

var stock: Map[string, i32] = Map { "frond": 3, "spore": 7 };
stock = stock.insert("rhizome", 1);

match (stock.get("frond")) {
    Some(n) => { print(n.to_string()); },
    None    => { print("(none)"); },
}
```

Keys are integers or strings. Iteration order is insertion order and is
part of the contract, not an accident — see the
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
var a: i32 = 7;
var b: i64 = a as i64;
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
enum Option[T] { Some(T), None }
```

Generic calls infer `T` from the argument types when possible. The
compiler monomorphises every distinct instantiation before codegen,
so there's no runtime cost.

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

The checker desugars unions to synthetic enums with one variant per
member; everything downstream (IR / codegen) treats them as
ordinary enums.

## Built-in `Option` and `Result`

```fern
enum Option[T] { Some(T), None }
enum Result[T, E] { Ok(T), Err(E) }
```

Both are built into the language — always in scope, no import
required. Declare them yourself only if you want to shadow them.

The postfix `?` operator unwraps the success variant and early-
returns the failure variant:

```fern
function parse(s: string): Result[i32, string] {
    // ...
}

function double(s: string): Result[i32, string] {
    var n: i32 = parse(s)?;          // bails on Err
    return Ok(n * 2);
}
```
