---
title: Error handling
description: Option, Result, the ? operator, and exhaustive match — errors as values.
sidebar:
  order: 4
---

Fern has no exceptions and no `panic`-as-control-flow. Fallible
operations return a value that *describes* the failure, and the type
system makes you account for it. Two built-in enums carry that weight.

## `Option[T]` — a value that might be absent

```fern
enum Option[T] { Some(T), None }
```

Use `Option` when "nothing" is a normal, non-exceptional outcome — a map
lookup that misses, a parse that finds no token, the first element of a
possibly-empty list.

```fern
match (m.get("key")) {
    Some(v) => { print(v); },
    None    => { print("(absent)"); },
}
```

## `Result[T, E]` — success or a described failure

```fern
enum Result[T, E] { Ok(T), Err(E) }
```

Use `Result` when failure carries information you want to report — an I/O
error, a validation message, a parse position. The error type `E` is
yours to choose: a `string`, or a rich struct.

```fern
struct ParseError { message: string, pos: i32 }

function parse_int(s: string): Result[i32, ParseError] {
    if (s.len() == 0) {
        return Err(ParseError { message: "empty input", pos: 0 });
    }
    // ...
    return Ok(42);
}
```

Both `Option` and `Result` are **built into the language** — always in
scope, no import needed. Importing [`std/option`](../../stdlib/option/)
or [`std/result`](../../stdlib/result/) only adds the combinator methods
described below.

## The `?` operator

Postfix `?` is the workhorse. On a `Result`, it unwraps `Ok` and
early-returns `Err`; on an `Option`, it unwraps `Some` and early-returns
`None`. The enclosing function's return type must match.

```fern
function double(s: string): Result[i32, ParseError] {
    var n: i32 = parse_int(s)?;   // returns the Err if parse_int failed
    return Ok(n * 2);
}
```

Without `?`, that's a `match` with an explicit error-propagating arm.
`?` is the same thing, written once.

## `match` is exhaustive

The checker rejects a `match` that omits a variant — you can't forget the
`None` case or the `Err` case. Add a wildcard `_` arm only when you
genuinely mean "everything else":

```fern
match (parse_int(input)) {
    Ok(v)  => { print("got " + v.to_string()); },
    Err(e) => { eprint("error at " + e.pos.to_string() + ": " + e.message); },
}
```

This is the property that makes errors-as-values pay off: adding a new
variant to an enum turns every non-exhaustive `match` into a *compile
error*, so the compiler walks you to each place that needs updating.

## Combinator methods

When a full `match` is overkill, `std/option` and `std/result` provide
chainable helpers. Import the module to bring them into scope. These are
the complete sets; [`std/option`](../../stdlib/option/) and
[`std/result`](../../stdlib/result/) carry the exact signatures.

### On `Option[T]`

| Method                       | Returns          | Meaning                              |
| ---------------------------- | ---------------- | ------------------------------------ |
| `is_some()` / `is_none()`    | `boolean`        | Tag test.                            |
| `is_some_and(pred)`          | `boolean`        | `Some` *and* the payload satisfies `pred`. |
| `is_none_or(pred)`           | `boolean`        | `None`, or the payload satisfies `pred`. |
| `unwrap_or(fallback)`        | `T`              | The value, or `fallback` if `None`.  |
| `unwrap_or_else(f)`          | `T`              | …or compute the fallback lazily.     |
| `map(f)`                     | `Option[U]`      | Transform the `Some` payload.        |
| `map_or(fallback, f)`        | `U`              | Transform, or fall back to a value.  |
| `map_or_else(default_fn, f)` | `U`              | …with a lazily-computed fallback.    |
| `and_then(f)`                | `Option[U]`      | Chain another optional computation.  |
| `and(other)`                 | `Option[U]`      | `other` if this is `Some`.           |
| `or(other)`                  | `Option[T]`      | This, or `other` when `None`.        |
| `or_else(f)`                 | `Option[T]`      | Supply an alternative when `None`.   |
| `xor(other)`                 | `Option[T]`      | `Some` only if exactly one is.       |
| `filter(pred)`               | `Option[T]`      | Keep `Some` only if `pred` holds.    |
| `zip(other)`                 | `Option[(T, U)]` | Pair two `Some`s, else `None`.       |
| `unzip()`                    | `(Option[T], Option[U])` | Split an optional pair.      |
| `flatten()`                  | `Option[T]`      | Collapse `Option[Option[T]]`.        |
| `ok_or(e)`                   | `Result[T, E]`   | Promote `None` to an `Err(e)`.       |
| `ok_or_else(f)`              | `Result[T, E]`   | …computing the error lazily.         |
| `transpose()`                | `Result[Option[T], E]` | Swap with a nested `Result`.   |

### On `Result[T, E]`

| Method                       | Returns          | Meaning                              |
| ---------------------------- | ---------------- | ------------------------------------ |
| `is_ok()` / `is_err()`       | `boolean`        | Tag test.                            |
| `is_ok_and(pred)`            | `boolean`        | `Ok` *and* the payload satisfies `pred`. |
| `is_err_and(pred)`           | `boolean`        | `Err` *and* the error satisfies `pred`. |
| `unwrap_or(fallback)`        | `T`              | The value, or `fallback` if `Err`.   |
| `unwrap_or_else(f)`          | `T`              | …with access to the error value.     |
| `map(f)`                     | `Result[U, E]`   | Transform the `Ok` payload.          |
| `map_err(f)`                 | `Result[T, F]`   | Transform the `Err` payload.         |
| `map_or(fallback, f)`        | `U`              | Transform, or fall back to a value.  |
| `map_or_else(default_fn, f)` | `U`              | …deriving the fallback from the error. |
| `and_then(f)`                | `Result[U, E]`   | Chain another fallible computation.  |
| `and(other)`                 | `Result[U, E]`   | `other` if this is `Ok`.             |
| `or(other)`                  | `Result[T, E]`   | This, or `other` when `Err`.         |
| `or_else(f)`                 | `Result[T, F]`   | Recover, possibly changing the error type. |
| `flatten()`                  | `Result[T, E]`   | Collapse a nested `Result`.          |
| `ok()` / `err()`             | `Option[…]`      | Project to the success / error side. |
| `transpose()`                | `Option[Result[T, E]]` | Swap with a nested `Option`.   |

```fern
import "core/map";
import "std/option";

// Default a missing config value instead of branching.
var config: Map[string, i32] = Map { "workers": 4 };
var port: i32 = config.get("port").unwrap_or(8080);
```

## Choosing between them

- Reach for **`Option`** when absence is ordinary and there's nothing to
  explain about it.
- Reach for **`Result`** when the caller deserves to know *why* something
  failed — and put a descriptive struct in `E` rather than a bare string
  once the error has more than one shape.
- Convert between them with `Option.ok_or(e)` and `Result.ok()` when an
  API boundary calls for the other shape.
