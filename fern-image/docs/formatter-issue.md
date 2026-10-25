# Compiler issue: divergent match arms lose source form

Status: fixed locally in `compiler/printer.fern`.
This is a local issue record. No GitHub issue was published.
This file records the issue and its reproduction.

The original formatter converts a match value with a divergent arm into a lambda.
It also duplicates the return statement in that arm.
Native compilation then rejects the formatted program.
The defect affects the downloaded nightly and the initial compiler build from this checkout.

The complete reproduction is `experiments/format-match.fern`.
It tests both the success arm and the early error return.
Run these commands from this package directory with the original compiler:

```sh
fern -target x86-64-linux -o build/original experiments/format-match.fern
./build/original
fern -fmt experiments/format-match.fern > build/formatted.fern
fern -target x86-64-linux -o build/formatted build/formatted.fern
```

The original program returns zero.
The formatted output contains an explicit lambda and duplicate returns.
The formatted program fails native compilation.

The fix reuses `print_match_expr_arms` for single-statement match lambdas.
That helper already handles divergent arms and their parser metadata.
It preserves the match expression and outer return semantics.
The change removes the narrower duplicate formatter loop.

The package test script compiles the formatted reproduction and executes both paths.
It also verifies formatter idempotence.
The compiler parity corpus includes the same reproduction.
The full Go parity harness did not run in this audit.
An additional check passed on all 107 literal formatter fixtures.
See the capability audit for its scope.
