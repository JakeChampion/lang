# PPS qualification after the CI repair

These are historical reports for feature head `5944dc03b4` and compiler repair `ffda9726b6`.
The repair owner withdrew that compiler change after native ARM measurements showed regressions.
Issue #15093 records the cause and the separate compiler work.
The current media branch removes that withdrawn code and its 44 baseline updates.
Current qualification must use the new producer and current source, without these obsolete baseline pins.

PR #15089 reuses the published repair from PR #15087 at `ffda9726b6`.
The repair corrects array-view inline rules and array reads through lends.
It also restores the HTTP target fast path and corrects the TLS resumed-client expectation.
The PPS reader does not occur in these benchmark imports.

The repaired compiler produces identical benchmark code with the feature and repair source trees.
The controls cover five affected benchmarks on x86, ARM, and WASI.
`controls.json` records all 15 identical pairs and the frozen compiler hash.
The control worktree contains the published repair without the PPS feature.

The local reports contain 80 native, 120 self-host emission, and 180 profile metrics.
Callgrind 3.24.0 supplies the x86 retired-instruction counts.
Cross-emitted ARM assembly supplies only static instruction counts.
Native ARM retired-instruction counts require CI and remain unchanged here.

`measurements.json` records each changed baseline row, its measurement source, and the compiler hashes.
The update records both smaller emitted code and larger retired-instruction counts.
All tolerances remain unchanged.
The HTTP default-profile measurements return within the current limits after the repair.
These reports do not establish an isolated PPS, codec, or device performance gain.

The local tools ran these report commands:

```sh
scripts/perf-bench native.tsv
scripts/perf-bench-selfhost selfhost.tsv
scripts/profile-costs profile.tsv
```

The controls use the frozen stage-one compiler with each source tree:

```sh
compiler -stdlib TREE/internal/stdlib -target TARGET -emit asm -o OUTPUT bench/BENCHMARK.fern
```

The report comparison requires nonempty files and `FERN_CI_PERF_GATE_STRICT=1`.
All 380 report metrics pass after the measured baseline updates.

The branch also corrects the Android emulator harness for Paper Back navigation.
The harness requires keyboard dismissal, a fresh home transition, and the rendered home screen.
Six host protocol tests and the Paper control suite pass locally.
The live emulator verdict remains a required CI result.
