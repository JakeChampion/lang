# The self-host SSA backend: `-backend ssa`

Status: landed 2026-09-16 for arm64 (`arm64-linux`, `arm64-darwin`,
`arm64-android`) and x86-64 (`x86-64-linux`), opt-in; since 2026-09-17 every
function of the compiler compiling itself goes through it. Owner: compiler /
self-host. This is the self-host
half of #4112, the register-allocating SSA backend track; the native half is
`internal/codegen/arm64ssa` and `x86_64ssa` behind the same flag spelling. The
measurements are in `docs/ssa-log/` (the entries whose names carry
`selfhost-ssa-backend`).

## What it is

The self-hosted compiler lowers every function to the stack IR (`irlower`,
or the semantic lowering's `ssarc` when `FERN_SEM_IR` produces the module)
and the flat backends instruction-select that stream onto a machine stack:
every value is pushed, every operand popped. `-backend ssa` puts a second
emitter beside the flat one inside each native backend, `asm_arm64_ir.fern`
and `asm_ir.fern`. For each lowered function it:

1. lifts the ops to SSA with `ssa_lift.lift_from_ir_prod`, which admits the
   integer spine (constants, locals, the integer operators, `not`, the width
   casts), the structured control flow, every `call_direct`, and the box ops
   spelled onto the flat backends' layouts (`ssa.fern` kinds 40 to 50: a load
   and a store at an offset, the bounds-checked element read and store of a
   length-prefixed box, the bounds-checked byte of a string, the address of a
   function, a shape or a string literal, the rc-headered allocation, and
   the two ways the stack machine calls the runtime), and bails on anything
   else, naming the op;
2. drops what nothing reads (`ssa.prune_dead`), which is most of the zeros
   the lift gives declared locals and most of the loop-header phis;
3. allocates registers with `ssa.regalloc_linear` over the caller-saved
   temporaries (x9 to x15 on arm64; rsi, rdi and r8 to r11 on x86-64);
4. emits the function on the stack machine's own conventions: the same
   frame record, parameters read from the caller's slots (`[x29, #16 + 16*i]`,
   `16 + 8*i(%rbp)`), the result in x0 or %rax, calls made by pushing the
   arguments the way the stack machine leaves them and calling the same
   `__fn_*` and runtime symbols. Each op is selected from the flat backend's
   own table (`ir_bin_asm`, `ir_div_guarded`), so the two emitters cannot
   disagree about an operator.

A function the lift or the emitter declines is emitted by the stack machine
as before. Nothing about the module changes for it. `FERN_SSA_REPORT=1`
prints one line per declined function on stderr, `FERN_SSA: <name>: <op>`,
and a module tally, `FERN_SSA: module: N of M functions through the SSA
backend, K declined`. The op name is the IR kind that stopped the lift, so
the report's histogram is the coverage checklist. `FERN_SSA_ONLY` and
`FERN_SSA_SKIP` are comma-separated name prefixes: ONLY admits the functions
one of them matches, SKIP excludes them, and a declined function keeps the
stack machine, so a wrong answer or a slow compile on a whole program is
walked in on by halving the emitted set. Under the report a function whose
lift or emit took over 200 ms prints both times with its op and slot counts.
`-backend flat` names the stack machine, as on native, so a comparison can
ask for the baseline by name; it is byte-identical to omitting the flag.

## Why per function, and why the stack ABI

Native's `-backend ssa` replaces the whole program's emitter and carries its
own copies of the runtime helpers. The self-host does not need to: the stack
IR already contains every reference-count operation as a call to a runtime
helper, and the flat backend's helper bodies are emitted from the same needs
table whichever emitter marked them (`helper_call_needs`). An SSA-emitted
function therefore keeps the memory model of the stack-machine function beside
it, and the two can call each other because the argument convention is the
same. That is what makes the mixing sound, where the semantic lowering's
mixed modules were not: there the two halves disagreed about ownership, here
they only disagree about where a temporary lives.

The per-function granularity is what makes the track measurable from its
first day. The whole examples corpus builds and runs with the flag on, and
the tally says how much of each program the new emitter produced.

## What the lift admits, and what declines

Everything the compiler uses. Measured on the compiler compiling itself
(`fern.fern`, arm64-linux, 2026-09-17, the build of #9570): 9,129 of 9,130
functions go through the SSA backend; the one left is `heap_bump_bytes`, an
op that arrived on main while the coverage stack was open. The whole
examples corpus builds and runs the same both ways. How each family is
emitted, in the order it was admitted:

- **The integer spine** (#9484, #9494): constants, locals, the integer
  operators, `not`, the width casts, the structured control flow, every
  `call_direct`, each with the instruction the stack machine's table gives
  it (`ir_bin_asm`, the guarded division on x86-64).
- **The box layouts** (#9498, #9503): the record and array layouts are the
  flat backend's, fixed and documented at the top of `asm_arm64_ir.fern`, so
  `struct_get` is a load at the field's slot, `arr_len` a load at 0,
  `str_len` a load at 8, `variant_is` a load of the shape word compared with
  the variant's shape address, a construction an allocation followed by
  stores, a string op or a push the same runtime call the flat arm makes,
  `call_indirect` a call through a scratch register, a folded aggregate the
  address of its interned box.
- **f64** (#9567): a float is its 8-byte IEEE pattern in an integer
  register throughout, as on the stack machine, so the literal is its bits,
  the arithmetic and NaN-aware compares a `binary` carrying the op name, the
  math and conversions a `unary` carrying the op name, the reinterprets
  nothing, the transcendentals calls to the runtime helpers.
- **The host floor** (#9569, #9570): the fs, env and clock helpers as
  stack-ABI calls with their needs; the raw syscalls and `exit` by pushing
  the operands and running the flat arm's own pop sequence (its
  `ldr x8, [sp], #16` is what `darwinize` keys the Mach-O rewrite off); the
  string box stamp, the string builder, `raw_arr_box`, `map_new`, `args` and
  `map_hash_seed` as register-ABI calls; `raw_scratch`, `raw_environ` and the
  `arr_push` counters as `sym_addr`, a static symbol's address or the word
  at it; the width loads and stores as the box load and store at offset 0.
- **The stack machine's own arm** (`flat_op`, 59): `memcpy`, `memset`,
  `memchr`, the map ops and the byte-buffer builder are inline kernels or
  calls with extra operands drawn from the IR op's fields. The instruction
  carries the op's index; the emitter pushes the operands, runs the flat arm
  for that exact op, and pops the result. The allocator counts a `flat_op`
  as a call, so every value live across it is in its frame slot and the
  arm's registers hold nothing.

The lift's older arms for these ops lower to `build_func`'s layouts and are
not usable here (see "What this retires").

## The emitter today

Values live in seven caller-saved registers or in frame slots numbered over
the spilled values, addressed from sp on arm64; a value live across a call
is spilled, so a loop that calls out keeps its loop-carried values in
memory. An instruction the emitter selects itself writes into its result's
home and reads operands from theirs (`ssa_dst`, `ssa_src`); one selected
from the table shared with the stack machine goes through x0/x1
(`%rax`/`%rcx`), as does every call result. A compare read only by its
block's branch is consumed as flags. Two or more phis on one edge stage
through the frame. The optimiser's passes do not run. On the compiler
compiling itself the SSA text is 1.12x the flat text (4.71M against 4.21M
instructions, arm64); the numbers of each step are in
`docs/ssa-log/2026-09-17-selfhost-ssa-backend-whole-compiler.md`. What
remains, from that histogram:

| class | flat | ssa |
|---|---|---|
| frame loads and stores | 1,062,000 | 1,960,000 |
| register-to-register `mov` | 61,559 | 610,000 |
| of which call results moved out of x0 | | 333,000 |

Widening the caller-saved set from seven registers to eleven (x4 to x7)
changed the text by 0.07%, so the frame traffic is call-crossing values,
not register pressure. The order to take that in:

1. Done (#9579): callee-saved registers (x19 to x28; rbx, r12 to r15) as
   a second allocator pool for the intervals `spans_call` names, a
   per-function save and restore of the ones used, the slots below the save
   area. Frame loads and stores fall from 1.19M and 769k to 768k and 590k;
   the text barely moves because an operand that was loaded is now moved.
   Native's allocator work on #4112 found the spill rule that decides when a
   call-crossing value is better in a slot than in a saved register; that
   rule is still to take.
2. Call results into their home. The cheap half is done (#9595): a load
   into x0 of the value it already holds emits nothing, which removes the
   move back after every move out. The rest is x0 as an allocatable
   register for a value whose only reader follows the call.
3. The optimiser (`ssa.optimize`) on the lifted function once its binary
   arms read IR kind names rather than the symbols the `build_func` frontend
   used.

## Gates

- `internal/e2eselfhost/self_host_ssa_backend_test.go` builds the CLI for
  the host, compiles each of its programs both ways for every target the
  host can run output for (its own ISA natively, the other through its qemu
  user emulator when present), runs both and compares stdout and exit status. A program the
  lift admits whole must report no declined function; the mixed program pins
  the functions that must go through the backend. It also pins the refusal
  for other targets and that a second `-o` to one path replaces the
  executable.
- The whole examples corpus, built both ways and run, is the measurement
  in the ssa-log entries; `.github` has no lane for it yet. That lane is
  shaped like `internal/e2e/arm64_ssa_differential_test.go`.
- `scripts/selfhost-emit-hashes` does not reach this backend; a purity sweep
  of it needs `-backend ssa` added to that script's SSA mode.

## What this retires, and in what order

`docs/SELFHOST-SSA-DECISION.md` kept the SSA layer as analysis and slated
the `build_func` frontend for retirement, blocked on the runtime helpers
its lift-fed drivers could only obtain from `build_func`. This backend
takes its helpers from the production runtime, so nothing on the retained
path needs `build_func` any more. In order:

1. Done: coverage of every op the compiler uses, above.
2. Retire `ssa.build_func` with `-ssa`, `-ssa-scan`, `try_ssa`,
   `ssa_wasm.fern`, the `__fern_ssa_*` runtime in `ssa_x86.fern` and
   `ssa_arm64.fern`, the lift's `build_func`-layout arms, and the drivers
   and Go tests that exist only for them. `ssa.fern` keeps its data model,
   optimiser and allocator; `ssa_lift.fern` keeps the production lift.
3. The allocator and emitter work above, measured against the flat backend
   on `examples/bench` and on the compiler building itself.
4. The default flip, on the conditions native's flip is held to: the binary
   at or under flat's, compile time within a stated multiple, the corpus
   lane clean on both targets. Native's flat retirement waits on this
   (#4112, the 2026-09-16 comment).

## The target

The backend exists to beat the stack machine on all three of these at once,
measured on the compiler building itself and on `examples/bench`:

- **Faster output.** Native's SSA build is at or under flat on every bench
  program; the self-host's must be too, and the compiler it builds must run
  the whole tree faster than the flat-built one. Today the compiler built
  with `-backend ssa` compiles `checker.fern` in 10.2 s against the
  flat-built compiler's 13.2 s and `irlower.fern` in 2.2 s against 4.3 s
  (arm64-darwin, best of three, #9579); callee-saved registers were 17% of
  that on both.
- **Smaller output.** Native's SSA text is 45% of flat's over the corpus.
  The self-host's SSA text is 1.12x flat's today (4.71M against 4.21M
  instructions for the compiler, down from 1.67x before #9570); the binary
  was 1.12x at #9571. Every call spills what it crosses; the emitter items
  above are the plan.
- **Faster compile.** The lift, prune and allocation must cost less than the
  emitted text they save the assembler: the self-host assembles its own
  output, so fewer lines is less to parse. Today the SSA self-build is 1.11x
  the flat one (142 s against 128 s); 1.5x is native's flip condition and the
  ceiling here, with parity the aim.

An entry in `docs/ssa-log/` carries each step's numbers against these three.
