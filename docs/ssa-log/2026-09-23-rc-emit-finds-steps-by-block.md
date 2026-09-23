# The rc emitter finds its steps by block

`ssarc.at`, which the rc lowering calls for every operation, entry, return
and edge step it emits, scanned the whole unit plan for the step, so a
function with n steps paid n² comparisons. It now asks `ssaunits.step_at`,
which scans only that block's steps through the index
`ssaunits.step_index` builds once per function (the `steps` field of
`Emit`); the verifier's `find_step` already reads the same index.

| | before | after |
|---|---|---|
| `lexer.fern` compile, native-built compiler, Ir | 2,963,132,001 | 2,867,420,886 (−3.2%) |

The baseline is the tree with `2026-09-23-quadratic-checks-in-the-rc-plan.md`
applied; the emitted assembly is byte-identical. Answering an id-equals-position
lookup first in `ssaunits.block_index` and `ssarc.block_index` moved the same
compile by a further 0.2% and is not taken.
