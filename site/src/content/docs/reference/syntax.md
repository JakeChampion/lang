---
title: Syntax overview
description: Keywords, literals, operator precedence, block forms, functions, and attributes.
sidebar:
  order: 1
---

An informal reference. The normative grammar is
[`spec/grammar.ebnf`][1]; the parser in [`compiler/parser.fern`][2] is
the primary implementation.

## Keywords

Reserved across all syntactic positions:

```
function let use as
if else while for loop break continue return
true false boolean void string
i32 i64 u8 u32 u64 usize f32 f64
default
struct enum type
import pub const
match when
defer errdefer
trait impl dyn
```

`in` is *not* reserved — the foreach forms below match it positionally,
so `in` stays available as an ordinary identifier. Neither is `float`,
the width-unqualified alias for `f64`: the parser recognises it in type
position only, which keeps `float.pi()` module calls working.

Several more names mean something only in one position and are ordinary
identifiers everywhere else:

| Name | Where it means something |
| ---- | ------------------------ |
| `char`, `str`, `Self` | type position |
| `Map` | before `{`, a map literal |
| `own` | before a parameter name: the function consumes the argument |
| `fip`, `fbip`, `async` | before `function`: see [function modifiers](#function-modifiers) |
| `assert`, `todo` | statement position: see [statement builtins](#statement-builtins) |

## Literals

| Form | Examples | Type |
| ---- | -------- | ---- |
| Integer | `42`, `0xff`, `0b1010`, `0o17` | whatever integer type the context needs, else `i32` |
| Suffixed integer | `7i64`, `3u32`, `255u8` | the suffix (`i32` `i64` `u8` `u32` `u64`) |
| Float | `2.5`, `1e3`, `2.5f32` | `f64`, or `f32` with the suffix |
| Character | `'x'`, `'é'`, `'\u{1F600}'` | `char`, one Unicode scalar value |
| Byte | `b'x'`, `b'\n'` | `u8` |
| String | `"tab\there"` | `string` |
| f-string | `f"{n} items"` | `string` |
| Boolean | `true`, `false` | `boolean` |
| Unit | `()` | `void` |

There are no digit separators. An integer literal that does not fit its
type is a compile error (`E047`), so `let b: u8 = 300;` is rejected rather
than wrapped. A character literal and a byte literal never convert into
each other or into an integer without an `as` cast: `s[i] == b'['`
type-checks, `s[i] == '['` does not.

## Comments

Line comments only, introduced by `//`. The formatter preserves
their original position, and emits declarations in source order so a
comment stays with the declaration it documents.

```fern
// Header comment.
function main(): i32 {
    let x: i32 = 7;  // Trailing comment.
    return x;
}
```

An enum or struct is written on one line when it fits, but a comment
inside its braces expands it — the one-line form has nowhere to put the
comment, so keeping it attached takes a line per element:

```fern
enum Verdict {
  Balanced,
  Unexpected(i32),  // closer at pos, nothing open
  Unclosed(i32),  // opener never closed
}
```

## Operator precedence

Highest to lowest, evaluated left-to-right within each level
except where noted.

| Precedence | Operators                       | Associativity |
| ---------- | ------------------------------- | ------------- |
| 1 (highest)| `(...)` `f(...)` `a[i]` `a[i:j]` `a.f` `e?` | left  |
| 2          | `e as T` `e as? T`              | left          |
| 3          | `!` `-` (unary)                 | right         |
| 4          | `*` `/` `%` `*\|` `*?` `/?` `%?` | left         |
| 5          | `+` `-` `+\|` `-\|` `+?` `-?`   | left          |
| 6          | `<<` `>>` `<<\|` `<<?` `>>?`     | left          |
| 7          | `<` `<=` `>` `>=`               | left          |
| 8          | `==` `!=`                       | left          |
| 9          | `&`                             | left          |
| 10         | `^`                             | left          |
| 11         | `\|`                            | left          |
| 12         | `&&`                            | left          |
| 13         | `\|\|`                          | left          |
| 14         | `..` `..=`                      | none          |
| 15         | `\|>`                           | left          |
| 16 (lowest)| `=` `+=` `-=` `*=` `/=` `%=` `&=` `\|=` `^=` `<<=` `>>=` | right |

The bitwise operators bind **looser** than comparison, as in C:
`a & 3 == 2` parses as `a & (3 == 2)` and is a type error, so write
`(a & 3) == 2`. A cast binds tighter than unary minus, so `-a as i64` is
`-(a as i64)`, and a range takes whole arithmetic operands:
`0..n + 1` is `0..(n + 1)`.

`+|` / `-|` / `*|` / `<<|` are the **saturating** integer operators: they
clamp to the operand type's `[MIN, MAX]` instead of wrapping. `+?` / `-?` / `*?` /
`/?` / `%?` / `<<?` / `>>?` are the **checked** integer operators: they
evaluate to `Some(result)` when it fits the operand type and `None` on
overflow (for `/?` / `%?`, on a zero divisor or the signed `MIN / -1`;
for `<<?` / `>>?`, on an out-of-range shift count). Both families are
integer-only and otherwise behave exactly like their wrapping
counterparts — see [Integer arithmetic](../types/#integer-arithmetic).

`e as? T` is the checked downcast from a `dyn Trait` value to a concrete
type, giving `Option[T]` — see [Traits](../traits/#runtime-dispatch--dyn-trait).

## Trailing commas

A trailing comma is legal in the comma-separated lists you write most —
array literals, call arguments (positional and named), function and
lambda parameters, a function's type parameters, explicit call type
arguments (`id[i32,](x)`), struct literals and declarations, enum
variant lists, match arms, variant and tuple patterns, tuple literals,
map literals, and the map-foreach binder (`for (k, v,) in m`):

```fern
let xs: i32[] = [
    1,
    2,
];
function f(a: i32, b: i32,): i32 { return add(a, b,); }
```

One comma, and only after an element: `[,]`, `[1,,]` and `[,1]` are all
parse errors. A tuple type does not take one: `(i32, i32,)` is rejected. `fern -fmt` normalises the trailing comma away, so it is a
convenience for hand-written and generated source rather than a style the
formatter emits.

## Block forms

Statements end with `;` and group in `{ ... }` blocks. Whitespace
isn't significant.

- **`if (cond) { ... } else { ... }`** — statement or expression. As an
  expression it needs the `else`, and each branch is a braced block.
- **`if let Pat = expr { ... } else { ... }`** — one-arm match; the
  pattern's bindings are in scope for the `then` block only. `Pat` is
  the same grammar `match` arms use, so struct patterns, tuple
  patterns, nested patterns, or-patterns, `@` bindings, literals and
  ranges all work here.
- **`while (cond) { ... }`** — pre-test loop.
- **`for (init; cond; step) { ... }`** — three-part loop.
- **`for x in expr { ... }`** — foreach over an array, slice, range or
  iterator.
- **`for Pat in expr { ... }`** — foreach with a destructuring binder,
  the same irrefutable pattern `let Pat = e;` and a destructuring
  parameter take: a tuple (nested elements and `_` discards included) or
  a struct pattern (with renaming and `..`), optionally with an `@`
  binding for the whole element. Over an array it binds against each
  element; over a map, against each entry's key and value — a map binds
  those separately, so there is no whole entry for `@` to name and one is
  rejected there.
- **`loop { ... }`** — infinite loop; exit with `break` / `return`.
- **`label: while … `** / **`label: for … `** / **`label: loop …`** —
  a named loop, so a nested `break label` / `continue label` can target
  it instead of the innermost one.
- **`match (expr) { Pat(b) => { ... }, ... }`** — pattern dispatch,
  statement or expression. Arms may carry a `when` guard.
- **`{ stmt; stmt; value }`** — a block in value position. Its statements
  run first; the final expression, written without a `;`, is its value.
  See [value blocks](../language-features/#value-blocks-and-expression-forms).
- **`let Pat = expr else { ... };`** — refutable binding, same pattern
  grammar as `if let` and `match`; the pattern's bindings are live for
  the rest of the block and the `else` block must diverge.
- **`function f(Pat: T)`** — destructuring parameter, same grammar again
  but irrefutable only (a tuple or struct pattern, optionally with an
  `@` binding for the whole value).
- **`let name: T = expr;`** / **`let name = expr;`** — a binding, with or
  without a type annotation. `let` is the only binding keyword, and every
  binding can be reassigned with `=`.
- **`let Pat = expr;`** — irrefutable destructure, the third site taking
  that same pattern. A refutable head belongs to the `let … else` form
  above, which has a branch for the miss.
- **`defer expr;`** / **`errdefer expr;`** — schedule expr to run when the
  scope that reached the statement finishes (LIFO): function exit, or the
  end of the iteration for a `defer` in a loop body. `errdefer` runs only
  on an error exit.

Ranges are `start..end` (exclusive) and `start..=end` (inclusive):

```fern
for i in 0..xs.len() { print(xs[i]); }
for n in 1..=100 { total = total + n; }
```

Outside a foreach head, a range is an ordinary iterator value —
`0..5` desugars to `iter.range(0, 5)` — so it can be passed to any
combinator, given `import "core/iter"`. In a `for … in` head it compiles
to a counted loop instead, with no iterator allocated.

See [Language features](../language-features/) for the iteration forms,
`defer` / `errdefer`, `let … else`, `loop`, closures, the pipe operator,
`use`, and modules.

## Functions

```fern
function area(w: i32, h: i32 = 1): i32 {
    return w * h;
}

function main(): i32 {
    let a: i32 = area(3);            // h defaults to 1
    let b: i32 = area(3, h = 4);     // a named argument
    return a + b - 15;
}
```

- **The return type is required** on every named function — `: void` when
  it returns nothing. Leaving it off is a compile error, not a request to
  infer it. A lambda may omit its return type (see
  [closures](../language-features/#closures)).
- **Default values** follow a parameter's type (`h: i32 = 1`); a call may
  leave those trailing arguments out.
- **Named arguments** (`area(3, h = 4)`) may follow the positional ones.
- **Methods** are functions with a receiver clause:
  `function (p: Point) norm(): f64 { … }`, called as `p.norm()`. An
  `impl Type { … }` block is the other spelling — see
  [Traits](../traits/#methods-and-inherent-impls).
- **Generic** functions take type parameters in `[...]` after the name;
  see [Generics](../types/#generics).
- **`const NAME: T = expr;`** declares a top-level constant. It may be
  `pub`, and the annotation may be omitted.
- **Nested functions** — a `function` declared inside a body is local to
  it and can read the enclosing function's bindings.

### Function modifiers

| Modifier | Meaning |
| -------- | ------- |
| `pub` | exported from the module; `pub(package)` limits that to modules in the same directory |
| `fip` / `fbip` | the compiler checks that the function updates in place: no fresh heap allocation, only reuse of memory it consumes (`E053`). A `fip` function may call only `fip` functions; an `fbip` one may also call `fbip` ones. See [`docs/MODE-LATTICE.md`](https://github.com/JakeChampion/lang/blob/main/docs/MODE-LATTICE.md) |
| `async` | marks a WebAssembly component export as asynchronous (WASI Preview 3); it changes nothing about how the body is written or run. There is no `await` keyword: concurrency is the [`std/async`](../../stdlib/async/) combinators over `Future[T]` |

## Statement builtins

Two constructs are recognised in statement position only, so both names
stay usable as ordinary identifiers elsewhere.

- **`assert(cond);`** / **`assert(cond, msg);`** — on failure prints
  `assertion failed[: msg]` to stderr and exits `1`.
- **`todo;`** / **`todo("msg");`** — an unimplemented marker that prints
  `todo[: msg]` and exits `101`. It counts as diverging, so it can stand
  in for the entire body of a function that owes a return value.

```fern
function render(width: i32): string {
    todo("wire up the renderer");
}
```

## String literals

Double-quoted. The escapes are `\n` `\t` `\r` `\0` `\\` `\"` and `\xNN`
(one byte); each produces exactly one byte, and an embedded NUL counts
toward `len()`. A character literal also takes `\'` and `\u{…}`. An `f`
prefix introduces an f-string with `{expr}` interpolation:

```fern
import "std/string";

function main(): i32 {
    let name: string = "world";
    print(f"hello, {name}");
    return 0;
}
```

`{{` and `}}` escape literal braces inside an f-string. Each hole
becomes `(expr).to_string()`, so the `to_string` for that type has to be
in scope: `import "std/string"` for a string, `"std/i32"` for an `i32`,
`"std/float"` for a float, and so on. Without it the checker names the
module to import.

## Attributes

An attribute starts with `@` and applies to the declaration after it.

| Attribute | Applies to | Effect |
| --------- | ---------- | ------ |
| `@derive(cmp.Eq, …)` | struct, enum | generates trait impls — see [Traits](../traits/#deriving-traits--derive) |
| `@try` | enum | lets `?` unwrap it — see [Error handling](../error-handling/#on-your-own-enums--try) |
| `@must_consume` | struct, enum | every value must be consumed on every path — see [Language features](../language-features/#values-that-must-be-used--must_consume) |
| `@inline` / `@noinline` | function | lift the inliner's size limit, or never inline |
| `@import("iface", "name")` | body-less function | binds a WebAssembly component import |
| `@export("iface", "name")` | function | binds a WebAssembly component export |

Any other name is a parse error.

[1]: https://github.com/JakeChampion/lang/blob/main/spec/grammar.ebnf
[2]: https://github.com/JakeChampion/lang/blob/main/compiler/parser.fern
