# FLAC partition metadata comparison

The planner now reuses one bounded cache for Rice and Rice2 parameter candidates.
Only a smaller plan retains a separate parameter array.
The block divisibility, predictor order, and requested partition limit determine the maximum cache count.
The cache payload uses eight bytes per permitted partition.
Array headers and allocator overhead add to that storage.
The exact search order, cost scans, and tie rules remain unchanged.

The prior allocation profile contains 7,227 samples with no dropped samples.
Partition selection accounts for 5,728 sampled allocation events, about 79.3%.
A separate 436-sample CPU profile attributes 185 leaf samples each to LPC residual construction and Rice costs.
The change removes temporary metadata traffic and retains those arithmetic operations.
The profile builds remain separate from the timed executables.

The candidate implementation is commit `e529d5111e2bdd04b31cc1031eba2cb5071b5909`.
The prior library is commit `9d27d756ecfe4b6c3c86587d95756d901057da46`.
Both use compiler core SHA-256 `b888ea5fe864d17c077e02bc10efe2c3c76dde47d88814c2951b936098a99e3e`.
The reports retain compiler, codec, driver, and C reference hashes.
The host uses an AMD EPYC 9V74 processor, CPU 0, and a four-core quota.
The local validation jobs completed before these timed campaigns.

Each campaign uses seven alternating-order trials and 48,000 frames per input.
The LPC campaign uses four complete encodes after two warmup rounds.
The fixed-only control uses 16 complete encodes after two warmup rounds.
Both libraries use the same block sizes, predictor limits, and encoded-byte checksums.
Their complete encoded streams match byte for byte in every case.
Independent FFmpeg and libFLAC decoders verify every output sample and PCM MD5 outside the timed trials.

## LPC results

| Input | Candidate / prior time | Candidate allocations | Prior allocations | Fewer allocations |
| --- | ---: | ---: | ---: | ---: |
| Stereo music, 24-bit | 0.997 | 164,872 | 616,588 | 73.3% |
| Eight-channel music, 24-bit | 0.994 | 161,936 | 615,408 | 73.7% |
| Stereo noise, 16-bit | 1.002 | 141,404 | 610,076 | 76.8% |
| Stereo silence, 24-bit | 1.003 | 1,044 | 1,044 | 0% |
| Stereo music, 32-bit | 1.007 | 167,044 | 617,084 | 72.9% |
| Stereo noise, 32-bit | 0.996 | 102,236 | 124,364 | 17.8% |

Time comparisons use the median paired ratio.
The results do not establish a consistent CPU improvement.
The non-silent cases remove 17.8% through 76.8% of allocation events.
Silence retains its allocation count and does not enter the partition search.
Candidate process RSS differs by at most four KiB from the prior library in this campaign.
The figures do not establish a peak-memory improvement.

## Fixed-only control

| Input | Candidate / prior time | Candidate allocations | Prior allocations | Fewer allocations |
| --- | ---: | ---: | ---: | ---: |
| Stereo music, 24-bit | 1.007 | 120,528 | 306,432 | 60.7% |
| Eight-channel music, 24-bit | 1.006 | 109,056 | 301,744 | 63.9% |
| Stereo noise, 16-bit | 1.001 | 28,016 | 280,688 | 90.0% |
| Stereo silence, 24-bit | 1.029 | 4,176 | 4,176 | 0% |
| Stereo music, 32-bit | 1.005 | 129,008 | 308,304 | 58.2% |
| Stereo noise, 32-bit | 1.006 | 13,616 | 45,104 | 69.8% |

The fixed-only cases also retain their exact encoded streams.
The non-silent median time ratios differ by at most 0.8%.
Fixed-only silence is 2.9% slower in this campaign, and the report retains that regression.
Its constant path does not enter the partition search.
This change makes no CPU performance claim.
Fixed-only median RSS is unchanged or differs by four KiB.

Both campaigns report zero new runtime heap bytes during measurement because they reuse allocated heap blocks.
This does not mean zero allocation traffic.
RSS uses process VmHWM and includes input, executable code, warmup, and output storage.
C allocation counts remain unmeasured.
The candidate remains slower than matched libFLAC in every case.
The remaining CPU gap stays open in #14815 and #14516.

## Qualification

All 81 fixed and 70 LPC reference streams retain their exact bytes.
The independent decoders accept all 151 streams and verify every PCM sample and MD5.
The bank includes an order-32 LPC subframe with a 33-bit stereo side channel.
The exhaustive cost controls retain their exact predictor, Rice, escape, partition, and wasted-bit decisions.

All six encoder suites pass native sanitizers and WASI execution.
All six compile as Android ARM64 shared libraries.
Every native run reports zero live bytes.
The registered native, Go-oracle, and bounded self-host interpreter controls pass.
Source lint and complexity gates pass without larger limits.

A new native allocation control exercises partition limit 15 on 17-, 31-, and 32-frame inputs.
Each complete small encode requires at most 64 KiB of fresh runtime heap storage.
The same controls verify exact decoded PCM and PCM MD5.
This guards against allocation from the requested partition limit instead of the legal frame extent.

The campaigns do not measure source I/O, startup latency, physical Android behavior, power, or thermal effects.
The reference uses libFLAC 1.5.0, a Welch window, 15-bit coefficients, and the same LPC and partition bounds.
Full CI remains required.

```sh
scripts/bench-flac-encode --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/prior/internal/stdlib \
  --lpc-order 32 --baseline-lpc-order 32 --wide \
  --rounds 4 --trials 7 --output lpc-results.json

scripts/bench-flac-encode --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/prior/internal/stdlib --wide \
  --rounds 16 --trials 7 --output fixed-results.json
```

Issue: #14815. Encoder: #14688.

Performance: #14515 and #14516. Roadmap: #14508.
