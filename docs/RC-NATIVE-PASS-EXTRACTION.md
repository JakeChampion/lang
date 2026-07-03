# Extracting native Perceus RC into a discrete pass — rollout tracker

Status: **slices 1–3 landed** (2026-07-03). Tracking issue: **#4393**.
Convergence-debt tracker: #4451 (this is a refactor of existing
native surface, not new native-only surface, but it reshapes the
exact layer goal 2 ports — keep it visible there).

## Why

Native RC is woven through the ~20k-line `internal/ir/ir.go`
AST→IR builder: the decision analyses (borrow inference, consumed
params, free eligibility, moves, precise drops, reuse) and the Op
emission (alias incs, the exit dec sweep, drop glue) are
interleaved with lowering. Goal 2 (porting Perceus to the
self-hosted compiler) has to port *something*, and porting an
entangled builder is strictly harder than porting a clean
`lower → rc-insert → optimize` pipeline. #4393 proposes extracting
RC insertion into a discrete pass **natively first**, as a pure,
oracle-gated refactor — emitted output stays byte-identical at
every slice — so the port becomes a per-pass differential exercise
instead of a monolith transplant.

One premise to keep straight (from `RC-PERCEUS-SELF-HOST-IR.md` §1):
the native Perceus *decision* analyses are **AST-level** — they read
`fn.Body` and produce side-tables keyed by local name / AST node;
none of them are `[]Op`-keyed. So "a discrete pass" decomposes into
two separable moves:

1. **Analyses as a discrete stage** — computed once, up front, in
   one place, as pure functions producing a per-function plan
   ("analyses computed once, carried as parallel arrays", the
   self-host's existing style).
2. **Insertion as a discrete stage** — the Op-emitting half pulled
   out of expression/statement lowering, consuming the plan.

## Oracle (every slice must pass it)

- **A/B byte-identity**: build `cmd/fern` from the base commit and
  from the slice, compile the full self-host compiler with both,
  and `cmp` the outputs. Slice 1 verified: `asm_run.fern` →
  x86-64 `.s` (24.8M lines) and arm64 `.s` (18.2M lines), and
  `wasm_ir_run.fern` → wasm component (26.2MB binary), all
  byte-identical, plus every `examples/*.fern` on all three
  targets.
- The existing rc-placement tests (`internal/ir/*_test.go`
  op-count pins), the lowering determinism guard
  (`determinism_test.go`, `Program.String()`), and the e2e
  differential suites.

## Slices

- **Slice 1 (this one): carve the decision analyses out of the
  builder.** All Perceus decision analyses moved verbatim from
  `ir.go` to `internal/ir/rc_analysis.go` (~1.9k lines): the
  whole-program facts (`inferParamEscapes`,
  `findReturnsNoParamEscape`, `computeReadOnlyComparators`) and
  the per-function analyses (`computeConsumedParams`,
  `computeFreeEligible` + `rhsTainted`, `computeMovedLocals` +
  `markConstructionMoves`, `computeArraySetIncs`,
  `computeReuseSources`, `computePreciseDrops` + its alias/
  control-flow helpers, `needsRcIncOnAlias`). New single
  per-function entry point `(*builder).computeRcAnalyses()`
  replaces the inline assignment block in `lowerFunc`. Pure code
  motion — no signature, ordering, or behaviour change.
  (`computePreciseDrops` keeps its later call site in `lowerFunc`:
  `preciseDroppableType` → `dropFnNameFor` *records* into the
  shared `genEnumDrops`/`genTupleDrops` registries, so its call
  position is observable in generated-drop-fn order.)
- **Slice 2 (landed): group the plan.** The per-function decision
  tables (consumedParams / freeEligible / movedLocals / moveSites /
  arraySetInc / reuseSources / reuseConsumed / preciseDrops) moved
  off the builder into an explicit `rcPlan` struct (`b.rc`),
  constructed by `computeRcAnalyses`. This is the struct goal 2
  mirrors as parallel arrays. Two documented wrinkles:
  `preciseDrops` is filled at its later `lowerFunc` call site (the
  drop-fn registry-order constraint), and the C2 consuming-match
  reuse still registers its scrutinee pairing in `reuseSources`
  mid-lowering — an insertion-time decision a later slice should
  fold into the plan.
- **Slice 3 (landed): carve the insertion helpers.** The
  Op-emitting RC half moved verbatim to `rc_insert.go` (~2.7k
  lines, 44 functions): the exit dec sweep
  (`emitRcDecLocalsAtExit*`), precise drops, owned-temp stack
  drops, alias incs, reinit-overwrite / reuse-site old-field
  drops, the owned-temp classifiers (`freshOwnedRcTempType` /
  `ownedCallResultType` / `reclaimableMatchScrutinee`), and the
  whole drop-specialisation subsystem (`dropFnNameFor` routing +
  the `gen*DropFn` bodies the LowerWith worklist materialises).
  Still called from the same lowering sites in ir.go (in-build
  insertion); `ir.go` is down from 20.1k to 15.2k lines.
- **Slice 4+: true post-lowering insertion.** Where the plan
  allows, replace in-lowering emission with insertion on the
  lowered `[]Op` (entry prologue and exit sweep first — they sit
  at structural boundaries). Each conversion is A/B'd with the
  oracle above; anything whose placement depends on mid-lowering
  state (scratch-slot allocation, registry recording order) stays
  in-lowering until the dependency is broken.
- **Paired simplifications** (same review, each shrinks what the
  port mirrors): unify the type-classification predicates
  (`isOwnedByDefaultType`, `preciseDroppableType`,
  `typeIsStringArrayFree`, `enumRcPayloadsEligible*`, …) into one
  capability table; one per-param ownership verdict enum
  (borrowed / owned / consumed-threaded / read-only-comparator);
  factor the duplicated last-use micro-analyses.
- **Stage-boundary alignment** (related, not blocking): native
  runs closure conversion inside `ir.LowerWith` while the
  self-host runs `lift_lambdas` as an explicit pass — align the
  boundaries (either side) before the goal-2 port deepens.
