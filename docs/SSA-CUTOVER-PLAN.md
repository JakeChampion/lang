# SSA cutover: the shared lowering, and why Perceus needs it

**Status:** PROPOSED, as input to the `docs/SSA-DECISION.md` re-evaluation due
**2026-09-01**. That doc says: *"If a tripwire fired, write an
`SSA-CUTOVER-PLAN.md`"*. One has. This is that document.
**Owner:** compiler / IR.
**Supersedes nothing yet** — `SSA-DECISION.md` and `SELFHOST-SSA-DECISION.md`
stand until the re-eval acts on this.

## Which tripwire fired

The fourth:

> The flat-IR optimizer in `internal/ir/` grows enough ad-hoc cross-block
> analysis that we'd be reimplementing SSA badly — at which point doing it
> properly wins.

It has, and the RC work is where. Current inventory of hand-rolled
control-flow analysis over the flat op stream:

| | lines | what it hand-rolls |
|---|---|---|
| `internal/ir/rc_analysis.go` | 5458 | the whole ownership plan, over AST + checker tables |
| `internal/ir/verifystack.go` | 746 | operand-stack dataflow with its own scope stack |
| `internal/ir/verifyrc.go` | 374 | backward bracket matching, forward reachability that skips sibling arms |
| `internal/ir/rc_dropguided.go` | 250 | reuse-token flow that "dies at any control-flow join it cannot soundly cross" |
| `internal/ir/rc_cross_branch.go` | 136 | cross-block reuse pairing |
| `examples/self_host/irverifyrc.fern` | 416 | the same reachability walk, again, in Fern |
| `examples/self_host/irverifystack.fern` | 305 | the same stack dataflow, again, in Fern |

Two of those were written this week (#7783, #7791, #7785). Each contains a
`matchIfBackwards`, a `skipToMatchingEnd`, or a `reaches()` that exists solely
because there is no CFG to ask. `internal/ssa` already has dominator trees,
dominance frontiers, def-use chains and a liveness pass — 8,511 lines and 565
tests of exactly this, sitting off the production path.

**Tripwires 1–3 are NOT claimed.** They are about profiled performance (LICM,
GVN, SCCP as a demonstrated bottleneck) and nothing here measures that. This
plan rests on the fourth alone.

## The measurement that makes this actionable

`internal/ir` emits **15,516** reference-count operations across the 1,347
programs the conformance corpus lowers. Of those:

- **10,506** are attributable to a named local (`OpLoadLocal n; OpRcInc`)
- **5,010 — 32% — act on a value that exists only on the operand stack**: a
  call result, a field read. No name, no def-use edge, nothing to own it.

That is the ceiling. Every ownership analysis on this representation is
structurally blind to a third of the reference-count traffic:

- the verifier shipped in #7783 must skip them (its coverage counter reports
  them);
- the ownership signature table (#7786) cannot summarise what it cannot name;
- per-path unit accounting — the Roc-grade certifier, the single largest
  soundness gap in `docs/rc-log/`'s live defect list — is not reachable at all.

It is not an effort problem. Koka analyses Core, Lean analyses LCNF, Roc
analyses LIR, Pen analyses MIR. **Every other Perceus implementation runs its
ownership pass over a named-binding IR.** Fern is the only one that does not,
and it is the only one whose ownership analysis is interleaved with lowering.

## What the current docs get wrong

`SSA-DECISION.md` describes the SSA backends as covering "a subset of the
language", and its reconciliation section records the arm64 corpus
differential's first run finding "four wrong answers and 56 heap SIGSEGVs".
Both are stale.

Measured 2026-08-26 and pinned in `internal/e2e/arm64_ssa_differential_test.go`:

> over 286 corpus programs there is **no SSA coverage gap left at all**: 281
> compared, **0 ssa-refused**, 5 baseline-rejected (two deliberately-invalid
> probes and three that need `subprocess`, which no compiled target provides).

And `internal/e2e/testdata/arm64-ssa-diff-known-divergences.txt` is **all
header and no rows** — "the whole corpus either agrees or is refused". The four
wrong answers and 56 SIGSEGVs were fixed.

**arm64 SSA is corpus-complete and behaviourally identical to the shipping
backend.** The cutover is much closer than the shelve doc reads.

## Actual readiness, per backend

| backend | CLI-wired | corpus differential | state |
|---|---|---|---|
| `arm64ssa` | yes | yes | 281/281 compared, 0 refused, 0 divergences |
| `wasmssa` | yes | no | **single user function only** — measured below |
| `x86_64ssa` | **no** | no | unreachable from the CLI |

The spread is much wider than "arm64 is ahead". One backend is corpus-complete;
the other two cannot compile an ordinary program at all.

Two concrete blockers, and only two:

1. **wasm compiles one function.** Measured 2026-08-29, not inferred:

   ```
   function main(): i32 { ... }                  -> compiles, runs, correct
   function helper(x: i32): i32 { return x+1; }
   function main(): i32 { return helper(9); }    -> REFUSED
   ```
   ```
   wasm/ssa: wasmssa: OpCall to "helper" is neither self-recursion
   (callee == "main") nor a declared import
   ```

   `wasmssa` supports exactly one user function plus declared imports. Any
   program that calls another user function is refused, which is every real
   program — `examples/wasm/shape_area.fern` fails on its first `show_area`
   call.

   So the corpus differential `SSA-DECISION.md` asks for would come back
   almost entirely `ssa-refused`: honest, and nearly empty as a gate. **The
   wasm work is multi-function support in `wasmssa` — a call and
   module-assembly problem — not a test harness.** Build the differential
   after, as the proof, not before as the discovery.

   (When it is built, note the artifact asymmetry: the default backend emits
   a WASI command, `-backend ssa` a core module exporting `main`, so stdout is
   not comparable without `-component-wrap-cli` or a run-mode adapter.)
2. **x86-64 is not reachable.** `x86_64ssa` has `EmitModule` (objects) and
   per-function `EmitAsm`, but no module-level assembly emitter matching
   arm64's `EmitAsmModule`, so `-backend ssa` rejects `-target x86-64-linux`
   outright. This is #6979's complaint — the code-size harness measures a
   pipeline the CLI never runs. Real work, not a wiring line.

## The cutover point

Unchanged from the shelve doc's own rule, which is right: **IR → SSA → *all*
native backends, never one in isolation.** Reason 3 of the shelve still holds —
a half-migrated backend re-introduces the dual-path parity hazard.

The staging that follows from the readiness table:

1. **Give wasm multi-function support**, then build its corpus differential as
   the proof. Not the other way round: measured above, `wasmssa` refuses any
   program that calls a second user function, so a differential built first
   would report `ssa-refused` almost everywhere and gate nothing.
2. **Make x86-64 reachable.** A module-level asm emitter for `x86_64ssa`,
   then the same corpus differential. This is the long pole.
3. **Flip the shared lowering** once all three are differentially clean,
   behind a flag, with the differential oracle byte-identical across interp +
   every backend as the gate.
4. **Move RC insertion onto SSA.** This is the step the whole plan is for.
   Ownership stops being pattern-matching over an op stream and becomes
   ordinary dataflow with names, def-use and phis. The 32% blind spot closes
   by construction.
5. **Then** #7786 is standard interprocedural dataflow, and #7787 / #7789 /
   #7792 become tractable rather than heroic. The certifier (#7782 slice 3)
   lands where it is natural.

Steps 1 and 2 are ordinary engineering with a measurable finish line. Step 4 is
the one that changes what is possible.

## The cheaper route to step 4, which may make steps 1-3 optional

Steps 1-3 exist to make SSA the **codegen** path. But the Perceus unlock does
not need that — it needs SSA as an **analysis** representation.

`ssa.LiftFromIR` already exists, and arm64's differential is evidence the lift
is faithful over the whole corpus: 281 programs lifted, optimised, re-emitted,
and behaviourally identical to the flat backend. Nothing about that result
depends on shipping the SSA backend.

So there is a second path to the thing this plan is actually for:

> Lift IR → SSA for **analysis only**. Run ownership as real dataflow — names,
> def-use, dominance, phis — and map the decisions back onto op positions for
> the existing emitters to consume. Keep all four shipping backends exactly as
> they are.

That closes the 32% blind spot, gives #7786 a representation it can summarise,
and makes per-path unit accounting reachable — without touching `wasmssa`'s
single-function limit or writing x86-64 a module assembler.

It is not free: the decisions have to survive the round trip back to op
positions, and a lift that is faithful for *codegen* is not automatically
faithful for *ownership* (it must preserve the RC-relevant structure, not just
the computed answer). That is the thing to prove first, and it is a much
smaller experiment than either step 1 or step 2.

**Recommendation: try this before committing to the backend cutover.** If the
round trip holds, the cutover becomes a separate question decided on codegen
merits alone, and Perceus stops waiting on it.

## The conflict that has to be resolved with it

`docs/SELFHOST-SSA-DECISION.md` (#4391, 2026-07-03) decided the **opposite**
for the self-host: the stack IR is its single production lowering, SSA
`build_func` demoted to opt-in, `SELFHOST-SSA-ALWAYS.md` shelved. The stated
reason is exactly right and applies here in reverse:

> Every week both advanced was a week of work one of them would delete.

If native cuts over to SSA while the self-host stays on the stack IR, that
divergence returns at a larger scale — and **goal 2 is caught in the middle**,
because the Perceus port is precisely what has to be mirrored across whichever
representation each side settles on. The 71k-line `irlower.fern` is the current
price of having no pass to mirror; forking the representations makes that
permanent.

**So #4391 is reopened by this plan, not after it.** The encouraging part: the
shelved self-host plan records SSA reaching **100% per-function coverage**
there via `-ssa-scan`, blocked only on a Phase-4 whole-compiler memory wall. The
mirror may be nearer than the native subset language suggests.

## What this buys, stated as the goal

Fern already scores 8/10 on algorithmic completeness against the field —
drop-guided/frame-limited reuse (only Koka also has it), cross-kind reuse
donors (looser than Lean's relaxed reuse), fip/fbip verified against emitted
ops (Koka's Core-level check cannot do that), four backends. See #7784.

The gap to first is not more optimisations. It is structure (4/10) and
soundness (6/10), and both are downstream of the representation. **Koka has the
algorithms and no verifier; Roc has the verifier and not the algorithms.**
Nothing in the field is both. That is the position this plan is for, and the
32% blind spot is what currently makes it unreachable.

## Explicitly not claimed

- No performance argument. Tripwires 1–3 are unmeasured and this plan does not
  rest on them.
- No claim that wasm or x86-64 SSA are correct — that is what steps 1 and 2
  are for, and arm64's first differential run is the reason to assume nothing.
- No estimate. The steps have finish lines; how long they take is not
  something this document knows.
