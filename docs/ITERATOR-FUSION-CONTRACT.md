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

## Trigger conditions

Build this when a real workload demonstrates the eager
combinators allocating measurably in a hot path (an edge handler
or the self-host compiler's own loops), not before. Until then,
`for x in xs` / eager `std/array` combinators / `|>` (now with
the `_` placeholder) remain the blessed shapes — and `0..n`
ranges already lower to counted loops in `for` position, which is
the fusion sweet spot handled at parse time.
