# 2026-09-30 — a view merged past its source is copied (#10796)

Self-host (`ssasem.merge_copies` and `ssasem.with_merge_copies`, called from
`semsource.anchor_module`).

A `str` local assigned, in a branch, a bare block or a loop body, a view of a
string declared there reaches the join as a phi operand whose source only one
incoming path defines. The phi's dependency on that source failed
`ssadeps.verify` ("dependency unavailable at use"), and the whole module fell
to the AST lowering. std/time's `Zoned.format_rfc3339` is this shape, so every
program that calls either `format_rfc3339` was refused, including the
`date_iso`, `time_leap` and `audit_std_time` fixtures.

Such an operand is now copied at the end of its incoming block, as a returned
view that outlives its source is (`docs/STR-VIEW-CONTRACT.md` §5). An operand
whose source strictly dominates the join is left a view.

## Measured (x86-64 sanitize, allocs / frees)

| program | before | after |
|---|---|---|
| a branch and a bare block (row below) | refused | 27 / 27 |
| a loop over a body-local source | refused | 38 / 38 |
| `time.instant_from_unix(90061).format_rfc3339()` | refused | 30 / 30 |

Each program gives the interpreter's answer on x86-64, arm64 and wasm.

## Tests

`TestSelfHostSemanticProduction`:

- `a-str-local-assigned-a-view-of-a-branch-local-is-produced`
- `a-str-local-assigned-a-view-of-a-loop-body-local-is-produced`
- `std-time-format-rfc3339-is-produced`
