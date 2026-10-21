# Design docs index

Every top-level doc under `docs/` has one row here. Each doc's own
`Status: [tag]` line, in its first lines, is the source of its tag, and
`TestDocsIndexCoversEveryTopLevelDoc` (`internal/testing/sourcelint`) fails
when a doc has no row, a row names no doc, or the two tags differ.

- **[policy]** — a decided rule the compiler and the Go oracle must honour. Living; edit in place.
- **[reference]** — living description of a shipped feature, subsystem or process.
- **[tracker]** — living progress log for an arc that is still active.
- **[plan]** — accepted design whose implementation is not finished.
- **[research]** — survey or analysis input to a decision; not a commitment.
- **[record]** — historical: a shipped plan, a closed investigation, a measurement, or a
  superseded direction. Kept for archaeology; do not treat as current.

Within each section the living docs come first and the records after them.
Dated logs live in the log directories listed at the end.

The standing project roadmap lives in `CLAUDE.md`. The specification lives
outside `docs/`, at `spec/` (the syntactic grammar and the diagnostics index)
and `conformance/` (the behavioural corpus); those are normative and gated.
The `[policy]` docs are normative too but ungated, and `spec/README.md` lists
which ones.

## Language design & semantics

| Doc | Status | What it is |
| --- | --- | --- |
| `ARRAY-ALGEBRA.md` | [policy] | Normative answers to the seven array-algebra semantics questions of #9729 (pairing truncation, float reduction order, shape mismatch, views) that fusion must obey. |
| `ARRAY-BOUNDS.md` | [policy] | Bounds-checking semantics: an out-of-range index or `.with` write aborts the program on every backend. |
| `CLOSURE-CAPTURE.md` | [policy] | Closures capture enclosing variables by reference through a shared cell, with E049 governing write-back. |
| `FLOAT-SEMANTICS.md` | [policy] | IEEE 754 for ordinary arithmetic, with NaN bit patterns, the sign of zero and denormals left unspecified across backends. |
| `INTEGER-SEMANTICS.md` | [policy] | Wrapping, never-trapping integer operations with identical results on every backend, and the rules for usize. |
| `STR-VIEW-CONTRACT.md` | [policy] | What enforces the borrowed `str` contract (#8635). |
| `STRINGS-SOTA.md` | [policy] | String state of the art and Fern's string decisions D1–D11 (UTF-8, byte indices, char, guaranteed validity), and the October 2026 migration audit. |
| `ASSOCIATED-TYPES.md` | [reference] | Trait associated types and `Base::Name` projections (`Self::Item`, `T::Item`) as shipped. |
| `BLOCK-EXPRESSIONS.md` | [reference] | Block-expression syntax and value rules. |
| `CELL-TYPE-PLAN.md` | [reference] | `Cell[T]`, the one mutable slot: which element types it admits, why E057 refuses the rest, and how it is implemented. |
| `DYN-TRAITS.md` | [reference] | `dyn Trait` runtime dispatch and its RC handling; slices 1–4 shipped, remaining leaking positions listed. |
| `LANGUAGE-DIRECTION.md` | [reference] | The language's direction: positioning, ideas adopted and rejected from other languages, and the shipping plan. |
| `MUST-CONSUME.md` | [reference] | `@must_consume` types: structs and enums that may not be implicitly dropped, checked as E067. |
| `NAMED-FIELD-VARIANTS.md` | [reference] | Named-field enum variants: declaring, constructing and destructuring them. |
| `TRAITS.md` | [reference] | Traits: static ad-hoc polymorphism, bounded generics and cross-module coherence; phases 1–3 shipped. |
| `TRY.md` | [reference] | The `?` operator on any enum marked `@try`, and the shape rule the checker enforces (E078). |
| `SIMD-GENERIC-KERNELS.md` | [plan] | One numeric kernel over a `Lanes` trait: scalar, `f64x2` / `f32x4`, `Dual` autodiff, `DoubleDouble`; associated constants now, const generics after the lane type. Revises `ATLAS-PLATFORM-PLAN.md` §1.2. |
| `COMPTIME-BRIEF.md` | [research] | Rules (from Zig) and architecture (from Lean 4) any future comptime must follow; comptime remains deferred. |
| `EFFECT-SYSTEMS-PRIOR-ART.md` | [research] | Prior-art survey of effect systems (Koka, Effekt, Unison, Scala CC, OCaml 5, Roc, Flix, Haskell, Rust, WASI); input to #5320. |
| `NICHE-LANGUAGE-RESEARCH.md` | [research] | 2026-07 survey of niche and research languages for mechanisms Fern could borrow, with a ranked shortlist. |
| `PATTERN-MATRIX-PROPOSAL.md` | [research] | A proposal to replace the nested-pattern desugar with Maranget decision trees, lifting the three P001 limits; not implemented. |
| `PLT-LANDSCAPE-2026.md` | [research] | 2026-07 survey of programming-language theory and practice: where Fern is at the frontier, its gaps, and ranked actions. |
| `SELFHOST-LANGUAGE-FRICTION.md` | [research] | Which Fern features make writing the self-host compiler harder, with a census, probes and ranked fixes. |
| `SPECIFICATION-RESEARCH.md` | [research] | Would a language spec pay for itself? Prior art, and a small mechanically checked executable spec. |
| `ARRAY-BUILDER-PLAN.md` | [record] | The `Array.build` / `Map.build` scoped linear builder, a parse-time desugar; shipped in both parsers, followed by the E056 subscript-assignment ban. |
| `EFFECT-ROWS-BRIEF.md` | [record] | The effect-rows prototype (#5320): the `uses` clause was built, measured and dropped; `fern -effects` shipped. |
| `GENERIC-VARIANT-FN-PAYLOAD-INFERENCE-GAP.md` | [record] | A Go-checker inference gap for generic variants with function payloads, its cause and fix. |
| `IMMUTABILITY-MIGRATION-PLAN.md` | [record] | The move to immutable data (E048, E049, E055, E056, E057, struct update, `Cell[T]`); shipped and enforced. |
| `LANGUAGE-REVIEW-2026-07.md` | [record] | A July 2026 critical review of the language, stdlib, implementations and ecosystem, before the native compiler's retirement. |
| `NICHE-BORROWS-PLAN.md` | [record] | The execution plan for the niche-language shortlist; its items shipped, were evaluated, or were folded into goal 2. |
| `P5-PLAN.md` | [record] | WIT resource handle types (`own R` / `borrow R`) with automatic `[resource-drop]`; shipped in the self-host compiler. |
| `PURE-COLLECTION-API-PLAN.md` | [record] | Making every collection operation value-returning (E055, renamed methods, `Array.build`, fip in-place assertion); shipped. |
| `TRAIT-USAGE-AUDIT.md` | [record] | Where Fern's own code could use traits (#2691); its claim that the self-host cannot compile traits is out of date. |

## Modules & packages

| Doc | Status | What it is |
| --- | --- | --- |
| `PACKAGE-CAPABILITIES-BRIEF.md` | [reference] | Compile-time per-package capability grants enforced by import-graph reachability (`internal/pkg/caps`, `compiler/caps.fern`). |
| `PACKAGES.md` | [reference] | The `fern.toml` manifest: path and url dependencies, the fetch store, vendoring, workspaces, `fern -add`, and MVS into `fern.lock`. |
| `PRELUDE-TO-MODULES.md` | [reference] | How programs load std/ and core/ modules: mangling, the transitive-import dedupe, `pub use` re-exports. |
| `PUB-PACKAGE.md` | [reference] | `pub(package)` visibility: what a package is today and what each visibility level exposes. |
| `MODULE-PACKAGES-RESEARCH.md` | [research] | Survey of inter-project module and package management for Fern's first third-party packages. |
| `PACKAGE-MANAGEMENT-SOTA.md` | [research] | 2026 survey of package management: resolution, semver enforcement, content-addressed stores and supply-chain defences. |
| `POST-PRELUDE-CLEANUP.md` | [record] | Follow-ups from removing the auto-injected prelude; done, item 5 deliberately deferred. |

## Compiler architecture, runtime & backends

| Doc | Status | What it is |
| --- | --- | --- |
| `FREESTANDING-CORE.md` | [policy] | Which builtins need a host and which do not: the core-versus-host rule and each judgement call (#6506). |
| `ITERATOR-FUSION-CONTRACT.md` | [policy] | The contract lazy iterator chains must meet before adoption; the eager fusion pass and part of the lazy surface are built. |
| `ARRAY-FUSION-OPERATORS.md` | [reference] | Per-operator proof behind `compiler/semfuse.fern`: what map, filter, fold, reduce, scan, take, take_while and flat_map each contribute to a fused loop. |
| `BACKEND-PARITY.md` | [reference] | The targets, CPU baselines, syscall floor, macOS support stance and known limitations of each backend. |
| `BYTE-COPY-LOOPS.md` | [reference] | The `compiler/sembytecopy.fern` pass that replaces zero-based byte-copy loops over owned `u8[]` values with bulk copies. |
| `INTERP-ARRAY-INPLACE-WRITE.md` | [reference] | How the interpreter's arrays carry an owner count so `arr.with(i, v)` writes in place (#7287). |
| `NATIVE-REGISTER-POOLS.md` | [reference] | Register homes, emitter temporaries and fixed clobbers of the SSA register allocator on arm64 and x86-64 (#12210). |
| `RUNTIME-INTRINSICS.md` | [reference] | The raw-memory intrinsic floor (syscalls, scratch, environ, raw loads and stores) the Fern runtime helpers are built on. |
| `RUNTIME-IN-FERN.md` | [tracker] | Rewriting the runtime helpers in Fern (#2649): Tiers 0–2 done, and syscall leaves moving onto shared Fern source. |
| `BARE-METAL-PLAN.md` | [plan] | Scope below the OS (guest libraries, drivers, kernels, an OS in Fern), why the interrupt/RC memory model gates it, and the order of work; nothing built. |
| `MULTICORE-RESEARCH.md` | [research] | Survey of parallelism: share-nothing workers with per-worker heaps, given non-atomic refcounts (#5366). |
| `PERFORMANCE-RESEARCH.md` | [research] | Survey of compiler and runtime performance techniques from Zig, Rust, OCaml, APL, LuaJIT, SBCL and others. |
| `PLATFORM-RESEARCH.md` | [research] | Survey of the handler/host seam for edge functions: capabilities, request lifecycle, one handler to many targets (#4414). |
| `ADVERSARIAL-REVIEW-2026-06.md` | [record] | A June 2026 adversarial review hunting correctness, soundness and backend-parity bugs; all 17 findings fixed, each with a regression test. |
| `ARRAY-PIPELINE-BASELINE-2026-09.md` | [record] | Pre-fusion measurements of the array pipeline programs (#9728), and what array fusion (#9731) changed in allocations and instructions. |
| `CODEGEN-QUALITY-AUDIT-2026-09.md` | [record] | A September 2026 audit of emitted machine code: 42.6% wasted instructions; Tier A of its fixes landed. |
| `COMPILER-ARCHITECTURE-2026.md` | [record] | A 2026 scorecard of the compiler against a modern-design checklist, written while both compilers were maintained. |
| `IMPROVEMENTS.md` | [record] | The compiler and test-infrastructure improvement list from the fernsmith work; most items landed. |
| `INTERP-CLOSURE-BINDING-IDENTITY.md` | [record] | The interpreter fix (#8970) giving captured bindings shared cells. |
| `INTERP-MAP-COW-PLAN.md` | [record] | Copy-on-write Maps in the interpreter (finding M1); implemented. |
| `NATIVE-CONVERGENCE.md` | [record] | The policy to freeze the native Go compiler as bootstrap and oracle; it froze on 2026-09-28 and its backends were deleted by 2026-10-05. |
| `NATIVE-FREEZE.md` | [record] | The 2026-09-28 native compiler freeze: the gate's state on the day and what moved to the self-host compiler. |
| `NATIVE-RETIREMENT.md` | [record] | The retirement of the Go native backends: what was measured, how each test was handled, and the order of the deletions; done by 2026-10-05. |
| `PACKED-OPERAND-STACK-PLAN.md` | [record] | 8-byte operand-stack slots on the Go native backends; x86-64 done, arm64 never, and the backends deleted. |
| `PR-326-REVIEW.md` | [record] | Six-lens review of PR #326 (pair-form register returns for Result); fix-now items landed. |
| `RESEARCH-ROADMAP.md` | [record] | The reading-order index for the eight *-RESEARCH.md surveys; its recommendations became #4412–#4416. |
| `SSA-CLOSURE-DISPATCH.md` | [record] | Closure and function-value dispatch on the Go SSA native path (#4112); that backend was deleted. |
| `SSA-CUTOVER-PLAN.md` | [record] | A shared IR-to-SSA lowering for the Go native backends; never adopted, and those backends were deleted. |
| `SSA-DECISION.md` | [record] | The ship-or-shelve log for the Go SSA layer; it and the native backends were deleted in 2026-10. |
| `SSA-RC-RUNTIME.md` | [record] | RC runtime helpers on the Go SSA native path (#4112); that backend was deleted. |
| `SSA-REGALLOC-PLAN.md` | [record] | SSA register allocation for the Go x86-64 and arm64 backends (#4112); those backends were deleted. |
| `SSO-NATIVE-FLIP-STATUS.md` | [record] | The two-word string ABI flip on the Go arm64 backend; those backends were deleted. |
| `SSO-PLAN.md` | [record] | The migration roadmap for small-string optimisation and the two-word string ABI on the Go backends. |
| `SSO-TWOWORD-EXEC.md` | [record] | The file-level execution record of the wasm32 two-word string ABI flip; shipped. |
| `SSO-TWOWORD-FLIP-STATUS.md` | [record] | The journal kept during the two-word string ABI flip; shipped. |
| `TREESHAKE-REACHABILITY.md` | [record] | The 2026-08-26 reachability audit (#4114): module pruning worked, and the over-rooting it found was fixed. |

## Self-host compiler

| Doc | Status | What it is |
| --- | --- | --- |
| `BOOTSTRAP.md` | [reference] | How `make bootstrap` builds the compiler from a pinned stage0 without Go, and how `make distcheck` proves byte-identical self-reproduction. |
| `SELFHOST-SEMANTIC-SOURCE.md` | [reference] | The typed lowering (`semsource.fern`) from checked source to semantic SSA, which produces every function body. |
| `SELFHOST-SSA-BACKEND.md` | [reference] | The register-allocating SSA backend, the only emitter on arm64 and x86-64: lift, allocation, emission and measurements. |
| `SELF-HOST-AUDIT.md` | [tracker] | Code-quality audit of `compiler/` with SH-NNN findings, mirrored as the open checklist in #2849. |
| `SELFHOST-CHECKER-PORT.md` | [tracker] | Porting the Go checker's rules and coded diagnostics to `compiler/checker.fern`. |
| `BOOTSTRAP-RESEARCH.md` | [research] | How Rust, Zig, Crystal, OCaml, Go, TinyCC and others bootstrapped; its recommended strategy became BOOTSTRAP.md. |
| `CLOSURE-CONV-SELF-HOST-IR.md` | [record] | Capturing-closure-argument lowering on the self-host IR path; shipped. |
| `EFFECT-A-LOWERSTATE-INPLACE-PLAN.md` | [record] | A plan to move LowerState accumulation in place; superseded when measurement showed the rebuild was already in place. |
| `IR-CLOSURES-PLAN.md` | [record] | Lowering closures and lambdas through the self-host IR; shipped, and the AST fallback is deleted. |
| `IR-SELFCOMPILE-OOM-FINDINGS.md` | [record] | The 2026-06 investigation into the self-compile running out of memory; fixes tracked in #4394. |
| `ITERATOR-BOUNDED-IR-PLAN.md` | [record] | Iterator-bounded generic reducers (core/iter sum, count, to_array, product) on the self-host IR path; done. |
| `PERFORMANCE-AUDIT-2026-08.md` | [record] | The self-host compiler's compile-time gap against the Go compiler and its causes; the gap fell from 6.3x to 3.1x (#6888). |
| `RC-PERCEUS-SELF-HOST-IR-REBUILD.md` | [record] | Rebuilding the self-host compiler around a native-shaped stack IR; finished. |
| `RC-PERCEUS-SELF-HOST-IR.md` | [record] | An early 2026-06 note on whether a native-shaped IR would help the self-host Perceus port; superseded. |
| `ROADMAP-AND-SELF-HOSTING.md` | [record] | A 2026-05-15 snapshot of tech debt and self-hosting readiness; the roadmap moved to CLAUDE.md and #4368. |
| `SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md` | [record] | Enum variants with function-typed payloads miscompiled on the legacy emitter; resolved on the IR path. |
| `SELF-HOST-REMAINING-PLAN.md` | [record] | The per-module plan for widening what the self-host compiler could compile (goal 1); complete. |
| `SELFHOST-AST-RETIREMENT.md` | [record] | The retirement of the self-host's AST emitters; every backend now routes IR-or-error. |
| `SELFHOST-CALLABLE-CONTRACTS.md` | [record] | The self-host checker keeping callable parameter types and arity, with E004/E038 through projections (#8962). |
| `SELFHOST-CHECKER.md` | [record] | The original narrow Option/Result type guard in the self-host emitter; superseded by the checker port. |
| `SELFHOST-DECLARATION-CONTRACTS.md` | [record] | Exact type contracts for self-host declarations and a shared semantic type adapter (#8968). |
| `SELFHOST-ERASED-ELEMENT-RETURN.md` | [record] | A silent miscompile of `T[] -> T` generics on x86-64; fixed by monomorphising when the return mentions the type variable. |
| `SELFHOST-ERASED-WIDE-ARRAY-GENERICS.md` | [record] | A silent miscompile of erased wide-element `T[]` generics on wasm; fixed in two steps. |
| `SELFHOST-FUNCTION-TYPE-STRUCTURE.md` | [record] | The parser fix keeping a function type's array result part of the function type in TypeRef. |
| `SELFHOST-GENERIC-CALL-ELEMENT-TAG.md` | [record] | Generic-call results reaching call sites with unresolved type variables on the AST lowering; fixed, and that lowering is deleted. |
| `SELFHOST-LEXICAL-CAPTURE-SIZE.md` | [record] | Per-driver binary-size changes from the lexical capture contracts change (2026-09-09). |
| `SELFHOST-LITERAL-SETTLING.md` | [record] | A scoping note on the self-host checker typing every integer literal as i32; literal settling has since shipped. |
| `SELFHOST-PRECISE-FLOATS.md` | [record] | Exact f32/f64 float types with literal settling in the self-host checker (#8965). |
| `SELFHOST-SEMANTIC-PRODUCTION-SIZE.md` | [record] | Compiler binary growth from the typed semantic producer and ownership lowering (2026-09-15). |
| `SELFHOST-SSA-ALWAYS.md` | [record] | A plan to compile through SSA by default on every backend; shelved, then replaced by `-backend ssa`. |
| `SELFHOST-SSA-DECISION.md` | [record] | The 2026-07-03 decision (#4391) making the stack IR the only production lowering; partly superseded by `-backend ssa`. |
| `SELFHOST-SYMBOL-INTERNING.md` | [record] | The symbol-interning memory lever (#4394): built, measured, and closed with a negative result. |
| `SELFHOST-TUPLE-ARRAY-LOCAL-TAGS.md` | [record] | A wasm bug where an array-of-tuples local bound from a call read wrong values; fixed. |
| `SELFHOST-TYPED-OWNERSHIP-CUTOVER.md` | [record] | Moving ownership decisions onto the typed semantic boundary; done. |
| `SELFHOST-VARIANT-PAYLOADS.md` | [record] | Wide and multi-payload enum variants in the self-host; slices S1–S5 shipped. |
| `TYPED-IR-REWRITE.md` | [record] | Feeding checker types into lowering instead of re-inferring them; done. |

## Memory management (RC, Perceus, ownership)

| Doc | Status | What it is |
| --- | --- | --- |
| `ALLOCATION-OBSERVABLE.md` | [policy] | The contract for the allocation observable (`__heap_bump_bytes`): what the number means, what is portable across backends, and the AL-* conformance cases. |
| `REUSE-CONTRACT.md` | [policy] | The reuse and move shapes RC guarantees to fire, their taints and runtime guards, the fbip/E068 contract, and the tests that lock each. |
| `FIP-APPLICATION-GUIDE.md` | [reference] | Building bounded applications with FIP and FBIP: what own/fip/fbip guarantee, capacity design, and measuring. |
| `RC-DESIGN.md` | [reference] | How the self-host compiler decides and emits reference counting: semantic graph, ownership inference, unit plan, physical lowering, certifier, runtime box contract. |
| `SEMANTIC-GENERICS.md` | [reference] | The typed lowering builds a body per generic instantiation, so reference counting sees concrete types. |
| `MODE-LATTICE.md` | [research] | Framing own, fip, view types and `@must_consume` as one two-axis mode lattice (#5365). |
| `ARENA-DECISION.md` | [record] | The 2026-06 decision to remove the arena block and `arena_save`/`arena_restore`; per-request memory is reclaimed by reference counting only. |
| `CYCLE-COLLECTION-ANALYSIS.md` | [record] | Whether RC needs a cycle collector; its recommendation was adopted and reference cycles are not constructible. |
| `FIP-BROKER.md` | [record] | The bounded message-broker experiment (#9589): recycled slots allocate nothing in unique state. |
| `FIP-DSP.md` | [record] | The bounded DSP experiment (#9588): the FIP graph allocates nothing, but the direct loop also does and is faster. |
| `FIP-ETL.md` | [record] | The telemetry ETL experiment (#9591): the ordinary baseline was fastest, and all four forms allocated nothing in steady state. |
| `FIP-EVENT-LOOP.md` | [record] | Experiment 1 (#9583): a bounded key/value event loop in baseline, fbip and fip forms. |
| `FIP-HTTP-APP.md` | [record] | Experiment 4 (#9586): an HTTP-like pipeline in three memory disciplines, including behaviour at capacity. |
| `FIP-HTTP-CODEC.md` | [record] | Experiment 3 (#9853): an HTTP/1.1 parse and serialise as a fip codec against the stdlib HTTP path. |
| `FIP-KV-CONFIG.md` | [record] | The startup-configured key/value experiment: a bounded FIP table had zero allocations and the lowest median in 54 groups. |
| `FIP-KV-CORE.md` | [record] | The bounded key/value core experiment (#9585): three table representations compared for allocation and speed. |
| `FIP-MAIN11A.md` | [record] | The FIP experiments re-run on main 11a7173; superseded by the f939351 refresh. |
| `FIP-MAINF939.md` | [record] | 3,130 native runs of all FIP experiments on main f939351: zero allocation alone did not pick the fastest program. |
| `FIP-PACKET-PROTOCOL.md` | [record] | Experiment 2 (#9584): a bounded binary packet codec in four buffer-ownership forms. |
| `FIP-RING.md` | [record] | Extracting a shared generic bounded `Ring[T]` for the event loop and broker; the broker got slower with it. |
| `FIP-SIMULATION.md` | [record] | The bounded simulation experiment (#9587): FBIP and FIP worlds avoid allocation and beat a persistent vector. |
| `FIP-STORAGE.md` | [record] | The storage and page-cache simulator experiment (#9590): the unique FIP data plane makes no allocations; snapshots force copies. |
| `OWNERSHIP-INFERENCE-PLAN.md` | [record] | A plan to infer `own` in the Go compiler; slice 0 landed, superseded by the self-host's ownership inference. |
| `OWNERSHIP-TYPES-PLAN.md` | [record] | A phased plan for a checked ownership axis and the `str` view type (#4297); the self-host and `str` slices landed. |
| `PERCEUS-COMPARISON.md` | [record] | Fern against Koka and Lean on the standard Perceus workloads: wall time, peak memory, allocation counts (#11907). |
| `RC-ARRAY-MOVE-SEMANTICS-PLAN.md` | [record] | Move semantics on threaded array parameters to fit the 2026-05 self-compile in memory; superseded. |
| `RC-NATIVE-PASS-EXTRACTION.md` | [record] | Extracting the Go native Perceus RC into a pass before the self-host port (#4393); slices 1–4 landed, the native compiler since deleted. |
| `RC-PERCEUS-PHASE-1E-PLAN.md` | [record] | Widening refcounting from arrays to structs, strings, enums and closures in the Go native compiler, since deleted. |
| `RC-PERCEUS-PLAN.md` | [record] | The Go native compiler's RC and Perceus implementation, phase by phase; implemented, then deleted with the backends. |
| `RC-PERCEUS-SELF-HOST-PORT.md` | [record] | The goal-2 log of porting Perceus RC to the self-host compiler up to 2026-08-20; later entries are in `rc-log/`. |
| `RC-STRINGS-PLAN.md` | [record] | Bringing heap strings into Perceus reclamation (rc header plus a static-literal sentinel per backend); shipped. |
| `SELFHOST-BSTATE-RECLAIM-PLAN.md` | [record] | Reclaiming the SSA builder's BState churn; never implemented, and BState no longer exists. |
| `SELFHOST-COUNTED-UNITS.md` | [record] | The first landing of `ssaunits.fern`'s counted-unit plan; RC-DESIGN.md describes it as it stands. |
| `SELFHOST-DEPENDENCY-VERIFICATION.md` | [record] | The landing of `ssadeps.fern`, which verifies lifetime dependencies and dominance before ownership planning. |
| `SELFHOST-PERCEUS-REUSE.md` | [record] | Porting Perceus constructor reuse to `irlower.fern`; that lowering is deleted and reuse is planned in ssaunits. |
| `SELFHOST-PHYSICAL-RC.md` | [record] | The first landing of `ssarc.fern`, which lowers verified ownership plans to the stack IR. |
| `SELFHOST-RC-PLAN-PROMOTION.md` | [record] | A proposal to replace the shape-enumerated rc credit table with a promoted ownership plan; the table went with the AST lowering. |
| `SELFHOST-REFERENCE-LIFETIMES.md` | [record] | The landing of `ssalive.fern`'s dependency-aware SSA liveness. |
| `SELFHOST-SEMANTIC-PROJECTIONS.md` | [record] | The first landing of `ssasem.fern`'s typed semantic values and exact type identity. |
| `TYPED-AVAILABILITY-VALUES.md` | [record] | A slice of the Go typed-ownership pilot (`internal/semir`); the pilot was deleted. |
| `TYPED-BINDING-PLACES.md` | [record] | A slice of the Go typed-ownership pilot: typed binding places and an initialization verifier; deleted. |
| `TYPED-CLEANUP-REGIONS.md` | [record] | Cleanup regions for the Go typed-ownership pilot; deleted. |
| `TYPED-OWNERSHIP-IR-MIGRATION.md` | [record] | The typed ownership IR migration and its Go ARM64 pilot; the self-host typed lowering now produces every body. |

## Async & the wasm/WASI component stack

| Doc | Status | What it is |
| --- | --- | --- |
| `ASYNC.md` | [reference] | User guide to the std/async combinators (`Future[T]`, gather, race, with_deadline) and the WASI Preview-3 async export and stream surface. |
| `ASYNC-IMPLEMENTATION-RESEARCH.md` | [research] | How Koka, Lean 4 and Roc implement async and what that compiles to, recommending a stackless state machine plus a single-threaded reactor. |
| `CONCURRENCY-RESEARCH.md` | [research] | Survey of concurrency models (Erlang, Go, Rust, Trio, Zig, OCaml 5 and others) that led to colorless structured concurrency; predates std/async. |
| `ASYNC-FUTURE-UNIFICATION.md` | [record] | The fifth async-redesign slice: `Future[T]` resolving on wasm via pollables; std/task, std/reactor and std/wasm_reactor deleted. |
| `ASYNC-IMPLEMENTATION-PLAN.md` | [record] | The original async plan; its concurrent/await/race surface was removed by the redesign, while its reactor became `Future[T]`'s implementation. |
| `ASYNC-REDESIGN.md` | [record] | The decision to replace the concurrent/await/race surface with `Future[T]` plus gather/race/with_deadline; shipped as std/async. |
| `ASYNC-SELFHOST-IR.md` | [record] | The plan to compile std/async through the self-host IR path; done, `async_combinators_test.fern` runs 6/6. |
| `NET-P3-SUSPENSION-PLAN.md` | [record] | Networking P3: inferred suspension of handlers as resumable state machines, cancellation and streamed bodies; landed (#9857). |
| `STREAM-TYPE-SURFACE.md` | [record] | The design checkpoint that exposed `stream[T]` and kept `future[T]` internal; shipped (#11530). |
| `WASI-PREVIEW2.md` | [record] | The WASI Preview 1 to 2 component migration; complete, with components composed in-tree. |
| `WASI-PREVIEW3-ASYNC-PLAN.md` | [record] | Preview-3 component-model async; async exports, async imports and `stream[T]` shipped for scalar types. |
| `WASM-COMPONENT-GENERATOR.md` | [record] | A generated wasm component envelope in the self-host; complete. |
| `WASM-REACTOR-PLAN.md` | [record] | The wasm reactor on Preview-2 pollables and the composition blocker it hit; shipped. |
| `WIT-BRING-YOUR-OWN.md` | [record] | Compiling against a user-supplied WIT world; P1–P6 shipped in both compilers. |

## Stdlib

| Doc | Status | What it is |
| --- | --- | --- |
| `ARRAY-SHAPES.md` | [policy] | Normative rules for std/ndarray: a counted handle with shape, strides and offset, views, shape mismatch, and when a view materializes. |
| `CURSOR-IDIOM.md` | [policy] | The immutable read-and-advance cursor idiom for stdlib parsers and readers. |
| `CRASH-ONLY-SERVE.md` | [reference] | Crash-only serving in std/serve: the trap taxonomy, the supervisor with a prefork worker pool, and its test bar. |
| `DST-PLATFORM-BRIEF.md` | [reference] | Deterministic simulation testing in std/sim, std/sim_platform and std/sim_fetch: a virtual clock and seeded fault injection. |
| `PERSISTENT-COLLECTIONS.md` | [reference] | std/ordmap, ordset, pmap, pset and pvec: design, measurements, and what the compiler still owes them. |
| `STDLIB.md` | [reference] | The standard library: the std/ and core/ module map, conventions and receiver methods. |
| `WASI-SOCKET-ERRORS.md` | [reference] | How WASI socket error-code results map to negative Preview 1 errnos. |
| `FEATURE-AUDIT.md` | [tracker] | Every built-in and stdlib function against every backend and the interpreter, with a log of the bugs found and fixed. |
| `SOTA-STDLIB-MATRIX-2026.md` | [tracker] | The execution matrix over the blueprint: verdict, prerequisite, phase and acceptance gate per row. |
| `ATLAS-PLATFORM-PLAN.md` | [plan] | The external Atlas blueprint reconciled against Fern: SIMD, dispatch, allocator and concurrency substrate, the fused-intrinsic SIMD ABI, and a sequenced plan, partly landed. |
| `NET-P8-HTTP3-PLAN.md` | [plan] | Networking P8: QUIC and HTTP/3, slice by slice; slices 1–7 landed, and the rest are open. |
| `NETWORKING-RESEARCH-2026-09.md` | [research] | The survey behind the networking plan: server performance, API ergonomics, TLS/DNS/QUIC/io_uring, simulation testing, security and operations. |
| `SOTA-STDLIB-BLUEPRINT.md` | [research] | Each stdlib primitive audited against the best known algorithm, with a verdict and recommended order. |
| `STDLIB-DESIGN-RESEARCH.md` | [research] | Design research on four stdlib areas: HTTP, JSON, date/time and I/O. |
| `MAP-SPECIALIZATION.md` | [record] | An unstarted proposal to monomorphise `Map[K, V]`; two attempts regressed code size, and keyed helpers landed instead. |
| `NET-P0-MESSAGE-LAYER-PLAN.md` | [record] | Networking P0: allocations of the HTTP framing path and serve loop, taken to zero per hello request; done (#9853). |
| `NET-P4-TLS-PLAN.md` | [record] | Networking P4: TLS 1.3 slice by slice, from crypto primitives to kTLS; done (#9858). |
| `NET-P5-FRAMEWORK-PLAN.md` | [record] | Networking P5: router, bodies, cookies, SSE/WebSocket, static files, compression and observability; done (#9859). |
| `NET-P6-HTTP2-PLAN.md` | [record] | Networking P6: HPACK, HTTP/2 framing, serving, limits, h2spec, gRPC interop and the client; done (#9860). |
| `NET-P7-IO-URING-PLAN.md` | [record] | Networking P7: the opt-in io_uring backend behind the async Driver; measured level with epoll and kept (#9861). |
| `STDLIB-ROADMAP.md` | [record] | A seven-language stdlib survey and prioritised additions; its tracking issues are closed. |

## coreutils

| Doc | Status | What it is |
| --- | --- | --- |
| `COREUTILS.md` | [reference] | The coreutils project (#8278): GNU 9.12 byte parity, exemptions, the oracle harness, self-host and wasm legs, and per-utility performance. |

## Tooling, tests & process

| Doc | Status | What it is |
| --- | --- | --- |
| `AGENT-PROMPTING.md` | [reference] | How the published Claude prompting guidance applies here: behaviours CLAUDE.md corrects for, harness settings no prompt can fix, and the two places this repo overrides it. |
| `CI-NATIVE-TEST-WORKERS.md` | [reference] | How CI runs the native ARM64 suite in two worker processes via `cmd/ci-test-workers`, with the measurement behind it. |
| `CI-SIGNOFF.md` | [reference] | How to run a CI lane locally and record a signoff with `scripts/signoff` so CI skips that lane. |
| `CI-WEIGHT-REFRESH.md` | [reference] | How `scripts/ci-test-weights refresh` rebuilds the shard weight table from several successful runs. |
| `CI-X86-WORKERS.md` | [reference] | How CI runs the x86-64 suite in two worker processes via `cmd/ci-test-workers`, with the measurement behind it. |
| `COVERAGE.md` | [reference] | Line and branch coverage via `-cover` / `FERN_COVER=1`, and the `-cover-report` summary and LCOV output. |
| `EMBED.md` | [reference] | Compile-time asset embedding: `-embed DIR` and `__fern_asset` / `__fern_asset_bytes`. |
| `FERN-PROPOSALS.md` | [reference] | The Fern Proposal program: build an app as a user, fix the worst defect hit, report it as an issue and PR. |
| `FUZZING.md` | [reference] | The fuzzing system: fernsmith generators, the oracles, the mutation score that measures them, and the roadmap. |
| `HARDENING.md` | [reference] | Binary hardening: opt-in `-harden` for BTI and PAC on arm64-linux, and the default W^X segment layout. |
| `LINT.md` | [reference] | `fern -lint`: why it runs on the parse tree, its rules including cyclomatic complexity, `[lint]` config and allow comments. |
| `LITERATE.md` | [reference] | Literate programming: `.fern.md` documents, the chunk grammar, tangle and weave, and diagnostic line remapping. |
| `LOCAL-DEV-LOOP.md` | [reference] | Machine facts for local work: suite timings and sharding, build memory budgets, `FERN_*` switches, exit codes, and which instrument to measure with. |
| `LSP-INTEGRATION-PLAN.md` | [reference] | `fern -lsp`: what each request answers, how the VS Code extension and the playground run it, and its gates. |
| `PROFILING.md` | [reference] | Profiling a native program with `FERN_PROFILE=cpu`: SIGPROF sampling and collapsed stacks for flame graphs. |
| `SANITIZER.md` | [reference] | `-sanitize` / `FERN_SANITIZE=1`: rc over-release, use-after-free quarantine and leak census, with output and exit codes. |
| `TEST-GATES.md` | [reference] | Which suites prove what for which kind of change, which look authoritative and are not, and what nothing gates. |
| `TRANSLATION-VALIDATION.md` | [reference] | SMT (Z3) translation validation of compiler rewrites, Alive2-style; the first slice validates strength reduction (#5543). |
| `COMPLEXITY-CAMPAIGN.md` | [tracker] | The campaign to cut cyclomatic complexity in first-party Fern sources, slice by slice, against the lint ratchet. |
| `DIAGNOSTIC-UX-RESEARCH.md` | [research] | Survey of compiler diagnostics (Elm, Rust, ReasonML, TypeScript) and what Fern's would need to match them (#4413). |
| `IDE-COMPILATION-RESEARCH.md` | [research] | Survey of rust-analyzer-style incremental compilation for `fern -lsp`: what to port and in what order (#4415). |
| `READING-NOTES-2026-10-21.md` | [research] | Seven outside sources (CRAP, Highlights, packslip, STE, MultiTable, constmap, usage) read against the code on 2026-10-21, with the decision each led to. |
| `PLAYGROUND-SELFHOST-WASM.md` | [record] | Whether the self-host compiler can power the browser playground; it can, and has since 2026-09-28. |
| `TEST-RUNNER-MIGRATION.md` | [record] | An early audit of moving 72 Go test files to the pure-Fern TAP runner; its counts are out of date. |
| `TOOLCHAIN-SELF-HOSTING.md` | [record] | Building every target with no external toolchain on `$PATH`; complete. |

## Logs and supporting material

Dated records live in topic directories, named `YYYY-MM-DD-slug.md`. In
`rc-log/` the newest file is the live list of RC work.

| Directory | What it holds |
| --- | --- |
| `rc-log/` | Reference counting: the live work list and the record of each RC change since 2026-08-20. |
| `ssa-log/` | The SSA backend's changes and measurements. |
| `compiler-log/` | Compiler size and compile-time investigations. |
| `string-log/` | The string and byte migration reports (2026-09-30 to 2026-10-04). |
| `coreutils-log/` | coreutils parity and performance reports. |
| `typed-log/` | The typed-ownership slices of 2026-09-09. |
| `ci-log/` | CI changes and the CI performance investigation. |
| `benchmarks/` | The scripts and frozen inputs behind the measured reports. |
| `measurements/` | Raw measurement output the reports cite. |
| `contexts/` | Spec summaries of the FIP and runtime experiments. |
| `proposals/` | The Fern Proposal draw list and settled rulings (FERN-PROPOSALS.md). |
