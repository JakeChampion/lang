# WavPack decorrelation comparison

The decoder selects mono, stereo, or delay kernels before the sample loop.
Terms 17 and 18 retain their history in scalar variables.
The stereo kernel uses separate scalar weights and histories for both channels.
The delay and cross-channel terms retain their array histories.

The weight calculation now matches the reference split-word arithmetic.
The reference wraps 32-bit intermediate values before the final shift.
A 64-bit product gives the same result for signed 16-bit weights only with an explicit intermediate wrap.
Larger adaptive weights use the full reference calculation.
The 69 direct reference controls include weights that reach +34,624 and -34,624.
The controls also retain caller aliases and check unused output storage.

A valid 156-byte file exposes the previous arithmetic error.
WavPack 5.8.1 returns the single sample -8,388,608 with no checksum errors.
The previous Fern decoder rejects the file with a PCM checksum error.
The corrected decoder accepts the file through byte views, short sequential reads, and indexed file seeks.
The generator freezes the file and verifies it through the public C decoder.

The benchmark compares the baseline, the candidate, and WavPack 5.8.1 in each trial.
Both Fern binaries use the same compiler, driver, inputs, and output checksums.
The baseline standard library comes from commit `5146e3ad9e639d04595704f63a2b25c895141e81`.
The compiler core SHA-256 is `65ead4d702d1ee8a3f96985c7cb8277847a7d50d4db37b32eff2ec4d51f83ba8`.
The report records each candidate and baseline source hash.
Its Git head identifies the parent revision because the candidate sources were uncommitted during measurement.

Seven trials use CPU 0 and reverse the execution order on alternate trials.
Each trial decodes 8,192 frames 512 times after four warmup rounds.
The host uses an AMD EPYC 9V74 processor with a four-core CPU quota.
The independent verification driver checks every PCM byte before the timed trials.
Concurrent compiler checks can affect host execution, so these results describe this host and campaign.

| Case | Candidate / baseline time | Candidate / C time | Median paired gain |
| --- | ---: | ---: | ---: |
| Stereo music, 24-bit | 0.819 | 3.36 | 18.1% |
| Eight-channel music, 24-bit | 0.839 | 2.74 | 16.1% |
| Stereo noise, 24-bit | 0.838 | 3.48 | 16.2% |
| Stereo silence, 24-bit | 0.976 | 3.28 | 2.4% |
| Stereo integer, optimized 32-bit | 0.863 | 3.76 | 13.7% |
| Stereo float, exact bits | 0.864 | 3.15 | 13.6% |

The figures use the median paired ratio.
The candidate remains slower than the C reference in every case.
The non-silent cases remove 3,072 allocations for stereo and 15,360 allocations for eight channels over 512 rounds.
This is a reduction of 9.2% through 9.9%.
Silence retains its allocation count.
Median RSS and new runtime heap bytes match the baseline in every case.

The report does not measure C allocations, source I/O, startup latency, or physical Android behavior.
Zero new runtime heap bytes mean reuse of allocated heap blocks, rather than zero allocation traffic.
The baseline retains the arithmetic defect and serves only as a performance comparison on inputs where both decoders agree.
The frozen kernel controls use unmodified WavPack 5.8.1 sources at `4827b9889665b937b6ed71b9c6c0123152cd7a02`.

All 72 predictor and file controls pass native sanitizers, WASI execution, and Android ARM64 shared-library compilation.
The registered native and Go-oracle checks pass.
The self-host interpreter checks also pass.
The full 132-file corpus, boundary campaign, sequential source corpus, and indexed file corpus pass the same three compiled targets.
All native runs report zero live bytes.
Full CI remains required.

Run the paired benchmark from the repository root:

```sh
scripts/bench-wavpack --compiler /path/to/bin/fern \
  --baseline-stdlib /path/to/baseline/internal/stdlib --output results.json
```

Issues: #14802 and #14803.

Performance: #14779. Codec: #14686. Roadmap: #14508.
