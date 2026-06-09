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
