# The allocation observable

Status: policy doc, normative where marked. Indexed by
`spec/semantics.md` (`AL-*`).

Allocation is not ungated: `docs/TEST-GATES.md` records
`TestX86_64AllocScaling`, which bounds the RATIO of
`__heap_bump_bytes()` between `n` and `2n` so an asymptotic regression
fails, and `TestSelfHostAllocDifferentialX86_64`, which compares the two
compilers' volume against each other. Both are good gates and neither is
what this document is for.

Two things they are not. They are **Go tests**, so they measure
`internal/` — and `docs/NATIVE-CONVERGENCE.md` makes the self-host
compiler the definition once the freeze preconditions (#4451) go green,
at which point a Go test measures the wrong implementation. And they run
on **x86-64 only**, so a backend that allocates differently from the
others is invisible to them; the first thing this document's conformance
cases did was find exactly that (below).

What was missing is a *contract*: a statement of what the number means,
what is portable about it, and what a conforming implementation
therefore owes — the thing a conformance case can be written against,
and that any implementation can be measured by. Without one, every leak
in #6127 had to be found by writing a bespoke differential harness for
it first.

This document is that contract, in the smallest form that is useful.

## What is measured

`__heap_bump_bytes()` returns the bump allocator's **high-water mark**:
the total bytes handed out fresh since the program started, i.e.
everything the freelist could not recycle. It is a monotone counter. It
is not live-heap size and not RSS.

RSS is the wrong measurement and the reason is worth recording: the
arena is a 16 GiB `MAP_NORESERVE` mapping, so a first touch anywhere in
it maps a 2 MB huge page under `THP=always` and a 4 KB page under
`madvise`. The same binary on the same input has measured 43 MB locally
and 552 MB on a CI runner — a 12x spread with identical allocation. The
bump counter is exact, host-independent, and meaningful under qemu.

## What is portable, and what is not

| | Portable? |
| --- | --- |
| Whether the high-water mark **grows with the round count** | ✅ |
| The absolute byte count | ❌ |
| Availability of the observable at all | ❌ — see below |

**The absolute number is not portable and must never be asserted.** It
depends on the pointer width, the string ABI, struct padding, and how
much the freelist happened to recycle before the measurement — all of
which differ legitimately between backends. A case that asserts a byte
count is asserting an implementation detail and will be deleted the
first time a backend changes one.

**The shape is portable, and it is the whole point.** Run a loop N
times, then run it 2N times, and compare the fresh bytes each round
consumed. A body that reclaims what it allocates adds a bounded amount
however many times it runs; a body that retains adds an amount that
scales with N. That difference is exactly what separates a working
reference-counting implementation from a leaking one, and it is
observable without knowing a single absolute number.

**The interpreter does not model the arena.** `internal/interp` has no
bump allocator — it is a tree-walking evaluator over Go values — so
`__heap_bump_bytes()` returns `0` there unconditionally, and the shape
above is unobservable. This is a deliberate hole, not a defect: giving
the interpreter a fake arena to measure would make it report numbers
that describe nothing. A conformance case for an allocation claim
therefore restricts `backends` to the three codegen targets.

## The claims

All are stated over the *shape*, and all are pinned — see
`spec/semantics.md`.

- **AL-01.** A loop whose body allocates and lets the allocation die
  adds a bounded number of fresh bytes: doubling the round count does
  not increase what the second run consumes. This is the property every
  reclaim fix in #6127 was ultimately restoring, measured directly
  rather than by RSS or by inspection.
- **AL-02.** A loop whose body allocates and *retains* — appending into
  an array that outlives the loop — adds fresh bytes that scale with the
  round count. Without this half, AL-01 is satisfiable by a program that
  allocates nothing at all, and a conformance case that passes for the
  wrong reason is worse than none.
- **AL-03.** The AL-01 shape again, allocating a closure environment per
  round rather than a string. Separate because it is reclaimed by a
  separate helper: #6423 was one stale constant duplicated across
  `__fern_str_dec` and `__fern_closure_drop`, and a case that only
  allocated strings would have left the second half silent.
- **AL-04.** The AL-01 shape where the loop body only READS a container.
  `m.get(k)` allocates on the caller's behalf — a box to carry the
  `Option[V]` — so a read-only loop has a reclaim obligation even though
  the program never constructed anything. Separate because the cost of
  missing it scales with the read-heavy hot path rather than with the
  data: #6561 stranded two blocks on every lookup, and both the HIT and
  the MISS edge leaked, by different amounts and through different boxes.

## What it found immediately

The first run of `alloc_flat_under_reclaim` across the three codegen
backends disagreed, which is the whole reason to have the observable.
Sampling the counter around `rounds(50)`, `rounds(100)` and
`rounds(200)`, where each round builds a string and drops it:

| backend | 50 rounds | 100 rounds | 200 rounds |
| --- | --- | --- | --- |
| x86-64 | 32 | 0 | 0 |
| arm64 | 48 | 0 | 0 |
| wasm, before #6423 | 1600 | 3200 | 6400 |
| wasm, after | 32 | 0 | 0 |

The natives pay a bounded warm-up and then serve every later round off
the freelist. wasm was exactly 32 bytes per round, forever — a
long-running program on that shape walked the arena to exhaustion
(exit 125) instead of reaching a steady state.

The cause was one constant. `__fern_str_dec` and `__fern_closure_drop`
still tested a 64 KiB low-address floor carried over from a WASI memory
layout, while `__fern_str_inc` reaches `__fern_rc_inc`, which had already
moved to `rcLowAddrGuard`. On the preview-2 layout every heap object sits
below 64 KiB, so incs happened and decs returned early: a heap string's
refcount only ever went up, and its buffer never reached the freelist.
Arrays were unaffected — `__fern_arr_dec` was already on the correct
floor — which is why the leak looked string-shaped.

It was **known and written down**: `buildStrAppendBody` carried a comment
calling the asymmetry "a leftover from the WASI layout, which keeps that
helper from freeing sub-64K heap strings", and reasoned that its own path
was safe. It was, and the leak stayed anyway, because nothing turned the
observation into a failing test. `alloc_flat_under_reclaim` is now that
test, and it runs on wasm.

Ten minutes of having a contract found a leak that had been visible in a
comment for months.

## What this deliberately does not say

Nothing here specifies *when* a value is freed, only that a reclaiming
loop does not grow the arena without bound. Drop order, the point at
which a temporary dies, whether reuse fires for a given shape, and every
question `docs/REUSE-CONTRACT.md` raises remain unspecified — they need
a store semantics, which is the large piece
`docs/SPECIFICATION-RESEARCH.md` calls Layer 3.

What changes with this document is smaller and prior to that: there is
now *an* observable with a written contract, so a claim about allocation
can be pinned by a conformance case at all. Before it, the corpus could
not express one.

## Such a case must declare itself

Every conformance case is also run with reclamation compiled OUT, and
`*FixturesFreeMatchesNoFree` requires the two runs to agree. That gate is
what turns an rc bug into a visible behaviour change instead of a leak
nobody measures, and it rests on the corpus being normative about the
*language* — where whether the allocator ran is not observable.

A case built on this document's observable breaks that assumption on
purpose: reading `__heap_bump_bytes()` is exactly how it tells. So it
must carry a `reclaim-observable` sidecar file, and the gate then
**inverts** for it — the two runs are required to DIFFER.

The inversion, rather than a skip, is the point. A skip would make the
marker a way to silence any free-off divergence, including the
miscompiles the gate exists to catch; and a case that quietly stopped
observing reclamation would keep passing while testing nothing. As a
claim, it fails loudly in both directions.

Only a case whose *expected output* changes with reclamation needs it.
`alloc_grows_when_retained` reports `grows` either way — a retained
value is not reclaimed regardless — so it satisfies the ordinary rule
and carries no marker.
