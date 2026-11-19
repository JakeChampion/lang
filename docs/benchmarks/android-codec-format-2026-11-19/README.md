# Borrowed MediaCodec format keys

Date: 2026-11-19.

Video output format queries read 12 integer keys. Audio output format queries read four integer keys.
The baseline creates and frees a temporary C string for each key.
Each conversion also copies the key bytes and writes a final zero byte.
The candidate borrows fixed literals with explicit zero terminators through `.as_bytes()`.
The package helper validates the final zero byte before the synchronous NDK call.

Both paths share the integer status and fallback logic.
The public `CodecFormat.integer` method retains its dynamic-name conversion.
The decoders retain their fields, output-value scratch storage, format checks, and resource ownership.
The change adds no decoder or player API.

## Host results

| Case | Baseline median ns/query | Candidate median ns/query | Fern allocations/query |
|---|---:|---:|---:|
| Video, normal fields | 556.04 | 478.27 | 14 → 2 |
| Video, crop defaults | 568.30 | 485.61 | 14 → 2 |
| Audio, PCM encoding present | 235.81 | 202.52 | 6 → 2 |
| Audio, PCM encoding absent | 238.83 | 197.41 | 6 → 2 |

Every measured video baseline trial counts 14 Fern allocations per query. The candidate counts two.
Every measured audio baseline trial counts six Fern allocations per query. The candidate counts two.

The two allocations remain in these optimized consumers.
Other consumers and builds can allocate more.
The platform format object and any platform allocations remain outside the Fern heap counter.
These results do not establish zero total allocations.

The video keys request 130 bytes across 12 temporary C strings and explicitly copy 118 key bytes per query.
The audio keys request 52 bytes across four temporary C strings and explicitly copy 48 key bytes per query.
These byte counts come from the fixed key lengths in the code.
They exclude allocator metadata, size classes, and platform memory.
The candidate removes these temporary C strings and their explicit byte copies.
It retains the integer output in the decoder scratch storage.

Each media kind uses three fresh benchmark processes.
Each process has ten pairs and reverses execution order after each pair.
Each variant uses 100 warmup queries per profile, then 100,000 measured queries per profile.
Thus each profile and variant has 30 measured trials.
[`results.json`](results.json) retains all 480 warmup and measured records.

Video profiles use 16 by 8 and 8 by 16 dimensions, each with a 128-pixel limit.
The second video profile omits crop-right and crop-bottom, so the reader uses its defaults.
Audio profiles supply 48,000 frames per second and two channels.
The second audio profile omits `pcm-encoding`, so the reader uses signed 16-bit PCM.
The benchmark checks every returned video and audio format field.
It also checks the live mock resource count and closes each decoder.

The measured loop includes a mock flush and an output format query.
Video also has one mock retry before its format event.
The loop repeats format events to expose the key conversion cost.
It does not represent the frequency of format changes in an app.
Setup, close, and report output are outside the measured interval.
The benchmark ran after the task's builds and tests completed.

Both variants use the same host mocks.
The video mock uses a static format token. The audio mock allocates and frees a host C format object per query.
Those platform behaviors are unchanged across the pair.

The mock codec name does not establish hardware execution or hardware performance.
No real decoder, Android device, pixel output, power measurement, or physical lip-sync measurement participates.
Host times do not predict device throughput or playback latency.

## Inputs

The baseline is main commit `6dc89e247ec6a0e6a5eaa0f92f7deda78360f457`.
The candidate code is at `b1b1ef529b9b5780466eb31b3fc3dc79ba3a4564`.
The compiler and stdlib inputs remain unchanged after that code commit.
Both variants use identical benchmark source and compiler options.
The common mocks come from the candidate fixtures and serve both variants.
Both fresh stage0 builds produce the same core compiler bytes.

- Compiler tree: `08ba92e8868c07d7f6d82c495025c33125a44e9c`.
- Baseline stdlib tree: `75ff092e835ff8ec864b4f81f589886004e48436`.
- Candidate stdlib tree: `bcaf3402cda307d07eda5ea94089e9ad63a01700`.
- Bootstrap tree: `3543fb5cf5ea612eae88569b7268d230522fdffd`.
- Pinned stage0 SHA-256: `9efa770e2d5979eb744e7e1faeb27b004ef21ecaabf67fdf27c1408d2ca4e094`.
- Core compiler SHA-256: `366e40aeba33ae64e843c136c651b273dc55cd32a3a19cb304a0c842c5cb70b8`.
- Canonical build environment: locked Go 1.27.4.
- Compile options: `-O -target x86-64-android -shared -export main`.

The JSON report includes all library hashes, common mock hashes, benchmark source hashes, and component trees.

## Reproduction

Place the identical benchmark sources in the baseline checkout.
Build `bench/audio/android_video_format.fern` as `BASE/video.so` against its stdlib with the compiler and options above.
Build `bench/audio/android_audio_format.fern` as `BASE/audio.so` with the same options.
Preserve both libraries before the candidate build.
Then run:

```sh
FERN="$PWD/bin/fern-selfhost" scripts/bench-codec-format /absolute/BASE results.json
```

The script builds the candidate libraries and common mocks.
It requires 14 or six baseline Fern allocations per query and two candidate allocations per query.
It records every trial from all six benchmark processes.

## Correctness checks

The complete `scripts/test-video` suite passed with the candidate stdlib and pinned compiler.
It includes native sanitizer tests, WASI controls, Android ARM builds, and host ABI and lifecycle checks.
The ARM artifacts establish build compatibility only. The WASI controls do not call the Android NDK.

The new video tests check all 12 fields with explicit platform values and fallback values.
They also cover a failed lookup that writes an invalid output value.
The tests preserve the 28 adjacent scratch bytes across 128 repeated format queries per case.
The current video checks cover dimensions, crop bounds, pixel budgets, format ownership, and decoder errors.
The current audio checks cover rates, channel limits, PCM formats, masks, the default format, and resource cleanup.

Both the video and audio scripts enforce the optimized allocation counts.
The previous audio timestamp benchmark reuses the same output capture function and still passes its allocation check.
Three deliberate regressions compiled and failed: a missing terminator, a wrong key, and a restored temporary allocation.
The mutation script restores source bytes, mode, and modification time after all three probes.

The complexity ratchet and changed-program checks passed against the baseline commit.
The normal repository CI remains the merge gate.
