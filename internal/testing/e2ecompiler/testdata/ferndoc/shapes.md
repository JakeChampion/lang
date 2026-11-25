---
title: std/shapes
description: "This module demonstrates every declaration that a reference page can show."
---

# `std/shapes`

std/shapes — This module demonstrates every declaration that a reference page can show.

The module introduction has a second paragraph.

## `struct Point`

```fern
pub struct Point { x: i32, y: i32 }
```

Point identifies a position on a plane.

## `enum Turn`

```fern
pub enum Turn {
    Left,
    Right(i32),
}
```

Turn identifies the direction of a turn.

## `trait Sized`

```fern
pub trait Sized {
    function size(self: Self): i32;
}
```

Sized defines a size method.

## `trait Area`

```fern
pub trait Area[T]: Sized {
    function area(self: Self): T;
}
```

Area defines an area method with result type T.

## `const LIMIT`

```fern
pub const LIMIT: i32
```

LIMIT is the largest coordinate.

## `dist`

```fern
pub function dist(a: Point, b: Point): i32
```

dist returns the sum of the coordinate differences.

## `first`

```fern
pub function first[T](xs: T[]): Option[T]
```

first returns the first element, or None for an empty array.

## `count`

```fern
pub function count(m: Map[string, i32], test: (Map[string, i32], string) => boolean): i32
```

count returns the number of keys that test accepts.

## Implementations

| Trait | Implementing types |
| ----- | ------------------ |
| `Sized` | `Point` |

