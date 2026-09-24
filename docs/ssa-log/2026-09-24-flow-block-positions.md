# The liveness flow carries its block positions

`ssaunits` turned an edge's target block id into a position with
`block_index`, a linear scan of the function's blocks, once in `plan`, once
in `verify_edge`, once in `held_elements`, and once more inside
`edge_supplies` for the same edge. `ssalive.compute` already builds the
id-to-position table for the graph it analyses and threw it away. `Flow` now
keeps it as `at`, `ssalive.position` reads it, and `edge_supplies` takes the
position its callers already have.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,766,063,154 | 1,745,797,368 (−1.1%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical, and so is
the assembly the native-built compilers emit for `checker.fern` on x86-64
and arm64.
