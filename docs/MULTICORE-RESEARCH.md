# Multicore research — share-nothing workers with per-worker heaps

Status: research survey (2026-07). Tracking issue: #5366, filed from
`PLT-LANDSCAPE-2026.md` §2.8 ("the one frontier with no Fern story at
all"). Nothing in this doc is a build item. It exists so that stdlib
and platform decisions stop accreting against an *implicit*
single-thread assumption: the doc names the intended parallelism
shape, the constraints it must satisfy, and the guardrails that keep
today's work compatible with it.

The question, precisely: **what should Fern's eventual parallelism
story be**, given (a) the hard constraint that Perceus refcounts are
non-atomic and must never be touched from two threads, and (b) the
general-purpose direction (`CLAUDE.md ▸ Language direction`) that
rules out "edge handlers never need multicore" as an answer?

Scope split, so terms stay sharp:

- **Concurrency** (overlapping I/O waits on one thread) is solved
  and out of scope here: the colorless `Future[T]` combinators
  (`docs/ASYNC.md`, `std/async`) plus the `concurrent{}`
  recommendation of `CONCURRENCY-RESEARCH.md` Rec §1. Nothing below
  replaces them.
- **Parallelism** (two cores executing Fern code simultaneously in
  one logical program) is what Fern lacks entirely and what this doc
  surveys.

External claims were re-verified by web in July 2026 rather than
recalled; each deep dive notes what was checked. Anything that could
not be verified through the network proxy is marked as such.

## Sources at a glance

| Source | Core mechanism | Verified status (2026-07) | Verdict for Fern |
|---|---|---|---|
| Erlang/BEAM | per-process heaps, copying sends, reduction-preemptive schedulers | OTP 28 current major (May 2025) | the semantic model transfers; the VM machinery does not |
| Inko | single ownership, `uni T` via `recover`, moves not copies | 0.18.1 (2025-02-12) verified; 0.20.0 news post seen in search index (site 403s via proxy) | closest sibling; its heap-arrangement *evolution* is the key lesson |
| OCaml 5 | one shared multicore-safe GC, domains + effects | 5.4 (Oct 2025); still closing the 4.14→5.x sequential gap | the cautionary tale per-worker heaps exist to avoid |
| Verona | regions + behaviour-oriented `when` | language repo dormant (last commit 2025-07-08, docs-only); runtime repo research-active; FAQ: "not ready for use outside research" | mine region-transfer ideas; do not wait for it |
| Pony | ORCA distributed RC across actor heaps, ref caps | ponyc 0.67.0 (2026-07-11) — alive | proof cross-heap refs work without atomic RC; complexity says no |
| Swift 6 | `Sendable` + region-based isolation (SE-0414) + `sending` (SE-0430) | both shipped in Swift 6.0 | the mainstream vindication of statically-checked transfer; maps onto `own`/#5365 |
| wasm threads | shared-everything-threads; wasi-threads; WASI 0.3 async | phase 1 / legacy-superseded / released 2026-06-11 (async ≠ parallelism) | no wasm parallelism story exists to target; defer, degrade sequentially |
| Go | goroutines + channels on one shared heap; errgroup | background knowledge (stable, not re-verified) | surface ergonomics inform; the shared-heap model is exactly what the RC constraint forbids |

## Per-source analysis

### Erlang/BEAM — the reference share-nothing design

What they do. Every process owns a private heap (a few hundred
bytes at spawn, grown independently). `!` (send) **copies** the
message into the receiver's heap; the one exception is large
binaries (>64 bytes), which live on a shared off-heap area with
*atomic* refcounts. Schedulers (one OS thread per core) preempt
processes by reduction counting — roughly a function-call budget
per slice; OTP 28 (verified current: the OTP 28 highlights post,
May 2025) still tunes what bumps the counter (e.g. big-integer
arithmetic now charges reductions). Process death frees the whole
heap in O(1), which is what makes "let it crash" cheap.

What transfers to a compiled RC language:

- **Copy-at-send keeps every free local.** Because a message is
  copied into the receiver's heap, every allocation is only ever
  freed by its owning scheduler — no cross-thread frees, no atomic
  anything in the allocator. This is the precise property Fern's
  non-atomic RC and non-atomic freelist need, and it falls out of
  copying rather than being engineered.
- **Heap-per-process makes teardown O(1) and leak-proof.** A worker
  that exits can have its heap unmapped wholesale. For Fern this
  interacts nicely with the accepted cycle-leak posture
  (`ARENA-DECISION.md`): a cycle leaked *inside a worker* dies with
  the worker, restoring some of what the arena reset used to give.
- **The shared-binary carve-out is a warning, not a feature.** Even
  Erlang could not resist a shared refcounted case, and refc-binary
  leaks are a well-known BEAM production failure class. Fern should
  not replicate the carve-out (see non-recommendations).

What depends on the VM and does not transfer: reduction-based
preemption needs interpreter-inserted safepoints; AOT Fern has no
scheduler to yield to. A Fern worker is an OS thread, scheduled by
the kernel — a CPU-bound worker simply runs. Also out: hot code
swap, supervisor trees, the mailbox-scanning `receive` semantics.

### Inko — the closest modern sibling

What they do. Inko is "share-nothing concurrency on single
ownership without a borrow checker": lightweight processes, and the
compiler only lets a value cross a process boundary if it is a
**value type** (copied) or **unique** (`uni T`) — produced by a
`recover` expression whose body may not reference any outer
variable, so the resulting object graph provably has no external
aliases. Sending a `uni T` is a **move**: no copy, no race,
enforced entirely in the type system. (Verified via the Inko
manual/`docs.inko-lang.org` excerpts and Dusty Phillips' 2023
write-up; inko-lang.org itself 403s through the proxy. Release
state: 0.18.1 on 2025-02-12 verified; a 0.20.0 announcement exists
in the search index but its date could not be fetched.)

The key historical lesson is Inko's *evolution* (Yorick Peterse,
"Friendship ended with the garbage collector"): Inko **used to** be
Erlang-shaped — physical per-process heaps, deep-copying sends —
and moved to single ownership precisely so it could stop copying:
with ownership proved statically, the physical heap arrangement
became an implementation detail (per-OS-thread allocation caches
over a thread-safe allocator), and sends became pointer moves.

What transfers: the two-step ladder. Step one (copy at the
boundary) needs no type-system work at all and keeps the allocator
thread-confined. Step two (move at the boundary) is purely an
optimisation *if the boundary contract is ownership-shaped from day
one* — which is exactly what `own` parameters (E050/E051) and the
#5365 mode lattice give Fern. Inko's `recover` is the same judgment
Swift's SE-0414 regions compute: "this graph has no external
aliases." What does not transfer: Inko's runtime scheduler
(lightweight processes, M:N), and its reliance on a thread-safe
system allocator — Fern's bump-plus-freelist is deliberately not
thread-safe, which is why step two is *later* (see design space).

### OCaml 5 — the cautionary tale

What they do. OCaml 5 kept one shared heap and made the runtime
multicore-safe: a new minor/major GC with stop-the-world sections,
`Domain` as the unit of parallelism (intended ≤ #cores), effects
for intra-domain concurrency, Domainslib for work-stealing task
pools. What it cost (verified): the multicore effort ran the better
part of a decade ("Retrofitting Parallelism onto OCaml", ICFP 2020,
through the 5.0 release in December 2022), and the *sequential*
performance gap against 4.14 was still being closed in 5.4
(October 2025 — Tarides' release notes list GC pacing and
mark-delay work explicitly to "reduce the performance gap between
versions 4.14 and 5.x", three years post-release). Every program,
parallel or not, runs on the multicore runtime and pays its costs.

Why this is the anti-model for Fern: OCaml had a mature ecosystem
that demanded shared-memory compatibility; Fern has no such debt.
The whole OCaml bill — runtime rework, sequential regression,
memory-model specification for racy programs — is the price of a
*shared* heap. Per-worker heaps make the single-threaded runtime
the only runtime; a program that spawns no worker executes
byte-identical code to today. The one OCaml idea worth keeping:
domains-as-capability ("you get at most #cores of them") is a saner
default than unbounded green threads.

### Verona — regions and behaviour-oriented concurrency

What they do. Microsoft Research's Verona explores *concurrent
ownership*: heap objects live in **regions** with a single dominating
entry point; a `when (a, b) { ... }` behaviour declares which
regions (cowns) it needs and the runtime schedules behaviours so no
two run concurrently on the same region — data-race freedom by
scheduling over ownership rather than by type-checked `Sendable`.

Status (verified 2026-07): the main `microsoft/verona` language
repo's last commit was 2025-07-08 and is documentation-only; the
FAQ states the project "is not ready to be used outside of
research" and is being heavily refactored; the runtime split out to
`microsoft/verona-rt`, which remains research-active. Treat the
language as dormant for adoption purposes.

What transfers: the **region-transfer idea** is the correct
eventual shape for zero-copy sends in Fern — if a worker's heap (or
a sub-heap) is a region with a proven single entry point, handing
the whole region to another worker is O(1) and keeps RC non-atomic
because ownership is exclusive before and after. This is the
formal backing for "copy now, transfer later." What doesn't: the
behaviour scheduler (a runtime at odds with AOT cold start), and
`when`-style implicit ordering — Fern's explicit spawn/join +
structured combinators are the declared surface posture.

### Pony — ORCA, or cross-heap references without atomic RC

What they do. Pony actors each own a heap, but messages pass
*references*, not copies. Soundness comes from reference
capabilities (`iso`/`val`/`tag`…) making cross-actor mutation
unrepresentable; reclamation comes from **ORCA**: a deferred,
distributed, weighted reference-counting *protocol* in which actors
maintain local RC tables for foreign objects and reconcile by
sending inc/dec protocol messages through the same mailboxes as
user messages. No stop-the-world, no read/write barriers, no atomic
RC — the counts are only ever touched by their owning actor.
(Project alive: ponyc 0.67.0 released 2026-07-11, verified.)

What ORCA teaches: atomic RC is *not* forced by cross-heap
references; message-mediated ownership accounting can replace it.
What it costs: a capability lattice woven through the entire type
system (Pony's infamous learning cliff), GC protocol messages as a
permanent runtime tax and debugging surface, and correctness
arguments subtle enough to have produced multiple published proofs.
That is a research-budget line item Fern does not need to buy,
because Fern's values are already immutable and cycle-free — deep
copy is *semantically invisible* here (see design space (a)), so
the entire problem ORCA solves (sharing mutable graphs) does not
arise. Explicitly not recommended (see non-recommendations).

### Swift 6 — the mainstream take on statically-checked transfer

What they do. Swift's actors isolate state; `Sendable` marks types
safe to cross isolation boundaries. Two accepted proposals (both
verified shipped in Swift 6.0): **SE-0414 region-based isolation**
— a flow-sensitive analysis that partitions values into *isolation
regions* and allows a non-`Sendable` value to cross a boundary when
its whole region provably goes with it — and **SE-0430 `sending`**
— a parameter/result annotation meaning "arrives disconnected from
the caller's regions; the callee may transfer it onward." Swift
6.2's "approachable concurrency" work continues loosening
annotation burden (not examined in detail here).

Why this maps directly onto Fern: `sending` *is* `own` with a
disjointness proof attached, and SE-0414's region judgment is the
same "no external aliases" fact as Inko's `recover`. The #5365 mode
lattice (borrowed ≤ owned ≤ unique) is the natural home: a
`Worker.spawn`/`send` boundary demands the top of the lattice
(unique / disconnected), and the checker work is an
intra-procedural flow analysis, not a borrow checker. Swift is the
field evidence that this checking style scales to a mainstream
user base — and its evolution notes (years of `Sendable` friction
before SE-0414 relaxed it) argue for starting Fern at the
*permissive-by-copying* end rather than the annotate-everything end.
What doesn't transfer: the actor runtime, executors, and the
baseline cost Swift pays everywhere — ARC with atomic refcounts —
which is precisely the tax Fern's constraint forbids.

### wasm threads + the edge platforms — the target that isn't there

Verified state, July 2026:

- The original **threads** proposal (shared linear memory +
  atomics, no spawn) is phase 4.
- **shared-everything-threads** — shared GC objects, shared
  functions/globals/tables, thread-local storage, a real spawn
  story — is **phase 1** in the WebAssembly proposals list, under
  active churn ("frequent and significant changes expected").
- **wasi-threads** is officially legacy: its README says it is "a
  legacy proposal, retained for engines that can only support WASI
  v0.1," with future work redirected to shared-everything-threads.
- **WASI 0.3.0** shipped 2026-06-11 (Bytecode Alliance
  announcement; wasmtime 43+) with *native async* — `async func`,
  `stream<T>`, `future<T>` in the component model. This is
  concurrency plumbing, not parallelism: it is the substrate for
  Fern's existing combinator lane (#4315–#4320), not for workers.
- The edge hosts do not expose threads regardless: Cloudflare
  Workers documents that a request executes in a single-threaded V8
  isolate with no worker-threads API and no shared memory; Fastly
  Compute instantiates a fresh sandbox per request (~35 µs) —
  parallelism at those platforms is *between* instances, owned by
  the host.

Consequence: Fern's wasm backend has nothing to compile a worker
*to*, and its flagship wasm deployment targets would not run one
anyway. That is not a blocker for the design — it is a strong hint
that the worker API must **degrade to sequential execution with
identical semantics** (which share-nothing gives for free; see
design space (a)) and that the wasm lowering waits for a trigger
(R5).

### Go — the ergonomic baseline on the wrong memory model

Background knowledge (stable; not re-verified): goroutines are
cheap M:N-scheduled stackful coroutines over **one shared heap**;
channels + `select` are the communication idiom;
`golang.org/x/sync/errgroup` retrofits the structured join —
`g.Go(f)` … `g.Wait()` returns the first error and (with context)
cancels siblings.

What transfers is surface, not substance. `errgroup` is the shape a
Fern worker-join API should echo: spawn against a group/scope,
collect `Result`s at a single join point, first-error semantics
composing with `?`. Channels inform the eventual mailbox surface
(bounded, blocking sends as backpressure — same conclusion as
`CONCURRENCY-RESEARCH.md` Rec §6). What is disqualified by
construction: the shared heap. Every Go value can be aliased from
any goroutine; safety is by convention plus a race detector. Under
Perceus that model would force atomic RC on every inc/dec —
constraint C1 below — so Go's *memory* model is the explicit
non-option, however good its ergonomics.

## The Fern constraint set, stated precisely

These are the facts any parallelism design must satisfy. C1–C3 are
runtime invariants; C4–C6 are language-surface invariants; C7–C8
are directional.

**C1 — refcounts are non-atomic, permanently.**
`RC-PERCEUS-PLAN.md ▸ Non-goals` states it: "Refcounts are
non-atomic. Single-threaded. When concurrency lands, either atomic
ops (slower) or thread-local heaps with explicit sharing." Perceus
inserts inc/dec pervasively — often inlined (`rcInlineOK`) — so an
atomic upgrade taxes *every* program, parallel or not, in the
hottest paths the compiler generates. The resolution is the second
branch: thread-local heaps. Corollary: **no object may ever be
reachable from two threads**, even for reading, because a read-only
alias still incs and decs.

**C2 — the allocator is a process-global, non-thread-safe bump +
freelist.** One `__fern_heap_ptr`/`__fern_heap_end` cursor pair
(the two-cursor design was collapsed — `ARENA-DECISION.md`), a
fixed 8 GiB reservation on native, freelist tiers for reuse.
Per-worker heaps therefore mean: per-worker cursor + freelist
(thread-local, e.g. TPIDR_EL0 / %fs-relative or a reserved
register), per-worker mmap reservations (cheap in 64-bit address
space; impossible on wasm32 — another reason wasm defers), and the
rule that **a worker only frees memory it allocated** — which is
what copy-at-boundary guarantees (design space (a)).

**C3 — cycles are unconstructible, and that must survive workers.**
E048 (no struct-field assignment) + E049 (no reference-capture
write-back) close the cycle vectors so RC needs no cycle collector
(`RC-PERCEUS-PLAN.md`, `CELL-TYPE-PLAN.md`). Nothing about message
passing may reopen a vector (e.g. a mailbox must not be a value a
message can contain, or two workers' mailboxes could form a
cross-heap cycle). Positive interaction: a per-worker heap can be
**bulk-unmapped at worker exit** after the join-result is moved
out, which even reclaims anything a leak-class bug retained —
partial restoration of the arena's old safety valve.

**C4 — `Cell[T]` is worker-local by construction.** `Cell` (scalar
+ `string` payloads only) is the language's sanctioned shared
mutable state — shared by *aliasing*, mutated in place, refcounted
non-atomically (`CELL-TYPE-PLAN.md`). A cell reachable from two
workers is a data race on the payload and on the rc word. So the
send-safety rule must classify `Cell[T]` as not sendable, full
stop. There is no "shared atomic cell" planned (see
non-recommendations); cross-worker state is messages.

**C5 — closure captures share pointers, so closures do not cross
workers freely.** Per `CLOSURE-CAPTURE.md`: scalar captures copy,
reference captures copy the *pointer* and share the pointee with
the enclosing scope. A closure that crossed a worker boundary would
carry live aliases into the spawning worker's heap — violating C1.
Therefore: the *spawn* closure is special-cased (its captures are
checked send-safe and are copied/moved at spawn, exactly like
message fields), and closures **inside** messages are simply not
sendable in v1 (their environments are opaque pointer graphs;
classifying them is Inko-`recover`-grade analysis, deferred to the
mode lattice era).

**C6 — the #5365 mode lattice is the transfer-checking substrate.**
Fern already has four ownership surfaces — `own` consuming params
(E050/E051), `fip` (E053), owned `T[]` vs view `[T]` (E063),
`@must_consume` — that #5365 will unify into one lattice
(borrowed ≤ owned ≤ unique). "Sendable" should be *derived from*
that lattice (send requires owned-and-disjoint, i.e. the Swift
`sending` / Inko `uni` point), not bolted on as a fifth ad-hoc
analysis. Sequencing consequence: the checker half of workers
naturally lands **after** the #5365 write-up, which itself rides
the goal-2 Perceus port.

**C7 — the colorless intra-worker async story is untouched.**
`std/async`'s `Future[T]` + `gather`/`race`/`with_deadline` over
the `Driver` seam (`ASYNC.md` §6, `DST-PLATFORM-BRIEF.md`) is the
concurrency surface, and it stays single-threaded *per worker*.
Workers do not replace it and must not fork it: a worker body that
wants overlapped I/O uses the same combinators on its own reactor
(the `poll` builtin is already thread-safe by nature — each worker
polls its own fd set). No second async model, no coloring.

**C8 — process-level parallelism already exists.** The
`subprocess` builtin (checker `FuncSigs["subprocess"]`, interp
builtin, native + partial-wasm lowering) gives
fork/exec-and-collect today. Serialization is manual (strings/argv)
and spawn cost is a process, but it is the working baseline the
worker design must beat, not a gap.

## Design space

### (a) Share-nothing workers, per-worker heaps, copy-or-transfer messages — the lead candidate

The #5366 shape, and the one every constraint points at. One
worker = one OS thread + one heap (own cursor, own freelist, own
reactor). Values cross only at three checked boundaries — spawn
captures, sent messages, join results — and in v1 every crossing is
a **deep copy into the receiving worker's heap**.

The load-bearing observation: because Fern values are immutable and
`Cell`/closures are excluded from sendability, **deep copy is
semantically unobservable** — a copied tree and a moved tree cannot
be told apart by any legal program. So the API contract can demand
`own` at every boundary from day one, v1 can implement it as copy
(zero type-system prerequisites beyond "the type is send-safe"),
and a later version can implement the same contract as a move
(when the #5365 lattice can prove disjointness, or when worker
sub-heaps become Verona-style transferable regions) as a pure
optimisation. Copy also keeps C2 airtight: every object is
allocated, inc'd, dec'd, and freed by exactly one thread, so the
allocator and RC stay byte-identical to today.

**Surface sketch** (illustrative, not a commitment):

```fern
import "std/worker";

function render(own job: Job): Report { ... }   // ordinary code

function main(): i32 {
    var w: worker.Worker[Report] = worker.spawn(own function (): Report {
        return render(job);        // captures checked send-safe, copied in
    });
    var local = do_other_work();   // overlaps with w, incl. async.gather
    var r: Report = w.join()?;     // own result, copied into this heap
    return combine(local, r);
}
```

with a deferred mailbox extension shaped like a bounded channel
(`worker.Mailbox[M]`, `send(own m: M)`, blocking = backpressure),
and an `errgroup`-flavoured `worker.Group` for fan-out/join-all
with first-error semantics composing with `?`.

**What the checker must enforce** (all derivable from C4–C6):

1. `spawn` takes an `own` closure; each reference capture's type
   must be send-safe and is copied (v1) at spawn.
2. send-safe(T): scalars; `string`; and structs / enums / arrays /
   tuples / maps whose components are send-safe. **Not** send-safe:
   `Cell[_]`, function/closure types, `dyn` trait objects, view
   slices `[T]` (views borrow someone else's buffer by definition),
   and `Worker`/`Mailbox` handles themselves (C3's cycle guard).
3. Messages and join results are `own` at the boundary (consumed on
   send — forward-compatible with move semantics, and it keeps the
   cost model honest even under copy).
4. Join is mandatory: a `Worker[T]` is `@must_consume`, so no
   detached workers — the structured-concurrency stance of
   `CONCURRENCY-RESEARCH.md` carried over to parallelism.

**What each backend runtime actually needs** (the full bill —
deliberately small):

- **x86-64 / arm64 Linux:** thread create/join via `clone`
  (`CLONE_VM|CLONE_THREAD|…`) with an mmap'd stack, or libc
  `pthread_create` since binaries already link through gcc/clang;
  per-worker heap = one mmap reservation + thread-local
  cursor/freelist (%fs / TPIDR_EL0); mailbox/join = futex-backed
  mutex + condvar (or a Dekker-free SPSC ring for the v1
  spawn/join-only shape, which needs just the futex wait/wake
  pair). No changes to codegen'd user code at all — that is the
  entire point.
- **arm64-darwin:** no stable raw-syscall thread ABI on Darwin;
  must go through libSystem (`pthread_create`/`pthread_join`,
  `os_unfair_lock` or pthread mutexes). The toolchain already
  links libSystem via clang/ld64, so this is glue, not a new
  dependency.
- **wasm (core + preview1, and the component-model lane):** no
  thread primitive exists to lower to (verified above; wasi-threads
  legacy, shared-everything-threads phase 1, WASI 0.3 async is not
  parallelism). Lowering: `spawn` captures the closure, `join`
  runs it to completion inline — *sequential degrade*. Because
  share-nothing programs have no cross-worker observable
  interleaving, this preserves semantics exactly (v1's spawn/join
  + one-way feeds; bidirectional mid-flight protocols would
  deadlock sequentially, which is one more reason v1 excludes
  them). wasm32's 4 GiB address space also cannot host N × 8 GiB
  heap reservations — same deferral.
- **interp:** run the closure at join (same sequential degrade), or
  trivially on goroutines later (the interpreter's values live on
  the Go GC heap, so C1/C2 do not bind there).

**DST story.** A `Worker` in `std/sim` is a *deterministic
interleaving*: the sim scheduler owns a run queue of worker
continuations and picks the next one by seeded PRNG at every
cross-worker event (spawn, send, join) and at every `Driver` seam
call inside a worker body (`poll_ready`/`now_ns`). All the
machinery already exists in shape — `DST-PLATFORM-BRIEF.md`'s
virtual clock + token table extends to worker turns without new
concepts. This must be a design input, not a retrofit: every
cross-worker runtime event needs a driver-shaped seam (R2).

### (b) Pony/ORCA-style cross-heap references — studied, rejected

Keep per-actor heaps but send pointers, with a distributed
message-mediated RC protocol for reclamation and a capability
lattice for race freedom. It genuinely avoids atomic RC — the
existence proof matters — but it buys zero-copy for *mutable
shared* graphs, a problem Fern does not have (immutability makes
copy unobservable, (a) above), at the price of a pervasive
capability system, protocol traffic on every cross-heap reference's
lifecycle, and a soundness argument that took the Pony authors
multiple papers. Wrong complexity budget. If zero-copy ever
matters, the cheaper route is region *transfer* (Verona-shaped,
whole-heap ownership handoff), not cross-heap reference sharing.

### (c) Shared heap + atomic RC — rejected on the constraint itself

The OCaml/Swift route: one heap, make the runtime safe. For Fern
this means every Perceus-inserted inc/dec — including the inlined
fast paths — becomes an atomic RMW, paying contention and fence
costs in every program whether or not it spawns anything, and the
freelist/bump allocator grows locks or sharding. Swift carries
exactly this tax (atomic ARC) as a permanent baseline; OCaml spent
years and is still recovering sequential performance (verified,
§OCaml). The refined variant — dual-mode RC, where objects are
non-atomic until marked shared (the Lean 4 runtime's
multi-threaded-bit approach, as reported in its Perceus lineage;
implementation detail not re-verified here) — still puts a
mode-test branch in every RC op and drags the whole runtime into
the memory-model business. Both variants violate C1's spirit:
programs that never spawn must pay nothing. Rejected.

### (d) Process-level parallelism only — the do-nothing baseline

Already shipped: `subprocess` (C8). Fork N Fern binaries, pass work
as argv/stdin, collect stdout — per-process heaps with total
isolation, courtesy of the kernel. This is the right answer *today*
for coarse-grain jobs (fernsmith-style fuzz fan-out, batch file
processing) and stays the right escape hatch even after workers
exist (it is also the only parallelism that reaches wasm hosts,
where the platform itself forks instances). Its limits define the
worker trigger: no typed messages, serialization by hand, process
spawn cost, no shared read-only data even by copy, and no
composition with `Result`/`?`. The baseline to beat — and until a
workload beats it, the correct amount of worker code to write is
zero.

## Recommendations

House rule: each is numbered, costed as decision vs build, and
carries an explicit trigger.

### R1 — Declare share-nothing workers as the direction (decision, now; no code)

Adopt design (a) as Fern's stated parallelism model: worker = OS
thread + private heap + private reactor; `own`-at-boundary
messages; copy in v1 with move as a future optimisation under the
same contract; no shared mutable state of any kind. Record it here
and stop re-litigating in stdlib/platform reviews. **Trigger to
build anything:** the first real workload that is CPU-bound across
cores and outgrows `subprocess` — the named candidates are
parallel per-module self-host compilation (post-#3457 world),
fernsmith differential-fuzz throughput, and batch CLI data
processing. Until one materialises, this doc is the deliverable.

### R2 — Workers must be DST-expressible from day one (invariant, now)

No worker primitive ships without a `std/sim` leg. Concretely:
every cross-worker runtime event (spawn, send, join) goes through a
driver-shaped seam exactly like `poll`/`now_ns` do
(`DST-PLATFORM-BRIEF.md`), so a simulated scheduler can interleave
worker turns from a seed and a parallelism bug is a replayable
seed, not a flake. Sequential degrade (wasm/interp) and simulation
are the same mechanism, which is a design economy worth protecting.
**Trigger:** binds at R1's build trigger; costs nothing until then
beyond not designing seams away.

### R3 — Keep the stdlib and platform worker-agnostic meanwhile (guardrails, now)

The reason this doc exists before any code. Standing rules:

1. **No new process-global mutable state** in stdlib or runtime.
   The allocator cursors are the sanctioned exception (they become
   thread-local under workers); anything else — caches, counters,
   registries — must be value-passed or `Cell`-held by the caller,
   because a hidden global becomes a data race the day workers
   land. (`std/async` already conforms: reactor state lives in the
   combinator call frames.)
2. **`Cell[T]` stays worker-local and never grows a shared/atomic
   variant.** Cross-worker state is messages, full stop.
3. **Prefer send-safe shapes in new stdlib types**: no closures or
   `dyn` smuggled inside data-carrying structs where a plain
   enum/struct would do — every such field subtracts the type from
   future sendability (C5).
4. **The leak detector (#5362) should scope its census per heap**,
   not per process, so it extends to per-worker heaps and to
   worker-exit reporting without rework.
5. **`std/sim`/`Driver` remains the single nondeterminism funnel**
   (restates R2 from the stdlib side).

**Trigger:** immediate and standing; review-time checklist, zero
build cost.

### R4 — Stage the build behind the mode lattice (build, later, in order)

When R1's trigger fires, the order is fixed by dependency, not
preference:

1. **Send-safety in the checker, derived from #5365.** Sendable =
   the unique/disjoint point of the mode lattice; v1 may ship the
   conservative structural rule (Rec (a) list, item 2) before full
   uniqueness inference, since copy semantics only need
   *classification*, not disjointness proof. Gated on the #5365
   write-up (which itself rides the goal-2 Perceus port).
2. **Native runtime, spawn/join only:** Linux clone/futex (or
   pthread), Darwin pthread, per-worker heap reservations +
   thread-local cursors. No mailboxes yet; `Worker[T]` +
   `@must_consume` join.
3. **Bounded mailboxes** (`own` messages, blocking send as
   backpressure) — only when a real pipeline workload asks;
   revisits `CONCURRENCY-RESEARCH.md` Rec §6's deferral with the
   same bar.
4. **Move-instead-of-copy at boundaries** — trigger: profiling
   shows boundary-copy cost dominating a real worker workload AND
   the lattice can prove disjointness (or worker sub-heaps have
   become transferable regions). Pure optimisation; no API change
   by construction (R1's contract).

### R5 — wasm lowering waits for an explicit external trigger (deferral, now)

Do not build any wasm worker lowering until **shared-everything-
threads reaches phase 3 with at least one engine implementation
Fern targets** (wasmtime), or a component-model thread-spawn story
ships in a WASI 0.3.x/1.0 release. Verified state 2026-07: phase 1,
wasi-threads legacy, WASI 0.3 async-only — and the edge hosts
(Cloudflare isolates, Fastly per-request sandboxes) expose no
threads regardless. Until the trigger: `spawn`/`join` on wasm is
the sequential degrade, documented as such, semantics identical.

### R6 — Document `subprocess` as the interim answer (doc, now)

A short stdlib-docs example of the fan-out-via-`subprocess` pattern
(spawn N, collect outputs, exit codes → `Result`) so users hitting
the parallelism wall find the sanctioned baseline instead of
requesting threads ad hoc. Cost: an example file; no new surface.

## Non-recommendations — explicit "do not adopt"

- **Atomic or dual-mode refcounts, ever.** Both variants of design
  (c). The constraint is C1; the evidence is OCaml's decade and
  Swift's permanent ARC tax. If a future workload seems to demand
  shared references, the answer is region transfer, not atomics.
- **ORCA-style cross-heap references.** Design (b): solves mutable
  sharing, which Fern's immutability already dissolved; costs a
  capability system plus a distributed protocol. Wrong budget.
- **A shared/atomic `Cell` or any "just one global" escape hatch.**
  Erlang's refc-binary carve-out is the precedent for how one
  shared refcounted exception becomes a permanent leak class and
  an atomic tax. Cross-worker state is messages.
- **Exposing OS threads raw** (thread handles, mutexes, shared
  memory, thread-locals as API). Workers are the only surface;
  threads are their implementation. Raw threads would instantly
  void C1–C5 and every DST property.
- **async/await coloring as the worker API** (`spawn` returning an
  awaitable that only an async fn can join, etc.). Join is a plain
  blocking call; overlap between a worker and local I/O composes
  via the existing combinators (a joinable worker can later expose
  a pollable token and become one more `Future[T]` source — inside
  the colorless model, not beside it). The anti-pattern stance of
  `CONCURRENCY-RESEARCH.md` carries over unchanged.
- **An in-runtime M:N green-thread scheduler** (BEAM/goroutine
  style). Preemption needs safepoints Fern's AOT output doesn't
  have; cold-start pays for the scheduler; the kernel already
  provides one. One worker = one OS thread is the whole runtime
  story.
- **Workers as a replacement for `concurrent{}`/combinators.** The
  single-threaded structured-concurrency surface
  (`CONCURRENCY-RESEARCH.md` Rec §1, `std/async`) remains the
  answer for I/O overlap; workers exist only for CPU parallelism
  and *contain* that surface per worker (C7). Two tools, one
  boundary, no overlap in remit.

## Cross-references

- **#5366** — this survey's tracking issue (share-nothing workers).
- **#5365** — mode-lattice unification; sendability is derived from
  it (C6, R4.1).
- **#5362** — debug leak detector; per-heap census scoping (R3.4)
  and worker-exit reporting are the interaction points.
- **#5360 / `docs/DST-PLATFORM-BRIEF.md`** — simulation platform;
  workers must remain sim-expressible (R2).
- **`docs/CONCURRENCY-RESEARCH.md`** — the intra-worker
  concurrency posture this doc composes with (its Rec §1
  `concurrent{}` block and Rec §6 channel deferral both stand;
  workers add a parallel tier above them, they do not reopen them).
- **`docs/ASYNC.md`** — the shipped colorless combinator surface
  (C7).
- **`docs/RC-PERCEUS-PLAN.md` / `docs/ARENA-DECISION.md`** — the
  memory-model constraints (C1–C3).
- **`docs/CELL-TYPE-PLAN.md` / `docs/CLOSURE-CAPTURE.md`** — the
  sendability edge cases (C4–C5).
- **`docs/PLT-LANDSCAPE-2026.md` §2.8** — the motivating analysis
  this doc discharges.
