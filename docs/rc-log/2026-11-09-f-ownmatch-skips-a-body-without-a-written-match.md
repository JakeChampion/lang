# Ownmatch skips a body without a written match

`ownmatch.source` gives back the match that a statement was written as, before the ownership walk in `checker.own_diags` reads the body.
The parser keeps that match in the `sugar` field of a desugared `match` or `if let`.
`source` mapped every function body through `astwalk.map_stmts_where`, also when no statement had sugar.

`source` now returns the body unchanged when `sugared` finds no statement with sugar.
`sugared` is a read-only fold over the same statements as the map walk.
For a statement without sugar, the map walk changes nothing.
It gives back the same subtree, so the result does not change.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,653,427,843 |
| this change | 29,581,158,653 (−0.24%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
