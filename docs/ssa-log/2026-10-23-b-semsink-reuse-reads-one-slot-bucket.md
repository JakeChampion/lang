# 2026-10-23 — semsink.reuse reads one slot bucket

`semsink.reuse` in the semantic lowering. Refs #8171.

## The shape

`reuse` finds a counted read that repeats a read a dominating block made.
For each read, `earlier_read` examined every counted read of the
function, so the work was the square of the reads. It called
`ssadeps.dom_holds` before `semcse.same_slot` compared the two slots. The
self-compile made 9.4 M calls of `dom_holds`, and the scan cost about
550 M Ir.

## What changed

`reuse` chains the reads by a hash of what `same_slot` compares before
the parent: the kind, the index and the name. Each chain keeps the order
of the reads. `earlier_read` examines only the reads in the chain of
its read, with the same test as before. A read in another chain has a
different slot, so the first read that passes the test is the same read
as before.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | main at 4a45ac1 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 78.626 G | 78.103 G (−0.66%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

`util.minus_ones` costs about 306 M Ir. It fills an array one element at
a time, and `name_index`, `ssadeps.dominates` and the allocator call it
for each function.
