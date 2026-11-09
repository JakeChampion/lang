# WavPack bit-field scan comparison

The decoder now counts bounded code widths with the leading-zero intrinsic.
It scans unary prefixes one byte at a time with the trailing-zero intrinsic.
Each scan stops at the declared bit boundary or the unary limit.
It does not read padding beyond the declared stream.
Negative unary limits return `Header`.

The benchmark compares the baseline, the candidate, and WavPack 5.8.1 in each trial.
Both Fern binaries use the same compiler, driver, inputs, and output checksums.
The baseline standard library comes from commit `77c3449fd4f43dbddd438a6b655665cf3fa56b11`.
The compiler core SHA-256 is `4352e6887e523028a6ca39b9c238dd51e34bc75c409ad2b086e5d8dbaba954ec`.
The report records the individual source hashes for the candidate and the baseline.
Its Git head identifies the parent revision because the candidate sources were uncommitted during measurement.

Seven trials use CPU 0 and reverse the execution order on alternate trials.
Each trial decodes 8,192 frames 512 times after four warmup rounds.
The host uses an AMD EPYC 9V74 processor with a four-core CPU quota.
The independent verification driver checks every PCM byte before timing.

| Case | Candidate / baseline CPU | Candidate / C CPU | Change |
| --- | ---: | ---: | --- |
| Stereo music, 24-bit | 0.971 | 4.32 | Noisy result, 2.9% median gain |
| Eight-channel music, 24-bit | 0.898 | 3.29 | 10.2% faster |
| Stereo noise, 24-bit | 0.859 | 4.09 | 14.1% faster |
| Stereo silence, 24-bit | 1.018 | 3.32 | 1.8% slower |
| Stereo integer, optimized 32-bit | 0.866 | 4.31 | 13.4% faster |
| Stereo float, exact bits | 0.922 | 3.60 | 7.8% faster |

The figures use the median paired ratio, rather than the ratio of independent medians.
Stereo music has substantial trial variation and does not establish a reliable speed gain.
Silence has a small regression in this sample.
The candidate remains slower than the C reference in every case.
All Fern allocation counts, new heap byte counts, and median RSS values match the baseline.
The report does not measure C allocations, source I/O, startup latency, or physical Android behavior.

The field controls cover 5,880 unary cases and 2,976 independent bounded codes.
They check all eight bit alignments, truncated streams, run limits, and widths through 32 bits.
Four invalid span and limit controls retain `Header` errors.
The registered native and Go-oracle controls pass, and the self-host interpreter controls pass.
The controls also pass WASI execution and Android ARM64 shared-library compilation.

All 132 frozen reference files retain exact PCM under native sanitizers.
The full corpus and the existing boundary campaign pass WASI execution and Android ARM64 compilation.
The native boundary campaign also passes sanitizers.
Every native suite reports zero live bytes.
Source lint remains within the existing limits.
Full CI remains required.

Run the paired benchmark from the repository root:

```sh
scripts/bench-wavpack --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/baseline/internal/stdlib --output results.json
```

Issue: #14779. Codec: #14686. Roadmap: #14508.
