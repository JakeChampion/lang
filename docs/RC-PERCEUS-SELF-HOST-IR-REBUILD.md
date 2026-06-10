# Self-host IR rebuild — native-shaped `[]Op` stack IR (Option A)

Status: **design + rollout tracker** (started 2026-06-09).
Decision: **Option A** from `RC-PERCEUS-SELF-HOST-IR.md` — rebuild the
self-hosted compiler around the same IR shape as native, rather than
continuing the AST→asm-direct port. This is the whole-backend
convergence path: the self-host gains native's lowering layer, so *all*
codegen (Perceus RC included) shares one IR and one set of analyses, and
the two compilers can be diffed.

This supersedes the recommendation in `RC-PERCEUS-SELF-HOST-IR.md` §4
(which favoured the narrower Option C). The feasibility analysis there
still stands and is worth reading first; this doc is the plan for the
path that was chosen.

---

## 1. Which native IR — and why the self-host is missing it

Native has **two** IRs, and they are not the same layer:

- **`internal/ir`** — a **stack-machine** IR (`ir.Op` / `[]Op`:
  push/pop operand stack, structured control flow via
  `block`/`loop`/`if`/`end`/`br`/`brif`). This is what the **production
  backends consume**: `arm64codegen.Emit(prog, info)` /
  `x86_64codegen.Emit` / `wasmbin` all call `ir.LowerWith` internally
  and emit from the resulting `[]Op`. **Perceus reference counting lives
  here** — alias-inc, the exit dec sweep, drop specialization, and reuse
  tokens are all emitted as `OpCallDirect` to `__fern_rc_*` helpers
  during AST→IR lowering.
- **`internal/ssa`** — an SSA optimiser IR with ~29 passes, **lifted
  from** `internal/ir` (`ssa.LiftFromIR`) and consumed only by the
  experimental `wasmssa` backend.

The self-host today:

- lowers **AST→asm directly** in each backend (`asm.fern`,
  `asm_arm64.fern`, `wasm.fern`) — there is no shared lowering target,
  so every RC emission is hand-written three times;
- *does* already have `ssa.fern` (3730 LOC) — but that mirrors the
  **higher** `internal/ssa` optimiser (i32-subset, with AST fallback),
  **not** the `internal/ir` stack IR.

So the layer Option A adds is precisely the one the self-host lacks and
the one Perceus needs: a port of **`internal/ir`** (the `[]Op` stack IR
+ the AST→IR lowering `builder`), with the three backends re-targeted to
consume `Op[]`. The existing `ssa.fern` can later be re-based to *lift
from* the new IR (mirroring `ssa.LiftFromIR`), unifying both IRs under
the native architecture.

```
            TODAY                          AFTER REBUILD
   AST ──> emit_module (asm.fern)   AST ──> ir.lower ──> Op[] ──> emit (asm.fern)
   AST ──> emit_module (arm64)              (Perceus here)  └────> emit (arm64)
   AST ──> emit_module (wasm)                               └────> emit (wasm)
                                                  (ssa.fern lifts from Op[], later)
```

## 2. The one up-front decision: builder style

Native's `builder` is a **mutable** struct that appends to a `[]Op`. The
self-host's established style (`EmitState`, `ssa.fern`) is **immutable,
threaded** values, which the byte-identical self-bootstrap fixpoint
depends on (no map-iteration-order or shared-mutable nondeterminism).

**Decision: mirror native's IR _data_ shape exactly, but build it the
self-host way.** The `Op` value and the `Op[]` stream are field-for-field
native (that is what makes the analyses and emission port mechanically —
§4). The *builder API* threads an `Op[]` (append returns a new
state/array) instead of mutating a struct. We keep native's data-shape
convergence — which is what the Perceus analyses and the backends key on
— without importing native's mutability or its determinism hazards. This
is the same trade `ssa.fern` already made against `internal/ssa` and it
is proven by the fixpoint.

`ir.fern` (slice 0, landed) already follows this: `Op` is a pure value
struct mirroring `ir.Op`'s fields (`kind`, `i32_imm`, `i64_imm`,
`f64_imm`, `width`, `unsigned`, `str`), kinds string-tagged like
`ssa.fern`'s `SInst`, one constructor per opcode.

## 3. Green-preserving migration (the hard part)

The backends currently consume the AST. We cannot half-convert one
without breaking it, and the **byte-identical self-bootstrap fixpoint
must stay green at every slice**. Strategy: **parallel path, one backend
at a time, behind a switch.**

1. Build `ir.fern` (the IR) + `irlower.fern` (AST→IR) as new modules,
   imported by nobody — additive, bootstrap-inert. (Slice 0 done.)
2. Add a *new* `emit_module_ir(ops)` alongside the existing AST
   `emit_module` in **one** backend (x86-64 first, per CLAUDE.md's
   local-gate guidance), behind a selector the run-harness sets.
3. Grow `irlower` + `emit_module_ir` feature-by-feature until the IR
   path reaches functional parity with the AST path on the self-host
   corpus *and* compiles the self-host's own source. Flip x86-64's
   default to the IR path; the fixpoint now runs through it (it defines
   its own byte-identical stage1==stage2 — output need not match the
   old AST path, only be deterministic and correct).
4. Delete x86-64's AST `emit_module`. Repeat for wasm, then arm64.
5. Once all three emit from `Op[]`, the AST→asm path is gone and the
   self-host matches native's architecture.

Each numbered step is itself sliced so the tree (incl. the fixpoint)
stays green between commits. The safe-leak invariant and the runtime
`__fern_rc_is_unique` net (from `RC-PERCEUS-SELF-HOST-PORT.md` §8) bound
the RC-specific risk: a mis-lowering leaks, never UAFs, until free is
turned on.

## 4. Why this makes Perceus near-mechanical

With the IR in place, the Perceus port from `RC-PERCEUS-SELF-HOST-PORT.md`
collapses to native's own structure:

- The **analyses** (`compute_precise_drops`, `compute_moved_locals`,
  `compute_reuse_sources`, `infer_param_escapes`, …) stay AST-level —
  they already are in native (see `RC-PERCEUS-SELF-HOST-IR.md` §1) — and
  produce the same name/node-keyed side-tables.
- The **emission** stops being triplicated: `irlower` inserts
  `op_call_direct("__fern_rc_inc", 1)` / the exit dec sweep / the
  generated `__drop_*` calls / reuse tokens **once**, into `Op[]`, and
  each backend's `emit_module_ir` instruction-selects `call_direct` (it
  already emits calls). This is exactly how native does it.

So the rebuild front-loads the lowering work but turns every later
Perceus slice (and every future optimisation) into a write-once change,
which is the entire reason for choosing Option A.

## 5. Slice plan

- **Slice 0 — IR data types. DONE.** `examples/self_host/ir.fern`: the
  `Op` value + constructors for the opcode spine (constants, locals,
  arithmetic/comparison, load/store, alloc, the call forms, structured
  control flow, drop, return) + `render_op`. Type-checks standalone
  (`fern -check`); imported by nobody, so bootstrap-inert.
- **Slice 1 — IR round-trip test.** A Go-side e2e
  (`internal/e2e/self_host_ir_test.go`) that builds a small `Op[]` via
  the constructors and asserts `render_op` output — pins the data shape
  and printer the way `ssa.fern` is pinned.
- **Slice 2 — `irlower.fern` skeleton.** AST→IR for the i32 spine
  (params, `var`/`=`, literals, binary/unary, `return`) — mirror the
  native `builder`'s expression/statement lowering, threaded-state style.
  Validate via an IR interpreter (port native's, or reuse the
  `ssa.fern` interpreter pattern) so lowering is checkable without a
  backend.
- **Slice 3 — `emit_module_ir` on x86-64.** Consume `Op[]`; reach
  parity with the AST path on the i32 corpus behind the harness switch.
- **Slices 4+** — widen lowering (strings, structs, enums, arrays,
  closures, control flow, calls) to full parity; flip x86-64 default;
  delete its AST emit; repeat for wasm + arm64.
- **Then** — fold the Perceus phases from
  `RC-PERCEUS-SELF-HOST-PORT.md` onto the IR (emission now write-once),
  and re-base `ssa.fern` to lift from `Op[]`.

## 6. Test strategy

Same nets as the existing self-host work, plus IR-specific ones:

- **Determinism / bootstrap.** The byte-identical fixpoint
  (`self_host_stage2_*_fixed_point_test.go`,
  `TestSelfHostBootstrapsItself`, `self_host_arm64_fixpoint_test.go`)
  is the master gate at every slice. The threaded-`Op[]` builder must
  walk deterministically (index-ordered, no map-iteration dependence).
- **IR unit.** `render_op` golden + `irlower` → IR-interpreter
  value-correctness on the i32 corpus (slices 1–2), before any backend
  consumes IR.
- **Differential.** Per backend, diff `emit_module_ir` behaviour
  against the surviving AST `emit_module` on the self-host corpus while
  both exist (slice 3+), so the switch-over is provably behaviour-
  preserving before the AST path is deleted.
- **Matrix.** Gate locally on x86-64 + wasm; let CI run arm64/qemu
  (CLAUDE.md). Whole `internal/e2e` with `-timeout 30m`.

## 7. Risks

- **Scope.** This is the largest single refactor on the self-host —
  native's `internal/ir` + lowering is thousands of LOC and the three
  emitters are large. Mitigation: the parallel-path migration (§3) keeps
  every slice green and independently revertable; no big-bang switch.
- **Two emitters in lockstep, again.** Re-targeting three backends
  re-opens the parity surface during migration. Mitigation: convert one
  backend fully before starting the next; the differential test gates
  each switch-over.
- **Builder determinism.** An IR reintroduces every nondeterminism
  hazard the fixpoint guards. Mitigation: threaded-immutable `Op[]`
  (§2), index-ordered walks, fixpoint green per slice.

---

## 8. Implementation log

- 2026-06-09: decision recorded (Option A). Design + rollout (this doc).
- 2026-06-09: **Slice 0 — IR data types — DONE.**
  `examples/self_host/ir.fern`: native-shaped `Op` value (mirrors
  `ir.Op`'s fields, pointer-free / threaded style), string-tagged kinds
  like `ssa.fern`, constructors for the opcode spine + `width_ptr()`
  sentinel + `render_op`. Type-checks standalone (`fern -check
  examples/self_host/ir.fern`, exit 0); imported by nobody, so the
  byte-identical self-bootstrap is unaffected. Next: slice 1 (IR
  round-trip Go-side test), then `irlower.fern` skeleton.
- 2026-06-10: **Slice 1 — `Op[]` round-trip test — DONE (#2590).**
  `ir_run.fern` + `TestSelfHostIRRoundTrip`: builds a representative
  `Op[]` (one per opcode family) and asserts the `render_op` golden
  through the self-host → native pipeline — proves `ir.fern` compiles,
  not just type-checks.
- 2026-06-10: **Slice 2 — AST→IR lowering + interpreter — DONE (#2591).**
  `irlower.fern`: `lower_func` (stack-machine emit for the straight-line
  i32 subset) + `eval_ops` (a stack interpreter validating lowering
  without a backend), `irlower_run.fern` driver,
  `TestSelfHostIRLowerRoundTrip`.
- 2026-06-10: **Slice 3 — first IR-consuming backend (x86-64) — DONE
  (#2592).** `ir_x86.fern` emits a freestanding x86-64 program from
  `Op[]` (operand stack on the machine stack, locals in an rbp frame).
  The self-host emits machine code from the IR, not the AST, for the
  first time. `TestSelfHostIRx86Run` runs real ELF binaries.
- 2026-06-10: **Slice 4 — if/else — DONE (#2593).** Structured control
  flow lowered to `if`/`else`/`end`; interpreter executes it
  (`find_matching_*`); `ir_x86` translates to `jz`/labels via a scope
  stack.
- 2026-06-10: **Slice 5 — while loops — DONE (#2594).** Canonical wasm
  `block`/`loop`/`br`/`br_if` shape; interpreter resolves branches via
  `enclosing_opener`; `ir_x86` scope stack generalised (per-scope kind +
  br-target label).
- 2026-06-10: **Slice 6 — calls + multi-function — DONE (#2595).**
  `call_direct` lowering; `lower_module` + `eval_call` (recursion-safe
  interpreter); `ir_x86` emits per-function `call`/`ret` with the SysV
  integer-register convention (no 16-byte alignment needed — integer-only
  callees). Recursion (fib/fact/mutual) runs as real x86-64.
- 2026-06-10: **Slice 7 — differential gate — DONE.** `TestSelfHostIRDiff`
  compiles a 25-program i32 corpus through BOTH the AST backend
  (`asm_run`) and the IR backend (`ir_x86_run`) and asserts identical
  exit codes — the rollout prerequisite (§6 Differential) for flipping
  x86-64's default. The IR backend is now proven behaviour-equivalent to
  the production AST backend on the i32 subset (params/locals,
  arithmetic, comparisons, bitwise/shift, unary, if/else, while,
  multi-function recursion). Next: widen lowering past i32 (strings /
  structs / arrays), or stand up `emit_module_ir` as a selectable path in
  the real `asm.fern` backend ahead of flipping the default.
- 2026-06-10: **Slice 8 — first heap type (i32 arrays) — DONE (#2597).**
  `ExprArray` / `ExprIndex` lower to a bump-allocated buffer; `eval_ops`
  gains a word-addressed heap; `ir_x86` emits `alloc`/`load`/`store` + a
  `.bss` heap. Differential corpus gains array programs.
- 2026-06-10: **Slice 9 — array `.len()` + index-assignment — DONE
  (#2598).** Length header `[len, e0, …]`; `.len()` is a header load;
  `arr[i] = v` (`__set_index`) stores in place; `drop` op added.
- 2026-06-10: **Slice 10 — first RC op (alias-inc) — DONE (#2599).** rc
  header `[rc, len, …]`; `var b = a` emits `call_direct __fern_rc_inc`;
  `__rc()` observation hook; `LowerState.local_is_arr` tracking. RC now
  lives in the IR, lowered once.
- 2026-06-10: **Slice 11 — exit dec-sweep + underflow detector — DONE
  (#2600).** `emit_dec_sweep` decs every array local at `return`
  (`__fern_rc_dec`); over-release detector + `__rc_underflow()`;
  `LowerResult.arr_slots` drives backend entry-zeroing. Completes Perceus
  Phase 1 (counting, no free).
- 2026-06-10: **Slice 12 — free path + block reuse (freelist) — DONE
  (#2601).** `__fern_rc_dec` frees to a size-class freelist at rc==0;
  `fn___fern_alloc` reuses an exact-size freed block before bumping.
  Reclamation arrives.
- 2026-06-10: **Slice 13 — move-on-return — DONE (#2602).** A returned
  array local is excluded from the exit dec-sweep
  (`emit_dec_sweep_except`) — moved to the caller, not freed. First
  pair-cancellation optimization; closes the array-return UAF.
  Cross-function arrays validated on x86 (shared heap) + the differential
  gate (the interpreter models a per-call heap).
- 2026-06-10: **Slice 14 — array params (borrow boundary) — DONE
  (#2603).** Array-typed params are array-tracked but BORROWED — the
  dec-sweep skips slots `< n_params`; the caller retains ownership. The
  `n_params` borrow boundary.
- 2026-06-10: **Slice 15 — peak-memory measurement — DONE.**
  `__heap_used()` (bytes bump-allocated; freelist reuse does not bump)
  makes the reclamation win measurable: three arrays each freed before the
  next reuse ONE block (20 B) vs three live arrays (60 B). Doc log brought
  current (slices 8–15).

  **State of the IR backend after slice 15:** a complete i32 language
  (params/locals, full arithmetic, if/else, while, multi-function
  recursion) + i32 arrays (literal/index/write/len) with **Perceus RC**:
  alias-inc, exit dec-sweep, free + freelist reuse, move-on-return,
  borrowed array params — all lowered once in `irlower` and instruction-
  selected by the backend, every step differentially matched against the
  production AST backend. Remaining (diminishing returns in this toy
  freestanding model; each unobservable until paired with the one below):
  precise-drops (mid-function last-use frees — needs `__heap_used`-style
  peak observation), borrow escape inference (only matters once
  precise-drops free escaped params mid-function), strings / structs as
  further heap types, and the eventual integration of `emit_module_ir`
  into the real `asm.fern` backend (the original §3 rollout) to flip
  x86-64's default and retire the AST emit path.

### Production integration phase (slices 16–19)

The §3 rollout, started. **Key isolation discovery:** importing `irlower`
into `asm.fern` directly ripples to the byte-identical bootstrap *and* the
~18 curated test file-lists *and* the ~50 `asm_run`-built harnesses (each
copies a fixed file set; a missing `ir.fern` is a hard error). So the IR
path lives in **separate modules** — `asm_ir.fern` (the IR emitter +
`emit_module_ir`) and `asm_ir_run.fern` (a `-ir` differential driver) —
imported by nobody else. `asm.fern` and `asm_run.fern` stay **byte-for-byte
unchanged**, so the fixpoint and every existing harness are untouched. The
gate is `internal/e2e/self_host_asm_ir_path_test.go`
(`TestSelfHostAsmIRPath`): each program compiled through asm.fern BOTH ways
(AST `emit_module` vs IR `emit_module_ir`), asserting identical exit codes.

- 2026-06-10: **Slice 16 — IR path into the production backend — DONE
  (#2605).** `asm_ir.emit_module_ir` lowers each function AST → stack IR →
  asm in asm.fern's OWN dialect (`__fn_<name>:`, rbp frame + `leave`/`ret`,
  machine-stack operands), reusing asmcore's `EmitState` + the `strbuf`
  builtins; the structured-CF → label bridge is the one new piece. Scope:
  pure i32 functions. The seam proven end-to-end.
- 2026-06-10: **Slice 17 — params + calls — DONE (#2606).** Param copy from
  the caller stack (`+16+i*8(%rbp)` → local slots) + `call_direct` via
  asm.fern's stack-arg ABI (args reversed since the IR pushes left-to-right
  but the ABI wants arg0 on top). Recursion (fib/fact/mutual) through the
  production IR path.
- 2026-06-10: **Slice 18 — arrays — DONE (#2607).** `alloc`/`load`/`store`/
  `drop` + a `call_direct` split: runtime helpers (`__fern_rc_*`,
  `is_fern_helper`) use the SysV ABI + the freestanding `fn___fern_*` bodies
  (ported verbatim from ir_x86), user calls keep the stack ABI. The
  freelist allocator + RC runtime + 1 MiB heap are emitted only when the
  module allocates. Within-function arrays.
- 2026-06-10: **Slice 19 — cross-function arrays — DONE (#2608).** Borrowed
  array params + array returns (move-on-return). Sound because the IR path
  only fires when the WHOLE module is eligible (`all_eligible`), so caller
  and callee share irlower's array layout — nothing crosses an IR/AST
  boundary. `emit_module_ir` threads the module's array-returning function
  names into `lower_func`.

  **State after slice 19:** the production IR path is **feature-complete for
  the i32 + arrays subset with full Perceus RC** — everything the standalone
  `ir_x86` backend does, now inside the production toolchain, every step
  differentially matched against asm.fern's AST emitter.

### Remaining: fold + flip (the bootstrap-touching steps)

To make the IR path the default, the isolation that protected the bootstrap
must finally give way — these steps are deliberately deferred to a careful,
dedicated pass (highest regression risk; the fixpoint is the master gate):

1. **Fold `asm_ir` → `asm.fern`.** Move `emit_module_ir`/`emit_function_via_ir`
   into `asm.fern` behind a `use_ir` flag (or `EmitState` field), so
   `asm.fern` imports `irlower`/`ir`. This ripples: add `ir.fern` +
   `irlower.fern` to every curated test file-list that copies `asm.fern`
   (and to the fixpoint's source-bundle), then re-establish the
   byte-identical fixpoint (stage1==stage2 with the new, deterministic
   compiler source). Risk is bounded — the IR analyses are deterministic
   (index-ordered, no map iteration) — but the file-list sweep is broad.
2. **Widen eligibility to remove the `all_eligible` gate.** For mixed
   modules (some functions IR, some AST), arrays must interoperate across
   the boundary — so reconcile irlower's array layout with asm.fern's
   (rc at `[data-8]`, its `__fern_arr_box`/`__fern_alloc`/`__fn___fern_rc_*`
   helpers) instead of the freestanding one. Until then, per-function
   fallback is only sound for heap-free functions.
3. **Flip the default + retire the AST emit** for the covered subset, one
   construct at a time, each gated by `TestSelfHostAsmIRPath`-style
   differential parity, until `emit_module` is IR-only and `emit_function`
   (AST) is deleted.

### Fold + flip — DONE (slices 20–24, x86-64)

The deferred bootstrap-touching steps, landed:

- **Slice 20 — fold `asm_ir` → `asm.fern` (default off) (#2610).** `asm.fern`
  imports `irlower`/`ir`/`asm_ir`; `emit_module` opens with
  `if (new_state().use_ir && asm_ir.all_eligible(mod)) return
  emit_module_ir(mod);`. The file-list ripple (every curated test bundle that
  copies `asm.fern` now also needs `ir.fern`/`irlower.fern`/`asm_ir.fern`) was
  done via the central `writeSelfHostAsmProject` helper + the `///MODULE`
  bundles. Fixpoint held (the compiler isn't `all_eligible`).
- **Flip attempts #2611/#2612 — surfaced two real bugs.** Broad exposure
  (every eligible program through the IR) caught what the curated differential
  corpus couldn't: (a) `emit_function_via_ir`'s threaded grow-and-overwrite
  `string[]` scope arrays mis-ran under the SELF-HOSTED compiler (undefined
  `.Lir_*` labels) — fixed by deriving control-flow labels purely from
  OP-STREAM INDICES (`lp<j>`); (b) `fn___fern_rc_inc` lacked the null/low-ptr
  guard. Both fixed in #2612; flip re-deferred pending RC-path drop-in parity.
- **Slice 21 — high-level array opcodes (#2613).** `arr_make` / `arr_get` /
  `arr_set` / `arr_len` / `arr_rc` replace the byte-layout arithmetic irlower
  baked into the op stream. The IR now carries array SEMANTICS, not a layout;
  each backend instruction-selects them. Behaviour-preserving (every backend +
  eval_ops kept their 4-byte layout) — the target-agnostic-IR discipline, and
  the prerequisite for reconciling the production layout with asm.fern's.
- **Slice 22 — IR array path uses asm.fern's 8-byte layout (#2614).** `asm_ir`
  lowers `arr_*` to asm.fern's `__fern_arr_box` rc-header box (data = base+16;
  cap@-16, rc@-8, element i @ data+(i+1)*8) and calls asm.fern's stack-ABI RC
  helpers (`__fn___fern_rc_inc` / `__fn___fern_arr_dec`); `emit_ir_runtime`
  emits asm.fern's exact array/RC/heap bodies; `emit_function_via_ir`
  zero-inits body-local slots (`rep stosq`). A true drop-in — a local flip
  took the RC suite from 8 failures to 1.
- **Slice 23 — IR-path Perceus reassignment + the FLIP (this slice).** The
  last gap: irlower's `StmtVar`/`StmtAssign` now emit the reassignment Perceus
  — retain-new (alias-inc) + cow-guarded release-old (`if (old != new)
  arr_dec(old)`) — via a shared `emit_arr_store` (pure existing-op
  composition, so every backend inherits it). The differential corpora
  (AsmIRPath + IRDiff) gained array-slot-reassignment cases (the gap that hid
  the bug). **`use_ir` now defaults ON for x86-64:** the self-host x86-64
  default emits from the stack IR with asm.fern's array layout + full Perceus
  RC for the i32 + arrays subset; out-of-subset modules (strings, structs,
  methods, maps, floats, closures) fall back to the AST emitter, so the
  compiler itself still compiles via AST and the byte-identical fixpoint
  holds. Only `asm.fern` reads `use_ir` — arm64 / wasm keep their AST paths
  (their IR emitters are a later slice).

**State after slice 23:** x86-64's self-host default is the IR path, with
Perceus RC at asm.fern parity, for the i32 + arrays subset. Toward the goal
(full IR for all backends + native-parity Perceus), what remains: widen IR
language coverage (strings → structs/enums → closures/maps/floats) so more
modules — eventually the compiler itself — qualify; port the remaining native
Perceus opts (precise drops, FBIP reuse tokens, escape/borrow inference, TRMC,
drop specialization); build the arm64 + wasm IR emitters and flip their
defaults; then retire the AST emit path.

## Perceus opts port — native-shaped, on the IR (governing principle)

**All Perceus lives on the IR — never in a backend.** This is how native is
structured, and it is the rule for porting the remaining opts. Native does
Perceus in two layers, both target-agnostic (`internal/ir`):

1. **RC insertion during AST→IR lowering** (`ir.LowerWith`): alias-inc, the
   exit dec-sweep, move-on-return / move-on-construction, and the drop calls
   are emitted as `OpCallDirect __fern_rc_*` as the AST is lowered — driven by
   analyses computed up front: `inferParamEscapes` (borrow inference,
   `ast.BorrowInferEnabled`), `findReturnsNoParamEscape` (escape),
   `findTrmcFuncs` (TRMC eligibility).
2. **IR→IR optimisation passes over the lowered `[]Op`**: the reuse / FBIP
   passes (`general_reuse`, `struct_reuse`, enum/tuple reuse — all over
   `*ir.Func`, gated by `ast.RcReuseEnabled`), drop specialisation,
   `insert_resource_drops`, `tco` / `trmc`. The backends only
   instruction-select the resulting opcodes.

The self-host mirror:

- **`irlower.fern` is the `LowerWith` analogue.** Layer-1 RC already lives
  there (alias-inc, exit dec-sweep, move-on-return, the reassignment
  retain/cow-release). Because these are emitted as IR ops, every backend
  inherited them for free — e.g. the reassignment Perceus (slice 23) needed
  zero backend asm. New layer-1 analyses (escape, borrow inference, TRMC
  eligibility) become functions over the AST / `Op[]` in `irlower`, computed
  once and consulted during lowering — the `inferParamEscapes` /
  `findTrmcFuncs` analogues.
- **Layer-2 opts become `Op[]`→`Op[]` passes** in `irlower` (or a sibling
  `iropt.fern`), exactly like native's post-lowering passes, so one
  implementation serves x86-64 (today) and arm64 / wasm (once their IR
  emitters land). **Never** a per-backend asm hack in `asm_ir` / `ir_x86` /
  `asm_arm64`.

**Ordering — opts ride with the coverage that exercises them.** On the current
i32 + arrays subset the layer-1 Perceus is already at native parity (alias-inc,
move-on-return, borrowed params, exit-sweep, reassignment retain/release). The
remaining opts need richer types / ops to bite, so they land alongside the
coverage that gives them a test (the engineering bar: every feature ships with
its test, no "coverage next PR"):

- **strings** (next slice): the next rc-tracked heap type, used everywhere in
  the compiler — brings string alias-inc / move / dec-sweep, reusing the array
  Perceus machinery. Critical path to "the compiler itself goes IR".
- **structs / enums / tuples**: unlock the reuse / FBIP passes (in-place
  reconstruction of a unique value) and drop specialisation (type-specific
  drop bodies vs. the generic helper) — the bulk of native's layer-2.
- **owned-by-value params**: make borrow inference (`inferParamEscapes`) bite —
  an owned param the analysis proves non-escaping is kept borrowed (no retain
  on use, no exit dec), matching native.
- **tail-position construction**: TRMC (tail-recursion-modulo-cons) — the
  Perceus-aware sibling of plain self-TCO, reusing the tail allocation.

Each opt: an IR-level analysis or `Op[]` pass in `irlower` / `iropt.fern`,
gated behind a flag like native's, proven by a differential case
(AsmIRPath / IRDiff) plus a focused unit case at the layer it touches.

## Multi-backend IR + all-backends flip — DONE (slices 24–30)

The §3 rollout, completed across all three backends. Each backend's IR emitter
lowers the SAME irlower `Op[]` stream — proving the IR is genuinely
target-agnostic — and is pure instruction selection: the eligibility, array
layout choice, RC helper-name map, control-flow label/depth scheme, and ALL of
Perceus (alias-inc, move-on-return, borrowed params, exit dec-sweep,
reassignment retain/cow-release) live ONCE in the shared layer (irlower +
asm_ir's pub analysis), so every coverage/opt slice from here lands on all
three backends at once.

- 2026-06-10: **arm64 IR emitter (asm_arm64_ir) (#2617).** Lowers the op stream
  to arm64 in asm_arm64's dialect (x29 frame, 16-byte operand rt-stack,
  identical 8-byte `__fern_arr_box` layout). Reuses asm_ir's pub analysis +
  asm_arm64's emit_runtime (no cycle: asm_arm64_ir does not import asm_arm64 in
  the isolated slice). qemu differential gate (AST≡IR exit codes).
- 2026-06-10: **arm64 fold + flip (#2618).** asm_arm64.emit_module dispatches
  on use_ir; asm_arm64_ir.emit_body emits function bodies, asm_arm64 appends
  its own runtime (the cycle-free "caller owns the runtime" pattern). arm64
  defaults to IR; byte-identical arm64 fixpoint holds (the compiler is not
  all_eligible → AST).
- 2026-06-10: **wasm IR emitter (wasm_ir) — pure-i32 (#2619), then arrays
  (#2620).** wasm is the most natural target: the IR is a stack machine with
  structured control flow and wasm IS that machine, so it transliterates to
  FLAT WAT (const_i32→i32.const, structured ops keep their names, `br depth`
  carries the SAME relative depth — no label generation; `call` needs no arg
  reversal). Arrays use wasm's linear-memory `__fern_arr_box` layout + RC,
  reused from wasm.fern. wasmtime differential gate.
- 2026-06-10: **watbin flat-WAT support (#2621).** The self-host's own
  WAT→binary encoder handled only the AST emitter's folded S-expressions; a
  linear enc_flat_body (dispatched per-function by body shape) lets it assemble
  the IR's flat WAT too — flat is the natural shape of the binary format
  (numeric locals/br straight to LEB, no name resolution). Also handles the
  abbreviated `(local type type …)` decl form. This unblocked the wasm flip.
- 2026-06-10: **wasm fold + flip (#2622).** wasm.emit_module dispatches on
  use_ir (preview1, non-component); wasm_ir.emit_functions emits bodies, the
  caller appends the runtime. div/rem lower to the non-trapping
  $__fern_idiv/$__fern_irem (matching wasm.fern's x/0=0 contract). The flip's
  WAT now assembles through BOTH wasmtime and watbin.

### 🎯 Milestone: all three backends emit via the IR by default

x86-64, arm64, and wasm all default to the stack-IR path with Perceus array RC
for the i32 + arrays subset. **The first half of the goal is met** ("the
self-host is fully IR for all its backends" — for the covered subset). What
remains is the second half ("a Perceus implementation as good as the native
Go compiler's") plus widening the subset toward the whole language.

## Remaining — toward full-language coverage + native-parity Perceus

Both tracks ride the SHARED irlower, so each slice lands on all three backends:

1. **Widen IR coverage** — strings (leak-only, pure coverage; the compiler uses
   them everywhere), then structs / enums / tuples (rc-tracked composites),
   then closures / maps / floats. As composite types land, the IR-eligibility
   gate (`all_eligible`) admits more modules — eventually the compiler itself.
2. **Port the remaining native Perceus opts as IR-level passes** (native does
   them on `internal/ir`; see "Perceus opts port" above) as the types that
   exercise them arrive:
   - **structs/enums** unlock construction-store retains, **drop
     specialization** (per-type `__drop_*` bodies vs. a generic helper), and
     the **reuse / FBIP** passes (in-place reconstruction of a unique value);
   - **owned-by-value params** make **borrow inference** (`inferParamEscapes`)
     bite — an owned param proven non-escaping is kept borrowed;
   - **tail-position construction** unlocks **TRMC**.
   Each opt: an IR analysis or `Op[]`→`Op[]` pass in irlower / a sibling iropt
   module, gated behind a flag like native's, proven by a differential case +
   a focused unit case — NEVER a per-backend asm hack.

### Coverage slice 1: strings — DONE (all three backends)

The first full-language coverage slice: string **literals**, `.len()`, **concat**
(`+`), and **equality** (`==` / `!=`), lowered through the shared `irlower` to all
three backends. Strings are leak-only (not reference-counted — asm.fern's AST path
doesn't sweep them either, fine for short-lived CLI / edge programs), so this slice
is *pure coverage*: it adds no Perceus machinery, only the type dispatch needed to
pick the string opcodes.

- **IR (`ir.fern`)** — `str_len` / `str_concat` / `str_eq` (layout-agnostic, like
  the array ops); `const_str` already existed.
- **`irlower.fern`** — a `local_is_str` dimension parallel to `local_is_arr`
  (string params/locals tracked for dispatch only) and `expr_is_str`, which routes
  `+`/`==`/`!=` on string operands to the string opcodes and `.len()` to `str_len`.
  Out-of-slice string forms **bail to the AST emitter** so they can't mis-dispatch:
  string **ordering** (`<` `>` `<=` `>=`, needs strcmp), string **indexing**
  (`s[i]`), string **arrays** (`string[]` literal / param / return), and
  string-**returning** functions (the call-site tracking is a follow-up).
- **Backends** — each selects its own string box: x86-64 / arm64 reuse asm.fern's
  16-byte `[data@0, len@8]` heap box + the `__fern_str_concat` / `__fern_str_eq`
  helpers (transcribed into asm_ir's IR runtime; emitted by asm_arm64's own
  `emit_runtime`); wasm uses the data-section `[len@0, bytes@4]` block (which
  shifts `fl_base` / `heap_base` off the empty-table defaults — `*_for(mod)`
  recompute them) and wasm.fern's `$__fern_strcat` / `$__fern_streq`, factored out
  of the larger `strcat_helpers` bundle into a narrow `strcat_streq_helpers` so the
  IR path doesn't pull in the split/predicate helpers' array dependencies.
- **Oracle note** — once strings are eligible, the wasm/x86/arm64 *differential*
  tests stop being independent oracles for string programs (the "AST baseline",
  `emit_module`, now also routes through the IR for an eligible module — both arms
  agree while possibly both wrong). The authoritative gates are the
  **absolute-value** suites: `TestSelfHostAsmRunX86_64` (x86) and
  `TestSelfHostWasmRun` (wasm), which assert exact exit codes; arm64 follows by
  mirror-symmetry with the absolutely-validated x86 path + CI's full matrix.
  Coverage: ~16 string cases per differential test + the absolute suites.

Next: **structs** (slice 2) — the first rc-tracked composite, which also brings
per-struct **drop specialization** to x86/arm64 (wasm already emits it).

### Coverage slice 2: structs (scalar-field, leak-only) — DONE (all three backends)

Struct **literal construction** + **field read** through the shared `irlower` to
all three backends, for **scalar-field (i32/boolean) structs**, leak-only. Two new
layout-agnostic ops: `struct_make <type, nfields>` (irlower reorders the literal's
fields into DECL order so the backend stores field i at its slot i) and
`struct_get <field_index>` (static field offset — no runtime shape dispatch, since
irlower tracks each local's struct type via `local_struct_type`).

**Why leak-only is exit-code-correct.** The absolute oracles assert exit codes, and
for a scalar-field struct the value is read before any free — so whether the box is
reclaimed doesn't change the result. This is what lets a single leak-only IR lowering
match all three backends' AST paths even though they diverge on struct RC (wasm emits
per-struct `__fern_release_*`, x86/arm64 don't). rc-tracked fields (array/struct/
string) + drop specialization — the actual Perceus-parity work — are the next slices.

- **`irlower`** — threads `mod.structs` (read-only) + a `local_struct_type`
  dimension (struct literals and struct params tracked for field-index lookup);
  `ExprStructLit` → `struct_make` (decl-order field reorder), bare `ExprFieldAccess`
  → `struct_get`. Bails to AST for anything outside the slice: `has_base` (struct
  update), non-scalar-field structs, struct **params** of a non-scalar struct,
  struct-**returning** functions, and field **mutation** (`p.x = v`, which desugars
  to an unrecognized `__set_field` call). `lower_func` / `lower_module` gained a
  `structs` parameter (threaded through all 11 call sites + the eligibility gates).
- **Backends** — x86/arm64 reuse asm.fern's `[shape_ptr, f0, f1, …]` 8-byte box
  (shape = the interned struct-name string; field i at `(i+1)*8`); wasm uses
  `[type_id@0, f0@4, …]` via `$__fern_str_box` (rc-headered; field i at `4 + i*4`).
  All three pull the box-allocating runtime in via the existing `module_uses_heap`
  / `module_allocates` gates (extended to recognize `struct_make`).
- **Validated** by the absolute oracles (`TestSelfHostAsmRunX86_64`,
  `TestSelfHostWasmRun`, exact exit codes — ~11 / 7 struct cases) + the three
  differentials; arm64 by mirror-symmetry + its differential under qemu. Fixpoint
  holds. No regressions (the same 8 pre-existing container-local wasmtime failures).

Next: **struct/enum fields that are themselves rc-tracked** (arrays / strings /
nested structs) — which is where construction-store retains + **drop
specialization** (per-type `__fern_release_*` on x86/arm64) finally bite.
