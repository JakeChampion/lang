# 2026-10-02 — the assembler encodes each fixed line once

`x86_native.x86_gas_assemble`, `x86_gas_assemble_pass_prepared`. Refs
#8171. No emitted byte changes: the `selfhost-emit-hashes` sweep is
1,965 rows per compiler with 0 differing against a compiler built from
main at 9cc02c45, and the `checker.fern` binaries the two stage-2
compilers emit are byte-identical.

## What the profile named

`x86_gas_assemble` was 4.60 G of the 35.38 G stage-2 compile of
`checker.fern`. Since 959b0fe8 branch relaxation settles on the first
round's offsets and assembles once more to confirm, so
`x86_gas_assemble_pass_prepared` runs twice over the 588,514 instruction
lines, 1.77 G between them, and the second round encoded every line
again. Only the lines that name a label can change between rounds,
since the byte offsets a round computes are what relaxation moves:
173,007 branches and calls, 11,233 rip-relative operands and 2,003
`.quad sym` rows. The other 400,000 lines produced the same bytes twice.

## What changed

The first round records, per line, the range of the code it produced
when the line names no label (`ref_idx < 0`) and its encoding changed
nothing but `code`: no fixup queued, no branch recorded, no refusal, no
data or bss moved. Every round after it copies those bytes
(`x86_replay_bytes`) and encodes only the rest. The cache is the first
round's resolved code and two index arrays (`EncCache`), handed back
with the round's result (`AsmRound`).

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 9cc02c45 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 35.38 G | 35.06 G (−0.92%) |
| stage 2, `x86_gas_assemble` inclusive Ir | 4.60 G | 4.27 G |
| stage 2, `x86_gas_assemble_pass_prepared` inclusive Ir | 1.77 G | 1.45 G |
| stage 2, `x86_gas_emit_op` inclusive Ir | 1.39 G | 0.85 G |
| stage 2, `x86_replay_bytes` self Ir | 0 | 103.5 M |

`x86_gas_prepare` keeps its 1.96 G: it runs once, and nothing here
touches it. Measured against the main before 959b0fe8, where four
rounds ran, the same change was 36.88 G to 35.06 G; a module that
needs more than the confirming round still gains per round.

## Witnessed

`TestSelfHostX86Gas*`, `TestSelfHostX86Capstone`,
`TestSelfHostX86TableRowsMatchNative`, `TestSelfHostAsmLoad*`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, and the emit-hash
sweep.

## Next

The first round still encodes every line, 0.85 G, and `x86_gas_prepare`
is 1.96 G on its own: `x86_gas_classify` 965 M (a chain of mnemonic
tests per line), `x86_gas_parse_mem` 583 M, `x86_gas_link_labels` 271 M
and `x86_gas_trimmed` 159 M. The assembler is still 4.27 G, 12% of the
compile, and a GasLine that keeps numeric operands (register numbers, a
parsed memory operand) instead of the strings every arm decodes again
would take the decoding out of the first round too. Elsewhere, self
cost: `ssa_lift.lift_impl` 1.09 G, `__fern_alloc` 1.04 G,
`__fern_str_eq` 0.65 G.
