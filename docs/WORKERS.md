# Workers

Status: [reference] shipped: `std/worker` with `spawn` and `join`, the `send`
parameter mode, and `E100`. Real threads are not shipped. Every engine runs
a worker inline.

## The model

Share-nothing workers are the thread model. The owner decided it on
2026-10-29 for SEC-009 ([#13511](https://github.com/JakeChampion/lang/issues/13511)).
It is design (a) of `MULTICORE-RESEARCH.md`.

A worker owns its heap. A value crosses a worker boundary only by copy, at
`spawn`, at `send` and at `join`. Reference counts stay non-atomic. A
program that spawns nothing pays nothing.

Fern values are immutable. A copy of a `Cell`, a closure, a `dyn` value, a
view or a handle splits or forges what it names. The checker keeps those
values inside one worker. So no accepted program can tell a copied tree
from a shared tree, and the copy is unobservable. A later release can move
a value in place of the copy under the same contract.

## The surface

```fern
import "std/worker";

function main(): i32 {
    let job: Job = read_job();
    let w: worker.Worker[Report] = worker.spawn((): Report => render(job));
    let local: i32 = do_other_work();
    let r: Report = w.join();
    return combine(local, r);
}
```

| Declaration | Rule |
|---|---|
| `spawn[T](send body: () => T): Worker[T]` | Runs `body` as a worker. The captures of `body` and its result cross the boundary, so both are send-safe (`E100`). |
| `(own w: Worker[T]) join(): T` | Returns the result and consumes the worker. A second `join` is `E051`. |
| `@must_consume struct Worker[T]` | A program joins every worker it spawns (`E067`). No worker runs detached. |

The source is `internal/stdlib/std/worker.fern.md`.

## The `send` mode

`send` is a parameter mode. It is written where `own` is written:

```fern
pub function spawn[T](send body: () => T): Worker[T]
```

A `send` parameter is an `own` parameter whose argument also crosses a
worker boundary. It is the point above `own` on the access axis of the mode
lattice (`MODE-LATTICE.md`), where Swift writes `sending`. Every `own` rule
applies to it, and one rule is added at each call:

1. A closure written at the call crosses with its captures. The type of
   every captured variable is send-safe, and the result type of the closure
   is send-safe.
2. Any other argument has a send-safe type. A function value is not
   send-safe, because its environment is not visible at the call. So a
   closure held in a variable cannot be passed.

A type is send-safe when every part of it is:

| Send-safe | Not send-safe, and why |
|---|---|
| a scalar, `boolean`, `string` | `Cell[T]`: a Cell is shared by aliasing |
| an owned array `T[]` of send-safe elements | a function or closure type: a function value carries its environment |
| a tuple or `Map[K, V]` of send-safe parts | `dyn Trait`: a dyn value hides its concrete type |
| a struct or enum whose fields and payloads are send-safe, with its type arguments applied | a view, `[T]` or `str`: a view borrows another value's storage |
| `Option[T]` and `Result[T, E]` of send-safe arguments | `Reader`, `Writer`, `Dir`, `Socket`: a built-in handle owns a descriptor |
| | a resource handle: it is not copied |
| | a `@must_consume` type: a copy would double its obligation. `Worker[T]` is one, so a worker cannot be sent |
| | a type parameter: it is not known to be send-safe |

The last row is the conservative choice of this release. A generic function
cannot send a value of its type parameter, because the check runs once on
the generic body.

The rule is `send_guard_call` in `compiler/checker.fern` and `guardSendArg`
in `internal/check/checker/send.go`. The two checkers give one message. The
cases are `conformance/cases/diag_e100` and
`conformance/cases/diag_e100_closure_value`, and the rows named `send-` in
`TestSelfHostCheckerCodesX86_64` pin both checkers to the same codes.

## Sequential degrade

In this release `spawn` runs the body before it returns, and `join` returns
the stored result. Every engine does the same: the interpreters, the native
targets and wasm.

The body runs at `spawn`, not at `join`, for one reason. A closure and the
scope around it share the cell of a captured scalar (`CLOSURE-CAPTURE.md`).
A body that runs at `join` reads a write made after `spawn`. A thread reads
its copy at `spawn`.

A body that runs at `spawn` reads each capture at the moment a thread copies
it. That order is one of the schedules a thread can produce: the worker runs
to completion before the spawner continues.

A program whose worker waits for the spawner mid-flight stops in this
release, and it stops on a thread too. There is no channel in this release.

## What follows

The order is R4 of `MULTICORE-RESEARCH.md`:

1. Real threads on x86-64 and arm64 Linux: `clone` and a futex, a heap
   reservation per worker, and a thread-local cursor and free list. Darwin
   goes through `pthread`. The API does not change.
2. The `std/sim` leg: a seeded scheduler that picks the next worker at each
   spawn and join. The degrade of this release and the simulation are one
   mechanism.
3. The SEC-020 teardown of a worker's heap ([#13522](https://github.com/JakeChampion/lang/issues/13522)).
4. Bounded mailboxes, when a pipeline workload asks for them.

wasm keeps the degrade until a thread primitive ships that wasmtime
implements (R5).

## Tests

- `conformance/cases/worker_spawn_join`: a struct, an array, a string and an
  `Option` cross the boundary and come back with the same contents, on every
  engine.
- `conformance/cases/diag_e100`, `conformance/cases/diag_e100_closure_value`,
  `conformance/cases/diag_e067_worker_unjoined`: the refusals.
- `tests/stdlib/worker_test.fern`: the module through `std/test`.
- `TestSendParameterRefusesWhatCannotCrossAWorkerBoundary` and
  `TestSendParameterAdmitsSendSafeValues`: the Go checker, one row per reason.
- `TestSelfHostCheckerCodesX86_64`: the `send-` rows pin both checkers.
