# Audio decode measurements, 2026-11-18

The combined MP2 change reduces median decode time by 7–13% on this x86-64 host.
Frame decode with an explicit workspace uses 31–34% fewer allocations.
This result does not establish world-leading performance or Android power savings.

The baseline is main at `c4a7a3b94930cc3434f76ba303d13456f1e1cb4c`.
Both implementations use the same compiler and input streams.
The candidate uses `decode_frame_with` for frame decode and one internal workspace for complete-input decode.

The caller can retain the workspace independently of the small decoder history.
Workspace elements occupy 12,928 bytes, before array and aggregate headers.
Successful calls reuse unique arrays. Payload errors can replace scratch storage.

## Results

Times are milliseconds per complete encoded stream. Each stream contains approximately one second of audio.
Allocation counts are per stream. Each median contains nine trials of 100 rounds.

| Input: rate-channels-kbps | Mode | Main ms | Candidate ms | Time change | Allocations |
|---|---|---:|---:|---:|---:|
| 48000-2-192 | whole | 2.931 | 2.569 | -12.4% | 650 → 448 |
| 48000-2-192 | frames | 2.765 | 2.397 | -13.3% | 591 → 389 |
| 32000-1-96 | whole | 1.517 | 1.412 | -6.9% | 438 → 306 |
| 32000-1-96 | frames | 1.503 | 1.380 | -8.1% | 395 → 263 |
| 16000-1-32 | whole | 0.742 | 0.682 | -8.1% | 227 → 165 |
| 16000-1-32 | frames | 0.734 | 0.674 | -8.1% | 199 → 137 |

The reference minimp3 revision is `ea99364f61c14656440e8d77e9c233ccf3124633`.
The report records its header hashes, compiler hash, CPU, reference tools, and every trial.
Scalar minimp3 remains faster than Fern. Its SIMD implementation remains faster still.
Fern validates protected-frame CRC. The reference timing does not perform that CRC check.

## Measurement scope

`--timing decode` removes the per-sample checksum from the timed region.
It retains one observable sample per decoded frame or file to prevent elimination.
Complete PCM export and comparison occur outside that region.
Fern output matches scalar minimp3 exactly. SIMD reference output passes the existing numerical tolerance.

`--timing checksum` retains the previous benchmark mode.
A separate three-trial check confirms that mode still agrees across implementations.

Fresh-byte counters measure allocator advancement, not allocation activity or resident memory.
Zero fresh bytes after warmup do not mean zero allocations.
Raw process RSS is not a retained-decoder-state measurement.

## Profile and change

The workspace profile contains 2,768 CPU samples with no drops.
Synthesis and its scalar pair helper account for 1,658 samples, about 60%.
Zero-fill accounts for seven samples. Further zero-fill work cannot explain a large remaining speed gap.
The profile includes process setup and final export. It predates the SIMD pair change.

The pair helper now computes adjacent channels with portable four-lane operations.
It preserves multiplication, addition, and division order within each useful lane.
The isolated SIMD comparison reduces median time by 7–14% beyond workspace reuse.
The initial workspace-only comparison reduces allocations but supplies small CPU gains.
These intermediate reports support the choice of synthesis as the next target.

Their baseline stdlib snapshot contains unrelated Android additions with unchanged MPEG sources.
The final report uses a clean archive of the main commit above.

## Validation

All four independent MP2 corpus shards pass with exact PCM on native x86-64 under the sanitizer.
Both frame APIs run against the independent corpus.
Boundary tests cover CRC failure, format changes, truncation, output bounds, and workspace recovery.
The MP3 reference and mutation suite also passes after the shared synthesis change.
MP2 smoke, MP2 boundaries, and MP3 reference tests pass on WASM.
Those suites compile for Android ARM64.

Native ARM64 speed measurements and physical Android playback measurements remain separate requirements.
The optional `audio_baseline` input of `perf.yml` runs this comparison on native x86-64 and ARM64 hosts.
Hosted ARM64 results do not establish phone power use, thermal behavior, or codec hardware availability.

## Reproduce

```sh
python3 scripts/bench-mp2 --compiler bin/fern-selfhost \
  --baseline-stdlib /path/to/main/internal/stdlib --workspace \
  --reference-dir /path/to/pinned/minimp3 --output /tmp/audio-pair \
  --rounds 100 --trials 9
```

`--baseline-workspace` compares two implementations that both expose the workspace API.
The baseline otherwise uses the existing frame API.
Issues #16437, #14886, and #14516 retain the remaining performance work.
