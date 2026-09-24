# The self-host SSA backend: `-backend ssa`

Status: landed 2026-09-16 for arm64 (`arm64-linux`, `arm64-darwin`,
`arm64-android`) and x86-64 (`x86-64-linux`), opt-in; since 2026-09-17 every
function of the compiler compiling itself goes through it; the default on
both native ISAs since 2026-09-18; **the only emitter on them since
2026-09-22**, when the stack machine's per-function driver was deleted.
Owner: compiler / self-host. This is the self-host
half of #4112, the register-allocating SSA backend track; the native half is
`internal/codegen/arm64ssa` and `x86_64ssa` behind the same flag spelling. The
measurements are in `docs/ssa-log/` (the entries whose names carry
`selfhost-ssa-backend`).

## What it is

The self-hosted compiler lowers every function to the stack IR (`irlower`,
or the semantic lowering's `ssarc` when `FERN_SEM_IR` produces the module).
On wasm the backend instruction-selects that stream onto the machine's own
stack and locals. On the native ISAs the register path in `asm_arm64_ir.fern`
and `asm_ir.fern` is the emitter — the stack machine that used to stand
beside it, pushing every value and popping every operand, is gone. For each
lowered function it:

1. lifts the ops to SSA with `ssa_lift.lift_from_ir_prod`, which admits the
   integer spine (constants, locals, the integer operators, `not`, the width
   casts), the structured control flow, every `call_direct`, and the box ops
   spelled onto the flat backends' layouts (`ssa.fern` kinds 40 to 50: a load
   and a store at an offset, the bounds-checked element read and store of a
   length-prefixed box, the bounds-checked byte of a string, the address of a
   function, a shape or a string literal, the rc-headered allocation, and
   the two ways the runtime is called), and bails on anything else, naming
   the op;
2. drops what nothing reads (`ssa.prune_dead`), which is most of the zeros
   the lift gives declared locals and most of the loop-header phis;
3. allocates registers with `ssa.regalloc_linear` over two pools: the
   caller-saved registers (x0 and x9 to x15 on arm64; rax, rsi, rdi and r8
   to r10 on x86-64) and, for a value live across a call, the callee-saved
   ones (x19 to x28; rbx and r12 to r15);
4. emits the function on the conventions the stack machine established:
   the same frame record, parameters read from the caller's slots
   (`[x29, #16 + 16*i]`, `16 + 8*i(%rbp)`), the result in x0 or %rax, calls
   made by pushing the arguments and calling the same `__fn_*` and runtime
   symbols. Each op is selected from the same instruction table
   (`ir_bin_asm`, `ir_div_guarded`, the literal-divisor forms on x86-64).

A function the lift or the emitter cannot take is a **refusal** naming the
function and the op (`ircore.ssa_refuse`, exit 3), never a fall-through:
there is no other emitter. Every op the lowering produces has an arm, so
that refusal is a compiler bug report. `FERN_SSA_REPORT=1` prints, on
stderr, one line per function whose lift or emit took over 200 ms, with both
times and its op and slot counts; nothing about the emitted program changes
with it. `-backend ssa` names the register path explicitly and is
byte-identical to omitting the flag; `-backend flat` is refused on the native
ISAs and names wasm's one emitter.

## Why per function

Native's `-backend ssa` replaces the whole program's emitter and carries its
own copies of the runtime helpers. The self-host did not need to: the stack
IR already contains every reference-count operation as a call to a runtime
helper, and the helper bodies are emitted from the same needs table whichever
emitter marked them (`helper_call_needs`). An SSA-emitted function therefore
kept the memory model of the stack-machine function beside it, and the two
could call each other because the argument convention was the same. That is
what made the mixing sound while both emitters existed, where the semantic
lowering's mixed modules were not: there the two halves disagreed about
ownership, here they only disagreed about where a temporary lives.

The per-function granularity is what made the track measurable from its
first day: the whole examples corpus built and ran with the flag on, and a
tally said how much of each program the new emitter produced. With the stack
machine gone the granularity is still per function, and the stack ABI it
established is what every caller outside a unit's own direct calls still
uses.

## Calls: two entries per function

Pushing every argument cost about 15% of the static instructions on the
compiler compiling itself (x86-64, 38k `pushq` and 34k `addq $n, %rsp` of
481k), plus a load of each parameter back in the callee. On x86-64 a
function with parameters now has two entries:

- `__fn_<name>`, the stack-ABI entry, loads the first six arguments from the
  caller's stack into the caller-saved pool (`%rax, %rsi, %rdi, %r8, %r9,
  %r10`, in pool order) and falls through into
- `__fn_<name>.r`, the register-ABI entry, where the body starts with them
  already there. A seventh parameter onward is read from the caller's stack
  either way.

A direct call to a function the same unit emits (`reg_entries`, built by
`emit_module_funcs`), with one to six arguments, moves its arguments into
the pool in parallel (`ssa_parallel_moves`, which phi edges use too) and
calls the `.r` entry: no pushes and no pop. Everything else keeps the stack
entry — runtime helpers, `__c_call`, the flat-op arms, hand-written stubs,
indirect and dyn-dispatch calls, and a call across per-module units — so a
callee the registry does not name is still called correctly. The pool is the
argument order because it is also where the allocator homes caller-saved
values: `ssa_arg_prefs` asks for a parameter's arrival register and an
argument's departure register, and a call's result may keep `%rax` when its
dying first argument was there, so `s = f(s, …)` moves nothing when `s` does
not live across another call.

Measured 2026-09-23 against the same tree without it, x86-64:

| subject | before | after |
|---|---|---|
| `checker.fern`, static instructions | 479,522 | 466,435 (-2.7%) |
| of which `pushq` / `addq $n, %rsp` | 50,825 / 33,809 | 31,463 / 21,501 |
| stage-2 compiler binary | 10,863,416 B | 10,736,144 B (-1.2%) |
| stage-2 compiler compiling `ssa.fern`, Ir | 4,034 M | 3,971 M (-1.6%) |
| `examples/bench/call_overhead`, Ir | 22.0 M | 17.9 M (-18.9%) |
| `tokenize` / `sort_ints` / `enum_match` / `record_update` | | -4.5% / -4.5% / -3.1% / -1.9% |

The compiler's values mostly live across calls in callee-saved registers,
so many of its arguments become a move rather than disappearing; what goes
is the store and reload through memory, and the pop.

arm64 has the same two entries over x0 and x9..x15 (eight arguments in
registers, stack slots 16 bytes apart): `checker.fern` 459,464 -> 444,286
static instructions.

The two runtime helpers the compiler calls most, `__fern_arr_dec` (10,267
sites on `checker.fern`) and `__fern_str_free` (6,823), are hand-written
and began by loading their one argument into `%rax` / x0, so each has a
`.r` entry after that load (`ssa_reg_helper`): `checker.fern` falls to
449,708 static instructions (x86-64, -6.2% on the stack ABI), and the
stage-2 compiler to 3,912 M Ir compiling `ssa.fern` (-3.0%) and 10,444,920
bytes (-3.9%). The rest of the track, in order:

1. The other hand-written helpers with arguments (`str_eq`, `str_concat`,
   `arr_inc_elems`, `alloc_reuse`), each by its own argument order.
2. Indirect calls, function addresses, closures and dyn dispatch move to
   the `.r` entries together — the one step that can miscompile silently,
   since both symbols exist.
3. More than six arguments: the caller reserves the stack slots of the
   first six and pushes the rest where the stack ABI puts them.
4. Once nothing refers to a stack entry, the shims go.

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
- **The stack machine's own arm** (`flat_op`, 59): every other op with a
  known stack effect — `ir.op_pops` models it and it pushes one value — the
  byte kernels, the map ops, the byte-buffer builder and the whole OS floor
  (the process and host queries, the handle ops, the signal, socket and
  timer ops). The instruction carries the op's index; the emitter runs
  `emit_stack_op` for that exact op into a capture buffer
  (`EmitState.capture`). The operands the arm's first lines pop are loaded
  straight into the registers those pops name, and the rest are pushed
  beneath them. An arm whose last line pushes one register hands its result
  back in that register; any other arm's result is popped.
  The byte-buffer builder's ops skip the stack: their helpers take the
  register ABI, so `ssa_buf_call` loads the operands straight into the
  argument registers (`asmcore.buf_helper` names the helper for both).
  `emit_stack_op` is what survives of the stack machine: an op table with
  one arm per op the lift has no instruction of its own for, and a refusal
  (`ircore.flat_arm_missing`) for an op that reaches it without one. The
  allocator counts a `flat_op` as a call, so every value live across it is
  in its frame slot and the arm's registers hold nothing. The one op this
  cannot bridge is `dyn_dispatch`, whose arm reads its arguments from the
  frame slots the lowering spilled them to.
- **`dyn_dispatch`** (kind 60): the lift carries the values of the argc
  slots the lowering stored the receiver and the arguments to, and the
  emitter runs the same compare-branch chain as the stack machine
  (`emit_dyn_dispatch_chain`) over a list of argument locations, a
  register home or a spill slot, in place of the locals. The chain's own
  scratch (rax, x0) is moved out of the way first when a value lives there.
  `ssa_lift_admits_run.fern` is the census: every registered kind is
  admitted except the three no lowering produces.

The lift's older arms for these ops lower to `build_func`'s layouts and
went with it (see "What this retires").

## The emitter today

Values live in registers from the two pools above or in frame slots
numbered over the spilled values, addressed from sp on arm64. An instruction the emitter selects itself writes into its result's
home and reads operands from theirs (`ssa_dst`, `ssa_src`): the integer
add, subtract, multiply, and, or, xor and the compares compute into the
home with only a spilled operand passing through a scratch
(`ssa_bin_in_place`; on x86-64 the operands swap, or a comparison flips,
when the right one lives in the destination), and a compare read only by
its block's branch is consumed as flags straight from the homes. A
constant those ops alone read is an immediate operand and is never
materialised (`ssa.imm_operands`: any i32 on x86-64, 0 to 4,095 on arm64
for add, sub and the compares; a constant on the left swaps or flips the
same way, and an op whose operands are both constants keeps them in
registers). A value defined by a phi, one of those ops or a unary takes
the register of its phi mate or of an operand of its definition when that
register is free or its holder dies at the definition, and a loop-carried
operand takes its phi's register whenever no use of the phi is reachable
from the operand's definition without passing the header
(`ssa.phi_mates`, `ssa.mate_interferes`), so `sum = sum + i` computes into
`sum`'s register and the back edge moves nothing. A spilled value takes its
phi mate's frame slot by the same rule (`ssa.assign_spill_slots`), so a loop
with more carried values than registers does not copy slot to slot on its
back edge: the whole compiler's x86-64 text is 3.8% shorter for it, and a
self-host `uniq` runs 5% fewer instructions. A phi also takes its entry
operand's slot when no use of the operand is reachable from the phi, and a
free slot another phi is waiting for is passed over, so entering an inner
loop does not copy either. A phi reads its operand
on the edge, at the predecessor's terminator, not inside the header, and
a loop-carried operand's interval ends at that edge. Empty blocks holding
only a branch are skipped by every edge into them and dropped, a phi loses
its slot for a dropped predecessor (`ssa.thread_forwarding`), and a branch
whose false target is the next block falls through into it. Division,
the shifts and the table shared with the stack machine still go through
x0/x1 (`%rax`/`%rcx`), as does every call result. The phis of one edge are
parallel moves between homes (`ssa_parallel_moves`): one instruction per
move, a cycle broken by parking one location in the scratch, and no move at
all for a phi whose operand already sits in its home. On the compiler
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
3. `build_func`'s optimiser (`ssa.optimize`) is deleted: its binary arms
   read the frontend's operator symbols rather than IR kind names, and the
   subset that worked on the lifted function saved 1.2-1.9% of instructions
   for 20-22% more compile time
   (`docs/ssa-log/2026-09-17-the-optimiser-costs-more-than-it-saves.md`).

## Gates

- `internal/e2eselfhost/self_host_ssa_lift_admits_test.go` runs
  `ssa_lift_admits_run.fern`, the lift's admission census over every
  registered IR op kind, and pins the kinds it declines: the three kinds no
  lowering produces. A new op kind reaches the register path through the
  flat-op arm unless `ir.op_pops` does not model it, and then it is a new
  line here rather than a refusal on every program that uses it.
- `internal/e2eselfhost/self_host_ssa_backend_test.go` builds the CLI for
  the host, compiles each of its programs with it and with the native
  compiler for every target the host can run output for (its own ISA
  natively, the other through its qemu user emulator when present), runs
  both and compares stdout and exit status. It also pins the `-backend`
  refusals and that a second `-o` to one path replaces the executable.
- `internal/e2eselfhost/self_host_ssa_loop_tail_label_test.go` reaches the
  same invariant from the SOURCE end: a self-tail-recursive function whose
  body ends in a `return`, compiled through the register path for both ISAs
  and then assembled and run. `assertNoDuplicateLocalLabels` reads the
  listing, so it catches a label written twice for ANY reason — including two
  distinct blocks or functions whose `asmcore.sanitize_label` spellings
  collide, which `repeated_block_id` cannot see. It carries over the
  read_file and frontend-bundle listings too.
- The fixture legs (`internal/e2e/fixture_selfhost_test.go`) are the corpus:
  every program through the register path on both ISAs, against the
  expected output. A function the lift cannot take fails the compile there,
  naming the op.
- `scripts/selfhost-emit-hashes` hashes what this emitter produces, since
  it is the only one.
- `ssa.repeated_block_id` is checked in each backend's `ssa_try_function`
  before emit: the emitters write one `.Lssa_<fn>_<id>:` label per entry in
  `f.blocks`, so two entries carrying one id spell one label twice and the
  assembler rejects the module without naming the pass that produced it. A
  repeat is a compiler bug, so the check exits rather than declining the
  function onto the stack machine. Every state the lift leaves a dead block
  in must therefore carry a FRESH id — the shape `br` establishes, and what
  a loop nothing leaves alive failed to (#9688).
  `internal/e2eselfhost/self_host_ssa_lift_blocks_test.go` lifts those op
  streams directly.

## What this retires, and in what order

`docs/SELFHOST-SSA-DECISION.md` kept the SSA layer as analysis and slated
the `build_func` frontend for retirement, blocked on the runtime helpers
its lift-fed drivers could only obtain from `build_func`. This backend
takes its helpers from the production runtime, so nothing on the retained
path needs `build_func` any more. In order:

1. Done: coverage of every op the compiler uses, above.
2. Done: `ssa.build_func` with `-ssa`, `-ssa-scan`, `try_ssa`,
   `ssa_wasm.fern`, the `__fern_ssa_*` runtime in `ssa_x86.fern` and
   `ssa_arm64.fern`, the lift's `build_func`-layout arms, and the drivers
   and Go tests that existed only for them are gone. `ssa.fern` keeps its
   data model, optimiser and allocator; `ssa_lift.fern` keeps the
   production lift, now the only lift.
3. The allocator and emitter work above, measured against the flat backend
   on `examples/bench` and on the compiler building itself.
4. The default flip, on the conditions native's flip is held to: the binary
   at or under flat's, compile time within a stated multiple, the corpus
   lane clean on both targets. Native's flat retirement waits on this
   (#4112, the 2026-09-16 comment). **arm64 now meets all three** — binary
   0.93x, self-build 1.04x against a 1.5x ceiling, corpus clean on both
   targets — and the numbers are in
   `docs/ssa-log/2026-09-17-arm64-meets-every-flip-condition.md`. x86-64 met
   the corpus condition and not the size one until #9683 folded the trivial
   phis, which took its output from 1.25x the stack machine's to 0.92x the
   instructions and 0.80x the linked binary; re-measured after it, every
   condition holds there too
   (`docs/ssa-log/2026-09-18-x86-64-meets-every-flip-condition.md`).
   **Taken on both native ISAs** — arm64 in #9672, x86-64 after it: omitting
   `-backend` selects the register path wherever there is one, and the stack
   machine on wasm, where there is not. Until step 7 `-backend flat` still
   named the stack machine on either native ISA, and a function the register
   path declined fell back to it on its own.
5. Every op the stack machine can emit, the register path can emit (2026-09-20,
   through `emit_stack_op`): the compiler compiling itself was already
   whole; the corpus sweep (the conformance cases, the coreutils, the
   benches, the CLI examples and the stdlib tests: 863 programs that
   compile, 95,072 functions) found 381 functions in 115 programs declining
   for 58 OS-floor ops, on either ISA
   (`docs/ssa-log/2026-09-20-every-op-through-the-stack-machines-arm.md`);
   `dyn_dispatch` followed the same day, and the sweep declines nothing.
6. Done: the corpus lane. The fixture legs
   (`internal/e2e/fixture_selfhost_test.go`, x86-64 and arm64) compiled every
   program under `FERN_SSA_REPORT=1` and required each module's tally to
   read `0 declined`, holding the number the hand sweep measured: 866
   modules on each ISA, every one `0 declined`.
7. Done (2026-09-22): the deletion. The stack machine's per-function driver
   on both native ISAs — the prologue, the local slots, the op-index labels,
   the epilogue — and every arm of its op table for an op the lift lowers
   itself are gone, with the `-backend flat` spelling on those ISAs, the
   `FERN_SSA_ONLY` / `FERN_SSA_SKIP` bisect knobs (a declined function has
   nowhere to go), the module tally and the corpus coverage gate that read
   it (a decline is now a compile failure the legs report on their own).
   What stays is `emit_stack_op` as the flat-op arm's table, and the x86-64
   literal-divisor forms, which moved onto the register path's division
   (`ssa.Imms.konst`) rather than going with the driver that reached them.
   `docs/ssa-log/2026-09-22-the-stack-machines-function-driver-is-gone.md`
   has the numbers.

## The other backends

The register path is the self-host's, and the self-host is what the native
compiler is converging on (`docs/NATIVE-CONVERGENCE.md`), so the order is:

- **The self-host's native ISAs first**, above. They are the compiler's own
  output and the two the flip already took.
- **wasm stays on the stack IR**, and `-backend flat` names its emitter.
  wasm IS a stack machine with locals: the
  IR's `load_local` is `local.get`, its structured control flow is wasm's,
  and there are no phis to fold and no registers to allocate — the engine
  does that from the locals. What the register path bought the native ISAs
  (0.69x and 0.92x the text) came from the trivial phis and the frame
  traffic of a machine stack, neither of which wasm has, and native's
  `wasmssa` was retired for the same reason (#9397). An optimisation wasm
  should share lands in the IR layer (`ir.fern`, #6638), where all three
  emitters read it.
- **The native compiler gets no further SSA work.** `internal/codegen/{arm64ssa,x86_64ssa}`
  stay opt-in behind `-backend ssa` as they are; the native default flip
  (#9640) and a Darwin or Android arm of `arm64ssa` are native-only surface,
  which the convergence policy counts as debt, and the backends themselves
  are slated to go once the self-host bootstraps without them.

## The target

The backend exists to beat the stack machine on all three of these at once,
measured on the compiler building itself and on `examples/bench`:

All three are met on BOTH targets as of 2026-09-18, after folding the
lift's trivial phis (`docs/ssa-log/2026-09-18-the-trivial-phi-was-the-whole-gap.md`).

- **Faster output.** Native's SSA build is at or under flat on every bench
  program; the self-host's must be too, and the compiler it builds must run
  the whole tree faster than the flat-built one. The compiler built with
  `-backend ssa` compiles `checker.fern` in 8.2 s against the flat-built
  compiler's 13.5 s and `irlower.fern` in 2.0 s against 4.4 s (arm64-darwin,
  best of three): **40% and 53% faster**.
- **Smaller output.** Native's SSA text is 45% of flat's over the corpus.
  The self-host's is **0.69x flat's on arm64** (2,805,064 against 4,043,922
  instructions for the compiler) and **0.92x on x86-64** (2,812,651 against
  3,043,396), from 1.12x and 1.25x. The linked compiler follows: 12.8 MB
  against 18.0 on arm64-darwin, 11.6 against 14.5 on x86-64-linux.
- **Faster compile.** The lift, prune and allocation must cost less than the
  emitted text they save the assembler: the self-host assembles its own
  output, so fewer lines is less to parse. The SSA self-build is **1.02x**
  the flat one on arm64 (110.3 s against 108.6 s); 1.5x is native's flip
  condition and the ceiling here, with parity the aim. Per module it is
  already under: `checker.fern` emits in 7.2 s through the register path
  against 7.5 s through the stack machine.

An entry in `docs/ssa-log/` carries each step's numbers against these three.
