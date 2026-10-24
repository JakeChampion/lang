# Record leaf checks after the supertrait fixes

The source commit is `e0d6b682597eb5628439dd40bbf95ae2de000d85`.
It includes the public CSV commit `a4f4ab3292da45da53eeb4be67ea612753b24077`.
The record leaf implementation retains its earlier source bytes.
The parser and target tests include the subsequent CI fixes.

All 244 focused test results and the feature census pass without failures or skips.
The tests cover record leaves, generic supertraits, parse positions and the complexity limits.
The SIMD matrix passes all 11 commands.
Both interpreters agree with all three execution targets.
All three allocation counts balance with zero live bytes.
Darwin passes its compile check.

Four pairs compare the updated CSV compiler with this compiler.
The source and library are identical for both builds.
Dual allocations fall from 21 per element to zero.
DoubleDouble allocations fall from 323 per element to zero.
Scalar allocations remain zero.

The report retains all elapsed time samples and executable hashes.
These samples ran with other local validation work, so they describe that host load.
The earlier performance report retains its original source scope.

The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `d294ee4d34be58c3f2cd8335c0ba382d39f4732ed99bef46624554038044fb24`.
All 11 strict driver size checks pass without baseline changes.
The changed parser and Go test files pass their format checks.
The manifest records all 496 compiler and library source files.
The archive retains all 38 execution logs.
