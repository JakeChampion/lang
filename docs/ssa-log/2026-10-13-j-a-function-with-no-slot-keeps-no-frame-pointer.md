# 2026-10-13 — a function with no slot keeps no frame pointer

x86-64 only. `asm_ir.emit_ssa_allocated_x86` (`bare`), `ssa_bare_frame`,
`ssa_bare_epilogue`, `ssa_emit_exit`; `EmitState.ssa_trap_suffix`,
`ssa_trap`, `leaf_trap`, `ssa_jumped_traps` and `ssa_called_traps` with
their stubs.
Refs #8171.

## The shape

Every x86-64 function that made a call built a `%rbp` frame:

- `pushq %rbp`, `movq %rsp, %rbp`, its callee-saved pushes and an alignment
  `subq` on entry;
- `leaq -8n(%rbp), %rsp`, the pops and `popq %rbp` (or `leave`) on exit.

A function with no frame slot never reads `%rbp`. The only reader is the
fatal abort's backtrace walk. A stage-2 compile of `checker.fern` enters such
frames 127 M times, and the frame-pointer half of each prologue and epilogue
is four of their instructions.

## What changed

A function that is not a leaf, has no spill slot and no view, takes every
parameter in a register, and is built without `-g` (the same test tail calls
use) is now emitted without a frame pointer:

- On entry it pushes the callee-saved registers it uses, then
  `pushq %rcx` as a pad when their count is even. That leaves `%rsp`
  16-byte aligned at its calls.
- On exit it pops them, with a `.cfi_def_cfa_offset` after each push and
  pop, as a frameless leaf does.
- A `-g` build keeps every frame.

The backtrace still names the function a trap fires in:

- A trap such a function reaches by a jump goes through the stub for its
  pushed words, `<trap>.l<saved + pad>`. These are the array bounds check,
  the string view's bounds check and the negative allocation size. The stub
  builds a frame record from the return address, as the bounds stub already
  did for a leaf. The stubs exist for all three traps now, and run to
  `nsaved + 1` to cover the pad. A frameless leaf's jumps to the string and
  allocation traps go through them as well.
- A trap the body calls, `__fern_oob_abort` or `__fern_slice_abort`, goes
  through its `.c` stub: `pushq %rbp; movq %rsp, %rbp; jmp <trap>`. The
  call's return address heads the walk.

What the walk loses is the call site that led into such a function: the
next frame up is its caller's caller. `docs/ARRAY-BOUNDS.md` says so.

## Measured

The stage-2 compile of `checker.fern` under callgrind, without `-g` on both
sides, warm certifier memo, both compilers built from the same base with
tail calls on:

| | Ir |
|---|--:|
| tail calls (#12346) | 14.369 G |
| no frame pointer | 14.143 G (−1.57%) |

The stage-2 compiler recompiles itself byte for byte.

`scripts/perf-bench` x86-64 moves 28 rows, all re-banked:

| row | change |
|---|--:|
| `call_overhead.ir` | −3.51% |
| `map_string.ir` | −2.42% |
| `http_hello.ir` | −2.09% |
| `map_int.ir` | −1.62% |

`perceus_deriv`, `perceus_nqueens`, `record_update`, `string_build`,
`struct_drop` and the other `.ir` rows fall as well. The `.text` rows of the
smallest programs rise. That is the trap stubs every runtime now
carries: two `.c` stubs and six `.l` stubs for each of the three jumped
traps, a fixed 80 instructions per binary.

## The machines

The retained constructor and small-caller controls assumed the framed
prologue: they deleted `pushq %rbp`, rewrote `movq %rsp, %rbp` and
`leaq -8(%rbp), %rsp`, and deepened the call by editing the alignment
`subq`. They now state the same facts for a body with no frame pointer:

- Dropping either half of a save or pad pair is refused.
- Popping the pad as `addq $8, %rsp` is certified.
- An explicit `subq $16`/`addq $16` around the body is certified.

The single-field `parent` now calls `__fern_arr_box` 8 bytes below its entry
rather than 24, so the "outside the derived frame bound" mutation moves
from `subq $24` to `subq $40`. That clears the machine's −40 window from
either depth.

## Witnessed

- `TestSelfHostLeafFrame` now requires that `outer`, which calls, builds no
  frame, and that a `-g` build of it does.
- `TestSelfHostAbortReportsX86_64` holds every case to at least one frame
  under the header. The two byte-view cases failed until the `.c` stubs
  landed.
- These pass: `TestSelfHostRetainedNativeBodies/x86-64-linux`, the x86-64
  fixture corpus, the e2e backtrace, bounds and sanitizer tests, and the
  e2ecompiler frame, CFI, release and SSA suites.
