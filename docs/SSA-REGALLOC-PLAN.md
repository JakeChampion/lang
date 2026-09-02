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

### Literal pools, at 26 MB of .text

Past the frame fix, `wasm_modload_run` reached the assembler and stopped there:

```
arm64: ldr-literal at insn 264553 is 26676608 bytes from its pool
       — outside the ±1 MB imm19 range (missing .ltorg?)
```

`ldr Xt, =constant` addresses its pool with the same signed 19-bit offset the
conditional branches use, and literals were flushed once, at the end. Flushing
per function does not fix it either: `parser__parse_stmt_at` is **2.97 MB of
code on its own**, three times the reach.

So the pool goes where the loads are. A far load's value is re-homed into an
island spliced in within reach, headed by a `b` that hops over it — the same
placement veneers use against a different limit, so it shares their machinery.
The original entry stays put: a literal is 8 bytes, and duplicating one is
cheaper than the bookkeeping to move it. Islands are anchored at even indices
and sized even, which is what keeps a wide entry 8-byte aligned wherever it
lands.

**`wasm_modload_run` compiles, links and runs under `-backend ssa` with this** —
and here the SSA backend is not at parity but *ahead*:

| `wasm_modload_run` `.text` | bytes | of flat |
|---|---|---|
| flat backend | 31,711,628 | — |
| SSA backend | 27,735,580 | **87.5%** |

Its output matches the flat build's exactly, on `-per-module-count` and
`-per-module-manifest` over real modules, not just on the usage line.

### The fifth is not a limit at all: a helper nobody emitted

Sweeping the 47 self-host drivers under `-backend ssa` past the pool fix,
`asm_ir_run` stops on something of a different kind:

```
arm64: branch to undefined label "fn___fern_rc_underflow_count"
```

`__rc_underflow_count()` is the Phase 3 over-release probe. The flat backend
implements it and its detector; this one implemented neither, and
`referencedRuntimeHelpers` **skipped the call silently** — a callee with no
entry in `runtimeHelperEmitters` is dropped, which is right for a user function
and was wrong for a helper the table had never heard of. The call went out with
nothing behind it and died in the assembler half an hour later, on a mangled
label that says nothing about which backend owed it.

Both halves ship: `__fern_rc_dec` counts an over-release into a `.bss` word,
and `__fern_rc_underflow_count` reads it back. A probe wired to storage nothing
writes would be worse than the link error — every test asserting the count is
zero would pass while detecting nothing.

And the silent skip is now an emit-time error naming the helper. It is the same
condition the assembler already checks; the point is to fail where a coverage
gap reads as one.

### The sixth, named by the fifth's diagnostic

The new emit-time check earned itself back on the very next driver.
`asm_modload_run` stops with:

```
arm64ssa: 3 call target(s) the module never defines — a runtime helper this
backend does not emit: fn_proc_exec, fn_proc_fork, fn_proc_waitpid
```

Three lines instead of a mangled label out of the assembler forty minutes
later, and the fix reads straight off it: `asm_modload_run` spawns per-module
workers, and the flat backend had the subprocess trio while this one did not.

Ported: `proc_fork` (arm64 Linux has no bare `fork(2)` — it is
`clone(SIGCHLD, 0, 0, 0, 0)`), `proc_waitpid` (a blocking `wait4` plus the
shell's status decode, so a bounds-trapped worker surfaces as 134 rather than
as a raw status word), and `proc_exec` (`execve`, building the NUL-terminated
`argv` the kernel wants from this backend's length-prefixed strings). They also
pull in the `envp` snapshot `_start` takes, so a program that only ever spawns
still hands its child an environment.

One trap worth recording: these arrive at codegen under their **bare** builtin
names — `proc_fork`, not `__fern_proc_fork`, which is the flat backend's
spelling. Keying the table the other way emits three helpers nobody calls and
leaves the three calls dangling, which is what the first attempt did.

Note what the sequence has been: six ceilings — arguments, frame size, branch
reach, literal reach, a missing detector, a missing syscall trio — each
invisible until the one before it was lifted, none of them about register
allocation. Sizing the work by what the compiler was *reporting* at any point
would have missed all six.

### Every self-host driver compiles

**47 of 47 `examples/self_host/*_run.fern` build under `-backend ssa`.** That is
the whole tree of self-host entry points: the asm and wasm emitters, the module
loaders, the checker, the interpreter, the IR lowerer, `ferndoc`, the capability
and platform probes.

Compiling every driver is what found all six ceilings, and it is the only thing
that could have. Each one sat behind the one before it, so no single failure
ever named more than the next step — reading the compiler's current error and
sizing the work from it would have stopped after the first.

Two sizes, both against the flat backend and both byte-identical to it on real
input:

| `.text` | flat | SSA | |
|---|---|---|---|
| `checker_modload_run` | 8,255,292 | 8,278,404 | 100.3% |
| `wasm_modload_run` | 31,711,628 | 27,735,580 | **87.5%** |

What this does not claim: the sweep is a *compile* gate. It proves every driver
emits, assembles and links, and two of them were run against the flat build over
real modules — `checker_modload_run` byte-identical across all 94 self-host
modules, `wasm_modload_run` on `-per-module-count` and `-per-module-manifest`.
Running the other 45 end-to-end is the next increment, and it is where a
miscompile that links cleanly would show up.

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

### The epic optimised size and never once measured speed

Every measurement above this line counts bytes. The corpus went from 163.7% of
flat's `.text` to under 90%, and nothing in that record says whether the smaller
code runs faster, slower, or the same.

It ran slower. Best-of-7 over `examples/bench/*.fern` under qemu, both backends
built by the same compiler, with a measured 3 ms process-start floor subtracted:

| bench | flat | SSA before | SSA after |
|---|---|---|---|
| `string_build` | 194 ms | 463 ms (2.39×) | 174 ms (0.88×) |
| `sort_ints` | 68 ms | 141 ms (2.07×) | 96 ms (1.41×) |
| `string_slice` | 103 ms | 108 ms (1.02×) | 77 ms (0.75×) |
| `array_append` | 43 ms | 43 ms (1.00×) | 31 ms (0.74×) |
| `string_scan` | 89 ms | 136 ms (1.53×) | 56 ms (0.63×) |

The cause was one decision repeated eight times. Each SSA runtime helper that
copies bytes — `__str_concat`, `__str_slice`, `__fern_arr_push_grow`,
`__fern_arr_cow_inplace`, `string_from_bytes_unchecked`, `strbuf_append`,
`strbuf_take`, `__memcpy` — open-coded its own copy loop at one byte per
iteration, five instructions a byte, to stay a leaf. Flat routes all of them
through a size-classed `__fern_memcpy` moving 32 bytes an iteration. They all
call `__ssa_bcopy` now: 16 bytes per iteration, one 8-byte step, a tail of at
most 7. It clobbers only x0–x2 and x16/x17, so its callers keep their live
values and stack nothing but x30 — the arrangement `__ssa_heap_guard` already
used, which is why "so it is a leaf" was stale in those comments before this
change touched them.

It costs **72 bytes**, once, in any program that copies: the routine itself. The
call sites came out even — a three-instruction call replacing a nine-instruction
loop.

`__str_eq` was the same defect one helper over, and `string_scan` — a needle
compared against every entry of a string array, which the benchmark's own
comment calls the self-hosted compiler's dominant shape — was paying six
instructions a byte for it. Equality needs no first-difference position, so a
whole word settles in one `cmp`: 8 bytes an iteration, then 4, then a tail of at
most 3. That is 136 ms → **59 ms**, past flat's 89 ms, which compares 4 bytes at
a time. `__str_ord` still runs byte-grain and is left alone: ordering has to
name the first differing byte, so a word compare there needs a `rev` and a
`clz`, and nothing measured makes it hot.

These are ~100 ms runs under emulation: best-of-7 makes the ranking trustworthy,
not the third digit.

### Where that leaves phase 4

Phase 4 of #4112 is "arm64 SSA emit **+ default**". The flip needs three things,
and after the two helper fixes above the evidence stands like this.

**Size — settled.** SSA is smaller on all 17 benchmarks, from 24.7% of flat
(`call_overhead`) to 91.1% (`map_int`).

**Correctness — settled.** The corpus run differential is 286/0 on `asm_run` and
285/0/1 on `interp_run`, both drivers built from one compiler.

**Speed — the open blocker.** Ten of seventeen are at or faster than flat; seven
are slower:

| slower | ratio | faster | ratio |
|---|---|---|---|
| `sort_ints` | 1.49× | `int_loop` | 0.44× |
| `map_int` | 1.28× | `string_scan` | 0.62× |
| `map_string` | 1.23× | `closure_call` | 0.70× |
| `tokenize` | 1.22× | `string_slice` | 0.73× |
| `sort_inplace` | 1.15× | `array_append` | 0.76× |
| `enum_match` | 1.15× | `struct_drop` | 0.76× |
| `utf8_ingest_validated` | 1.11× | `string_build` | 0.89× |

Geometric mean is about 0.92× — SSA is ~8% faster on average and much smaller —
but an average is the wrong test for a default. Flipping it ships a 20–49%
slowdown to anyone whose workload looks like `cmp.sort` or `core/map`.

The seven share a shape, and it is not the one the helper fixes addressed:
`cmp.sort`, `core/map`, the tokenizer's scan loop. All are **compiled Fern with
hot loops over arrays and maps**, so the cost is in generated code quality —
register allocation, spill placement, bounds-check and RC traffic in a loop body
— not in a hand-written runtime helper. That is the harder class, and the one
the allocator work in this plan was originally about.

So phase 4 is blocked on codegen quality in loop bodies, with seven named
reproducers to work against. Nothing else about it is outstanding.

### What the map-shaped reproducers were actually paying

Two of the named reproducers were not loop-body codegen at all. Both were
per-call overheads that the map benchmarks execute on every lookup, and both are
gone.

**A bare function name allocated a closure cell every time it was evaluated.**
`__map_lookup` picks a hash and an eq function by key kind and hands them to
`__map_lookup_keyed`. `OpConstFunc` lifts to a capture-free `OpMakeClosure`, and
both SSA backends bump-allocated that `{fn_idx, env=0, drop_idx=0, 0}` cell —
40 bytes and a heap-guard call, twice per lookup, for four words that are
compile-time constants. They are one immortal `.rodata` cell per target now, the
same shape the string literals and enum sentinels already had, materialised with
an `adrp`/`add` pair. The lift slice always described this cell as static; only
the emit was not.

**The raw pokes were out-of-line one-instruction functions.** `__load_i32` /
`__load_u8` / `__load_i64` / `__load_ptr` / `__store_i32` / `__store_i64` /
`__store_ptr` / `__ptr_width` / `__str_len` are what `core/map.fern` reaches its
untyped kv buffer through, several per probe. arm64ssa emitted them as leaf
helpers and called them; x86_64ssa had no emitter for them at all, so a program
using one was refused. Both inline them at the call site now, like the array
index, and the arm64 helper bodies are deleted. The cost was never the `bl` —
it is that a call makes the allocator save every caller-saved register holding a
value live across it, which the **stack traffic is the caller-save area**
section above measures at 92% of this backend's stack traffic.

Ratio to the flat backend, best-of-5 under qemu-aarch64, and peak RSS:

| bench | before | after | RSS before | RSS after | flat RSS |
|---|---|---|---|---|---|
| `map_int` | 3.25× | **1.61×** | 20,320 KB | 10,172 KB | 10,300 KB |
| `map_string` | 2.25× | **1.30×** | 20,696 KB | 10,136 KB | 10,168 KB |
| `map_probe_chain` | 2.58× | **1.76×** | 119,636 KB | 15,700 KB | 15,828 KB |
| `utf8_ingest_unchecked` | 1.31× | 1.07× | | | |
| `utf8_ingest_validated` | 1.24× | 1.05× | | | |
| `tokenize` | 1.13× | 0.97× | | | |
| `string_slice` | 0.78× | 0.68× | | | |

No program in the corpus is slower than before, and every exit code still
matches the flat build's. The memory column is the static cell: the allocation
those benchmarks were doing per lookup was all of their heap growth, so their
peak RSS now sits on the flat backend's.

Still slower than flat: `ordmap_insert` 2.31×, `pvec_with` 2.03×, `pmap_insert`
1.96×, `map_probe_chain` 1.76×, `map_int` 1.61×, `enum_match` 1.13×,
`map_string` 1.30×, `call_overhead` 1.26×. The three persistent-collection rows
barely moved, so whatever they pay is a third thing, not these two.

### What the persistent-collection rows pay, measured but not yet fixed

`ordmap_insert` 2.31x, `pvec_with` 2.03x and `pmap_insert` 1.96x barely moved
for either fix above, and counting call sites in their emitted modules says
why — it is the RC helpers, reached by a call each:

| callee | `ordmap_insert` | `pvec_with` | `pmap_insert` |
|---|---|---|---|
| `__fern_box_free` | 165 | 202 | 196 |
| `__fern_rc_dec` | 84 | 86 | 81 |
| `__fern_rc_is_unique` | 84 | 76 | 70 |
| `__fern_rc_inc` | 40 | 55 | 32 |

The flat backend inlines all three of the rc primitives (`emitRcInc` /
`emitRcDec` / `rcop` in `internal/codegen/arm64`) — its hot pmap functions
contain zero calls to `__fern_rc_is_unique`. Each is a guard chain of about six
instructions, so this is the same trade the raw pokes just took, one size up:
the caller-saves around the call cost more than the work.

`__fern_box_free` is the sharper one. On this backend it is a bare `ret` — the
heap does not reclaim — so those 165-202 sites were a `bl` and a full caller-save
around a function that does nothing.

### A call saves what its callee can disturb, not what is live

Inlining the rc primitives, the way the flat backend does, was the obvious
answer and it is not the one taken: it trades code size for the win, and the
epic is about code size. The cost is not in the helper bodies — six instructions
each — it is in the caller-saves the allocator plants around a call whose callee
it knows nothing about.

For this file's own helpers, it can know. `helperClobbers` derives from each
emitted body which caller-saved registers a call to it can leave changed, and
`callLines` keeps only those (plus the argument registers the caller's own
parallel move writes on the way in). The derivation is over-approximated twice:
a register a body so much as MENTIONS counts, and a branch to anything that is
not another helper — a compiled module function, which uses the whole register
file — counts as every one. An indirect branch has no body to read, so it counts
as every one too. The callee-saved half never enters it: a helper that touches
one already has to save and restore it (`TestRuntimeHelpersPreserveCalleeSaved`).

The rc primitives name x0 and x1 and nothing else, so a value homed in x2..x11
and live across an inc, a dec or an is_unique stops being stored and reloaded at
each of them; `__fern_box_free` names nothing at all, so those 165-202 sites now
cost the `bl` alone.

Both directions move, which is what distinguishes this from inlining. Ratio to
the flat backend, best of 7 under qemu-aarch64 on an idle container:

| bench | before | after | `.text` |
|---|---|---|---|
| `map_probe_chain` | 1.91x | **1.56x** | 99.35% |
| `map_int` | 1.94x | **1.65x** | 99.34% |
| `pvec_with` | 2.09x | **1.87x** | 92.99% |
| `map_string` | 1.45x | **1.40x** | 99.35% |
| `enum_match` | 1.15x | 1.10x | 100.00% |
| `sort_ints` | 1.08x | 1.03x | 96.93% |
| `ordmap_insert` | 2.34x | 2.28x | 92.58% |
| `pmap_insert` | 1.90x | **2.02x** | 93.28% |

`.text` over fifteen benchmarks is 95.79% of what it was, and the emitted stack
traffic in `ordmap_insert`'s module falls 25% (1539 sp-relative loads/stores to
1157).

`pmap_insert` is the one row that goes the wrong way, and it is not noise:
best-of-15 puts it at 0.588s before and 0.627s after, +4% to +7% depending on
the statistic, in a program whose `.text` is 6.7% SMALLER and whose exit code is
unchanged. Bisected to the narrowing itself rather than the frame-slot half of
it. No mechanism is established — fewer instructions running slower points at
code layout under emulation rather than at the work done — and it is reported
rather than explained. Everything else in the corpus improves or holds.

The narrowing does less than the call counts suggest because the allocator
already steers call-crossing values into the callee-saved half (#7550) — where
that succeeds there was no save to remove. It is the values it could not fit
there, in the functions with the most live at once, that were paying.

Which is why compiler-shaped input gains most of all. `checker_modload_run`'s
`.text` falls from 2,749,016 to 2,423,504 bytes — **88.2%**, against 95.8% over
the benchmark set — and its output stays byte-identical to the previous build's
on every self-host module tried. That is the largest single size win in this
epic since the callee-saved partition, and like the `stp`/`ldp` pairing it cost
no allocator complexity: allocation is untouched, only the set of registers a
call bothers to save.

### A call into a bare `ret`

`__fern_box_free` and `__free` are do-nothing helpers on this backend — the
heap does not reclaim, so both bodies are a single `ret`. Compiled code called
them anyway: 165 sites in `ordmap_insert`, 196 in `pmap_insert`, each with its
argument setup, on the path every drop takes.

A call to a helper whose emitted body is exactly a `ret` is the identity on its
first argument (the AArch64 PCS passes and returns in x0), so it lowers to that
move, and usually to nothing once the move is a self-move. The set is derived
from the emitted text rather than listed, which is what makes it temporary in
the right direction: when the `RC-4+` freelist slice gives those bodies work,
the derivation stops matching and the call sites come back on their own.

Helper-to-helper branches are left alone — `__fern_closure_drop` tail-branches
to `__fern_box_free` and the body has to stay for it.

| bench | before | after | `.text` |
|---|---|---|---|
| `pmap_insert` | 1.89x | **1.69x** | 87.3% |
| `ordmap_insert` | 2.06x | **1.99x** | 85.9% |
| `pvec_with` | 1.83x | 1.84x | 86.8% |
| `struct_drop` | 0.76x | 0.73x | 99.7% |

The size number is the one to read: **13-14% of `.text` off the three
persistent-collection programs**, where a drop-heavy body is most of the code.
The time moves are small — best-of-15 gives `ordmap_insert` 0.625s to 0.605s
and `pmap_insert` 0.330s to 0.295s, `pvec_with` unchanged within noise — which
is the expected shape: the elided instructions were cheap, there were simply a
great many of them. Programs that never drop a box are byte-identical.

### Reclamation is a memory fix, not a speed fix

The SSA heap never frees (`docs/SSA-RC-RUNTIME.md`), and the obvious reading is
that the slow benchmarks are slow because their live set grows without bound and
locality collapses. That reading is wrong, and the control experiment is cheap:
build the flat backend with its freelist POP disabled — same IR, same frees,
same everything else, nothing reused.

| bench | flat | flat, no reuse | SSA | flat RSS | no-reuse RSS | SSA RSS |
|---|---|---|---|---|---|---|
| `map_probe_chain` | 0.672s | 0.674s | 1.898s | 15,960 KB | 15,832 KB | 119,768 KB |
| `map_int` | 0.060s | 0.082s | 0.215s | 10,172 KB | 10,172 KB | 20,308 KB |
| `ordmap_insert` | 0.529s | 0.553s | 1.176s | 10,172 KB | 54,240 KB | 62,560 KB |
| `pvec_with` | 0.343s | 0.380s | 0.722s | 10,172 KB | 27,376 KB | 28,644 KB |
| `pmap_insert` | 0.316s | 0.351s | 0.624s | 10,204 KB | 20,076 KB | 22,628 KB |

Turning reuse off reproduces the SSA backend's **memory** profile almost exactly
— row for row, which is what confirms that the SSA path allocates the same
volume the flat one does rather than more — and costs **3–15% of the time**,
against the 2–3.5× being explained. So the freelist slice (`RC-4+`) is worth
building for what it is, bounded memory in a long-running program, and nobody
should expect the benchmark ratios to move when it lands.

(`map_probe_chain` is the one row where no-reuse does not reproduce SSA's
memory: 15.8 MB against 119.6 MB. That gap was the closure cell above, and it
is closed.)

The lesson is the one this epic keeps relearning in a new costume. Six ceilings
were found by lifting the previous one, none of them the register allocator; the
seventh was found by measuring a quantity nobody had measured. `.text` size was
picked as the proxy at the start and never revisited, and a 2.4× slowdown sat
inside a corpus that was passing every gate.

### The run differential, and the miscompile the fixpoint could not see

Every driver result above this line is a COMPILE result: 47/47 link, sizes match.
That proves link-ability, not behaviour. The run differential feeds each of the
286 corpus programs to the flat-built and SSA-built driver and compares stdout
and exit status.

`interp_run` came back clean — 285 match, 0 differ, 1 skip. `asm_run` did not:
283 match, 3 differ. Two of those three are this bug; the third was an artifact
of the harness (below). On the real ones the SSA-built compiler emitted

```
movq $4294967295, %rax      # SSA build
movq $-1, %rax              # flat build
```

which are different encodings holding different values — the first zero-extends
to `0x00000000FFFFFFFF`, the second sign-extends to `0xFFFFFFFFFFFFFFFF`. Both
driver binaries are individually deterministic across repeated runs, so this was
a miscompile, not nondeterminism. A three-line program reproduces it.

**Cause.** `ssa.ResolveWidths` marks values that hold a machine address, so the
backend skips the `sxtw` that would corrupt a pointer above `0x7fffffff`. The
marking is deliberately conservative. What broke is that the guess did not stay
inside the function that made it: `mark` overwrote a callee's **declared**
`ParamAddrs` entry, which then marked every OTHER caller's argument at that
position. `Addr` stamps `Width 64`, which is exactly how `memLoadSeq` is told to
skip its `maskFix`, so a negative i32 loaded through one of those stayed
zero-extended — and arm64ssa compares at 64 bits, so every signed test on it read
positive. `util.i32_to_string(-1)` rendered `"4294967295"`.

Which value first seeded each cascade is not established. `ptrA - ptrB` is an
integer — a length, an offset — and the `OpSub` case's own comment says so while
the code marks it as an address whenever `Args[0]` is one; that is a real
over-approximation and it is what the regression test constructs. It is not the
seed here, though: instrumenting the pass to report an `OpSub` of two marked
addresses finds **none** over the whole self-host compile. The propagation rule
was wrong independently of what fed it, and closing it fixed the miscompile; the
seed is still open.

Instrumenting the pass counted **588 such loads across 201 functions** — 165 in
`irlower`, 14 in `parser`, 10 in `asm_ir`, including `emit_ir_op_const_i32`
itself. The same probe reports zero for `ir_strength_run`, which matches the
flat build exactly.

The fix refuses both impossible answers in `mark`: a value read from fewer than
eight bytes cannot be a machine address here (`ResolveWidths` runs only for the
64-bit arm64 backend, whose lift renders every pointer-width load as the 8-byte
`OpLoad`), and inference may not overturn a parameter's declared classification.
The first mirrors the guard `mark` already applied to integer constants.

**One compiler, both backends.** The first comparison ran a freshly built SSA
driver against a flat driver compiled hours earlier, because `drvrun.sh` rebuilt
only the SSA side. That varies the compiler VERSION as well as the backend, and
one of the three differing programs — `examples/probes/retained_param_leak.fern`
— was exactly that: a frame-size and RC-drop difference between two mains, not
between two backends. It matches once both drivers come from one compiler. The
tell was `irlower_run -slots`, which reports `main` with 9 locals in both builds:
the SSA driver emitted `movq $9` agreeing with it, and the STALE flat driver
emitted `movq $8`. The odd one out was the old binary.

Rebuilt properly — one compiler, only the backend differing — the picture is
clean. Before the fix, 4 of the 5 probe programs differ on the constant; after
it, none do. The harness now rebuilds both sides.

The whole corpus, re-run that way with the fix in, is the number to quote:

| driver | match | differ | skip |
|---|---|---|---|
| `interp_run` | 285 | 0 | 1 |
| `asm_run` | **286** | **0** | 0 |

`interp_run`'s one skip is `examples/bench/array_append.fern`, where the
interpreter runs out of memory under qemu — a skip on both sides, not a
mismatch. Every other program in the corpus now compiles to the same bytes
through either backend.

**What this says about the gates.** The bug predates the whole size campaign —
the compiler built at the commit before #7643 reproduces it byte for byte. It
survived the per-module fixpoint, all 335 fixtures, the 281-program corpus
differential and the native suite, because none of them runs a self-host driver
built by the SSA backend against its flat twin. `docs/TEST-GATES.md` already
warns that the fixpoint is self-referential and blind to a stable miscompile;
this is that blindness with a name. A compile sweep answers "does it link". Only
a run differential answers "does it compute the same thing".
