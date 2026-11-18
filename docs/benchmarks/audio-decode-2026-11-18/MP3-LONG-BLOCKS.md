# MP3 long-block transform measurements

The long-block change reduces median MP3 decode time by 21–26% on this x86-64 host.
Allocation counts fall by 4–10% without larger retained decoder state.
The comparison uses nine alternating trials of 100 rounds per input and mode.
Each input contains approximately one second of audio.

The baseline is commit `31d2dcf0` from #16442, with the MP2 workspace, shared synthesis SIMD, and compiler fix.
Both libraries use the same corrected compiler.
This comparison isolates the additional long-block change from those earlier improvements.
Issue #16445 records the work.

| Rate, channels | API | Baseline ms | Candidate ms | Time change | Allocations per stream |
|---|---|---:|---:|---:|---:|
| 48000, 2 | whole | 7.471 | 5.552 | -25.7% | 3431 → 3095 |
| 48000, 2 | frames | 7.283 | 5.385 | -26.1% | 3295 → 2959 |
| 24000, 1 | whole | 2.041 | 1.527 | -25.2% | 1412 → 1326 |
| 24000, 1 | frames | 2.000 | 1.493 | -25.4% | 1273 → 1187 |
| 8000, 1 | whole | 0.812 | 0.641 | -21.1% | 742 → 714 |
| 8000, 1 | frames | 0.818 | 0.640 | -21.7% | 689 → 661 |

## Cause and implementation

A 790-sample CPU profile attributes 23.8% to `long_blocks` and 13.9% to `dct9` before this change.
Bit reads account for about 2.4% of those samples.
The profile includes setup and complete-input decode, with no dropped samples.

Each SIMD lane carries one independent cosine or sine transform.
Four lanes process two long blocks, with the scalar operation order preserved in each lane.
Two temporary nine-element arrays are no longer necessary for each long-block call.
All transform values remain local lane values.
The overlap and window operations process both blocks together.

An odd final block duplicates the final lane inputs and writes identical results to the same positions.
The public decoder supplies even block counts.
Short-block arithmetic remains unchanged.

## Validation and scope

The independent MP3 reference, free-format, boundary, and seek suites pass on native x86-64 under the sanitizer and on WASM.
The complete reference suite also passes on ARM64 under QEMU with the sanitizer.
QEMU supplies correctness evidence only, not performance measurements.

Every benchmark exports complete PCM outside the timed region.
Indexed and free-format outputs match pinned scalar minimp3 exactly.
SIMD minimp3 output meets the existing tolerance.
The timed region retains one observable PCM sample per frame or file.
`--timing checksum` retains the earlier checksum workload.

The C references remain faster than Fern.
These inputs do not establish a universal ranking or a phone power improvement.
Native ARM64 comparisons run through the optional `audio_baseline` workflow input.
The raw JSON reports include all trials, hashes, tool versions, process RSS, and allocation counters.

## Reproduce

```sh
python3 scripts/bench-mp3-freeformat --compiler bin/fern-selfhost \
  --baseline-stdlib /path/to/31d2dcf0/internal/stdlib \
  --sample-rate 48000 --channels 2 --rounds 100 --trials 9 \
  --output /tmp/mp3-transform
```

The script accepts other MP3 sample rates and either channel count.
