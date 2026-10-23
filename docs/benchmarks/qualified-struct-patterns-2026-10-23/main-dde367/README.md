# Encoder-table integration

Base: dde367dab55a1fbac2bc9e4cedd6cd871142942b.
Source: 9bbde32f3.

The six-file main update changes the native encoder tables and their tests.
The qualified-pattern, lane, register-spill, encoder-table, and complexity tests pass in 51.336 seconds.
Both checkers, both interpreters, and all three execution targets pass the qualified-pattern tests.
The Go table and generator packages also pass.

The formatter reports no changes across all 1,176 tracked Fern sources outside conformance and testdata.
Go vet and the whitespace check pass.
The main-4a45 archive records the preceding bootstrap, all eleven size rows, and the broader frontend suite.
The encoder update does not change the parser, checker, or those frontend tests.
