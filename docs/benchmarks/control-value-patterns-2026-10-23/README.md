# Grammar and pattern value expressions

Status: [record] measurements for the implementation below.
Source paths and measurements in this record are historical.

Implementation: `1ea744aef243d647527862d38ea524f5e001ebde` and
`928bc1990bbb1ff42dbbe45d0ea72eea98f955d2`.
The source patch applies to `03ec8a6930345bf9d48c47e8028b2a7aad45c575`.
This base includes the qualified pattern work from PR #13032.

The parsers require blocks for loops. Both parsers accept expression bodies
in statement match arms, and both support `if let` expressions.
The grammar excludes unsupported field visibility and unconditional
`let` forms with `else`. It checks a complete value tail before a statement
can consume the same prefix. The language reference explains the explicit
`loop` and `if let` form instead of a separate `while let` form.

The compiler and standard library source digest is
`4102eee6da46732a68e883f336e9f8fe80bc19687061a1136c229949846469f7`.
`source-files.json.gz` lists the source files and their byte hashes.
The bootstrap started before the implementation commit, so its original
`source_git` records the base. Its source digest matches the committed
compiler and standard library exactly.

The locked stage-zero binary produced stage one at 13,509,536 bytes.
Stage two and stage three are identical at 14,281,376 bytes, with SHA-256
`a8e18179e9ecd80cbd6f8d68494e245941cb58aaaafce295d7ea571f91464b7c`.
All 11 driver sizes pass the strict gate with the unchanged baselines.
The largest increase is 3.1%, within the 5% limit.

The two committed compilers pass 162 commands over nine runtime fixtures.
The commands check types, interpret the programs, compare formatted output,
format that output again, and execute it. Each fixture also compiles and
runs on x86-64, ARM64 and Wasm with strict IR, sanitizer and leak checks.
Each run has equal allocation and free counts, with zero live bytes.
Some fixtures require no allocation.

Twelve negative checks confirm diagnostic codes in both compilers.
Forty loop checks confirm that both compilers accept blocks and reject
single statements at each loop entry point. The targeted Go compiler suite
passes. It also checks prior if-let and match value tests.

The grammar suite,
Go checker suite, source format check, test selection checks and vet pass.
The source format check covers 1,178 tracked Fern files outside conformance
and parser test data. New comments and reference prose pass the STE check.

The broad Go formatter corpus test reached its ten-minute limit during
whole-program type checks. Every other Go formatter test passes.
The same test also reaches its ten-minute limit on the dependency base,
`299fa8c1383cddc2b4da06c694a486258ccc61c5`. That base has identical compiler
and oracle sources to the patch base. Both raw timeout logs are retained. This result does not claim that the
complete Go formatter corpus type-check gate passed.

`manifest.json` records each proof file's length and SHA-256.
The command scripts use the paths under `/workspace/scratch/lang-grammar`.
`committed-compiler` points to the final stage-three compiler.
`committed-oracle` is the Go CLI built from the implementation source.
