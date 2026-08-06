# Cycle-collection risk under the RC pivot

Date: 2026-06-01.
Status: design analysis. No compiler code changed by this doc.

> **UPDATE (2026-06-02): `state { }` and the persistent heap are
> gone.** This analysis repeatedly frames process-lifetime cycles
> around `state { }` module variables and proposes collectors
> scoped to "the persistent heap." Both the `state` feature and
> the two-cursor allocator's persistent region have since been
> removed — there is no longer a persistent heap, and no
> language-level mechanism for process-lifetime state at all.
> Read the persistent-heap-specific passages below as
> conditional on `state` (or an equivalent) being reintroduced.
> The core finding is unchanged and now *broader*: RC cannot
> collect cycles, and with the per-request arena reset also gone,
> **any** cycle a long-running handler builds leaks until exit
> (not just `state`-rooted ones).

> **UPDATE (2026-08-06): THE CYCLE IS NO LONGER CONSTRUCTIBLE, AND
> THIS DOCUMENT'S RECOMMENDATION WAS ADOPTED.** Everything below
> describing a constructible cycle is now HISTORICAL. Read the TL;DR
> as "what was true on 2026-06-01", not as a live risk assessment —
> taken at face value it sends a reader off to build a cycle
> collector for cycles that cannot exist.
>
> The last section recommends "cycle-free by construction, enforced
> by the checker". That is what shipped, at three enforcement points:
>
> - **E048** — struct fields are immutable after construction, so
>   `a.next = [b]` no longer type-checks ("rebuild with
>   `T { ...old, next: value }`"). This was the sole mechanism the
>   proof below relies on.
> - **E048's subscript counterpart** — the same ban for element
>   assignment, completing the immutable-data enforcement.
> - **E057** — `Cell[T]`, the sanctioned mutable box, restricts `T`
>   to scalars and `string` *explicitly* so "a cell can never
>   reconstruct a reference cycle" (`internal/checker/checker.go:635`).
>   `Cell[Node]` and `Cell[fn]` are both rejected.
>
> Re-verified 2026-08-06 by running the proof program below: it now
> fails `-check` with two E048s. The remaining routes were probed and
> are closed too — a struct rebuild (`S { ...s, xs: [s] }`) captures
> a SNAPSHOT of the old value rather than creating a back-edge, which
> is value semantics doing the work, and an array `.append` of a
> container to itself has the same shape.
>
> **Consequence for the RC design: Fern needs no cycle collector.**
> Not "not yet" — the constructs that could close a cycle are
> rejected at check time. If a future feature reintroduces
> interior mutability over composite types (a `Cell[T]` widened past
> scalars, a `ref`/`weak` type, or bringing back field assignment),
> that feature reopens this question and should cite this document.

## TL;DR (historical — see the update above)

**Yes — a Fern program can construct a reference cycle today, and
it does so through a real, shipped, all-backends-supported feature:
in-place struct-field assignment (`p.field = v`) combined with
recursive struct/enum types.** This is empirically verified (see
"Proof" below): `a.next = [b]; b.next = [a]` type-checks, runs on
the interpreter and the x86-64 native backend, and the back-edges
resolve at runtime.

Pure reference counting cannot reclaim a cycle. Free-on-zero is
**on by default today** (`internal/ast/ast.go:414`,
`RcFreeEnabled = true`), and `__fern_rc_dec` has no cycle/trace
collector — when a value's last *external* reference drops, the
internal back-edge keeps every node's rc ≥ 1, so the whole cycle's
storage is stranded.

Two things still mask this:

1. **The arena.** The bump-allocator arena (`arena { … }` and the
   implicit per-handler arena in wasi-http's `__http_entry`)
   reclaims everything allocated since the save in one cursor
   rewind — cyclic or not. A *request-scoped* cycle is therefore
   reclaimed at request end regardless of refcount. This is the
   safety net the pivot to long-running processes removes.
2. **The leak is silent, not corrupting.** A leaked cycle is not a
   use-after-free; it doesn't trip the rc-underflow detector. It
   just grows RSS until OOM.

**Where a long-running server leaks:** any cycle rooted in
*process-lifetime* state — a module-level `state { }` variable, or
any data structure that outlives the per-handler `arena_restore` —
that is (or transitively reaches) a cycle. Request-scoped cycles
are safe; process-lifetime cycles leak unboundedly across requests.

**Recommendation (detail + rationale in the last section):** adopt
**"cycle-free by construction, enforced by the checker"** as the
primary stance — reject the precise constructs that can close a
cycle in the persistent heap — and keep a tracing backup collector
explicitly out of scope for now. Fern's value-semantics design
makes the cycle-free invariant almost free to hold; the only hole
is the `p.field = v` / mutable-closure-capture mutation surface,
which is young, narrow, and not yet load-bearing in the stdlib.
Closing it now is far cheaper than retrofitting a Bacon-Rajan
collector onto a non-atomic RC runtime later.

---

## 1. Can the type system express a cycle?

### 1a. Recursive types are first-class

The type layer permits self-referential types as long as the
recursion goes through a heap-boxed indirection (which breaks the
*size* cycle, not the *reference* cycle):

- **Recursive enums** are explicitly supported and shipped. The
  builtin `JsonValue` is the canonical example
  (`internal/checker/checker.go:183-213`): `JArray(JsonValue[])`
  and `JObject(Map[string, JsonValue])` are self-referential
  variants. The comment at `checker.go:187-190` states the
  mechanism outright: *"Self-referential variants … work because
  enum payloads are heap-allocated, breaking the size cycle."*
- **Recursive structs** type-check the same way. A user
  declaration `struct Node { val: i32, next: Node[] }` is accepted
  by the checker — the `Node[]` field is a heap pointer, so the
  struct has a finite, fixed layout. (Verified: `fern -check` on
  the program in §2 reports no error.)

So the type system says "tree-shaped recursive types are fine."
The open question is whether the *value* graph can be made cyclic,
which requires mutation.

### 1b. There IS a mutation path that closes a loop

The RC plan assumes there isn't. `docs/RC-PERCEUS-PLAN.md:71-77`
(the "Garbage cycles" non-goal) says:

> Fern already has this property [no cycles] by accident (no
> mutable struct fields, no mutable closure captures).

and `RC-PERCEUS-PLAN.md:2003-2004` (Open Question #9) repeats:

> Currently Fern's types are tree-shaped at the type level (no
> cycles).

**Both statements are now stale.** Mutable struct fields exist and
are wired end-to-end:

- **Field assignment `p.field = v`.** The checker accepts a
  `*ast.FieldAccess` as an assignment target
  (`internal/checker/checker.go:4919-4940`: `case *ast.Assign`
  type-checks `n.Target`, and line 4932 lists `FieldAccess`
  alongside `Ident` and `Index` as an addressable target). The IR
  lowers it to a raw in-place store —
  `internal/ir/ir.go:9921-9956`: compute `base + field_offset`,
  evaluate the value, `payloadStoreOpFor(ft, ptrW)`. There is **no
  mutability gate** (no `mut` keyword, no `let` vs `var`
  distinction on fields), **no rc check, and no copy-on-write** on
  this path — unlike `arr.push` / `arr[i] = v` / `Map.set`, which
  all route through CoW helpers. A struct field store mutates the
  shared box directly.

- **Structs have reference semantics.** Because structs are
  heap-boxed and a field store writes the shared box, the mutation
  is visible through every alias. Verified empirically:

  ```fern
  struct Box { items: i32[] }
  function main(): i32 {
      var a = Box { items: [10] };
      var c = a;          // alias — same box
      a.items = [99];     // mutate through a
      return c.items[0];  // 99 (reference semantics), not 10
  }
  ```
  Returns `99` on both the interpreter and the x86-64 native
  backend. This is exactly the shared-mutable-heap-state property
  needed to form a *persistent* cycle.

- **Mutable closure captures.** A closure can capture a
  heap value and mutate it; the env block is heap-allocated and
  shared across re-invocations. `internal/ir/ir.go:9958-9983`
  (the `*ast.CaptureRef` assignment case) states it directly:
  *"The env block is heap-allocated and shared by all calls to
  this closure — mutation persists across re-invocations."* So a
  closure holding a captured container that (transitively) holds
  the closure is a second cycle-construction route.

### 1c. Other potential cycle shapes

- **Map value → container holding the map.** `JObject(Map[string,
  JsonValue])` already nests a Map inside a recursive enum; mutate
  a `JsonValue`-typed local's field (or use `Map.set`) to point a
  value back at an enclosing structure and the loop closes through
  the map. Maps participate in the same heap graph.
- **Array/slice of structs that reference back.** The §2 proof
  uses exactly this (`next: Node[]`).
- **Tuples** are heap-boxed too, but tuples are immutable (no
  element-assign target), so a tuple can only be part of a cycle
  if a *mutable* field elsewhere points back through it.

**Conclusion for §1:** the type system can express recursive
shapes, and the `p.field = v` (and mutable-capture) mutation paths
let a program close those shapes into a value-graph cycle. The
"cycle-free by accident" invariant the RC plan relies on **no
longer holds.**

---

## 2. Proof: a constructible, traversable cycle

```fern
struct Node {
    val: i32,
    next: Node[],
}

function main(): i32 {
    var a = Node { val: 1, next: [] };
    var b = Node { val: 2, next: [] };
    a.next = [b];          // a -> b
    b.next = [a];          // b -> a   (closes the cycle)
    // a -> b -> a -> b : reading val proves the back-edge resolves
    return a.next[0].next[0].next[0].val;   // == 2 (b's val)
}
```

Observed:

- `fern -check`  → OK (no type error).
- `fern -interp` → prints `cycle built`, exits **2**.
- `fern -target x86-64` (native, free-on by default) → exits **2**.

The traversal `a.next[0]` is `b`, `b.next[0]` is `a`, `a.next[0]`
is `b` → `b.val == 2`. The cycle is real and its back-edges
resolve at runtime.

A single-node self-cycle works identically:

```fern
struct Node { val: i32, link: Node[] }
function main(): i32 {
    var a = Node { val: 7, link: [] };
    a.link = [a];                  // a points at itself
    return a.link[0].link[0].val;  // a -> a -> val == 7
}
```
→ exits **7** on both interpreter and x86-64 native.

---

## 3. Exact reclamation behaviour for a cycle today: it leaks

`__fern_rc_dec` is a pure rc-arithmetic chokepoint with **no
graph awareness**. From `docs/RC-PERCEUS-PLAN.md:743-756` ("Current
state"), confirmed against the runtime:

- Per-backend dec helper (`buildRcDecBody` in
  `internal/codegen/wasmbin/runtime.go`; `emitRcDecRuntime` in the
  arm64/x86-64 backends) does: null guard → low-address guard
  (`< 0x10000`) → load rc at `[ptr-8]` → static-sentinel
  short-circuit (high bit) → rc-underflow check → decrement → store
  → **on the last reference (rc==1) free the box via the freelist**
  (`__fern_box_free` / `__fern_arr_dec`, now wired —
  `RC-PERCEUS-PLAN.md:1046-1218`, `RcFreeEnabled = true` at
  `internal/ast/ast.go:414`).
- Drop handlers (`__fern_drop_arr_ptr`, `__drop_struct_<N>`,
  `__drop_enum_<Name>`, the closure thunk) recurse **down** the
  ownership tree, dec'ing pointer-shaped fields/elements. They
  follow edges in the *forward* direction only; there is no
  back-pointer discovery, no mark phase, no trial-deletion.

There is no cycle collector anywhere in the tree. A grep for
`cycle` / `trace` / `mark` collection machinery turns up only the
*acknowledgement* that it doesn't exist:

- `RC-PERCEUS-PLAN.md:71-77`: "Garbage cycles. Cycles in RC graphs
  leak. … For phase 1: no fallback, document the invariant."
- `RC-PERCEUS-PLAN.md:2074-2076` (Reference reading): Bacon &
  Rajan, "Concurrent Cycle Collection in Reference Counted
  Systems" — listed *"for when cycles eventually become a thing"*,
  i.e. explicitly future work.

**What happens to the §2 cycle at scope exit:** when `a` and `b`
go out of scope, the exit sweep dec's each *external* reference.
But `a.next[0]` still references `b` (rc stays ≥ 1) and
`b.next[0]` still references `a` (rc stays ≥ 1). Neither box ever
reaches rc 0, so neither is freed. Under the bump-only arena
(free-off) the storage is reclaimed by the eventual arena reset
anyway; under the freelist (free-on, today's default) the boxes
are **permanently stranded** — a classic RC cycle leak.

(Note: the §2 program is short-lived, so its arena/process teardown
hides the leak from the exit code. The leak only *matters* in a
process that doesn't tear down — see §4.)

---

## 4. Cycle leaks after the arena removal

> **UPDATE (2026-06-01):** the per-request arena reset described in
> this section has been **removed** — `arena_save` / `arena_restore`
> and the `__http_entry` / `tcp_serve` bracketing no longer exist (see
> `docs/ARENA-DECISION.md`). The "safety valve" below is gone; the
> section is kept to explain what changed and why the cycle exposure
> is now broader. RC is the only reclaim mechanism.

### The arena used to mask request-scoped cycle leaks (removed)

Historically `arena_save()` snapshotted the bump cursor and
`arena_restore(handle)` rewound it — a refcount-blind reset that
reclaimed live, dead, and cyclic allocations identically. The
wasi-http `__http_entry` wrapper and `std/tcp.fern`'s `tcp_serve`
loop wrapped each request in that save/restore, so **request-scoped
cycles did not leak**: anything a handler allocated for a request,
cycles included, died at request end regardless of refcount. That was
the design's safety valve for the common edge-function shape.

### After removal: request-scoped cycles leak too

With the arena gone, per-request memory is reclaimed only by RC as
references drop. RC cannot collect cycles, so a cycle a handler builds
while serving a request is **no longer reclaimed at request end** — it
survives until process exit and accumulates across requests.

### Process-lifetime cycles leak (unchanged)

These always leaked and still do:

- **`state { }` module-level variables** — *removed* (see the banner
  at the top). While they existed, state-rooted allocations were
  routed to a persistent heap region (the two-cursor allocator) so a
  `state`-rooted `Map`/`T[]` survived request teardown, and a cycle
  reachable from a `state` var was never reclaimed. With `state` gone
  there is no such process-lifetime root today.
- **Any long-lived accumulator** in a native accept-loop server that
  outlives a single request — this is now the *only* way to root a
  process-lifetime cycle.

So the precise risk statement is now:

> A long-running Fern HTTP server leaks any reference cycle a handler
> constructs — request-scoped cycles (formerly reclaimed by the arena
> reset, now leaked) as well as cycles reachable from `state { }` or a
> persistent accumulator.

Request-scoped cycles: **now leak** (previously reclaimed by the
arena). Process-lifetime cycles: **leak unboundedly** (unchanged).
This raises the priority of a real cycle collector (§5+).

---

## 5. How other RC languages handle this (brief, cited)

- **Koka / Lean 4 (Perceus).** Assume cycle-freedom and provide no
  collector. Koka's effect/type discipline and Lean's purely
  functional core make cyclic *values* essentially unconstructible;
  Perceus (Reinking et al., PLDI 2021,
  `RC-PERCEUS-PLAN.md:2070-2072`) is "garbage-free reference
  counting" *given* that invariant. This is the model Fern's RC
  plan was cribbed from — and it only holds if Fern keeps the
  invariant Koka/Lean get from their semantics.
- **Swift.** Pure RC (ARC) with **no cycle collector**. Cycles are
  the programmer's problem; the language provides `weak` and
  `unowned` references that don't contribute to the retain count,
  so the programmer manually breaks the cycle at the back-edge.
  Leaks are a known, documented Swift footgun.
- **Python / CPython.** RC as the primary mechanism **plus a backup
  generational tracing collector** specifically to reclaim cycles
  the refcounts can't. The cost: stop-the-world-ish pauses and the
  collector must understand every container type's layout.
- **Roc.** Pure RC, **no cycle collector**, and — like Koka — stays
  cycle-free *by construction*: Roc's data is immutable and there's
  no mutable field that could form a back-edge (the RC plan cites
  this at `RC-PERCEUS-PLAN.md:71-72`: *"Roc disallows them via the
  type system: there's no mutable field that could form a cycle."*).
  This is the stance Fern *claims* to share but — per §1b — has
  already drifted from by shipping `p.field = v`.

The spectrum: Koka/Lean/Roc *prevent* cycles (by semantics/types)
and need no collector; Swift *permits* cycles and offloads breaking
them to the programmer (`weak`/`unowned`); Python *permits* cycles
and *cleans up after* with a tracing backup.

---

## 6. Options for Fern, with trade-offs

### Option A — Cycle-free by construction, enforced by the checker

Keep the Koka/Roc model: make it *impossible* to construct a cycle
that outlives an arena. The current hole is the `p.field = v` /
mutable-capture mutation surface (§1b), so the enforcement targets
exactly that.

Possible enforcement shapes (pick the least invasive that closes
the hole):

1. **Forbid back-edges structurally.** Disallow assigning a value
   of a type that can transitively reach the target's own type into
   a mutable field — i.e. reject `node.next = <expr of Node-ish
   type>`. Conservative; rejects some legitimate non-cyclic shapes
   (DAG-of-same-type), which is the usual cost of a syntactic
   acyclicity rule.
2. **Forbid mutable fields entirely on recursive types.** A struct
   that is (transitively) recursive may not have its fields
   assigned after construction; build it bottom-up only. This is
   essentially Roc's rule. It keeps recursive *immutable* trees
   (the JSON AST shape) fully working and only blocks the imperative
   "patch a back-pointer in" pattern, which is the only way to close
   a cycle.
3. **Restrict only the persistent heap.** Allow `p.field = v`
   freely in arena/request scope (cycles there are reclaimed), but
   forbid a `state`-rooted or arena-escaping value from being on the
   receiving end of a field assignment that could close a loop. This
   is the most permissive but the analysis (does this assignment
   target reach persistent state?) is the hardest to get right.

Trade-offs:
- **+** No runtime cost, no collector, no pauses, preserves
  "garbage-free RC". Matches the language's stated single-threaded,
  fast-startup, edge-function focus.
- **+** The mutation surface is *young and narrow*: `p.field = v`
  shipped recently, has no CoW story yet (it's a raw store, see
  §1b), and no stdlib type relies on building cyclic graphs. The
  blast radius of restricting it is small *now* and grows every
  month it's deferred.
- **−** Reduces expressiveness: genuinely cyclic data (doubly-linked
  lists, graphs with back-edges, observer wiring) becomes
  awkward/impossible without an explicit indirection (indices into
  an arena/arena-array, a la Rust's `Vec<Node>` + indices).
- **−** A precise "can this assignment close a cycle" analysis is
  non-trivial; a conservative version over-rejects.

### Option B — Weak references (Swift model)

Add a `weak`/`unowned` reference kind that doesn't bump rc, so the
programmer breaks the back-edge manually (`node.prev` is `weak`).

Trade-offs:
- **+** Lets the programmer build genuine cyclic structures and
  still reclaim them.
- **+** No tracing machinery; stays within the RC runtime.
- **−** Pushes correctness onto the programmer — the exact footgun
  Swift is criticised for. A wrong/missing `weak` either leaks (cycle
  survives) or dangles (weak ref outlives strong, then a strong
  promotion UAFs). On a *non-atomic* RC runtime with the existing
  borrow/free tension (`RC-PERCEUS-PLAN.md:1326-1370` documents how
  subtle the borrow⇄free interaction already is), adding a second
  reference kind multiplies the cases the codegen and the
  free-eligibility analysis must track.
- **−** Contradicts the value-semantics surface the language sells:
  `weak` is an explicitly *reference*-semantic, lifetime-sensitive
  construct, which is a different mental model from "everything looks
  immutable."

### Option C — Opt-in backup tracing collector for the persistent heap

Add a CPython-style mark/sweep (or Bacon-Rajan trial-deletion,
`RC-PERCEUS-PLAN.md:2074-2076`) that runs only over the *persistent*
heap (the `state`-rooted region), since request-scoped garbage is
already handled by the arena.

Trade-offs:
- **+** Cycles "just work"; no expressiveness loss, no programmer
  burden.
- **+** Scoping it to the persistent heap (small, slow-changing)
  keeps the trace cheap and avoids pausing per-request work.
- **−** Large implementation: the collector must walk every
  container's layout (struct shapes, enum variant payloads, map
  buckets, closure env blocks, array element types) to find
  pointers — duplicating the per-type knowledge the drop handlers
  already encode, but in the *reverse/discovery* direction. This is
  the single biggest piece of runtime the project would own.
- **−** Reopens the thread-safety question: a tracing collector and
  the planned move to concurrency (`RC-PERCEUS-PLAN.md:78-80`,
  non-atomic rc, single-threaded) interact badly; Bacon-Rajan's
  concurrent variant exists but is a research-grade effort.
- **−** Pause behaviour conflicts with the "fast-startup, short
  latency edge function" use case if it ever runs during a request.
- **−** It is, by the plan's own framing, the *fallback* — adopting
  it concedes the garbage-free-RC property the whole pivot was built
  around.

---

## 7. Recommendation

**Adopt Option A (cycle-free by construction, checker-enforced),
specifically variant A.2 — no post-construction mutation of fields
on recursive types — as the near-term stance. Keep Option C (backup
tracing collector) explicitly deferred and Option B (weak refs)
rejected for now.**

Rationale:

1. **It preserves the property the entire RC pivot was designed
   around.** The plan is lifted from Koka/Roc/Lean, all of which are
   *garbage-free by assuming cycle-freedom*
   (`RC-PERCEUS-PLAN.md:30-34, 71-77`). Adding a tracing collector
   (C) throws that away; weak refs (B) push the problem onto users
   and clash with the value-semantics surface. A is the only option
   that keeps the design coherent.

2. **The hole is small and young — close it before it's
   load-bearing.** `p.field = v` is a raw in-place store with no CoW,
   no mutability keyword, and (per §1b) the RC plan's own design
   docs still believe it doesn't exist. No stdlib type builds cyclic
   graphs today. Restricting field mutation on recursive types now
   touches almost nothing; deferring it means every cyclic structure
   written in the interim becomes a migration liability and a latent
   production leak.

3. **The arena already covers the common case, so the restriction
   only bites where it must.** Request-scoped cycles are reclaimed
   by `__http_entry`'s arena (§4). The checker rule only needs to
   protect the persistent heap (`state`-rooted / arena-escaping
   data). In practice A.2's "don't patch back-pointers into recursive
   types" is a clean, teachable rule that leaves the actually-common
   shapes — immutable recursive trees like `JsonValue`, bottom-up
   built ASTs (the self-hosted compiler's own data!) — completely
   untouched. The self-hosted parser/checker build trees bottom-up
   and never patch a child's back-pointer to a parent; they would
   not be affected.

4. **It defers the expensive, risky machinery until there's
   evidence it's needed.** If a real use case later demands genuine
   cyclic structures in persistent state (a graph server, a
   long-lived observer mesh), revisit with Option C scoped to the
   persistent heap — by then the RC borrow/free model
   (`RC-PERCEUS-PLAN.md:1326-1370`) will have stabilised and the
   per-type layout metadata the collector needs will be more mature.
   Choosing C now means building the project's largest runtime
   component to solve a problem no shipping program has.

Concrete next steps (each its own PR, tests-first per the
engineering bar):

1. **Fix the stale invariant docs.** Update
   `RC-PERCEUS-PLAN.md:71-77` and Open Question #9
   (`:2003-2004`) to record that mutable struct fields + recursive
   types now *can* form a cycle (with the §2 repro as the test
   fixture). This is the prerequisite honesty for any decision.
2. **Add a checker rule (A.2)**: reject an `Assign` whose target is
   a `*ast.FieldAccess` (or capture assignment) when the owning type
   is transitively recursive and the field is part of that recursion
   — i.e. forbid closing a back-edge. Land it with checker tests
   covering the §2 `Node`/`Node[]` shape, the self-cycle shape, and
   the closure-capture shape, plus *negative* tests proving
   immutable recursive trees (`JsonValue` construction, bottom-up
   AST building) still compile.
3. **Add an e2e leak guard** for a long-running shape: a `state`-
   rooted accumulator across many simulated requests, asserting
   bounded RSS / bounded live-allocation count — so a future
   regression that re-opens the cycle hole is caught.

The fallback path stays open: if the cycle-free rule proves too
restrictive in practice, the persistent-heap-scoped tracing
collector (Option C) is the documented escape hatch, and Bacon-Rajan
is already in the reference list (`RC-PERCEUS-PLAN.md:2074-2076`) for
when "cycles eventually become a thing."

---

## Appendix: evidence index

| Claim | Evidence |
|---|---|
| Recursive enums supported | `internal/checker/checker.go:183-213` (JsonValue), comment at `:187-190` |
| Recursive struct type-checks | `fern -check` on §2 program (empirical) |
| `p.field = v` is a valid assign target | `internal/checker/checker.go:4919-4940` (line 4932) |
| Field assign is a raw in-place store (no CoW/rc gate) | `internal/ir/ir.go:9921-9956` |
| Mutable closure captures persist in shared heap env | `internal/ir/ir.go:9958-9983` |
| Structs have reference semantics (alias sees mutation) | §1b `Box` program → 99 (interp + x86-64) |
| Cycle constructible + traversable | §2 `Node` program → exit 2; self-cycle → exit 7 (interp + x86-64) |
| Free-on-zero on by default | `internal/ast/ast.go:414` (`RcFreeEnabled = true`) |
| `rc_dec`/drop has no cycle collector | `docs/RC-PERCEUS-PLAN.md:743-756`, `:71-77`, `:2074-2076` |
| RC plan's "no cycles" assumption (now stale) | `docs/RC-PERCEUS-PLAN.md:71-77`, `:2003-2004` |
| ~~Arena reset reclaims regardless of rc~~ | removed (see `docs/ARENA-DECISION.md`) |
| ~~Per-handler arena save/restore~~ | removed (see `docs/ARENA-DECISION.md`) |
| ~~`state{}` persistent heap survives `arena_restore`~~ | both removed (see top banner) |
| Roc disallows cycles via types | `docs/RC-PERCEUS-PLAN.md:71-72` |
| Perceus paper / Bacon-Rajan deferred | `docs/RC-PERCEUS-PLAN.md:2070-2076` |

---

## Decision (2026-06-01)

**Direction: immutable data structures only — which makes cycles unconstructible by construction.**

The two questions this doc raised — "how do we handle cycles under RC?"
and "is `p.field = v` without copy-on-write a bug?" — collapse into one
answer. If post-construction mutation is removed from the language, a
reference cycle cannot be formed (you can only point at values that
already exist when you build a node, never patch an existing node to
close a loop), so RC stays **garbage-free with no runtime cycle
collector and no weak-reference burden on users** — exactly the
property the pivot was lifted from Koka / Roc / Lean to get.
Immutability *is* the cycle strategy; Options B (tracing collector) and
C (weak refs) are not needed and are explicitly **not** pursued.

### Correction to the analysis above

The body of this doc (and the original `RC-PERCEUS-PLAN.md:71-77`
assumption) treated field mutation as a narrow, unused hole. That is
wrong and the sequencing below depends on the correction: in-place
field mutation is **shipped, intended, and load-bearing today**.

- `internal/e2e/self_host_field_assign_test.go` documents the intended
  semantics: `obj.field = value` "mutation persists through the heap
  pointer, so a struct passed to a function is mutated in place" — and
  asserts it (`bump(b)` through a function call → exit 12). The
  alias-visible mutation flagged in §1b is *intended* under today's
  reference-semantic structs, not a latent bug.
- ~497 `obj.field = …` call sites exist across `examples/` and
  `examples/self_host/`; the self-hosted compiler's own passes
  (parser / constfold / flatten) mutate fields.

So in-place field assignment is **not** an accidental gap to ban
cheaply — it is the current intended behaviour, and the self-hosting
path relies on it.

### Therefore: this is a migration, not a one-line checker ban

Forbidding field assignment in the checker *today* would break
`examples/wasm/wc.fern`, the self-host parser/constfold/flatten passes,
and `TestSelfHostFieldAssign*`. The order has to be:

1. **Record the decision** (this section) — the target is no
   post-construction mutation; cycles are designed out, not collected.
2. **Provide the functional-update replacement** before removing the
   mutable form: a struct-update expression (`Node { ...old, next: x }`)
   or equivalent rebuild, so the parser / checker / self-host passes
   that currently mutate can be rewritten to produce new values.
3. **Migrate** the in-tree mutators (self-host passes, `wc.fern`, the
   `state{}` accumulator patterns) to the functional form.
4. **Then** flip the checker to reject `obj.field = v` (and mutable
   closure-capture write-back). At that point cycles are
   unconstructible and the `RC-PERCEUS-PLAN.md` "cycle-free invariant"
   becomes true-by-enforcement rather than true-by-accident — update
   that doc's stale line at the same time.

Until step 4 lands, the leak risk identified above is real but bounded:
it only bites a long-running process that forms a *process-lifetime*
cycle (rooted in `state{}` or an accumulator surviving the per-request
arena). Request-scoped cycles are still reclaimed by `arena_restore`.
A short-term mitigation, if a long-running server ships before the
migration completes, is to document "don't build self-referential
mutable graphs in `state{}`" — but the durable fix is the immutability
migration above.

### Follow-up work items (not in this doc PR)

- Design + implement the functional struct-update expression.
- Migrate self-host passes + examples off field mutation.
- Checker rule forbidding post-construction field/capture mutation.
- Update `RC-PERCEUS-PLAN.md:71-77` once enforcement lands.
