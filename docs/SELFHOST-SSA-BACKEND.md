# The self-host SSA backend: `-backend ssa`

Status: landed 2026-09-16 for arm64 (`arm64-linux`, `arm64-darwin`,
`arm64-android`) and x86-64 (`x86-64-linux`), opt-in. Owner: compiler / self-host. This is the self-host
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

Measured on the compiler compiling itself (`fern.fern`, arm64-linux,
2026-09-16): 941 of 8,922 functions go through the SSA backend. The op that
stopped each of the rest, most frequent first:

| op | functions |
|---|---|
| `struct_get` | 1,997 |
| `const_str` | 1,962 |
| `variant_is` | 947 |
| `arr_len` | 923 |
| `arr_make` | 891 |
| `str_len` | 476 |
| `arr_get` | 238 |
| `struct_make` | 153 |
| `const_func` | 119 |
| `str_index` | 78 |
| `const_f64` | 65 |

Everything below that is under 25. Those rows are now lifted (#9498): the
record and array layouts are the flat backend's, fixed and documented at the
top of `asm_arm64_ir.fern`, so `struct_get` is a load at the field's slot,
`arr_len` a load at 0, `str_len` a load at 8, `variant_is` a load of the
shape word compared with the variant's shape address, a construction an
allocation followed by stores, and a string op or a push the same runtime
call the flat arm makes. The lift's older arms for these ops lower to
`build_func`'s layouts and are not usable here (see "What this retires").
The histogram after that lift is in the ssa-log entry for #9498, and is the
next checklist.

## The emitter today

It is written for correctness, and the measurements say so. Values live in
seven caller-saved registers or in frame slots; a value live across a call is
spilled, so a loop that calls out keeps its loop-carried values in memory.
A constant is materialised into x0 and moved to its home. Two or more phis
on one edge stage through the frame. The optimiser's passes do not run. The
binary the backend builds is larger than the flat one, not smaller, and
the code it emits for a call-heavy function is close to the stack machine's.
The order to take that in:

1. Callee-saved registers (x19 to x28) with a prologue save, so values
   live across calls stay in registers. Native's allocator work on #4112
   found the spill rule that decides when a call-crossing value is better
   in a slot than in a saved register; take its result rather than
   re-deriving it.
2. Constants at the use instead of a register, compare fused into the
   branch, and copies coalesced with their source; each is a peephole on
   the emitter with a size number to show.
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

1. Coverage by the histogram above, each op lifted to the flat backend's
   layout and emitted with the flat arm's instructions.
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
  the whole tree faster than the flat-built one. Today: parity.
- **Smaller output.** Native's SSA text is 45% of flat's over the corpus.
  The self-host's SSA text is a few percent larger today; the plan is the
  three emitter items above, then coverage, since a function the stack
  machine still emits pays the stack machine's size.
- **Faster compile.** The lift, prune and allocation must cost less than the
  emitted text they save the assembler: the self-host assembles its own
  output, so fewer lines is less to parse. Today the SSA self-build is 1.7x
  the flat one; 1.5x is native's flip condition and the ceiling here, with
  parity the aim.

An entry in `docs/ssa-log/` carries each step's numbers against these three.
