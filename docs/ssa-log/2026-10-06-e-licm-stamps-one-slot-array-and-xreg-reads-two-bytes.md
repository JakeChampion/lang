# 2026-10-06 — LICM stamps one slot array per function; `ssa_xreg` reads two bytes

`ir.hoist_loop_invariants` and `asm_ir.ssa_xreg`, every target for the
first, x86-64 for the second. Refs #8171.

## The shapes

`licm_hoist_from_loop` built a boolean array as long as the function's slot
range for every loop it visited, to mark the slots the loop's body writes,
and most loops hoist nothing. On `checker.fern` the fill was 64 M
instructions: `parse_stmt_at` alone has 1,424 slots and 102 loops. One
`i32` array per function now serves every loop: a body write stamps its
slot with the loop's index plus one, and a candidate is a slot whose stamp
is not this loop's. The index is unique per loop, since the pass walks the
loops from the last to the first and a rewrite shifts only the ops after it.

`ssa_xreg` turns a register name into its number. `%r8` to `%r15` were read
from their digits; the eight named registers went through a `match` over
eight string literals, 1.7 M times per compile, 84 M instructions with the
compares. The two bytes after `%r` tell the eight apart, so they are read
directly.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at d102187b against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries, as do the native-built pair. `scripts/selfhost-emit-hashes`
matches on all 2,004 rows:

| | main | this branch |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.687 G | 18.561 G (−0.67%) |
| `ir.optimize_ops`, self (the LICM helpers inline into it) | 276 M | 206 M |
| `asm_ir.ssa_xreg`, self | 54 M | 35 M |
| `__fern_str_eq`, self | 225 M | 198 M |

