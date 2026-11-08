# Checked arithmetic in CAF decode

This comparison compiles the same CAF source with two Fern compiler builds. Both builds run the default and reusable buffer APIs. All four modes decode the same input and checksum every playable sample.

The run uses an AMD EPYC 9V74 on x86-64 Linux. CPU affinity fixes every trial to CPU 0. The container permits four CPU cores of execution time. Each case runs 512 decode rounds in seven trials. Trial order rotates across all four modes. A long interpreter check also runs on this shared host.

| Case | Old reuse (ms) | New reuse (ms) | Old reuse allocation calls | New reuse allocation calls | Native peak RSS (KiB, old/new) |
| --- | ---: | ---: | ---: | ---: | ---: |
| remux-encoded-2ch-24bit | 293.00 | 282.41 | 4,204,545 | 8,705 | 428/428 |
| remux-encoded-8ch-24bit | 1251.05 | 1207.66 | 16,806,913 | 22,017 | 860/864 |
| direct-encoded-2ch-24bit-silence | 77.92 | 78.11 | 13,825 | 9,729 | 400/400 |
| direct-encoded-2ch-16bit-noise | 62.36 | 62.56 | 6,657 | 5,633 | 412/412 |

The times and counts are medians. The two music cases show a large reduction in allocation calls and a small reduction in elapsed time. The silence and noise cases have no material timing change in this run. The result establishes no performance lead over other codec implementations.

Input, index, decoder, and initial buffer setup precede the timed interval. Four warm-up rounds precede each measurement. The interval includes seek, decode, and the full playable PCM checksum. Native peak RSS uses Linux VmHWM and includes native setup. The Python process RSS field includes its launch context. All warm loops report zero fresh heap storage, while allocation calls still occur.

The new compiler keeps local checked-arithmetic Options in scalar slots. The isolated 10,000-iteration probe changes from 10,000 allocation calls to zero with identical results. Independent arbitrary-precision tests check 4,158 arithmetic operations across all five integer types and seven operators. Native and WASI execution also check allocation counts and escaped Options. All nine ALAC/CAF suites pass native sanitizers, WASI execution, and ARM64 Android shared compilation.

The JSON records both compiler hashes, source hashes, raw trials, input hashes, PCM counts, checksums, affinity, and CPU quota. The baseline compiler is the supported build from main 3721ce4e. The new compiler includes the checked-Option change and main 0faefdef. Later source merges do not change the measured codec sources. Binary hashes identify the exact builds.

Run the matched comparison from the repository root:

```sh
python3 scripts/bench-caf-buffers --compiler bin/fern-selfhost \
  --baseline-compiler /path/to/baseline/bin/fern \
  --rounds 512 --trials 7 --output caf-compilers.json
```

Physical ARM64 Android speed, peak memory, power, and thermal qualification remain open under #14514 and #14515.
