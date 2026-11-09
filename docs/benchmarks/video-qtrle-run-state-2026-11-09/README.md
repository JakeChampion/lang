# QTRLE run-state allocation control

Date: 2026-11-09. Target: x86-64 Linux.

The decoder now validates a run with a scalar byte count before a pixel write.
It no longer returns an owned pixel array through a result object for each run.
The public API, buffer ownership, bounds checks, and error categories remain the same.
The new error tests check truncated operands and the priority of row-extent errors.

## Matched measurement

The parent source is `b9ba0a1a685d84103abd377440f41217dc3d33b6`.
Both variants use frozen compiler SHA256 `a3195d8efc590f62bb48ca7a3ba54ab6b9a03216f4a52afada3ddab3c2b1ebc3`.
The pinned stage0 builds that compiler from the parent source.

The [matched report](matched.json) contains every trial, source and binary hashes, and corpus identities.
Its tracked git tree identifies the parent because measurement precedes the commit.
Its per-file hashes identify the measured candidate decoder and harness.
The [initial baseline report](base.json) supplies the frozen binary identities and corpus hashes.
Its earlier timing samples do not enter the matched comparison.

Each trial uses the same four packets and canonical ARGB frames as the existing qualification harness.
Seven groups alternate execution order between parent, candidate, and FFmpeg.
Every process retains one current frame and completes one warmup corpus cycle.
Both Fern variants use the same compiler and measurement client.
The timed executables omit allocation instrumentation. Separate executables count heap allocations.

| Workload | Parent allocations per call | Candidate allocations per call | Reduction | Candidate / parent median time |
| --- | ---: | ---: | ---: | ---: |
| RGB24 runs | 436.50 | 74.50 | 82.93% | 0.916 |
| ARGB32 literals | 436.50 | 74.50 | 82.93% | 1.001 |
| ARGB32 HD sparse | 4596.75 | 272.50 | 94.07% | 0.908 |
| RGB24 HD dense | 13772.00 | 812.00 | 94.10% | 0.947 |

All median time differences remain within the repository's 10% shared-host noise threshold.
These results establish fewer allocations. They do not establish a speedup.

Median peak process RSS remains identical for each pair: 1.125, 2.25, 31.625, and 51.5 MiB.
The change does not establish lower peak memory, general video performance, device power, or lip-sync accuracy.
FFmpeg allocation counts remain unmeasured.

## Correctness and guard checks

Parent, candidate, both allocation probes, native sanitizer, FFmpeg, and WASI outputs match every canonical ARGB byte.
All timed checksums also match.
The native and WASI suites preserve malformed-input errors and retained previous-frame pixels.
Independent FFmpeg checks decode 24 Fern-encoded packets with exact ARGB equality.

The [harness controls](controls.json) reject a different compiler, allocation instrumentation, changed client source, and changed baseline binary.
Normal smoke mode still passes without a baseline.

The [small benchmark control](bench-control.json) preserves its checksum and reduces its allocation count from 1125000 to 325000.
That benchmark includes allocation instrumentation. Its Callgrind counts are not production timing results.
Its local x86-64 instruction and cross-target emitted-size counts update the corresponding baseline rows.
The native aarch64 instruction count remains pending CI measurement.
No tolerance changes enter this work.

## Reproduce

Build one compiler from the parent source. Preserve that executable for both decoder variants.
Use FFmpeg, its development headers, a C compiler, and the pinned Fern toolchain.

Run this command in the parent checkout:

```sh
FERN=/path/to/frozen-compiler scripts/compare-video-qtrle --pairs 3 --wasi --output /tmp/qtrle-base.json
cp -a build/video/compare /tmp/qtrle-base-binaries
```

Run this command in the candidate checkout:

```sh
FERN=/path/to/frozen-compiler scripts/compare-video-qtrle --baseline-report /tmp/qtrle-base.json --baseline-binaries /tmp/qtrle-base-binaries --pairs 7 --wasi --output /tmp/qtrle-matched.json
```

The new optional baseline mode checks compiler, client, corpus, and binary identities before each comparison.
