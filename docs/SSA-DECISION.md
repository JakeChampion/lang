# SSA: ship-or-shelve decision

**Status:** OPEN for codegen; SHIPPED for analysis. The layer is **not**
shelved — it carries the ownership/Perceus analysis today, and its emitters are
under active measurement for #8822. The cutover has not been scheduled and has
not been declined.
**Next decision point:** not a date — whether to fund closing `x86_64ssa`'s
helper-coverage gap so `coreutils/sort.fern` builds under `-backend ssa` and
can be measured (#8822, #8047).
**Owner:** compiler / IR.

Read "Where this stands (2026-09-15)" below FIRST: it corrects a stale claim in
the 2026-09-02 section above, which says tripwires 1–3 are unfired. One of them
has fired.

## The question

`internal/ssa/` is a fully-built SSA framework — construction (dominator
tree, pruned SSA via dominance frontiers, phi insertion) plus a real pass
suite (`constfold`, `cse`, `licm`, `trivialphi`, `blockmerge`,
`branchfold`, `strength`, `sccp`). It is the second-largest test surface in
the repo (~469 tests). **But it is not on any production path.** The
shipping backends — `internal/codegen/{arm64,x86_64,wasmbin}` — all consume
the flat, structured-control-flow `ir.Program` and run only the
peephole-grade passes in `internal/ir/` (`fold`, `dce`, `copyprop`,
`constprop`, `inline`, `strength`, `tco`, `defunctionalise`). Every consumer
of the SSA layer is behind `-backend ssa`: `wasmssa`, `arm64ssa`, and — since
2026-09-01 — `x86_64ssa`. None is the default for any target.

So we are carrying a substantial, well-tested optimization framework whose
benefit to shipped artifacts is currently zero. This doc forces the call:
either route a production backend through SSA on a date, or formally shelve
the migration so the code's status is explicit rather than ambient.

## Decision: shelve, with a tripwire

We **shelve** the SSA-on-production migration for now and keep `-backend ssa` (wasm) as
the experimental proving ground. Rationale:

1. **It is not load-bearing for current priorities.** The active fronts are
   the RC + Perceus + FBIP memory work (`RC-PERCEUS-PLAN.md`,
   `RC-STRINGS-PLAN.md`) and self-hosting. Neither is bottlenecked on
   mid-level SSA optimizations; both are bottlenecked on *correctness* of
   reclamation and codegen parity. Spending the cutover budget now competes
   directly with the project's stated goals.

2. **The target workload doesn't yet demand it.** Fern's stated use case is
   short-lived CLI tools and edge handlers. For those, startup latency and
   binary size dominate; aggressive loop optimization (LICM, SCCP, GVN-class
   CSE) is not where the wins are. The peephole + TCO + inline pipeline
   already collapses the representative example in the README to a single
   `const; return`.

3. **A half-migrated backend is worse than none.** Routing one production
   backend through SSA while the others stay on flat IR re-introduces the
   exact dual-path parity hazard we work hard to avoid elsewhere (every
   feature must land on every backend). A cutover only makes sense if it is
   the shared lowering all native backends consume.

4. **Shelving ≠ deleting.** The framework stays, stays tested, and stays
   exercised through `-backend ssa` (wasm). We are deferring the *production cutover*,
   not abandoning the investment.

## Tripwires (any one flips this back to "schedule the cutover")

- A profiled, real Fern program (not a microbenchmark) where the missing
  SSA-class optimization — LICM, global CSE/GVN, or SCCP — is the
  demonstrated bottleneck, and the flat-IR peephole passes provably cannot
  reach it.
- The self-hosted compiler's own generated code becomes a measured
  bottleneck for `make distcheck` / bootstrap turnaround in a way SSA passes
  would address.
- `-backend ssa` (wasm) reaches feature parity with `wasmbin` and outperforms it on the
  e2e corpus by a margin that justifies making it the default wasm path
  (which would make SSA production by definition).
- The flat-IR optimizer in `internal/ir/` grows enough ad-hoc
  cross-block analysis that we'd be reimplementing SSA badly — at which
  point doing it properly wins.

## Re-evaluation input (2026-08-29)

**Tripwire 4 has fired**, and two claims in this document are stale. See
`docs/SSA-CUTOVER-PLAN.md` for the evidence: the ad-hoc control-flow analysis
inventory in `internal/ir` (and its self-host mirrors), the measurement that
32% of the IR's reference-count operations act on unnamed operand-stack values,
and the readiness of the three SSA backends.

Stale here: arm64 SSA is no longer "a subset" — 281 of 286 corpus programs
compared, 0 refused, 0 divergences, and the known-divergences file is empty.
The four wrong answers and 56 SIGSEGVs recorded below were fixed.

Tripwires 1-3 remain unfired and unmeasured.

## Re-evaluation input (2026-09-02) — the date passed; the call is OPEN

The 2026-09-01 date arrived and nothing was recorded against it. This section
is the "fresh numbers" half of the procedure below, so the date does not rot a
second time. **It does not make the decision**, which is the owner's: neither
"re-shelve and set the next date" nor "schedule the cutover" is chosen here.

Tripwire state is unchanged from 2026-08-29: **4 fired, 1-3 unfired and
unmeasured.** `docs/SSA-CUTOVER-PLAN.md` was written, as a fired tripwire
requires.

What moved since, all on the x86-64 backend the plan named as the long pole:

- **It is reachable from the CLI** (#8012). The plan's stated blocker — no
  module-level assembly emitter — was stale; `x86_64ssa.EmitAsmModule` had
  been there all along and nothing selected it.
- **`EnumSentinel` landed** (#8044), which was the emit-stage long pole, along
  with a wrong-answer bug it exposed in the rc helpers: five of them returned
  the rc header where the IR contract says they return their pointer.
- **Coverage has two numbers and they are far apart.** Emitting assembly:
  58 → 256 of 317 corpus programs. Producing a runnable binary: **8 → 9.**
- **The helper gap is 84 symbols** (#8047), with a step-function unlock curve —
  nothing moves until the eleventh, 19 reach 152 programs, then it flattens.
  `x86_64ssa` has 13 helper emitters against `arm64ssa`'s ~120.

Two things that bear on the choice and are easy to lose:

- **Fewer instructions is not faster.** `docs/SSA-REGALLOC-PLAN.md` records
  size and correctness as settled on arm64 and **speed as the open blocker**:
  seven of seventeen benchmarks run 1.11x-1.49x slower under SSA, geomean
  ~0.92x. On x86-64 the allocated path is now smaller on call-heavy loops too
  (199 instructions against 213 on the enum-match loop in
  `TestX86_64SSABackendCLI`), but the reason it was ever larger is untouched:
  with no call-clobber awareness it still saves every caller-saved allocatable
  register around every call — 34 push/pop against the stack machine's 6 on
  that program.
- **The alternative route is further along than this document suggests.** SSA
  as an ANALYSIS representation rather than a codegen path is substantially
  built: `Op.SrcOp` provenance with a totality gate
  (`internal/e2e/ssa_lift_provenance_test.go`), and
  `internal/ssa/{ownership,ownership_solve,ownership_returns,units,certify}.go`
  are 2,100+ lines of interprocedural ownership over the lifted form. That
  route feeds roadmap goal 2 (Perceus in the self-host) directly, where the
  codegen cutover does not.

## Where this stands (2026-09-15)

**The codegen question is still OPEN, and the "tripwires 1–3 unfired" line
above is STALE.** This section corrects it rather than deciding on top of it.

**Tripwire 1 has fired.** #8822 — open, labelled `backend` / `codegen` /
`performance` — is a profiled real program, not a microbenchmark:
`coreutils/sort.fern` is 4–5x slower than GNU, callgrind attributes ~1500
instructions per comparison, and the issue's own diagnosis names the cause as
"every local is a memory slot and every temporary goes through push/pop". It
then names the remedy explicitly:

> `-backend ssa` is the register-allocating emitter and **would answer (1)
> directly**, but it does not cover the I/O surface: building `sort.fern` with
> it reports 17 undefined call targets.

The letter of tripwire 1 names LICM / GVN / SCCP and this is register
allocation, so it is arguably tripwire 1's *spirit* rather than its text. The
distinction does not rescue the "unfired and unmeasured" claim: a profiled real
Fern program where the SSA emitter is the demonstrated answer is exactly the
evidence that clause was waiting for, and it has existed since 2026-09-07.

**And the work is live.** `docs/COREUTILS-SSA-ARGS-2026-09-08.md`,
`COREUTILS-SSA-BRANCHES-2026-09-08.md` and
`COREUTILS-X86-SSA-SLICES-2026-09-08.md` are a stream of SSA-backend work under
epic #8278, and the index-helper inlining landed on both native SSA backends on
2026-09-15. Each of those documents is careful to say it proposes **no
default-backend switch** — so the active work is not itself a cutover, but it
is the opposite of a shelved layer.

So what the 2026-09-02 numbers support is narrower than a resolution:

- **The analysis route is real, funded and already built** — `Op.SrcOp`
  provenance with a totality gate, plus
  `internal/ssa/{ownership,ownership_solve,ownership_returns,units,certify}.go`,
  2,100+ lines of interprocedural ownership over the lifted form, reaching the
  32% of reference-count operations that act on unnamed operand-stack values.
  It answers tripwire 4 and feeds roadmap goal 2 directly. Nothing here is in
  tension with the codegen question; both roles use the same lift.
- **The codegen route is not ready to be defaulted** — seven of seventeen arm64
  benchmarks run 1.11x–1.49x slower under SSA, geomean ~0.92x, call-clobber
  awareness unwritten, and on x86-64 the flat backend is ahead on the scan loop
  (18 instructions a byte against 23). `SSA-REGALLOC-PLAN.md` records speed as
  the open blocker.
- **Neither of those decides the question.** "Not ready to default" is not
  "will never be a codegen path", and #8822 is a standing argument that it
  should become one for x86-64 specifically.

**The next decision point is concrete**, which is better than another date:
whether to fund closing `x86_64ssa`'s helper-coverage gap so `sort.fern` builds
under it, and then measure. That is the 84-symbol gap (#8047) with its
step-function unlock curve, and #8822 is the reason to pay it.

### Per-backend disposition

| backend | disposition |
| ------- | ----------- |
| `internal/codegen/wasmssa` | **RETIRED** (#9397). The relooper-era emitter, one fixed page of memory that never grows, and the only SSA backend with no corpus differential — its cover was its own hand-written cases. It existed to satisfy the "keep the layer exercised end-to-end" clause below, which `arm64ssa` now discharges far better. This is independent of the codegen question: the coreutils work is native, wasm is not where a comparison-bound utility runs, and nothing in #8278 or #8822 touches it. `-backend ssa` no longer accepts a wasm target. |
| `internal/codegen/x86_64ssa` | **KEPT, and its coverage gap is live work.** It is the backend #8822 names as the direct answer to the epic's dominant cost, blocked on 17 undefined call targets in the I/O surface. `arm64ssa` also imports its layout and call-coalescing model, so it could not be cut on its own even if that were wanted. |
| `internal/codegen/arm64ssa` | **KEPT and load-bearing**, twice over: the emit target for `internal/semir`'s typed pre-RC ownership pipeline (`-backend typed-ssa`, `cmd/fern/typedssa.go`), and the subject of the coreutils perf stream. Its 281-program corpus differential is what keeps the SSA layer honest end-to-end. |

The self-host mirror — the stack IR is the single self-host production
lowering — was taken on 2026-07-03 and is untouched by any of this:
`docs/SELFHOST-SSA-DECISION.md`.

## Maintenance contract

The framework is maintained for BOTH roles — analysis, and an emitter that is
not defaulted but is actively measured:

- Keep `internal/ssa/` building and its tests green in CI (they already run).
- Keep the **arm64 corpus differential**
  (`internal/e2e/arm64_ssa_differential_test.go`) green, with an empty
  known-divergences file. This is the clause that replaces "keep
  `-target wasm32-wasi -backend ssa` in the e2e matrix": it exercises the lift
  and the register allocator over 281 programs rather than a handful of
  hand-written cases, so it is a real gate rather than a reminder that the
  layer compiles.
- `LiftFromIR` must stay **total** over what `internal/ir` emits — the
  provenance gate (`internal/e2e/ssa_lift_provenance_test.go`) is the one that
  matters now, because an analysis that cannot see a construct silently
  under-reports rather than refusing to emit.
- New IR ops / language features are **not** required to land in the SSA
  *emitters*. They ARE required to lift, for the same reason.
- A gap that blocks a `-backend ssa` build of a real program is logged against
  #8822, not silently absorbed: the helper-coverage gap is the thing standing
  between the epic and its measurement.

## Reconciliation: the native `x86_64ssa` / `arm64ssa` backends (2026-07-03, #4391)

Since this doc was written, two native SSA backends grew —
`internal/codegen/x86_64ssa` (#4266) and `internal/codegen/arm64ssa`
(#4310) — joining `wasmssa`. This is **not** the September re-eval happening
early: it is an **extension of the "keep an experimental proving ground"
maintenance contract above**, not a production cutover. The decision stands —
SSA remains shelved for production; these backends are the sanctioned proving
ground on the native side, exactly as `-backend ssa` (wasm) is.

**Their gate (so the status is explicit, not ambient):**

- **Not on any production path.** The shipping backends stay
  `internal/codegen/{arm64,x86_64,wasmbin}` over the flat `ir.Program`. No
  compile reaches `x86_64ssa` / `arm64ssa` unless a test / experimental flag
  selects it.
- **Differential-gated.** Each carries its own emit + run tests
  (`internal/codegen/x86_64ssa/*_test.go`, `arm64ssa/gas_run_test.go`, …) and
  must stay byte-identical-in-behaviour to the flat-IR backends and the
  interpreter across their covered subset. A divergence is a bug in the
  proving ground, not a reason to ship it.

  On **arm64** that is now enforced by a corpus differential rather than by
  hand-written cases: `internal/e2e/arm64_ssa_differential_test.go` runs every
  `examples/**` program through both `-target arm64-linux` and
  `-target arm64-linux -backend ssa` and compares exit status and stdout, with a
  refusal counted as the documented coverage endpoint rather than as a pass.
  Its first run found four wrong answers and 56 heap SIGSEGVs; the
  divergence file (`internal/e2e/testdata/arm64-ssa-diff-known-divergences.txt`)
  has no rows today. `x86_64ssa` has the same shape of leg
  (`internal/e2e/x86_64_ssa_differential_test.go`) over the same corpus, but it
  compares 30 programs to arm64's 281 — the rest are refused for missing
  helpers. **`-backend ssa` (wasm) still has no corpus differential**; its only
  cover is its own hand-written cases.
- **Not required to carry new features.** As with `-backend ssa` (wasm), a language
  feature these backends can't yet express is a logged gap, not a blocker.

**The re-evaluation does not change this.** Whenever the call is made (or a
further tripwire fires), the cutover point is still the shared
one — IR → SSA → *all* native backends — never one backend in isolation. The
self-host mirror of this decision (the IR path is the single self-host
production lowering; SSA `build_func` demoted to opt-in) is
`docs/SELFHOST-SSA-DECISION.md`.
