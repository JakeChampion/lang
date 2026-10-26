# Wasm runtime static snapshot

Status: [record] measurements from main `c03b8473e4a1458210d978ca2f4f581c7cba090f`.

The runtime source port changes all 39 Wasm rows in the complete 117-row report.
There are 31 reductions and eight increases in the emitted WAT line-count proxy.
All 78 native rows remain unchanged.
Every tolerance remains unchanged.

The source-generated string helpers use linear WAT instead of the previous folded text.
The growth cases were also assembled and validated with `wasm-tools`, then executed with Wasmtime.
Each retains the prior compiler's exit status and stdout.
`validation.json` records their module byte sizes and the compiler hashes.
These checks do not measure dynamic performance.

The complete report is `selfhost-size-report.txt`.
Reproduce it with `scripts/perf-bench-selfhost REPORT` from the stated source.
Compare it with `FERN_CI_PERF_GATE_STRICT=1 scripts/ci-check-perf .github/perf-baseline-selfhost.txt REPORT`.
