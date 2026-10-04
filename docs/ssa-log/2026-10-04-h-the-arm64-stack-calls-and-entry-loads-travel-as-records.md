# 2026-10-04 — the arm64 stack calls and entry loads travel as records

`asm_arm64_ir` and `arm64_native`, arm64-linux. Slice 9 of #11305, refs
#8171.

## The shape

After the frame, the inline helpers and the unwind directives, checker's
arm64 listing still carried 38,143 text lines, and most of them were two
sequences the register path writes around a call on the stack ABI. The
call itself: each argument pushed with `str xN, [sp, #-16]!`, the `bl` to
the runtime symbol, and the `add sp, sp, #N` that drops the arguments —
6,723 pushes, 1,750 calls (1,670 of them `__fn___fern_str_concat`) and
3,384 drops. The callee's stack entry: `ldr xN, [sp, #N]` loading each
register parameter from the caller's pushes, 4,487 of them, before the
`.r` label the register entry starts at. With them, 1,597 `sxtw xN, wM`,
the signed widening of an i32.

## What changed

The push goes through `ssa_ldst_wb`, the call through `ssa_bl_sym`, the
drop through `ssa_addsub_imm` and the entry loads through `ssa_ldst`: the
record helpers the frame already used, each with its text fallback. The
widening is the new `arm64_rec_sxtw`, the word of `sxtw Xd, Wn`, which
refuses a w destination or an x source; the unary site records it when
both operands are registers and writes the text builders' line otherwise.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container, main at f74195e7 against this change:

| | main | with the stack calls and entry loads |
|---|--:|--:|
| stage 2, arm64 target, total Ir | 19.246 G | 19.118 G (−0.67%) |

`arm64_native` falls from 674 M to 576 M: `arm64_gas_program_words` 109 M
to 102 M, `arm64_gas_line_cut` 14 M to 7 M, `arm64_gas_operands` 10 M to
2 M, the trims (19 M) to 2 M, `arm64_gas_reg` and `arm64_gas_mem` leave.

Of checker's emitted lines, 802,646 travel as records and 20,327 as text
(from 784,308 and 38,143). The binary is byte-identical on both targets
and `scripts/selfhost-emit-hashes` matches main on every (fixture,
target) pair.

## What is left

The arm64 text is now the data directives (6,394 `.quad`, 2,178 `.ltorg`),
the labels by name (3,913 functions, 1,927 string blobs), the
stack-machine arms the register path runs for the OS floor, and the
hand-written runtime: 13,316 directives, 6,673 labels and 338 instructions
in a listing of 823,000 lines. The x86 listing is at the same point. What
the two assemblers do with a record is now the cost, and the entries on
slices 6 and 8 cost it out: the per-byte copy, the fixup arrays and the
line table on x86, the fixup and label lists on arm64.
