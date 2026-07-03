# Extracting native Perceus RC into a discrete pass — rollout tracker

Status: **slices 1–4 landed** (2026-07-03). Tracking issue: **#4393**.
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
  reuse initially still registered its scrutinee pairing in
  `reuseSources` mid-lowering — folded into the plan by #4475
  (`computeConsumingMatchReuse`), so the plan is immutable once
  `computeRcAnalyses` returns.
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
- **Slice 4 (landed): first true post-lowering insertion + the
  feasibility boundary.** The consumed-param entry retain-incs are
  now spliced into the LOWERED `[]Op` after the whole body is
  built (`insertConsumedParamEntryIncs`, `rc_insert.go`): lowerFunc
  captures the prologue-boundary op index + position, and the pass
  inserts the inc sequence there post-hoc — byte-identical because
  the splice allocates no slots and nothing records absolute op
  indices (control flow is depth-relative). That is the template
  for further conversions: capture an anchor, emit nothing
  in-build, splice on the finished stream, A/B the result.

  The honest boundary, measured against the byte-identity
  constraint (not against taste):
  - **Exit dec sweep**: WAS blocked as a post-pass — each
    `return`'s sweep could allocate fresh scratch slots (the
    enum-param tag stash, #2828) whose numbering interleaved with
    body scratch. #4476 removed the blocker: every inline enum
    slot-drop now shares ONE per-function tag stash
    (`b.enumDropTagSlot`, allocated on first use), so the sweep
    allocates nothing after its first enum drop. The change is
    deliberately NOT byte-identical (scratch slots renumber,
    frames shrink); its characterisation oracle proved the entire
    self-host-corpus output identical modulo frame offsets /
    frame sizes on x86-64 and identical after frame-access-form
    normalisation on arm64. Converting the sweep itself to the
    anchor→splice template is now unblocked.
  - **Alias incs / overwrite decs / precise drops / reuse
    plumbing**: inherent to expression lowering — they are part of
    each expression's value production, and their positions are
    AST-statement facts the op stream does not preserve. Deriving
    identical placement from lowered IR alone means reconstructing
    statement boundaries — a reimplementation, not a refactor
    (`RC-PERCEUS-SELF-HOST-IR.md` §1's premise correction: the
    analyses are AST-level *by design*, and that is faithful for
    the goal-2 port too).

  Net: "a discrete IR→IR pass" lands as *discrete stages around
  the builder* — plan (rc_analysis.go) → in-build insertion at
  named sites (rc_insert.go) → post-lowering splices where anchors
  suffice — rather than a monolithic post-pass. The goal-2 port
  consumes exactly this decomposition: port each analysis as a
  pure AST→tables function (differential-diff the tables), then
  port the insertion helpers site by site.
- **Paired simplifications** (same review; #4474 epic — landed):
  the type-classification predicates consolidated into
  `rc_caps.go` behind a documented capability matrix, with the
  layered tracked-sets made explicit (`arrElemIsRcTracked` ⊂
  `rcTrackedSlotType` ⊂ the sweep set) — #4477; one per-param
  ownership verdict ladder (`paramVerdict`: NotOwnedType / Owned /
  Borrowed / ReadOnlyComparator / TrmcExcluded / TrmcConsume)
  replacing the paramBorrowable trio — #4478; the duplicated
  last-use micro-analyses factored behind `identOrder`/`isLast` +
  `stmtReferencesName` — #4480; the C2 consuming-match pairing
  folded into the plan (`computeConsumingMatchReuse`) — #4475.
- **Stage-boundary alignment** (related, not blocking): native
  runs closure conversion inside `ir.LowerWith` while the
  self-host runs `lift_lambdas` as an explicit pass — align the
  boundaries (either side) before the goal-2 port deepens.
