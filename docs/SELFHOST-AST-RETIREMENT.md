# Retiring the self-host legacy AST emitters (#3457)

Status: **IN PROGRESS — slice 1 done; the rest is memory-gated.** This doc is
the single home for the #3457 endgame (retire `asm.fern` / `asm_arm64.fern` /
`wasm.fern`, the pre-IR AST→asm emitters, plus the ~512-function merged-bundle
budget). It exists because the analysis kept being re-derived and mis-scoped —
CLAUDE.md's "VERIFY tracker state against the code first; #3457's blockers have
repeatedly lagged reality" warning applies especially here. Everything below was
verified against the code (2026-07-26).

## Where the roadmap stands (context)

Goals 1 and 2 are **essentially complete**, so #3457 is the remaining frontier
before the native freeze (`docs/NATIVE-CONVERGENCE.md`):

- **Goal 1 (full IR subset).** The last *per-function* remnant — genuinely
  two-typevar `Result[T, E]` returns — closed 2026-07-26 (`parser.fern`
  `result_two_bare_vars`, clause (c′)). The only AST fallbacks left are the
  non-per-function ones **owned by #3457**: the merged-bundle path + its budget.
- **Goal 2 (Perceus reuse).** Substantially met; `docs/SELFHOST-PERCEUS-REUSE.md`
  §2/§3 (with its 2026-07-17 correction) is the record. The one genuinely-open
  delta (own-param enum/string field reuse) is *fundamentally* blocked — a
  parameter has no bind literal to prove field freshness — not a tractable slice.

## The AST-emitter call graph (what must go, and what blocks each)

The three legacy emitters are still reached through these entry points
(`asm.emit_module` / `asm_arm64.emit_module` / `wasm.emit_module`):

| Site | Path | Reachable today because… |
|---|---|---|
| `asm_run.fern:23` | merged AST (x86) | `TestSelfHostBootstrapsItself` / `TestSelfHostAsmRunX86_64` pipe programs through it |
| `asm_load_run.fern:376` (arm64 373) | merged AST | `TestSelfHostStage2FixedPoint{,Arm64}` fixpoint on this driver |
| `asm_modload_run.fern:335` (arm64 332) | merged AST default | `TestSelfHostModloadFixpointX86_64` / `…Arm64` invoke the driver with **no** `-per-module-*` flag → merged path |
| `asm_ir_run.fern:158` (arm64 135/144) | AST *fallback* of the IR differential | reached when `emit_module_ir_gated` returns "" (an ineligible program) |
| inline `main.fern` | AST | `TestSelfHostStage2Bootstrap` / `…Stage2Compiler` build a one-off compiler over `asm.emit_module` |

Plus the IR path's own coupling to the AST files (the untangle target, slice 4):

- **x86: already clean.** `asm_ir.fern` is self-contained — its own
  `emit_ir_runtime` (asm_ir.fern:831–2897, ~2067 lines of hand-written bodies)
  and `emit_ir_runtime_fern_fn` (5327, IR-compiles the Fern runtime helpers via
  `emit_function_via_ir`). `asm.fern` imports `asm_ir`, not the reverse.
- **arm64: NOT clean.** `emit_module_ir_unit_arm64` lives in **`asm_arm64.fern`**
  (3968) and calls **`asm_arm64.emit_runtime`** (4480–9354, ~4870 lines) on the
  entry unit. That `emit_runtime` compiles the Fern runtime helpers with the
  **AST `emit_function`** (via `emit_runtime_fern_fn`, 31 interleaved call sites)
  and uses the AST `emit_ldc` — so the arm64 IR path transitively depends on the
  AST emitter. (`asm_arm64_ir.fern` — the arm64 IR *instruction selector*,
  `emit_function_via_ir` — is already free of `asm_arm64`; the runtime is the gap.)
- **wasm: NOT clean, and larger.** `wasm_ir.fern` reuses `wasm.fern`'s WAT
  runtime extensively (heap/RC, `str_*_helpers`, `to_string_helpers`,
  `divrem_helpers`), and the per-module IR framing entry points
  `wasm.emit_ir_module_units` / `wasm.emit_ir_rc_bodies_from`
  (`wasm_modload_run.fern:336-337`) **live in `wasm.fern`**, not `wasm_ir.fern`.

## The gate: #3425 (self-host runtime memory)

The merged path is fast (one emit) but needs the AST emitter + 512-budget. The
per-module IR path is the replacement but its **self-host-built (gen1) emit runs
~16 min serially / is arena-limited** — the self-host runtime does not reclaim
the whole-program string/analysis allocations during a large-module emit (#3425,
the reclamation frontier that goal-2 reuse reduces but does not close, since the
peak is the accumulated *output*, not per-iteration churn). This is why:

- `TestSelfHostPerModuleFixpointX86_64` (proves gen0==gen1 per-module
  byte-identity — the property the default-flip depends on) is **env-gated**
  (`RUN_PERMODULE_FIXPOINT=1`), too slow for a CI lane.
- Routing the merged bundle through IR instead peaks ~13 GB RSS (the same leak).

So the budget must retire **with** the merged drivers, not before, and the
per-module path cannot become the primary CI fixpoint guard until the emit is
cheap enough. **#3425 is the true unblocker for slices 2/3/5.**

### #3425 is a bounded, reference-guided port (not research-grade)

The root cause is concrete: the **self-host** RC runtime (`asm_ir.fern`'s
`emit_ir_runtime`, mirrored in `asm.fern` / `asm_arm64.fern` / `wasm.fern`) uses
a size-classed freelist of **65536 exact word-classes** (`__fern_freelist`, up to
~512 KB blocks). Anything larger has no class and **leaks into the bump arena**
(asm_ir.fern:1067 "large-tier buffers leak (sound)"), so a long-running emit that
frees big blocks (per-function analysis temps, strbuf-growth cast-offs)
accumulates them until `__fern_alloc`'s bounds check `exit(137)`s.

The **native** runtime already solved this: `internal/codegen/x86_64/x86_64.go`
(~L4056) emits a **two-tier segregated freelist** — small tier (0..127, 16-byte
classes 16..2048) **plus a large tier** (heads 128+b, power-of-two capacity
2^b, b=12..30, i.e. 4 KiB..1 GiB), free blocks storing the successor pointer in
their first 8 bytes. That large tier is exactly what the self-host lacks.

This asymmetry **is** the "gen0 fits, gen1 OOMs" behaviour: gen0 (self-host
source compiled by the Go backend) runs the **native** runtime → has the large
tier; gen1+ (compiled by a self-host-built compiler) run the **self-host**
runtime → no large tier → leak. So the fix is a **port**, not an invention: add
the native large-tier binning to the self-host runtime emitters. It re-baselines
the fixpoints (the emitted runtime bodies change) but stays self-reproducing, and
it is memory-safety-critical + cross-cutting (4 backends), so land it native-parity-
first with the differential + both fixpoint suites as the nets — start with the
x86 self-host runtime (`asm_ir.fern`), locally gated by the x86 differential +
`TestSelfHostModloadFixpointX86_64`, then arm64 / wasm.

## Slices

- **Slice 1 — retire `bundle_demo.fern`. DONE** (#5603). Dead AST-only demo,
  coverage redundant with the modload fixpoint's file-based multi-module cases.

- **Slice 2 — flip the bootstrap/fixpoint to per-module. BLOCKED on #3425.**
  Make `TestSelfHostModloadFixpointX86_64` drive `-per-module-*` (as
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` already does for gen0) so no
  path emits the merged bundle. The gen0 (Go-built) per-module emit is already
  fast enough for CI; the *self-reproduction* proof needs the slow gen1 emit,
  which is the #3425 wall. Options when unblocked: (a) accept a slow env-gated
  fixpoint lane, (b) fix #3425 so gen1 is cheap, (c) a gen0-only fast guard that
  forgoes self-reproduction (weaker).

- **Slice 3 — replace the now-unreachable AST fallbacks.** Once slice 2 makes the
  merged path unreachable, replace `asm.emit_module` at the sites above with a
  per-module call or a clean error. Note the `asm_ir_run.fern:158` fallback is
  the *differential oracle* (compares IR vs AST); retiring it means trusting the
  IR path outright, which the fixpoint + differential suites must justify first.

- **Slice 4 — untangle the arm64/wasm IR runtime from the AST files.**
  Independent of #3425, but **delivers no standalone deletion** (the driver still
  imports the AST file for the merged path until 2/3 land), so it is prep, best
  done *with* or *after* the memory work — not in isolation.
  - **4a arm64 (~5k lines).** Port x86's structure: give `asm_arm64_ir.fern` its
    own `emit_ir_runtime` (duplicate `asm_arm64.emit_runtime`'s hand-written
    bodies) + `emit_ir_runtime_fern_fn` (arm64 sibling of asm_ir's, IR-compiles
    the Fern helpers via `asm_arm64_ir.emit_function_via_ir`), move
    `emit_module_ir_unit_arm64` there, repoint `asm_modload_run.fern:233`. This
    **changes arm64 IR codegen** (the 31 Fern helpers become IR-compiled, matching
    x86) → re-baselines the arm64 fixpoint; it is NOT a byte-preserving move
    (`emit_function`'s transitive closure IS the AST emitter, so it can't move
    with the runtime). Note the helper sets have **diverged**: arm64 currently
    Fern-emits 11 helpers x86 does not (`i32_pow/gcd/lcm`, `i64/u64_to_string`,
    `arr_i32_{sum,product,min,max,index_of}`, `arr_str_index_of`) and x86 IR-emits
    3 arm64 keeps hand-written (`clock`, `env`, `random_bytes`) — reconcile
    per-helper (most of the 11 are likely `has_need`-gated off on the IR path;
    verify before porting). Validate: build the driver on x86 (compiles → module
    boundaries resolve), emit whole-compiler arm64 asm and diff (only the 31
    helper-body regions should change), run small arm64 programs under
    `qemu-aarch64` for correctness, then `TestSelfHostFixpointArm64` (the arm64
    byte-identity fixpoint) as the final gate.
  - **4b wasm (larger).** Same shape but move `emit_ir_module_units` /
    `emit_ir_rc_bodies_from` + the shared WAT runtime helpers out of `wasm.fern`
    into `wasm_ir.fern`.

- **Slice 5 — delete `asm.fern` → `asm_arm64.fern` → `wasm.fern` + the
  512-budget.** Repoint every driver + Go test module list; retire the AST-side
  differential tests (their oracle role ends when the IR path is trusted). Gated
  on all the above + #3425.

## Recommended order

1. **#3425** (the actual unblocker) — measure the self-host emit's RSS on a large
   module to find the non-reclaimed allocation; a bounded reclamation win here
   unblocks 2/3/5 and makes the per-module fixpoint cheap. Research-grade; no
   guaranteed bounded slice, but the highest leverage.
2. Then **slice 2 → 3**, with the per-module fixpoint now affordable.
3. **Slice 4a/4b** alongside/after, since the untangle only pays off once the
   merged path is gone.
4. **Slice 5** last.

Doing slice 4 first (the tempting "mechanical" step) is a ~5k-line, qemu-gated
duplication that unlocks nothing on its own — avoid it until the endgame is
actually reachable.
