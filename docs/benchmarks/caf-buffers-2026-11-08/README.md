# CAF output buffers

A CAF cursor can reuse a caller-owned PCM buffer for each packet. The default API still allocates packet output storage. Both APIs decode the same bytes and checksum every playable sample.

The native x86-64 Linux run uses an AMD EPYC 9V74 processor. Each case runs 512 decode rounds in five trials on CPU 0. Trial order rotates across the three modes. The process shares this development host with compiler and interpreter checks. These results describe this host and corpus. They do not establish a lead over other codecs.

| Case | Previous API (ms) | Current default (ms) | Reuse (ms) | Default allocations | Reuse allocations | Native peak RSS (KiB, default/reuse) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| remux-encoded-2ch-24bit | 318.90 | 311.37 | 296.53 | 4,212,737 | 4,204,545 | 432/432 |
| remux-encoded-8ch-24bit | 1592.21 | 1640.69 | 1503.08 | 16,817,153 | 16,806,913 | 864/860 |
| direct-encoded-2ch-24bit-silence | 107.18 | 107.03 | 95.41 | 22,017 | 13,825 | 404/400 |
| direct-encoded-2ch-16bit-noise | 92.18 | 91.57 | 79.70 | 14,849 | 6,657 | 416/412 |

The times and counts are medians. Every mode produces the same sample count and checksum in every trial. All 64 reference cases also compare every decoded sample against the independent ALAC corpus. Eleven boundary groups check buffer size, trim, suffix preservation, completion, empty presentations, malformed packets, and live aliases.

The timed interval includes warm seeks, decode, and the full PCM checksum. It excludes input, index, decoder, and initial buffer setup. Four warm-up rounds precede each measurement. Native peak RSS comes from Linux VmHWM and includes native setup. The process RSS field also includes the Python launch context. Use the native field for this comparison.

Fresh bytes count new heap storage. They exclude storage that the runtime reuses from its free list. All measured loops report zero fresh bytes after warm-up. Allocation calls still occur. The residual decoder allocates a checked-arithmetic Option for each successful scalar operation. Compiler issue #14720 tracks that independent cost.

The JSON file records raw trials, compiler and source hashes, fixture hashes, PCM counts, checksums, and CPU affinity. The previous default uses the CAF implementation from a86bf320. The compiler binary hash identifies the supported build from main 3721ce4e. The source revision in the JSON includes the reusable API and later main changes.

Run the comparison from the repository root:

```sh
python3 scripts/bench-caf-buffers --compiler bin/fern \
  --baseline-stdlib /path/to/a86bf320/internal/stdlib \
  --rounds 512 --trials 5 --output caf-buffer-results.json
```

Without the baseline option, the script compares the current default and reusable APIs. Physical Android timing, power, and thermal results remain open.
