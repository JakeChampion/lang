# 2026-10-07 — each row is analysed once

`semsource.with_analyses`, read by `semlower.produced_rows`. Refs #8171. No
emitted byte changes.

## What changed

A produced row carries the semantic analysis (`ssasem.analyze`) its
anchoring made, so the planner can read it instead of analysing the same graph
again. A rewrite after the anchoring, such as `inline_leaves` splicing a leaf
into a caller, drops it. Three readers then each analysed such a row anew:

- the declared plans (`rows_planned`, through `ssaunits.plan`);
- the ownership inference (`row_analysis` in `infer_rows`);
- the inferred plans, for every row the inference moved.

On a `checker.fern` compile with `-g` the two plan rounds called
`ssasem.analyze` 630 times for 346 M, and the inference 296 times for 172 M.
Each call analysed a graph that had not changed since the last one.

`with_analyses` analyses each row that will be planned and carries none, once,
right after the build. The planner already reads a carried analysis after the
inference has rewritten a row's contract modes, so the analysis does not
depend on them, and the inferred plans can read it too.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built by its own stage 2.
The baseline is main at b6ab8c2b0. Both stage 3s rebuild themselves byte for
byte. The two compile `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, from main's sources to the same bytes.

| | before | this change |
|---|--:|--:|
| total Ir | 15.009 G | 14.674 G (−2.24%) |
| stage 3 size | 10,794,232 | 10,795,720 |

Keeping only the inference's analyses on the rows, so that the inferred plans
read them, measured 14.848 G (−1.07%). Analysing once before the first plan
also saves the declared plans' share.
