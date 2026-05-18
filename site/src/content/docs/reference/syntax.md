---
title: Syntax overview
description: Grammar surface, keywords, operator precedence.
sidebar:
  order: 1
---

This is an informal reference, not a normative grammar. When in
doubt, the parser is the source of truth ([`internal/parser`][1]).

## Keywords

Reserved across all syntactic positions:

```
function var let use as
if else while for break continue return
true false boolean void string
i8 i16 i32 i64 u8 u16 u32 u64 usize f32 f64
switch case default
struct enum type
import pub const
match when
defer arena
```

## Comments

Only line comments, introduced by `//`. The formatter preserves
them in their original position.

```lang
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
| 3          | `*` `/` `%`                     | left          |
| 4          | `+` `-`                         | left          |
| 5          | `<<` `>>`                       | left          |
| 6          | `&`                             | left          |
| 7          | `^`                             | left          |
| 8          | `\|`                            | left          |
| 9          | `<` `<=` `>` `>=`               | left          |
| 10         | `==` `!=`                       | left          |
| 11         | `&&`                            | left          |
| 12         | `\|\|`                          | left          |
| 13         | `\|>`                           | left          |
| 14 (lowest)| `=` `+=` `-=` `*=` `/=` etc.    | right         |

## Block forms

Statements end with `;` and group in `{ ... }` blocks. There's no
significant whitespace.

- **`if (cond) { ... } else { ... }`** — statement or expression.
- **`while (cond) { ... }`** — pre-test loop.
- **`for (init; cond; step) { ... }`** — three-part loop.
- **`match (expr) { Pat(b) => { ... }, ... }`** — pattern dispatch.
- **`switch (expr) { case 1: ...; default: ... }`** — value dispatch.
- **`defer expr;`** — schedule expr to run on function exit (LIFO).
- **`arena { ... }`** — bump-allocator scope; allocations inside
  reclaim on exit.

## String literals

Double-quoted, escape with `\\`. The prefix `f` introduces an
f-string with `{expr}` interpolation:

```lang
var name: string = "world";
print(f"hello, {name}");
```

`{{` and `}}` escape literal braces inside an f-string.

[1]: https://github.com/JakeChampion/lang/tree/main/internal/parser
