# MPEG bit extraction experiments

Date: 2026-11-10. Issues: #15165 and #15166. Parent: #14516 and #14886.

Neither candidate establishes a consistent end-to-end improvement across the measured workloads.
The production codec code remains unchanged.
These results do not establish a performance lead over SIMD minimp3.

The first candidate reads three adjacent direct quantizers with one bounded operation when each width is at most eight bits.
The second candidate replaces the shared byte loop with bounded direct extraction for widths through 24 bits.
Each candidate uses the same unchanged baseline.
The patches reproduce the exact measured library hashes after formatting.
Apply each patch separately with `git apply --unidiff-zero` to reproduce its candidate.

## Method

The baseline commit is `2fa4d82b27c43eb9cc34c062d307218111d2db44`.
The producer is the current self-hosted compiler built through `bootstrap/bootstrap.sh build`.
Its bootstrap program and coreutils smoke tests passed.
Both candidates and the baseline use that same executable.
The raw reports record its SHA-256, library hashes, binary hashes, input hashes, host, and reference versions.

Each campaign uses nine trials with 100 rounds per trial and two warmups.
Variant order reverses on alternate trials.
The timed workload includes decode and a byte checksum of each PCM sample.
All exported candidate PCM matches scalar minimp3 bit for bit on the three benchmark inputs.
No concurrent codec build or test runs during either timed campaign.

A ratio below one means less elapsed time than the baseline.
The results vary by input and API mode.
Small improvements coexist with regressions.
No production change follows from these results.

| Candidate | Rate-channels-kb/s | Mode | Median candidate/baseline time |
| --- | --- | --- | ---: |
| Packed quantizers | 48000-2-192 | whole | 0.9736 |
| Packed quantizers | 48000-2-192 | frames | 0.9933 |
| Packed quantizers | 32000-1-96 | whole | 0.9707 |
| Packed quantizers | 32000-1-96 | frames | 0.9780 |
| Packed quantizers | 16000-1-32 | whole | 0.9583 |
| Packed quantizers | 16000-1-32 | frames | 1.0100 |
| Short-field reader | 48000-2-192 | whole | 1.0127 |
| Short-field reader | 48000-2-192 | frames | 0.9568 |
| Short-field reader | 32000-1-96 | whole | 1.0020 |
| Short-field reader | 32000-1-96 | frames | 0.9857 |
| Short-field reader | 16000-1-32 | whole | 0.9871 |
| Short-field reader | 16000-1-32 | frames | 1.0175 |

The independent C reference includes scalar and SIMD builds.
Fern validates protected-frame CRCs. Minimp3 does not.
The unchanged and candidate Fern libraries perform the same validation.
The raw JSON retains all 432 trial results and output errors across the two campaigns.

## Independent controls

`tests/stdlib/mp3_bits_test.fern` compares the current reader with a one-bit-at-a-time reference.
It checks 13,167 field combinations across physical prefixes, offsets, and widths from zero through 32.
Another 880 cases check logical limits, invalid positions, and widths outside the read contract.
Native sanitizer execution ends with 894 allocations, 894 frees, and zero live bytes.
WASI execution and Android ARM64 shared-library compilation pass.
Both interpreter paths also run these controls.

## Reproduce

```sh
python3 scripts/bench-mp2 --compiler bin/fern-selfhost \
  --baseline-stdlib /path/to/unchanged/internal/stdlib \
  --reference-dir /path/to/pinned/minimp3/headers \
  --output /tmp/mpeg-benchmark --rounds 100 --trials 9
```

The next performance work needs fresh profiles and generated-code measurements on current main.
Compact native numeric storage remains under #14818.
These experiments do not prove that storage alone accounts for the remaining performance gap.
A world-leading claim requires broader reference comparisons and native ARM64 measurements.
