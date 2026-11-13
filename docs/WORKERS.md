# Workers

Status: [reference] shipped: `std/worker` with `spawn`, `join` and `poll`, the
`send` parameter mode and `E100`. A worker is a process on native Linux,
Android and macOS, and `std/sim` implements the scheduler seam. Every other engine runs a worker
inline. Real threads are not shipped.

## The model

Share-nothing workers are the thread model. The owner decided it on
2026-10-29 for SEC-009 ([#13511](https://github.com/JakeChampion/lang/issues/13511)).
It is design (a) of `MULTICORE-RESEARCH.md`.

A worker owns its heap. A value crosses a worker boundary only by copy, at
`spawn`, at `send` and at `join`. Reference counts stay non-atomic. A
program that spawns nothing pays nothing.

Fern values are immutable. A copy of a `Cell`, a closure, a `dyn` value, a
view or a handle splits or forges what it names. The checker keeps those
values inside one worker. A closure shares each variable that it captures,
so the checker also refuses a write to a captured variable inside a closure
that crosses the boundary (`E100`). So no accepted program can tell a copied tree
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
| `(own w: Worker[T]) poll(): Polled[T]` | Does not wait. Returns `Finished` with the result of a finished worker, or `Running` with the worker, which the caller polls or joins later. An event loop, such as an Android app, polls on its timeouts. A worker on a scheduler is joined. |
| `@must_consume struct Worker[T]` | A program joins every worker it spawns (`E067`). No worker runs detached. |
| `spawn_on[T](s: dyn Scheduler, send body: () => T): Worker[T]` | Runs `body` as a worker of scheduler `s`. `std/sim` is a scheduler. |
| `trait Scheduler` | `admit(run)` starts a worker thunk and returns its index. `finish(index)` runs the workers until that one has its result. |

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
| a `Map[K, V]` of a send-safe key and value | a resource handle: it is not copied |
| a type parameter bounded by `worker.Send` | a `@must_consume` type: a copy would double its obligation. `Worker[T]` is one, so a worker cannot be sent |
| | a type parameter without that bound: it is not known to be send-safe |

The check runs once on the generic body, so a type parameter is send-safe
only when its bound says so. `worker.Send` is a marker trait with no
methods. A type satisfies it when every part of the type is send-safe, and
the call that binds the type parameter checks the bound (`E021`).

No impl declares `Send`: an `impl worker.Send for T` is refused (`E021`),
because an impl could name a type whose parts cannot be copied. `spawn`,
`spawn_on` and `join` carry the bound, so the module itself sends through it.

```fern
function twice[T: worker.Send](x: T): (T, T) {
    let w = worker.spawn((): T => x);
    return (x, w.join());
}
```

The rule is `send_guard_call` in `compiler/checker.fern` and `guardSendArg`
in `internal/check/checker/send.go`. The two checkers give one message. The
cases are `conformance/cases/diag_e100`,
`conformance/cases/diag_e100_closure_value`, `worker_send_bound`,
`diag_e021_send_bound` and `diag_e021_send_impl`, and the rows named `send-`
in `TestSelfHostCheckerCodesX86_64` pin both checkers to the same codes.

## Sequential degrade

On an engine without a process per worker, `spawn` runs the body before it
returns, and `join` returns the stored result. The interpreters and wasm do
the same.

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

`spawn` maps a shared region and forks. The child runs the body on its private
copy-on-write heap. A nested worker thus cannot change its parent's reference
counts or free lists. After the body returns, the child adopts the shared
region and clears its free lists. It copies the result into that region with
`__send_copy`, writes the root address at its start, and exits. The parent
keeps the child's pid and the region.

`join` waits for the child, reads the root, and copies the result into the
parent heap. It then removes the entire worker region with `munmap`.
The returned result has no pointer into the removed region.
Later joins and parent allocations cannot invalidate it.

The region holds only the result copy. It reserves four gigabytes of address
space that the kernel backs page by page. A result copy that needs more aborts
with the out-of-memory trap, and the parent reports the exit. The body uses
the ordinary private heap before this copy.

The copy at the end of the body is what makes this sound. The child's heap
is a copy-on-write image of the parent's, so a box the body builds can point
at a box the parent owns. `__send_copy` builds a fresh tree. Every box of
that tree is inside the region, because the child's cursor is there and its
free lists are empty.

The module decides at `spawn`. A native Linux, Android or macOS program
forks. The interpreters map no region, and wasm keeps the inline order of
step 1.

A worker ends when its parent ends, so no worker runs detached. Linux and
Android set `PR_SET_PDEATHSIG` to SIGTERM in the child. macOS has no such
signal. There the child forks a watchdog process, which waits with kqueue
for the exit of the parent or of the worker. If the parent exits first, or
is gone before the watch starts, the watchdog sends SIGTERM to the worker.
Then it exits.

When a process exits, the macOS kernel first moves its children to launchd
and then posts the exit event. A watch added after that event
does not fire. Thus, after it adds the watch, the watchdog reads the parent
of the worker. If that parent is not the process that made the worker, the
watchdog sends SIGTERM at once. A worker whose parent is launchd before the
link starts exits at once.

The macOS runtime makes its system calls directly, so the child calls no
function of libSystem.

An Android app is a library in a process that the Java runtime owns, with
threads of its own. The child is a copy of one thread only, and a lock that
another thread held at the fork stays held in the child. The fork is a raw
`clone`, and the child exits through `exit_group`, so neither runs the fork
or exit handlers of the C library.

Fern code makes its system calls directly. So a body that stays in Fern
code takes no such lock, and sockets and DNS through netd are Fern code. A
body must not call a C function through `@import`, because `malloc` and
`liblog` take locks.

A fork or a mapping that the kernel refuses runs the body inline too.
A refused fork releases its unused region before the inline fallback.
A failed worker releases its region before the parent reports the failure.

The leak census crosses with the result. The region's header holds its size
and the four leak-check counters: `__worker_region` writes the parent's snapshot, the
child writes its deltas at exit, and `__worker_reap` adds them to the
parent's counters. A refused fork imports no child counters.

The body runs on the private heap of the child, and the child counts each
free of the body. That includes a free of a box of the parent's image. After
the heap adoption, the child counts a free only inside the region.

After a successful `munmap`, the parent records the reclamation of the
remaining live child allocations. This bulk reclamation includes temporary
objects that the child retained until exit. The live count is signed. A body
that frees boxes of the parent's image makes it negative. The negative count
cancels those frees, because the parent counts its own release of each box.
A failed `munmap` changes no free counter and stops the program.

The builtins behind the step are `unsafe`, and only `std/worker` calls
them.

| Builtin | Role |
|---|---|
| `__worker_region(size)` | Maps a shared anonymous region that the fork inherits at the same address. The interpreters return zero. |
| `__heap_adopt(start, end)` | Moves the cursor and the end into the region and clears both free lists. A range outside the heap moves its base too, and keeps the old bounds as the parent's image. A raw free of a block in the image does nothing, because the kernel reclaims the image at exit. |
| `__send_root(v)` | Gives up `v` and returns the address of its root box. |
| `__send_release(v)` | Releases `v` where it is written, not at its last use. |
| `__send_adopt(addr)` | Takes the box at `addr` as a value of the declared type. |
| `__worker_exit(region, root)` | Stores the root in the region and ends the child at once, with no at-exit work. |
| `__worker_reap(region)` | Returns the root the child stored. |
| `__worker_unmap(region, size, reaped)` | Removes the region and returns the kernel status. A reaped region carries child counters that the parent already imported. |

What this costs and what it buys is listed below.

- A fork costs more than a thread. The named workloads are CPU-bound jobs
  that run for longer than that.
- Programs without workers retain their allocator behavior. Reference
  counts remain non-atomic. No worker shares a parent free list.
- The captures cross for free: the child reads them from its own image.
  The child copies the result into the shared region. The parent copies it
  into its heap before it removes that region. Both copies use the same
  per-type helper.
- The thread design of `MULTICORE-RESEARCH.md` stays open. Its API is this
  API, and `__send_copy` and the region adoption carry over to it.

The table gives the measured cost of a spawn and a join. The host was
x86-64 Linux 6.18 with a Xeon at 2.1 GHz, on 2026-11-04. The body of the
worker returns a number. The program built the parent heap first, then
spawned and joined 1,000 workers in a loop. Each row is the time of the
loop over the count.

The cost grows with the live heap of the parent, because the fork copies its
page tables. A thread pays neither cost.

| live parent heap | one spawn and join |
|---|--:|
| under 1 MiB | 0.7 ms |
| 64 MiB | 2.7 ms |
| 256 MiB | 6.4 ms |
| 1 GiB | 20 ms |

A workload shows the fork cost when its bodies run for less than these
times. A parent with a heap of hundreds of MiB shows it too.

### `__send_copy`

`__send_copy(v)` is a builtin that returns a fresh deep copy of a send-safe
value. The lowering emits one helper per reference type the value holds,
beside the release helpers. The helper that the builtin calls has the name
`__sem_send_` and the type's key.

A string copies its bytes. An array copies each element. A record, a tuple and an
enum variant allocate a fresh box and copy each field. An `Option` copies
its payload. A map reads its key and value columns, and a fresh map of the
same size takes a copy of each pair.

The copy keeps the sharing of the source. An array, a tuple, a record or an
enum variant that two parts of the value hold copies one time, and the two
parts of the copy hold that one box. The helpers keep a table from the
address of a source box to its copy for the time of one `__send_copy`. A
box with one owner has one path to it, so it does not go into the table. A
string and a map that two parts hold copy one time for each part.

Both checkers refuse a value that is not send-safe with `E100`. The
interpreters copy every array store, so a builtin that writes through a
box cannot reach the original.

The case is `conformance/cases/send_copy_deep`.

## Step 3: the scheduler seam and the simulation

`spawn_on(s, body)` runs a worker on a scheduler the program names. On an
engine without a process per worker, `spawn` runs it on the `Inline`
scheduler. The degrade and the simulation are one mechanism, as R2 of
`MULTICORE-RESEARCH.md` asks.

A scheduler holds workers of every result type, so a body reaches it as a
thunk of one type: `() => usize`. The thunk wraps the result in `Some`,
gives the box up with `__send_root` and returns its address. `join` runs
the scheduler until the worker has its result. Then it takes the address
back with `__send_adopt` as the declared type.

These are the two builtins the process path already uses at its boundary.
They are the only way a value crosses a scheduler.

```fern
pub trait Scheduler {
  function admit(self: Self, run: () => usize): i32;
  function finish(self: Self, index: i32): usize;
}
```

`Inline` runs each thunk at `admit` and keeps the roots. `sim.Sim`
implements the trait over `std/async` tasks:

- `admit` starts the body as a task at once, as a process reads its
  captures at the fork. A body that parks waits in a slot of the
  simulation. The slot holds plain words: the task id, the root of the
  thunk, and the wait. A `Cell` holds no closure (`E057`), so the thunk
  crosses into the slot the way a result crosses a worker boundary. At
  each spawn the seed then says whether a parked worker runs before the
  spawner goes on.

- `finish` resumes the parked workers in the order of their deadlines
  until the joined worker is done. The seed breaks a tie. Each turn moves
  the clock to the deadline. A worker that waits for an event the
  simulation never gives stops the program with exit 134 at its join.

A worker body captures only send-safe values, so it cannot hold the
simulation. It parks with `async.wait_any` on a virtual `Instant`, and the
spawner reads the clock. On the interpreter a task does not park, so a
body runs to its end at `spawn` and the clock does not move.

The `__send_root` and `__send_adopt` marks lower to IR ops that emit
nothing on every backend. The certifier counts the move and the fresh
unit. The interpreters keep a rooted value in a table and give it back at
the adopt, so the scheduler runs under `-interp` too.

## What follows

The SEC-020 teardown of a worker's heap, issue 13522, is next. Then
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
  makes nine boxes, four freed temporaries and the table of copied boxes,
  and a sanitized run frees them all, on both native targets and wasm.
- `TestSelfHostSendCopyKeepsSharedBoxes`: the copy of 79 boxes with 2^26
  paths makes one box for each box, on both native targets and wasm.

- `conformance/cases/worker_runs_in_its_own_process`: a worker that
  allocates sixty-four megabytes does not grow the parent's arena, on
  x86-64 and arm64 Linux.
- `TestSelfHostHeapAdoptIR`: after `__heap_adopt` the heap mark lies inside
  the adopted region, on both native targets.
- `conformance/cases/worker_spawn_in_a_loop`: two hundred workers in
  turn, each joined, on every engine. On Linux each region goes back to the
  kernel at its join.

- `tests/stdlib/worker_test.fern`: the module through `std/test`, `poll`
  included. A forked worker has not finished at the first poll, and an
  event loop holds a worker in its state across polls.
- `tests/android/worker`: on the emulator, the loop thread of a
  NativeActivity app polls two workers, and one of them resolves a name
  through netd.

- `conformance/cases/worker_runs_on_the_simulation`: three workers park on
  the virtual clock, their results cross back in join order, and the joins
  move the clock to the last deadline, on every engine.
- `tests/stdlib/sim_worker_test.fern`: a seed replays its schedule, seeds
  differ at the spawns, and the `Inline` scheduler runs a body at spawn.
- `TestSendParameterRefusesWhatCannotCrossAWorkerBoundary` and
  `TestSendParameterAdmitsSendSafeValues`: the Go checker, one row per reason.
- `TestSelfHostCheckerCodesX86_64`: the `send-` rows pin both checkers.
