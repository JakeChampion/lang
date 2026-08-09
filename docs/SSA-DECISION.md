# SSA: ship-or-shelve decision

**Status:** SHELVED for production backends (decided 2026-05-31).
**Re-evaluation date:** 2026-09-01 (or earlier if a trigger below fires).
**Owner:** compiler / IR.

## The question

`internal/ssa/` is a fully-built SSA framework — construction (dominator
tree, pruned SSA via dominance frontiers, phi insertion) plus a real pass
suite (`constfold`, `cse`, `licm`, `trivialphi`, `blockmerge`,
`branchfold`, `strength`, `sccp`). It is the second-largest test surface in
the repo (~469 tests). **But it is not on any production path.** The
shipping backends — `internal/codegen/{arm64,x86_64,wasmbin}` — all consume
the flat, structured-control-flow `ir.Program` and run only the
peephole-grade passes in `internal/ir/` (`fold`, `dce`, `copyprop`,
`constprop`, `inline`, `strength`, `tco`, `defunctionalise`). The only
consumer of the SSA layer is the experimental `-target wasm -backend ssa` emitter
(`internal/codegen/wasmssa/`), which covers i32/i64/f32/f64 + memory + a
reducible-CFG subset.

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

## What happens on the re-evaluation date

On 2026-09-01, revisit with fresh numbers:

- If no tripwire has fired, **re-shelve** and set the next date. Record why.
- If a tripwire fired, write an `SSA-CUTOVER-PLAN.md`: pick the shared
  lowering point (IR → SSA → all native backends, not one), define the
  parity gate (the differential oracle must stay byte-identical across
  interp + every backend), and stage it behind a flag until green.

## Maintenance contract while shelved

So the framework doesn't rot:

- Keep `internal/ssa/` building and its tests green in CI (they already run).
- Keep `-target wasm -backend ssa` in the e2e matrix so the layer stays exercised
  end-to-end, not just unit-tested.
- New IR ops / language features are **not** required to land in the SSA
  layer while it is shelved — but if `-backend ssa` (wasm) can't express a feature, that
  gap is logged here, because a growing gap raises the cutover cost and is
  itself a signal.

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
- **Not required to carry new features.** As with `-backend ssa` (wasm), a language
  feature these backends can't yet express is a logged gap, not a blocker.

**The 2026-09-01 re-evaluation is unchanged.** If a tripwire fires before then
(or the re-eval decides to cut over), the cutover point is still the shared
one — IR → SSA → *all* native backends — never one backend in isolation. The
self-host mirror of this decision (the IR path is the single self-host
production lowering; SSA `build_func` demoted to opt-in) is
`docs/SELFHOST-SSA-DECISION.md`.
