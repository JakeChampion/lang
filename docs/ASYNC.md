# Async & concurrency in Fern (user guide)

Fern's concurrency is **colorless**: there is no `async` function modifier and no
`Future`/`Promise` type in the surface. You write straight-line code; `await`
marks a suspension point, and `concurrent` / `race` blocks overlap work. Suspension
is a property of the *block*, not the function signature — so an ordinary function
can suspend without "infecting" its callers' types.

This is the user-facing guide. For the design and internals see
`docs/ASYNC-IMPLEMENTATION-PLAN.md`, `docs/STREAM-TYPE-SURFACE.md`, and
`docs/WASI-PREVIEW3-ASYNC-PLAN.md`.

> Status: the in-language scheduler (`concurrent`/`spawn`/`await`/`race`) runs on
> the `std/task` runtime; today its reactor delivers in-memory completion values
> (great for composing and testing task logic). Real outbound I/O (`plat.fetch`
> over the OS reactor) is the next milestone (Phase 4). The WASI Preview-3
> `stream[T]` surface below already does real wire I/O.

The patterns shown here are exercised by the test corpus under `examples/tests/`
(`async_task_fn_test.fern`, `async_concurrent_test.fern`) and the Go e2e suite;
a few snippets below combine features for illustration.

---

## 1. Streams: `stream[T]`

An async host import can return a `stream[T]` — a sequence delivered incrementally
over the wire. Under the colorless model the call site sees a plain value:

```fern
@import("wasi:http/types", "body-stream")
async function body(): stream[u8];
```

**Collect it eagerly** — the whole stream into a `u8[]`:

```fern
async function handle(): i32 {
    var bytes: u8[] = body();   // drains the stream to EOF, returns the array
    return bytes.len();
}
```

**Iterate it lazily** — one element at a time, off the wire, before EOF (O(1)
memory, no full buffer):

```fern
async function sum(): i32 {
    var total = 0;
    for x in body() {           // pulls + awaits one element per turn
        total = total + (x as i32);
    }
    return total;
}
```

Lazy `for x in stream` works for any scalar element type (`u8` / `i32` / `i64` /
`f64`, …). A `stream[T]` **parameter** is the mirror: you pass an eager `T[]` and
the wrapper streams its elements out over the wire.

---

## 2. `await` and task functions

Inside a task, `await EXPR` suspends until the awaited value is ready, then
resumes with it. A **task function** is just an ordinary function that uses
`await` — no special signature:

```fern
function fetch_and_add(simulated: i32, addend: i32): i32 {
    var x = await simulated;    // suspend; resume with the value
    return x + addend;
}
```

`await` works anywhere an expression does, and across control flow:

```fern
function process(items: i32[], bonus: i32): i32 {
    var acc = 0;
    for x in items {                 // loop with awaits in the body
        var b = await bonus;
        if (b < 0) { continue; }     // break / continue work
        acc = acc + x + b;
    }
    while (await more() != 0) { … }   // await in a loop condition
    if (acc > 100) { return acc; }    // early return before an await
    var last = await final_step();
    return acc + last;
}
```

You can mix several awaits (sequential), await in `if` branches that merge
afterward, and use `await` in sub-expressions (`acc + await x`) — the compiler
hoists and sequences them left-to-right.

Task functions are run by spawning them in a `concurrent` or `race` block (below);
they are not called directly like ordinary functions.

---

## 3. `concurrent { … }` — fan out and join

A `concurrent` block spawns tasks whose I/O **overlaps** on one thread, then joins
them. All `spawn`s start before the work runs, so two fetches happen concurrently
rather than one-after-another:

```fern
import "std/task";

function handle(): i32 {
    concurrent {
        var a = spawn fetch_and_add(10, 1);   // -> 11
        var b = spawn fetch_and_add(20, 2);   // -> 22
        return combine(await a, await b);     // join both, then combine
    }
}
```

- All `var NAME = spawn CALL;` bindings come first; ordinary join statements
  follow.
- `await a` reads a spawned task's result. (The bound name holds the same value,
  so `await` here is documentation of the join point.)
- The block is a structured-concurrency scope: the result names don't leak out.
- Requires `import "std/task";`.

---

## 4. `race { … }` — first to finish wins

A `race` block runs spawned tasks until the **first** reaches completion, returns
its result, and abandons the rest (happy-eyeballs / first-wins). It is an
*expression* yielding `(winnerIndex, result)`:

```fern
import "std/task";

function fastest(a: i32, b: i32): i32 {
    var (winner, value) = race {
        spawn fetch_and_add(a, 0);   // racer 0
        spawn fetch_and_add(b, 0);   // racer 1
    };
    // `winner` is the index of the task that finished first; `value` its result.
    return value;
}
```

(It's spelled `race`, not `select`, because `task.select` — the runtime function
it desugars onto — is already a name.)

---

## 5. How it works (one paragraph)

`concurrent` / `race` desugar at parse time onto the `std/task` runtime: each task
is a stackless state machine (`Step = Done(i32) | Wait(token, resume)`), and the
parser splits a task function's body at each `await` into continuation closures
that capture the live locals. A single-threaded readiness reactor multiplexes the
spawned tasks. No function coloring, no green-thread stacks, and no per-backend
codegen for the core mechanism — it rides closures, enums, and loops, which every
backend already lowers. See `docs/ASYNC-IMPLEMENTATION-PLAN.md`.

## Current limitations

- The task runtime is **i32-throughout** (task results and awaited values are
  `i32`); non-scalar task results are a future generalization.
- The in-language reactor currently delivers **in-memory** completion values; real
  outbound I/O (`plat.fetch`) lands in Phase 4. The `stream[T]` import surface
  already does real wire I/O.
- `concurrent` / `race` are parse-time desugars, so `fern -fmt` prints the expanded
  form rather than the block (consistent with the other parse-time desugars).
- Not yet supported inside an `await`-bearing loop: a nested `await`-bearing loop,
  and labeled `break`/`continue`.
