# 2026-10-23 — the lane scan asks few instructions

`ssa.lane_values` in the native backends. Refs #8171.

## The shape

`ssa.lane_values` marks the lane values of a function. The x86-64
backend calls it about four times for each function: in
`float_values`, in `assign_spill_slots`, in the emitter, and once more
in `select_round`. The self-compile made about 35 K calls, and each
call examined every instruction with `ssa.lane_result`: 4.6 M calls.

Each call retained and released the instruction, and `lane_inst` did the
same again. The scan cost about 490 M Ir, about 100 Ir for each
instruction, and the compiler has no lane values.

## What changed

`lane_results` now asks `lane_result` only about an instruction that can
be a lane value: a parameter (kind 6), a call with the lane mark (kind
11, mark 6), or an operator or store whose name starts with `v`. The
test reads three fields of the instruction and makes no call. The mark
array is made when the first lane value is found, not for each function.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | main at 4a45ac1 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 78.626 G | 78.283 G (−0.44%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

`ssa.lane_inst` is still called for each instruction from
`asm_ir.emit_function_via_ir_named`, about 2 M calls. Each call retains
and releases its instruction for a test of three fields.
