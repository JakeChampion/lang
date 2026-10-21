---
title: std/shapes
description: "Every declaration shape a reference page renders."
---

# `std/shapes`

std/shapes — every declaration shape a reference page renders.

The intro's second paragraph.

## `struct Point`

```fern
pub struct Point { x: i32, y: i32 }
```

A point on the plane.

## `enum Turn`

```fern
pub enum Turn {
    Left,
    Right(i32),
}
```

Which way a turn goes.

## `trait Sized`

```fern
pub trait Sized {
    function size(self: Self): i32;
}
```

Something that can be measured.

## `trait Area`

```fern
pub trait Area[T]: Sized {
    function area(self: Self): T;
}
```

Something with an area of type T.

## `const LIMIT`

```fern
pub const LIMIT: i32
```

The largest coordinate.

## `dist`

```fern
pub function dist(a: Point, b: Point): i32
```

dist is the Manhattan distance between two points.

## `first`

```fern
pub function first[T](xs: T[]): Option[T]
```

first returns the first element, if there is one.

## Implementations

| Trait | Implementing types |
| ----- | ------------------ |
| `Sized` | `Point` |

