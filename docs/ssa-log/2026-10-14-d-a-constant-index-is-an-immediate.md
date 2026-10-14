# 2026-10-14 — a constant index is an immediate

Both native backends. `ssa.imm_use`, `ssa_mem_at` in `asm_ir.fern` and
`asm_arm64_ir.fern`. Refs #8171.

## The shape

`arr_get`, `arr_set` and `str_index` (SSA kinds 42..44) took their index in
a register even when it was a constant, so `xs[2]` loaded 2 into a register
and then read `8(%a,%idx,8)`. On arm64 it was worse: `add x5, idx, #1` and
then a register-offset `ldr`. Every closure call pays this. A call through a
function value reads the body's address out of slot 0 of the environment box
(`ssarc.body_address`), so each one ran `xor %esi, %esi; mov 8(%rbx,%rsi,8),
%rdi` before its `call`.

## What changed

A constant from 0 to 4087 used as the index of one of these kinds is now an
immediate. The emitters fold it into the access: `8+8c(%a)` for a word,
`8+c(%a)` for a packed byte, `c(%data)` for a string byte, and `[a, #off]`
on arm64. A checked access compares the length with the constant instead,
`cmpq $c, (%a); jbe` on x86-64 and `cmp x6, #c; b.hi` on arm64. The bound
keeps the arm64 offset inside the scaled 12-bit form for a word, and inside
the unscaled form for a byte.

## Measured

Stage-2 compile of `checker.fern`, callgrind, warm memo:

| | Ir |
|---|--:|
| main (8b57fccb) | 13.965 G |
| this | 13.930 G (−0.24%) |

The stage-2 compiler reproduces itself, and is 8 KB smaller. Against main's
emitted sizes (`scripts/perf-bench-selfhost`), no program grows, and 22
shrink on each of x86-64 and arm64.

2026-10-14-e takes the closure call's read into the call itself.
