# Free-format MP3 qualification and costs

Fern now resolves free-format Layer III frame extents across packet, memory, sequential-source, and source-backed playback.
The same extent supports gapless metadata, frame padding, reservoir history, and sample-exact seeks.
Each incremental discovery step checks at most 256 byte positions and performs at most one source read.
The source window and audio limits bound lookahead before PCM output.
An established extent also permits isolated packets and the final stream frame.

The change also accepts CRC-protected LAME Info metadata that overlaps the final two side-info bytes.
The decoder verifies the CRC over the complete original side-info region.
It requires a zero side-info prefix, valid Xing fields, and the LAME extension before it accepts the tag at this offset.
Layer I and Layer II remain outside this change.
Issues #14832 and #14837 track these changes under #14689 and #14508.

## Independent references

The generator uses LAME 3.100, pinned minimp3, and mpg123.
The manifest in `scripts/audio-reference/mp3-freeformat.json` retains library hashes, encoder settings, input hashes, and PCM hashes.
The 17 cases cover all nine sample rates, mono, stereo, joint stereo, padding, reservoir history, high bitrates, CRC, and gapless metadata.
Two cases retain indexed-bitrate protected Info controls.
Every raw and playable sample matches the scalar minimp3 reference bits.
The maximum absolute difference from mpg123 is 0.0000008941.

The pinned minimp3 file helper misses the protected LAME Info offset.
For these cases, the generator trims raw minimp3 PCM from the independent LAME fields.
Every trimmed sample also agrees with mpg123 within the recorded tolerance.
FFmpeg does not accept these free-format fixtures and supplies no free-format decode reference.

The small interpreter fixture repeats one isolated active frame three times for header context.
Its first frame agrees with scalar minimp3 bit for bit and with mpg123 within 0.0000002571.
The smoke checks independent PCM bits, one-byte reads, both seek APIs, two-frame EOF, and ID3v1 metadata.
The complete reference and boundary suites retain compiled target coverage.

## Matched measurement

The campaign uses one AMD EPYC 9V74 x86-64 Linux host.
Both Fern libraries use the same compiler core, with SHA-256 `c47b538d4090ae3d4f7847899ffd9f1bc289ec89c0594aa1a20e70b4e99fe2f2`.
The baseline library comes from commit `f9adcf89f3a8913dfb74d94cf810c34b142957db`.
The report retains complete library hashes, compiler hashes, driver hashes, reference revisions, and executable hashes.
The supported build produces the same compiler core and a CLI that embeds the new library.

The input contains one second of deterministic stereo PCM at 48 kHz before MP3 encoder delay and padding.
The generator clears only the bitrate nibble in each unprotected CBR header to create the free-format control.
Both streams therefore contain identical coded audio payloads.
Each process performs two warmup rounds and 30 measured decodes.
Seven trials alternate execution order after the other local build and test campaigns complete.

The timed region includes a byte checksum of every output float.
Frame mode retains decoder state and reuses a 2,304-sample output buffer within each stream.
Whole mode returns a complete raw PCM array.
The C drivers use the same workload shape and scalar or SIMD minimp3 with contraction disabled.
All non-SIMD results match every output byte across formats, modes, and trials.
The maximum SIMD difference is 0.0000001491.

| Workload | Median time, 30 decodes | Allocations | Peak RSS KiB |
| --- | ---: | ---: | ---: |
| Prior Fern, indexed, whole | 223.621 ms | 117,420 | 2,112 |
| New Fern, indexed, whole | 223.180 ms | 117,420 | 2,124 |
| New Fern, free, whole | 222.704 ms | 117,540 | 2,124 |
| Scalar C, free, whole | 45.468 ms | Not measured | 1,060 |
| SIMD C, free, whole | 35.631 ms | Not measured | 1,056 |
| Prior Fern, indexed, frames | 213.301 ms | 113,070 | 304 |
| New Fern, indexed, frames | 212.561 ms | 113,070 | 316 |
| New Fern, free, frames | 212.014 ms | 113,190 | 316 |
| Scalar C, free, frames | 42.248 ms | Not measured | 760 |
| SIMD C, free, frames | 33.255 ms | Not measured | 748 |

Indexed MP3 retains its allocation counts.
The median time changes remain below 0.4% in this campaign and establish no speed improvement.
Free-format discovery adds four allocation events per stream.
Fresh runtime heap bytes remain zero after warmup because the allocator reuses blocks.
This figure does not mean zero allocation traffic or zero codec state.

Free-format Fern remains 4.90 to 5.02 times slower than scalar C and 6.25 to 6.38 times slower than SIMD C.
The measurements do not support a claim of the best performance worldwide.
RSS records process VmHWM before the final PCM dump and includes code, input, warmup, and allocator state.
Fern uses static executables, while the C executables use the host C library.
Their RSS difference does not establish a codec memory advantage.
The raw report also retains process CPU time, which includes warmup and the PCM dump.

## Qualification

All ten MP3 suites pass native x86-64 sanitizers, leak checks, strict IR, WASI execution, and Android ARM64 shared-library compilation.
Native allocations and frees balance, with zero live bytes in every suite.
The new complete suite checks 17 independent cases through whole decode, isolated packets, short reads, and both seek APIs.
Six boundary cases cover false sync patterns, short context, large tags, limits, I/O failure, and protected metadata.
The interpreter smoke passes both Go and self-hosted interpreters.
The self-hosted run takes about 38 seconds.

Source lint, the unchanged complexity limits, and standard-library shard coverage pass.
The supported build and its bootstrap smoke also pass.
`qualification.json` retains the target results and executable hashes.
Native ARM64 CI and full PR checks remain incomplete at publication.
Android device playback, power, latency, and thermal measurements remain separate work.

## Reproduction

```sh
make build
scripts/gen-mp3-freeformat-vectors.py
bin/fern -fmt -w tests/stdlib/mp3_freeformat_vectors.fern tests/stdlib/mp3_freeformat_smoke_vectors.fern
scripts/bench-mp3-freeformat \
  --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/baseline/internal/stdlib \
  --candidate-stdlib internal/stdlib \
  --output /tmp/mp3-freeformat-report --trials 7 --rounds 30
go test ./internal/testing/sourcelint
go test ./internal/testing/e2ecompiler -run '^TestSelfHostRepoComplexityRatchet$' -count=1
go test ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$/^mp3_freeformat_smoke$' -count=1
```

The reference generator requires LAME 3.100, mpg123, a C compiler, and the pinned minimp3 headers.
The benchmark additionally requires FFmpeg with libmp3lame.
The script excludes the old library from free-format trials because that library rejects free-format headers.
The benchmark retains one compiler for both libraries to separate codec changes from compiler changes.
