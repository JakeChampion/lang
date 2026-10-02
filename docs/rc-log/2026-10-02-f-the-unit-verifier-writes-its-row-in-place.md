# 2026-10-02 — the unit verifier writes its row in place

`ssaunits` verifier. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 9351c9ca, and the `checker.fern`
binaries the two stage-2 compilers emit are byte-identical.

## What the profile named

`__fern_arr_slice` was 440.7 M self Ir on the stage-2 compile of
`checker.fern`, and 157 M of it came from the plan verifier's row of
counted units: `move_supplies` (30.6 k copies), `replay` (17.1 k),
`drop_units` (14.1 k) and `enter_phis` (9.9 k) each wrote the row through
`.with` on a receiver the caller still held, so the runtime copied the
whole row for the first bit each of them cleared or set, once per step.

## What changed

A step is checked and then applied, by two functions instead of one.
`replay_error` reads the row and reports every rejection (`lent_error`
already did the same for lent operands); `apply_step` takes the row as an
`own` parameter and writes the moves, the result and the drops in place.
`verify_operations` threads the one row through every operation step with
the `next = apply_step(next, ...)` move; `verify_edge` and `enter_phis`
take it by `own` as well. `verify_block` hands the row on once: a copy to
the first of two successor edges (`copy_row`), the row itself to the last
edge. The return step is checked without applying it, since a unit it
leaves counted is one the row holds that the step neither moves nor
drops (`step_clears`). `verify_block` reports a `BlockProgress` of why
and count, since nothing read the row it used to return.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 9351c9ca and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 38.64 G | 38.45 G (−0.49%) |
| stage 2, `__fern_arr_slice` self Ir | 440.7 M | 316.7 M |
| row copies from the verifier | 71.7 k | 18.5 k |
| stage 2, `__fern_alloc` self Ir | 1,117.7 M | 1,105.9 M |
| stage 2, `ssaunits` verifier self Ir | 103.2 M | 103.2 M |

The verifier's own functions cost the same in total: the checking half
walks the step's lists where the old code walked them while writing.

The 18.5 k copies left are the 15.5 k `copy_row` makes for the first of
two edges and 2.9 k inside `apply_step`, where the row reaching
`verify_edge` from the block's `operations.state` is still held by that
record, so its first write copies.

## Witnessed

`TestSelfHostSSAUnits*` (the planner and verifier fixtures, the
rejection shapes included), `TestSelfHostIRVerifyRc`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, and the emit-hash
sweep.

## Next

On the same profile, self cost: `__fern_alloc` 1.11 G, `ssa_lift.lift_impl`
1.09 G, `__fern_str_eq` 1.02 G, `util.hash_bucket` 963 M, `__fern_arr_push`
816 M; `__fern_arr_slice` keeps 316.7 M, of which `live_out_row` 59.3 M and
`invariant` 56.8 M each build a fresh row by writing into `flow.none`,
`irlower.noesc_set_kill` 39.0 M, `ir.cp_kill_after` 27.9 M and
`ir.cp_loop_body_kill` 25.8 M, `ssalayout.release` 23.9 M, `add_use`
18.5 M. `holds_invariant` is 103 M on its own: it walks the whole row per
edge to count the units present. The `hash_bucket` and `str_eq` callers
are in the previous entry.
