# Iterator fusion contract

Plan item **B3** of `docs/NICHE-BORROWS-PLAN.md`. Fern's standing
posture (`LANGUAGE-DIRECTION.md`, "Things deliberately NOT
cribbed") rejects Rust-style lazy iterator chains *until the IR
can fuse them* — "beautiful when the optimizer fuses them,
allocation traps when it doesn't." This doc defines what "can
fuse" must mean before that posture flips, so the future
implementation is built to a contract rather than to vibes.
Source: the strymonas work (POPL 2017, sourced in
`NICHE-LANGUAGE-RESEARCH.md`) — the first stream library to
combine full generality with a zero-overhead guarantee.

## The contract (definition of done)

Adopting lazy iterator chains requires ALL of:

1. **Compositional zero-allocation guarantee.** If each operator
   in a pipeline individually satisfies "no heap allocation, no
   unspecialised calls per element", the COMPOSED pipeline
   compiles to a single loop with no intermediate allocations and
   no per-element closure calls. Compositional means proved per
   operator, not per whole-pipeline pattern-match — a user
   combining any supported operators gets the guarantee without
   the compiler having anticipated that combination.
2. **A specified operator algebra.** The guarantee names its
   operators. Minimum viable set, in difficulty order:
   - `map`, `filter` — trivial (consume-side inlining);
   - `take` / bounded sub-ranging — needs a termination flag
     threaded through the loop;
   - `flat_map` — needs nested-loop generation (defeats simple
     "next()-inlining" fusion schemes);
   - `zip` — the hard one: two producers advancing at potentially
     mismatched rates (filter on one side). strymonas is the
     evidence this is solvable; it is also the natural cut line
     for slice 1 (ship map/filter/take/flat_map fusion, document
     zip as unfused-yet).
3. **The measurement bar: hand-written-loop parity.** Each fused
   pipeline shape gets a benchmark against its hand-written
   `while`-loop equivalent; parity means allocation count equal
   (assert via the RC/alloc counters used by the reuse tests) and
   runtime within noise. strymonas's motivation stands as the
   warning: mainstream library-level streams pay order-of-
   magnitude penalties — if the pass can't hit parity, eager
   combinators + `|>` remain the right default surface.
4. **Failure is visible, not silent.** A chain using an operator
   (or operator position) outside the fused algebra either (a)
   refuses to type as a lazy chain, or (b) compiles with an
   opt-in-visible diagnostic — never silently allocates an
   intermediate per stage. This is the same specified-not-
   best-effort stance as `REUSE-CONTRACT.md`, applied to loops.

## Where it lives

An `internal/ir` pass over the existing cursor-iterator protocol
(`core/iter`'s `has_next`/`value`/`advance` shape and the
`Iterator` trait), NOT a source-level rewrite: the IR is the
target-agnostic layer, so all three backends inherit fusion, and
the pass can consult the same liveness/uniqueness facts the RC
analyses already compute (a fused loop that reuses its
accumulator in place composes with `REUSE-CONTRACT.md` R-shapes).
strymonas achieves its guarantee via multi-stage programming —
if Fern ever grows a comptime (see `COMPTIME-BRIEF.md`), fusion-
as-a-library becomes an alternative host; until then the IR pass
is the path.

## The per-operator proof

Clause 1 is a claim about operators, so it is discharged per operator
rather than per benchmark: `docs/ARRAY-FUSION-OPERATORS.md` gives each
one's `init` / `step` / `finish` fragments, the obligations a fragment
carries, and the argument that concatenating fragments that allocate
nothing yields a loop that allocates nothing. It is written for the
EAGER `std/array` combinators of #9731, and the argument does not depend
on which of the two surfaces the operators came from.

That eager pass is **built** — `internal/ir/array_fusion.go`, covering
`map` and `filter` as stages and `fold` and `reduce` as sinks. It
discharges clause 1 for those operators on the eager surface: the
intermediates are gone and, because fusion runs before
`Defunctionalise`, every per-element call is resolved to a statically
named target. So "no unspecialised call per element" holds. Whether the
call is then removed is `Inline`'s ordinary decision: a leaf element
function is absorbed, one that calls something else stays a direct call.
"No per-element closure call" therefore holds for some chains and not
others, which is weaker than clause 1 reads at first. Clause 2's
harder operators (`take`, `flat_map`, `zip`) are not in it, and clause
3's runtime half has not been remeasured since it landed. None of that
flips the posture on LAZY chains, which is what this document governs —
it removes one of the two reasons the posture existed.

## Trigger conditions

The trigger condition below is **met**, as of
`docs/ARRAY-PIPELINE-BASELINE-2026-09.md` (#9728): the eager combinators
cost 2.6x to 4.4x a hand-written loop on native-built code and
materialize an intermediate per stage that is linear in the input. What
that measurement also found is that the intermediates are not the whole
of it — the per-element indirect call is a quarter to two fifths of the
gap on native-built code and half to three quarters on self-host-built
code, so clause 1's second half ("no unspecialised calls per element")
is load-bearing rather than a refinement.

Build this when a real workload demonstrates the eager
combinators allocating measurably in a hot path (an edge handler
or the self-host compiler's own loops), not before. Until then,
`for x in xs` / eager `std/array` combinators / `|>` (now with
the `_` placeholder) remain the blessed shapes — and `0..n`
ranges already lower to counted loops in `for` position, which is
the fusion sweet spot handled at parse time.
