# Record checks after the comment audit

The source commit is `ea66df3dc8cc918a6e3ca6591edea47a730e9493`.
It includes main commit `e9be823eb31bc028a1c58b12fdf3d55d2ae5a747`.
The merge retains the upstream comment revisions and both record test fixes.
All 489 compiler and standard library Fern token streams match the previous record build.
Go production source matches the new main branch.
This retains the upstream lexer reserve adjustment and generator output changes.

All 125 focused test results pass without failures or skips.
The five Go packages pass 2868 test results, with three intentional synthetic-AST skips.
The fresh Go oracle records a clean source tree.
The eleven-command SIMD matrix passes both interpreters and all three execution targets.
All three runtime allocation counts balance with zero live bytes.
Darwin passes its compile check.

Four pairs compare identical kernel source and library inputs on one host.
Dual allocations fall from 21 per element to zero.
DoubleDouble allocations fall from 323 per element to zero.
Scalar allocations remain zero.
The report retains all samples and executable hashes.

| Evaluator | Baseline median p50 | Candidate median p50 |
| --- | ---: | ---: |
| f64 | 178677 ns | 178075 ns |
| Dual[f64] | 2473958.5 ns | 329679 ns |
| DoubleDouble[f64] | 44525074.5 ns | 21643440 ns |

These measurements describe this host and workload.
The report corrects a copied source-commit label against the verified source and binary hashes.
All measured samples remain unchanged.

All three compiler stages are byte-identical to the earlier record build.
Stages 2 and 3 have SHA-256 `d294ee4d34be58c3f2cd8335c0ba382d39f4732ed99bef46624554038044fb24`.
All 11 strict driver size checks pass without baseline changes.
The changed Go files and compiler file pass their format checks.
The manifest records all 496 compiler and library source files.
