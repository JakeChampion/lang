# 2026-10-04 — the x86 branches carry their condition

`asm_ir` and `x86_native`, x86-64. Refs #8171, #11452.

## The shape

`ssa_branch` took a jump's mnemonic as a string, `"jne"` or `"jmp"`, and
with records on asked `x86_rec_jump_kind` for the kind a branch record
carries. That sliced the mnemonic after its `j`, copied the slice into a
fresh string, and matched it against the condition-code spellings one
string compare at a time, for every branch the emitter wrote. The emitter
always knew the condition: the mnemonics are literals or come from
`ir_cmp_jcc`'s table.

A jump is now an integer id. Its low five bits are the record's kind, the
condition code or 16 for `jmp`, so the record path is a mask. Bit 5 picks
the `jz`/`jnz` spelling of `je`/`jne`, so `ssa_jump_text` writes every
mnemonic the text path wrote before, and `-emit asm` output is
byte-identical. `ir_cmp_jcc` returns ids. `x86_rec_jump_kind` had no other
caller and is deleted.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 4183c442, both compilers building the same
`checker.fern` to a byte-identical binary:

| | main | jump ids |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.634 G | 20.586 G (−0.23%) |
| `ssa_branch`, inclusive | 74.5 M | 29.4 M |
| `x86_rec_jump_kind`, inclusive | 44.9 M | gone |

## What is left

The arm64 emitter has the same lookup. `ssa_bcond` and `ssa_cbz` hand
`arm64_rec_bcond` a mnemonic, which it checks, strips and parses back into
a condition code for every conditional branch. Passing the condition as an
integer there is the same change on the other backend, and sits beside
#11452's slice 6 (the arm64 record helpers take register numbers).
