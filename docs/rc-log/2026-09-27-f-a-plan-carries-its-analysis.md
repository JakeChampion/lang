# A unit plan carries the analysis it was planned from

2026-09-27 — `ssaunits.Plan.analysis`, `ssaunits.verify_planned`,
`ssarc.bare_flags`. Refs #8171.

`ssasem.analyze` was 9.3% of a `checker.fern` compile to a binary
(9.77 G of 105.3 G Ir, a `-g` build of the self-host compiler under
callgrind). It runs up to five times on each produced function. Here is
the share of each call site:

| Call site | Share |
|---|--:|
| `ssaunits.verify` | 2.52% |
| `ssaunits.plan` | 2.45% |
| `semsource.complete` | 1.54% |
| `semsource.infer_rows` | 1.44% |
| `ssarc.bare_flags` | 1.33% |

The first two see different functions. Returned views are copied, anchors
are added and the inference rewrites the call table between them. The
last three see the same final function: `semlower` plans `p.func`, and
lowers and signs that same `p.func` with that plan. A plan reused from
the previous inference pass goes only with a row the inference did not
move, which is already what lets its body be reused.

`analyze` is a function of the function alone. So `plan` now keeps the
analysis it computed. `bare_flags` reads it, and so does the production
verifier, through `verify_planned`. `ssaunits.verify(f, modes, p)` still
analyzes `f` itself. Its tests edit `f` after planning to check that the
verifier notices, and a stored analysis would not see the edit.

## Measured

`checker.fern` to a binary, callgrind on a `-g` build at `e74448e8`:
**105.32 G to 101.25 G Ir (-3.9%)**. `analyze` falls to 5.72 G. The
emitted binary is byte-identical.
