# Quiet ownership changes, 2026-11-09

Quiet nonrecursive chown and chgrp calls now omit unused metadata lookups.
A 200-file batch removes 200 metadata syscalls and retains all 200 ownership syscalls.
The direct-loop cost diagnostic shows 12.0% to 19.1% less time on the four batch rows.
The original short rows have substantial noise. The repeated-shell result remains below the 10% threshold.
This report does not establish a complete utility win or full-catalogue completion.

## Selected loss and implementation

The published November 4 named-group batches lose to GNU coreutils 9.12.
Fern takes 1.84 ms for chown and 1.83 ms for chgrp. GNU takes 1.39 ms and 1.42 ms.
The fresh baseline already beats GNU on those rows on this host. The old source and new source remain separate.

The old path obtains metadata for every operand, even without recursion, verbose output, or a `--from` filter.
The new quiet path calls the ownership operation first.
It reads metadata only after an error, for the original diagnostic.

The failed operation is not retried. Recursive, verbose, and filtered paths retain their metadata checks.
Both utilities share the same ownership implementation. The compiler, libraries, syscall policy, and benchmark sources are unchanged.

Two new GNU parity regressions also require repairs:

- Issue #14924: an empty chown spec must still perform the ownership syscall. On Linux, `chown ':' file` clears set-ID bits.
- Issue #14925: `-H` and `-L` only affect recursive traversal. Without `-R`, `-h -L` must change a broken link itself.

The unchanged baseline fails both regressions. The fixed source passes them on native x86 and emulated ARM.

## Source and measurements

The base is main `12be8f11864c00d71bc3ae9e0c3bc1ddabea75ef`.
The code tree is `41a1d48414886bc18e58d0937d6c0620ebad76a9`. The published code commit is `f8085868b695750875005752bf0190e90d7e092c`.
Both utility columns use the same compiler, SHA256 `2837918ebeab3d89c67c45627f909394fa549ee5b5e3e651cc7486df4b9b5d8a`.
The pinned bootstrap and smoke checks pass. The archive has full source and binary identities.

Later main changes do not inherit these performance claims.

Hyperfine 2.0.0 measures all 20 original workloads, with 80 samples per command and five warmups.
Both command orders contain baseline, candidate, and GNU 9.12 on the same fixtures.
All 9600 primary samples remain in the archive, with standard deviations and every raw observation.
Each workload matches GNU stdout, stderr, status, and relevant file-tree state before measurement.

The original same-owner fixtures and shell choices remain unchanged.
No test or compiler build runs beside the measurements.

Primary means below are milliseconds, forward / reverse. They are observations with the short-row limitation below.

| Workload | Baseline | Candidate | GNU 9.12 |
|---|---:|---:|---:|
| chown 200 numeric groups in one call | 0.900 / 1.200 | 0.680 / 0.995 | 1.452 / 1.642 |
| chown 200 named groups in one call | 1.043 / 0.937 | 0.664 / 0.659 | 2.185 / 1.357 |
| chgrp 200 numeric groups in one call | 0.980 / 0.810 | 0.460 / 0.651 | 1.079 / 1.497 |
| chgrp 200 named groups in one call | 1.024 / 0.681 | 0.775 / 0.576 | 1.406 / 1.361 |

## Noise controls and separate cost diagnostic

Hyperfine warns that the shell-calibrated rows below 5 ms can be inaccurate.
One unchanged recursive path shows an apparent 69.2% gain in one order.
Other recursive controls move in opposite directions. These results do not qualify broad utility speedups.
Some original candidate/GNU rows remain tied, noisy, or slower.

A separate balanced check repeats each original shell invocation 25 times per observation.
It covers all six command permutations, 10 samples per command, and two unchanged recursive controls.
Those 1080 observations show only 3.83% to 9.95% batch reductions. They remain below the 10% threshold.

A second 1080-observation diagnostic repeats the utility command 25 times inside one Bash loop.
It retains the original operands, glob expansion, filesystem mutations, and all three binaries.
It removes repeated inner-shell startup and shell calibration. It is a different launcher method.
It isolates utility cost and does not replace an original published row.

The table gives means per utility invocation in milliseconds, across the six balanced orders.
Individual rounds remain variable. The archive retains all rounds.

| Workload | Baseline | Candidate | GNU 9.12 | Time reduction |
|---|---:|---:|---:|---:|
| chown 200 numeric groups in one call | 1.205 | 0.976 | 1.778 | 19.04% |
| chown 200 named groups in one call | 1.131 | 0.996 | 1.689 | 11.96% |
| chown -R a 400-file tree | 1.332 | 1.335 | 1.760 | -0.22% |
| chgrp 200 numeric groups in one call | 1.192 | 0.967 | 1.725 | 18.91% |
| chgrp 200 named groups in one call | 1.213 | 0.981 | 1.718 | 19.13% |
| chgrp -R a 400-file tree | 1.288 | 1.345 | 1.672 | -4.39% |

The direct-loop recursive controls change by -0.22% and -4.39%.
The separate syscall traces explain the bounded batch cost: baseline and GNU perform 200 metadata calls. Candidate performs none.
All three perform 200 ownership calls. Traced call counts are diagnostics, not speed measurements.

## Correctness and gates

- Native GNU 9.12 parity passes 230 chown and 150 chgrp cases, plus help and version checks, with zero skips.
- Emulated ARM GNU 9.12 parity passes the same 380 cases and help/version checks, with zero skips.
- Sanitized native and emulated ARM candidates pass 94 extra cases against native GNU 9.12. Output, status, modes, ownership, and tree match.
- All sanitized cases report equal allocation and release counts, with no sanitizer findings.
- New cases cover empty specs, set-ID effects, quiet and verbose modes, recursive controls, broken and cyclic links, and mixed errors.
- Full source lint, compiler/stdlib complexity, Go vet, changed-file format, and diff checks pass.
- Chown and chgrp retain their WASI capability refusal. No WASI capability was added.
- Native Darwin checks require the normal hosted platform gate. No native ARM or Darwin speed claim is made.

## Preserved failures and limits

The archive retains the two failed baseline regressions and the GNU probes, with before/after modes and timestamps.
The initial strace stat filter missed `newfstatat`. The corrected filter and original traces both remain.
A proposed proc-file denial control returned success. It is not evidence of permission denial.

A sanitized build first omitted the required stdlib argument. Its setup failure remains.
The first balanced driver used `round` as a loop variable. Its final print failed after all samples completed.
The corrected driver and original source/log both remain. No measurement was rejected or replaced.

The direct-loop execution wrapper reported a nonzero status despite complete data and final output.
Independent validation confirms all 36 blocks, all command statuses, and all samples. The wrapper status is not a passed gate.

The host is one shared Linux x86-64 AMD EPYC 9V74 80-Core Processor machine, with the normal scheduler.
The full published scoreboard remains November 4: 669 rows over 106 utilities.
Its conservative ledger retains 349 ahead, 183 loss, 119 noisy, and 18 rounded-tie rows.
The missing stdbuf utility and ten unverified distribution SELinux references remain explicit.
No average, partial utility result, or merged PR satisfies the all-workload stop condition.

## Evidence

[Raw data and scripts](2026-11-09-owner-quiet-data.tar.gz), 530170 bytes.
Archive SHA256: `4378bd937fb216a1e165a9e394adea45f42b8fa7bb8f32b0f5aec229b4d41e39`.
The archive includes all 11760 observations, commands, sources, hashes, failures, controls, probes, checks, and the per-utility ledger.
Executable copies remain in the local evidence directory. The archive records their hashes and producers.
