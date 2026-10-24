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

Stages two and three match at 14,252,160 bytes.
Their SHA-256 is e172556e14d186c2ce5797077d097e17660aaaa7f434e80c39f3c1ea78a3af6c.
All eleven strict driver-size rows pass without baseline changes.
The ten stock-driver builds complete in 96.850 seconds.

The broader checker, pattern, value-block, semantic-execution, and consuming-call suite passes in 273.188 seconds.
The recorded source fingerprint matches the measured tree.

After main 8207ede, the payload, qualified-pattern, complexity, lexical-error, and source-decoding suite passes in 32.713 seconds.
Both checkers, both interpreters, and all three runtime targets pass the payload and qualified-pattern tests.
The bootstrap archive retains the source fingerprint before that Unicode-diagnostic update.
