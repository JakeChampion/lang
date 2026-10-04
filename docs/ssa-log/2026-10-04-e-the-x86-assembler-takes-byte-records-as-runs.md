# 2026-10-04 — the x86 assembler takes byte records as runs

`x86_native`, x86-64-linux. Slice 6 of #11305, refs #8171.

## The shape

With the x86 SSA emitter's frames, moves and calls as records, the
assembler's input for `checker.fern` was 732,206 records and 77,540 text
lines, and each record became its own `GasLine`: a fifteen-field struct
built in `x86_gas_prepare`, appended to the line table, read by every
relaxation round through `x86_gas_bytes`, one call per record per round,
and dropped at the end. That is why the emitter slice before this one
measured −0.33% where the arm64 slices measured −2.5% and −4%: the x86
line memo had already made a repeated text line cheap, and a record cost
what a memoised line cost. `x86_gas_bytes` alone was 222 M, up from 120 M,
with the 200,000 lines that moved to it.

## What changed

Consecutive records are one line. `x86_gas_prepare` keeps an open run while
it meets `\x01` markers whose records the words reach, and closes it into one
`GK_BYTES` line over `[ext, run_end)` of the words when a line of any other
kind arrives: a label, a branch by id, a text line, or the end. A record the
words do not reach is still its own refusal line. `x86_gas_bytes` takes the
run's bounds and lays down every record in it in one walk, each a length
byte and its bytes, so a round copies the same bytes it copied before, from
one line instead of one per instruction. `GasLine` gains `run_end`.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container, main at 6a190a39, the emitter slice
(#11397) and this change:

| | main | the emitter's records | with runs |
|---|--:|--:|--:|
| stage 2, x86-64 target, total Ir | 20.684 G | 20.616 G (−0.33%) | 20.523 G (−0.78%) |

The binaries are byte-identical on both targets. `x86_native` falls from
1,612 M to 1,546 M; `x86_gas_bytes` from 192 M to 154 M, `x86_gas_prepare`
from 119 M to 104 M, the `GasLine` drops from 81 M to 70 M.

## What is left

The run lines removed the per-record struct work, and what remains in the
assembler's 1.55 G is its shape rather than its input. `x86_gas_bytes` is
154 M because it appends one `i32` per byte, and a round copies every byte
of the program: 3.3 MB, twice. The label and fixup machinery
(`x86_queue_fixup_at` 93 M, `x86_branch_record` 69 M, `x86_gas_link_labels`
64 M, `x86_place_label` 40 M, `x86_jcc_label_at` 44 M) is about 350 M,
relaxation (`x86_relax_*`, `x86_events_before`) about 180 M, and the line
table (`x86_gas_prepare`, the drops, `x86_filled`, the pass loop) about
290 M. The text lines still each take a `GasLine`: 11,262 `leaq .L(%rip)`,
the buffered leaf bodies, `movslq`, the `.cfi_*` directives, the labels by
name. Each becomes a record in its own slice; a single-pass layout that
writes records straight into the code and relaxes in place is the step
that removes the rest, and it is a redesign of the assembler's core rather
than a slice of it.
