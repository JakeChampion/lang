# Native WavPack decode baseline

Fern remains slower than WavPack 5.8.1 on all six decode cases.
The median paired CPU ratios range from 3.26 through 4.83.
The Fern processes record lower RSS in this test.
These results do not establish a world-leading codec.

The host uses an AMD EPYC 9V74 processor with a four-core CPU quota.
Both programs use CPU 0.
Each input contains 8,192 frames at 48 kHz, with a 4,096-frame encoder block setting.
Each trial decodes the input 512 times after four warmup rounds.
Seven trials alternate the execution order.

| Case | Fern / C CPU ratio | Fern allocations | Fern new heap bytes | Fern RSS KiB | C RSS KiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Stereo music, 24-bit | 4.54 | 32,257 | 0 | 432 | 960 |
| Eight-channel music, 24-bit | 3.50 | 155,137 | 0 | 920 | 1,224 |
| Stereo noise, 24-bit | 4.83 | 32,257 | 0 | 480 | 996 |
| Stereo silence, 24-bit | 3.26 | 5,633 | 0 | 372 | 912 |
| Stereo integer, optimized 32-bit | 4.78 | 33,281 | 0 | 440 | 1,020 |
| Stereo float, exact bits | 3.93 | 33,281 | 0 | 440 | 968 |

The timed region includes a seek to zero, complete decode, checksum validation, and a checksum over every PCM sample.
Both programs retain input bytes and reuse signed 32-bit output storage.
Input reads, index construction, context construction, and warmup remain outside the timed region.
The C decoder uses its public memory-source callbacks.
The Fern decoder uses its byte-view index and reusable reader.

The generator creates deterministic PCM and encodes it through the pinned reference.
The reference decoder and the Fern verification driver must reproduce every PCM byte before the timed trials start.
All trials must agree on the sample count and the PCM checksum.
The report retains input and PCM hashes, library and compiler hashes, source hashes, raw trials, and paired ratios.
The core compiler SHA-256 is `4352e6887e523028a6ca39b9c238dd51e34bc75c409ad2b086e5d8dbaba954ec`.

Zero new heap bytes means that the runtime reuses prior heap blocks.
It does not mean zero allocations.
The C allocation count remains unmeasured.
RSS is the Linux process `VmHWM`, including code, input, indexes, contexts, and output storage.
It does not isolate the codec heap or prove a memory advantage on other hosts.

These measurements cover warm byte-view decode on this Linux host.
They exclude source I/O, index startup, first-output latency, physical Android latency, power, and thermal behavior.
The performance issue remains open for those measurements and for CPU and allocation improvements.

A separate CPU profile contains 944 samples with no dropped samples.
The leaf counts include 387 samples in residual restoration and 212 in bit reads.
Entropy decode accounts for 165 leaf samples, with 61 in bounded codes and 37 in unary reads.
The profile uses 8,192 stereo music decode rounds.
The report excludes profiler overhead from all benchmark timings.

The Fern benchmark also passes native sanitizers with zero live bytes.
The C harness passes AddressSanitizer and UndefinedBehaviorSanitizer.
That C check instruments the harness, rather than the installed reference library.
Both sanitizer runs agree on the complete sample count and checksum.

Run the benchmark from the repository root:

```sh
scripts/bench-wavpack --compiler /path/to/bin/fern --output results.json
```

Issue: #14779. Codec: #14686. Roadmap: #14508.
