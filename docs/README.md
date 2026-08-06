# Design docs index

Everything under `docs/` in one place. Docs fall into a few kinds, tagged
per entry below:

- **[policy]** — a decided rule both compilers must honour. Living; edit in place.
- **[reference]** — living description of a shipped feature or process.
- **[tracker]** — living progress log for an active arc; append as slices land.
- **[plan]** — accepted design whose implementation is not finished.
- **[research]** — survey/analysis input to a decision; not a commitment.
- **[record]** — historical: a shipped plan, a resolved investigation, or a
  superseded direction. Kept for archaeology; do not treat as current.

Statuses reflect a 2026-07 audit. Where a doc contradicts this index, trust
the doc's own status banner (and fix whichever is stale).

The standing project roadmap itself lives in `CLAUDE.md` (goal 1: full IR in
the self-hosted compiler; goal 2: port native Perceus to it). Umbrella issue
[#4368](https://github.com/JakeChampion/lang/issues/4368) tracks
roadmap-shaped items that live only in these docs; the native-convergence
freeze tracker is [#4451](https://github.com/JakeChampion/lang/issues/4451).

## Language design & semantics

| Doc | Status | What it is |
| --- | --- | --- |
| `LANGUAGE-DIRECTION.md` | [reference] | Where the language is going; shapes adopted and rejected. The design north star. |
| `ARRAY-BOUNDS.md` | [policy] | Bounds-checking semantics. |
| `INTEGER-SEMANTICS.md` | [policy] | Portable, never-trapping integer semantics. |
| `FLOAT-SEMANTICS.md` | [policy] | Float edge-case semantics. |
| `BLOCK-EXPRESSIONS.md` | [reference] | Block-expression syntax + value rules. |
| `ASSOCIATED-TYPES.md` | [reference] | Trait associated types (`Self::Item`). |
| `NAMED-FIELD-VARIANTS.md` | [reference] | Named-field enum variants. |
| `CLOSURE-CAPTURE.md` | [reference] | Capture-by-value contract + E049 enforcement (shipped). |
| `TRAITS.md` | [reference] | Trait system; phases 1–3 shipped. |
| `DYN-TRAITS.md` | [reference] | `dyn Trait` objects; slices 1–4 shipped. |
| `PUB-PACKAGE.md` | [reference] | `pub(package)` visibility. |
| `CURSOR-IDIOM.md` | [policy] | Immutable read-and-advance cursor idiom (decided 2026-06). |
| `FEATURE-AUDIT.md` | [tracker] | Living per-feature × per-backend audit matrix. |
| `TRAIT-USAGE-AUDIT.md` | [record] | Where our own code should use traits; feeds epic #2691. |
| `ADVERSARIAL-REVIEW-2026-06.md` | [record] | 2026-06 break-the-compiler review; all 17 findings fixed. |
| `LANGUAGE-REVIEW-2026-07.md` | [record] | 2026-07 whole-language critical review snapshot. |
| `IMPROVEMENTS.md` | [record] | fernsmith-era improvement backlog; mostly landed, residue in #2852/#2669/#2673. |
| `GENERIC-VARIANT-FN-PAYLOAD-INFERENCE-GAP.md` | [record] | Generic-variant fn-payload inference gap — fixed. |

## Modules, packages & prelude retirement

| Doc | Status | What it is |
| --- | --- | --- |
| `PRELUDE-TO-MODULES.md` | [record] | Prelude → explicit `std/`/`core/` imports migration — complete (#1561). |
| `POST-PRELUDE-CLEANUP.md` | [record] | Post-migration cleanup checklist — done (item 5 consciously deferred). |
| `MODULE-PACKAGES-RESEARCH.md` | [research] | Third-party package/dependency-management survey. |

## Compiler architecture, roadmap & backend parity

| Doc | Status | What it is |
| --- | --- | --- |
| `BACKEND-PARITY.md` | [tracker] | Cross-backend feature/limitation tracker + macOS support stance. |
| `NATIVE-CONVERGENCE.md` | [policy] | How `internal/` and the self-host converge; freeze preconditions in #4451. |
| `ROADMAP-AND-SELF-HOSTING.md` | [record] | 2026-05-15 tech-debt + self-host-readiness snapshot; live roadmap is CLAUDE.md + issues. |
| `RESEARCH-ROADMAP.md` | [record] | Reading-order meta-index for the research docs; recommendations now filed as #4412–#4416. |
| `BOOTSTRAP-RESEARCH.md` | [research] | Bootstrap/trust strategy survey (Rust, Zig, Go, …). |
| `TOOLCHAIN-SELF-HOSTING.md` | [record] | Zero-external-toolchain builds — complete for all targets. |
| `PERFORMANCE-RESEARCH.md` | [research] | Compiler/runtime perf survey; recommendations tracked in #4412. |
| `IDE-COMPILATION-RESEARCH.md` | [research] | Post-MVP incremental-compilation survey; tracked in #4415. |
| `DIAGNOSTIC-UX-RESEARCH.md` | [research] | Elm/Rust-grade diagnostics survey; tracked in #4413. |
| `PLATFORM-RESEARCH.md` | [research] | Handler/host seam survey for edge targets; tracked in #4414. |
| `CONCURRENCY-RESEARCH.md` | [research] | Concurrency-model survey behind the async direction. |
| `PR-326-REVIEW.md` | [record] | Six-lens review of PR #326 (pair-form Result returns). |

## Self-host compiler (goal 1: full IR coverage)

| Doc | Status | What it is |
| --- | --- | --- |
| `SELF-HOST-REMAINING-PLAN.md` | [record] | Goal-1 widening tracker; the per-function IR subset is now mature — remaining gaps are the bundle budget (#3425) and wasm-IR exclusions (#4316–#4320). |
| `SELF-HOST-AUDIT.md` | [tracker] | SH-NNN code-quality worklist; mirrored in #2849. |
| `SELFHOST-CHECKER-PORT.md` | [tracker] | Checker parity port log; open residue in #4363, #4346, #4344, #4345. |
| `SELFHOST-CHECKER.md` | [record] | Early narrow Option/Result guard — superseded by `SELFHOST-CHECKER-PORT.md`. |
| `IR-CLOSURES-PLAN.md` | [record] | Closures on the IR path — shipped. |
| `CLOSURE-CONV-SELF-HOST-IR.md` | [record] | Capturing-closure-arg lowering — shipped. |
| `ITERATOR-BOUNDED-IR-PLAN.md` | [record] | Bounded-iterator lowering — done. |
| `EFFECT-A-LOWERSTATE-INPLACE-PLAN.md` | [record] | Superseded 2026-06-21 by direct measurement. |
| `IR-SELFCOMPILE-OOM-FINDINGS.md` | [record] | Self-compile OOM investigation; levers tracked in #4394. |
| `SELFHOST-SYMBOL-INTERNING.md` | [plan] | Symbol-interning memory lever (#4394). |
| `SELFHOST-BSTATE-RECLAIM-PLAN.md` | [plan] | Reclaiming SSA-builder `BState` churn (design-only). |
| `SELFHOST-VARIANT-PAYLOADS.md` | [plan] | Wide/multi variant payload slots; S6+ residue listed in #4368. |
| `SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md` | [record] | fn-payload variant gap — resolved on the IR path (#4364/#4722). |
| `SSA-DECISION.md` | [policy] | SSA shelved for production native backends (2026-05-31). |
| `SELFHOST-SSA-DECISION.md` | [policy] | Stack IR is the single production lowering (2026-07-03, #4391). |
| `SELFHOST-SSA-ALWAYS.md` | [record] | SSA-always-on plan — shelved by the decision above. |
| `SSA-REGALLOC-PLAN.md` | [plan] | SSA register allocation (experimental `-target arm64-ssa`; #4112). |
| `SSA-CLOSURE-DISPATCH.md` | [plan] | Closure dispatch on the SSA path (#4112). |
| `SSA-RC-RUNTIME.md` | [plan] | RC runtime helpers on the SSA path (#4112). |
| `WASM-COMPONENT-GENERATOR.md` | [plan] | Generative component builder for the self-host wasm backend (#4368, poss. subsumed by #4315). |

## Memory management (goal 2: RC / Perceus / ownership)

| Doc | Status | What it is |
| --- | --- | --- |
| `RC-PERCEUS-PLAN.md` | [record] | The native Perceus implementation — goal 2's reference target; implemented. |
| `RC-PERCEUS-SELF-HOST-PORT.md` | [tracker] | **The active goal-2 tracker.** Port of native Perceus to the self-host, slice by slice. |
| `RC-PERCEUS-SELF-HOST-IR-REBUILD.md` | [tracker] | Design + rollout for the IR-path RC rebuild. |
| `RC-PERCEUS-SELF-HOST-IR.md` | [record] | Early feasibility note — superseded by the REBUILD/PORT trackers. |
| `RC-PERCEUS-PHASE-1E-PLAN.md` | [plan] | Phase-1e free-flip slices (draft executing). |
| `RC-NATIVE-PASS-EXTRACTION.md` | [record] | Native RC pass extraction — slices 1–4 landed (#4393). |
| `RC-STRINGS-PLAN.md` | [plan] | String RC design (not blocked on SSO). |
| `RC-ARRAY-MOVE-SEMANTICS-PLAN.md` | [plan] | Move semantics for threaded array params. |
| `SELFHOST-PERCEUS-REUSE.md` | [record] | Reuse-analysis status note (corrected: largely implemented; divergences in #4356). |
| `CYCLE-COLLECTION-ANALYSIS.md` | [research] | Do we need a cycle collector? Analysis only. |
| `ARENA-DECISION.md` | [record] | `arena {}` block — removed in full. |
| `IMMUTABILITY-MIGRATION-PLAN.md` | [record] | Immutable-data migration — shipped native; self-host enforcement partial. |
| `INTERP-MAP-COW-PLAN.md` | [record] | Interp Map copy-on-write (M1) — implemented. |
| `CELL-TYPE-PLAN.md` | [record] | `Cell[T]` mutable cell — implemented for scalar + string. |
| `OWNERSHIP-INFERENCE-PLAN.md` | [tracker] | Ownership inference; slice 0 landed 2026-06-05. |
| `OWNERSHIP-TYPES-PLAN.md` | [tracker] | Checked ownership axis (#4297; sub-issues #4812–#4814). |
| `SSO-PLAN.md` | [record] | SSO migration roadmap — shipped on wasm + both natives. |
| `SSO-TWOWORD-EXEC.md` | [record] | wasm two-word ABI flip execution record — shipped. |
| `SSO-TWOWORD-FLIP-STATUS.md` | [record] | Mid-flip status journal — historical; the flip shipped. |
| `SSO-NATIVE-FLIP-STATUS.md` | [record] | Native (arm64 + x86-64) SSO flip — shipped 2026-06-03. |
| `PACKED-OPERAND-STACK-PLAN.md` | [plan] | 8-byte operand-stack slots — not started; tracked as #4111. |

## Async & the wasm/WASI component stack

| Doc | Status | What it is |
| --- | --- | --- |
| `ASYNC.md` | [reference] | The shipped `std/async` combinator surface. |
| `ASYNC-REDESIGN.md` | [policy] | Accepted direction: `future[T]` + structured combinators over CPS surface syntax. |
| `ASYNC-FUTURE-UNIFICATION.md` | [plan] | Fifth redesign slice — design, not yet implemented. |
| `ASYNC-IMPLEMENTATION-PLAN.md` | [record] | Original async plan; CPS surface parts superseded by the redesign, runtime parts kept. |
| `ASYNC-IMPLEMENTATION-RESEARCH.md` | [research] | Koka/Lean/Roc async implementation mechanics. |
| `ASYNC-SELFHOST-IR.md` | [plan] | Async on the self-host IR path — not started. |
| `WASM-REACTOR-PLAN.md` | [record] | wasm reactor on Preview-2 pollables — shipped. |
| `WASI-PREVIEW2.md` | [record] | Preview-2 component migration — complete; no adapter, no wasm-tools shell-out. |
| `WASI-PREVIEW3-ASYNC-PLAN.md` | [tracker] | Preview-3 native async: channels done at the ABI level; surface exposure staged. |
| `STREAM-TYPE-SURFACE.md` | [plan] | `stream[T]` in the language surface — design checkpoint. |
| `WIT-BRING-YOUR-OWN.md` | [tracker] | Bring-your-own `.wit` world — phased plan. |
| `P5-PLAN.md` | [plan] | Resource-handle language layer (`own R` / `borrow R`) over the shipped handle baseline. |

## Stdlib

| Doc | Status | What it is |
| --- | --- | --- |
| `STDLIB.md` | [reference] | The stdlib: module map, conventions, receiver-method dispatch. |
| `STDLIB-ROADMAP.md` | [tracker] | Seven-language survey → prioritised additions; residue tracked in #4416/#4385. |
| `STDLIB-DESIGN-RESEARCH.md` | [research] | Depth research: HTTP, JSON, date/time, I/O. |
| `SOTA-STDLIB-BLUEPRINT.md` | [research] | Per-primitive survey against the best known algorithm; verdicts + recommended order. |
| `ATLAS-PLATFORM-PLAN.md` | [plan] | Platform companion to the blueprint: SIMD/dispatch/allocator/concurrency ordering, and the fused-intrinsic SIMD ABI. |
| `STRINGS-SOTA.md` | [research] | String/byte-primitive depth survey. |
| `ARRAY-BUILDER-PLAN.md` | [plan] | Array-builder design. |
| `PURE-COLLECTION-API-PLAN.md` | [plan] | Pure (persistent) collection API. |
| `MAP-SPECIALIZATION.md` | [record] | Compile-time Map monomorphization proposal — unstarted (#4368); prose predates prelude removal. |
| `RUNTIME-IN-FERN.md` | [tracker] | Runtime helpers rewritten in Fern (#2649); Tier 0–2 complete. |
| `RUNTIME-INTRINSICS.md` | [reference] | The raw-memory intrinsic floor Tier-2 helpers build on. |

## Tooling, tests & process

| Doc | Status | What it is |
| --- | --- | --- |
| `EMBED.md` | [reference] | Compile-time asset embedding (`-embed`, `__fern_asset`). |
| `LITERATE.md` | [reference] | Literate programming (`.fern.md`, tangle/weave). |
| `LSP-INTEGRATION-PLAN.md` | [record] | LSP MVP — shipped (`cmd/fern-lsp`); post-MVP ideas in IDE-COMPILATION-RESEARCH. |
| `TEST-RUNNER-MIGRATION.md` | [record] | Go-test → pure-Fern runner migration audit. |
