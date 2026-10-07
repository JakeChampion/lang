---
title: Traits
description: Traits and impls, bounded generics, supertraits, associated types, derive, methods, operators, Drop, and dyn Trait.
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
let p: Point = Point { x: 3, y: 4 };
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
rejected (`E074`), so the empty form is the intended way to adopt
behaviour.

### Default methods

A trait method may carry a body. An impl that leaves it out gets the
default; one that writes it replaces it.

### Supertraits

`trait Loud: Named { … }` requires every `Loud` type to implement `Named`
too, and a `[T: Loud]` bound makes `Named`'s methods callable as well:

```fern
import "std/string";

trait Named {
    function name(self: Self): string;
    function greet(self: Self): string { return "hello, " + self.name(); }
}

trait Loud: Named {
    function shout(self: Self): string;
}

struct Ash { }
impl Named for Ash { function name(self: Self): string { return "ash"; } }
impl Loud for Ash {
    function shout(self: Self): string { return self.name().to_upper(); }
}

function both[T: Loud](t: T): string {
    return t.greet() + " / " + t.shout();
}

function main(): i32 {
    print(both(Ash { }));   // hello, ash / ASH
    return 0;
}
```

### Generic traits

A trait can take type parameters — `trait Sink[T] { function put(self:
Self, v: T): i32; }` — and an impl names its argument:
`impl Sink[i32] for Counter { … }`. A type implements a given trait once,
so a second `impl Sink[string] for Counter` is a redeclaration (`E006`).
[`std/convert`](../../stdlib/convert/)'s `From[T]` and `Into[T]` are
generic traits.

### Associated types

A trait can declare a type that each impl fixes, and refer to it as
`Self::Name`. Generic code reaches it through the type parameter
(`C::Item`), so it needs no extra parameter of its own:

```fern
import "std/i32";

trait Container {
    type Item;
    function first(self: Self): Self::Item;
}

struct Ints { xs: i32[] }

impl Container for Ints {
    type Item = i32;
    function first(self: Self): Self::Item { return self.xs[0]; }
}

function head[C: Container](c: C): C::Item {
    return c.first();
}

function main(): i32 {
    print(head(Ints { xs: [7, 8] }).to_string());   // 7
    return 0;
}
```

An impl must bind every associated type the trait declares, and no
others.

### Associated functions

A trait function without `self` is called on the type: `Point.default()`,
`Celsius.from(20)`. Inside a generic function the type parameter stands in
for the type, so `[T: convert.From[i32]]` can build a `T` with
`T.from(v)`.

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

### A trait as a parameter type

A parameter whose type is a trait is shorthand for a type parameter bounded
by it:

```fern
import "core/cmp";

// The same as `describe[T: cmp.Display](s: T): string`.
function describe(s: cmp.Display): string {
    return "<" + s.to_string() + ">";
}

function main(): i32 {
    print(describe(42));        // <42>
    print(describe("pear"));    // <pear>
    return 0;
}
```

Each such parameter gets a type parameter of its own, so in
`function pair(a: cmp.Display, b: cmp.Display)` the two arguments may be
different types. Write the type parameter out when two parameters must share
one, or when the return type names it. The parameter's type is the trait
itself, with its type arguments when it has them (`s: Sink[i32]`); an array
of a trait is not it, and a trait's methods are not generic, so inside an
`impl` the rule does not apply. A trait means this only as a parameter type:
in a return type, a field or a `let`, write
[`dyn Trait`](#runtime-dispatch--dyn-trait) for a value of any implementing
type, or a type parameter.

### A generic function as a value

A generic function can be passed or stored where a function type is
expected, and takes its type arguments from that type:

```fern
import "core/cmp";

function show[T: cmp.Display](v: T): string { return v.to_string(); }
function apply(f: (i32) => string, v: i32): string { return f(v); }

function main(): i32 {
    print(apply(show, 42));                 // 42: `show` at T = i32
    let g: (string) => string = show;       // `show` at T = string
    print(g("pear"));
    return 0;
}
```

Each use is its own instance, as a call would be. The expected type has to
determine every type parameter: `let f = show;` has none and is refused
(E040), as is a type that leaves a bound unmet (E021).

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
| `Debug`   | `to_debug(self): string`                | Diagnostic rendering.    |

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

The derivable traits are **`cmp.Eq`, `cmp.Ord`, `cmp.Hash`,
`cmp.Display`, `cmp.Debug`, `cmp.Default`**, and from
[`std/json`](../../stdlib/json/) **`json.Json`** (`to_json()`) and
**`json.FromJson`** (`T.from_json(text)`). Each composes structurally:

- **`Eq` / `Ord` / `Hash`** fold field-by-field (and, for enums, over the
  variant tag then its payload), so a type is comparable/hashable as soon
  as its fields are.
- **`Display`** renders `Name { field: value, … }` (for an enum,
  `Variant(payload)`); **`Debug`** is the same but quotes strings, for an
  unambiguous diagnostic dump.
- **`Default`** builds a zero value — scalars use their zero literal,
  nested types delegate to their own `default()`, and an enum defaults to
  its first variant, with each payload at its own default (`Running(0)`
  for an enum whose first variant is `Running(i32)`). A field whose type
  has no `default()` makes the derive fail; implement `Default` by hand
  in that case.

A derived impl is an ordinary impl — it satisfies bounds (`[T: cmp.Eq]`)
and is callable (`p.hash()`, `Status.default()`) exactly like a
hand-written one. Mix and match: derive the boring traits and
hand-write the interesting one. Deriving `Eq` also gives the type `==`
and `!=`, and `Ord` gives `<` `<=` `>` `>=` (see
[operators](#operators-on-your-own-types)).

## Methods and inherent impls

A method needs no trait. Either spelling declares one:

```fern
import "std/i32";

struct Point { x: i32, y: i32 }

impl Point {
    function origin(): Point { return Point { x: 0, y: 0 }; }   // no self
    function sum(self: Self): i32 { return self.x + self.y; }
}

function (p: Point) scaled(k: i32): Point {
    return Point { x: p.x * k, y: p.y * k };
}

function main(): i32 {
    let p: Point = Point { x: 3, y: 4 };
    print(p.sum().to_string());               // 7
    print(p.scaled(2).sum().to_string());     // 14
    print(Point.origin().sum().to_string());  // 0
    return 0;
}
```

The receiver clause `function (p: Point) name(…)` works on any type,
primitives included — that is how `std/i32` gives `i32` its
`to_string`.

## Operators on your own types

The operators are spelled as methods, matched by name, so a type that
declares the method gets the operator. No trait is involved.

| Operator | Method |
| -------- | ------ |
| `==` `!=` | `eq(other): boolean` |
| `<` `<=` `>` `>=` | `cmp(other): i32` |
| `+` `-` `*` `/` `%` | `add` `sub` `mul` `div` `rem` |
| `&` `\|` `^` `<<` `>>` | `bitand` `bitor` `bitxor` `shl` `shr` |
| unary `-` | `neg()` |

```fern
import "std/i32";

struct V2 { x: i32, y: i32 }

function (a: V2) add(b: V2): V2 { return V2 { x: a.x + b.x, y: a.y + b.y }; }
function (a: V2) eq(b: V2): boolean { return a.x == b.x && a.y == b.y; }

function main(): i32 {
    let p: V2 = V2 { x: 1, y: 2 } + V2 { x: 3, y: 4 };
    print(p.x.to_string());                     // 4
    print((p == V2 { x: 4, y: 6 }).to_string()); // true
    return 0;
}
```

## Finalizers — `mem.Drop`

`impl mem.Drop for T` (from [`core/mem`](../../stdlib/mem/)) gives `T` a
`drop(self)` that runs when the reference count of a `T` value reaches
zero, before its fields are freed. Calling `drop` yourself is an error
(`E073`).

Treat it as a cleanup hook, not a guarantee. When it runs is the
compiler's release point, which can be earlier than the end of the
enclosing block, and the interpreter (`fern -interp`) never runs it at
all. For cleanup that must happen at a known point, use
[`defer`](../language-features/#deferred-cleanup--defer-and-errdefer).

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

let xs: dyn cmp.Display[] = [42, "hi", true];
for x in xs { print(x.to_string()); }
```

It works on the interpreter and on all three compiled backends (x86-64,
arm64, wasm), for struct, enum, string and primitive concretes, including
multi-trait sets (`dyn A + B`). `e as? T` downcasts back to a concrete
type, giving `Option[T]`:

```fern
import "std/float";

trait Shape { function area(self: Self): f64; }

struct Circle { r: f64 }
struct Rect { w: f64, h: f64 }
impl Shape for Circle { function area(self: Self): f64 { return 3.0 * self.r * self.r; } }
impl Shape for Rect { function area(self: Self): f64 { return self.w * self.h; } }

function main(): i32 {
    let shapes: dyn Shape[] = [Circle { r: 1.0 }, Rect { w: 2.0, h: 3.0 }];
    for s in shapes {
        match (s as? Rect) {
            Some(r) => { print(r.w.to_string()); },   // 2, for the Rect
            None    => { print(s.area().to_string()); },  // 3, for the Circle
        }
    }
    return 0;
}
```

`as?` applies only to a `dyn` value (`E059`). Bounded generics stay the better choice when the type IS
known at each call site — they monomorphise, so there is no box and no
indirect call. See [`docs/DYN-TRAITS.md`][dyn] for the design, the
representation per backend, and the remaining gaps.

## See also

- [Type system](../types/) — generics, unions, `Option` / `Result`.
- [`core/cmp`](../../stdlib/cmp/) — the reference for the trait set above.
- The full design of record lives in [`docs/TRAITS.md`][traits].
[traits]: https://github.com/JakeChampion/lang/blob/main/docs/TRAITS.md
[dyn]: https://github.com/JakeChampion/lang/blob/main/docs/DYN-TRAITS.md
