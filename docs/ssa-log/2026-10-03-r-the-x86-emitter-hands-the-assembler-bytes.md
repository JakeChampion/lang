# 2026-10-03 — the x86 emitter hands the assembler encoded bytes

`asm_ir` and `x86_native`, x86-64-linux. Slice 2 of #11305, refs #8171;
the arm64 half is the previous entry.

## The shape

The x86 emitter writes every instruction as GAS text and `x86_gas_assemble`
reads it back. Unlike the arm64 assembler it memoises repeated lines: a
repeated `movq %rax, %rcx` costs one hash of the raw text and a byte copy,
so the text round trip is cheaper here to begin with. Compiling
`checker.fern` for x86-64 under the stage-2 compiler, `x86_gas_assemble`
was 2.76 G of 23.9 G, of which the prepare pass was 1.04 G.

## What changed

When the output is a binary the emitter gets the same byte buffer the arm64
emitter has (`EmitState.recs`), and three marker lines carry what it knows
without the assembler re-deriving it from text:

- `\x01` takes the next record of the buffer: a length byte, then the
  instruction's bytes; since 2026-10-04-e consecutive records are one line, a
  run. The assembler reads the record straight from the
  buffer by offset (`GK_BYTES`), in every relaxation round, and never caches
  or memoises it.
- `\x02name` defines the label `name`.
- `\x03insn` is an instruction line with nothing to trim and no comment, so
  it skips the memo lookup, the comment scan and the label scan.

The shapes recorded as bytes: `movq` between registers and to or from a
frame slot; the group-1 ops (add, or, and, sub, xor, cmp) between
registers, from an immediate into a register or memory, and between a
register and memory, including the fused compare before a branch; `testq`
of a register with itself and `testb` of a register's low byte; the heap
floor compare. Labels and the jumps, conditional jumps and calls to them
go as marks.

Each `x86_rec_*` takes register NUMBERS and displacements, not names: the
first attempt took the operand text and parsed it with the assembler's own
register and memory parsers, and that moved the parse from the assembler,
where the memo paid it once per distinct line, to the emitter, where it was
paid on every instruction. That version measured 1.1% slower than the text
path. The emitter maps its register names with `ssa_xreg` (a digit read for
`%r8`..`%r15`, a match for the rest) and builds no operand strings on the
record path; the text path still renders exactly the text it did before.
The assembler's own `mov`, group-1 and `test` arms call the same encoders,
so a record is the bytes its text would have assembled to.

A label or branch name is taken as the emitter composed it, from its label
prefix or through `sanitize_label`, with no scan: the first version checked
every byte of every name against what the text path reads as more than a
name, and that scan alone was 203 M Ir on the stage-2 compile, 0.85% of the
whole and more than the byte records saved. The arm64 check keeps only the
numeric-local test (`1f`), which the text path resolves differently.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Stage 2 is built from main at a4d8eba1
and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 23.939 G | 23.576 G (−1.51%) |
| stage 2, arm64 target, total Ir | 23.745 G | 23.656 G (−0.37%) |

The arm64 row is the label scan alone: that target's records were in place
before this change. The steps on the way, on the x86-64 target:

| variant | total Ir |
|---|--:|
| mov, group-1 and test records from operand TEXT (against 23.798 G) | 24.068 G (+1.1%) |
| label and branch marks only, no byte records (against 23.798 G) | 23.719 G (−0.33%) |
| records by register number, names scanned (against 23.939 G) | 23.780 G (−0.66%) |

Both targets' binaries are byte-identical; `scripts/selfhost-emit-hashes` matches
main for every (fixture, target) pair. `TestSelfHostWordsMatchText` now
runs the one-process text-versus-records comparison for both native
targets; its x86 leg puts 13,931 instructions of 210,323 code bytes through as records.

## What is left

`pushq`/`popq`, `call *%reg`, `leaq`, `movl`/`movzbl` and the SSE forms
are still text, as is the hand-written runtime. The marker lines are still
parsed by `x86_gas_prepare` into a `GasLine` per line; #11305's later
slices retire the text parse for generated code altogether.
