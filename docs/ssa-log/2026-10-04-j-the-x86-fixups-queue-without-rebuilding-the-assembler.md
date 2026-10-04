# 2026-10-04 — the x86 fixups queue without rebuilding the assembler

`x86_native`, x86-64-linux. Slice 1 of #11452, refs #8171.

## The shape

Every reference the x86 assembler cannot patch on the spot is queued: a
fixup for each branch displacement and rip-relative address, and a branch
record for each relaxable jcc and jmp. Both lived as parallel arrays on
`X86Asm` (`fix_offs`, `fix_widths`, `fix_names`, `fix_idx`; `br_offs`,
`br_names`, `br_idx`, `br_shorts`, `br_n`), and each one queued rebuilt the
whole 29-field struct to append to four of them. Compiling `checker.fern`
queues about 414,000 fixups and 305,000 branch records per round, two
rounds, at about 200 Ir each: `x86_queue_fixup_at` 93 M and
`x86_branch_record` 69 M, with the struct drops behind them.

## What changed

Each queue is a builder handle on the struct, allocated at its first entry,
and an entry is one `buf_push_u64` that leaves the struct as it was: the
patch offset in the low word, and in the high word the label table index
shifted left two, with a bit for a one-byte rel8 patch (a short branch for
a branch record) and a bit for a reference that arrived by name alone. A
by-name entry's index is into `fix_names` or `br_names`, which hold only
those; on the assemble path nearly every reference has a table index.
`x86_resolve` takes both buffers. It reads the fixups in place as it
patches them, since nothing reads them again, and splits the branch
records into `br_offs` and `br_hi` for the relaxation passes, which read
them several times each. The branch count is the buffer's length during a
round and the array's after it.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container, main at 333339f6:

| | main | with the queues |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.376 G | 20.353 G (−0.12%) |

`x86_native` falls from 1,449 M to 1,422 M. The writers fall from 162 M to
37 M, and the `X86Asm` drops from 11 M to nothing; `buf_push_u64` rises
from 7 M to 23 M. Most of what they saved goes to reading the entries back
out of bytes: `x86_resolve` rises from 27 M to 59 M, `x86_le32_split` is
32 M, and the counts and accessors about 21 M. A first cut that read every
entry back through `x86_words_i32`, and recounted the branches in every
loop condition, was a regression (20.417 G); splitting once and hoisting
the counts is what turned it.

The binary is byte-identical on both targets and
`scripts/selfhost-emit-hashes` matches main on every (fixture, target)
pair.

## What is left

The byte round trip is the cost this slice left in place: an entry is
eight bounds-checked byte loads to read back. Laying the code down as bytes
(the next slice of #11452) gives the assembler a byte buffer it already
owns; the same treatment for the queues, entries the round reads as words
rather than bytes, removes the rest.
