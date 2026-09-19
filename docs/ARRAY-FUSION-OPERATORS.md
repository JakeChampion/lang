# The per-operator fusion proof

Status: the contract `internal/ir/array_fusion.go` implements (#9731). What
each array operator contributes to a fused loop, and why composing those
contributions gives the guarantee `docs/ITERATOR-FUSION-CONTRACT.md` clause
1 asks for.

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

### `scan(z, h)` — prefix

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

That is a real win of a different kind from the others, and it is the
reason `scan` is in the minimum viable set rather than deferred: it
demonstrates that a materializing operator composes with the same
fragment interface, which is what stops the interface being quietly
specialised to the allocation-free cases.

A `scan` in the middle of a chain ends the fused loop and begins a new
one. Nothing about that is special-cased — a sink is a sink.

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

Both halves of clause 1 hold, and the second one is not something this pass
does by itself. Fusion runs FIRST in `OptimizeProgram`, so what the later
passes see is one loop whose element functions are locally constructed
closures — `Defunctionalise` and `InlineZeroCaptureClosures` then inline them
outright. After the full battery the fused `map.map.fold` contains **no
indirect call at all**: the only calls left are the index helper and the
closure drops. Running fusion after `Inline` instead would have left the
chain unrecognisable, since `Inline` rewrites std/array's one-line method
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

The runtime half of clause 3 is not claimed here. Wall-clock on this
hardware put a pipeline ahead of its own hand-written loop, which is how
`docs/ARRAY-PIPELINE-BASELINE-2026-09.md` came to use callgrind retired
instructions instead; that instrument needs the Linux devbox and the ratio
has not been remeasured since the pass landed. `scripts/array-pipeline-baseline`
runs both compilers over every backend, so it is one command when someone
wants it.
