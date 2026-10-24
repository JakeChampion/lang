# Byte reader names and formatter checks

Issues: #12708, #13068, and #13069.
Code commit: `a6c41c4be14334a31d1207c137e374a2018379aa`.
Base commit: `7394f435de7353bdbb2ad29413c236dd5bf7bb21`.

The byte reader is `ByteStream` and the generic async channel is `Stream[T]`.
The self-host formatter retains checked casts with their precedence and parentheses.
The Go formatter retains inline comments after tuple and record declarations.

## Source and producers

The compiler and standard library source commit is
`e3bd61f9e4acb0a88d11df490738b404da3283fc`.
The later code commit changes the Go formatter, its tests, and the validation note.
The compiler and standard library have identical contents in both commits.
Their source digest is
`40316214d3762ea853c5a803b7af57e9abe265babce9c7bb7b99ae33c691607e`.

The bootstrap starts from the exact pin in the base commit:
`6c5cdeed770e5db0aadbdb663efe2d29e3304acacb76229b951ca549e951f6e2`.
All `FERN_*` variables are removed for bootstrap and driver builds.
Stage 2 and stage 3 are identical at 14,269,312 bytes.
Their SHA-256 is
`30986923323a80d003f2cd94fc75da39347a920913ace7473f9ad18452f6b20c`.
The manifest records producer and output digests.

## Checks

- All 11 strict binary-size gates pass against the unchanged baselines.
  The largest increase is 2.5%, within the 5% limit.

- The runtime and formatter matrix passes all 72 commands.
  It covers both interpreters, both formatters, and x86-64, ARM64, and WASI binaries.
  Every compiled run enables strict IR, sanitizer checks, and leak checks.
  All runs have equal allocation and free counts with zero live bytes.

- The selected fixture suites pass 24 cases across six runners in 71.946 seconds.
  These include both interpreters, all three targets, and x86-64 quarantine.
  The fixture format check also passes.

- Formatter parity passes all 102 cases in 8.903 seconds.
  The printer's own tests pass on x86-64 and ARM64.
  Dedicated Go tests retain inline comments after tuple, record, and split declarations.

- The Go printer package passes in 27.309 seconds with the slow corpus type-check test excluded.
  The command uses `-skip '^TestFormatCorpusOutputTypeChecks$'`.
  That test has a recorded baseline timeout in the earlier control-value work.
  Its exclusion changes no repository test configuration.

- The complete checker, parser, source-lint, and AST packages pass.
  `go vet` passes for the checker, AST, and printer packages.
  The formatter accepts all 1,178 tracked Fern files in the standard format gate.

## Records

`manifest.json` records file lengths and SHA-256 digests.
Large records use gzip without a timestamp.
The source patch passes reverse application against the code commit.
The raw formatter failures are stored as compressed text.

The matrix records every command, exit code, output, and diagnostic.
The Go logs retain individual fixture, formatter, and printer test results.
The size report includes every driver from the strict gate.
