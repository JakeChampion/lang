# Payload struct pattern checks

The final focused suite passes in 25.756 seconds.
After the qualified-pattern review fixes, the integrated suite passes in 21.668 seconds.
It covers payload patterns, qualified records, existing nested patterns, pattern heads, and the complexity gate.
Both checkers, both interpreters, and all three runtime targets pass.
Seven invalid payload patterns retain their required diagnostics in both checkers.

Both formatter parity cases pass in 1.714 seconds.
The grammar derives all 2,405 parseable source files.
The census trace measures zero unmatched allocations for the new fixture.
The census-row gate passes.
All 1,176 tracked Fern sources outside conformance and testdata pass the formatter.
Go vet passes.

The complete Go checker and monomorphization suites pass.
The Go parser suite also passes.
The source records retain the measured scopes and times.
