# MP3 synthesis storage experiment

This candidate reuses the QMF array as synthesis line storage.
It removes the separate 2,112-float allocation, zero fill, and initial history copy on each granule.
The public compact helper retains its 960-float history contract.
The decoder uses a larger array with 2,127 float slots.
The final 15 slots preserve inactive mono history that the first synthesis pair overwrites.

The experiment improves CPU time but increases retained decoder memory.
It remains a draft because it does not meet both performance and memory goals.
It does not establish the preferred default for Android playback.
Issue #14848 tracks the experiment under #14516 and #14508.

## Diagnostic evidence

The baseline CPU profile retains 640 samples without drops.
Long-block transforms account for 204 inclusive samples, and granule synthesis accounts for 172 inclusive samples.
These nested counts overlap.
The allocation profile retains 1,949 samples without drops.
The zero-array helper accounts for about 82% of its byte weight.
The raw profiles remain separate from production measurements.

The profiles use the free-format frame workload from #14846.
CPU sampling uses 900 rounds, and allocation sampling uses 30 rounds.
The compiler core is `c47b538d4090ae3d4f7847899ffd9f1bc289ec89c0594aa1a20e70b4e99fe2f2`.
The baseline library is commit `86447a72aaf1ac9f7e225b51275bce2a9add311e`.
The profile builds use `FERN_PROFILE=cpu` and `FERN_PROFILE=alloc` without debug variables.

## Matched results

The production campaign uses one AMD EPYC 9V74 Linux host and one compiler for both libraries.
The indexed and free-format inputs contain identical coded payloads.
Each trial performs two warmup rounds and 50 measured decodes of one second of stereo audio.
Seven trials reverse execution order on alternate trials after other local checks complete.
Each timed decode includes a byte checksum of every output float.
All Fern and scalar C outputs match every byte across modes and trials.

| Workload | Median paired time ratio | Allocations, prior / candidate | Peak RSS KiB, prior / candidate |
| --- | ---: | ---: | ---: |
| Indexed, whole | 0.9671 | 195,700 / 191,400 | 2,124 / 2,132 |
| Free-format, whole | 0.9680 | 195,900 / 191,600 | 2,124 / 2,128 |
| Indexed, frames | 0.9757 | 188,450 / 184,150 | 316 / 304 |
| Free-format, frames | 0.9815 | 188,650 / 184,350 | 316 / 304 |

The candidate removes 86 allocation events per stream.
The median paired time reductions range from 1.8% to 3.3% in this workload.
Fresh runtime heap bytes remain zero after warmup because the allocator reuses blocks.
This does not mean zero allocation traffic.
The SIMD C reference remains about 6.0 to 6.4 times faster than the candidate.
The results do not establish performance leadership.

The process RSS measurement precedes the final PCM dump and includes code, input, warmup, and allocator state.
The report retains all process CPU measurements, which also include the PCM dump.
The raw JSON retains compiler, library, input, driver, and executable hashes.
RSS changes in this small workload do not replace the retained-state comparison below.

## Retained-state cost

Native Fern float arrays currently use eight-byte slots.
The QMF payload grows from 7,680 bytes to 17,016 bytes.
The old transient line array disappears, but every retained decoder now owns the larger QMF allocation.
This matters when an application retains multiple idle or paused decoders.

| Cold constructor probe | Prior | Candidate |
| --- | ---: | ---: |
| One decoder, fresh heap bytes | 13,456 | 25,744 |
| One decoder, process peak RSS KiB | 80 | 92 |
| 100 decoders, fresh heap bytes | 1,342,072 | 2,570,872 |
| 100 decoders, process peak RSS KiB | 1,384 | 2,580 |

The probe retains every decoder across the RSS read and checks their arrays afterward.
Allocation counts remain four for one decoder and 306 for 100 decoders in both versions.
The extra fresh heap cost is 12,288 bytes per decoder.
`state-probe.fern` and `state-results.json` retain the probe and measurements.
This memory increase limits the value of the small CPU improvement.
The next experiment will target zero-fill code generation without larger decoder state.

## Correctness and qualification

A separate generator calls the pinned scalar minimp3 synthesis kernel directly.
Nine steps alternate mono and stereo with nonzero initial history and deterministic spectral input.
The reference initializes its temporary line storage to zero, as the compact Fern helper does.
The tests compare every PCM byte and all 960 retained history values through independent hashes.
They also preserve the prefix and tail of the reusable output buffer.
Both compact and cached helpers match the independent reference.

All eleven MP3 suites pass native sanitizers, leak checks, strict IR, WASI execution, and Android ARM64 shared-library compilation.
These checks comprise 33 builds and 22 executions.
Every native suite reports balanced allocations and zero live bytes.
Both interpreter suites pass the registered Go and self-hosted comparison.
The supported build, complexity limits, source lint, and standard-library shard coverage pass.
Native ARM64 CI, full PR checks, and Android device measurements remain incomplete at publication.

## Reproduction

```sh
make build
scripts/gen-mp3-synthesis-vectors.py
bin/fern -fmt -w tests/stdlib/mp3_synthesis_vectors.fern
scripts/bench-mp3-freeformat \
  --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/86447a72aa/internal/stdlib \
  --baseline-freeformat --candidate-stdlib internal/stdlib \
  --output /tmp/mp3-synthesis-report --trials 7 --rounds 50
bin/fern -stdlib internal/stdlib -target x86-64-linux \
  -o /tmp/mp3-state docs/benchmarks/mp3-synthesis-2026-11-09/state-probe.fern
/tmp/mp3-state 100
go test ./internal/testing/e2ecompiler \
  -run '^TestSelfHostStdTestInterp$/^(mp3_synthesis|mp3_freeformat_smoke)$' -count=1
```
