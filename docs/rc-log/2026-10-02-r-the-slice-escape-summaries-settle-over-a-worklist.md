# 2026-10-02 — the slice-escape summaries settle over a worklist

`checker.check_module_pass` (the E063 summary fixpoint), `slc_call_names`,
`slc_calls_any`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 189a9417. (The `checker.fern`
binaries differ, as they must: the change is in `checker.fern`.)

## What the profile named

`slice_escape_diags` was called 20,695 times on the 31.08 G stage-2
compile of `checker.fern`, 442 M, for a module of 2,124 functions: the
E063 summary fixpoint walked every function on every round until a
round changed nothing, about ten rounds, and the last round was the
confirming one. A function's summary reads only its callees'
summaries, so on every round after the first, most of those walks
read nothing that had changed.

## What changed

The fixpoint is the one `ow_result_borrows` already runs: the first
round walks every function, and each later round walks only the
functions calling a name whose summary changed in the round before.
The call names of each body are collected once (`slc_call_names`: the
callee of a plain call, the method name of a method call), and a round
tests them against the changed names (`slc_calls_any`). Both sides
are the bare name: the last segment of the key a walk reads through
`view_callee` (`recv.field` for a method call, whose summary
`view_sum_key` keys `Base.field`) and the `fn.name` of a changed
summary, so a body naming none of the changed set reads nothing that
changed, and two types sharing a method name only over-include. A summary
only ever gains a flag, so the least fixed point the rounds reach is
the one the full walks reached. The per-function diagnostic walk after
the fixpoint is as it was.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 189a9417 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.08 G | 30.94 G (−0.46%) |
| stage 2, `slice_escape_diags` inclusive Ir | 442 M | 151 M |
| stage 2, `slice_escape_diags` calls | 20,695 | 5,738 |
| stage 2, `slc_call_names` + `slc_calls_any` inclusive Ir | 0 | 78 M |

## Witnessed

`TestSelfHostChecker*` (the codes and differential suites cover E063
over the corpora), `TestSelfHostFrameViews`,
`TestSelfHostLentViewHandback`, `TestSelfHostSemanticSourceRC`, the
lint ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

The E065 summary fixpoint (`e065_sums`) has the same shape.
`check_module_pass` is 3.30 G: `own_diags` 858 M (`ow_result_borrows`
375 M, `ow_stmts` 200 M, `lu_last_use_args` 152 M), `stmts_call_diags`
617 M, `stmts_assign_diags` 229 M, `check_func_body` 204 M,
`e049_diags` 202 M.
