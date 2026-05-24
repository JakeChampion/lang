---
title: Type system
description: Sized integers, generics, unions, error-handling.
sidebar:
  order: 2
---

## Built-in types

| Category   | Members                                              |
| ---------- | ---------------------------------------------------- |
| Integers   | `i8` `i16` `i32` `i64` `u8` `u16` `u32` `u64` `usize` |
| Floats     | `f32` `f64`                                          |
| Other      | `boolean` `string` `void`                            |
| Composite  | `T[]` (owned array), `[T]` (slice), `(T, U, ...)` (tuple) |
| Function   | `(T1, T2) => R`                                      |

`usize` is target-aware: 4 bytes on wasm32, 8 bytes on arm64 and
x86-64. Use it for "size of a thing in memory" semantics.

## Implicit conversions

There aren't any between numeric widths. Casts are explicit:

```fern
var a: i32 = 7;
var b: i64 = a as i64;
```

The single exception is the polymorphic numeric literal: `1` types
as whatever integer the context demands.

## Generics

Functions and structs/enums take type parameters in `[...]`:

```fern
function id[T](x: T): T {
    return x;
}

struct Pair[A, B] { first: A, second: B }
enum Option[T] { Some(T), None }
```

Generic calls infer `T` from the argument types when possible.
The compiler monomorphises every distinct instantiation into a
separate copy before codegen, so there's no runtime cost.

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
member, then everything else (IR / codegen) treats them as ordinary
enums.

## Built-in `Option` and `Result`

```fern
enum Option[T] { Some(T), None }
enum Result[T, E] { Ok(T), Err(E) }
```

Both are injected by the prelude — declare them yourself only if
you want to shadow them.

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
