# Bulk initialization of scalar zero arrays

The compiler converts proven scalar zero-fill loops to the bulk allocation and zero kernels.
The pass retains the original empty array for zero and negative lengths.
It first checks exact loop cardinality, scalar elements, aliases, effects, and escaped values.
It skips nonzero fills, negative float zero, unknown fill values, mutable bounds, early exits, observable prefixes, handle elements, and extra effects.
The typed IR node `array_zero` retains the ordinary array layout and ownership checks.

This change removes repeated append work from unchanged codec sources.
MP3 retains its 960-slot QMF history and its output-buffer contract.
Issue #14849 tracks the compiler change under #14516 and #14508.
The separate storage experiment and its retained-memory cost remain in #14850.

## Compared sources and producers

The measured compiler source snapshot is `cb4f52a100cecd5ff5027959a59b935de4ec1690`.
The baseline compiler source is `15a62e417fe39ef4e8fd93942084db32f7a743b1`.
Both compilers pass the supported bootstrap and the stage-2/stage-3 fixed-point check.
The producer is the self-built stage-2 core with an explicit standard-library path.
The baseline core SHA-256 is `d6a2ad95872b4f003c43a60c980c58ccde31a1d4f833c95046cd7a75d38c42eb`.
The candidate core SHA-256 is `d6dee1df3654260c1ce5b875133869f76edebcfcac6b8c316ad39a6a2cc61e10`.

Both codec campaigns use the same frozen library and drivers from `86447a72aaf1ac9f7e225b51275bce2a9add311e`.
The report retains source hashes, compiler hashes, input hashes, executable hashes, and build commands.
The baseline source archive reproduces the previously qualified stage-1 core before the fixed-point check.
The bootstrap wall times in these logs describe qualification under concurrent local work.
The separate compiler campaign below measures time after that work completes.

## Matched MP3 campaign

The campaign uses one AMD EPYC 9V74 Linux x86-64 host.
The input contains one second of deterministic stereo audio at 48 kHz before encoder delay and padding.
The generator clears only the bitrate nibble of the indexed CBR headers to create the free-format control.
Both inputs contain identical coded audio payloads.
Each process performs two warmup rounds and 50 measured decodes.
Seven trials reverse execution order on alternate trials after other local build and test work completes.

The timed region includes a byte checksum of every output float.
Whole mode returns a complete raw PCM array.
Frame mode retains decoder state and reuses a 2,304-sample output buffer within each stream.
All Fern and scalar minimp3 outputs match every byte across formats, modes, and trials.
The maximum SIMD minimp3 difference is 0.0000001491.

| Workload | Median paired time ratio | Median time, prior / candidate | Allocations, both versions | Peak RSS KiB, prior / candidate |
| --- | ---: | ---: | ---: | ---: |
| Indexed MP3, whole | 0.9462 | 369.806 / 351.006 ms | 195,700 | 2,124 / 2,116 |
| Free-format MP3, whole | 0.9336 | 372.585 / 347.875 ms | 195,900 | 2,124 / 2,116 |
| Indexed MP3, frames | 0.9501 | 362.453 / 340.441 ms | 188,450 | 316 / 312 |
| Free-format MP3, frames | 0.9667 | 357.520 / 340.067 ms | 188,650 | 316 / 312 |

The median paired time reductions range from 3.3% to 6.6% in this workload.
Allocation counts remain unchanged.
Fresh runtime heap bytes remain zero after warmup because the allocator reuses blocks.
This does not mean zero allocation traffic.
The fastest measured C reference remains 5.79 to 6.31 times faster than Fern.
The results do not establish performance leadership.

Process RSS includes code, input, warmup, and allocator state, and precedes the final PCM dump.
The small RSS differences do not establish a general heap reduction.
The raw report also retains process CPU time, which includes warmup and the PCM dump.
These results cover one input on one native x86-64 host.
Native ARM64 and Android device performance remain separate measurements.

## Retained decoder memory

The constructor probe retains all decoders across the RSS read and checks their arrays afterward.
Both versions retain 960 QMF slots per decoder.
One decoder uses four allocation events and 13,456 fresh heap bytes in both versions.
One hundred decoders use 306 allocation events and 1,342,072 fresh heap bytes in both versions.
The probe detects no retained-state increase.
`state-probe.fern` and `state-results.json` retain the source and measurements.

## Compiler and profile costs

The compiler campaign uses the same fixed checker source for both producers.
Ownership certification stays enabled, with separate cold and warm proof-cache policies.
Seven paired trials reverse execution order on alternate trials.

| Workload | Median candidate / prior time |
| --- | ---: |
| Compiler, cold proof cache | 0.9962 |
| Compiler, warm proof cache | 1.0010 |
| Generated X25519 program | 0.9965 |
| Generated calendar program | 1.0012 |

The timings remain within 0.4% of the baseline and establish no material compiler-time change.
The self-built core grows from 16,223,712 bytes to 16,234,128 bytes, an increase of 10,416 bytes.
All 14 compiler drivers fit the complete-report size limits without changes to the 5% tolerance.

The CI-style profile campaign records all 180 metrics with Valgrind 3.24.0 for native x86-64 execution and ARM64 static output.
Three Deflate instruction counts cross the 1% improvement threshold.
A separate matched comparison of the self-built cores confirms the reductions and identical program exit checksums.

| Profile | Prior instructions | Candidate instructions |
| --- | ---: | ---: |
| Hardened | 34,557,885 | 33,840,967 |
| Minimal | 39,906,492 | 39,191,644 |
| Safe | 33,595,455 | 32,880,607 |

The matched reductions range from 1.8% to 2.1%.
Minimal and safe counts repeat exactly against the first complete profile run.
The hardened count differs by 388 instructions, about 0.0012%.
`profile-repeat.json` retains both measurements and executable hashes.
Only the three measured profile ceilings move downward, and the 1% tolerance stays fixed.
The native array-append row also retains the measured reduction from #14829, as #14844 records.

## Correctness and qualification

Nineteen codec suites pass native sanitizers, balanced leak checks, strict IR, WASI execution, and Android ARM64 shared-library compilation.
They cover MP3, QOA, WAVE, FLAC decode and encode, Opus, AAC, Vorbis, ALAC, and WavPack.
Scalar controls also pass with fusion enabled and disabled on native and WASI, plus Android ARM64 compilation.
The complete target campaign contains 63 builds and 42 executions.
Every native execution reports balanced allocations and zero live bytes.

The controls cover scalar widths, packed bytes, signed zero, NaN payloads, aliases, negative lengths, early exits, mutable bounds, and loop effects.
Admission tests reject handle arrays, views, invalid count types, and invalid result types.
Counted-append controls, the semantic-kind registry, and unchanged complexity limits pass.
The MP3 interpreter smoke also passes with the final self-built producer.
All 30 final MP3 target binaries match the previous qualified producer byte for byte.

The report retains qualification results and producer hashes in `qualification.json`.
Native ARM64 CI and the full integrated PR verdict remain incomplete at publication.
Android device playback, latency, power, and thermal measurements remain separate work.

## Reproduction

The measurement directories contain the matching self-built stage-2 core as both `fern` and `fern-selfhost`.
Both source trees pass `make distcheck` before the measurements.
The frozen codec checkout uses the revision above.

```sh
scripts/bench-mp3-compilers \
  --baseline /path/to/baseline/fern --candidate /path/to/candidate/fern \
  --codec-root /path/to/86447a72aa --output /tmp/mp3-zero-fill \
  --trials 7 --rounds 50
scripts/native-pair-bench /path/to/baseline/fern /path/to/candidate/fern \
  /path/to/fixed/compiler/source /tmp/compiler-zero-fill.json --compiler-pairs 7
scripts/profile-costs /tmp/zero-fill-profiles.txt
FERN_CI_PERF_GATE_STRICT=1 scripts/ci-check-perf \
  .github/profile-costs-baseline.txt /tmp/zero-fill-profiles.txt
```
