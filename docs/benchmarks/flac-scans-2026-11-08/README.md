# Exact FLAC cost scans and predictor differences

This comparison holds the compiler and benchmark source fixed.
It changes the FLAC encoder algorithms from parent a090df6e to the implementation for #14765.
Every output byte remains unchanged for the 81-stream bank and every benchmark input.

Rice search computes exact adjacent candidate costs in one scan.
Fixed predictors use successive wide differences instead of repeated source prediction.
Constant stereo channels skip the other assignments, whose side sample needs one additional bit.
The difference workspace adds eight bytes per frame. The default block uses 32 KiB, with a maximum raw extent of 524,280 bytes.

## Matched native results

Each input contains one second of synthetic PCM at 48 kHz. These inputs do not represent a music collection.
Each trial measures 16 complete encodes after two warmup encodes.
Seven paired trials alternate implementation order on the same pinned CPU.
The interval includes PCM MD5, frame CRCs, and a checksum of every output byte.
Input loading, compilation, file writes, and warmup are outside the measured interval.

The same compiler builds both encoder versions with production optimization.
libFLAC 1.5.0 uses matching 4096-frame blocks, fixed predictor orders through four, residual partition orders through six, and full stereo search.
LPC is disabled in all implementations. The JSON records source, compiler, core, and reference-library hashes.

| Input | Before, ms | After, ms | Reduction | libFLAC, ms | After/libFLAC |
| --- | ---: | ---: | ---: | ---: | ---: |
| music-2ch | 61.458 | 36.503 | 40.6% | 1.667 | 21.90 |
| music-8ch | 66.356 | 45.049 | 32.1% | 4.957 | 9.09 |
| noise-2ch | 66.226 | 35.021 | 47.1% | 1.670 | 20.97 |
| silence-2ch | 1.827 | 1.179 | 35.5% | 0.822 | 1.43 |

| Input | Before allocations | After allocations | Before native peak, KiB | After native peak, KiB |
| --- | ---: | ---: | ---: | ---: |
| music-2ch | 19,056 | 19,152 | 2,812 | 2,876 |
| music-8ch | 18,763 | 18,859 | 10,552 | 10,616 |
| noise-2ch | 17,447 | 17,543 | 2,780 | 2,848 |
| silence-2ch | 369 | 261 | 2,732 | 2,736 |

Allocation counts are per encode. The difference workspace adds one allocation for each channel analysis.
Native memory comes from Linux VmHWM inside each process, including input storage and warmup.
It excludes the Python launcher. Fresh-byte counts describe runtime heap growth, not total allocation traffic.

The result improves the previous algorithm without establishing a speed or memory lead over libFLAC.
The input conversion and retained PCM also affect process memory. This comparison does not isolate encoder-state memory.
Native ARM64, Android power and thermals, representative music, and LPC compression remain open.

## Exact Rice range

For folded residuals `u`, define `C(k) = n*(k+1) + sum(u >> k)`.
Its adjacent difference is `D(k) = n - sum(floor((u + 2^k) / 2^(k+1)))`.
Let `mu` be the arithmetic mean and `s = floor(log2(mu))` for `mu >= 1`.

For `k <= s-2`, the sum in `D(k)` exceeds `n`, so the cost decreases.
For `k >= s+1`, the sum is less than `n`, so the cost increases.
The global minimum therefore lies in `s-1`, `s`, or `s+1`.
The bounded search clamps that range to the permitted parameters and keeps the lower parameter on a tie.
If `mu < 1`, parameter zero is the unique minimum.

The independent generator checks the bound against all parameters from zero through 30.
It enumerates every sequence of one through five values from zero through seven, plus 1000 wide random sequences.
The Fern oracle bank adds 93 distributions near powers of two and three extreme cases.
It still enumerates all predictor and partition costs independently.

The runtime bank contains 147 independently enumerated subframe minima.
It includes cases where lower-order residuals overflow but higher predictors remain valid.
All four encoder suites pass native sanitizers, WASI, Android ARM64 compilation, and the Go interpreter.
The registered interpreter and complexity gate also pass.
FFmpeg and libFLAC independently decode all 81 frozen streams and accept their PCM checksums.

## Reproduce

Install a native C compiler, FFmpeg, and libFLAC 1.5.0.
Use one compiler that includes #14764, then compare both standard-library checkouts:

```sh
python3 scripts/bench-flac-encode \
  --compiler /path/to/qualified/bin/fern \
  --baseline-stdlib /path/to/parent/internal/stdlib \
  --rounds 16 --trials 7 --output report.json
```

The benchmark checks exact PCM with FFmpeg and libFLAC, verifies MD5, and requires identical Fern output bytes.
It verifies every trial checksum and output extent. Header downloads use pinned SHA-256 hashes.

The preserved result is [x86-64-linux.json](x86-64-linux.json).
The earlier compiler comparison is in [the fixed-predictor baseline](../flac-fixed-2026-11-08/README.md).
Parents: #14515, #14516, and #14688.
