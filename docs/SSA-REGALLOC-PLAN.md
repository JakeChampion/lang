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

- **SSA coverage parity.** `LiftFromIR` and the native emit path have to cover
  everything the native IR backends emit (strings, structs, arrays, maps,
  closures, **RC inc/dec**, runtime-helper calls, PIE) before the differential
  suite can pass. This was the bulk of the work, not the allocator itself, and
  on arm64 it is now **closed against both fernsmith corpora**: all 2048
  exit-byte and all 1024 printable seeds compile through
  `-target arm64-linux -backend ssa` and match the interpreter, with no skips
  left on either leg (`diffOracleSSAMinRunRatio` is 1.0 accordingly).

  The examples corpus reaches past what the generator writes, and it is now
  closed too: **all 281** of the 286 programs the flat backend can build also
  build under `-target arm64-linux -backend ssa` and behave identically —
  0 refused, 0 diverged, the other 5 rejected by the flat backend as well
  (`TestArm64SSABackendDifferential`, whose floor is the full 281 accordingly).

  **That corpus is not the whole language, and it excludes the program this
  epic is about.** `arm64SSADiffCorpus` skips `examples/self_host` outright
  (`arm64_ssa_differential_test.go`), and the self-hosted compiler does NOT
  compile under `-backend ssa`. Do not read "both corpora are clean" as
  "coverage is done" — see **Phase 3 measurement** below for what is actually
  blocking, which is a missing stack-argument ABI rather than any lowering gap.
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
        are emitted only when a program uses memory ops. The arena is 16 GiB
        based at 16 GiB, so every address it hands out has bits above 31 set and
        any arithmetic that narrows a pointer is wrong from the first
        allocation; `ssa.ResolveWidths` is what keeps the address path 64-bit
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
- [~] Phase 4 — arm64 SSA emit + default. The emit path ships as
  `-target arm64-linux -backend ssa` (`internal/codegen/arm64ssa`), not as a
  separate target, so the target descriptor and its E066 capability
  enforcement still apply. Both fernsmith corpora and the whole examples
  corpus sweep clean through it. The self-host does not build under it, and
  compiler-shaped input still costs ~115% of the flat backend where the corpus
  gets 45% — **Phase 3 measurement** below. The default flip is NOT close.
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
parity or worse). The residual call-heavy overhead (fib/calls) was the
call sequence itself, and both halves of it are now gone. Call-clobber-aware
*allocation* steers values live across a call into callee-saved registers
(`ssa.Target.CalleeSaved`, with both real-asm backends handing the allocator a
partition — `x86_64ssa` System V, `arm64ssa` AArch64), and a call result is
delivered straight into its home rather than through a capture scratch and a
staging register. What remains is memory traffic — and it is spilling rather
than anything the emit layer does, measured in **Phase 3 measurement** below.

## Phase 3 measurement — the size win, and what still costs

Measured 2026-08-26 on `-target arm64-linux`, comparing the R+E LOAD segment's
FileSiz (these are minimal static ELFs with no section headers, so that segment
is the code; total file size is 64 KiB-page-padding-dominated below ~128 KB and
is the wrong metric).

### Over the examples corpus: 55% smaller

All 281 corpus programs the flat backend can build, compiled both ways:

| | bytes |
|---|---|
| flat `.text`, all 281 | 24,347,696 |
| ssa `.text`, all 281 | 11,021,044 |
| **ssa / flat** | **45.3%** |

280 of 281 are individually smaller, and the ratio improves with program size —
the ten largest land at 10–31% of flat (`miniparse` 863,904 → 86,636, i.e.
10.0%). So 45.3% understates what a large program gets.

One program regresses: `bench/map_int`, 13,648 → 13,940 (102.1%), fixed helper
overhead over a body of a few KB.

### On compiler-shaped input: ~115%, and it still does not build

The corpus does not extrapolate. Compiler-shaped input is still on the far side
of a crossover:

| program | flat `.text` | ssa | ssa % |
|---|---|---|---|
| `miniparse` (largest corpus program) | 863,904 | 86,636 | 10.0% |
| `checker_modload_run.fern` | 8,018,492 | ~9,259,012 | ~115% |

The SSA figure there is **inferred** — instruction count × 4 off an
instrumented emitter — because the program does not link; the proxy reads
about 0.2% low on `miniparse`, which does. `examples/self_host/fern.fern`
cannot be measured even that way: emit stops at the parameter-count check
before any assembly exists.

Two independent blockers, both pre-existing:

- **No stack-argument ABI.** `argRegCount = 8` in `internal/codegen/arm64ssa/gas.go`
  — register arguments only. 14 self-host functions exceed 8 params and 43 call
  sites exceed 8 args; the build stops at the first.
- **No imm19 veneers.** `internal/native/arm64/veneer.go` plants veneers for
  `b`/`bl` (imm26, ±128 MB) but not for conditional branches (imm19, ±1 MiB).
  `checker_modload_run` clears emit and dies in the assembler on a conditional
  branch spanning far more instructions than the signed 19-bit range holds. So
  fixing the ABI alone does not get the self-host to link.

**Re-measure against the same base.** The figure above is not comparable with
the one this section carried before: `main` itself grew this program by roughly
65,000 instructions between the two measurements. Compare a codegen change only
against a build of the base it sits on — the cross-`main` delta reversed the
sign of one measured here before that was caught.

### What the remaining gap is made of

`checker_modload_run`, all three built from the same `main`:

| | instructions | load/store | `mov` |
|---|---|---|---|
| flat | 2,010,751 | 859,653 (42.8%) | 357,069 (17.8%) |
| ssa | 2,340,784 | 1,358,266 (58.0%) | 421,995 (18.0%) |
| ssa, register-resident phi resolution | 2,314,753 | 1,323,354 (57.2%) | 430,876 (18.6%) |

Copies are no longer the story: call-result coalescing brought `mov` to within
a point of the flat backend's share and it has stayed there. What is left is
memory traffic — 463,701 more load/store than flat, against a total excess of
304,002 instructions, with everything else running below flat.

Resolving phi copies in registers rather than through temp slots trades 34,912
memory ops for 8,881 moves: 26,031 instructions, about 1%. Real, and much
smaller than the shape suggested.

**That correction matters more than the win.** This section previously called
phi resolution "the whole remaining gap", generalising from the pattern in a
ten-line loop without measuring how much of the total it was. It is about 3% of
the stack traffic.

### The stack traffic is the caller-save area

Measured directly, and this replaces an attribution this section got wrong
twice. **93% of load/store is sp-relative**, so it is stack traffic rather than
data access. Of that stack traffic, counting the stores and loads in the window
around each call:

| | ops | share |
|---|---|---|
| caller-save around calls | 1,127,496 | **92%** |
| everything else (spill slots, phi temps, prologue) | 104,409 | 8% |

6.4 stores and loads per call, over 175,765 calls. Spilling is **not** the
story: across the same 1065 functions the allocator homes 394,251 values and
spills **3,649 of them, 0.9%**, which cannot account for a million memory ops.
The 104,409 non-call ops are the right order for that many spilled values plus
the phi temps and prologues.

An earlier revision of this section said the opposite — that 83% of stack
traffic was spilled operands and the caller-save area only 16%. That came from
a text heuristic that only counted a `str` IMMEDIATELY before a `bl`; the
argument moves sit in between, so it caught roughly one save per call instead
of eight, and the remainder was assigned to spilling without being checked.
Attributing by exclusion is how both wrong answers happened. Count the thing
you are naming.

**Why this reframes the ordering.** The lever is what crosses calls, not what
spills. Two consequences, both measured:

- It is why the callee-saved partition (#7550) was the largest single win in
  the epic: it moves call-crossing values into registers the callee restores,
  removing their per-call save entirely. There are only 10 such registers, so
  everything beyond that pays at every call it spans.
- It is why hole-aware live ranges made compiler-shaped input **worse**, not
  better (#7577): keeping more values in registers puts more caller-saved
  registers live across each call, so the save set grows — 6.41 ops per call on
  main against 8.06 with the refinement, +289,166 ops, which is 79% of that
  change's +366,057 instruction regression. Reducing spills and reducing
  caller-save traffic pull against each other in call-dense code.

**Ranking the callee-saved registers by call count: measured, ~0.** `crosses`
in `ssa.LinearScan` is a boolean, and the ten callee-saved registers go to
whoever linear scan reaches first — so a value spanning one call can hold one
while a value spanning forty pays a store and a load at each of its forty.
Ranking by span looked like the obvious next lever. It is not:

| `checker_modload_run` | instructions | call-save ops | per call |
|---|---|---|---|
| main | 2,311,859 | 1,127,158 | 6.41 |
| ranked by calls spanned | 2,311,978 | 1,129,263 | 6.43 |

+0.005%. Two shapes were measured. Gating the preference — mark only the
top-ranked crossers, leave the rest to the caller-saved half — is much worse
(+43% instructions): the preference is a steer, not a cap, so denying it pushes
a call-crosser into a caller-saved register even when a callee-saved one is
free. Swapping instead — when the callee-saved registers are all busy, the
newcomer takes one from the active holder crossing fewest calls, and that holder
takes the newcomer's caller-saved register — is sound and does nothing.

The reason is in the quantity. How many values are live across a call is a
liveness fact; allocation only chooses which register class each one lands in.
At 6.41 caller-saved values live across the average call plus ten callee-saved
registers, the average call has around 16 values live across it and 10 places to
put them for free. Rearranging which six spill is not where the ops are.

**The ops were in how each save is spelled.** The call-save area went out one
`str`/`ldr` per register into consecutive 8-byte slots at `8*(callSaveBase+k)`
— precisely the shape `stp`/`ldp` addresses. Emitting the pair form is a
peephole over an already-correct sequence: allocation is untouched, the frame
layout is untouched, every register still lands in the slot it always did.

| `checker_modload_run` | instructions | sp-relative mem | call-save ops | per call |
|---|---|---|---|---|
| before | 2,311,859 | 1,231,903 | 1,127,158 | 6.41 |
| paired | 2,066,651 | 986,695 | 870,157 | 4.95 |

**−245,208 instructions, −10.6%**, and −13.4% across the 281-program examples
corpus (45.3% → 39.2% of the flat backend's `.text`) with no program in it
larger than before. The largest win in this epic after the callee-saved
partition itself, and unlike that one it cost no allocator complexity.

Not the halving a naive count suggests: the average call saves about three
registers, so pairing removes one instruction per side rather than half of six.
The saving is one per pair, not one per op.

The pair forms scale a signed 7-bit offset by 8, so a slot past `[sp, #504]`
falls back to the single form. `PairLoadStore` in
`internal/native/arm64/arm64.go` masked its `imm7` with no range check, so an
out-of-range offset would have encoded silently as a valid instruction against
the wrong slot; `asmPair` rejects it now.

### The self-host compiler links, and the sizes match

With the pairing in, `examples/self_host/checker_modload_run.fern` compiles,
links and runs under `-backend ssa` — the first time any self-host module has.
It was the last thing the epic was for.

| `checker_modload_run` `.text` | bytes | of flat |
|---|---|---|
| flat backend | 8,255,292 | — |
| SSA backend | 8,278,404 | **100.3%** |

163.7% at the start of the work, 112% before pairing. Parity on the hardest
input in the tree, and it behaves: the SSA build's output is byte-identical to
the flat build's on every self-host module it type-checks, its own `lexer.fern`
and `parser.fern` included.

**What was in the way was branch reach, not size.** `b.cond` / `cbz` / `cbnz`
carry a signed 19-bit instruction offset (±1 MB) and `tbz` / `tbnz` a 14-bit one
(±32 KB), against `b` / `bl`'s 26 bits. One self-host function outgrew imm19, so
a branch between two of its own blocks stopped encoding.

A veneer cannot fix that: the conditional itself is what will not reach, so the
fix has to shorten the conditional rather than what it points at. Invert it and
hop over an unconditional branch, which reaches 128 MB on its own and can take a
veneer past that:

```
b.eq far        ->      b.ne .Lskip
                        b far
                    .Lskip:
```

One instruction, only at the branches that need it. `internal/native/arm64`'s
island machinery is shared: `splice` takes any run of instructions to insert at
an index and does the label/fixup/debug-row remap, and `fitBranches` (was
`insertVeneers`) relaxes before it veneers each round, since relaxation creates
`b` the veneer pass may then have to cover and a veneer never creates a
conditional.

### Arguments past the eight the PCS passes in registers

`argRegCount` was a ceiling, not just a count: emit refused a function with more
than 8 parameters or a call with more than 8 arguments. `checker_modload_run`
stayed inside it, which is why it was not what blocked the link — but 23
self-host functions take more than 8 parameters, so the modules holding them
could not emit at all.

They go on the stack now, as the AArch64 PCS says. The wrinkle is that `sp` does
not move for a body's lifetime here, so a caller cannot push arguments at the
call. The outgoing-argument area instead lives at the **bottom** of the caller's
frame: a callee reads its stack arguments from the `sp` it is entered with,
which is the caller's own `sp`, so what the caller writes at `[sp, #0]` upward
is exactly what the callee finds, and everything else the caller owns sits above
it.

Every offset the emitter produces now goes through one `frameLayout` value
rather than being computed at each `str`/`ldr` site, which is what makes the
shift a change in one place instead of seventeen.

Two orderings are load-bearing and both are the opposite of the obvious one:

- **Outgoing arguments go out first**, before the register half. The register
  half writes x0..x7, and a stack argument's home may well be sitting in one of
  them.
- **Incoming stack parameters are read last**, after both the slot-homed stores
  and the register parallel-copy. A stack parameter's home can be one of the
  argument registers those two steps still read.

A function whose arguments all fit in registers reserves no outgoing area, so
nothing that does not need this pays for it.

Getting past the ceiling surfaced the next one immediately, on
`wasm_modload_run`: `sub sp, sp, #20304`. The ADD/SUB immediate is 12 bits with
an optional 12-bit left shift, so a frame past 4095 bytes takes two
instructions — the 4096-multiple part and the remainder, both of which encode
because the frame is 16-aligned. A latent bug, not a new one: the arm64
assembler refused it correctly, and nothing had a frame that large until a
module this size could reach emit.

### The next one: literal pools, at 26 MB of .text

Past the frame fix, `wasm_modload_run` reaches the assembler and stops there:

```
arm64: ldr-literal at insn 264553 is 26676608 bytes from its pool
       — outside the ±1 MB imm19 range (missing .ltorg?)
```

`ldr Xt, =constant` addresses its pool with the same signed 19-bit offset the
conditional branches use, and the emitter flushes literals once. At 26 MB of
`.text` that is nowhere near enough: pools have to be flushed periodically, each
one hopped over, with every `ldr`-literal kept within reach of the nearest —
which is the island placement `internal/native/arm64` already does for veneers,
against a different limit. That machinery is now `splice`, so this is an
addition to an existing pass rather than a new one.

Note what the sequence has been: three separate ceilings, each invisible until
the one before it was lifted, none of them about register allocation. Sizing the
work by what is currently *reported* would have missed all three.

### The interval approximation, and what sizing it missed

Sized before building it, by comparing each function's true maximum of
simultaneously live values against the demand its single hole-free intervals
imply. Over `checker_modload_run`'s 1065 live functions, against the 22
allocatable registers:

| | functions | excess demand |
|---|---|---|
| spilling as intervals are built today | 158 | 3020 |
| of those, would not spill at all under exact liveness | 36 | — |
| still spilling under exact liveness | 122 | 1837 |

So **39% of the excess register demand is an artifact of the approximation**,
and 61% is real. That number is correct as measured and was still the wrong
thing to measure.

#7577 built the change it justified, and the change **worked**: spilled values
fell from 3,649 to 2,874, a 21% reduction, exactly what hole-aware interference
is supposed to buy. The program still got **16% bigger**, because spilling was
0.9% of values and 8% of the stack traffic while the caller-save area it
inflated is 92%.

That is the lesson worth keeping from this whole line of work. An optimisation
delivering precisely what it promises can still lose, and the sizing that
justified it can be arithmetically correct and still measure the wrong
quantity. Before building, establish what share of the total the target
actually is — not just how much of the target is addressable.

Both halves of that are worth knowing. Counting only the functions the change
would fix outright says 36 of 1065, which reads like a rounding error and is
the wrong measure: most of the win is in functions that keep spilling but spill
less. Excess demand is not proportional to memory ops either — a spilled
value's cost is its use count — so this sizes the lever without predicting the
byte count. Measure that against a base-matched build, not from this table.

### Two divergences the differential lane cannot see

`arm64SSADiffCompare` compares exit status, signal and **stdout** — never
stderr. Both of these are inside the covered subset that `docs/SSA-DECISION.md`
holds to byte-identical behaviour:

- **Aborts are silent.** An out-of-range index exits 134 under both, but flat
  writes `fern: array index out of range` plus a backtrace and the SSA build
  writes zero bytes. Not a missing backtrace — no cause line at all.
- **`examples/proposals/trie.fern` allocates 6.7–32× more** under SSA on
  struct-element arrays updated through `.append` / `.with`, where the
  unique-reference in-place update appears to be lost. Same values; only the
  bump high-water mark differs, and it prints to stderr.
