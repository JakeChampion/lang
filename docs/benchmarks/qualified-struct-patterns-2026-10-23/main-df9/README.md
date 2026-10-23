# Validation on main df9e0c111

Main now contains the shared CI repairs and the stale-test updates.
This branch keeps the main versions of those files.
The compiler and standard library differ from the verified `7b8b78382`
input only in comments. The comment diff is retained here.

A fresh pinned bootstrap produces identical stages 2 and 3.
All three bootstrap binaries are byte-for-byte identical to the earlier
verified binaries. Stages 2 and 3 contain 14,256,432 bytes with SHA-256
`eaa7bac0ffabd7d2d61078b47a81f78fba2204833196950a5704dd050e9e694c`.

The ten stock drivers use the same direct pin commands and clean environment
as `CompileWithSelfHost`. Every driver size equals the earlier measurement.
All eleven rows pass the unchanged strict 5% gate.

The fresh Go oracle and stage 3 pass all 24 direct feature checks.
These reproduce the existing qualified-pattern test: both checkers, both
interpreters, three compiled targets, and seven opaque-field cases in both
checkers. Native and core WebAssembly allocation censuses balance.
The Go parser tests also pass.

The earlier full verifier corpus remains scoped to its recorded input.
Its compiler binary is identical to this stage 3 binary.
