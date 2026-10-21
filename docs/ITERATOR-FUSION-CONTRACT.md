# Iterator fusion contract

Status: [policy] the contract iterator fusion must meet; the eager pass and part of the lazy surface are built.

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

`compiler/semfuse.fern`, a pass over the typed semantic graphs every
backend lowers from, reading the cursor-iterator protocol (`core/iter`'s
`Iterator` trait) rather than rewriting source: that layer is
target-agnostic, so all three backends inherit fusion, and the pass runs
ahead of the ownership planning the RC analyses compute (a fused loop that reuses its
accumulator in place composes with `REUSE-CONTRACT.md` R-shapes).
strymonas achieves its guarantee via multi-stage programming —
if Fern ever grows a comptime (see `COMPTIME-BRIEF.md`), fusion-
as-a-library becomes an alternative host; until then the graph pass
is the path.

## The per-operator proof

Clause 1 is a claim about operators, so it is discharged per operator
rather than per benchmark: `docs/ARRAY-FUSION-OPERATORS.md` gives each
one's `init` / `step` / `finish` fragments, the obligations a fragment
carries, and the argument that concatenating fragments that allocate
nothing yields a loop that allocates nothing. It is written for the
EAGER `std/array` combinators of #9731, and the argument does not depend
on which of the two surfaces the operators came from.

That eager pass is **built** — `compiler/semfuse.fern`, fusing `map`,
`filter`, `take`, `take_while` and `flat_map` stages into `fold`,
`reduce` and `scan` sinks. It discharges clause 1's first half for those
operators on the eager surface: the intermediates are gone, and a
`flat_map` is an inner loop rather than an array per element. Whether a
per-element call is then removed is the inliner's ordinary decision: a
leaf element function is absorbed, one that calls something else stays a
direct call, so "no per-element closure call" holds for some chains and
not others. `zip` is not in it. None of that flips the posture on LAZY
chains, which is what this document governs — it removes one of the two
reasons the posture existed.

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

#6795 builds the lazy surface on top of the eager pass; the next section
records what it fuses.

## The lazy surface

`core/iter`'s `Iterator` trait carries the lazy adapters as default
methods, `map`, `filter`, `take`, `skip`, `flat_map`, `enumerate` and
`zip`, and the sinks `fold`, `count`, `collect`, `find`, `any` and `all`.
No adapter returns an array; a chain ends in `collect` to build one.
`compiler/semfuse.fern` fuses a chain that:

- starts at `iter.of(xs)` or `iter.range(lo, hi)`;
- runs `map`, `filter`, `take` and `flat_map`;
- and ends in `fold` or `count`.

The result is the single loop the eager surface gets. The iterator, the
adapters, and the `Option` and tuple every `next` would return are all gone:
`TestSelfHostIterFusionAllocatesNothing` asserts that a fused chain allocates
nothing at all. A `flat_map` chain still allocates the arrays its function
returns, as the eager stage does. A call counts as an adapter, root or sink only when its body
came from core/iter, as the eager pass trusts only std/array's.

These are not fused yet, and `FERN_ARRAY_REPORT` names each one (clause 4):

- `skip`, `enumerate` and `zip`;
- the `collect`, `find`, `any` and `all` sinks;
- `range_incl`, and any iterator other than the two roots;
- a chain whose intermediate has a second use.

Each runs as written, one `next` at a time.
