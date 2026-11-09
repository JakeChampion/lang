---
title: std/pair
description: "Standard library reference for std/pair."
---

# `std/pair`

## `Pair.c_size`

```fern
pub function c_size(): usize
```

## `Pair.c_offset_a`

```fern
pub function c_offset_a(): usize
```

## `Pair.c_load_a`

```fern
pub function c_load_a(p: usize): i32
```

## `Pair.c_store_a`

```fern
pub function c_store_a(p: usize, v: i32): void
```

## `Pair.c_offset_b`

```fern
pub function c_offset_b(): usize
```

## `Pair.c_load_b`

```fern
pub function c_load_b(p: usize): i32
```

## `Pair.c_store_b`

```fern
pub function c_store_b(p: usize, v: i32): void
```

## `Pair.c_load`

```fern
pub function c_load(p: usize): Pair
```

## `c_store`

```fern
pub function (self: Pair) c_store(p: usize): void
```

## `struct Pair`

```fern
pub struct Pair { a: i32, b: i32 }
```

Two numbers side by side.

