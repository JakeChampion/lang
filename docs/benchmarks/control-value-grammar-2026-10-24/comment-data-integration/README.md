# Grammar checks after the current main merge

The source commit is `7b9bcc9a3e9aba8ffbd25020e13a0f27795aa45b`.
It retains the owner comment cleanup and the Map iterator changes.
The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `489e47e90393b87a746fd6d685f778cd258d65a3fa85e974eaa139e3e59841b4`.
All three compiler images match the earlier grammar build byte for byte.

All 563 focused native compiler cases pass without failures or skips.
The selected Go suites run 3429 cases across seven packages.
All 3418 applicable cases pass. Eleven synthetic-AST cases skip.
The named Go selector extends the earlier proof with the current source checks.

The matrix passes all 190 commands.
It checks both interpreters, both formatters and four target builds.
All 30 runtime allocation counts balance with zero live bytes.
All 54 diagnostic checks preserve code parity between the Go and native checkers.

All 11 strict driver size gates pass without baseline changes.
All 490 compiler and library Fern files pass the canonical format check.
Go format and Go vet pass.
The manifest records every native and Go source file, input and output log.

An additional full printer corpus attempt reached its local 1200-second timeout.
The raw output is retained in `go-regressions.jsonl.gz`.
The selected regression results use their separate selector and complete output.
