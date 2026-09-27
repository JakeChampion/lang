# 2026-09-27 — a parameter read before its links grow is still a root

#10362's `link_roots` refused a borrowed array parameter with any use other
than a deferred append or a phi input. A `b.len()` before the loop, a
`return b` on a path that never grows it, or `b` lent to a call all made the
loop copy the caller's buffer once per call.

A temporary diagnostic over coreutils and the compiler's own source counted
the rejected parameters that feed an append chain and recorded the use that
disqualified each:

| disqualifying use | coreutils | compiler |
|---|---|---|
| `.len()` read | 10 (all `bigint.__bi_mul_small`) | 4 |
| returned unchanged on another path | — | 13 |
| passed to a call | — | 8 |
| element read | — | 1 |

## The rule now

A read is only unsafe if it can happen after a link has grown the box in
place, and liveness already answers that. A deferred append requires the
parameter dead after it (`Plan.kept`). `ssaunits.plan` now also records
`Plan.entry_kept`: a parameter carried into a phi on an edge while it, or a
value depending on it, is still live in the target. `ssalive` folds
dependencies into uses and counts a phi's inputs on the incoming edge, so
this is `live_in_has(flow, target, p)`.

`link_roots` then admits every borrowed, non-owned array parameter that is
not `entry_kept`. Any other read of it happens before any grow. `propagate`
checks the deferral itself for an append straight onto the parameter, which
the old per-use scan used to guarantee.

## Measured

A program with seven such shapes (`relax.fern`: a read before the loop, a
return before any grow, a read inside the loop, a return after a grow, a
call before the loop, an element read) matches `-interp` with leakcheck
balanced at 152/152. The push-cliff copy counter reads 100 on main and 7
here. The 7 are the kept-alias calls and the shapes that must still copy.

`loop_pre_fill(50)` reads 1044 (44 copies) under the old rule and 1000
under this one.

## Not covered

`bigint.__bi_mul_small` is the coreutils case, and it threads its
accumulator with `with`, not `append`, so it is unaffected: `with` has no
non-consuming helper to defer onto.
