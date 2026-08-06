---
title: Syntax overview
description: Grammar surface, keywords, operator precedence.
sidebar:
  order: 1
---

An informal reference, not a normative grammar. When in doubt, the
parser is the source of truth ([`internal/parser`][1]).

## Keywords

Reserved across all syntactic positions:

```
function var let use as
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

## Comments

Line comments only, introduced by `//`. The formatter preserves
their original position.

```fern
// Header comment.
function main(): i32 {
    var x: i32 = 7;  // Trailing comment.
    return x;
}
```

## Operator precedence

Highest to lowest, evaluated left-to-right within each level
except where noted.

| Precedence | Operators                       | Associativity |
| ---------- | ------------------------------- | ------------- |
| 1 (highest)| `(...)` `f(...)` `a[i]` `a.f` `e?` `e as T` | left          |
| 2          | `!` `-` (unary)                 | right         |
| 3          | `*` `/` `%` `*\|` `*?` `/?` `%?` | left         |
| 4          | `+` `-` `+\|` `-\|` `+?` `-?`   | left          |
| 5          | `<<` `>>` `<<\|` `<<?` `>>?`     | left          |
| 6          | `&`                             | left          |
| 7          | `^`                             | left          |
| 8          | `\|`                            | left          |
| 9          | `<` `<=` `>` `>=`               | left          |
| 10         | `==` `!=`                       | left          |
| 11         | `&&`                            | left          |
| 12         | `\|\|`                          | left          |
| 13         | `\|>`                           | left          |
| 14 (lowest)| `=` `+=` `-=` `*=` `/=` etc.    | right         |

`+|` / `-|` / `*|` are the **saturating** integer operators: they clamp to
the operand type's `[MIN, MAX]` instead of wrapping. `+?` / `-?` / `*?` /
`/?` / `%?` / `<<?` / `>>?` are the **checked** integer operators: they
evaluate to `Some(result)` when it fits the operand type and `None` on
overflow (for `/?` / `%?`, on a zero divisor or the signed `MIN / -1`;
for `<<?` / `>>?`, on an out-of-range shift count). Both families are
integer-only and otherwise behave exactly like their wrapping
counterparts — see
[Integer semantics](https://github.com/JakeChampion/lang/blob/main/docs/INTEGER-SEMANTICS.md).

## Trailing commas

A trailing comma is legal in **every** comma-separated element list —
array literals, call arguments (positional and named), function and
lambda parameters, type parameters, generic and call type arguments,
struct literals and declarations, enum declarations, match arms, tuple
literals, and map literals:

```fern
var xs: i32[] = [
    1,
    2,
];
function f(a: i32, b: i32,): i32 { return add(a, b,); }
```

One comma, and only after an element: `[,]`, `[1,,]` and `[,1]` are all
parse errors. `fern -fmt` normalises the trailing comma away, so it is a
convenience for hand-written and generated source rather than a style the
formatter emits.

## Block forms

Statements end with `;` and group in `{ ... }` blocks. Whitespace
isn't significant.

- **`if (cond) { ... } else { ... }`** — statement or expression.
- **`if let Pat = expr { ... } else { ... }`** — one-arm match; the
  pattern's bindings are in scope for the `then` block only. `Pat` is
  the same grammar `match` arms use, so struct patterns, tuple
  patterns, nested patterns, or-patterns, `@` bindings, literals and
  ranges all work here.
- **`while (cond) { ... }`** — pre-test loop.
- **`for (init; cond; step) { ... }`** — three-part loop.
- **`for x in expr { ... }`** — foreach over an array, slice, range or
  iterator.
- **`for (k, v) in m { ... }`** — foreach over a map's entries.
- **`loop { ... }`** — infinite loop; exit with `break` / `return`.
- **`label: while … `** / **`label: for … `** / **`label: loop …`** —
  a named loop, so a nested `break label` / `continue label` can target
  it instead of the innermost one.
- **`match (expr) { Pat(b) => { ... }, ... }`** — pattern dispatch.
  Arms may carry a `when` guard.
- **`let Pat(b) = expr else { ... };`** — refutable binding; the `else`
  block must diverge.
- **`defer expr;`** / **`errdefer expr;`** — schedule expr to run on
  function exit (LIFO); `errdefer` runs only on an error exit.

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
`defer` / `errdefer`, `let … else`, `loop`, the pipe operator, and `use`.

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

Double-quoted, escape with `\\`. An `f` prefix introduces an
f-string with `{expr}` interpolation:

```fern
var name: string = "world";
print(f"hello, {name}");
```

`{{` and `}}` escape literal braces inside an f-string.

[1]: https://github.com/JakeChampion/lang/tree/main/internal/parser
