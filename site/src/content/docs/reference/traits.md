---
title: Traits
description: Shared behaviour across types — trait / impl and bounded generics.
sidebar:
  order: 3
---

A **trait** names a set of method signatures a type can implement. It's
how Fern expresses "any type that can do X" — comparison, hashing,
stringification — without inheritance and, by default, without any
runtime cost. Dispatch is resolved at compile time.

## Declaring a trait

A trait lists method signatures. `Self` stands for the implementing
type. A method with a `self` parameter is called on a value; one without
is an *associated function* (a constructor-like operation on the type
itself).

```fern
trait Display {
    function to_string(self: Self): string;
}

trait Default {
    function default(): Self;          // associated — no `self`
}
```

## Implementing a trait

`impl Trait for Type { … }` provides the bodies:

```fern
struct Point { x: i32, y: i32 }

impl Display for Point {
    function to_string(self: Self): string {
        return "(" + self.x.to_string() + ", " + self.y.to_string() + ")";
    }
}
```

Once a type implements `Display`, its method is callable like any other:

```fern
var p: Point = Point { x: 3, y: 4 };
print(p.to_string());   // (3, 4)
```

### Empty impls adopt existing methods

If a type *already* has a method matching the trait, an **empty impl**
records conformance without redeclaring it. That's how primitives opt
into a trait — `i32` already carries `to_string` from `std/i32`:

```fern
impl Display for i32 { }     // adopts the existing to_string
```

A non-empty impl whose method would collide with an existing one is
rejected, so the empty form is the intended way to adopt behaviour.

## Bounded generics — `[T: Trait]`

The payoff is writing one generic function that works for *every* type
implementing a trait. A type parameter carries a bound; inside the
function you may call the trait's methods on values of that type.
Combine bounds with `+`:

```fern
import "core/cmp";

// Works for any T that is both orderable and printable.
function max[T: cmp.Ord + cmp.Display](a: T, b: T): T {
    if (a.cmp(b) >= 0) { return a; }
    return b;
}

function main(): i32 {
    print(max(3, 9).to_string());          // 9
    print(max("apple", "pear"));           // pear
    return 0;
}
```

Bounded generics are **monomorphised**: the compiler stamps out one
concrete copy of `max` per type it's called with and resolves each
method call statically. There's no vtable and no per-call overhead — the
same dispatch story as the rest of the language.

This is exactly how the [test runner](../../tutorial/testing/) types its
assertions — `assert_eq[T: cmp.Eq + cmp.Display]` accepts any comparable,
printable value.

## The `core/cmp` foundation

The standard library's [`core/cmp`](../../stdlib/cmp/) module defines the
common traits so you rarely declare your own from scratch:

| Trait     | Method                                  | Meaning                  |
| --------- | --------------------------------------- | ------------------------ |
| `Display` | `to_string(self): string`               | Render as text.          |
| `Eq`      | `eq(self, other): boolean`              | Equality.                |
| `Ord`     | `cmp(self, other): i32`                 | Three-way ordering.      |
| `Hash`    | `hash(self): i32`                       | Hash code.               |
| `Default` | `default(): Self`                       | A zero/empty value.      |

The built-in primitives already implement them, so generic code bounded
on `cmp.*` works for `i32`, `string`, and friends out of the box.

## Deriving traits — `@derive`

For the mechanical traits, writing the `impl` by hand is busywork — the
body is determined entirely by the fields. The `@derive(...)` attribute
on a `struct` or `enum` synthesises it for you:

```fern
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Point { x: i32, y: i32 }

@derive(cmp.Display, cmp.Default)
enum Status { Idle, Running(i32), Done(i32, i32) }
```

The derivable traits are **`Eq`, `Ord`, `Hash`, `Display`, `Debug`,
`Default`, and `Json`**. Each composes structurally:

- **`Eq` / `Ord` / `Hash`** fold field-by-field (and, for enums, over the
  variant tag then its payload), so a type is comparable/hashable as soon
  as its fields are.
- **`Display`** renders `Name { field: value, … }` (for an enum,
  `Variant(payload)`); **`Debug`** is the same but quotes strings, for an
  unambiguous diagnostic dump.
- **`Default`** builds a zero value — scalars use their zero literal,
  nested types delegate to their own `default()`, and an enum defaults to
  its first variant. It's an error to derive `Default` for a type with a
  field that has no default (e.g. a payload-carrying first variant);
  implement it by hand in that case.

A derived impl is an ordinary impl — it satisfies bounds (`[T: cmp.Eq]`)
and is callable (`p.hash()`, `Status.default()`) exactly like a
hand-written one. Mix and match: derive the boring traits and
hand-write the interesting one.

## Coherence (the orphan rule)

An `impl Trait for Type` is only legal in the module that defines the
**trait** or the module that defines the **type**. This keeps a program
from containing two conflicting impls of the same trait for the same
type — conformance is global and unambiguous.

## Runtime dispatch — `dyn Trait`

Everything above is *static* dispatch. When you genuinely need a
heterogeneous collection — values of different concrete types behind one
trait — `dyn Trait` is the runtime-dispatch counterpart: the value carries
a method table alongside its data, and `d.m()` calls through the table.

```fern
import "core/cmp";

var xs: dyn cmp.Display[] = [42, "hi", true];
for x in xs { print(x.to_string()); }
```

It works on the interpreter and on all three compiled backends (x86-64,
arm64, wasm), for struct, enum, string and primitive concretes, including
the `e as? T` downcast back to a concrete type and multi-trait sets
(`dyn A + B`). Bounded generics stay the better choice when the type IS
known at each call site — they monomorphise, so there is no box and no
indirect call. See [`docs/DYN-TRAITS.md`][dyn] for the design, the
representation per backend, and the remaining gaps.

## See also

- [Type system](../types/) — generics, unions, `Option` / `Result`.
- [`core/cmp`](../../stdlib/cmp/) — the reference for the trait set above.
- The full design of record lives in [`docs/TRAITS.md`][traits].
[traits]: https://github.com/JakeChampion/lang/blob/main/docs/TRAITS.md
[dyn]: https://github.com/JakeChampion/lang/blob/main/docs/DYN-TRAITS.md
