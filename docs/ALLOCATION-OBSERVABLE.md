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
therefore restricts `backends` to the codegen targets — and today
`alloc_flat_under_reclaim` excludes wasm as well, for the separate and
tracked reason below.

## The claims

Both are stated over the *shape*, and both are pinned — see
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

## What it found immediately

The first run of `alloc_flat_under_reclaim` across the three codegen
backends disagreed, which is the whole reason to have the observable.
Sampling the counter around `rounds(50)`, `rounds(100)` and
`rounds(200)`, where each round builds a string and drops it:

| backend | 50 rounds | 100 rounds | 200 rounds |
| --- | --- | --- | --- |
| x86-64 | 32 | 0 | 0 |
| arm64 | 48 | 0 | 0 |
| wasm | 1600 | 3200 | 6400 |

The natives pay a bounded warm-up and then serve every later round off
the freelist. wasm is exactly 32 bytes per round, forever: the string is
never returned to a freelist, so a long-running program on this shape
walks the arena to exhaustion (exit 125) instead of reaching a steady
state. Filed as #6423; `alloc_flat_under_reclaim` carries an
`implementation-gap` waiver naming it, so the case widens to wasm on its
own terms when the leak is fixed.

Ten minutes of having a contract found a leak that had been invisible
because nothing could express the property it violates.

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
