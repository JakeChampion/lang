# Self-host backend: one production lowering — the IR path

**Status:** DECIDED (2026-07-03). The **stack IR** (`irlower.fern` →
`asm_ir.fern` / `asm_arm64_ir.fern` / `wasm_ir.fern`) is the single
production lowering path for the self-hosted compiler. The SSA
`build_func` pipeline is **demoted to experimental / opt-in** (`-ssa`);
`SELFHOST-SSA-ALWAYS.md` is **shelved**.
**Owner:** compiler / self-host.
**Resolves:** #4391.

## The question

The self-host backend was advancing **two mutually-exclusive futures**,
each claiming the same endpoint — deleting the legacy AST emitters
(`asm.fern` / `asm_arm64.fern` / `wasm.fern`) — through a *different*
replacement pipeline:

1. **SSA-always** (`SELFHOST-SSA-ALWAYS.md`): make
   `ssa.build_func` → `{ssa_x86, ssa_arm64, ssa_wasm}` the production path.
   Per-function coverage reached 100% (measured via `-ssa-scan`), blocked
   only on a Phase-4 whole-compiler memory wall.
2. **Per-module IR retirement** (#3451 / #3457): make
   `irlower.fern` → `{asm_ir, asm_arm64_ir, wasm_ir}` compile per module and
   retire the AST emitters that way.

Every week both advanced was a week of work one of them would delete. This
doc forces the call, exactly as the native side's `SSA-DECISION.md` did.

### Ground truth before this decision

- **The effective default was three live lowering pipelines in one
  compile.** `fern.fern` tried `try_ssa` first (whole-program,
  all-or-nothing); on bail it fell to the AST emitters, which *internally*
  re-route to the IR path (`asm.fern:emit_module` when `use_ir` — default
  `true`, `asmcore.fern` — and `all_eligible`). So the real default was
  **SSA → [AST shell → IR path → AST emit]**. The bootstrap fixpoints never
  touched SSA.
- **Goal 2 (the Perceus port) lives in the IR layer** (`ir.fern`,
  `RC-PERCEUS-SELF-HOST-IR.md`). The SSA path bypasses it and would need its
  own RC story (`SELFHOST-SSA-ALWAYS.md` notes its subset can't even lift RC
  `call_direct`s). The 2026-07 parity review found bugs in exactly these
  duplicated value paths (#4341: hex literals wrong in interp / vm / **ssa**,
  correct in IR).

## Decision: the IR path is the single production lowering

We **declare the stack IR the production lowering** and demote SSA
`build_func` to an experimental, opt-in path. Rationale:

1. **It is already the de-facto production path.** It is what #3451 / #3457
   converge on, what the Perceus port (goal 2) targets, and where the 250+
   e2e fixtures sit. The AST shell *already* routes eligible modules through
   it (`use_ir=true`). Declaring it production changes the strategy, not the
   code that runs for most programs.

2. **SSA `build_func` is a second, redundant frontend.** It is a complete
   AST→SSA lowering (~730-line `build_expr` plus its own Option/Result
   boxing, closure ABI, and runtime ops re-derived per backend) built in
   parallel with the IR the compiler already produces. Carrying two
   AST→codegen frontends is the exact dual-path parity hazard the project
   works to avoid — and it costs a whole differential axis (`build_func` ×
   three backends) on every value-shape change.

3. **The memory wall is structural, not incidental.** SSA self-hosting
   (`SELFHOST-SSA-ALWAYS.md` Phase 4) is blocked because `try_ssa` is
   all-or-nothing: it must build *every* function up front to prove the whole
   program is in-subset, and that first build pass alone overruns the no-GC
   bootstrap heap. The per-module IR path has no such all-or-nothing barrier —
   it compiles module by module — so it reaches a byte-stable self-compile
   without waiting on RC-freeing to land.

4. **One RC story, not two.** Perceus reclamation is being ported into the IR
   layer. Keeping SSA on the production path would demand a *second* RC
   implementation for a lowering we've decided not to ship. Retiring
   `build_func` keeps memory management on a single, auditable path.

## What changes

- **Flip `fern.fern`'s default** ✅ (this PR). SSA is no longer attempted by
  default; `try_ssa` runs only under the opt-in **`-ssa`** flag (experimental,
  x86-64 / arm64 / a wasm subset). The production default is the AST shell →
  IR path. `-no-ssa` is kept as a back-compat no-op (the IR/AST path is
  already the default). This supersedes `SELFHOST-SSA-ALWAYS.md` Phases 1–4,
  which are shelved (see the banner atop that doc).

- **Retire `ssa.build_func`** (sanctioned follow-up). Remove the second
  AST→SSA frontend — `build_func` / `build_expr` and its per-backend
  Option/Result boxing, closure ABI, and runtime-op derivations — while
  **keeping** `ssa.fern`'s data model + optimiser and `ssa_lift.fern`
  (stack-IR → SSA) as the *downstream* optimiser entry, exactly mirroring
  native's architecture (`ssa_lift.fern` already documents `build_func` as
  the redundant thing it exists to replace). This deletes ~6.5k lines and a
  whole parity axis. Land it **before** #3457 so the retirement removes one
  fallback, not two.

- **Retire the superseded `ir_x86.fern` + `ir_run` / `ir_x86_run` PoC**
  (sanctioned follow-up). These are the early stack-IR proof-of-concept
  (smoke-test only, a handful of references), superseded by
  `asm_ir.fern` / `asm_arm64_ir.fern` / `wasm_ir.fern`.

The `-ssa` opt-in and the `ssa_lift.fern` optimiser entry are deliberately
kept: SSA stays a valid *experimental* lowering and a live **optimiser**
(lifted from the IR, native's shape), so the investment in `ssa.fern`'s
pass suite and register allocator is not thrown away — only the redundant
*frontend* is.

## Non-goals

- This is **not** a decision to delete `ssa.fern`, its optimiser, its
  register allocator, or the `ssa_*` backends. They remain, exercised through
  `-ssa` and the SSA emit test matrices.
- This does **not** change the native (Go) compiler. Native already lowers
  through `internal/ir` and lifts to SSA downstream; see the reconciliation
  note appended to `docs/SSA-DECISION.md`.

## Tripwires (any one reopens the SSA-as-production question)

- The per-module IR path (#3457) stalls on a blocker the SSA path provably
  does not have.
- A profiled, real Fern program where an SSA-class optimisation the IR
  peephole passes cannot reach is the demonstrated bottleneck — *and* it is
  reachable via `ssa_lift.fern` (the downstream optimiser), which does **not**
  require resurrecting `build_func`.
- The Perceus-in-IR port hits a wall that an SSA-form RC would avoid.

## Maintenance contract while SSA is opt-in

- Keep `-ssa` building and its emit test matrices
  (`self_host_ssa_emit*_test.go`, the CLI `emit-ssa*` cases) green.
- Keep `ssa_lift.fern` + the `ssa.fern` optimiser / regalloc building and
  tested (they are the retained downstream layer).
- New language features are **not** required to land in `build_func` while it
  is opt-in and slated for retirement; they must land on the **IR path** (the
  production lowering) with differential cases at the layer they touch.
