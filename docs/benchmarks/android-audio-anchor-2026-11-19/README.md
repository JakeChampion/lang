# Android audio anchor scratch reuse

Date: 2026-11-19.

The lower video controller queries an AAudio timestamp through `synchronize_audio` or `synchronize_music`.
The previous path requests a separate 16-byte scratch allocation for each query.
The new path reuses the decoder's 32-byte scratch storage.
The helper resets both output fields before each synchronous platform call.
It preserves the status checks and the checks for negative output values.

The decoder and the controller retain their current fields and ownership rules.
The public `Stream.timestamp`, `Music.timestamp`, and `Buffered.timestamp` methods still allocate timestamp storage and validate the same platform fields.
The controller remains video-only and requires an external audio stream and media origin.
The app must call the controller on its owner thread, outside a real-time audio callback.

## Host results

Each case has 30 measured trials across three processes.
Each process uses ten pairs and reverses execution order after each pair.
Each execution first uses 100 warmup calls per case, then measures 100,000 calls per case.
All cases supply a stream rate of 48,000 frames per second.
The available case supplies the valid zero frame and time anchor.
The unavailable case supplies `AAUDIO_ERROR_INVALID_STATE` without output writes.

| Source | Anchor | Baseline median ns/call | Candidate median ns/call | Allocations/call |
|---|---|---:|---:|---:|
| Stream | Available | 34.75 | 31.58 | 1 → 0 |
| Stream | Unavailable | 29.28 | 23.95 | 1 → 0 |
| Music mono | Available | 34.50 | 29.43 | 1 → 0 |
| Music mono | Unavailable | 29.62 | 24.56 | 1 → 0 |
| Music stereo | Available | 33.56 | 29.35 | 1 → 0 |
| Music stereo | Unavailable | 27.72 | 24.66 | 1 → 0 |

Every measured baseline trial counts 100,000 allocations.
Every measured candidate trial counts zero allocations.
The measured loop immediately consumes each `Tick` and retains the returned player.
Other result consumers and unoptimized builds can allocate result records.
The unoptimized lifecycle diagnostic counted one allocation per candidate call.
The allocation claim applies to the optimized loop in this report.

These are host mock results, with no device decode or playback work.
The mocks check the AAudio ABI, field addresses, alignment, sentinel values, and call counts.
The benchmark checks the returned clock fields and closes every mock codec and extractor.

The measured time includes controller guards, the mock query, and clock conversion.
It excludes setup, close, and report output.
The benchmark ran after the task's builds and tests completed.
Host timings do not establish Android throughput, power use, physical lip-sync accuracy, or timestamp availability.

## Inputs

The baseline is main commit `dc681ea382abc000dd3790d9e92a2ebfdd393f7f`.
The candidate code and test scripts are at `ef85264acc3f942cdda2fc39781059673a87a8c1`.
The compiler and stdlib inputs are unchanged after that code commit.
Both variants use identical benchmark source and compiler options.
Both fresh stage0 builds produce the same core compiler bytes.

- Compiler tree: `c2de592444378928a76235d0fcc7241280e8224e`.
- Baseline stdlib tree: `22a3b2be5b9f4499ef3e7cb05e6d3efd228ad222`.
- Candidate stdlib tree: `5bf931c76d702aef426c4fc4b7d40f189415d7e4`.
- Bootstrap tree: `3543fb5cf5ea612eae88569b7268d230522fdffd`.
- Pinned stage0 SHA-256: `9efa770e2d5979eb744e7e1faeb27b004ef21ecaabf67fdf27c1408d2ca4e094`.
- Core compiler SHA-256: `eeea727783457291a1426022f7adcdb7c8116fef249117493f821a2bcd3a4eb3`.
- Canonical build environment: locked Go 1.27.4.
- Compile options: `-O -target x86-64-android -shared -export main`.

[`results.json`](results.json) contains all 720 warmup and measured records, binary hashes, input hashes, and component trees.
The benchmark source is [`bench/audio/android_timestamp.fern`](../../../bench/audio/android_timestamp.fern).
The baseline library predates the source change and remains a separate artifact.

## Reproduction

Place the identical benchmark source in the baseline checkout.
Build its library against the baseline stdlib with the compiler and options above.
Preserve that library before the candidate build.
Then run the candidate script with its absolute path:

```sh
FERN="$PWD/bin/fern-selfhost" scripts/bench-audio-timestamp /absolute/base/bench.so results.json
```

The script builds the candidate library and common host mocks.
It starts three benchmark processes and records every trial.
It requires one allocation per baseline call and zero per candidate call.
A baseline library built from the candidate source fails that comparison.

## Correctness checks

The complete `scripts/test-video` suite passed with the pinned compiler and candidate stdlib.
It includes native sanitizer tests, WASI controls, Android ARM builds, and host ABI and lifecycle mocks.
The ARM artifacts establish build compatibility, without Android or ARM execution.
The WASI controls exercise the checked clock conversion, without an Android platform query.

The clock tests check exact scratch addresses and alignment.
They preserve adjacent bytes, retained output metadata, frame deadlines, and seek behavior across repeated queries.
The tests also cover unavailable, partial, invalid, and absent platform anchors.
The public timestamp tests cover full-width positions and repeated available and unavailable results.
Two deliberate pointer regressions compiled and failed the pointer assertion.
The source bytes, mode, and modification time were restored after both probes.

The complexity ratchet and changed-program checks passed against the baseline commit.
The normal repository CI remains the merge gate.
