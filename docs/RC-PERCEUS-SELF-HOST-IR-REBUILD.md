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
  `__heap_bump_bytes()` (bytes bump-allocated; freelist reuse does not bump)
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
  precise-drops (mid-function last-use frees — needs `__heap_bump_bytes`-style
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
dedicated pass (highest regression risk; `internal/e2eselfhost` is the master
gate, with the fixpoint alongside it — the fixpoint is self-referential and
cannot see a miscompile that is stable across the compiler's own sources; see
[TEST-GATES.md](TEST-GATES.md)):

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

### Coverage slice 3: methods (scalar-struct receivers) — DONE (all three backends)

Receiver methods (`function (p: P) area(): i32 { … }`) and their call sites
(`p.area(args)`) through the shared `irlower` to all three backends, for
scalar-field struct receivers. The receiver is simply **arg 0**: it sits at the
first argument position (`+16(%rbp)` on x86, `[x29,#16]` on arm64, param 0 on
wasm), exactly like a normal first parameter, so the backend change was minimal —
copy `r.n_params` slots (which now counts the receiver) instead of
`fd.params.len()`, plus a **receiver-type-qualified label** (`__fn_<Type>.<name>` /
`$<Type>.<name>`).

- **`irlower`** — `lower_func` binds the receiver as slot 0 (struct-type tracked,
  `n_params` includes it); bails for non-scalar-struct / primitive / enum
  receivers. The call site `p.method(args)` is **statically dispatched** on the
  receiver's tracked struct type to `call_direct("<Type>.<method>", args+receiver)`
  — no runtime shape compare (the AST path's mechanism), since irlower knows the
  receiver type. (Methods that *return* a struct/string still bail via the
  return-type checks.)
- **Backends** — `emit_function_via_ir` / `emit_function_ir` emit the qualified
  label + per-function branch-label prefix (so two `get` methods on different
  types don't collide), and copy `r.n_params` arg slots. `ir_eligible` no longer
  rejects receiver functions; `module_has_func` recognizes the `"<Type>.<method>"`
  dispatch name so `calls_only_known` keeps method modules eligible.
- **Validated** — ~8 / 4 method cases on the absolute oracles
  (`TestSelfHostAsmRunX86_64`, `TestSelfHostWasmRun`) incl. args, methods on
  params, method→method (self) dispatch, and same-named methods on two types;
  plus the three differentials (arm64 under qemu). Existing self-host method tests
  now route through the IR and stay green. Fixpoint holds; no regressions.

With **structs + methods + scalar arithmetic + strings + arrays** all on the IR,
the eligible subset now covers a large slice of ordinary Fern.

### Coverage slice 5: enums + `match` — DONE (all three backends)

Enum-variant **construction** and `match`-statement **dispatch** through the
shared `irlower` to all three backends, for scalar-payload (and no-payload)
variants. This is the critical-path slice — the AST and every traversal are built
on enums + `match` — and it turned out to reuse almost everything:

- **Construction** is `struct_make`: a variant box is `[shape/type-id @ slot 0,
  payload as field 0]`, exactly the struct layout. `Circle(5)` (an `ExprCall`
  whose callee is a variant-struct name) → `struct_make("Circle", 1)`; a
  no-payload variant used as a value (`Red`, an `ExprIdent` that resolves to a
  0-field struct) → `struct_make(name, 0)`. **Payload read** is `struct_get(0)`.
- The one **new op** is `variant_is(name)`: pop the box, read its slot-0 tag,
  push 1 if it is the named variant. x86/arm64 compare the interned shape-name
  pointer (the same `.S` label `struct_make` stores — pointer equality);
  wasm compares the `struct_type_id`.
- The **`match` statement** lowers to a `block`/`br` chain (the same structured-
  control-flow ops as `while`): the scrutinee saved to a fresh slot, each variant
  arm its own `block` that `variant_is`-tests + `brif`s out on mismatch, binds the
  scalar payload (`struct_get 0`), runs its body, and `br`s out of the outer
  block; a trailing wildcard runs unconditionally. Built-in `Option`/`Result`/
  `bool` patterns (i32-tag discriminants), guards, multi-bindings, non-scalar
  payloads, and union members bail to the AST emitter.
- **Validated** by the existing `TestSelfHostEnumX86_64` / `BuiltinEnum` suites
  (which now route through the IR and stay green, incl. the string-payload case
  correctly bailing) plus new enum cases on both absolute oracles and the three
  differentials. Crucially, the **desugared** forms — `if let`, `let … else`,
  `switch` — all lower to `match` and now route through the IR too
  (`TestSelfHostIfLet`/`LetElse`/`Switch` green). Fixpoint holds; the full broad
  self-host x86 sweep (RC suites, pipeline/parser/SSA/VM/bundles, generics) is
  green. No regressions.

Next: field **mutation** / non-scalar payloads, then tuples / closures / maps /
floats — after which the rc-tracked-composite + drop-specialization Perceus work
lands on a much wider base.

### Coverage slice: `string[]`-returning functions — DONE (all three backends)

Functions whose return type is `string[]` (`function names(): string[] { … }`)
now lower through the IR; previously `lower_func` bailed the whole module to the
AST emitter on a `string[]` return. This composes the two pieces already in the
IR — array RC (the returned array is an ordinary rc-tracked box: move-on-return
like any array, via `arr_ret_fns`) and string-element leak-tracking (`string[]`
locals/params from the typed-string-array slice) — so it needs no new RC
machinery, only **call-site element typing**: a `var xs = names()` must know `xs`
is a `string[]` so `xs[i]` dispatches to `str_len` (not `arr_len`).

- **`irlower.fern`** — a `strarr_ret_fns` list (parallel to `str_ret_fns`),
  collected by `strarr_ret_fns_of` and threaded through `LowerState` /
  `lower_func`. `expr_is_strarr` gains an `ExprCall` arm: a call to a
  `strarr_ret_fns` member is a `string[]`, so `var xs = f()` marks `xs` as
  string-array and `f()[i]` / `xs[i]` element-type to a string. The array itself
  is still RC'd through `arr_ret_fns` (a `string[]` *is* an array type), so
  move-on-return and the borrow/move accounting are unchanged — the string
  **elements** leak, matching the leak-safe string-array model.
- **Backends** — none changed: `string[]` returns reuse the array box + RC
  runtime each backend already emits; only the lowering's op selection differs
  (`str_len` vs `arr_len` on the indexed element).
- **Validated** — five `strarr-ret*` cases on both absolute oracles
  (`TestSelfHostAsmRunX86_64`, `TestSelfHostWasmRun`: return-then-index,
  direct-index of the call, `.len()` + element `.len()`, return-a-param
  move-out, and a loop summing element lengths) plus the three differentials
  (arm64 under qemu). The IR path is confirmed taken (`all_eligible` true);
  fixpoint holds; the broad self-host x86 + Rc sweep is green. No regressions.

### Known issue: AST-wasm struct-field RC double-free (resolved by the IR migration)

The legacy **AST** wasm backend (`wasm.fern`) has a use-after-free in its
hand-written struct reference counting that surfaces on large struct-and-array-
heavy programs — notably the arm64-darwin assembler driver run under wasm
(`TestSelfHostArm64DarwinMachORealAsm`, `TestSelfHostArm64DarwinAssemblesRealRuntime`),
which trap with an out-of-bounds memory access (freelist corruption). Root cause:
`var p = p0` (aliasing a struct value) increments the **box** rc but not its
fields, while a spread `p = SomeStruct { ...p, field: … }` threaded in a loop
then drops the old box and **deep-releases a field the new box still
co-references**. The array's own rc (1, one box-field slot) doesn't reflect the
box-level sharing, so neither an rc-aware `arr_push` nor forcing copy-on-append
fixes it — it is a structural limitation of the box-only alias model. The
reference (Go) backend runs the same programs correctly, so this is purely an
AST-wasm-RC defect, not a language/logic bug.

It is deliberately **not** fixed in `wasm.fern`: that backend's per-construct RC
is being retired in favour of the shared IR's self-hosted Perceus, which tracks
ownership uniformly (the same machinery that already handles arrays/strings/
structs in the IR path). Once struct construction / spread / field access route
through the IR with Perceus, these programs get correct RC for free and the trap
disappears at the source. Until then the affected self-host **wasm** tests are
not gated in CI (they `t.Skip` without `wasmtime`, and the `^TestSelfHost` job
does not install it), so the failure is latent rather than blocking.

## Composite-struct Perceus RC on the IR — slice plan

This is the "actual Perceus-parity work" for structs whose fields are themselves
rc-tracked (arrays / strings / nested structs / enums). It mirrors native's
model in `internal/ir/ir.go`, which is **box-only-alias + per-field
retain-on-construction + recursive drop-on-unique**:

- **Alias** a struct value → inc the **box header only** (`__fern_rc_inc`); the
  fields are NOT re-incremented (`emitAliasInc` / `needsRcIncOnAlias`, ir.go
  ~15730/15747).
- **Construct / spread** → each pointer-shaped field value stored into the box
  is retained iff it is alias-shaped (Ident/FieldAccess/Index) and not moved;
  spread copies of un-overridden pointer fields are inc'd too
  (`*ast.StructLit` lowering, ir.go ~10123/10201/10240).
- **Drop on unique** → a generated `__drop_struct_<T>` checks
  `__fern_rc_is_unique`; on rc==1 it recursively drops each rc-tracked field
  then `__fern_box_free`s the box; on rc>1 it just `__fern_rc_dec`s
  (`genStructDropFn`, ir.go ~6645; recursive dispatch `appendChildDrop` ~6492).
- **Exit sweep / move-on-return** → owned struct locals are dropped at last use
  / scope exit, except a struct that is moved on return
  (`computeMovedLocals` ~4541, `emitRcDecLocalsAtExitExcept` ~4786).

This is exactly the machinery whose ABSENCE makes the AST-wasm backend's
box-only model double-free (see the "Known issue" note): native is safe because
the drop only deep-releases fields when the box is uniquely owned (rc==1), and
because construction/spread retains keep each co-owned field's count correct.

### Prerequisite: rc-headered struct boxes on x86/arm64

The self-host IR struct box differs per backend today:

- **wasm** — already rc-headered (allocated via `__fern_str_box`, which carries
  the 8-byte rc+bsz header); `[type_id@0, field@4+i*4]`. Drops are *possible*
  here now.
- **x86/arm64** — `[shape_ptr@0, f_i@(i+1)*8]` via **`__fern_alloc`** (no rc
  header) — leak-only; cannot be `rc_dec`'d or `box_free`'d. (`asm_ir.fern`
  `struct_make` ~562; arm64 mirror.)

So composite-struct RC cannot begin on x86/arm64 until struct boxes carry an rc
word. That is slice 1.

### Slices

1. **rc-header x86/arm64 IR struct boxes.** Allocate `struct_make` boxes via an
   rc-boxing helper (mirroring `__fern_arr_box`: rc word at `[p-8]`, payload
   pointer returned), keeping `struct_get`/`struct_set` field offsets unchanged
   relative to the returned pointer. Behaviour stays leak-only (no drops yet) —
   so the absolute oracles keep their exit codes and fixpoint holds; this is
   pure foundation. Also add the `__fern_rc_is_unique` / `__fern_box_free`
   helpers to the x86/arm64 IR runtimes if absent (wasm already has them).
2. **Drop specialization + exit sweep for i32[]-field structs.** Generate a
   per-type struct drop (inline at the sweep site, or a `__drop_struct_<T>` fn)
   that, on `rc_is_unique`, releases each array field then frees the box; wire
   it into the IR exit sweep (`emit_dec_sweep_except` gains a struct arm).
   Construction retain for array fields. Remove the leak-only
   `is_scalar_arr_field_access` bail (#2630) and the `decl_is_leaksafe` gate for
   this case — aliasing/returning an array-field struct now refcounts correctly.
   Validate with the existing RC oracles (`TestSelfHostRc*`) plus new
   array-field-struct lifetime cases (alias, return-move, reassign).
3. **string / string[] / nested-struct fields.** Extend the recursive field
   drop + construction retain to string fields (leak-safe: inc/dec but never
   freed — or skip per the leak model), string[] fields (array of leaking
   strings), and nested struct fields (recurse through their drop).
4. **enum payloads + spread/update.** Variant-payload drops (dispatch on tag),
   and the `S { ...base, f: v }` copy-inc / override-retain rules end-to-end.
5. **move-on-return + precise drops for structs.** Exclude a struct moved on
   return from the sweep; place drops at last use where the analysis allows.

Each slice ships on all three backends with absolute-oracle + differential +
fixpoint coverage, same as the coverage slices. After slice 5 the AST-wasm
struct-RC "Known issue" is moot: those programs route through the IR with
correct Perceus and the double-free is gone at the source.

## Phase A: full IR (native parity), then Phase B: direct Perceus port

Decision (refined): **complete the self-host IR to full feature parity with
native's `internal/ir` FIRST — in leak-mode — then do a single direct port of
native's Perceus**, rather than interleaving RC into a partial IR. Rationale:
interleaving "widen coverage" with "get RC right" per-construct is what produced
the bail-heavy heuristics and the RC bugs (the AST-wasm double-free; the struct
field-release over-release). Separating them makes each clean: coverage is a
values-only problem under the safe-leak invariant (a mis-lowering leaks, never
UAFs, while free is OFF), and Perceus then ports once onto a stable, total IR —
correct-by-construction and diffable against native (the Option A goal).

**Definition of done for Phase A:** the self-host compiler compiles ITSELF
end-to-end through the IR with zero AST fallback — i.e. `all_eligible` holds for
every module in the compiler's own source, and the byte-identical self-bootstrap
**fixpoint runs through the IR path**. The AST emitters (`asm.fern`/`asm_arm64`/
`wasm.fern` `emit_module`) are then deletable.

**Driver metric:** percentage of the self-host compiler's OWN functions that are
`ir_eligible`. Attack the bails that block the most functions first. (A tiny
harness that runs `irlower.lower_func` over every function in the self-host
sources and counts `ok` makes this measurable per slice.)

### Backlog (native parity checklist − current self-host coverage)

Already covered: i32 arithmetic/compare/control-flow; arrays (i32 + string
elements) with leak-mode RC; strings (literal/concat/eq/len); leak-only structs
(scalar / scalar-array / string fields) + scalar-struct methods; enums + `match`
(scalar payloads); tuples (scalar elements); `string[]`.

Gaps, ordered by how much of the compiler's own source they unblock:

1. **String indexing / slicing / ordering** (`s[i]`, `s[i:j]`, `s < t`). The
   lexer and many parser/util functions index strings constantly — this is the
   single biggest unblocker. (Native: `__str_idx` / `__str_slice` / `__str_cmp`,
   ir.go ~9598/9761/11497.)
2. **`Option` / `Result` + full `match`**: built-in `Some`/`None`/`Ok`/`Err`
   patterns, guards, multi-binding, and the desugared `if let` / `let … else` /
   `?`. Used pervasively across the compiler. (ir.go ~8043-8236, 9469-9597.)
3. **Composite struct / enum fields**: struct/enum/tuple/array-of-struct fields,
   struct-valued params + returns. The AST node types ARE composite structs/
   enums, so the compiler can't self-compile without these. Leak-mode = store
   the pointer, no drop. (ir.go ~10123, structFieldLayout.)
4. **Tuples with non-scalar elements** + tuple returns (`(i32, string)` etc.).
5. **Maps**: `map_new` + set/get/get_or/has/delete/keys/values/len/iter, i32/
   string/wide K-V. Symbol tables / interners use these. (ir.go ~10005, 12247.)
6. **i64** (and sub-i32 widths / unsigned where used): distinct wasm stack type —
   needs the IR value-type tagging the backends already have a `WidthPtr`
   precedent for. (ir.go width/extend/wrap ops.)
7. **f64 / floats**: the `is_float` bail. Distinct wasm stack type like i64;
   lower priority (the compiler uses few floats, but parity requires it).
8. **Closures / function values**: lambdas, captures, indirect calls
   (`OpMakeClosure`/`OpMakeEnv`/`OpCallIndirect`). Used where the compiler passes
   callbacks. (ir.go ~10281, 11768.)
9. **Builtins the compiler emits**: `print`/`write`/`eprint`, `*_to_string`,
   `string_from_bytes`, `args`, file IO — lowered as direct calls to the
   existing runtime symbols.
10. **for-in** over arrays/maps (desugars to a 3-part loop; confirm the desugar
    reaches the IR rather than bailing).
11. **Optimizations (defer to after parity / Phase B)**: FBIP reuse, TRMC,
    pair-form `Option[i32]` returns — correctness-neutral, so not on the Phase-A
    critical path.

Each gap ships as a slice across `irlower` + the three backends, gated by the
absolute oracles + differentials + fixpoint, in **leak-mode** (no new frees).
When the metric hits 100% and fixpoint runs on the IR, Phase A is done and
Phase B (the direct Perceus port — analyses + emission + drop specialization,
free turned on) begins on a complete, stable IR.

---

## Phase A status + Phase B kickoff (2026-06-14)

### Phase A coverage — effectively complete for the common surface

A broad eligibility survey (run `irlower.lower_func` via the `asm_pathprobe_run`
driver over a corpus of native-valid programs) now routes through the IR path
("ir") for essentially every native-valid construct probed: i32/i64/f64
arithmetic + control flow, strings (+ split/chars/lines/case/trim/reverse/
replace/predicates), arrays (i32/string/f64/i64, of-arrays, of-structs,
of-tuples), structs (+ nested, update, methods, struct-returning), enums +
`match` (payloads, qualified-variant locals, fresh-variant + enum-receiver
method dispatch), `Option`/`Result` + the **try-operator `?`**, tuples,
module-level `const`, closures (capture-by-value, capturing array/struct
literals, **nested** capturing lambdas, fn-value args incl. at method sites,
no-capture lambdas in **array + struct-field** positions), maps, and for-in.
Recent slices: #2940/#2942 (chars/lines), #2954/#2961 (const), #2990/#2991 +
#2997 (enum-receiver methods, incl. qualified variants), #3035 (nested
closures), #3036/#3041 (lambdas in array/struct aggregates), plus the
try-operator and capture-literal slices.

Remaining Phase-A gap found: **first-class CAPTURING closures stored in
collections** (a capturing lambda *literal* in an array/struct, or a closure
local that escapes into one) — these still bail to AST. The closure-box-in-array
runtime + call-through-element path already exists (`[mk(1), mk(2)]` of factory
closures routes IR), so the gap is narrowly "lower a capturing-lambda literal to
a uniquely-named closure box at an arbitrary value position" — a generalization
of the current one-`<cur_fn>$clo`-per-function escaping model.

### Current IR Perceus state (the foundation Phase B builds on)

Layer-1 RC is live on the IR (`irlower.fern`), **free ON**, at native parity for
the covered subset:
- alias-inc (`needs_rc_inc_on_alias` over ident/field/index reads of rc-typed
  values); reassignment retain + COW-aware release (skip the dec when the new
  value == the old, the in-place-mutator case);
- function-exit dec-sweep (`emit_dec_sweep_except`) over owned array locals AND
  reclaimable (fresh, non-escaping) struct locals, with one level of struct
  array-field deep-drop (`emit_struct_field_drops`, leaf-safe);
- move-on-return (the `keep` slot elides the balanced inc/dec pair on a returned
  bare owned local); `n_params` borrow boundary (params conservatively borrowed,
  never swept); construction-store retains (struct/array/tuple/closure/enum
  payload); freelist reuse (`__fern_alloc` size-class pop) so rc==0 reclaims.

### Phase B — remaining native Perceus opts to port (issue #3003)

All target-independent, as IR-level analyses/passes in `irlower` (never a backend
hack), each gated behind a flag like native's, validated by the byte-identical
fixpoint + `TestSelfHostStdTestE2E` (the over-release/UAF nets) at every slice:

1. **Precise drops / drop-on-last-use** ← `computePreciseDrops` (ir.go ~3294).
   Release a value right after its last use instead of at function exit, bounding
   the live set. Depends on `freeEligible`/`movedLocals`/`localNameUnique`/
   `preciseDroppableType`/`initMayAliasLive`/`flowsIntoUncountedAlias` guards.
2. **Inter-procedural borrow inference** ← `inferParamEscapes` (ir.go ~1336).
   The self-host borrows *all* params today; native proves which owned params can
   stay borrowed (greatest-fixpoint call-graph escape analysis).
3. **Reuse / FBIP** ← `computeReuseSources` (ir.go ~12119) — in-place
   reconstruction of a unique value (dead owned box ↔ same-class construction).
4. **Drop specialization** — per-type `__drop_*` bodies vs. the generic helper
   (partially present for struct array fields).
5. **TRMC** ← `findTrmcFuncs` (`trmc.go`) — tail-recursion-modulo-cons.

**First slice — conservative precise-drops for arrays.** Fire only for a
provably *linear, owned* array local: declared once, never reassigned anywhere
(matches native's reassigned-walk bail); init is a fresh owned array (literal /
scalar-arg array-returning call — never a slice/index/field/ident alias, the
`initMayAliasLive` guard); never aliased into another slot, stored in a
container, captured, or returned (no uncounted reference outlives the drop); a
straight-line top-level last-use statement (control-flow-aware placement deferred
to a later increment, as native staged it). Emit `__fern_rc_dec` right after the
last-use statement and exclude the slot from the exit sweep — net dec count
unchanged, just earlier. Anything uncertain degrades to today's exit-sweep
(sound, the safe-leak invariant). Land behind an off-by-default flag (zero
regression risk; full suite green with it off), then flip on in a separately
fixpoint-/std-test-validated follow-up — native's staged `RcFreeEnabled` pattern.

---

## Next slice (ready to execute): borrow-inference-enabled reclamation (2026-06-14)

Two precise-drop slices have landed on the IR (#3054 i32[] literals, #3079
i32[]-returning builder calls). The **most impactful** next Perceus step is to
reclaim arrays passed to read-only helpers — today a `var t = [..]; helper(t);`
local is NOT precise-dropped because the escape walker conservatively treats ANY
call argument as an escape (the callee might retain it). The fix is a **two-level
borrowability** model that reuses the precise-drop emission entirely (no new
runtime, no per-backend asm):

- **Level 1 — param borrowability (intra-procedural, conservative).** A function
  param is *borrowable* iff it is only borrow-read in the body and never
  returned / stored in a container / passed to any call / captured — i.e.
  `!body_unsafe_for(fn.body, paramName, /*empty borrowable registry*/)`. This is
  exactly the strict escape walker already written for precise drops. Encode as a
  `string[]` registry `"<fn>|<flags>"` (flag i = '1' iff param i borrowable),
  computed once via `borrowable_params_of(funcs)` (sibling of `fn_param_sigs_of`).
  Conservative: a param passed to ANY call is NOT borrowable (we don't yet do the
  inter-procedural `inferParamEscapes` fixpoint — native ir.go:2554), so it's a
  safe under-approximation.

- **Level 2 — reclaim.** In the precise-drop escape walker (`expr_unsafe_for`'s
  FREE-call case), a DIRECT bare-ident arg `name` at a borrowable param position
  is a BORROW (safe), not an escape. So `var t = [literal]; f(t); g(t); …` (f, g
  borrowable) makes `t` a precise-drop candidate, released after its last use via
  the existing dec+zero emission. Method calls stay escapes (a method like
  `.slice()` can return a view). Soundness: a borrowable callee provably doesn't
  retain the arg, so after the call the sole-owner local is dead → free is sound.

**Why it's a focused-session task, not a quick edit:** it threads a new
`borrowable_params` registry through `lower_func` (≈25 call sites across
`irlower` / `asm_ir` / `asm_arm64_ir` / `wasm_ir`) and the recursive
`expr_unsafe_for` / `stmt_unsafe_for` / `body_unsafe_for` chain (which is also
reused at Level 1 with an EMPTY registry to avoid circularity). Mechanical but
broad; a single threading error breaks the build, and a mis-judged borrowable
param is a UAF that only the fixpoint + std-test + `asm_run` gates catch — so it
must land as one careful, fully-gated slice (gate set: TestSelfHostAsmRunX86_64 +
byte-identical fixpoint/stage-2 + TestSelfHostStdTestE2E + the RC suites,
detector 0).

After this: the inter-procedural `inferParamEscapes` fixpoint (lets a param
passed to a borrowable callee stay borrowable, widening Level 1), then the full
owned-by-value param model + move-on-call (frees owned/escaping params and
call-arg temporaries — the largest remaining leak), then reuse/FBIP, drop
specialisation, TRMC.

### Landed (2026-06-14)

Implemented exactly as specced. `borrowable_params_of(funcs)` (sibling of
`fn_param_sigs_of`) emits the `"callee|flags"` registry; `param_is_borrowable`
mirrors `callee_param_is_fn`'s lookup; the `borrowable` registry threads through
`expr_unsafe_for` / `stmt_unsafe_for` / `body_unsafe_for` → `precise_drop_names`
→ `lower_func` (new last param, all ~23 call sites across the four IR files).
Level 1 computes borrowability with an EMPTY registry (every call arg escapes);
Level 2 admits a direct bare-ident arg at a borrowable free-function param as a
borrow. Firing proof: the identical `var t=[..]; sum_arr(t); sum_arr(t)` program
emits one MORE array dec than before (3 → 4), and the over-release detector reads
0. Gates green: `TestSelfHostAsmRunX86_64` (auto-routes to IR), byte-identical
fixpoint + stage-2, `TestSelfHostStdTestE2E`, the RC suites, and a new
`TestSelfHostRcPreciseDropX86IR` borrow/escape/transitive case set. Method-call
args and any aliasing (`var u = t`) stay escapes — conservative and sound. Next:
the inter-procedural `inferParamEscapes` fixpoint to widen Level 1.

### Next slice (validated, blocked on a compute-once hoist): inter-procedural fixpoint (2026-06-14)

Prototyped and **proven correct** but **not landed** — it hit a compile-time cost
wall that needs a small refactor first. Recording the full state so the next
session lands it directly.

**The change (validated):** turn `borrowable_params_of` into a GREATEST fixpoint
(native `inferParamEscapes`, ir.go:2554): seed every param `'1'` (optimistically
borrowable), then re-run the escape walker against the CURRENT registry —
`!body_unsafe_for(fn.body, p, reg)` — and drop borrowability wherever an escape
is found, iterating until stable. Monotone-decreasing from all-`'1'` ⇒
terminates; sound because a DIRECT escape (return / store / slice / alias /
pass-to-non-borrowable) is caught regardless of the optimistic seed. A
`borrowable_key(fn)` helper builds the `"name"` / `"<Type>.<method>"` key; every
function with ≥1 param stays in the registry (even all-`'0'`) so callee look-ups
during iteration stay accurate; change-detection is a positional diff of `reg`
vs `newreg` (both share key order). Verified on the driver: the transitive
`outer(v) → inner(v)` (inner borrowable ⇒ outer borrowable) now reclaims `t`
(dec 3 → 4), value correct, detector 0; the escape chain `wrap(v) → idf(v)` where
`idf` returns its param is correctly still rejected (detector 0). The targeted
`TestSelfHostRcPreciseDropX86IR` (with added transitive-reclaim + escape-chain
cases) passed.

**Why it's blocked:** `borrowable_params_of(mod.funcs)` is an INLINE argument at
every `lower_func` call site, and those sit INSIDE the per-function lowering loops
— so it is recomputed once per function lowered. A single-pass registry tolerates
that (it's how every other `*_ret_fns_of` helper is used), but the fixpoint's
extra iterations × per-call recomputation, in the slow self-host runtime, pushed
mmc-compiles-mmc over its budget: `TestSelfHostLoadFixpointX86_64` stage-1 and
both `TestSelfHostStage2FixedPoint{,Arm64}` were SIGKILLed (exit 137). The x86
`AsmRunX86_64` + `StdTestE2E` + RC suites + the targeted IR test all PASSED; only
the self-compile-of-the-whole-compiler tests tipped over.

**The fix (the actual next task):** compute the borrowable registry **once per
module compile** and reuse it, instead of recomputing per function. Hoist
`var bparams = irlower.borrowable_params_of(<funcs>);` out of each per-function
lowering loop (≈12 loops across `asm_ir` / `asm_arm64_ir` / `wasm_ir` /
`irlower`) and pass `bparams` into the loop's `lower_func` calls. That drops the
fixpoint from O(funcs) recomputations to O(passes) (~once per full module walk),
making it effectively free — at which point the validated fixpoint logic lands as
a single slice. Gate it on the SAME set, with the stage-1/stage-2 self-compile
tests as the specific must-pass (they are what caught the cost regression).

### Update (2026-06-14): three cost attempts confirm the hoist is mandatory

Tried to land inter-procedural widening as a cheap per-function change, three ways
— ALL blocked by the self-compile budget, even though each was verified
functionally correct on the IR driver (transitive `outer → inner` reclaims, value
+ detector sound; the escape chain `wrap → idf` stays rejected):

1. **All-`'1'` greatest fixpoint** (iterate removing escapers to convergence):
   SIGKILLed `TestSelfHostLoadFixpointX86_64` stage-1 + both
   `TestSelfHostStage2FixedPoint{,Arm64}` (exit 137).
2. **Bounded all-`'0'` least fixpoint, cap=2, stripped registry** (one extra
   propagation round): STILL SIGKILLed x86 stage-1/stage-2 — proving the cost is
   the per-function recomputation × any extra round, not the round count.
3. **Single forward pass with a growing stripped registry** (no iteration; sees
   only earlier-defined callees): PASSED the full x86 gate
   (`TestSelfHostAsmRunX86_64` auto-routes to IR, x86 fixpoint + stage-2,
   `TestSelfHostStdTestE2E`, RC suites, targeted IR test) — but SIGKILLed
   `TestSelfHostStage2FixedPointArm64` **even run in isolation with 14 GB free**.
   The arm64 self-source is larger (adds `asm_arm64*.fern`), and admitting more
   borrowable params also makes `precise_drop_names`' per-candidate
   `collect_idents_stmt` walk allocate more — the native-x86 `mmc1` compiling that
   larger source OOMed.

Conclusion: the merged conservative slice (#3087) is the most that fits as a
per-function computation. ANY inter-procedural widening — even a single pass — has
to get cheaper first. **Before committing to the compute-once hoist, the next
session should PROFILE which cost dominates the arm64 `mmc1` self-compile OOM:**

- If it is `borrowable_params_of`'s per-function recomputation, the hoist (compute
  the registry once per module compile, thread `bparams` into the ≈12 per-function
  lowering loops across `asm_ir` / `asm_arm64_ir` / `wasm_ir` / `irlower` detailed
  above) fixes it, and the single-pass / full-convergence logic drops in for free.
- If it is instead `precise_drop_names` allocating more (its per-candidate
  `collect_idents_stmt` walk scales with the number of admitted candidates, which
  inter-procedural widening increases regardless of how borrowability is computed),
  then the hoist alone will NOT fix the OOM — `precise_drop_names` itself needs to
  be made cheaper (e.g. compute the last-use map once per function instead of an
  O(candidates × statements) re-walk) before the widening can land.

Reverted all three attempts; the tree stays at the merged conservative version,
which is green. The validated fixpoint/single-pass logic is recorded here so
whichever cost fix is needed, the widening itself is a drop-in.

### Profiled (2026-06-14): the OOM is the per-function recomputation, not `precise_drop_names`

Ran the decisive experiment the previous note called for: applied the single-pass
growing-registry `borrowable_params_of` but made it `return []` (discard the
result), so the computation runs in full while DOWNSTREAM does *less* work than the
merged conservative version (an empty registry admits no borrow-based precise
drops at all). `TestSelfHostStage2FixedPointArm64` still SIGKILLed (exit 137, in
isolation, 14 GB free). **Conclusion: the cost is the computation itself (option A),
not `precise_drop_names` (option B) — so a `precise_drop_names` last-use-map
rewrite would NOT help.** Mechanism: the growing registry makes the escape walker
short-circuit less (a param forwarded to a borrowable callee is no longer an
immediate escape, so the walk continues instead of returning early), so each
`body_unsafe_for` walks more of the body and allocates more in the recursive walk
— and `borrowable_params_of` is recomputed once per lowered function, across ~15
`module_uses_*` / `str_values` inspection passes PLUS the emit pass, each looping
over every function. On the larger arm64 self-source that product OOMs.

**So the fix is unambiguously to move `borrowable_params_of` off the per-function
path — compute it ONCE per module compile and reuse it.** The cleanest mechanism:
the inspection passes (`module_uses_*`, `str_values`, `fn_value_table`, `ir_eligible`,
`calls_only_known`, …) do not need precise-drop accuracy — the only op a precise
drop adds is `__fern_arr_dec`, which the function-exit dec-sweep already emits for
every array-using function, so no `module_*`/`module_emits_op` property check can
flip — so they can pass an EMPTY registry to `lower_func` (cheap, conservative).
Only the real emit path (`emit_function_via_ir` in `asm_ir`/`asm_arm64_ir`,
`emit_function_ir` in `wasm_ir`, driven from the per-module emit loops at
`asm_ir.fern:2651` / `asm_arm64_ir.fern:793` / `wasm_ir.fern:1218`) needs the full
registry — computed once before that loop (or stored on `EmitState`, which costs a
field added to every `EmitState` constructor in `asmcore.fern`) and threaded into
`lower_func`. With the computation off the per-function path, the single-pass (or a
full fixpoint) drops in for free. Gate on the arm64 stage-2 self-compile, which is
the specific test that catches this — it passes on x86 but not arm64 because the
arm64 self-source is larger.

### Landed (2026-06-14): inter-procedural widening via a per-module emit-path registry

Executed the plan above. Added `borrowable_params_interproc(funcs)` (the single
forward pass with a growing registry + `borrowable_key`) alongside the unchanged
conservative `borrowable_params_of`. The ~15 inspection passes keep calling the
cheap conservative `borrowable_params_of` inline (no edits, no recomputation cost
change, and — as argued — no property-check flips). Only the three emit drivers
compute `borrowable_params_interproc(mod.funcs)` ONCE before their per-function
loop and thread it through `emit_function_via_ir` (`asm_ir` / `asm_arm64_ir`, new
last param) / `emit_function_ir` (`wasm_ir`, new last param) into `lower_func`.
`lower_module` (debug-only driver) keeps the conservative registry.

Result: the arm64 stage-2 self-compile passes (201 s, was exit 137), and the
transitive `inner`-before-`outer` forwarding now reclaims its array on the emit
path (dec 3 → 4) with value + detector sound; the escape chain `wrap → idf` stays
rejected. Full gate green (`TestSelfHostAsmRunX86_64` auto-routes to IR, x86 + arm64
fixpoint/stage-2, `TestSelfHostStdTestE2E`, RC suites, targeted
`TestSelfHostRcPreciseDropX86IR` with transitive-reclaim + escape-chain cases).

Still order-dependent (only earlier-defined callees propagate). Full convergence
(later-defined / mutually recursive callees, arbitrary depth) is now a cheap
follow-up: the registry is already off the per-function path, so iterating
`borrowable_params_interproc` to a fixpoint costs only a few module-level passes.

### Landed (2026-06-14): full convergence

Iterated `borrowable_params_interproc` to a least-fixpoint: each pass re-runs the
escape walker against the PREVIOUS pass's complete registry (pass 1 = conservative,
each later pass only ADDS borrowability), stopping when the borrowable set stops
growing (monotone ⇒ size-stable = converged), with a small safety cap. Now a param
that forwards an array to ANY borrowable callee is a borrow regardless of
definition order or mutual recursion — `outer` defined BEFORE the borrowable
`inner` now reclaims (the single forward pass missed it), and mutually recursive
borrowers stay sound. Affordable because it is computed once per module on the emit
path (the iteration adds only a few module-level passes, not a per-function cost).
Verified: caller-before-callee reclaims (value + detector sound), escape chains
still rejected, mutual recursion sound; arm64 stage-2 self-compile stays green; full
RC gate green with two added `caller-before-callee` cases in the targeted test.

---

## Status + next major slice: struct/enum in-place reuse (FBIP) (2026-06-14)

### What's done

The array-reclaim Perceus arc is COMPLETE on the self-host IR (merged): direct
borrow reclamation (#3087), inter-procedural borrow inference computed once on the
emit path (#3126), iterated to full convergence (#3143), and widened to all
scalar-element arrays — `i64[]`/`f64[]` literals (#3150) and `i64[]`/`f64[]`-
returning calls (#3160). Array FBIP is already realised implicitly: `__fern_arr_dec`
returns buffers to a size-class freelist, so a precise drop makes the next same-size
allocation reuse the freed buffer.

A probe sweep (`asm_pathprobe_run`) confirmed the IR subset is otherwise
COMPREHENSIVE: maps (with `import "core/map"`), options, f-strings (`f"..${e}.."`),
structs, enums, tuples, closures, dyn-traits, slices, `i64`/`f64`/`u32`/`u64`,
recursion, compound-assign, nested arrays all route to the IR path, and all are
already gated by the ~120 `self_host_*_ir_test.go` files. (Every apparent gap was
invalid syntax — backticks, `i32?`, lowercase `none`, `.push()`/`.sort()`, brace
map-literals — or a missing import; none is a real IR gap.)

### The next major Perceus slice (not yet started — large, atomic)

Struct/enum **in-place reuse** (native `computeReuseSources` / `consumingMatchReuse`,
ir.go ~3661): pair a drop site D (a reclaimable struct/enum box freed by the
exit-sweep) with a construction site C of the same shape later in the function, and
have C **reuse D's box in place** instead of free-then-alloc — the general FBIP win;
the marquee case is a consuming `match` arm that rebuilds the same variant
(zero-alloc map/fold over a linked structure).

Concrete touch points in this codebase (from investigation):

- **Construction site:** `irlower.fern:2568` `op_struct_make(type_name, ndecl)` is
  where a fresh struct box is allocated; the reuse-aware variant takes a reuse token
  (a freed same-size box ptr) and uses it instead of allocating, falling back to
  alloc when no token is live.
- **Drop site:** `emit_dec_sweep_except` (~6499) + `slot_is_reclaimable_struct` +
  `emit_struct_field_drops` (~6231) free reclaimable struct locals at exit. Reuse
  must *take* such a box (suppress its free, hand its ptr to C) instead of freeing —
  native's `reuseConsumed[D]` marks D so `computePreciseDrops` doesn't ALSO drop it.
- **Analysis:** a `reuseSources` pass pairing each C with a compatible D (same struct
  type / box size, D dead before C, D not escaping). Mirrors `computeReuseSources`.
- **New IR op / token:** either a `reuse` field on `op_struct_make` or a paired
  `op_take_box` (drop→token) + `op_struct_make_reuse` (token→box). Defined in
  `ir.fern`, emitted by all THREE backends (`asm_ir` / `asm_arm64_ir` / `wasm_ir`).
- **Runtime:** the alloc-or-reuse is trivial given the freelist — `op_struct_make`
  already allocs; reuse just skips the alloc and writes fields into the taken box
  (same size guaranteed by the pairing).

**Why it's atomic (no incremental stub):** the `deadcode` CI job rejects dormant
unused code, so unlike native's flag-gated `RcReuseEnabled` landing, the first slice
must be wired in AND active. Scope the first slice to the simplest SOUND case
(a single reclaimable struct local D dead before a single same-type `op_struct_make`
C in straight-line code — no match, no aliasing) to bound it. Gate set: the usual
`TestSelfHostAsmRunX86_64` (auto-routes to IR) + byte-identical fixpoint/stage-2 on
both arches + `TestSelfHostStdTestE2E` + the RC suites + a new reuse e2e with the
`__rc_underflow()` detector at 0 + a firing proof (one fewer alloc than before).

---

## Struct in-place reuse (FBIP) — landed shapes + the enum blocker (2026-06-14)

Three FBIP reuse shapes now lower on the self-host IR (all pure-lowering, no backend
changes, gated by the byte-identical self-compile fixpoint):

1. **Functional-update self-overwrite** (#3177): `var c = T { ...d, f: v }` with `d`
   a fresh, non-escaping, never-reassigned same-type struct local dead after the
   statement → reuse `d`'s box; write the overrides, bind `c`, zero `d`'s slot.
2. **Array / wide-scalar fields** (#3191): widened candidacy from i32/boolean-only to
   any struct whose fields are scalar (i32/boolean/i64/f64/u32/u64) or leaksafe-array
   (i32[]/boolean[]/i64[]/f64[]). An overridden array field's OLD value is released
   (`struct_get` + `__fern_rc_dec`) before the fresh override is written.
3. **Cross-statement donor pairing** (#3202): a full `var c = T { .. }` (no base)
   reuses the box of an earlier dead same-type local; deterministic
   nearest-from-the-front unconsumed-donor pairing (self-overwrite sites excluded),
   each donor consumed once.

### Consuming-`match` enum reuse — NOT a contained slice (blocked; do NOT retry as-is)

The marquee FBIP (zero-alloc map/fold: `match (x) { A(p) => B(g(p)) }` reusing the
consumed scrutinee box) is blocked structurally in the current IR. Verified in
`irlower.fern`:

- The match scrutinee enum box is **never reclaimable and never freed — it deliberately
  leaks**. Payload bindings are BORROWS (`struct_get` reads, box intact); the scrutinee
  is pushed into the escape set by `walk_expr_escapes` (a bare-ident scrutinee escapes),
  so it is never in `reclaimable_names` and never swept.
- Enum-variant locals are never even reclaim candidates: `collect_fresh_in_stmt` /
  `is_fresh_struct_init` only collect `ExprStructLit` inits; an enum box is built from an
  `ExprCall` via `op_struct_make` and sits outside the reclaimable-struct machinery
  (`emit_dec_sweep_except` sweeps only `slot_is_reclaimable_struct`).
- Both existing reuse emitters work by redirecting a box that **was already going to be
  freed at exit** (an RC-tracked reclaimable donor) and zeroing the donor slot so it's
  freed exactly once via the new binding. The scrutinee box is neither RC-tracked nor
  reclaimed, so the zero-the-slot/free-once trick has nothing to hook into.
- The reuse dispatch lives only in `lower_func`'s TOP-LEVEL statement loop; it never
  descends into match-arm bodies (`lower_block`).

**Prerequisite for a future attempt** (sizeable, soundness-critical, goal-2 territory):
make consuming-match enum scrutinee boxes RC-reclaimable single-owner values — extend
`is_fresh_struct_init`/`collect_fresh_in_stmt` to recognise variant-constructor
`ExprCall`s and the dec-sweep to deep-drop+free enum boxes (this changes the leak
invariant for ALL enum locals and risks double-frees on shared/escaping enums, so it
must keep the fixpoint), add a consuming-scrutinee classification (exclude the bare-ident
scrutinee from "escape" only when the scrutinee is fresh/single-owner/dead-after-match),
and thread a reuse token into arm-body lowering with same-variant-box-size analysis.
There is NO minimal sound slice before that prerequisite lands — you cannot reuse a box
the compiler currently leaks and does not own.

---

## ROOT CAUSE pinned (2026-06-14): IR struct/enum boxes have no rc header → reclamation is latently unsound

Investigating consumed-enum-box reclamation surfaced the EXACT prerequisite (and a
pre-existing latent bug) behind the whole "free / reuse-with-free struct & enum boxes"
direction. Pinned and reproduced on **origin/main**:

- On the IR path, `op_struct_make` (`asm_ir.fern` ~2277; arm64/wasm mirrors) returns the
  **raw alloc block** as the box pointer: layout `[shape_ptr@0, f0@8, f1@16, …]`, with
  **no refcount word**. ARRAYS differ: `__fern_arr_box` offsets `base = rawptr+4` and
  stores rc at `base-4` (`ir_x86.fern` ~101-104). So `__fern_rc_dec` (→ `__fern_arr_dec`),
  which reads/decrements `[ptr-4]`, is correct for arrays but **reads the previous block's
  last word as a refcount** for a struct/enum box → spurious over-release, and on a
  spurious drop-to-0 pushes a bogus freelist block (shape ptr misread as length) → heap
  corruption.

- **Reproducible latent over-release on main** (`__rc_underflow()` reads non-zero):
  - `struct P{x:i32,y:i32} fn compute(){ var d=P{x:3,y:4}; return d.x+d.y }` → detector **1**
    (the exit dec-sweep decs d's header-less box).
  - `struct V{xs:i32[],t:i32} fn use(){ var d=V{xs:[1,2,3],t:9}; return d.xs[0]+d.t }`
    → detector **1** as well (a SECOND, distinct over-release in the array-field /
    field-drop reclamation path).
  These are masked because no existing green test calls `__rc_underflow()` after a plain
  reclaimable-struct exit-sweep; the byte-identical fixpoint doesn't catch it (the unsound
  dec is deterministic, so stage1 asm == stage2 asm), and AsmRun passes when the corruption
  is benign for a given heap layout.

- **Why the landed struct in-place reuse (#3177/#3191/#3202) is still sound:** the reuse
  emitters deliberately bind the reused box into a NON-reclaimable alias and LEAK it (never
  emit a struct-box dec), so they never hit the header-less dec. They save the allocation;
  they do not free the box. The bug lives only in the *plain* reclaimable-struct exit-sweep
  (`emit_dec_sweep_except`'s `slot_is_reclaimable_struct` branch) and `emit_struct_field_drops`.

- A surgical "skip the header-less box dec, keep the (sound, rc-header'd) array-field drops"
  edit fixes the plain-struct case (detector 1→0) but NOT the array-field case — confirming
  the subsystem has multiple latent faults, not one. Reverted rather than ship a partial fix.

### The unblocking refactor (one change fixes the bug AND enables enum free + consuming-match reuse)

Give struct/enum boxes an **rc header** like arrays: in `op_struct_make` on all three
backends (`asm_ir.fern`, `asm_arm64_ir.fern`, `wasm_ir.fern`) allocate `header + (nf+1)*8`,
return a base offset past the rc word, init rc=1 — and move EVERY box-format reader in
lockstep (`op_struct_get`/`op_struct_set` offsets, `op_variant_is`, dyn-dispatch shape read,
`opt_payload`-of-struct, `emit_struct_field_drops`). Gate on the byte-identical fixpoint.
Once boxes are header'd: (1) the plain-struct + array-field reclamation decs become sound
(latent bug fixed), (2) the dead `emit_dec_sweep_except` struct branch becomes real, and
(3) the consumed-scalar-enum free (analysis already prototyped + reverted) and ultimately
consuming-`match` box reuse become implementable — there is no sound way to free or reuse a
box the runtime can't refcount, so this header refactor is the single prerequisite for the
entire remaining struct/enum-reclamation + FBIP-reuse-with-free roadmap.

---

## STATUS UPDATE (2026-06-15): the rc-header prerequisite AND the consumed-enum / FBIP arc have LANDED

The "ROOT CAUSE pinned" section above is now **historical**. The rc-header refactor
it identified as the single prerequisite shipped, and the whole struct/enum-reclamation
+ FBIP-reuse-with-free roadmap it gated has been built on top, all merged green (each
gated on the byte-identical self-compile fixpoint AND `TestSelfHostBootstrapsItself`,
the gcc-link test that catches self-host codegen gaps):

- **#3231 — rc header for struct/enum boxes.** `op_struct_make` now allocates via
  `__fern_arr_box(nf+1)` on all three backends, so a box carries an rc word at
  `data-8` and a free-size word at `data-16` (the array layout `__fern_arr_dec`
  frees), with field offsets unchanged (shape@0, field i at (i+1)*8) — every box
  reader stays byte-identical. Fixed the latent over-release/heap-corruption bug
  (header-less boxes mis-dec'd) and unblocked everything below.
- **#3232 — consumed scalar-enum free.** `consumed_scalar_enum_frees`: a fresh,
  sole-owner, dead-after, non-escaping `var x = V(scalars…)` consumed by exactly one
  top-level `match (x)` is freed (dec + zero) right after the match instead of leaking.
- **#3233 — enum-donor cross-reuse (FBIP).** A consumed-and-dead scalar-enum box is
  DONATED to a later same-size struct literal (`op_struct_set_shape` re-shapes it in
  place) instead of being freed; the post-match free is suppressed.
- **#3239 — rc-payload enum deep-drop free.** Widened the consumed-enum free to enums
  whose variants carry a leak-safe scalar-array payload (i32[]/i64[]/f64[]/boolean[]):
  at the free a per-variant `variant_is` dispatch deep-drops exactly that variant's rc
  array fields, then the box. (string stays leak-only — a header-less {ptr,len} fat
  struct; string[]/nested struct/enum/map/opt/tuple payloads excluded.)
- **#3252 — borrow-only payload bindings.** The rc-payload free's arm gate widened from
  "reject any arm that binds an rc payload" to "reject only when a bound rc payload
  ESCAPES its arm" (`binding_escapes_arm` reuses the precise-drop `body_unsafe_for` /
  `expr_unsafe_for` with an empty borrowable registry).
- **#3259 — in-arm consuming-match box reuse (FBIP).** The marquee win: `var y =
  match (x) { … }` over a fresh sole-owner dead-after scalar-enum box where EVERY arm
  builds a same-size variant reuses x's box IN PLACE to construct y (read payloads to
  temps → `op_struct_set_shape` → write fields → bind y), one fewer `__fern_arr_box`
  per arm. An in-arm scrutinee is not a top-level `match (x)`, so the box previously
  leaked — there is no free to suppress; y owns it on every path, freed once.

All of the above gate `var x = V(...)` candidates through `consumed_scalar_enum_frees`'s
fresh/sole-owner/dead-after/non-escape predicate family and emit through the shared
`op_struct_set_shape` / per-variant `variant_is` dispatch + `op_struct_get/_set`.

### Current frontier (next slices, in rough order)

1. **Widen in-arm reuse payload types.** Today `inarm_reuse_match_ok` requires every
   payload field to be `i32`; generalise to all scalar widths (i64/f64/boolean/u32/u64)
   with width-aware temp reads + field writes (mirror `emit_enum_donor_reuse`'s
   `struct_field_is_i64` / `lower_i64` / `op_struct_set_i64` branch). Contained.
2. **Widen in-arm reuse to rc-payload donors.** Reuse a box whose old fields held
   leak-safe arrays: drop those old arrays before the overwrite (mirror
   `emit_cross_struct_reuse`'s old-value dec), and admit array fields in the result.
3. **Mixed arms (some reuse, some not).** Today every arm must construct a same-size
   variant. Per-path free-or-reuse accounting would admit matches where only some arms
   reconstruct — needs care that each path frees-or-reuses exactly once.
4. **rc-payload enum free for non-array rc payloads** (nested struct/enum/tuple/map):
   needs a recursive per-field deep-drop, not the single shallow array dec.
5. **String payload reclamation** is blocked on the leak-only string model (header-less
   {ptr,len}); out of scope until strings gain an rc header.

## LANDED (2026-06-15): in-arm reuse admits leak-safe ARRAY payloads (frontier item 2)

Frontier item 2 (above) is done for the array sub-case. In-arm consuming-match box
reuse — `var y = match (x) { V(...) => W(...), ... }` over a fresh / sole-owner /
dead-after / non-escaping enum box `x` where every arm builds a same-size variant —
now admits **leak-safe scalar-array payload fields** (`i32[]`/`i64[]`/`f64[]`/
`boolean[]`, `is_leaksafe_array_field`) on the donor variant **and** the constructed
result, alongside the existing scalars. The box is still reused in place
(`op_struct_set_shape` + per-field writes); the new part manages the donor's OLD array
fields via a per-field **cow-guarded** write that mirrors `emit_arr_store` /
`emit_cross_struct_reuse`.

### Soundness model (move vs replace, via one cow-guard)

For each RESULT field `di` that is a leak-safe array, the emitter
(`emit_inarm_match_reuse`) writes:

```
lower_expr(arg) ; store $iar           // NEW array value in a scratch local
load box ; struct_get(di,0)            // OLD donor array at slot di (still intact:
load $iar                              //   reshape rewrites offset 0 only; earlier
bin ne                                 //   field writes touched di' < di)
if (old != new)
    load box ; struct_get(di,0)
    __fern_rc_dec ; drop               // release the donor's old array
end
load box ; load $iar ; struct_set(di,0)  // store NEW
```

This makes both arm shapes sound automatically:

- **Move** (`V(a, xs) => W(a+1, xs)`): the binding `xs` is read into a pointer temp at
  arm entry (read-before-overwrite); the temp **is** the array still in box slot `di`,
  so `old == new` → **no dec** → the array is reused in place (the strongest FBIP win;
  no `__fern_arr_box` for the array either).
- **Replace** (`V(a, xs) => W(a, [7,8])`): the arg is a fresh literal, `old != new` →
  the donor's old array is dropped and the fresh one written, owned by the reused box.

Field-order safety: the guard for field `di` reads `box.field[di]` *before* it is
overwritten, and writes proceed `di = 0..n-1`, so no field is read after being
overwritten (identical to `emit_cross_struct_reuse`).

### Conservative gates (what is rejected, and why)

Implemented in `inarm_reuse_match_ok` + `consumed_inarm_reuse_sites`:

1. **Donor arrays must be fresh literals** (`inarm_donor_arrays_are_fresh`): `var x =
   V(.., [..])` — every leak-safe-array payload arg is an `ExprArray`, so the box solely
   owns each donor array; the cow-guard dec can't double-free a value aliased elsewhere.
2. **Per-position array-ness must match** between the arm's pattern variant and its
   result variant: `result-field-di is array  iff  pattern-field-di is array`. This
   guarantees the cow-guard at an array result slot reads a *real array pointer* (never
   a scalar-as-pointer), and never silently overwrites + leaks a donor array under a
   scalar result.
3. **Each array result arg is a fresh literal OR the same-slot move** (`arg di == the
   pattern binding at slot di`). A **permutation / swap** (`V(p,q) => V(q,p)`) is
   rejected here — at slot `di` the new value would be bound from a *different* slot, so
   `old != new` would wrongly dec a still-needed array (use-after-free). A
   **double-reuse** (same binding moved into two result slots) and any **stray borrow**
   of a moved array (e.g. `xs.len()` in another arg) are rejected by a follow-up check:
   each array pattern binding may appear in the result ctor *only* as its own same-slot
   move.

Donor-init gate widened from `fresh_scalar_enum_init` →
`fresh_reuse_enum_init` (`enum_all_variants_reuse_ok` =
`enum_variant_reuse_payload_ok` per variant: every payload scalar-or-leak-safe-array).
Scalar payload fields keep the existing read/write path
(`struct_get`/`struct_set`/`struct_get_i64`/`struct_set_i64`); only array result fields
take the cow-guard branch.

### Firing proof

Route `"ir"`, value-correct, detector `__rc_underflow()` == 0, plus a heap-reuse
corruption probe (a fresh array allocated after the match reads back intact). The
`__fern_arr_box` count over the self-host IR emitter confirms the FBIP win: the MOVE
program emits **one fewer** `__fern_arr_box` than the REPLACE program (3 vs 4) — the
moved array is reused in place (no fresh box) whereas the replace allocates a fresh
`[7,8]`. The swap negative routes to the AST fallback (reuse correctly does not fire)
and stays value-correct.

Tests: `TestSelfHostRcPreciseDropX86IR` gains
`inarm-reuse-array-move-{value,detector}`,
`inarm-reuse-array-move-corruption-probe-detector`,
`inarm-reuse-array-replace-{value,detector}`,
`inarm-reuse-array-replace-corruption-probe-detector`, and
`inarm-reuse-array-mixed-i64-detector` (i64 scalar + i32[] array).

### Deferred (still open under item 2 / beyond)

- **i64[] / f64[] enum-array payload read-back** routes outside the IR subset for an
  orthogonal reason (the y read-back via the i32-only match-EXPRESSION / 8-byte-element
  enum-array path), so those firing cases aren't expressible in the route-asserting
  harness yet. The reuse cow-guard itself is element-width-agnostic (it moves/drops a
  single array *pointer*), so once the read-back lands they fire for free.
- **String / nested-struct / struct-array / map / option / tuple payloads** remain out
  (items 4–5).

## Item 1 — arrow-lambda syntax in the self-host parser (parse-level IR widening)

### Diagnosis (the real gap vs. the characterised one)

The characterisation that "lambdas fall back to AST" was an artifact of the probe
inputs using arrow syntax `(x) => …`, which the **self-host parser did not support at
all**. The verbose `function (params): R { … }` lambda form already lowers through
the IR path for every shape — non-capturing binding, capturing binding (param-lift),
lambda-as-call-argument (hoist to `__lam_N` const_func), lambda in a struct field, and
the escaping `return <capturing lambda>` closure-box. All of those route `"ir"` and
produce correct values end-to-end on the self-host x86-64 IR driver; the lambda-lift +
closure machinery in `irlower.fern` is mature.

The native compiler **does** accept arrow lambdas (`parser.go` `looksLikeArrowLambda` /
`parseArrowLambda`, #2701), desugaring `(params): R => expr` to the same `ast.Lambda`
the verbose form builds. The self-host parser had only the `function`-keyword form, so
arrow source mis-parsed (the parens read as a grouping/tuple, the `=>` was left stray)
and `main` carried no liftable lambda — bailing the module to AST.

Exact bail point: **parse** (not lift, not lower, not an eligibility gate). With the
verbose form the identical program already routes `"ir"`; only the arrow *surface
syntax* was missing.

### Fix (simplest sound slice)

Port the native disambiguator + parser into `examples/self_host/parser.fern`:

- `(Par).punct_at(idx)` / `(Par).ident_at(idx)` — absolute-index token lookahead.
- `(Par).arrow_lambda_at()` — at a `(`, returns true for `() =>`/`():` or
  `(IDENT: TYPE, …) [: R] =>` (scans to the balanced `)`, checks the following token),
  which a grouping `(e)` / tuple `(e0, e1)` never matches.
- `parse_arrow_lambda(p)` — parses `(params) [: R] => expr` into the SAME
  `ExprLambda { params, ret_type, body: [return expr] }` the verbose `function (…)`
  form produces, so it rides the existing lambda-lift + IR lowering with **zero**
  codegen changes.
- Wired into `parse_primary`'s `(` arm, checked before the grouping/tuple parse.

Because the desugar target is the existing `ExprLambda`, every arrow shape inherits the
verbose form's IR support: non-capturing bindings, capturing bindings, multi-param,
empty-param, and lambda-as-argument all route `"ir"`.

### Firing proof

`asm_pathprobe_run` flips `(x: i32): i32 => x + 1` (and the capturing / multi-param /
empty / as-arg forms) from `"ast"` → `"ir"`; the self-host x86-64 IR driver returns the
oracle value (6 / 15 / 7 / 42 / 10), matching the native interpreter. Groupings
`(a + b) * 2` and tuples `(1, 2)` are unaffected (the lookahead requires `IDENT :` or
`() =>`/`():`). The lambda-free self-host sources contain no arrow syntax, so the
byte-identical Stage-2 / LoadFixpoint bootstrap holds (gate green).

Tests: `TestSelfHostRcPreciseDropX86IR` gains `arrow-lambda-noncap-binding`,
`arrow-lambda-capturing-binding`, `arrow-lambda-multi-param`,
`arrow-lambda-empty-params`, `arrow-lambda-as-arg`, and `arrow-lambda-vs-grouping`
(each route-asserted `"ir"` + value-checked).

### Note / benign divergence

The self-host accepts an un-annotated arrow `(x: i32) => x + 1` (no return type) and
computes the correct value, where the native compiler rejects it with E002 (the
self-host lacks that strict void-return checker rule). The added tests use the
annotated `: R =>` form both compilers accept; the unannotated form is a permissiveness
gap on the checker side, not a codegen one — no wrong values, no fixpoint impact.

---

## STATUS UPDATE (2026-06-16): IIFE match-EXPRESSION result types, the x86-64 heap-ceiling lift, and the IR-subset frontier

A further arc of goal-#1 widening (shrink the AST fallback) plus the infra change that
unblocks it. All gated on the byte-identical self-compile fixpoint + the gcc-linking
`TestSelfHostBootstrapsItself` + (for the heap change) the large-program-link
`TestSelfHostStdTestE2E`.

### Landed

- **#3335 — struct / string / tuple / enum payload bindings in match-EXPRESSIONS.**
  A `var r = match (e) { V(p) => p.x, … }` (the IIFE form) bound only scalar / array
  payloads before; widened to leak-safe-struct / string / tuple / nominal-enum
  payloads, BORROW-only, i32-result (the underlying StmtMatch path already binds them;
  only the IIFE gate `iife_payload_field_bindable` rejected them). Coupled with the
  first x86-64 heap bump (1.875 GiB) the widening's extra IR-path working set needed.
- **#3350 — lift the x86-64 heap ceiling past 2 GiB.** The static-`.bss` `__fern_heap`
  was addressed RIP-relative (`leaq (%rip)`, disp32), capping heap_base+size < 2 GiB,
  and the self-compile peaked ~1.78 GiB (≈95 MiB from OOM). Fix: address the heap base
  with `movabs $__fern_heap` (64-bit absolute, valid `-no-pie`) and emit `__fern_heap`
  as the LAST `.bss` symbol (a giant heap otherwise pushes `__fern_strbuf_data` /
  `__fern_argc/argv/envp` — still on `leaq (%rip)` — past 2 GiB → `R_X86_64_PC32`
  truncation → LARGE programs fail to LINK; small ones don't, so only StdTestE2E caught
  it). Lockstep 2.5 GiB across asm.fern / asm_ir.fern / native x86_64.go.
- **#3357 — string-RESULT match-EXPRESSIONS.** Recover the result-temp KIND
  (`iife_payload_result_kind`) from the payload field type so a string result (bare
  payload `V(x)=>x`, struct/enum string field, tuple string element) classifies the
  temp `str`. (i64/f64 struct-FIELD results NOT admitted — the width-64 field read
  into the i64 temp segfaults; a pre-existing AST-backend segfault too. Composite
  results returned whole still bail — the temp can't carry the type.)
- **#3374 — string-CONCAT-result match-EXPRESSIONS.** `V(x) => x + "!"` /
  `"k=" + x` / `p.name + "!"`: `iife_string_concat_borrow_only` admits a `+`-tail whose
  operands are each a borrow-leaf of the payload or an independent string. Sound
  because strings are leak-only (no reclamation), so the only requirement is correct
  temp typing.
- **ci(selfhost) — 8→12 shards (#3362 / merged into the matrix).** The cumulative IR
  widening slowed the bootstrap/fixpoint self-compiles; the round-robin clustered the
  heavy ones and shard 7 hit the 10-min job timeout. 12 shards (24 jobs/arch) restores
  the margin.

### The IR-subset frontier (where it stands)

`asm_ir_run.fern -ir-probe` prints `eligibility_report(mod)` — a per-function IR-vs-AST
breakdown (see `TestSelfHostIREligibilityProbe`). Two caveats when reading it: run
STANDALONE on a single file it OVER-reports bails (a function whose param/return types
live in another module can't resolve them without the full import context, so it shows
`BAIL lower` spuriously); the accurate frontier is the FULL-context compile. Most common
constructs now lower (verified by direct pathprobe): scalar / array / struct / string /
tuple / enum / Option / Result / map (incl. untyped-local) / nested-match / generics
(unbounded + bounded `[T: Trait]`) / `dyn` dispatch / closures incl. escaping + arrow
lambdas / recursion / tuple returns / for-in / break-continue / try `?`.

Genuinely-remaining gaps (niche or large):
1. **Composite RESULTS from a match-EXPRESSION** (`var p = match(e){ V(q) => q }` returning
   a struct/tuple/enum/array whole) — the IIFE result temp can't carry a composite type.
2. **i64 / f64 struct-FIELD / tuple-ELEMENT results** in a match-EXPRESSION — the width-64
   field-read → i64-temp store doesn't round-trip (segfaults); also a pre-existing
   AST-path segfault. A real bug to fix, not just a gate.
3. **Bare trait-NAME param** (`function f(x: Trait)` without `dyn`) — `dyn Trait` and
   `[T: Trait]` already lower; the implicit-dyn spelling bails. Niche.
4. **Full non-leaksafe-struct RECLAMATION on the IR path.** Field READS of non-leaksafe
   structs (params, array elements) already lower; what bails is CONSTRUCTING + reclaiming
   a struct with rc-tracked (nested-struct / string[] / nested-array) fields — the IR path
   reclaims only leak-safe structs. Closing this (recursive per-field deep-drop, or a
   leak-only model for all structs like strings/enums) is the bulk of the remaining
   distance to retiring the legacy AST emitter, and is the natural next major arc.

### CORRECTION (2026-06-16, same day): gap #4 above was overstated

Direct pathprobe + a `__rc_underflow()`-gated run on current main show that
NON-LEAKSAFE structs already LOWER and RECLAIM soundly on the IR path: constructing
`Outer { inner: Inner { xs: [..] }, n }` (Inner non-leaksafe via an array field),
returning one from a function, reading its fields (incl. `o.inner.xs[0]` and
`o.items[i].v` for a nested struct-ARRAY field), and REASSIGNING a struct with a
`string` / `string[]` field (which frees the old rc-headered box) all route `"ir"`,
and the over-release detector reads 0. The box is reclaimed; the nested rc-tracked
fields (strings / string[] / nested structs) LEAK — exactly the path's existing
leak-only model for strings/enums — which is sound (no double-free), just not
optimal.

So "full non-leaksafe-struct reclamation" is NOT a goal-#1 correctness blocker (those
modules lower today). What remains there is a goal-#2 OPTIMIZATION: precise deep-drop
of a reclaimed struct's nested rc fields (recursive per-field drop) to shrink the
leak set — valuable for long-running programs, irrelevant to whether the AST emitter
can be retired.

The standalone `-ir-probe BAIL lower` count on a single file (e.g. ~166 on
irlower.fern) is therefore dominated by the missing-cross-module-context artifact,
NOT by genuine lowering gaps. The real goal-#1 frontier is the three NARROW
match-EXPRESSION / trait cases (composite result, i64/f64 struct-field result, bare
trait-name param) — each a contained IIFE-result-typing / width-store / implicit-dyn
slice, not a broad capability hole. Closing a whole MODULE to IR (so the legacy AST
emitter can retire) is then a question of the LAST per-module holdout function, best
found with a FULL-context eligibility probe rather than the standalone form.

### Goal-#2 deep-drop progress + the inlining wall (2026-06-16)

The reclaimed-struct field deep-drop (shrinking the leak set noted above) was
widened across several merged slices, all gated by the `__rc_underflow()`
over-release detector + the byte-identical x86-64 fixpoint:

- **scalar `string` fields** now FREE a fresh, sole-owned heap string on the
  field-drop (`emit_struct_field_drops` shallow-decs the box; construction incs
  an aliased value so the drop decs only the dup). The freeable set is
  `expr_is_fresh_str`: `a + b` concat, the transforms `.to_upper()` /
  `.to_lower()` / `.reverse()` / `.repeat(n)` (+ `str_to_upper` / `str_to_lower`
  / `str_repeat` free-fns), and the alloc builtins `chr` / `string_from_bytes`.
  EXCLUDED (alias live data → inc'd, leak): a string literal (`.rodata`), an
  ident / field-read, `.trim()` (a zero-copy VIEW into the receiver), `.replace()`
  / `.to_string()` (receiver-identity fast-paths). `i32_to_string` is excluded for
  a different reason — its emitter returns a box whose `data` points into the
  MIDDLE of a 32-byte scratch buffer (digits written backwards), not an allocation
  boundary, so the box can't be cleanly reclaimed (an emitter fix — copy to a tight
  buffer — would unblock it).

- **`string[]` slab fields — ATTEMPTED, REVERTED (the inlining wall).** A
  `string[]` field is a flat slab of string-box pointers marked `is_arr` (swept
  as a local, shallow-dec'd like an `i32[]`/`f64[]` slab; elements leak). Adding it
  to `emit_struct_field_drops` (shallow slab dec) + the construction alias-inc is
  SOUND and the targeted detector cases pass — BUT it OOMs the stage-2 self-compile
  (`exit 137`). Cause: the deep-drop is INLINED at every reclamation site, and the
  compiler's OWN wide structs — `LowerState` carries ~20 `string[]` fields — emit a
  ~20-field drop block per reclamation, blowing up the generated compiler's
  compile-time memory. (The scalar-`string` arc above avoided this because the wide
  structs carry `string[]`, not bare `string`, fields.)

  **Prerequisite for any further deep-drop widening (`string[]`, nested-struct,
  struct-array fields — all even larger inlined blocks):** emit one
  `__drop_<StructType>` helper FUNCTION per type (as native `internal/ir/ir.go`
  does — see the `__drop_arr_*` / `__drop_dyn_*` family) and CALL it at each
  reclamation site, so the per-type drop code is emitted once, not inlined N times.
  Until that mechanism exists in the self-host backend, the field-drop must stay
  limited to the single-pointer scalar cases (`string`, the leaksafe scalar arrays)
  whose inlined block is small.

## Retired the early PoC drivers (2026-07-03, #4391 follow-up)

The stack-IR proof-of-concept drivers from slices 1/3/7 — `ir_run.fern`
(`Op[]` round-trip), `ir_x86.fern` + `ir_x86_run.fern` (the first
IR→x86-64 backend), and their Go tests `TestSelfHostIRRoundTrip` /
`TestSelfHostIRx86Run` / `TestSelfHostIRDiff` — are **retired**. They were
smoke-test-only, i32-subset scaffolding that proved the AST→IR→machine-code
shape end-to-end; the production multi-backend emitters
`asm_ir.fern` / `asm_arm64_ir.fern` / `wasm_ir.fern` (slices 24–30) fully
superseded them, carry the whole-language IR subset, and run the 250+ e2e
fixtures. Removing them is the second sanctioned follow-up from
`docs/SELFHOST-SSA-DECISION.md` (#4391) — retired before #3457 so the AST-emitter
retirement deletes one PoC fallback fewer. The remaining slices' descriptions
above are preserved as the historical rebuild record.
