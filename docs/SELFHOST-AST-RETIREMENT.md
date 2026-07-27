# Retiring the self-host legacy AST emitters (#3457)

Status: **IN PROGRESS — slice 1 done; #3425 (the memory gate) CLOSED; slice 2
is now unblocked.** This doc is the single home for the #3457 endgame (retire
`asm.fern` / `asm_arm64.fern` / `wasm.fern`, the pre-IR AST→asm emitters, plus
the ~512-function merged-bundle budget). It exists because the analysis kept
being re-derived and mis-scoped — CLAUDE.md's "VERIFY tracker state against the
code first; #3457's blockers have repeatedly lagged reality" warning applies
especially here. Everything below was verified against the code (2026-07-26).

**Update 2026-07-26 — #3425 is closed.** The large-tier freelist port
predicted below LANDED on both native backends (x86 `asm_ir.fern` #5609, arm64
`asm_arm64.fern` #5614), and the direct proof it was meant to unblock now
passes: `TestSelfHostPerModuleFixpointX86_64` (env-gated,
`RUN_PERMODULE_FIXPOINT=1`) is **GREEN** — a self-host-BUILT compiler (gen1)
per-module-emits the whole compiler (35 units) in ~998 s with **no arena OOM**,
and gen0 == gen1 byte-identically across all 35 units (per-module emit is
self-reproducing). Measured gen1 peak: **~7.6 GB RSS per emit window** — under
the 8 GiB arena ceiling that the leaked large blocks previously blew past. So
**the arena wall is no longer the slice-2 blocker**; the only remaining slice-2
obstacle is the *CI-affordability* of the gen1 per-module fixpoint (serial
~16.6 min > a 13-min shard; 2-way parallel needs ~15 GB → OOM-risky on a 16 GB
runner). See "Slice 2" below for the now-concrete plan.

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

## The gate: #3425 (self-host runtime memory) — CLOSED

The merged path is fast (one emit) but needs the AST emitter + 512-budget. The
per-module IR path is the replacement. Its **self-host-built (gen1) emit** was
arena-limited — the self-host runtime did not reclaim the whole-program
string/analysis allocations during a large-module emit (#3425). **That is now
fixed** (the large-tier freelist port, below), and the direct proof
(`TestSelfHostPerModuleFixpointX86_64`, still env-gated `RUN_PERMODULE_FIXPOINT=1`
for its ~16.6-min serial runtime) is GREEN: gen1 emits all 35 units with no
arena OOM, gen0 == gen1 byte-identically. **The arena wall is gone; the only
residual slice-2 obstacle is CI *time*, not memory.**

### #3425 was a bounded, reference-guided port (prediction confirmed)

The root cause was concrete: the **self-host** RC runtime (`asm_ir.fern`'s
`emit_ir_runtime`, mirrored in `asm.fern` / `asm_arm64.fern` / `wasm.fern`) used
a size-classed freelist of **65536 exact word-classes** (`__fern_freelist`, up to
~512 KB blocks). Anything larger had no class and **leaked into the bump arena**,
so a long-running emit that freed big blocks (per-function analysis temps,
strbuf-growth cast-offs) accumulated them until `__fern_alloc`'s bounds check
`exit(137)`d.

The **native** runtime already solved this with a two-tier segregated freelist
(a large tier atop the small classes). The self-host runtime lacked it — which
**is** the "gen0 fits, gen1 OOMs" asymmetry: gen0 (self-host source compiled by
the Go backend) runs the **native** runtime → has the large tier; gen1+
(compiled by a self-host-built compiler) run the **self-host** runtime → no large
tier → leak.

**The port landed (2026-07-26):**
- **x86 (`asm_ir.fern`, #5609):** a `__fern_large_freelist` array + a
  `.Lalloc_large` path in `__fern_alloc` and a `__fern_large_push` free helper,
  redirecting `__fern_arr_dec` and `__fern_str_free` off the leak. LINEAR
  512-KiB binning (`class = round_up(size, 512 KiB) >> 19`, 1..2048 for
  512 KiB..1 GiB) — deliberately no `bsr` / variable-count shift, because the
  self-host `x86_gas` assembler (exercised by `TestSelfHostX86Capstone`) has
  neither; the linear scheme uses only `leaq`/`andq $imm`/`shrq $imm`.
- **arm64 (`asm_arm64.fern`, #5614):** the same design in aarch64 (mask via
  `lsr`/`lsl`, wide immediates built from `1<<19`, tail-`b` to preserve x30).
- **Both re-baselined their fixpoints byte-identically and stay
  self-reproducing** — confirmed by the modload fixpoints (CI) and the gen1
  per-module fixpoint (env-gated, GREEN).
- **wasm deferred** (task #18): wasm uses `memory.grow` (growable linear
  memory), so a leaked large block grows RSS but never hits a fixed arena wall
  → no exit-137, no slice-blocking. It is an RSS optimisation only, and adding a
  large tier there shifts `heap_base` across the byte-identity surface for no
  correctness gain; low priority.
- **Remaining self-host free sites — x86 + arm64 DONE (2026-07-27).**
  The self-host runtime's `__fn___fern_str_arr_free` (`.Lsaf`) /
  `__fern_arrarr_free` (`.Laaf`) / `__fern_strarrarr_free` (`.Lssaf`) /
  `__fern_optarrarr_free` (`.Loaf`) / `__fern_snapshot_dec` (`.Lsd`) /
  `__fern_arr_push_owned` (`.Lapo`) *outer-buffer* frees previously
  `leak (sound)`ed a ≥512 KiB collection buffer; each now recycles it via
  `__fern_large_push` (the same redirect `arr_dec` / `str_free` already use). This
  bounds RSS for the general-purpose large-collection programs Fern now targets
  (the free sites fire on Perceus-proven-fresh, non-escaping locals). x86 in
  `asm_ir.fern` (#5651), arm64 in `asm_arm64.fern` (the #5609→#5614-style mirror:
  `.Lsaf`/`.Laaf`/`.Lssaf`/`.Loaf`/`.Lapo` `bl`+`b` through the frame that already
  saves x30; `.Lsd` a terminal tail-`b` since `__fern_large_push` preserves x0).
  - **Still leaking on BOTH backends** (parity, separate follow-ups): the
    `__fern_alloc_reuse` oversize-donor discard (`.Lsarelo`), and arm64-only, the
    `__fern_str_free` DATA buffer's ≥512 KiB path (`.Lstrfree`, a #5614 gap x86
    already closed). Neither is a collection *outer* buffer, so both are out of
    scope for the #5651 mirror.
  - **Measured: this is soundness-completeness, NOT an arena-wall win — the doc's
    original assessment was right.** A single-process `-per-module-emit-all` gen1
    emit still OOMs at the SAME batch boundary with these sites recycled as
    without (an A/B test: byte-for-byte identical exit-137 at batch `[8:16]`). So
    these sites free *small* collection buffers in the compiler's own emit; the
    ≥512 KiB large path is rarely taken. The emit-all single-process batch limit
    is **arena-structural** — the bump pointer only retreats on process exit, and
    the self-host runtime serves fewer cross-window allocations from the freelist
    than the (fuller) native runtime, so gen1 accumulates where gen0 does not.
    Fixing THAT would need arena checkpoint/reset between windows, not more free
    redirects; it is deferred (the serial per-module fixpoint stays the proof).

## Slices

- **Slice 1 — retire `bundle_demo.fern`. DONE** (#5603). Dead AST-only demo,
  coverage redundant with the modload fixpoint's file-based multi-module cases.

- **Slice 2 — flip the bootstrap/fixpoint to per-module. UNBLOCKED (#3425
  closed); the remaining question is CI time, not memory.**
  Make `TestSelfHostModloadFixpointX86_64` drive `-per-module-*` (as
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` already does for gen0) so no
  path emits the merged bundle. The gen0 (Go-built) per-module emit is already
  fast enough for CI; the *self-reproduction* proof needs the gen1 emit, which
  no longer OOMs but runs ~16.6 min **serially** — past a 13-min shard.

  **Measured cost model (2026-07-26, gen0 driver, `RUN_MEASURE_SPLIT`).** Two
  earlier hypotheses are REFUTED by direct measurement:
  - The whole-program **parse+infer floor is only ~1.34 s** — so a
    single-process "parse once, loop-emit" mode saves essentially nothing on its
    own. *emit-all as a parse-once win is refuted.*
  - The per-unit emit cost is ~20–28 s and is **nearly independent of the unit's
    own size**: a 3-function module emits in 20.3 s, a 922-function module in
    27.3 s. So the cost is NOT per-window lowering — it is the **~22
    whole-program side-tables** `emit_module_funcs` derives on every call
    (`array_ret_fns_of` / `borrowable_params_interproc` / `str_ret_fns_of` / …,
    each an O(all_funcs≈1000) scan; asm_ir.fern:5489–5519). They run once per
    unit × 35 units. *(The code comment there calling them "cheap relative to the
    lowering they feed" holds only for large modules; for the many small units of
    the whole-compiler emit they dominate.)*
  - Because **every** gen1 unit peaks 5.5–7.8 GB (the retained whole-program view
    + those per-unit tables), 2 units won't fit a 16 GB runner. *Memory-budgeted
    parallelism is refuted too — there is no "one big, 34 small" split to exploit.*

  So the real lever is **hoisting the ~22 whole-program side-tables to
  compute-once** (the same move `wasm_ir.lower_all_for` and the `cache` mechanism
  at asm_ir.fern:5481 already make), which *requires* a single-process
  **`-per-module-emit-all`** driver mode to share them across units. Together
  they cut the ~20 s/unit recompute → gen1 per-module could drop from ~16.6 min
  to ~2–3 min AND lower per-unit peak (no per-unit table alloc), making the
  per-module fixpoint a real CI guard. Plan:
  - (a) **Add `-per-module-emit-all -out-dir DIR`** to `asm_modload_run.fern`:
    parse+infer once, then loop every module/window emitting each unit to `DIR`,
    passing a **once-computed** whole-program-table bundle into
    `emit_module_ir_unit` → `emit_module_funcs`. In-driver windowing mirrors the
    harness `emitWindowSize` (func_budget 100 + 300 KB byte budget) so the units
    are **byte-identical to the per-process fixpoint's** — a free correctness
    check.
  - (b) Refactor `emit_module_funcs` to accept the precomputed table bundle
    (compute-at-call-site for the single-unit callers = byte-identical + same
    cost; compute-once for emit-all = the win).
  - (c) Then point `TestSelfHostModloadFixpointX86_64` at emit-all; keep the
    env-gated per-process `TestSelfHostPerModuleFixpointX86_64` as belt-and-braces.
  - **gen0 parallel per-module is already CI-affordable (~3.3 min)** — the fast
    guard `TestSelfHostModloadPerModuleWholeCompilerX86_64` already exists; only
    the gen1 self-reproduction proof needs the hoist to become CI-cheap.

  **BLOCKER found + ISOLATED (2026-07-26) — a self-host RC miscount when a
  `string[][]` is extracted-from, then passed across a boundary to a function
  that re-extracts it. Fix is the flat representation (see below).** (The initial
  "nested-aggregate return" framing was refuted by a minimal repro, then a
  four-step bisect isolated the real trigger — both recorded below.) The plan
  above was implemented end-to-end and
  *works on the native (Go-built) driver*: the hoist (a `compute_wp_tables`
  bundling the 22 side-tables) + a **batched** `-per-module-emit-all -out-dir DIR
  -unit-range LO:HI` (batches of ~8 units per process, sharing the derivation,
  each a fresh process so the per-window emit's ~0.4 GB net working set — which
  is NOT reclaimed within one process, so all 35 units in one process OOM ~16 GB
  — is released on exit) emitted the whole compiler **byte-identically to the
  per-process path, ~2.1× faster** (238 s vs 560 s), no OOM. **But the
  self-host-BUILT compiler segfaults**: the merged path routes trivial programs
  through `emit_module_ir_gated → compute_wp_tables` (asm.fern:7345), and the
  self-host backend miscompiles the bundle. Isolated:
  - It is NOT the table values (the emitted output was byte-identical) — it is
    the **compiler's own codegen** of the bundle-carrying functions.
  - The bundle was carried first as a **24-field struct** and then as a
    **`string[][]`** — BOTH segfault the self-host-built compiler. `compute_wp_tables`
    is the ONLY function in the whole self-host source that returns a `string[][]`.
  - A read-after-consume UAF in `emit_module_ir_unit_wpt` (indexing the bundle
    after passing it to `emit_module_funcs`, which consumes it) was found and
    fixed; the segfault persisted regardless.

  **Minimal repro attempted — plain `string[][]` is NOT the cause (2026-07-26,
  correcting the first hypothesis).** A throwaway differential
  (`RUN_NESTED_AGG_REPRO`, `asm_run` self-host IR emit vs the interpreter oracle)
  exercised five `string[][]` shapes on the IR path: return a locally-built
  `string[][]` (`return t;`, not a literal), return-then-index, share the same
  `string[][]` across two reader calls, **extract elements into locals then let the
  container die**, and extract-in-caller-then-consume-the-container-then-use — the
  exact `emit_module_funcs` / `emit_module_ir_unit_wpt` patterns. **ALL FIVE PASS**
  (route "ir", exit-match the interpreter). So plain nested-aggregate
  return/extract/share/extract-then-free all lower correctly in isolation — the
  "self-host can't codegen `string[][]` return" hypothesis is **refuted**. The
  segfault is **contextual to the full self-compile**, not the `string[][]` shape.
  Leading remaining hypotheses (unverified):
  - **Whole-program-analysis shift.** Adding `compute_wp_tables` + the `wpt_*`
    accessors to the compiler changes `all_funcs`, so the ~22 side-tables (derived
    OVER `all_funcs`) reclassify some *other* function, exposing a latent codegen
    bug elsewhere — which would explain why no self-contained `string[][]` program
    reproduces it.
  - **Multi-boundary re-extraction of the same container.** In the compiler the
    one `wpt` is extracted 22× in `emit_module_ir_gated`, passed to
    `emit_module_ir_unit_wpt` (extracted 2×), passed again to `emit_module_funcs`
    (extracted 22×) — a depth the repro did not chain.

  **Bisect done (2026-07-26) — isolated to multi-boundary re-extraction; the
  analysis-shift hypothesis is cleared.** Four bisect steps against
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` (~5 min each), each toggling
  one piece of the refactor:
  1. `compute_wp_tables` added but **uncalled** → **PASS** (but vacuous: the
     self-host DCEs uncalled functions, so it was never emitted).
  2. A **live** `compute_wp_tables(all_funcs, all_structs)` call in
     `emit_module_ir_unit`, result used trivially → **PASS**. So its own
     codegen-when-called is fine, and the whole-program **analysis-shift**
     hypothesis is **refuted** (adding it + calling it changes nothing).
  3. `emit_module_ir_gated` derives its lowering tables by **extracting** them
     from a `wpt` bundle (`wpt[i]`) and feeding them to `lower_func` in the
     per-function loop → **PASS**. So single-site extract-then-use is fine.
  4. The only untested delta left is the **multi-boundary pass**: gated extracts
     `wpt`, then PASSES the same `wpt` onward to `emit_module_ir_unit_wpt` →
     `emit_module_funcs`, which **re-extract** it. The full refactor (which does
     this) segfaults; steps 1–3 (which don't) pass. **By elimination the trigger
     is passing an already-extracted-from `string[][]` across a call boundary to
     a function that re-extracts it** — a self-host RC miscount (double-dec /
     UAF) on the shared container the native memory model tolerates. (Confirming
     step — add JUST the onward-pass+re-extract — was not run; the elimination is
     strong but not yet a direct repro.)

  **So the fix is the flat representation, and it's now well-directed.** Extract
  the 24 columns **exactly once** and thread them as **individual `string[]`
  params** through `emit_module_funcs` — the shared `string[][]` never crosses a
  boundary and is never re-extracted, so the RC miscount can't arise.

  **Step 1 of the flat fix is IMPLEMENTED + VALIDATED (2026-07-26).**
  `compute_wp_bases` derives the 24 bases; `emit_module_funcs` now takes the 23 it
  needs as **individual `string[]` params** (applying the per-module `append_dyn`
  tails); `emit_module_ir_unit` / `module_runtime_needs` call `compute_wp_bases`,
  extract once, and pass individuals. `emit_module_ir_gated` is unchanged (still
  derives inline for its cache lowering). `TestSelfHostModloadPerModuleWholeCompilerX86_64`
  **PASSES** — the self-host-built compiler no longer segfaults, and the emit is
  byte-identical (same derivation + `append_dyn`, just relocated). This confirms
  the flat individual-params shape is the fix.
  - **Step 2 is DONE + VALIDATED (2026-07-26).** `emit_module_ir_unit_flat` takes
    the pre-computed bases (the public `emit_module_ir_unit` is now a thin wrapper
    that computes `compute_wp_bases` + delegates), and the batched
    `-per-module-emit-all` is re-added: it computes `compute_wp_bases` ONCE per
    process, extracts the bases into individual `string[]` locals, and passes them
    to every unit's `emit_module_ir_unit_flat`. `TestSelfHostPerModuleEmitAllX86_64`
    (env-gated `RUN_EMITALL_CHECK=1`, ~19 min) is GREEN: emit-all is
    **byte-identical to the per-process path across all 35 units**, **2.6× faster**
    (278 s vs 720 s), links into a working compiler, no OOM. The append_dyn
    COW-on-shared concern held — shared bases reuse correctly across the batch.

  **So slice 2's speedup is landed for gen0.** A CI-affordable **gen1** emit-all
  fixpoint was attempted and is **deferred (2026-07-27): the emit-all
  single-process batch limit is arena-structural, not leak-based.** A gen0
  emit-all → link → gen1 → gen1 emit-all fixpoint OOMs (exit 137) when gen1
  batches many units per process: the self-host bump arena's pointer only retreats
  on process exit, and the self-host runtime serves fewer cross-window allocations
  from the freelist than the fuller native runtime, so gen1 accumulates within a
  batch where gen0 (native runtime) does not. Recycling the remaining large
  collection-buffer free sites (above) did **not** move the OOM boundary (A/B
  byte-identical), confirming the accumulation is not those leaks. Making gen1
  emit-all CI-cheap would need arena checkpoint/reset between windows — deferred.
  **The env-gated serial `TestSelfHostPerModuleFixpointX86_64` remains the gen1
  self-reproduction proof** (the plan always allowed the env-gated route), and
  gen0's parallel per-module path (`TestSelfHostModloadPerModuleWholeCompilerX86_64`)
  is the fast CI guard. That is sufficient to proceed: repoint the
  bootstrap/fixpoint drivers off the merged bundle (slice 3) and delete the AST
  emitters (slice 5). Do NOT re-run the `string[][]` repro (proven to pass), the
  analysis-shift probe (refuted), or the emit-all-batch-size search (arena-bound).

  `internal/`-vs-self-host convergence item (#4451).

- **Slice 3 — replace the now-unreachable AST fallbacks. BLOCKED on self-host
  whole-program emit memory (2026-07-27 investigation; two direct fixes ruled
  out).** Grounding the call graph in code: `asm.emit_module` is a *dispatcher* —
  it calls `asm_ir.emit_module_ir_gated`, which returns IR asm when the whole
  module is eligible, else `""` → the AST emit loop. For a normal program every
  function is IR-eligible, so `emit_module_ir_gated` already returns IR and the
  **AST emitter body is never reached**. The *only* thing that still reaches it is
  the **whole-compiler self-compile**: the merged bundle is ~1000 functions and
  trips the `mod.funcs.len() > 512` gate (`asm_ir.fern:5785`), which bails to AST.
  That gate is the single remaining AST trigger, and its comment says it stands
  "**until the native large-tier freelist is fixed**" (#3425) — now done. So two
  direct unlocks were probed against `TestSelfHostModloadFixpointX86_64` (the
  merged 3-generation self-compile):
  1. **Lift the budget (cached merged IR).** Route the whole ~1000-func bundle
     through the existing cached IR emit. → **Stage 1 (native runtime) fits;
     stage 2 (self-host runtime, mmc) OOMs (exit 137).**
  2. **Stream the merged IR emit (no cache).** `emit_module_funcs` already
     lowers+emits one function at a time when handed an empty `cache`
     (`asm_ir.fern:5676`), so the eligibility pass discards each result and the
     emit re-lowers per function — the whole-program IR is never resident. →
     **Still OOMs stage 2 (exit 137), same wall.**

  So the peak is **not** the whole-program IR cache (streaming removed it and the
  OOM stayed). It is the self-host runtime's inability to hold a whole-compiler
  emit's *working set* — the ~22 whole-program side-tables + the ~470 MB output
  buffer + per-function lowering churn — within the 8 GiB arena. The **native**
  runtime holds it (stage 1 passes every time); the **self-host** runtime does not
  (stage 2 OOMs). This is the same asymmetry as the emit-all gen1 finding, and the
  same root: the self-host runtime reclaims less completely than the native one
  (per emit-all measurement a single 100-func window already peaks ~7.6 GB —
  emit memory scales far worse than the output size implies).

  **Therefore slice 3 cannot proceed by making the merged path route IR.** The
  whole-compiler emit must stay **windowed** (per-module), which is byte-fixpoint-
  stable and CI-affordable at gen0 (`TestSelfHostModloadPerModuleWholeCompilerX86_64`)
  but whose gen1 self-reproduction proof is env-gated/heavy. Retiring the AST
  emitter is gated on **making the self-host whole-program emit fit** — either a
  genuine reduction of the per-window emit peak (why 7.6 GB for 100 funcs? profile
  side-tables vs output vs lowering churn), or an arena checkpoint/reset so a
  single process can window without accumulating. That is the real slice-3/5
  prerequisite; the driver repoint + `asm.emit_module` → per-module-or-error swap
  is mechanical once it lands. (`asm_ir_run.fern:158` stays the differential
  oracle regardless; retiring it means trusting the IR path outright, which the
  fixpoint + differential suites must justify.)

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

1. **#3425 — DONE** (large-tier freelist port, x86 #5609 + arm64 #5614; the
   self-host collection-buffer siblings x86 #5651 + arm64 #5652; gen1 per-module
   fixpoint GREEN). The freelist port was a real unblocker but, as the slice-3
   investigation now shows, **not sufficient** to let the merged whole-compiler
   emit fit the self-host runtime.
2. **THE REAL BLOCKER (all of slice 3/5 gate on it): self-host whole-program
   emit memory.** Both direct ways to make the merged bundle route IR — lifting
   the 512-func budget, and streaming the emit with no cache — OOM the self-host
   runtime at stage 2 (see Slice 3). The whole-compiler emit only fits when
   **windowed** (per-module), and a single 100-func window already peaks ~7.6 GB.
   The next actionable step is a **profiling pass on the per-window emit peak**
   (side-tables vs the ~470 MB output buffer vs per-function lowering churn) to
   find what scales to 7.6 GB/100-func, then either shrink it or add an arena
   checkpoint/reset so one process can window without accumulating. Until that
   lands, the gen1 per-module fixpoint stays env-gated and the AST emitters stay.
3. **Slice 3 driver repoint + Slice 5 deletion** — mechanical once (2) lands.
4. **Slice 4a/4b** (arm64/wasm runtime untangle) alongside/after — a ~5k-line,
   qemu-gated duplication that unlocks nothing on its own; avoid until the
   endgame is reachable.

Do NOT re-probe the budget-lift or streaming merged-IR paths — both are recorded
above as OOMing the self-host runtime at stage 2; they are ruled out, not untried.
