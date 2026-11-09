# Three rewrites test each body once

Three passes test each body before they rewrite it:

- `checker.pretype_module` tests each body with `needs_pretype_or_rescope`.
- `checker.settle_literal_locals` tests each body with `needs_lit_settle`.
- The parser's index-call rewrite tests each body with `rta_has_index_call`.

Each pass first tested the bodies until one needed the rewrite.
Then it tested each body again to find which ones to rewrite.
So each body before the first hit was tested twice.

Each pass now records one answer for each body in a `BodyHits` value, or in a list in the parser.
The rewrite reads that answer, and the module environment is still built only when a body needs the rewrite.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,603,054,927 |
| this change | 29,584,611,763 (−0.06%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
