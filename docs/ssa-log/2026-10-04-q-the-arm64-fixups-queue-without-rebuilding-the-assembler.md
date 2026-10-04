# 2026-10-04 — the arm64 fixups queue without rebuilding the assembler

`arm64_native`, arm64. Finishes slice 5 of #11452, refs #8171. It is the
arm64 twin of slice 1 for x86.

## The shape

A branch to a label that is not placed yet queues a fixup for
`arm64_asm_resolve`. Each fixup appended to four parallel arrays: `fix_offs`,
`fix_names`, `fix_kinds` and `fix_ids`. Every append rebuilt the
`Arm64Asm` struct, so one fixup cost four rebuilds, and a branch to a label
id still pushed an empty name.

The queue is now one builder, `fixq`, with one u64 per fixup:

- **Low word:** the branch's offset.
- **High word:** the target shifted left three, the kind in bits 2:1, and bit
  0 set when the target is a name.

`fix_names` now holds only the targets that are names. Resolve reads the
entries in place, and `arm64_fix_count` answers how many are queued.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under callgrind,
main at 07615458 against this branch. Both compilers build `checker.fern` to a
byte-identical binary for arm64-linux, arm64-darwin and arm64-android, and
`-g` builds match too:

| | main | packed queue |
|---|--:|--:|
| stage 2, arm64-linux target, total Ir | 19.287 G | 19.259 G (−0.14%) |
| `arm64_asm_fixup_id`, inclusive | 52.3 M | 18.5 M |
| `arm64_asm_fixup_or_patch`, inclusive | 29.2 M | 24.3 M |
| `arm64_asm_resolve`, inclusive | 86.2 M | 98.1 M |

Resolve rises because it now reads each entry back out of bytes, as the x86
resolve has done since slice 1.

## What is left

Slice 6 of #11452 remains on arm64: the record helpers still decode the
register names the SSA emitter hands them (`arm64_rec_gpr`).
