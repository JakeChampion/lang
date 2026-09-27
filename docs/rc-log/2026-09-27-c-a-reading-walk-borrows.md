# A recursive walk whose result holds no reference borrows

2026-09-27 — `ownership.call_carried`. Closes #10352. Refs #8171.

## The shape

The ownership inference is a greatest fixpoint, so a parameter whose only
carrying use is the function's own recursive call stays counted: the call's
slot is counted because the parameter is. For a walk that rebuilds that is the
point. `std/pvec`'s `__pv_with_in` takes its child out of a unique node, and
the greatest fixpoint made `pvec_with` 0.322x. For a walk that only reads, like
`checker.type_eq`, it is pure cost: every call retains both arguments and every
exit runs the uniqueness test and the release, since the caller keeps them
alive and no take ever fires. `type_eq` emitted 2,454 lines against the AST
lowering's 659.

## The rule

A self-recursive call carries nothing when the function's result holds no
reference. Nothing a take frees can leave such a frame, so counting the
parameter through its own call buys no reclaim. A rebuilding walk returns what
it rebuilt, so it keeps today's answer.

## Measured

At `afda94a`, x86-64:

| | before | after |
| --- | ---: | ---: |
| `checker.type_eq` emitted lines | 2,454 | 654 |
| `checker.fern` emitted lines | 603,992 | 602,540 |
| stage-2 binary | 11,699,123 B | 11,677,267 B |
| stage 2 compiling `checker.fern`, 3 interleaved pairs | 8.53 s | 8.46 s |

The bench corpus (28 of 29 programs, callgrind) is identical to the
instruction, `pvec_with` included.

A least fixpoint was measured too and rejected: `checker.fern` 591,555 lines,
but `pvec_with` 231,092,895 to 671,777,034 Ir (2.91x).

## Gate

`TestSelfHostOwnershipInference`'s emitted-mode program gains `depth` (a
reading walk that must borrow) and `bump` (a rebuilding walk that must stay
counted, and the marker the first assertion relies on). Without the rule,
`depth` calls `__sem_release_Tree` and the test fails.
