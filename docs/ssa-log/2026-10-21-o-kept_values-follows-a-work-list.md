# 2026-10-21 — kept_values follows a work list

`seminline.kept_values`, the inliner's closure of the values that
something other than a part read needs whole. Refs #8171.

## The shape

A kept copy or phi keeps what it copies. The closure was a fixpoint. Each
round rescanned every instruction of the function and marked the operands
of each kept copy or phi, until a round marked nothing. That is at least
two full scans per function, and more where copies chain: 93 M Ir on the
header of the inner loop alone in the whole-compiler self-compile.

## What changed

One pass locates each copy and phi by its result. It lists those whose
result is already kept. Each listed one marks its operands. When a mark
lands on the result of a copy or phi for the first time, the pass lists
that one too. So the pass visits each copy or phi once.

The closure is the same, so the kept set is unchanged. `all_marked`,
which only the rescan used, is gone.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at 2210347.

| subject | main at 53442b3 | placeholder op | and this |
|---|--:|--:|--:|
| `compiler/fern.fern`, the whole compiler | 73.741 G | 73.650 G (−0.12%) | 73.523 G (−0.30%) |

The placeholder-op column is the slice of the previous entry. This slice
is measured on top of it. The whole-compiler binary is byte-identical to
the base compiler's after each.

## What is left

`inline_leaves` still walks every instruction of every function per
round of the inliner, in `awaits` and in the resolve pass. Both are one
scan per round by design. The leaves that a round splices bound the
round.
