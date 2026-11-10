# ARM64 map baseline after main integration

Status: [record] exact CI evidence from 2026-11-10.

CI run `42743098908` tests PR #15022 at `206df7eeb094f5540ce1bd25cc2bd8090dae5df2`.
Its ARM64 performance job `129589649703` reports 230,251,856 guest instructions for `map_probe_chain`.
The previous baseline is 233,221,143 instructions.
The decrease exceeds the unchanged 1% tolerance, so the strict gate fails.
The baseline now records the reported value.

The [decoded CI log](perf-aarch64.log.gz) preserves the exact evidence through lossless gzip storage.
The [identity record](evidence.json) retains its original byte hash, sources, job, metric, and tolerance.
The benchmark uses integer keys and retains its existing checksum and loop counts.
The [before](baseline-before.txt) and [after](baseline-after.txt) files record the sole value change and its dated note.

This archive does not establish that the interpreter change caused the decrease.
It retains a native ARM64 CI measurement, with no local ARM64 instruction measurement.
The producer is the pinned ARM64 stage0 `a19e87208c` that the CI log identifies.
Other source pairs retain their earlier results.

The existing [performance gate self-test](perf-gate-selftest.log.gz) passes all 22 cases.
Its [result](perf-gate-selftest-result.json) records exit zero.
The benchmark, comparator, tolerance, ownership certification, and proof rules stay unchanged.
Exact-head complete CI remains the merge gate.

The [manifest](manifest.json) hashes every data file.
The README and manifest itself are outside that hash list.
