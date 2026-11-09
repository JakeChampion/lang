# Semantic liveness map reuse

Status: [record] fixed-input comparison from 2026-11-09.

Semantic liveness rebuilt block positions and two definition arrays after checked graph facts already retained them.
The candidate supplies those maps to the shared solver while the graph remains unchanged.
Limits, dependency closure, dominance, and graph checks remain active.
Unchecked and verification-off callers retain the previous metadata construction.
No solver or ownership proof rule changes.

## Work counts

| Measurement | Baseline | Candidate | Candidate change |
| --- | ---: | ---: | ---: |
| Cold allocation events, assembly | 52,792,150 | 52,774,873 | -0.0327% |
| Cold guest instructions, native | 41,834,404,466 | 41,805,242,401 | -0.0697% |
| Warm guest instructions, native | 29,684,725,848 | 29,655,628,581 | -0.0980% |

Negative changes mean less work for this fixed input.
Both allocation runs free every allocation and report zero live bytes.
Allocation runs emit assembly.
Instruction runs emit native executables.
Output hashes match within each comparison.

Each compiler has one completed instruction run per cache mode.
These work counts do not establish a wall-time speedup or an external rank.
The [earlier certifier comparison](../compiler-liveness-main-2026-11-09/README.md) uses different sources and input.
Do not add reductions from these snapshots.

## Time and memory

Each compiler has five samples per cache mode.
The order alternates within each mode, and the warm mode starts with the other compiler.
Each sample completes native compilation.
The collector uses Python `perf_counter_ns` and Linux `wait4` resource usage for each compiler process.
Peak RSS uses KiB.

| Mode | Compiler | Median wall seconds (range) | Median CPU seconds | Median peak RSS KiB (range) |
| --- | --- | ---: | ---: | ---: |
| cold | base | 12.233 (11.550–40.630) | 12.231 | 609,776 (609,608–609,776) |
| cold | candidate | 12.247 (11.801–36.196) | 12.246 | 608,500 (608,460–608,500) |
| warm | base | 9.179 (8.884–10.756) | 9.177 | 606,960 (606,960–606,960) |
| warm | candidate | 9.332 (8.899–12.196) | 9.331 | 605,940 (605,940–605,940) |

The candidate has slightly slower wall-time medians in these samples.
Paired differences include positive and negative values.
Wall time and CPU time vary substantially on this shared host.
These samples do not establish a wall-time or CPU-time speedup.
Every sample remains in the [raw record](timing/samples.json), including the slow runs.

Median peak RSS decreases by 1,276 KiB cold and 1,020 KiB warm.
These observations cover this host and fixed input.
A separate Callgrind process was active during collection.
The bootstrap check starts after all timing samples finish.

The first collector could not start its first timed process because `/usr/bin/time` was absent.
Its four cache controls completed, but it produced no timing sample.
The [preflight record](timing-preflight/failure-note.txt) retains that attempt.
The replacement collector uses a new output directory.
No performance sample was discarded for its value.

## Sources and controls

The baseline compiler and fixed input source is `12be8f11864c00d71bc3ae9e0c3bc1ddabea75ef`.
The candidate code source is `3a296b887ccb1c879743cce8a2a67351c548481f`.
The pinned producer source is `38f241ca26366e5ef39a898c780bbfbc3a4694f5`.
The [manifest](manifest.json) records producer, compiler, cache, and data hashes.
The [work record](measurements.json) retains every command and exit status.
The [source patch](reuse.patch) includes the implementation and test changes.

All performance binaries use that same producer and omit `-g`.
Normal binaries disable CPU and allocation profiles.
Separate count builds enable `FERN_LEAKCHECK=1` at build time.
Ownership certification remains enabled in every measurement.
Cold runs disable only the proof cache.

Each normal compiler primes its own separate cache with equal-length paths.
The work probes report 2,407 baseline hits and 2,407 candidate hits, with zero misses.
The timing probes also report zero misses.
Actual measured runs use normal verifier defaults without the extra report flag.
The two collectors use separate cache and output paths.

Cold instruction runs execute baseline then candidate.
Warm instruction runs execute candidate then baseline.
Callgrind traces the compiler process without child tracing, cache simulation, or branch simulation.
The instruction comparisons and time comparisons check native output hashes.

## Correctness

Targeted dependency and liveness checks pass in 212.260 seconds with explicit Go exit zero.
The log contains 67 passing test and subtest results, with no skips.
Dependency checks compare legacy and reused-map flows and diagnostics on x86-64, ARM64 under QEMU, and WebAssembly.
Cases cover wide live sets, explicit parameters, sparse IDs, reverse storage, and invalid metadata.
Three native verification-off controls supply empty maps and exercise the original fallback.
Existing liveness, filtered-set agreement, and invalid-metadata controls pass.

Source checks, formatting, Go vet, and CI self-tests pass.
The test overlay removes the unavailable coredump-filter write and retains `ulimit -c 0`.
Verbatim logs and unified patch context retain their original whitespace.
These local checks do not substitute for a full CI verdict.

The candidate compiler rebuilds itself twice with certification enabled and the proof cache disabled.
The complete stage-2 and stage-3 executables are byte-identical.
Their SHA-256 is `67585e832dbec756767e59b0852c94b1bb3fa75b2f2d452a623241aa1df0bd51`.
The [bootstrap record](bootstrap/bootstrap.json) retains the commands, flags, and exit statuses.
Those rebuilt binaries are correctness controls, not the performance binaries in this comparison.

## Reproduction

Create clean checkouts of the recorded baseline and candidate revisions.
Use the producer identified by source and hash in the manifest.
Set local paths in [measure.py](measure.py) for those checkouts and a new output directory.
Run the script with Python 3.

Set the local paths in the [time and memory collector](timing/time-memory-rusage.py).
Run it after the two normal performance binaries are complete.
Run the [bootstrap check](bootstrap/bootstrap-check.py) after the timing samples finish.
Each script refuses to overwrite its existing measurement directory or record.
Retain all commands, raw samples, failures, and limitations.
