---
title: Language features
description: The distinctive constructs — iteration, defer, let-else, match guards, the pipe operator, f-strings, and use.
sidebar:
  order: 5
---

Beyond the basics covered in the [tutorial](../../tutorial/install/), Fern
has a set of constructs worth knowing on their own — the iteration forms
you'll reach for daily, plus a handful that each remove a class of
boilerplate. None are exotic.

## Iteration — `for … in`

The three-part `for (init; cond; step)` loop exists, but it is rarely what
you want. `for x in …` walks an array, a slice, a range, or anything
implementing `Iterator`, and binds each element directly:

```fern
var trees: string[] = ["ash", "beech", "elm"];
for name in trees {
    print(name);
}
```

Ranges are `start..end` (exclusive) or `start..=end` (inclusive), and in a
foreach head they compile to a counted loop with no iterator allocated:

```fern
for i in 0..xs.len() { total = total + xs[i]; }
for n in 1..=100    { sum = sum + n; }
```

Maps destructure their entries in one step. Iteration order is insertion
order, and it's part of the contract rather than an accident:

```fern
import "core/map";

var stock: Map[string, i32] = Map { "frond": 3, "spore": 7 };
for (species, count) in stock {
    print(f"{species}={count}");
}
```

### Labelled loops

Prefix any loop with `label:` and a nested `break label` / `continue
label` targets it instead of the innermost loop — the usual alternative
is a flag variable threaded through both levels:

```fern
function find_pair(xs: i32[], target: i32): (i32, i32) {
    search: for i in 0..xs.len() {
        for j in (i + 1)..xs.len() {
            if (xs[i] + xs[j] == target) { return (i, j); }
            if (xs[j] > target) { continue search; }
        }
    }
    return (-1, -1);
}
```

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

## Refutable bindings — `let … else` and `if let`

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

When the miss is *not* an early exit — you want to do something else and
carry on — use `if let` instead. It's a one-arm `match`, and the binding
scopes to the `then` block only:

```fern
if let Some(cached) = lookup(id) {
    return cached;
} else {
    print("cache miss");
}
```

The `if let` head takes the **same pattern grammar as a `match` arm** —
it *is* a one-arm match after parsing — so every pattern form is
available:

```fern
if let Point { x, y } = origin { … }        // struct pattern
if let (a, b) = divmod(17, 5) { … }         // tuple pattern
if let whole @ Some(n) = lookup(id) { … }   // `@` binding
if let Ok(Some(n)) = parse(s) { … }         // nested pattern
if let Red(n) | Blue(n) = c { … }           // or-pattern
if let 10..=20 = score { … }                // range pattern
```

An irrefutable head (a struct pattern, an all-binder tuple pattern)
is accepted — the `else` is simply dead — so `if let` doubles as a
destructuring form when you don't need the miss branch.

`let … else` reads the same grammar, so the refutable forms carry over
there too:

```fern
let whole @ Some(n) = lookup(id) else { return 0; };
let Ok(Some(n)) = parse(s) else { return 0; };
let Red(n) | Blue(n) = c else { return 0; };
```

Both forms are one `match` after parsing — `if let`'s success arm is the
then-block, `let … else`'s is the rest of the enclosing block, which is
exactly why its bindings stay live there. So exhaustiveness and
refutability are decided in a single place, and any pattern form the
language gains reaches all three binding sites at once.

## Match guards — `when`

A `match` arm can carry a `when` condition. The arm matches only if the
pattern fits *and* the guard holds, so several arms can share one variant
without nesting an `if` inside each body:

```fern
match (reading) {
    Temp(c) when c > 30 => { return "hot"; },
    Temp(c) when c < 0  => { return "freezing"; },
    Temp(_)             => { return "mild"; },
    Offline             => { return "no reading"; },
}
```

Guarded arms don't count toward exhaustiveness — a guard can always fail,
so a variant covered *only* by guarded arms still needs an unguarded
fallback like the `Temp(_)` above.

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

## Assertions and stubs — `assert` and `todo`

Both are recognised in statement position only, so neither name is
reserved anywhere else.

`assert(cond)` — optionally `assert(cond, msg)` — prints `assertion
failed: msg` to stderr and exits `1`. It desugars to a plain `if` plus
`eprint` plus `exit`, so it needs no import and costs nothing beyond the
branch:

```fern
assert(xs.len() > 0, "caller must supply at least one path");
```

`todo` marks a hole. `todo;` or `todo("msg")` prints `todo: msg` and
exits `101` — a distinct code, so an unimplemented path is
distinguishable from a failed assertion in a CI log. It counts as
diverging, which means it can stand in for the whole body of a function
that owes a return value:

```fern
function render(width: i32): string {
    todo("wire up the renderer");
}
```

That compiles, and the checker won't ask for the missing `return` — so
you can sketch a module's shape and fill it in afterwards.

## See also

- [Error handling](../error-handling/) — `Option`, `Result`, and `?`.
- [Traits](../traits/) — shared behaviour and bounded generics.
- [Syntax overview](../syntax/) — keywords, precedence, block forms.
- [Literate programming](../tooling/#literate-programming) — write
  programs as Markdown documents.
