# The per-operator fusion proof

Status: `internal/ir/array_fusion.go` (#9731) and the primary compiler's
`compiler/semfuse.fern` (#11072) implement the `map`/`filter` stages
and `fold`/`reduce` sinks below. The primary compiler also implements `scan`
as a materializing sink. This document states what each operator contributes to a
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

When no stage can skip an element, both compilers instead test for an empty
input, run the map fragments on element zero to seed the accumulator, and
start the loop at element one. That removes the arrival flag from the loop.
Empty input still returns `None`, and a singleton never calls the combining
function. A chain containing a filter retains the flag because its first
arriving element is not known in advance.

`reduce` is where `docs/ARRAY-ALGEBRA.md` §3 binds: `h` is applied in
index order and the fragments above do not license any other order. A
tree reduction is a different `step`/`finish` pair and needs the
explicit opt-in that document requires.

### `scan(z, h)`: prefix sink

The primary compiler fuses map/filter chains into scan. The Go bootstrap
compiler retains its eager scan implementation.

```
init:    acc = z; out = <empty buffer with capacity n>
step:    acc = h(acc, x); out.append(acc)
finish:  result is out
```

Scan materializes one prefix value per arriving element, excluding the initial
seed. The original input length is an upper bound after any number of filters,
so one reservation suffices even when the output length is unknown. Filtered
elements carry both the accumulator and output unchanged to the next iteration.
Append updates the actual length only after initializing the next element.

The typed `array_reserve` operation requires an owned scalar array and an i32
capacity. Physical lowering keeps the layout in each backend: packed bytes or
word slots on native targets, and the appropriate element stride on Wasm.
Negative capacities and Wasm byte-size overflow abort before allocation.
Ownership planning accounts for the output array through its loop phi.

A scan in the middle of a chain ends one fused loop and can feed another.
Its observable prefix array remains materialized. Effectful callbacks, shared
intermediates and non-scalar accumulators retain the existing refusal rules.
`FERN_ARRAY_REPORT=1` identifies fused scans with
`storage=no-intermediate-arrays/one-output-buffer`.

`TestSelfHostArrayFusionScan*` checks values, callback effects, scalar widths,
floating order and RC balance on all three primary targets. Allocation tests
require exactly one output allocation, including empty and all-filtered scans,
with input construction and verification excluded from the count. The repeatable
native benchmark is `examples/array_pipeline/map_scan.fern`; build it with
fusion enabled and disabled and run `2000 200 0` or `2000 200 1` for filtered
input. It verifies every prefix outside the timed region.

On Apple M3 Pro arm64-darwin, 2026-10-05, nine alternating processes per
build used 2,000 input elements. A two-round pilot preceded 200 rounds with
only that argument changed. Every prefix and both builds' checksums agreed.

| Pipeline | Fusion disabled, ns/scan | Fusion enabled, ns/scan | Allocator calls, disabled/enabled | Fresh bytes, disabled/enabled |
| --- | ---: | ---: | ---: | ---: |
| map.scan | 8,318 (8,133-8,578) | 2,120 (2,000-2,241) | 4,000 / 200 | 40,960 / 16,384 |
| filter.map.scan | 6,761 (6,445-7,778) | 1,612 (1,503-1,866) | 5,400 / 200 | 10,240 / 16,384 |

Times are medians with observed ranges; allocation counters cover all 200
calls. Reserving the input-length bound trades spare capacity for avoiding
growth. The filtered case keeps one element in three and used more fresh
bytes with fusion, as the table shows. The enabled benchmark's native text
section was 22,024 bytes, versus 22,312 with fusion disabled.
The compiler's native text grew by 7,136 bytes for the scan graph construction,
typed reservation checks, ownership integration and three backend emitters.
No size baseline was changed.

The benchmark also accepts `loop` as its fourth argument, selecting a
handwritten while/append implementation with the same prefix semantics.
For example, `2000 20000 1 loop` runs the filtered control. The language has
no public scalar-array capacity reservation, so this control grows its output
through ordinary append; the fused loop reserves its input-length bound once.

With the final ready-successor layout described below, a two-round pilot and
200-round run preceded 20,000 rounds, changing only the round count. Nine
alternating processes per variant on the same Apple M3 Pro gave:

| Pipeline | Fusion disabled, ns/scan | Fusion enabled, ns/scan | Handwritten loop, ns/scan |
| --- | ---: | ---: | ---: |
| map.scan | 8,936 (8,645-9,212) | 2,301 (2,184-5,039) | 2,609 (2,563-3,465) |
| filter.map.scan | 7,069 (6,751-7,530) | 2,016 (1,970-2,850) | 2,487 (2,364-2,826) |

All prefixes and checksums agreed. Across 20,000 scans, the fused variants
made 20,000 allocator calls each. The handwritten loops made 200,000 and
180,000 calls, respectively, versus 400,000 and 540,000 with fusion disabled.
Fused fresh bytes were 16,384 in both cases. The handwritten loops used
20,480 and zero fresh bytes, while the eager variants used 40,960 and 10,240.
Zero fresh bytes in the filtered control reflects freelist reuse, not zero
allocations. The fused output's larger reserved capacity remains a cost even
though it avoids growth calls. Empty, singleton, short and 257-element inputs
also pass the benchmark's prefix checks with both variants and fusion settings.

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

The self-host's pass, `compiler/semfuse.fern`, fuses the same
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
and `reverse`. Only the first five belong to the primary fusion algebra;
the others report `operator-outside-algebra`. This report does not inventory
every std/array helper. Calls outside this recognized set produce no site
line, so silence is not evidence that such a call fused or avoided allocation.
`TestSelfHostArrayFusion*` and `TestSelfHostArrayReport*` in
`internal/e2eselfhost` gate the primary path on x86-64, arm64 and wasm32-wasi.

The primary compiler now preserves a locally constructed closure's target
through physical lowering. Captured callbacks still receive their environment;
unknown function values retain indirect dispatch. After fusion, `seminline`
also splices small capture-free callback bodies into the changed callers.
It prepares callback helpers with the ordinary inliner in a scratch graph,
then requires each resulting callback to satisfy the small scalar-leaf limit.
Only fused callers receive these callback bodies. Fusion precedes general
single-use inlining, which would otherwise absorb the combinator calls.
The callback proof requires an unread environment parameter, respects
`@noinline`, `fip` and `fbip`, and retains the leaf, caller and splice limits.
`FERN_SEM_INLINE=` disables this additional inlining. Unread capture-free
closure constants are then removed before ownership and register planning.
`TestSelfHostClosureInlineAdmission` checks these proof boundaries and verifies
the resulting typed graphs. `TestSelfHostArrayFusionUsesKnownCallbackBodies`
checks emitted dispatch and executes the value fixture.
It also requires a single bound test in each emitted map-only reduction loop.
`TestSelfHostArrayFusionSeededReductionBits` checks empty and singleton inputs,
captured map callbacks and index-order floating arithmetic on all three targets.
Arithmetic NaNs follow FS-04; other result bits remain exact.

After fusion, `semoption.fern` splits private `Option[scalar]` values into a
boolean tag and a scalar payload before ownership planning. The proof covers
the complete component connected by copies and phis, including loop phis.
Every origin must be a local constructor, and every use must be a tag test,
payload read, copy or phi. A return, call, container store, closure capture or
external origin keeps the whole component boxed. Counted payloads, finalizers
and source debug bindings also retain their existing representation.

Some supplies its payload directly; None supplies an unobservable typed zero.
Tag and payload phis follow the original predecessor order. Repeated reads
remain reads, and escaping Options keep their public ABI. The scalar-option
tests require zero allocations for local reductions and the expected box for
escaping results on all three targets. They cover empty and rejected inputs,
integer and floating widths, booleans, branch/loop joins and RC balance.
Floating payload copies retain exact bits, including NaNs. The direct semantic
admission fixture verifies both graphs and checks refusal boundaries.

The measurements below track the primary compiler's path to the handwritten
controls. On Apple M3 Pro arm64-darwin, 2026-10-05,
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

Peeling the first mapped element removes the per-iteration arrival test in
the primary compiler too. A subsequent run with the same pilot and measurement
protocol measured map.map.reduce at 7,316 ns (7,047-11,208), versus 7,782 ns
(7,386-7,901) before peeling and 7,173 ns (6,997-8,520) for the handwritten
control. The unchanged filtered pipeline measured 2,558 ns (2,508-2,806),
with a 2,421 ns (2,350-2,642) control. Allocation counts and checksums were
unchanged. These overlapping ranges do not establish runtime parity.

Splitting private scalar Options removes that remaining allocation. A two-round
pilot and 200-round run passed before scaling only the round count to 20,000,
with nine alternating processes per variant and the same 2,000-element inputs.
The longer run separates the filtered pipeline's remaining runtime gap from
the short-run variation:

| Pipeline | Before Option splitting, ns/round | After splitting, ns/round | Same-build handwritten control, ns/round |
| --- | ---: | ---: | ---: |
| map.map.reduce | 7,634 (7,603-7,714) | 7,584 (7,549-7,649) | 7,598 (7,556-8,327) |
| filter.map.reduce | 2,724 (2,709-2,747) | 2,722 (2,703-2,736) | 2,540 (2,524-2,604) |

Both optimized pipelines and their controls make zero allocator calls and use
zero steady fresh bytes; the prior pipelines make 20,000 calls. Checksums agree.
The mapped reduction reaches the control's measured timing range, but the
filtered reduction remains slower. Removing the box alone does not close that
gap. The native compiler text grew by 11,616 bytes for the component analysis
and typed scalar rewrite. No size baseline was increased.

The final layout pass follows a ready successor before falling back to block
storage order. It still requires every forward predecessor to be ready and
keeps natural-loop regions contiguous. This lets accepted filter work fall
through instead of jumping across a rejection block. Direct layout tests cover
reversed storage order, a join with a pending predecessor, nested loops with
breaks and back edges, and refusal of irreducible graphs.

After a two-round pilot, the same nine-process alternating protocol ran at
200, 20,000 and 200,000 rounds, changing only that argument. At 20,000 rounds,
the filtered pipeline measured 2,417 ns (2,405-2,552), versus 2,550 ns
(2,514-2,776) before the layout change and 2,397 ns (2,354-2,472) for its
same-build control. The longest run retained more timing variation:

| Pipeline | Previous layout, ns/round | Ready-successor layout, ns/round | Same-build handwritten control, ns/round |
| --- | ---: | ---: | ---: |
| map.map.reduce | 7,253 (6,680-10,993) | 7,269 (6,809-8,268) | 7,206 (6,800-8,682) |
| filter.map.reduce | 2,625 (2,546-2,936) | 2,570 (2,437-3,335) | 2,475 (2,371-2,613) |

Both pipelines now fall within the observed control timing ranges. A separate
native ARM64 Linux Callgrind 3.19.0 run checks instruction cost without relying
on those timing ranges. A two-round pilot preceded 200 rounds of the same
2,000-element programs. Counts cover each complete benchmark process:

| Pipeline | Previous layout, instructions | Ready-successor layout, instructions | Same-build handwritten control, instructions |
| --- | ---: | ---: | ---: |
| map.map.reduce | 12,693,586 | 12,691,851 | 13,096,956 |
| filter.map.reduce | 9,106,961 | 9,246,669 | 9,642,513 |

Every variant agreed on checksums and reported zero steady allocator calls and
fresh bytes. The new filtered layout executes more instructions than
the old layout, but both pipelines remain below their handwritten controls.
The native timing and instruction results meet the measured control comparison
on these hosts; they do not establish a speedup on every machine or target.

The compiler reproduces itself byte-for-byte from stage 3 to stage 4. Its
native text shrank from 11,340,716 to 11,330,152 bytes. Nine alternating
checker-driver assembly compilations measured medians of 2.428 seconds before
and 2.374 seconds after, with overlapping ranges of 2.038-2.968 and
2.034-3.801 seconds. That does not establish a compiler speedup. No size or
performance baseline was raised.

### Integration with single-use inlining

The integration exposed a callback whose shared helper became a leaf only
after the first scratch inlining pass. Revisiting eligible callbacks with the
updated leaf table removes the remaining closure bookkeeping. The second
pass retains the ordinary budgets and annotation guards, and the final
capture-free scalar admission still applies. A dispatch regression calls the
helper independently to keep the single-use inliner from hiding this case.

With this fix and main through `66d7deb95`, the compiler reaches a
byte-identical stage-3/stage-4 fixed point. Native text is 10,382,448 bytes,
versus 10,374,416 before the fix and latest parser/Wasm integration, and
10,749,040 before the general-inliner integration. These comparisons include
main's changes and do not attribute the size differences to fusion alone.

Fresh Apple M3 Pro measurements used a two-round pilot followed by nine
alternating 20,000-round runs, changing only the round count. All outputs
agreed. The subsequent parser/Wasm integration left all eight benchmark
programs' assembly identical on both ARM64 targets.

| Pipeline | Current pipeline, ns/round | Same-build handwritten control, ns/round | Eager scan, ns/round |
| --- | ---: | ---: | ---: |
| map.map.reduce | 6,918 (6,485-7,407) | 7,125 (7,035-8,076) | n/a |
| filter.map.reduce | 2,560 (2,462-2,607) | 2,565 (2,492-2,591) | n/a |
| map.scan | 2,211 (2,195-2,261) | 2,590 (2,550-4,386) | 8,762 (8,649-9,034) |
| filter.map.scan | 2,044 (2,003-2,104) | 2,419 (2,372-2,776) | 7,068 (6,802-7,649) |

Both reductions and their controls still make zero steady allocator calls and
use zero steady fresh bytes. Both scans make exactly 20,000 allocator calls,
one per result. Their eager forms make 400,000 and 540,000 calls; handwritten
forms make 200,000 and 180,000. Fresh-byte counts remain 16,384 for either
fused scan, 40,960/10,240 for eager scans and 20,480/0 for handwritten scans.
The zero reflects allocator reuse, not an absence of allocations.

Both reduction timing ranges overlap their controls. Native ARM64 Linux
Callgrind, with a two-round pilot followed by 200 rounds of 2,000 elements,
also confirms that the fix removes the instruction excess. Complete-process
counts are shown below; these are instruction counts, not elapsed timings.

| Reduction | Before fix | Fixed pipeline | Fixed handwritten control |
| --- | ---: | ---: | ---: |
| map.map.reduce | 14,308,973 | 12,692,466 | 13,087,952 |
| filter.map.reduce | 9,648,203 | 9,242,724 | 9,376,471 |

Every profiled variant produces the same checksum with zero steady allocator
calls and fresh bytes. Together with the scan results, these measurements
establish parity for the documented workload and controls. They do not claim
identical speed for all inputs or machines.

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
