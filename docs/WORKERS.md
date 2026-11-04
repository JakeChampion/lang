# Workers

Status: [reference] shipped: `std/worker` with `spawn` and `join`, the `send`
parameter mode, `E100`, and a process per worker on native Linux. Every
other engine runs a worker inline. Real threads are not shipped.

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
| a tuple of send-safe parts | `dyn Trait`: a dyn value hides its concrete type |
| a struct or enum whose fields and payloads are send-safe, with its type arguments applied | a view, `[T]` or `str`: a view borrows another value's storage |
| `Option[T]` and `Result[T, E]` of send-safe arguments | `Reader`, `Writer`, `Dir`, `Socket`: a built-in handle owns a descriptor |
| | a resource handle: it is not copied |
| | a `@must_consume` type: a copy would double its obligation. `Worker[T]` is one, so a worker cannot be sent |
| | `Map[K, V]`: a Map does not cross a worker boundary in this release |
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

## Step 2: a process per worker

The second step gives native Linux real parallelism. A worker is a
process, not a thread. `RC-DESIGN.md` section 2.1 already holds the
one-context-per-heap guarantee at the `proc_fork` and `subprocess`
boundaries with separate processes, and a worker takes the same row.

`spawn` maps a shared region and forks. The child adopts the region as its
heap: the cursor and the end move into the region, and both free lists are
cleared, so the child allocates only inside the region. The child runs the
body, copies the result into the region with `__send_copy`, writes the root
address at the start of the region, and exits. The parent keeps the child's
pid and the region.

`join` waits for the child, reads the root, and copies the result into its
own heap. The region is mapped at the same address in both processes, so
every pointer inside the child's copy is valid in the parent. An immortal
literal the copy names is in the parent's image at the same address too.
Then the parent releases the region's boxes between a heap mark and its
restore, which drops their free-list entries. The region goes back to the
kernel, and no box of it outlives the join.

The region is the child's whole heap: four gigabytes of address space that
the kernel backs page by page. A body that needs more aborts with the
out-of-memory trap, and the parent reports the exit.

The join needs `__send_release`. The planner releases a value at its last
use, and the adopted value's last use is the copy, which comes before the
mark.

The copy at the end of the body is what makes this sound. The child's heap
is a copy-on-write image of the parent's, so a box the body builds can point
at a box the parent owns. `__send_copy` builds a fresh tree. Every box of
that tree is inside the region, because the child's cursor is there and its
free lists are empty.

The module decides at `spawn`. A native Linux program forks. The
interpreters map no region, and wasm and Darwin keep the inline order of
step 1.

A fork or a mapping that the kernel refuses runs the body inline too, so the
result is the same on every path.

The leak census crosses with the result. The region's header holds its size
and the four leak-check counters: `__worker_region` writes the parent's snapshot, the
child writes its deltas at exit, and `__worker_reap` adds them to the
parent's counters. The child counts a free only inside the region, because
a box of the parent's image that the child frees is the parent's to count.
So a box the child allocated and the parent freed is paired, and a leak in
the body is reported at the parent's exit.

The builtins behind the step are `unsafe`, and only `std/worker` calls
them.

| Builtin | Role |
|---|---|
| `__worker_region(size)` | Maps a shared anonymous region that the fork inherits at the same address. The interpreters return zero. |
| `__heap_adopt(start, end)` | Moves the cursor and the end into the region and clears both free lists. A range outside the heap moves its base too. |
| `__send_root(v)` | Gives up `v` and returns the address of its root box. |
| `__send_release(v)` | Releases `v` where it is written, not at its last use. |
| `__send_adopt(addr)` | Takes the box at `addr` as a value of the declared type. |
| `__worker_exit(region, root)` | Stores the root in the region and ends the child at once, with no at-exit work. |
| `__worker_reap(region)` | Returns the root the child stored. |
| `__worker_unmap(region, size)` | Returns the region to the kernel. |

What this costs and what it buys is listed below.

- A fork costs more than a thread. The named workloads are CPU-bound jobs
  that run for longer than that.
- The allocator, the counts and every runtime helper stay as they are. No
  thread-local cursor, no second free list and no atomic count exists.
- The captures cross for free: the child reads them from its own image.
  Only the result is copied, and the copy is the same per-type helper a
  thread design needs at both boundaries.
- The thread design of `MULTICORE-RESEARCH.md` stays open. Its API is this
  API, and `__send_copy` and the region adoption carry over to it.

### `__send_copy`

`__send_copy(v)` is a builtin that returns a fresh deep copy of a send-safe
value. The lowering emits one helper per reference type the value holds,
named `__sem_send_` and the type's key, beside the release helpers.

A string copies its bytes. An array copies each element. A record, a tuple and an
enum variant allocate a fresh box and copy each field. An `Option` copies
its payload.

Both checkers refuse a value that is not send-safe with `E100`. The
interpreters copy every array store, so a builtin that writes through a
box cannot reach the original. A `Map` does not cross in this release.

The case is `conformance/cases/send_copy_deep`.

## What follows

The `std/sim` leg comes next: a seeded scheduler that picks the next worker
at each spawn and join, with the degrade and the simulation as one
mechanism. Then the SEC-020 teardown of a worker's heap, issue 13522. Then
bounded mailboxes, when a pipeline workload asks for them. Threads come
under the same API, when a workload shows the fork cost. wasm keeps the
degrade until a thread primitive ships that wasmtime implements (R5).

## Tests

- `conformance/cases/worker_spawn_join`: a struct, an array, a string and an
  `Option` cross the boundary and come back with the same contents, on every
  engine.
- `conformance/cases/diag_e100`, `conformance/cases/diag_e100_closure_value`,
  `conformance/cases/diag_e067_worker_unjoined`: the refusals.
- `conformance/cases/send_copy_deep`: `__send_copy` of a record, an array,
  a tuple, strings and both enum layouts prints what the original prints.
- `TestSelfHostSendCopyAllocatesFreshBoxes`: the copy of a nine-box value
  makes thirteen allocations, nine boxes and four freed temporaries, and a
  sanitized run frees them all, on both native targets and wasm.

- `conformance/cases/worker_runs_in_its_own_process`: a worker that
  allocates sixty-four megabytes does not grow the parent's arena, on
  x86-64 and arm64 Linux.
- `TestSelfHostHeapAdoptIR`: after `__heap_adopt` the heap mark lies inside
  the adopted region, on both native targets.
- `conformance/cases/worker_spawn_in_a_loop`: two hundred workers in
  turn, each joined, on every engine. On Linux each region goes back to the
  kernel at its join.
- `tests/stdlib/worker_test.fern`: the module through `std/test`.
- `TestSendParameterRefusesWhatCannotCrossAWorkerBoundary` and
  `TestSendParameterAdmitsSendSafeValues`: the Go checker, one row per reason.
- `TestSelfHostCheckerCodesX86_64`: the `send-` rows pin both checkers.
