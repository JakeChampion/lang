# Native FLAC LPC comparison

The encoder now supports optional linear prediction through order 32.
Welch-window autocorrelation and stable Levinson-Durbin analysis select signed 15-bit integer coefficients.
The coefficient shift ranges from zero through 15.
Exact integer residuals, complete subframe costs, and overflow checks determine the emitted plan.
The planner compares LPC with the prior constant, verbatim, and fixed plans.
The default LPC order remains zero.

The new bank contains 70 streams with independent FFmpeg and libFLAC 1.5.0 decode and PCM MD5 checks.
It covers every depth from 4 through 32, all eight channel counts, short blocks, and LPC orders through 32.
A stereo control uses 33-bit side samples and an order-32 LPC subframe.
All 81 prior fixed-only streams retain their exact bytes.
The small controls also check retained input, invalid options, exact budgets, frame checksums, and PCM MD5.

The paired campaign uses seven alternating-order trials on CPU 0 of an AMD EPYC 9V74 host.
Each trial encodes 48,000 frames four times after two warmup rounds.
The compiler core SHA-256 is `b888ea5fe864d17c077e02bc10efe2c3c76dde47d88814c2951b936098a99e3e`.
The prior standard library comes from commit `1b2fb3998c2bce9379a28ea017e3a102041ce9a4`.
The report retains all codec, driver, reference, and compiler hashes.
Concurrent checks can affect host execution, so these results describe this campaign and host.

Both encoders use 4,096-frame blocks, LPC orders through 32, 15-bit coefficients, and a Welch window.
Both compare fixed predictors, residual partition orders through six, and stereo assignments.
The C reference is libFLAC 1.5.0 with exhaustive model search and coefficient-precision search disabled.
The independent decoders check every PCM sample outside the timed trials.
The timed trials include complete stream construction, PCM MD5, frame checksums, and a checksum over every output byte.

| Input | LPC / fixed time | LPC / libFLAC time | Fern LPC frame bytes | Fern fixed frame bytes | libFLAC frame bytes |
| --- | ---: | ---: | ---: | ---: | ---: |
| Stereo music, 24-bit | 12.06 | 11.30 | 59,883 | 60,416 | 61,301 |
| Eight-channel music, 24-bit | 9.85 | 5.74 | 296,938 | 297,901 | 309,760 |
| Stereo noise, 16-bit | 12.61 | 12.23 | 192,122 | 192,122 | 192,122 |
| Stereo silence, 24-bit | 1.03 | 1.42 | 194 | 194 | 194 |

Time comparisons use median paired ratios.
Frame-byte comparisons exclude the different metadata sizes in Fern and libFLAC output.
The two synthetic music inputs shrink by 0.88% and 0.32% relative to fixed-only Fern.
Noise and silence retain the same frame bytes.
These four generated inputs do not establish a general compression lead.
LPC takes substantially more CPU than fixed-only Fern and remains slower than libFLAC.

| Input | LPC allocations | Fixed allocations | LPC RSS, KiB | Fixed RSS, KiB | libFLAC RSS, KiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Stereo music | 616,588 | 76,608 | 2,912 | 2,860 | 1,792 |
| Eight-channel music | 615,408 | 75,436 | 10,648 | 10,600 | 3,456 |
| Stereo noise | 610,076 | 70,172 | 2,876 | 2,828 | 1,940 |
| Stereo silence | 1,044 | 1,044 | 2,720 | 2,716 | 1,560 |

Allocation counts cover four measured encodes after warmup.
RSS uses Linux process VmHWM and includes the input, executable code, warmup, and output storage.
All Fern modes report zero new runtime heap bytes during measurement because they reuse allocated blocks.
This does not mean zero allocation traffic.
The report does not measure C allocation counts.
LPC adds 48 through 52 KiB to process RSS on the non-silent inputs.

The separate fixed control uses seven paired trials of 16 encodes with the same compiler.
All fixed bitstreams and allocation counts match the prior library exactly.
The median paired time ratios are 0.995 for stereo music, 1.005 for eight-channel music, 0.984 for noise, and 1.008 for silence.
The individual trials include variation, so small differences do not establish a speed change.
The current fixed executable uses 12 through 20 KiB more median process RSS in the four-round campaign.
The optional encoder code remains in that executable.

The complete LPC, small LPC, fixed reference, cost, boundary, and smoke suites pass native sanitizers and WASI execution.
All six suites compile as Android ARM64 shared libraries.
Every native run reports zero live bytes.
The registered native, Go-oracle, self-host interpreter, and complexity checks qualify the same source library.
Full CI remains required.

The campaign does not measure source I/O, startup latency, physical Android latency, power, or thermal behavior.
The CPU and allocation gaps remain open.

```sh
scripts/bench-flac-encode --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/prior/internal/stdlib \
  --lpc-order 32 --rounds 4 --trials 7 --output results.json
```

Implementation: #14807. Encoder: #14688.

Performance: #14515 and #14516. Roadmap: #14508.
