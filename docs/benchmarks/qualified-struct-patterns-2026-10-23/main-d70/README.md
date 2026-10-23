# Validation on main d70db05

Base: d70db05173314181f6b99dc7d2073cc54b8c04db.
Source: caca898fc1ff9987d9294035f7062ec61e023f4a.

The focused suite passes in 53.235 seconds.
Both checkers and both interpreters accept the regression case.
The case runs on x86-64, ARM64, and Wasm with strict semantic lowering and a balanced allocation census.
The lane operations and pattern-head regressions also pass on their target legs.
All seven opaque-pattern cases report one E021 per checker.
The complexity gate passes without a baseline change.

The Go parser and module loader pass in 6.299 and 1.037 seconds.
Go vet reports no findings.
All 1,176 tracked Fern files in the formatter scope pass.

The parser separates plain initializers, qualified patterns, and the statement dispatcher into functions.
The checker separates scoped match and conditional rewrites.
These functions retain the previous statement bodies and do not relax the complexity limit.

This branch also supplies the missing array-cause argument in the Wasm lane bounds check.
Main d70db05 called the three-argument helper with two arguments, which prevented a compiler build.
The lane regression runs through the repaired compiler on all three targets.

Bootstrap and strict driver-size checks are in progress for this source.
The parent archive retains the earlier validation scope.
