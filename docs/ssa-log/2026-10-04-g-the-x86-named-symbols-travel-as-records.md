# 2026-10-04 — the x86 named symbols travel as records

`asm_ir`, `x86_native` and `asmcore`, x86-64-linux. Slice 8 of #11305,
refs #8171.

## The shape

With the frame, the moves, the calls' surroundings and the unwind
directives as records, checker's x86 listing still carried 43,123 text
lines and 44,382 `\x03` lines. A `\x03` line is a call or a jump to a
symbol, trimmed by the emitter but parsed by every round like any text:
its mnemonic looked up, its operand classified, its target resolved. The
text lines were mostly the address of a symbol — 11,264 `leaq sym(%rip)`,
of a string constant, a constant aggregate, a function or a shape — the
stack entry's parameter loads, 4,967 `movq N(%rsp), reg` opening nearly
every function, and 1,588 `movslq`.

## What changed

A symbol operand is the `\x07` record: the instruction's bytes as a `\x01`
record carries them, with the last four a rel32 placeholder, and the
symbol's name on the line. `asmcore`'s `EmitState.named` writes it;
`x86_gas_prepare` reads it into a `GK_NAMED` line whose target index
`x86_gas_link_labels` resolves once, and each round lays the bytes down and
queues the fixup through the same `x86_fixup_or_patch_at` a text call
takes. A record the words do not reach is refused; a name nothing defines
is unresolved as the text's is.

`ssa_call_sym` records a call through `x86_rec_call`, and every
`leaq sym(%rip), reg` of the emitter goes through `ssa_lea_sym`, which
records through `x86_rec_lea_rip`. The stack entry's loads go through
`ssa_mov_rm`, so the shim is records like the body it opens; the three
signed widenings that are `movslq src32, dst` take `x86_rec_movslq_rr`
when both operands are registers. The `\x03` line survives for the jumps
to a symbol (`jae __fern_oob_abort`), which the assembler still relaxes.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container, main at e0247f9f against this change:

| | main | with named records |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.438 G | 20.353 G (−0.41%) |

`x86_native` falls from 1,505 M to 1,447 M: `x86_gas_prepare` 106 M to
93 M, `x86_gas_emit_op` 26 M to 18 M, `x86_gas_classify_with` 12 M to
1 M, `x86_gas_trim` 8 M to 3 M, `x86_lea_rip_label` and
`x86_call_label_at` (13.5 M) leave. `x86_gas_bytes` rises from 154 M to
186 M, since each named record is laid down one at a time by the byte
loop a run shares, and `x86_queue_fixup_at` stays at 93 M: a fixup is
four array appends and a struct rebuild per round whether it arrives as
text or as a record. The rest of the gain is the emitter's string work for
the lines it no longer writes.

Of checker's emitted lines, 784,150 travel as records and 25,914 as text
(from 766,817 and 43,123); the `\x03` lines fall from 44,382 to 1,354,
and 54,408 of the records are named. The binary is byte-identical on
both targets and `scripts/selfhost-emit-hashes` matches main on every
(fixture, target) pair.

## What is left

On x86 the text is the buffered leaf bodies, text by construction, and
the hand-written runtime; on arm64 the stack-ABI call sequence
(`str xN, [sp, #-16]!`, `bl`, `add sp, sp, #N`: 11,000 lines around
`__fn___fern_str_concat`), the stack entry's `ldr xN, [sp, #N]` loads,
`sxtw`, and the `.ltorg` pools. In the x86 assembler the per-byte copy,
the fixup arrays and the line table are the cost that remains, and they
are the single-pass core the previous entries describe.
