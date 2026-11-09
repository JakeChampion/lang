# Bounded SIMD byte counts, 2026-11-09

The x86 byte-count kernel now accumulates matches across 1 KiB batches.
Isolated scan time falls by 32.3% to 43.5% against the unchanged compiler baseline.
The utility comparisons do not establish a consistent wc or tail improvement over that baseline.
This change does not establish a complete utility win or full-catalogue completion.

## Measured loss and implementation

The November 4 scoreboard contains wc line-count losses.
The later integrated comparison also retains regular-file and pipe losses.
The selected operation is the byte count in `count_stream`, not a larger read buffer.
Earlier buffer-size experiments remain rejected.

The original x86 kernel extracts a scalar mask and population count for every AVX2 vector.
The new kernel accumulates equal bytes across 32 vectors before the horizontal sum.
Each byte lane holds at most 32 matches, below the overflow limit.
`VPSADBW` sums the lanes, and four scalar additions update the count.
The temporary stack space returns immediately. The operation allocates no heap storage.

The 1 KiB path only reads complete batches.
The original 128-byte, 32-byte, 16-byte, and scalar paths handle the remainder.
Inputs below 128 bytes avoid the new batch check.
Strings and packed byte arrays share this kernel.
The unpacked-array path and the other target implementations retain their code.
The CPU baseline remains AVX2.

## Source and method

Both compilers use main `d04e79179fda465d5897d25a00f9a2f34c4694ab` and the same pinned stage0.
The utility and library sources are identical between the two columns.
The measured implementation tree is `1554059791023c6692fa6dd7e9e5ec56f34d1d81`.
The published code commit is `c427cc36363d936e28b6cfce04c67fa0455b2316`.
The later main merge adds test changes without changes to the measured compiler, libraries, or utilities.

The baseline compiler SHA256 is `eb5f672880abf9e65aad61edbab650424cb0e697d75b2a530987e9708583a62d`.
The candidate compiler SHA256 is `6ddd41bffce97a733817c9109f4900e7f78b6b6461b0b76c001a49d14d6e35b7`.
Both bootstrap and smoke checks pass.

Hyperfine 2.0.0 runs 80 samples per command after five warmups, in each of two opposite orders.
The primary comparison contains 6240 eligible samples.
It covers all six wc workloads, all five tail workloads, and three auxiliary scan kernels.
Every utility workload matches GNU coreutils 9.12 stdout, stderr, and exit status before measurement.
The fixture remains the original 62888896-byte input with eight million lines.
No test or compiler build runs alongside the samples.

The final launcher choices follow the published workload scripts.
The archive retains an earlier 2400-sample tail comparison with direct launches as a separate diagnostic.
That comparison does not supply a primary result.
Another 640-sample prototype places the batch guard before the short-input guard.
Its source, identities, samples, and commands remain separate from the final candidate.

## Isolated scan results

Each kernel counts 65536 bytes 20000 times and verifies its checksum.
The sparse-byte input repeats a 19-byte pattern.
The dense-byte input contains zeros. The string input contains newlines.

These kernels isolate scan cost. They are not GNU utility wins.
Times are means in milliseconds.

| Kernel | Baseline forward / reverse | Candidate forward / reverse | Time reduction forward / reverse |
|---|---:|---:|---:|
| Sparse packed bytes | 24.021 / 23.859 | 13.898 / 13.476 | 42.14% / 43.52% |
| Dense packed bytes | 23.984 / 22.292 | 13.716 / 13.808 | 42.81% / 38.06% |
| String bytes | 22.339 / 21.454 | 14.021 / 14.522 | 37.23% / 32.31% |

## Utility results and controls

The complete tables and standard deviations are in the archive.
The wc line-count file takes 12.268 / 9.538 ms, against the original 12.253 / 10.964 ms.
The wc line-count pipe takes 14.613 / 13.120 ms, against the original 14.594 / 13.094 ms.

The pipe remains slower than GNU at 13.778 / 11.363 ms.
The unchanged byte-count control also moves substantially between execution orders.
These observations do not establish a consistent end-to-end wc improvement.

The four-million-line tail workload takes 13.015 / 12.510 ms, against the original 13.206 / 13.368 ms.
GNU takes 35.425 / 37.249 ms on those same runs.
The candidate remains substantially faster than GNU on that workload, but its incremental gain remains within the 10% noise rule.
Other tail increments also remain inconsistent or within noise.
The `tail -c` control moves in opposite directions despite its unchanged data path.
No utility-wide speedup is attributed to this kernel change.

## Correctness checks

- GNU 9.12 probes cover 72 dense and mixed file/pipe cases at batch and read-buffer boundaries.
- The expanded packed-byte corpus passes on the interpreter, native x86-64, emulated ARM64, Wasm modules, and WASI components.
- Cases cover dense data, sparse data, high bytes, invalid search values, aliases, and zero storage beyond the input.
- Batch tests cover adjacent lengths through 65537 bytes and repeated dense scans without allocations.
- Each of x86-64, ARM64, and WebAssembly passes 5800 string-count cases against the Go reference.
- New `VPSADBW` register and memory forms compare byte-exactly with GNU assembler output.
- The AVX2 emission check passes.
- Native and emulated ARM64 wc parity passes 159 ordinary cases, 40 byte cases, and 40 real-GNU prefix cases.
- Native and emulated ARM64 tail parity passes 226 ordinary cases and the shared byte corpus.
- Help/version checks and sanitized wc/tail corpora pass on the applicable x86-64, ARM64, and WASI paths.
- Source lint, complexity, Go analysis, format, and diff checks pass without baseline changes.

The kernel suite reports one native Apple Silicon platform skip on this Linux host.
That result remains visible in the raw log.
Native Darwin execution remains unverified locally.

## Evidence and limits

The [raw archive](2026-11-09-count-batch-data.tar.gz) contains exact identities, source inventories, patches, probes, samples, commands, and test logs.
It preserves both earlier experiments and the original launcher choices.
Performance uses one shared AMD EPYC Linux x86-64 host with the normal scheduler.
ARM64 results establish correctness under QEMU, not native speed.
Required current-head CI still applies.

The ledger retains 183 published loss rows, 18 rounded ties, and 119 noisy rows.
It keeps the missing `stdbuf` utility and the ten distribution SELinux reference eligibility gaps visible.
The published scoreboard remains the November 4 full run.
The all-utility stop condition remains false.
