# Packed string byte capacity measurements

Source: `45148d4a5feb8cd0a305dbec134fa50c3ea825a0`.
The common native string-byte helper extends the byte length before the capacity addition.
The [regression description](../../security/STRING-BYTE-CAPACITY.md) records the test scope and limits.

The complete reports contain all 78 native static rows and all 117 self-host static rows.
Only six values in each report differ from the previous snapshot.
The x86-64 helper adds seven emitted instructions to each affected benchmark.
The AArch64 helper removes one instruction from each affected benchmark.
The three affected benchmarks are `http_hello`, `utf8_ingest_unchecked`, and `utf8_ingest_validated`.
All 39 Wasm values stay unchanged.

Every tolerance stays unchanged.
The native retired-instruction values stay unchanged because this local run does not measure them.
The strict static comparisons pass all 78 and 117 measured rows.
These figures do not establish a runtime speed change.

## Commands and method

`make build` produces the shipped CLI and its self-host compiler.
`scripts/perf-bench-selfhost REPORT` measures all 39 benchmarks on its three targets.
The native static check compiles each benchmark with `bin/fern -target TARGET FILE`.
It counts lines with `grep -c '^[[:space:]]'`, as the native performance script does.
It measures all 39 benchmarks for `x86-64-linux` and `arm64-linux`.

The first native comparison uses the complete baseline with a static-only report.
It fails because the report has no retired-instruction rows.
The final static comparison uses all measured static rows and preserves every other baseline value.

The [validation record](validation.json) contains hashes, exact changes, and test limits.
The reports are [native static](native-static-report.txt) and [self-host static](selfhost-static-report.txt).
