# The per-operator fusion proof

Status: `internal/ir/array_fusion.go` (#9731) and the primary compiler's
`examples/self_host/semfuse.fern` (#11072) implement the `map`/`filter` stages
and `fold`/`reduce` sinks below. The scan section describes an unimplemented
extension. This document states what each array operator contributes to a
fused loop, and why composing those contributions gives the guarantee
`docs/ITERATOR-FUSION-CONTRACT.md` clause 1 asks for.

Written before the pass, because clause 1 is a claim a benchmark cannot
establish:

> Compositional means proved per operator, not per whole-pipeline
> pattern-match — a user combining any supported operators gets the
> guarantee without the compiler having anticipated that combination.

A pass built as a table of recognized three-stage shapes fails that
clause **even if every benchmark passes**, so the table is what this
document exists to prevent. #9731's acceptance says the same thing in
one line: the per-operator proof is written down, not implied by
benchmarks passing.

Upstream: `docs/ITERATOR-FUSION-CONTRACT.md` (the contract),
`docs/ARRAY-ALGEBRA.md` (what fusion may and may not do),
`docs/ARRAY-PIPELINE-BASELINE-2026-09.md` (what it is worth). Recognition
of the pipelines this operates on is `internal/ir/array_pipeline.go`
(#9730).

## The shape of a fused pipeline

Every fused pipeline compiles to exactly one loop:

```
  <prologue>                  each operator's init, in stage order
  i = 0
  while i < n:
      x = xs[i]
      <step_1>                may rebind x, may skip to the next i
      <step_2>
      …
      <sink step>
      i = i + 1
  <epilogue>                  each operator's finish, in reverse order
```

An operator contributes three fragments, any of which may be empty:

| fragment | runs | obligation |
| --- | --- | --- |
| `init` | once, before the loop | at most O(1) allocations |
| `step` | once per surviving element | **zero** allocations, no unspecialised call |
| `finish` | once, after the loop | at most O(1) allocations |

A `step` may do one of three things to the element in flight: pass it
through unchanged, rebind it to a new value, or **skip** — abandon this
element and advance the loop. Nothing else. That restriction is what
makes the fragments composable without the pass reasoning about any
particular ordering of them.

## The composition argument

Let a pipeline be stages `s_1 … s_k` followed by a sink `t`, each
meeting the obligations above.

**Allocation.** The loop body is the concatenation of `step_1 … step_k`
and `t.step`. Each allocates nothing, and concatenation of
nothing-allocating fragments allocates nothing, so the body allocates
nothing per element. The prologue and epilogue contribute at most
O(1) each per operator, so the whole pipeline allocates O(k) —
independent of the element count `n`. That is the zero-allocation
guarantee in the only form that is true: constant, not literally zero,
because a sink may have a result to build.

**Calls.** Each `step` makes at most one call, to the stage's element
function, and §1 of `docs/ARRAY-ALGEBRA.md` admits a stage only when
that function is statically resolved. A statically resolved callee is a
direct call, so no `step` makes an unspecialised call, so neither does
their concatenation.

**Why this is not a shape table.** The argument above never names `map`
or `reduce`. It quantifies over any `k`, in any order, provided each
operator meets the fragment obligations — so a combination the pass was
never tested against fuses for the same reason the tested ones do. The
pass therefore composes fragments and must not branch on the stage
sequence; a special case for a particular chain would be a bug against
this document even if it produced correct code.

## The operators

### `map(f)` — elementwise

```
init:    —
step:    x = f(x)
finish:  —
```

Allocates nothing: the rebinding is a local. One call, direct by §1.
`f`'s own allocation is `f`'s business and is not per-element cost the
pipeline introduced — but a `f` that allocates is still a `f` whose
pipeline does not meet clause 1's premise, which the contract states as
a property of the operators, not of the pass.

### `filter(p)` — selection

```
init:    —
step:    if !p(x) { skip }
finish:  —
```

Allocates nothing. One call, direct by §1. The `skip` is what variable
cardinality means at this layer: a fused `filter` needs no size planning
because nothing is being sized — the elements that survive simply reach
the next fragment, and the ones that do not never do.

This is why `filter` is trivial to fuse while being the operator that
breaks a scheme built on a fixed trip count. The trip count is over the
*input*, and always was; only the sink sees how many elements arrived.

### `fold(z, h)` — reduction, a sink

```
init:    acc = z
step:    acc = h(acc, x)
finish:  result is acc
```

The accumulator is one value, so `init` is O(1) and `step` allocates
nothing. Reductions end a pipeline: a scalar has no next stage.

### `reduce(h)` — reduction with no seed, a sink

```
init:    seeded = false; acc = <undef>
step:    if seeded { acc = h(acc, x) } else { acc = x; seeded = true }
finish:  result is Some(acc) when seeded, else None
```

The seeded flag is what `reduce` has instead of `z`, and it costs one
`i1` in the loop rather than an allocation. The `Option` is built in
`finish`, once — O(1), not per element.

`reduce` is where `docs/ARRAY-ALGEBRA.md` §3 binds: `h` is applied in
index order and the fragments above do not license any other order. A
tree reduction is a different `step`/`finish` pair and needs the
explicit opt-in that document requires.

### `scan(z, h)`: proposed prefix sink

Neither compiler currently fuses scan. The primary report names such a site
`operator-outside-algebra`. The following fragments describe the proposed
extension, not the code emitted today.

```
init:    acc = z; out = <buffer of length n>
step:    acc = h(acc, x); out[i] = acc
finish:  result is out
```

**`scan` is a sink that materializes, and saying so is the point.** Its
output has the same length as its input, so it cannot be fused away —
but it can still be fused *into*: the buffer is sized once in `init`
from a length already known, so the pipeline pays one allocation instead
of the O(log n) geometric regrows the eager combinator pays today.

The original minimum viable design includes scan to demonstrate that a
materializing operator composes with the same fragment interface. That part
remains unimplemented. The shipped first slice uses reduction sinks; scan
still needs loop generation, cardinality handling after filters, allocation
and semantic tests, and measurement.

In that design, a `scan` in the middle of a chain would end one fused loop
and begin another.

## Deliberately not in the first slice

The contract's difficulty order, with what each one needs beyond the
fragment interface above:

| operator | what it needs | status |
| --- | --- | --- |
| `take(n)`, `take_while(p)` | a termination flag threaded through the loop — the `step` vocabulary gains a **stop**, distinct from **skip** | next |
| `drop_while(p)` | a latch in `init`, so `step` is stateful | next |
| `flat_map(f)` | nested loop generation; a `step` that yields many elements is outside the three-outcome vocabulary | after |
| `zip(a, b)` | two cursors advancing at mismatched rates, so there is no single `i` | last, and may stay unfused |

`zip` is the contract's own cut line and `docs/ARRAY-ALGEBRA.md` §4
already fixes its truncation semantics, so deferring it costs nothing
that has to be revisited.

Adding **stop** to the vocabulary is a real extension, not a widening:
it interacts with `reduce`'s `finish` (a pipeline stopped early still
has an accumulator to answer with) and with §2's rule about eliding
element-function applications. It is listed as next rather than folded
in here because the proof above is only sound for the three outcomes it
names.

## What the proof rests on

Each obligation below is checkable, and the pass must check it rather
than assume it. They are `docs/ARRAY-ALGEBRA.md`'s, restated as the
preconditions of the argument:

1. **Every element function is statically resolved** (§1). Without it
   the call is indirect and the "no unspecialised call" half fails.
2. **Every element function reaches no observable effect** (§1). Without
   it merging traversals can reorder effects. The pass tests this more
   strictly than §1 words it: `internal/caps` answers a security
   question, and `print` is deliberately ungated there while being
   exactly the effect an interleave exposes. So the allowed set is
   inverted — a program function, walked through, or a codegen runtime
   helper, and nothing else.
3. **No fragment elides an application that would be observable** (§2).
   The three-outcome vocabulary cannot express early exit, so the first
   slice satisfies this by construction — which is the other reason
   `take` is deferred rather than squeezed in.
4. **Float reductions keep index order** (§3). The `step` fragments
   above are already in index order; the obligation is on any later
   vectorizer, not on this pass.

A pipeline failing any of these does not fuse, and says so with the
closed refusal set of `internal/ir/array_pipeline.go` (#9732) rather
than silently allocating per stage — clause 4.

## What the first slice reaches

`map` and `filter` as stages, `fold` and `reduce` as sinks, over 8-byte
elements. The pass is `internal/ir/array_fusion.go`; `FERN_NO_ARRAY_FUSION=1`
turns it off, which is how a miscompilation suspected here is ruled out in
one run rather than by rebuilding the compiler.

The self-host's pass, `examples/self_host/semfuse.fern`, fuses the same
stages and sinks on the typed semantic graphs `semsource` produces, before
ownership is planned, so every self-host backend gains it and the loop is
counted like any other body. It takes any scalar element rather than only
8-byte ones, and a `map` may change the element type, since each value in the
graph carries its own. Its refusals are native's — an element function that is
not a closure built in the same body, or whose body reaches an effect; an
intermediate read by anything but the next stage — plus one native does not
need: the stages and the sink sit in one block with nothing between them that
calls anything. `FERN_NO_ARRAY_FUSION=1` turns it off too.
`FERN_ARRAY_REPORT=1` on a compile prints this pass's actual fusion decisions
and the final R7 map storage decisions; `-array-report` remains the separately
labelled Go analysis. See `ARRAY-ALGEBRA.md` for the closed refusal tags.
The primary fusion report recognizes `map`, `filter`, `fold`, `reduce`,
`scan`, `zip`, `take`, `take_while`, `drop`, `drop_while`, `flat_map`,
`flatten`, `chunks`, `chunks_exact`, `windows`, `partition`, `enumerate`
and `reverse`. Only the first four belong to the implemented fusion algebra;
the others report `operator-outside-algebra`. This report does not inventory
every std/array helper. Calls outside this recognized set produce no site
line, so silence is not evidence that such a call fused or avoided allocation.
`TestSelfHostArrayFusion*` and `TestSelfHostArrayReport*` in
`internal/e2eselfhost` gate the primary path on x86-64, arm64 and wasm32-wasi.

The primary compiler now preserves a locally constructed closure's target
through physical lowering. Captured callbacks still receive their environment;
unknown function values retain indirect dispatch. After fusion, `seminline`
also splices small capture-free callback bodies into the changed callers.
It first prepares eligible callback bodies using helpers exposed by the earlier
ordinary inlining pass. It requires an unread environment parameter, respects
declaration eligibility and `@noinline`, and retains the existing leaf, caller
and splice limits.
`FERN_SEM_INLINE=` disables this additional inlining. Unread capture-free
closure constants are then removed before ownership and register planning.
`TestSelfHostClosureInlineAdmission` checks these proof boundaries and verifies
the resulting typed graphs. `TestSelfHostArrayFusionUsesKnownCallbackBodies`
checks emitted dispatch and executes the value fixture.

Primary runtime parity remains open. On Apple M3 Pro arm64-darwin, 2026-10-05,
nine alternating runs of the existing 2,000-element benchmarks gave these
medians and observed ranges, in ns per round. A two-round pilot preceded the
200-round measurements; checksums agreed throughout.

| Primary compiler variant | map.map.reduce | filter.map.reduce |
| --- | ---: | ---: |
| Before direct closure lowering | 10,505 (10,310-11,240) | 4,957 (4,766-5,168) |
| Direct closure lowering | 8,321 (8,078-8,709) | 2,723 (2,627-2,840) |
| Same-run handwritten control | 7,295 (7,138-7,614) | 2,392 (2,346-2,609) |
| Inlining and dead closure removal, later run | 8,779 (8,397-10,110) | 2,886 (2,739-2,929) |
| Later-run handwritten control | 7,618 (7,303-7,831) | 2,592 (2,446-2,688) |
| Prepared helper callbacks, final run | 7,844 (7,743-8,273) | 2,690 (2,640-2,941) |
| Final-run handwritten control | 7,584 (7,364-9,080) | 2,491 (2,478-3,370) |

The initial closure pass missed benchmark callbacks whose helpers had just
become scalar leaves. Physical inlining removed those calls after ownership
planning, leaving unused closure bookkeeping. Preparing the helper calls before
the closure proof removes that bookkeeping too. In the final paired run, the
version immediately before that preparation measured 8,468 ns (8,167-8,792)
and 2,953 ns (2,804-7,547), respectively. The improvement still does not
establish parity. Pipelines make one allocator call per round for `Some`;
controls make none. Both have zero steady fresh bytes. The dispatch explanation
and September timing results below describe the Go compiler and do not
establish primary parity.

Clause 1's second half — "no unspecialised calls per element" — holds, and it
is not something this pass does by itself. Fusion runs FIRST in
`OptimizeProgram`, so what the later passes see is one loop whose element
functions are locally constructed closures, and `Defunctionalise` resolves
each call to a statically named target. After the full battery the fused
`map.map.reduce` contains **no `OpCallIndirect` at all**.

Be precise about what that does and does not say. Resolving the dispatch is
not the same as removing the call. Whether the call then survives is
`Inline`'s ordinary decision and nothing fusion does: a LEAF element function
is absorbed and the loop body ends up with no call at all, while one that
calls something else stays an `OpCallClosureDirect`. The benchmark programs in
`examples/array_pipeline` are the second kind — their element functions call
`modulus()` — so the fused loop there makes three calls an element, one per
stage plus the sink. Their `call_loop` control makes the same three, which is
how the measurement below separates the cost of calling from the cost of
reaching the callee through a closure.

An earlier version of this section said the element functions were "inlined
outright". That was read off a count of `OpCallIndirect` and `OpCallDirect`
which never looked at `OpCallClosureDirect`, the kind they are dispatched
through when they are not inlined. Both shapes are now pinned in
`internal/ir/array_fusion_test.go`.

Running fusion after `Inline` instead would have left the chain
unrecognisable, since `Inline` rewrites std/array's one-line method
delegates.

Two shapes inside that vocabulary still decline, and both are coverage rather
than correctness:

- **A receiver that is not a named local.** `b.xs.map(f).fold(...)` reads the
  array out of a field, so the receiver is not a slot the argument walk can
  name, and the chain is left alone. A receiver that is a call result does
  fuse — the result lands in a local first.
- **A chain whose element function reaches another chain.** std/array's
  combinators call their element function indirectly, an indirect call is
  counted effectful because its target is unknown here, and that propagates to
  anything calling them. So in `xs.map(x => x + inner(ys))`, `inner`'s own
  chain fuses and the outer one does not. Resolving a locally-constructed
  closure is `Defunctionalise`'s job and it runs later; duplicating it here to
  widen this would be the wrong place.

Measured on `examples/array_pipeline/`, arm64-darwin, 2000 elements over 50
rounds, against `docs/ARRAY-PIPELINE-BASELINE-2026-09.md`'s numbers:

| | allocator calls per round, before | after | hand-written loop |
| --- | ---: | ---: | ---: |
| `map.map.reduce` | 23 | 1 | 0 |
| `filter.map.reduce` | 19 | 1 | 0 |

Cold fresh bytes fall from 49952 and 6944 to 32. The one remaining
allocation per round is `reduce`'s own `Some(acc)` box, which its
`Option[T]` return type requires and which is O(1) rather than linear in
the input — the hand-written loops return a bare `i64` and so pay nothing.
A `fold` sink allocates nothing at all.

Runtime, measured the way the baseline was — callgrind retired instructions,
arm64-linux running natively in `scripts/devbox`, 2000 elements over 30 rounds.

The comparison needs the right control, and the one the baseline shipped is
not it. `loop` spells the element functions' arithmetic out by hand and hoists
`modulus()` above the loop; `closure_loop` calls them through function values,
which is the indirect dispatch fusion removes. Neither is "the same work,
written as a loop". `call_loop` is: a hand-written loop that CALLS the same
element functions the pipeline's lambdas call.

| | unfused ÷ call_loop | fused ÷ call_loop | fused ÷ loop |
| --- | ---: | ---: | ---: |
| `map.map.reduce` | 4.60x | **1.000x** | 1.021x |
| `filter.map.reduce` | 2.73x | **0.841x** | 0.877x |

That is clause 3's runtime half: parity with a hand-written loop doing the
same work, and within 2% of one that hand-inlines the callee bodies as well.
`filter.map.reduce` is below both, which is not a measurement error — its
hand-written loops carry the same `seeded` flag the fused form does, and the
fused form reads each element once where they re-read `xs[i]`.

Getting there took three wrong guesses about the residual, each disproved by
measuring it, and the sequence is worth keeping because the wrong answers were
all plausible:

1. **The per-element `__arr_idx_8_nc` call.** Computing the address inline
   instead measured 0.18%, inside the run-to-run variance of the controls.
   Dropped.
2. **`reduce`'s per-element arrival flag.** Real, worth 3.2%, and shipped —
   the first iteration is peeled when no stage can skip.
3. **The calls to the element functions themselves.** Disproved by the
   `call_loop` control: hand-inlining the callee bodies is worth 1.8% and
   4.0%, and both the fused loop and `call_loop` make three calls an element.

What it actually was: the fused loop reached its element functions through a
closure, so every call site re-read an env pointer out of memory —
`load fn; +ptrW; load` — that is the constant 0 for a closure capturing
nothing. `FoldZeroCaptureEnvLoads` replaces those three memory touches with
the constant, which is the whole of the remaining gap.

How widely that applies is narrower than it first looks, and worth writing
down because the obvious phrasing — "any loop calling a zero-capture closure
was paying it" — is wrong. `ElideClosurePair` already removes the same
sequence, and more, for a slot whose every reader is one of those call sites.
Two microbenchmarks written to show the general case measured IDENTICALLY with
the pass on and off, because that pass had already handled them. What reaches
`FoldZeroCaptureEnvLoads` is a slot that `Defunctionalise` resolved but
`ElideClosurePair` declined — one with a reader that is not a call. The fused
form is such a shape: it re-emits the chain's `__drop_closure_value` calls,
and those are the readers that block the earlier pass.
