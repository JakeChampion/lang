# 2026-10-02 — the verifier checks the modes of a planned function

`ssaunits.verify_analyzed`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 1745ab36, and the `checker.fern`
binaries the two stage-2 compilers emit are byte-identical.

## What the profile named

`contract_error` was 535 M of the 31.82 G stage-2 compile of
`checker.fern`, half of it under `plan_analyzed` and half under
`verify_analyzed`: the verifier ran the planner's contract check again
on the same function, and `schema_fields_error` (422 M) walks every
field of every record and enum in the function's tables asking
`supported` of each.

## What changed

`verify_planned`, the entry a caller uses on the very `f` it planned,
checks the parameter modes, this call's own input (the dimension test
and `mode_error`), and takes the rest of the contract from the plan: a
plan that is `ok` is `contract_error`'s answer on the values, result,
schema tables and calls of `f`, which are functions of `f` alone.
`verify`, which may check a plan against a function other than the one
it was planned from (the units driver mutates `f`'s calls after
planning and expects the verifier to notice), keeps the whole contract
check. The verifier's own derivation of the units (`units_of`,
`owned_values`, `units_analysis`) and its comparison with the plan's
`takes`, `held` and `payloads` are untouched on both entries: that is
the check of the plan, and it reads nothing from the plan it compares.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 1745ab36 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.82 G | 31.56 G (−0.84%) |
| stage 2, `verify_analyzed` inclusive Ir | 1.14 G | 0.87 G |
| stage 2, `contract_error` inclusive Ir | 535 M | 267 M |

## Witnessed

`TestSelfHostSSAUnits*`, `TestSelfHostFieldAppendGrowth*`,
`TestSelfHostGrowSoleOccurrence*`, `TestSelfHostSemanticSourceRC`, the
lint ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

The planner's `schema_fields_error` is still 211 M: for every function
it asks `supported` of every field of every entry in the function's
tables, and the tables are closed over their fields, so a nominal field
type is always in them; the answer is a property of each entry.
`ssarc.types_error` walks the same values and calls with its own
predicate, 293 M. `verify_block` is 483 M over 42,915 blocks and the
planner's `choose` 95 M over 50,902 calls.
