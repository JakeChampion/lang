# Stat formatter experiments, 2026-11-09

Two formatter candidates reduce allocations but do not establish a speed improvement.
Neither candidate changes the product source in this pull request.
The separate test repair preserves the selected emulator command for file-context checks.
Issue #14902 records that test failure.

## Measured loss and candidates

The November 4 scoreboard reports 11.48 ms for 4000 operands with `%A` and `%F`.
GNU coreutils 9.12 takes 8.10 ms on that published workload.
These older values select the investigation. They do not supply a comparison column for this experiment.

The first candidate converts a complete string directly to bytes when width and precision need no change.
It retains the original formatter for every other case.
The second candidate returns complete strings through the current text output path.
The eight new local cases check widths and precisions at adjacent string lengths.

Both candidates use main `8507ad3b563d6cdb8476fc95ce82d68367efd1dc` and the same compiler and libraries.
The compiler source matches the baseline source from the earlier count-kernel investigation.
The unmerged x86 count-kernel change is absent from both performance candidates.
No benchmark workload changes.

## Results and controls

Each complete comparison covers all 14 stat workloads in both execution orders.
Each command supplies 80 samples after five warmups.
The two comparisons contain 13440 eligible samples.
Earlier pilots contain another 720 samples.

For the mode/type workload, the first candidate removes 16000 allocations from the original count of 96119.
The direct-text candidate removes 24030 allocations.
Separate instrumented binaries supply these counts. Their runs do not enter the performance samples.

The direct-text candidate takes 13.716 ms against the original 11.825 ms in forward order.
It takes 9.536 ms against 10.950 ms in reverse order.
This reversal requires another control.

Six balanced command orders cover mode/type output, timestamps, and unchanged numeric output.
Each command supplies 30 samples after three warmups, for another 1620 eligible samples.
Each implementation occupies each command position twice.

| Workload | Original ms | Direct text ms | GNU 9.12 ms |
|---|---:|---:|---:|
| 4000 operands, `%A` and `%F` | 10.122 | 9.470 | 9.055 |
| 4000 operands, local timestamp | 11.001 | 10.745 | 9.968 |
| 4000 operands, `%s`, unchanged control | 9.187 | 8.581 | 9.171 |

The mode/type difference is 6.44%. The unchanged numeric control moves 6.59%.
Neither candidate establishes an improvement beyond the published 10% noise threshold.
Both remain rejected performance experiments. They do not establish a GNU win.

## Correctness and test repair

Both candidates pass 772 ordinary GNU parity cases on native x86-64.
These include the eight local boundary cases.
All 14 measured workloads match GNU stdout, stderr, and exit status.
The direct-text candidate passes the sanitized byte corpus on x86-64 and emulated ARM64.
The WASI capability-refusal check also passes.

The first ARM64 run passes the ordinary and byte corpora but fails `TestStatFileContext`.
That test invokes an ARM64 binary directly on x86-64 and reports `exec format error`.
The test now uses `crossArgv`, like the main corpus.
All three context assertions remain unchanged.

The repaired test runs with the original product formatter and the unmerged count-kernel compiler.
Both native and ARM64 runs pass 764 ordinary parity cases, 61 byte cases, and all three context checks.
Help and version checks also pass. Neither run skips a case.
The rejected formatter source is not necessary for this repair.

## Evidence and limits

The [raw archive](2026-11-09-stat-diagnostic-data.tar.gz) contains samples, commands, identities, source patches, probes, test logs, and controls.
It preserves the failed ARM64 run and the corrected runs.
The archive also records the initial catalogue-parser failure on an escaped pipe and its repair.

The catalogue ledger keeps 183 loss rows, 18 rounded ties, and 119 noisy rows visible.
It also keeps the absent `stdbuf` utility and ten distribution SELinux reference rows visible.
Those ten rows need exact GNU 9.12 eligibility evidence before they can satisfy the stop condition.
This ledger refines triage labels. It does not replace the published full run.

All performance samples use one shared Linux x86-64 host with the normal scheduler.
ARM64 results establish correctness under QEMU, not native speed.
Native Darwin execution remains unverified here.
The full-catalogue objective remains open.
