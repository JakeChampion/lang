---
title: Language features
description: The distinctive constructs — defer, let-else, the pipe operator, f-strings, loop, and use.
sidebar:
  order: 5
---

Beyond the basics covered in the [tutorial](../../tutorial/install/), Fern
has a handful of constructs worth knowing on their own. None are exotic,
but each removes a class of boilerplate.

## Deferred cleanup — `defer` and `errdefer`

`defer` schedules an expression to run when the enclosing function
returns, no matter how it returns. Multiple defers run in **last-in,
first-out** order — so cleanup unwinds in the reverse of acquisition.

```fern
function read(path: string): Result[string, IoError] {
    var r: Reader = open(path)?;
    defer r.close();          // runs on every exit path below
    return r.read_all();
}
```

`errdefer` is the same idea, but it runs **only on an error exit** — a
`?` that propagates an `Err`/`None`, or a `return` of an error value. Use
it to undo partial work that should survive the success path but be rolled
back on failure.

```fern
defer log("done");           // always
errdefer rollback();         // only if we bail with an error
```

## Refutable bindings — `let … else`

`var` binds an irrefutable value. `let` binds a **pattern** that might
not match, and forces you to handle the miss with a diverging `else`:

```fern
let Some(user) = lookup(id) else {
    return http.http_response_not_found();
};
// `user` is in scope for the rest of the block, unwrapped.
```

The `else` block must terminate the surrounding control flow
(`return`, `break`, `continue`, …) — the checker enforces it — so after a
`let … else` the binding is guaranteed present. Tuples destructure the
same way, and because their arity is static they need no `else`:

```fern
let (q, r) = divmod(17, 5);   // q = 3, r = 2
```

## The pipe operator — `|>`

`x |> f` is exactly `f(x)`, and `x |> f(a, b)` is `f(x, a, b)` — the left
value becomes the first argument. It reads left-to-right, so a chain of
transforms flows in the order it runs instead of nesting inside-out:

```fern
// These two are identical — the pipe form just reads forward.
var body: string = json.json_encode(describe(u.path, q));
var body: string = describe(u.path, q) |> json.json_encode;
```

It's a parse-time desugar with no runtime cost.

## f-strings

A string literal with an `f` prefix interpolates `{expr}` holes. Each hole
is stringified — numbers go through `.to_string()` automatically.

```fern
var name: string = "world";
var n: i32 = 42;
print(f"hello, {name} — the answer is {n}");
```

Use `{{` and `}}` for literal braces. f-strings only interpolate literal
templates; when the template itself is computed at runtime (a config
string, a locale table), reach for
[`format.format`](../../stdlib/format/) instead.

## `loop`

`loop { … }` is the canonical infinite loop — clearer intent than
`while (true)`. Exit it with `break` (or `return`):

```fern
var n: i32 = 0;
loop {
    n = n + 1;
    if (n * n > 100) { break; }
}
```

## Callback chains — `use`

`use` flattens right-leaning callback pyramids, Gleam-style. A function
whose **last parameter** is a callback can be called with `use`, and the
rest of the block becomes that callback's body:

```fern
// maybe_double(n, cb) calls cb(n + n). Written with `use`:
use a <- maybe_double(start);
use b <- maybe_double(a);
return Some(b + 1);
```

desugars at parse time to:

```fern
maybe_double(start, function (a: i32) {
    maybe_double(a, function (b: i32) {
        return Some(b + 1);
    });
});
```

Each `use` peels one level of nesting off what would otherwise be a
deeply-indented chain of closures — handy for sequencing fallible
`Option`/`Result`-returning steps. Where the callback target is
monomorphic, the compiler defunctionalises the closures away, so the
flattened form has no allocation overhead versus hand-written nesting.

## See also

- [Error handling](../error-handling/) — `Option`, `Result`, and `?`.
- [Traits](../traits/) — shared behaviour and bounded generics.
- [Syntax overview](../syntax/) — keywords, precedence, block forms.
- [Literate programming](../tooling/#literate-programming) — write
  programs as Markdown documents.
