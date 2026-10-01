# 2026-10-01 — the semantic analysis of a produced function is made once (#10913)

`semsource.anchor_module`, `semsource.infer_rows`, `ssaunits.plan`,
`seminline.inline_leaves`, `ownership.carry`, `ir.zg_mark`,
`ssalayout.absorb`. Refs #8171.

`2026-09-27-b-two-quadratic-scans-in-the-typed-path.md` ended on
`ssasem.analyze` recomputed for one function by the inference and the planner
as the next item. This is that item.

## Three analyses of one graph

On the `checker.fern` compile `ssasem.analyze` ran 5,643 times for about
1,700 produced functions: `anchor_module` verifies every graph once the
anchors are known, `infer_rows` analysed every eligible row again to seed the
ownership fixpoint, and `ssaunits.plan` analysed every row a third time. The
analysis is a function of the graph, the types and the anchors. Between the
anchoring and the planner only two things move: `inline_leaves` rewrites some
graphs, and the inference rewrites contract modes, which the analysis reads
through closure contracts alone, and those the inference never touches.

So `Produced` carries `analysis: Option[ssasem.Analysis]`: `Some` from the
anchoring, `None` before it and again on every row `inline_leaves` rewrote
(`seminline.inline_leaves` returns `Inlined` with a `rewritten` flag per row,
from `splice_calls` and `split_tuples` each answering a `Rewrite`). The
planner (`semlower.plan_of`, `ssaunits.plan_analyzed`) and the inference
(`semsource.row_analysis`) read it, and analyse only a rewritten row. The
inliner rewrote 43 of the 2,361 rows the planner saw, so the compile now runs
1,713 analyses in the anchoring and a few dozen more.

## Rows cloned on every carry

`ownership.carry` and the six walkers above it updated a borrowed row with
`with`, which clones the whole row on every call; so did `ir.zg_mark` and
`ssalayout.absorb`, the last rebuilding the loop membership from a borrowed
field per block it absorbed. The family takes its row as `own`, as
`ssaunits.add_use` does. On the self-host-built compiler the
`__fn___fern_arr_slice` copies behind these three were 2.6% of the compile.
`semlower.row_reusable` also read `FERN_SEM_REUSE` from the environment on
every row of every round; `rows_of` reads it once.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, every row
on the same source. "Native-built" is the compiler `bin/fern` builds, "stage 2"
the one the self-host compiler builds from the same commit.

| | main (de9c191) | analysis to inference | analysis to planner | rows in place |
|---|--:|--:|--:|--:|
| native-built, total Ir | 74.16 G | 72.66 G | 70.23 G | 70.08 G (−5.5%) |
| native-built, `ssasem.analyze` inclusive | 5.54 G | 4.05 G | 1.63 G | 1.63 G |
| analyses per compile | 5,643 | 4,074 | 1,756 | 1,756 |
| stage 2, total Ir | 47.26 G | | 44.39 G | 43.65 G (−7.6%) |

`scripts/selfhost-alloc-bench` on the same compile: 69,645,786 allocations on
main to 66,549,063 (−4.5%), 59,749,208 frees to 56,753,466 (−5.0%); the
baseline is re-banked. The append cliff (`scripts/cliff-bench`) moves by
−0.4% of bytes and −0.1% of crossings.

Byte-identical throughout: the `checker.fern` binary, the whole compiler, and
all 1,941 `selfhost-emit-hashes` rows, against a compiler built from the main
each head merged.

## Traps

- The first cut handed the planner's analyses to the inference through a
  second channel, an array of plan analyses in row order, beside the field on
  `Produced`. Two carriers of one invariant with the invalidation written
  twice; the field alone serves both readers, since the inference walks the
  same rows the planner planned.
- `2026-09-22-the-inference-re-lowered-rows-it-did-not-move.md` is the other
  half of this seam: that slice stopped re-lowering unchanged rows, this one
  stops re-analysing the rows that are genuinely re-lowered.

## Next

On the stage-2 profile after this: a record update that reuses its donor in
place still loads and stores every field (`ssarc.reuse_construct`), about
900 Ir per `X86Asm` update in the assembler; the ownership inference's greatest
fixpoint keeps every parameter in a call cycle counted (`semtypes.equal` and
`equal_list`), and a loop phi over a projection of a lent value owns a unit it
does not need (`ssaunits.phi_ownership`).
