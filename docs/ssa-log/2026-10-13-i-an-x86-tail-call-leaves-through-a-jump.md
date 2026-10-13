# 2026-10-13 — an x86 tail call leaves through a jump

x86-64 only; arm64 already did this. `asm_ir.ssa_tail_call`,
`ssa_calls_besides_tails`, `ssa_emit_tail_call`, `ssa_emit_exit`,
`ssa_tail_mark` / `ssa_resolve_tails`. Refs #8171.

## The shape

A function that returns a register call's result as it is, such as
`return scale(a, 1)`, called the callee and then ran its own epilogue and
`ret`. Because that call counted as a call, a function with no other call
and no frame slot was still framed: `pushq %rbp`, `movq %rsp, %rbp`, `leave`
on every entry, only to keep `%rsp` aligned at a call that could have been
the function's exit. arm64 has left through `b __fn_<callee>.r` since the
frameless-leaf work. x86-64 had no tail call at all.

## What changed

The x86 emitter takes arm64's rule unchanged. The call `ins` is a tail call
when it is the last instruction of a block that returns its result, it goes
through a register entry, and the build keeps no frames for a debugger: no
`-g` variable rows and no source-line marks. A tail call:

- moves its arguments into the entry's registers;
- leaves through the function's exit with `jmp __fn_<callee>.r` where the
  epilogue would `ret`.

A function whose only calls are tail calls is a leaf candidate. Its buffered
body carries a tail mark, `.ssa_tail <target>`, beside the epilogue mark. Once
its text decides the frame, `ssa_resolve_tails` replaces each tail mark with
that frame's epilogue, with the jump in place of the return. A frameless
leaf pops its saved registers and jumps, so `%rsp` is back at its entry
value and the callee returns straight to this function's caller. A framed
function tears the frame down first, in the record form as well as the text.

## Measured

`scripts/perf-bench` on x86-64 against the banked baseline:

| row | change |
|---|--:|
| `http_hello.ir` | −1.65% |
| `map_string.ir` | −1.30% |
| `perceus_deriv.ir` | −1.05% |
| `perceus_nqueens.ir` | −0.86% |
| `array_index.ir`, `enum_match.ir` | −0.85%, −0.84% |
| `perceus_rbtree.ir` | −0.77% |

Two rows passed the gate's threshold and are re-banked: `http_hello.ir` and
`perceus_deriv.ir`.

The compiler gains little. The stage-2 compile of `checker.fern`, without `-g`
on both sides, went from 14.372 G to 14.369 G instructions. The stage-2
compiler's own text has 1,378 tail jumps, but none is on the hot predicates.
`semtypes.is_cell` ends in the inline string compare, whose out-of-line call
to `__fn___fern_str_eq.r` is followed by a jump to the join, so it is not a
register call in tail position. The stage-2 compiler recompiles itself byte
for byte.

## Witnessed

`TestSelfHostSSATailCall` covers both targets:

- `pick` returns `scale(a, 1)` on one path and a parameter on the other.
- The test requires `pick` to jump to `__fn_scale.r` with no frame and no
  call, and the program to exit 9.
- A `-g` build must keep the frame and the call.

The x86-64 leg fails on main, where `pick` builds a frame and calls.
