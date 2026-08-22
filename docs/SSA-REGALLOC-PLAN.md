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
    - [x] Slice 2 — control flow + phi resolution (out-of-SSA): multi-block,
      Br/BrIf, parallel-copy phi moves (read-all-then-write-all via temp slots,
      so swaps/cycles are correct), and critical-edge splitting. Validated
      against `ssa.Eval` incl. diamond/loop/critical-edge/phi-swap. Also fixed a
      latent bug the swap test surfaced: `ssa.Eval` was resolving phis
      sequentially instead of in parallel.
    - [~] Slice 3 — real GAS-text emission + assemble/run validation:
      - [x] 3a — `EmitAsm` renders real x86-64 (Intel syntax) for no-parameter
        integer functions and the slice-2 control flow; assembled via
        `nativex86.AssembleProgram`, wrapped in a static ELF, and **run
        natively**, with the exit code diffed against `ssa.Eval` (arithmetic,
        bitwise+spill, comparison via cmp/setcc/movzx, branch, loop, and a
        real back-edge **phi swap**). First runnable machine code from the SSA
        path.
      - [~] 3b — System V parameter ABI on the real-asm path. The function
        prologue moves each incoming arg register (`rdi`/`rsi`/`rdx`/`rcx`/`r8`/
        `r9`) into that param's allocated home; `EmitAsmArgs` bakes the entry
        args into `_start`. Because an arg register can be another param's home,
        the moves are a **parallel copy** — slot-homed params first (read arg
        regs, write memory), then a register parallel-copy that emits any move
        whose dest isn't still a source and breaks leftover cycles with `xchg`.
        Up to six integer params (stack args a follow-up). Validated by
        assembling + running natively (`TestAsmRunParam*`: identity, 4-param
        sum, param-in-loop, the sixth/`r9` param, all over spill-forcing
        register counts).
      - [x] 3b (fixed registers) — shifts (`cl`) and `idiv`/`div` (`rdx:rax`)
        on the real-asm path, WITHOUT allocator pre-coloring. `gas.go` emits a
        self-contained sequence that `push`/`pop`s the pinned registers around
        the op and stages operands through `s3` (the last register in the file,
        always above `rax`/`rcx`/`rdx`): shifts copy the count into `rcx` and
        read `cl`; divisions stash the divisor in `s3`, load the dividend into
        `rax`, extend it (`cqo` signed / `xor rdx` unsigned), `idiv`/`div`, then
        capture the result (quotient `rax` / remainder `rdx`) into `s3` **before**
        the pops (so a dst that aliases `rdx` at `numAlloc==1` isn't clobbered)
        and write it into dst afterwards. Validated natively: signed/unsigned
        div+rem incl. a negative dividend (`cqo`+`idiv`), `sar` vs `shr` via a
        negative operand, and operands/counts kept live across the op so the
        push/pop preservation is exercised — all over spill-forcing counts.
      - [x] 3b (i32 width) — an i32-width result (`W != 64`) is sign-extended
        back into the full register (`movsxd dst, dst32`) after each computing
        op, mirroring the model's `maskW` (`int32(v)`). This matters when a
        later op reads the high 32 bits — unsigned shift/div or unsigned compare
        — where a bare 64-bit computation would diverge from `Eval`. `maskFix`
        is appended to `MovImm`/`UnNeg`/arithmetic-`BinOp`/shift/div; the
        `movsx`/`movzx` unary ops already set width explicitly. Validated
        natively by a case pinned to fail without the fix (`mul(0x10000,0x10000)`
        at i32 width → 0, then `>>u 32`; the unmasked 64-bit product keeps bit 32
        and yields 1) plus negated-then-unsigned-shifted cases, over
        spill-forcing counts. **Slice 3b is complete** — the real-asm path now
        covers the full integer op set with parameters.
      - [x] 3b (direct calls / multi-function modules) — `EmitAsmModule` emits a
        whole set of functions (each under a unique `fn_<name>` label, block
        labels namespaced per function) with a `_start` that calls the entry;
        `EmitAsmArgs`/`EmitAsm` are now single-function wrappers over it.
        `OpCall` lowers to the System V call ABI. Two ABI obligations the model
        never had to model are handled WITHOUT allocator call-clobber awareness:
        (1) *caller-saved* — every caller-saved allocatable register is
        conservatively saved around the call (callee-saved regs and spill slots
        survive on their own); args are passed via the stack (push from homes →
        pop into arg registers) so the home→arg-reg shuffle can't clobber a
        not-yet-consumed source; the result is captured through the scratch reg,
        which is never in the saved set; the stack is padded to stay 16-aligned
        at the `call`. (2) *callee-saved* — each function saves the callee-saved
        registers it may touch (rbx / r12–r15, reached via allocatable homes or
        scratch) into fresh slots above the allocator's spill area and restores
        them at every return. Validated natively: cross-function calls,
        recursion (factorial — `n` live across the self-call), several values
        live across multiple calls in one block, and a six-arg callee filling
        every arg register — all over spill-forcing counts.
      - [x] 3b (memory) — `OpAlloc`/`OpLoad`/`OpStore` (incl. the sub-word
        variants) on the real-asm path. A lazy anonymous `mmap` arena reserved
        in `_start`, with a bump cursor (`__ssa_heap_ptr`) and a limit
        (`__ssa_heap_end`); `MemAlloc` is
        `result = align8(cursor); cursor = result + size`, then a call to
        `__ssa_heap_guard`, which reports `fern: out of memory (heap arena
        exhausted)` and exits 125 once the cursor passes the limit. Loads/stores
        use `[base + disp]` with `movzx`/`movsx` + `byte/word ptr` for sub-word
        and the value sub-register for narrow stores. The heap section + init
        are emitted only when a program uses memory ops. The arena ends at
        `0x8000_0000`: `maskFix` sign-extends an i32-width address, so a wider
        one would hand out truncated pointers before the guard could fire
        (#7329).
        Validated natively: full-word round-trip, sub-word zero/sign-extension,
        a byte array, and a heap **shared across a call** (callee allocs, caller
        reads the pointer back) — over spill-forcing counts.
      - [x] 3b (strings) — `OpConstString` on the real-asm path.
        `collectStrings` assigns each unique literal a `.rodata` label (emitted
        as a `.byte` list); `ConstStr` lowers to `lea reg, [rip + str_N]`.
        `OpConstStringLen` already rides `MovImm` (the length is a compile-time
        constant), so no extra work. Validated natively: literal length + byte
        reads, the empty string, two coexisting literals, and a string passed
        across a call (pointer survives the ABI, bytes readable in the callee).
      - [x] 3b (floats via SSE) — `OpConstFloat`, `OpFAdd/FSub/FMul/FDiv`, float
        compares, `OpFNeg/FPromote/FDemote/IToF*/FToI*` on the real-asm path.
        Floats live in GP registers as their f64 bit pattern (matching the
        model); each op shuttles operands into `xmm0`/`xmm1` with `movq`, runs
        the scalar SSE op (`addsd`/`ucomisd`/`cvtsi2sd`/`cvttsd2si`/…), and
        shuttles the result back — the same scheme the stack-machine backend
        uses. f32 width rounds via a `cvtsd2ss`+`cvtss2sd` round-trip; `FNeg`
        flips the sign bit in the GP register; `FPromote` is the identity;
        `LoadF`/`StoreF` already ride the 8-byte memory path. NaN operands and
        unsigned int↔float ≥ 2^63 are out of scope (finite/in-range values match
        `Eval`). Validated natively: arithmetic, all six compares, int↔float
        round-trips, negation, truncation, f32-width, and floats through memory
        — over spill-forcing counts.
      - [x] 3b (closures) — `OpMakeEnv` / `OpMakeClosure` on the real-asm path,
        on the same `.bss` bump heap. `MakeEnv` allocates an env block of the N
        captures (8-byte slots) and returns the env pointer; `MakeClosure`
        additionally allocates a `{fn_idx, env_ptr}` cell — `fn_idx` = the
        target's index in the module's sorted function order, threaded like the
        string labels and matching the model's function-table index — and holds
        the env pointer in `s0` (free during the instruction) across the second
        allocation. Validated natively: env-block round-trip, a closure's
        `fn_idx` + captures read back, and a zero-capture closure — over
        spill-forcing counts. **This closes the real-asm op coverage**: the SSA
        real-assembly path now handles the full per-function op set —
        integers, parameters, direct calls, memory, strings, floats, and
        closures. Next: **3c** — gate the SSA path behind a flag and diff it
        against the stack-machine backend over the e2e corpus, then flip the
        x86-64 default and measure the self-host binary-size win.
      - [~] 3c — wire behind a flag and diff against the existing stack-machine
        backend over the e2e corpus.
        - [x] Real-corpus coverage differential (`corpus_coverage_test.go`):
          drive real programs through the actual pipeline (`parser → checker →
          ir.LowerWith → ssa.LiftFromIR → x86_64ssa.Emit`) and tally how many
          functions lift and emit, with a histogram of blocking ops — measuring
          the emitter against *real* checker/lowerer output, not hand-built SSA.
          First run surfaced a genuine robustness gap: the emitter walked *all*
          blocks, but the allocator's liveness only covers the reachable CFG, so
          a value defined solely in an unreachable block (a lowerer-left dead
          epilogue after both `if` arms return) had no allocation and failed to
          materialise. Fixed by emitting only `ssa.Reachable` blocks (never
          branch targets of a reachable block, so no jump dangles). Corpus now
          lifts+emits 11/11; the test asserts full emit coverage.
        - [x] Broaden the corpus to composite types (struct field access, array
          indexing, `match`, string `.len()`), closures (a returned closure over
          a capture; a closure passed as an argument and called indirectly), and
          `Option` construction + `match` (the pair-return path). All lift and
          emit: **25/25** functions across the corpus. So the per-function
          emitter is complete against *real* lowered code — the remaining gaps
          are whole-program, not op-level.
        - [~] Whole-program wiring. `EmitProgram(prog, info, numAlloc)` lowers a
          whole checked program via the SSA path — `ir.LowerWith → ssa.LiftFromIR`
          per function → `EmitAsmModule` with `main` as the entry (its `_start`
          calls `main` and exits with the i32 return). `program_run_test.go`
          compiles real programs to a native binary this way, runs them, and
          diffs the exit code against the **tree-walking interpreter** (an
          oracle independent of the SSA path) — the first end-to-end validation
          of the SSA register-allocated native path against real language
          semantics. Passing: constants, arithmetic, cross-function calls,
          recursion (factorial), a `while` loop, conditionals, and div/mod, over
          spill-forcing register counts. Scope so far: the integer/no-runtime
          subset (programs whose whole call graph lifts+emits); RC inc/dec,
          runtime helpers, and closure dispatch layer on next, then the flag +
          e2e diff against the stack machine and the binary-size measurement.
        - [~] Pair returns in real asm (`TRetPair` + `CallPair`) — the System V
          pair-return convention (tag in `rax`, payload in `rdx`) used for
          `Option`/`Result`. The callee epilogue moves (tag, payload) into
          rax/rdx via a parallel copy (shared `resolveRegMoves` with the param
          ABI); the caller (`CallPair`) captures both results, stashing the
          payload in `s0` across the caller-saved restores. Validated natively
          with hand-built SSA (a pair-returning callee summed by the caller,
          incl. both results live across an intervening call). Probing the
          *whole-program* `Option` path also surfaced a real **lift bug** (not
          the emitter): `match (opt) { Some(v) => v, None => k }` lifts to
          invalid SSA — the join block `ret`s a value not defined on the None
          path (missing phi), caught by `ssa.Verify`. The corpus differential
          now runs `ssa.Verify` on every lifted function and tracks verify
          failures as a distinct (lift-bug) category, asserting every *valid*
          function still emits. The match-`Option` join phi bug is now **fixed**
          in `ssa.LiftFromIR` (`mergeSlotsViaPhi`): a result slot left undefined
          on an unreachable predecessor edge (the impossible arm of an
          exhaustive match) used to make the merge give up and pick one arm's
          value; it now fills that edge's phi arg with an entry-block `const 0`
          undef (which dominates every block) and builds the phi. Corpus
          verified 24→25/25; guarded by `TestLiftMatchOptionJoinPhi`, and the
          wasmssa suite (the other lift consumer) stays green. End-to-end
          *execution* of `Option` additionally needs the IR's load/store
          **bit-width** carried through the lift (the boxes pack i32 fields at
          4-byte offsets, which the width-agnostic 8-byte memory path overruns)
          — a separate follow-up.
        - [x] Lift memory-width fix — **`Option`/`match` now runs whole-program**.
          The SSA gained a 4-byte `OpLoad32U`/`OpStore32` (mirroring the sub-word
          ops); `ssa.LiftFromIR` now width-switches `ir.OpLoad`/`OpStore` (and
          `OpMatchTag`): pointer-width (`Width == WidthPtr`/`64`) → the 8-byte
          `OpLoad`/`OpStore`, else the 4-byte i32 variant — so a box's i32 fields
          at 4-byte offsets no longer overrun. Wired through `Eval`, the
          `x86_64ssa` model + real-asm emitter (`mov r32`/`mov m32`), and
          **wasmssa** (`i32.load`/`i32.store` — where `OpLoad` was already 4-byte,
          so no wasm regression; confirmed by the wasm suites). `EmitProgram` now
          runs `match half(n) { Some(v) => v, None => k }` with the interpreter's
          result on both arms (Some=42, None=99) — the phi fix + this width fix
          together.
        - [~] Closure dispatch — **designed** in `docs/SSA-CLOSURE-DISPATCH.md`.
          Unify function-value representation to a `{fn, env}` cell (mirroring
          the native backend): `OpConstFunc` becomes a `{fn, env=0}` cell instead
          of a raw index, and `OpCallIndirect` uniformly derefs `fn`/`env` and
          appends `env` as the last arg (`env`-last confirmed for both
          zero-capture and capturing lambdas). Real-asm stores the code address
          and `call r11`s (register-indirect, assembler-supported — no dispatch
          table). Sequenced model → real-asm → wasm → whole-program. Note: the
          *simple* non-escaping case runs on dispatch alone, but capturing /
          dropped closures also emit `__fern_closure_drop` → need the RC-helper
          slice, shared with struct/array.
          - [x] Model slice — `OpCallIndirect` now treats `Args[0]` as a
            `{fn, env}` cell pointer: derefs `fn` (table index at +0) and `env`
            (+8) and calls `fn(args…, env)` (env last), in `Eval` + the
            `x86_64ssa` model. The raw-index `call_indirect` tests were rewritten
            to build the cell via `OpMakeClosure` and give dispatch targets an
            env param; validated `RunModuleTable == EvalInTable` incl. a
            runtime-selected closure (`OpSelect` between two cells). wasm
            untouched (it doesn't implement `OpCallIndirect`).
          - [x] Lift slice — `OpConstFunc` (a bare function value) now lifts to a
            zero-capture `OpMakeClosure` (a static `{fn_idx, env_ptr=0}` cell)
            instead of a raw `OpConstInt` index, so it derefs identically to a
            real closure through the model-slice `OpCallIndirect`. `fn_idx`
            resolves from the target name via the module table.
          - [x] Real-asm slice — `OpCallIndirect` lowers in `gas.go`: read
            `fn_idx`/`env` from the cell, index a `.rodata` function-address
            dispatch table (`__ssa_fn_table`, one `.quad` per function in module
            order — CallIndirect has no static `Callee`, so a table is required),
            stash the resolved target across the arg shuffle and `call` it
            register-indirect with `env` appended last. Validated by assembling +
            running natively (`TestAsmRunCallIndirect*`). This surfaced (and this
            slice also lands) the real-asm `OpSelect` lowering — a conditional
            branch over a unique label (materialize returns operands' own home
            registers, so no operand may be clobbered; a branch-free mask would
            need a second scratch), `TestAsmRunSelect`.
          - [x] Whole-program closure test (`TestProgramRunClosure`) — a lambda
            (and a bare named fn as a value) passed into a higher-order function
            and called indirectly runs the same result as the interpreter through
            the full pipeline. Non-escaping only; stored/capturing closures fail
            at assembly on `__fern_closure_drop`/`__fern_rc_is_unique` (the RC
            helpers), which pins the next sub-epic.
          Still ahead: the RC/runtime-helper migration — see
          **docs/SSA-RC-RUNTIME.md** for the design (rc-header allocator + the
          leaf-first helper port: `__fern_rc_is_unique` → `rc_inc`/`rc_dec` →
          `__fern_closure_drop`, then escaping-closure whole-program tests). The
          wasm `call_indirect` form and the flag + e2e diff follow.
- [~] Op-coverage broadening toward parity (each op validated against `Eval`
  in the model before it reaches real assembly):
  - [x] Direct integer calls + recursion — `ssa.EvalIn` (function-table eval),
    `x86_64ssa.EmitModule` / `RunModule` / the `Call` op. (Model only; the
    real-asm call ABI rides with the slice-3b parameter ABI.)
  - [x] Memory — `OpAlloc` / `OpLoad` / `OpStore` (full word) over a shared
    little-endian byte heap with a bump allocator + reserved null page, in both
    `ssa.Eval` and the `x86_64ssa` model (`Mem*` ops). The prerequisite for
    composite types. Validated: store/load roundtrip, heap shared across calls,
    out-of-bounds error. (Model only; floats and the real-asm allocator runtime
    are follow-ups.)
  - [x] Sub-word memory — `OpLoad8U/8S/16U/16S`, `OpStore8/16` (byte/halfword
    access with sign- or zero-extension; stores write only the low N bytes),
    in both `Eval` and the `x86_64ssa` model. Validated: byte signedness, a
    byte-array sum, halfword sign-extension, and store-width-preserves-high-
    bytes. The basis for string bytes and narrow array elements.
  - [x] Scalar unary completion — `OpNot` + the integer width conversions
    (`OpTrunc`, `OpExtendS/U`, `OpExtend8S/16S`) via a generic `UnOp`, in
    `Eval` + the model + **real assembly** (`Not` runs natively;
    `movsxd`/`movsx`/`movzx` for the rest). Driven by the measurement below —
    `OpNot` was the single most-frequent unhandled op.
  - [x] Strings — `OpConstString` (materialises the literal bytes on the model
    heap, returns a pointer) + `OpConstStringLen` (compile-time length via a
    const-string length map), in `Eval` + the model. The single biggest
    unhandled block in the measurement (~3668). Validated: length + byte reads,
    empty string, and a string passed across a call.
  - [x] Floats — `OpConstFloat`, `OpFAdd/FSub/FMul/FDiv/FNeg`, float compares,
    `OpFPromote/FDemote`, `OpIToFS/IToFU/FToIS/FToIU`, `OpLoadF/StoreF`, in
    `Eval` + the model. Floats live as their f64 bit pattern in the int64
    registers (like a hardware register), so `Run` and `Eval` agree bit-for-bit;
    f32 modelled via precision rounding. ~3500 occurrences. (`LoadF/StoreF`
    route through the 8-byte memory path.)
  - [ ] Composite types: structs, arrays, maps (mostly alloc+load/store, already
    covered post-lowering)
  - [x] Integer `div`/`rem` (`OpDiv/DivU/Rem/RemU`) in the model, mirroring
    `Eval` (div-by-zero → error). Real-asm `idiv` (rax/rdx pinning) stays
    deferred to the wiring slice.
  - [x] `OpEnumSentinel` — the shared static per-tag sentinel pointer (memoised
    by tag on the model heap; same tag → same pointer, tag stored at the
    pointer), in `Eval` + the model. ~472 occurrences.
  - [x] `OpCallPair` (two-result returns) + `TermRetPair` — the pair-return
    convention for Option/Result. `evalWith` / `runProg` now thread two return
    values `(tag, payload)`; `EvalIn` / `Run` / `RunModule` keep their
    single-value contract (returning the tag). The emitter delivers the tag into
    `s2` and the payload into `s3`, placing each result independently; the model
    recurses and writes both `Dst`/`Dst2`. Validated `RunModule == EvalIn` incl.
    both results kept live across an intervening (callee-clobbering) call and
    under spill-forcing register counts. ~132 occurrences.
  - [x] `OpCallIndirect` (function-index dispatch). A function value is an
    integer index into the module's ordered function list (as the backends model
    it — wasm `call_indirect` / the native closure-cell pool; `OpConstFunc`
    lifts to `OpConstInt`). `ssa.EvalInTable` / `x86_64ssa.RunModuleTable` take
    an ordered `table []string` (index → callee name) threaded through
    `evalWith` / `runProg`; `OpCallIndirect` reads `Args[0]` as the index,
    resolves `table[idx]`, and recurses (`Args[1..]` are the call args, single
    result). The emitter captures the index home (`IdxLoc`) + arg homes. Errors
    on an out-of-range index rather than dispatching wrong. Validated
    `RunModuleTable == EvalInTable` incl. a runtime-computed index (from a
    comparison) surviving to the call under spill-forcing register counts.
  - [x] `OpMakeClosure`/`OpMakeEnv` (closures). `OpMakeEnv` allocates an env
    block of the N captures (8-byte slots, capture `i` at offset `8*i`);
    `OpMakeClosure` additionally allocates a `{fn_idx, env_ptr}` cell (fn_idx =
    the target's index in the ordered function table, resolved from `Str`) and
    returns the cell pointer. Modelled in `Eval` + the model over the shared
    bump-allocator heap with byte-for-byte identical layout (a `storeCaptures`
    helper on each side), so `RunModuleTable == EvalInTable` holds. Validated by
    reading every field back (fn_idx + both captures via the env pointer) and
    combining them so any field mismatch shows, over spill-forcing register
    counts; unknown closure target errors. **This closes the call-tail** —
    `OpCallPair` / `OpCallIndirect` / closures are all modelled. Closure
    *dispatch* (a closure pointer flowing into a call — deref `{fn_idx,env}`,
    prepend env to args) is deferred: the SSA lift makes `OpConstFunc` a raw
    index while a closure is a pointer, so uniform dispatch likely wants a
    dedicated `OpCallClosure` rather than overloading `OpCallIndirect`; that
    resolves with the RC/Perceus + wiring work.
  - [x] `OpSelect` (ternary select) + the four `OpReinterpret*` bit-casts
    (`F32ToI32`/`I32ToF32`/`F64ToI64`/`I64ToF64`) in `Eval` + the model +
    emitter. Select is a `Select` inst (cond → then/else, three distinct scratch
    regs so spilled operands can't clobber). The reinterprets ride the existing
    `FConv` path: since floats live in the int64 slots AS their f64 bit pattern,
    the 64-bit reinterprets are the identity on the stored bits and the 32-bit
    ones round-trip through the f32 pattern. Validated `RunModule == Eval` incl.
    round-trips and a spill-forcing select. **This closes the per-function
    scalar op set** — `TestEmitRejectsUnsupported` now pins `OpInvalid` (the
    permanent zero-value sentinel) since every real op is handled.
  - [ ] RC inc/dec (Perceus ordering)

  **Coverage measurement (data-driven prioritisation).** Lifting the example
  corpus (33 programs, 11,497 functions) through `ir.LowerWith` →
  `ssa.LiftFromIR` and histogramming SSA op kinds: the lifter handled the whole
  corpus (0 failures), and the `x86_64ssa` emitter covered **92.5%** of op
  occurrences before this slice. So **wiring is gated on emitter op coverage,
  not lift coverage.** Top unhandled ops were: `not` (7107 — added here),
  `const_string` (3668), `extend_s` (1215 — added here), `const_float`/floats
  (~3500), `div`/`rem` (~1460), `enum_sentinel` (472), `trunc`/`extend_u` (320 —
  added here). Adding the scalar unary ops here lifts coverage to ~96%; strings
  + floats are the next big blocks.
- [ ] Phase 3 — default x86-64 to SSA; measure binary-size win
- [ ] Phase 4 — arm64 SSA emit + default
- [ ] Phase 5 — retire the stack-machine backends

## Emit-quality phase — results

After the whole-program coverage landed, a direct measurement showed the
SSA path was initially emitting **larger** `.text` than the stack machine
(fib 219%), because register allocation alone isn't enough — the emit was
over-saving and over-moving. Four correctness-preserving slices fixed that,
each diffed against the interpreter oracle:

- **EQ-1** call-clobber-aware caller-save — save only the caller-saved
  registers holding values live *across* a call, not all of them
  (`ssa.Allocation.LiveAcross`).
- **EQ-2** drop the redundant `movsxd` after an in-range `MovImm` (a
  `mov r64, imm32` already sign-extends).
- **EQ-3** pass call arguments by a parallel register move
  (`argMoveLines` / `resolveRegMoves`) instead of a push/pop stack
  round-trip.
- **EQ-4** compute each op directly into its result's register home
  (`coalesceDst` / `movReg`) instead of staging through the scratch `s2`
  and placing — the biggest lever.

`.text` size vs the stack machine, before → after the phase:

| program            | before | after |
|--------------------|--------|-------|
| arith (straight)   | 113%   | 77%   |
| loop               | 110%   | 89%   |
| fib (recursive)    | 219%   | 154%  |
| calls (many fns)   | 197%   | 175%  |
| **large mixed (100–200 fns)** | — | **84%** |

At codebase scale the SSA backend is a **stable ~84% of the stack
machine** — a ~16% code-size reduction that holds as the program grows, so
the binary-size premise of the epic is validated. Guarded by
`TestCodeSizeSmallerThanStackMachine` (fails if the SSA path regresses to
parity or worse). The residual call-heavy overhead (fib/calls) is the
call sequence itself; shrinking it further needs call-clobber-aware
*allocation* (prefer callee-saved registers for values live across calls),
a deeper allocator change, before the Phase-3 flag-gate + end-to-end
self-host binary measurement.
