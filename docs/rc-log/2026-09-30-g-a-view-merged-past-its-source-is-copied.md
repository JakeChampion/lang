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
view that outlives its source is (`docs/STR-VIEW-CONTRACT.md` §5). A container
phi (`str[]`, a tuple, an `Option[str]`) built in the branch around such a view
had the same refusal; its views are copied where the construction takes them.
A literal or retagged string anchors nothing and is not copied. An operand
whose source strictly dominates the join is left a view; the allocation parity
row below fails if it is copied (21 allocations become 34). A container that a
call returns, anchored to a string local to the branch, has no copy form and is
still refused.

The same PR roots core/map's functions for a map a program reaches only as a
`JsonValue` (`treeshake.ts_reaches_map`), which `std/json`'s `json_encode` needs.

## Measured (x86-64 sanitize, allocs / frees)

| program | before | after |
|---|---|---|
| a branch and a bare block (row below) | refused | 27 / 27 |
| a loop over a body-local source | refused | 38 / 38 |
| `time.instant_from_unix(90061).format_rfc3339()` | refused | 30 / 30 |
| a `str[]` appended a view of a branch local (row below) | refused | 7 / 7 |
| a tuple, option and array of views of a loop-body local | refused | 71 / 71 |

Each program gives the interpreter's answer on x86-64, arm64 and wasm.

## Tests

`TestSelfHostSemanticProduction`:

- `a-str-local-assigned-a-view-of-a-branch-local-is-produced`
- `a-str-local-assigned-a-view-of-a-loop-body-local-is-produced`
- `std-time-format-rfc3339-is-produced`
- `a-str-array-built-from-a-view-of-a-branch-local-is-produced`
- `a-str-array-built-from-a-view-of-a-dominating-local-is-produced`
- `a-tuple-option-and-array-of-views-of-a-loop-body-local-is-produced`
- `a-map-reached-only-through-a-jsonvalue-is-produced`
- `std-json-encode-is-produced`

`TestSelfHostSemanticAllocationParity`:
`a-view-of-a-dominating-source-is-not-copied`.
