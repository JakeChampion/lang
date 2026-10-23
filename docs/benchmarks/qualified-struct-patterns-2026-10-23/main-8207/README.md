# Unicode-diagnostic integration

Base: 8207edece5bcf7bb94bc5f4192024abc94c367d4.

The lexer, qualified-pattern, and complexity suite passes in 29.427 seconds.
The lexer tests execute on x86-64, ARM64, and Wasm.
Both checkers, both interpreters, and all three targets retain the qualified-pattern results.
All seven opaque-pattern cases retain one E021 per checker.

The source-error, source-decoding, REPL, effects, and import-census tests pass in 4.272 seconds.
All 1,176 tracked Fern sources outside conformance and testdata pass the formatter.
Go vet also passes.

The review archive records the completed bootstrap and all eleven strict size checks before this main update.
These new checks cover the changed lexer and CLI paths.
