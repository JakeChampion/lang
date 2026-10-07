---
title: Language features
description: Iteration, defer, let-else and if-let, match guards, value blocks, closures, the pipe operator, f-strings, use, modules, and must-consume types.
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
let trees: string[] = ["ash", "beech", "elm"];
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

The binder is a pattern — the same irrefutable tuple pattern `let (a, b) =
e;` takes, nested elements and `_` discards included — so a sequence of
tuples unpacks in the head rather than on the body's first line:

```fern
let readings: (string, i32)[] = [("ash", 3), ("elm", 7)];
for (species, count) in readings {
    print(f"{species}={count}");
}
```

Maps take the same header, binding each entry's key and value. Iteration
order is insertion order, and it's part of the contract rather than an
accident:

```fern
import "core/map";

let stock: Map[string, i32] = Map { "frond": 3, "spore": 7 };
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
import "std/option";

function first_line(path: string): Result[string, IoError] {
    let r: Reader = open_reader(path)?;
    defer r.close();          // runs on every exit path below
    return Ok(r.read_line().unwrap_or(""));
}
```

In a **loop body** the scope that schedules the cleanup is the iteration,
so each execution of the `defer` gets its own run at the end of *that*
iteration — a `break`, a `continue` and the body's tail all count as its
end. Nothing accumulates across iterations, and each run reads the values
its own iteration left:

```fern
for path in paths {
    let r: Reader = open_reader(path)?;
    defer r.close();          // closes this iteration's reader, before the next
    lines = lines.append(r.read_line().unwrap_or(""));
}
```

A `return` or a `?` that leaves the function from mid-iteration is an
exit for both scopes: the iteration's pending cleanup runs there, then the
function body's, LIFO. A deferred expression reads its names at the moment
it runs, not at the `defer`.

A loop iteration is the only scope narrower than a function that schedules
its own cleanup. A lambda body is a function, so a `defer` written inside
one runs when the lambda returns; a value-position `{ … }` block is not, so
a `defer` inside a block expression belongs to the function containing the
block and runs when that function returns.

`errdefer` is the same idea, but it runs **only on an error exit** — a
`?` that propagates an `Err`/`None`, or a `return` of an error value. Use
it to undo partial work that should survive the success path but be rolled
back on failure.

```fern
defer log("done");           // always
errdefer rollback();         // only if we bail with an error
```

On an error exit the plain defers run first, last-in first-out, and then
the errdefers, also last-in first-out — so above, `done` prints before
`rollback`. An `errdefer` in a loop body covers its own iteration only:
when the iteration ends normally, it is dropped without running.

`?` cannot appear directly inside a `defer` or `errdefer` action
(`E079`): the action runs on the function's exits, including the one `?`
would take.

## Refutable bindings — `let … else` and `if let`

A plain `let name = expr;` always binds. `let` can also bind a **pattern**
that might not match, and then forces you to handle the miss with a
diverging `else`:

```fern
let Some(user) = lookup(id) else {
    return http.not_found();
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
language gains reaches every binding site at once.

## Destructuring parameters

A parameter can be a pattern instead of a name, annotated with the type
it destructures:

```fern
function dist(Point { x, y }: Point): f64 { … }
function span((lo, hi): (i32, i32)): i32 { return hi - lo; }
function area(w @ Point { x, y }: Point): i32 { … }   // `w` is the whole value
```

`{ x: local }` renames a field and `{ x, .. }` documents the fields left
unbound, as in a match arm. A lambda takes the same grammar, whether its
body is a block or a bare expression:

```fern
let verbose = (Point { x, y }: Point): i32 => { return x + y; };
let arrow = (Point { x, y }: Point) => x + y;
```

A parameter binds unconditionally — there is no else branch to run on a
miss — so only patterns that always match are allowed. A refutable one
(an enum variant, a literal element) is a parse error; take the value
whole and `match` on it in the body.

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

A `match` also dispatches on literals: integers, characters, bytes,
strings, and ranges of signed integers (`1..=9 =>`). Several patterns can
share an arm with `|`. A string scrutinee always needs a final `_` arm,
since no list of literals covers every string.

```fern
function classify(n: i32): string {
    return match (n) {
        0 => "zero",
        1..=9 => "digit",
        -100..=-1 => "small negative",
        _ => "other",
    };
}

function answer(s: string): i32 {
    match (s) {
        "yes" | "y" => { return 1; },
        "no" => { return 0; },
        _ => { return -1; },
    }
}
```

## Value blocks and expression forms

`if`, `match` and a bare `{ … }` block can all produce a value. A block's
statements run first, and its last expression — written **without** a
trailing `;` — is the value:

```fern
import "std/i32";
import "std/string";

function main(): i32 {
    let x: i32 = { let a = 2; a * 3 };
    let y: i32 = if (x > 5) { let k = x + 1; k } else { 0 };
    let z: string = match (y) {
        7 => { let s = "seven"; s },
        _ => "other",
    };
    print(f"{x} {y} {z}");   // 6 7 seven
    return 0;
}
```

An `if` used as a value needs its `else`, and its branches are always
braced. A match arm's body may be a bare expression (`_ => "other"`) or a
block. Loops are statements only: `loop` produces no value and `break`
takes none, so a search loop assigns its result to a binding declared
before it.

## Closures

A lambda is a parenthesised parameter list, an optional return type, `=>`,
and either an expression or a braced body. Parameter types are required;
the return type is inferred when left out.

```fern
import "std/i32";

function make_adder(n: i32): (i32) => i32 {
    return (x: i32) => x + n;
}

function apply_twice(f: (i32) => i32, v: i32): i32 {
    return f(f(v));
}

function main(): i32 {
    let add3 = make_adder(3);
    print(add3(4).to_string());                                // 7
    print(apply_twice((x: i32): i32 => x * 2, 5).to_string()); // 20

    let count: i32 = 0;
    let bump = (): void => { count = count + 1; };
    bump();
    bump();
    print(count.to_string());                                  // 2
    return 0;
}
```

A lambda captures the variables it names **by reference**: it sees later
assignments to them, and assigning a captured scalar (`count` above)
updates the outer variable. A captured *reference-typed* variable — a
string, array, struct, enum, tuple or closure — is read-only inside the
lambda (`E049`), because writing one could close a reference cycle.
Return the new value instead, or keep the shared state in a
[`Cell`](../types/#shared-mutable-state--cellt).

A named function is a value too: pass `make_adder` where a
`(i32) => (i32) => i32` is expected. A function declared inside another
function's body is a closure in the same sense.

## The pipe operator — `|>`

`x |> f` is exactly `f(x)`, and `x |> f(a, b)` is `f(x, a, b)` — the left
value becomes the first argument. It reads left-to-right, so a chain of
transforms flows in the order it runs instead of nesting inside-out:

```fern
// These two are identical — the pipe form just reads forward.
let body: string = json.json_encode(describe(u.path, q));
let body: string = describe(u.path, q) |> json.json_encode;
```

When the value belongs somewhere other than the first argument, mark the
spot with `_`: `x |> f(a, _)` is `f(a, x)`. A call takes at most one `_`.

```fern
import "std/i32";

function sub(a: i32, b: i32): i32 { return a - b; }

function main(): i32 {
    print((10 |> sub(3)).to_string());      // sub(10, 3) = 7
    print((10 |> sub(3, _)).to_string());   // sub(3, 10) = -7
    return 0;
}
```

It's a parse-time desugar with no runtime cost.

## f-strings

A string literal with an `f` prefix interpolates `{expr}` holes. Each hole
is stringified — numbers go through `.to_string()` automatically.

```fern
let name: string = "world";
let n: i32 = 42;
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
let n: i32 = 0;
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

desugars at parse time to one lambda per `use`, each returning what the
enclosing function returns:

```fern
return maybe_double(start, (a: i32) => {
    return maybe_double(a, (b: i32) => {
        return Some(b + 1);
    });
});
```

Each `use` peels one level of nesting off what would otherwise be a
deeply-indented chain of closures — handy for sequencing fallible
`Option`/`Result`-returning steps. The binding's type is inferred from
the callee's callback parameter; write `use b: i32 <- …` to state it. The
callbacks are ordinary [closures](#closures), so they capture the
enclosing function's bindings the same way.

## Modules and visibility

Each `.fern` file is a module. `import "./geo";` loads `geo.fern` from
the importing file's directory, and its names are then written
`geo.name`; `import "./geo" as g;` picks a different qualifier. Standard
library modules import the same way (`import "std/string";`). A program
sees only what it imports — there is no prelude.

A top-level declaration is private to its module unless marked:

| Marker | Visible to |
| ------ | ---------- |
| *(none)* | its own module |
| `pub(package)` | modules in the same directory |
| `pub` | any module that imports it |

`pub use` re-exports another module's public names, so one module can
present a surface assembled from several:

```fern
// geo.fern
pub struct Point { x: i32, y: i32 }
pub function origin(): Point { return Point { x: 0, y: 0 }; }
```

```fern
// shapes.fern
import "./geo";
pub use "./geo".{ Point, origin };

pub function unit(): geo.Point { return geo.Point { x: 1, y: 1 }; }
```

```fern
// main.fern
import "./shapes";

function main(): i32 {
    let p: shapes.Point = shapes.origin();   // geo's Point and origin
    let q: shapes.Point = shapes.unit();
    return p.x + q.y - 1;
}
```

A re-exported name resolves to the original declaration; nothing is
copied. `pub use` does not bring the names into the re-exporting module's
own scope — `shapes.fern` above still imports `geo` to use them itself.
The [modules tutorial](../../tutorial/modules/) walks through imports
step by step, and [Packages](../packages/) covers dependencies.

## Values that must be used — `@must_consume`

A struct or enum marked `@must_consume` carries an obligation: every
value of it must be consumed on every path — passed to an `own`
parameter, returned, matched on, or stored inside another
`@must_consume` value. Letting one go out of scope unused is a compile
error (`E067`), which makes "respond exactly once" or "commit or roll
back" something the checker enforces.

```fern
@must_consume
struct Pending { id: i32 }

function finish(own p: Pending): i32 {
    return p.id;
}

function main(): i32 {
    let p: Pending = Pending { id: 3 };
    return finish(p) - 3;    // without this call: E067
}
```

An `own` parameter is the declared sink. A parameter without `own` passes
the obligation on, so the callee must discharge it in turn.

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
- [Syntax overview](../syntax/) — keywords, literals, precedence, block
  forms, functions, attributes.
- [Type system](../types/) — integers, strings, arrays, `Cell`.
- [Literate programming](../tooling/#literate-programming) — write
  programs as Markdown documents.
