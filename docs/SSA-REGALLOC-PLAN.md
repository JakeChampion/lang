# SSA-level register allocation for the native backends

Tracking: #4112 (child of the binary-size epic #4109).

## Why

The native x86-64/arm64 backends (`internal/codegen/x86_64`, `internal/codegen/arm64`)
are naive stack machines: every IR op pushes its result to the machine stack and
the next op reloads it, with `rax`/`rcx` (x0/x1) the only scratch. The peephole
passes (#4121, #4125) removed the *adjacent* push/pop pairs (−26% to −42%
instructions on real self-host drivers), but the *non-adjacent* spills — a value
held live across an intervening sub-expression — still round-trip through memory.
Eliminating those needs a register allocator.

Per `CLAUDE.md` ("new optimisations should live in `internal/ir` so all backends
benefit"), the allocator belongs at the **SSA layer** (`internal/ssa`), not bolted
into each backend. `internal/ssa` is already a full target-independent SSA with
dominators, RPO, loops, def-use chains, and ~100 ops — but it currently feeds only
`wasmssa` (a stack machine that needs no registers). So this track adds (1) the
allocation analysis/passes to `internal/ssa`, and (2) a new SSA→native emit path
that consumes the allocation, ultimately **replacing** the stack-machine backends.

## End state

`ir.LowerWith → ssa.LiftFromIR → ssa.Optimize → ssa.Allocate → {x86_64,arm64}ssa.Emit`,
with the legacy `internal/codegen/x86_64` / `internal/codegen/arm64` stack-machine
emitters retired once the SSA path reaches parity. The win is twofold: a much
smaller/faster self-host binary (the epic's goal) and one shared register-aware
backend instead of two hand-maintained stack emitters (plus their self-host
mirrors `asm.fern`/`asm_arm64.fern`).

## Phases

Each phase is an independently reviewable, tested PR. Earlier phases are inert
(no behaviour change) until the emit path is wired and defaulted.

- **Phase 0 — Liveness (this PR).** `internal/ssa/liveness.go`: SSA-aware
  backward dataflow producing per-block live-in/live-out sets, with correct phi
  semantics (a phi arg is live-out of its *predecessor edge*, not live-in of the
  phi block; a phi result is not live across edges into its block). Pure
  analysis, fully unit-tested (straight-line, cross-block, diamond+phi,
  loop+phi). Foundation for everything below.

- **Phase 1 — Live intervals + allocator.** Linearise blocks in RPO, compute
  per-value live intervals from the Phase-0 sets, and run a linear-scan
  allocator producing a `Value → physical-register | spill-slot` assignment.
  Parameterised by a small target description (register count, caller/callee-
  saved split, fixed registers for call args/returns, div/shift). Validated by
  an interference checker (no two values whose intervals overlap share a
  register) over hand-built and lifted SSA. Still no codegen.

- **Phase 2 — SSA→x86-64 emit (behind a flag).** New `internal/codegen/x86_64ssa`
  consuming the allocated SSA: real register operands, spill load/store at slot
  boundaries, phi resolution via parallel-copy on edges (with critical-edge
  splitting), two-address fix-ups (`add dst,src` needs `dst==arg0`), and the
  System V call ABI (caller-saved around calls, arg/return registers). Gated by
  an `Options` flag / env var; validated **differentially** against the existing
  stack-machine backend over the e2e corpus (same exit codes / output).

- **Phase 3 — Default x86-64 to the SSA path.** Flip the default once the
  differential suite is green across the whole corpus; keep the stack machine
  behind a flag as a fallback. Measure and record the self-host binary-size win
  on #4109/#4112 (expected the largest single drop in the epic).

- **Phase 4 — arm64 SSA emit + default.** Mirror Phase 2/3 for AAPCS64
  (`internal/codegen/arm64ssa`). arm64 is the default target, so this is where
  the headline self-host-binary number lands.

- **Phase 5 — Retire the stack-machine backends.** Delete
  `internal/codegen/x86_64` / `internal/codegen/arm64` (and eventually the
  self-host `asm.fern`/`asm_arm64.fern` mirrors) once the SSA path is the sole
  native path. Large code + maintenance reduction.

## Known hazards (called out so later phases don't trip on them)

- **SSA coverage parity.** `LiftFromIR`/`wasmssa` target wasm and may not yet
  cover everything the native IR backends emit (strings, structs, arrays, maps,
  closures, **RC inc/dec**, runtime-helper calls, PIE). Phase 2 must audit the
  lift for native-relevant ops and extend it before the differential suite can
  pass — this is the bulk of the work, not the allocator itself.
- **Perceus / RC ordering.** The IR carries reference-counting ops; the SSA path
  must preserve them and the allocator must not reorder across the points they
  assume. Confirm whether RC insertion happens before or after the SSA lift.
- **Phi edge cases.** Critical edges must be split before phi resolution;
  `predIndex` (Phase 0) assumes a block appears once in a successor's `Preds`
  (true for `Br`/`BrIf` unless `True==False`, which the lifter should normalise).
- **Fixed-register constraints.** x86 `idiv` (rax/rdx), variable shift count
  (cl), and call arg/return registers pin specific physical registers; the
  allocator needs pre-coloring for these.

## Status

- [x] Phase 0 — liveness analysis (`internal/ssa/liveness.go`, `liveness_test.go`)
- [x] Phase 1 — live intervals + linear-scan allocator (`internal/ssa/regalloc.go`, `regalloc_test.go`): `LiveIntervals` (single conservative interval per value over the RPO linearisation) + `LinearScan` (Poletto–Sarazin) + `VerifyAllocation` (interference oracle). Still pure analysis.
- [~] Phase 2 — SSA→x86-64 emit (flagged) + differential validation
  - [x] Oracle: `internal/ssa/eval.go` — reference interpreter for the
    integer/control-flow subset, used to validate the emitter differentially
    (and to guard semantic preservation across any SSA pass). Tested incl. an
    `Eval == Eval∘Optimize` property.
  - [~] The SSA→x86-64 emitter (`internal/codegen/x86_64ssa`):
    - [x] Slice 1 — straight-line integer subset → abstract register-machine
      program, driven by `ssa.LinearScan`. Proves the regalloc-specific logic
      (operand assignment, the x86 two-address fixup, spill load/store) via a
      model interpreter (`Run`) diffed against `ssa.Eval`, incl. spill-forcing
      cases. Dormant infrastructure — not yet wired into any codegen path.
    - [ ] Slice 2 — control flow + phi resolution (parallel-moves on edges,
      critical-edge splitting).
    - [ ] Slice 3 — real GAS-text emission + assemble/run validation (call ABI,
      `idiv` rax/rdx, the ELF `_start` runtime), then diff against the existing
      stack-machine backend over the e2e corpus.
- [ ] Phase 3 — default x86-64 to SSA; measure binary-size win
- [ ] Phase 4 — arm64 SSA emit + default
- [ ] Phase 5 — retire the stack-machine backends
