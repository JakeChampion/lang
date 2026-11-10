# Version boundaries in listing sorts, 2026-11-09


Status: partial performance evidence for #8278 and #15032. The all-utility stop condition remains false.


The change computes each version suffix boundary once per sort. The previous comparator computes both boundaries on every comparison.

The stable index merge retains the original filename bytes and version rules.

A bytewise tie-break fixes the GNU divergence in #15030. The generic comparator retains equality for `sort -V`.


## Result and controls


The primary comparison covers all 54 original `ls`, `dir`, and `vdir` workloads.

Each command has 80 samples and five warmups in both command orders. This gives 25,920 observations.

The commands use the original direct launcher, arguments, and fixtures from `scripts/coreutils-bench.d/ls.sh`.

Every workload matches GNU 9.12 stdout, stderr, and status before measurement.

A later replay also passes all 162 output and filesystem checks.

The filesystem control covers names, modes, ownership, sizes, symlink targets, mtime, and ctime. It excludes access times.


| Version row | Base ms, forward / reverse | Candidate ms | GNU ms | Time reduction % | GNU / candidate |
|---|---:|---:|---:|---:|---:|
| `ls -v` | 5.086 / 4.941 | 3.992 / 4.024 | 4.914 / 4.778 | 21.51 / 18.56 | 1.23 / 1.19 |
| `dir -v` | 5.188 / 5.253 | 4.738 / 4.485 | 5.760 / 5.116 | 8.68 / 14.61 | 1.22 / 1.14 |
| `vdir -v` | 15.134 / 14.486 | 12.959 / 13.545 | 15.058 / 15.414 | 14.37 / 6.49 | 1.16 / 1.14 |

A second comparison uses six command permutations and the same nine original direct commands.

The rows comprise three version rows, three long-format controls, and three name controls.

Each command has 80 samples and five warmups per permutation. This gives 12,960 observations.

The parent and every contender inherit the first allowed CPU. The shared host does not provide an exclusive CPU.

No tests or builds run during either measurement set. No samples are removed.


| Utility | Original row | Base ms | Candidate ms | GNU ms | Time reduction % | GNU / candidate |
|---|---|---:|---:|---:|---:|---:|
| `ls` | -v (filevercmp) over 4000 names | 4.382 | 3.589 | 4.745 | 18.10 | 1.322 |
| `ls` | -l over 4000 names | 10.487 | 11.170 | 13.397 | -6.52 | 1.199 |
| `ls` | 4000 names, no stat | 2.344 | 2.466 | 2.911 | -5.22 | 1.180 |
| `dir` | -v (filevercmp) over 4000 names | 5.233 | 4.653 | 4.981 | 11.10 | 1.071 |
| `dir` | -l over 4000 names | 10.810 | 11.379 | 12.998 | -5.27 | 1.142 |
| `dir` | 4000 names, no stat | 3.033 | 3.097 | 3.248 | -2.11 | 1.049 |
| `vdir` | -v (filevercmp) over 4000 names | 13.053 | 12.974 | 15.016 | 0.61 | 1.157 |
| `vdir` | -l over 4000 names | 11.004 | 10.881 | 13.430 | 1.12 | 1.234 |
| `vdir` | 4000 names, no stat | 11.374 | 11.674 | 13.028 | -2.64 | 1.116 |

The balanced `ls -v` reduction is 18.10%. Each of its six permutations improves by more than 10%.

The balanced `dir -v` reduction is 11.10%, but one permutation improves only 3.4%.

The balanced `vdir -v` reduction is 0.61%. It does not establish a speedup.

Control averages remain within 10%, but individual rounds vary more. The raw data retains every round and standard deviation.

These results do not establish complete utility wins. Fresh baselines already exceed GNU on some rows.


## Source and binary identities


- Base source: `fb7148ee91b0dad0c4fcd6f371cae731e68c55f5`.

- Candidate local source: `5c394a947fece0dab018d30bd5c2ae3f47ce074a`.

- Published product and oracle repair: `e911cc2bd86bbcf032fdba787b2ded0894fdd2ee`.

- Product tree before the oracle repair: `e8ad848c6139ea8b297c459a1807cd70b67e8998`.

- Compiler SHA256: `0009b870a410da441bfad17be77c144c7a0cf9d589c70dd1ace214acfb898209`.


The pinned stage0 is `stage0-20261109-a19e872`.

Its SHA256 is `a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751`.

Bootstrap and smoke checks pass. Both utility columns use this compiler, the same libraries, and the same original fixtures.

The base binaries reproduce earlier hashes. These measurements are fresh and do not transfer older time columns.

No CPU baseline, FFI, platform capability, workload, or baseline file changes.


`primary/metadata.json` records every command, binary hash, output hash, tool version, and source patch.

Source inventories identify the compiler, libraries, utilities, bootstrap, and test sources.

The archive excludes executables. `executable-identities.json` records their exact hashes and local paths.

The compiler and executable copies remain in the local evidence directories.

No time claim applies to later main sources without fresh qualification.


## Correctness and known gaps


GNU 9.12 parity passes 683 ordinary cases each for `ls`, `dir`, and `vdir` on native x86 and QEMU ARM.

The count includes 12 new suffix, zero-prefix, reverse, directory-group, and layout cases per utility.

Help/version and 498 ordinary `sort` cases pass on both platforms.

Native and ARM allocation checks pass. Each utility has 643 balanced returns and 40 usage/error exits per platform.

The native sort allocation corpus has 379 balanced returns and 119 usage/error exits.

WASI sort byte checks pass with balanced allocations. The three directory utilities retain E066 on WASI.

Native Darwin remains unverified locally. No native ARM or Darwin time claim applies.


The initial ARM sort byte command skipped its corpus. Its helper executed ARM GNU directly on x86 during the version probe.

The repair passes `crossArgv` to an explicit reference-command helper. Issue #15035 tracks this defect.

The repaired byte corpus and real-GNU command-prefix regression each pass 173 cases on native x86 and QEMU ARM.

The repaired runs have zero skips. Each corpus contains 167 GNU comparisons and six prior expected filename refusals.

Those six refusals are not GNU parity evidence. The original assertions and diagnostic rules remain unchanged.


Full sourcelint, Go vet, harness tests, Go/Fern format, and diff checks pass.

The first native test selector omitted `TestSortParity`. A separate exact selector ran all 498 cases.

Both logs remain in the archive. The original ARM skip remains in the archive.


The new raw-filename probes expose the baseline #15031 gap.

GNU lists a filename with bytes `61 ff`. Both baseline and candidate reject the directory with an EILSEQ diagnostic.

The original failed fixture, source, GNU bytes, statuses, and logs remain in the archive and local commit `692b0f8f9`.

The new fixture that passes uses valid filenames. No prior repository case or assertion was removed.

This unresolved raw-filename gap prevents complete correctness for these utilities and all-utility acceptance.


## Rejected and prior experiments


The numeric-width experiment passes 2,013 prior cases for these utilities and help/version.

Its 1,440 samples do not establish a repeatable improvement. The experiment is rejected.

Its source remains in local commit `d94b171b0` and in the archive.

Two earlier version-cache pilots each contain 1,440 samples. They precede the final tie-break fix.

The first prototype also passes the cache array through the ordinary comparator path.

The final source limits that path to version sorts. Pilot results are separate from the primary comparison.

The second prototype executable was overwritten. A later rebuild reproduces its recorded SHA256 exactly.

`prototype2-reproduction.json` records this reconstruction. It is not a recovered original file.


The archive also preserves the earlier stat-cost diagnosis at main `37545d63293ee8aae9419fec4ad554b97929e260`.

Its 1,920 utility samples and 320 C syscall samples are separate observations.

Both GNU and Fern use full paths with `statx(AT_FDCWD, ...)`. The held-directory mismatch hypothesis is rejected.

The request-mask probe has no repeatable 10% advantage. No stat API or global metadata-mask change follows from it.

The original incorrect hypothesis and corrected traces remain visible. Traced durations are not performance measurements.


## Catalogue ledger


The published full scoreboard remains the 2026-11-04 run: 669 rows over 106 utilities.

Its conservative rounded ledger retains 349 ahead, 183 `losing`, 119 noisy, and 18 tied rows.

The catalogue lacks `stdbuf`. Ten SELinux distribution-reference rows lack exact GNU 9.12 eligibility proof.

`catalogue-ledger.json` retains every published row and per-utility record.

It adds these partial observations, the raw-filename gap, and the six sort refusal cases without a change to the published totals.

The stop condition remains false. No average or selected utility result can satisfy the all-workload requirement.


## Complete primary table


Times are milliseconds. Each pair is forward / reverse command order.

The raw files contain every sample, user/system time, memory observation, standard deviation, and exit code.


| Utility | Original row | Base ms | Candidate ms | GNU ms | Time reduction % | GNU / candidate |
|---|---|---:|---:|---:|---:|---:|
| `ls` | 4000 names, no stat | 2.509 / 2.465 | 2.635 / 2.628 | 3.524 / 2.970 | -5.03 / -6.62 | 1.34 / 1.13 |
| `ls` | -l over 4000 names | 12.136 / 10.024 | 12.600 / 9.943 | 13.966 / 11.967 | -3.82 / 0.81 | 1.11 / 1.20 |
| `ls` | -U (unsorted) over 4000 names | 2.239 / 1.635 | 1.657 / 1.593 | 2.096 / 1.874 | 26.01 / 2.55 | 1.27 / 1.18 |
| `ls` | -v (filevercmp) over 4000 names | 5.086 / 4.941 | 3.992 / 4.024 | 4.914 / 4.778 | 21.51 / 18.56 | 1.23 / 1.19 |
| `ls` | -t over 4000 names | 9.130 / 10.043 | 10.130 / 9.139 | 7.994 / 6.837 | -10.96 / 9.00 | 0.79 / 0.75 |
| `ls` | -S over 4000 names | 10.538 / 8.288 | 10.520 / 8.527 | 9.069 / 8.101 | 0.18 / -2.89 | 0.86 / 0.95 |
| `ls` | -C -w 200 over 4000 names | 2.976 / 2.927 | 3.214 / 3.016 | 3.843 / 3.930 | -8.03 / -3.01 | 1.20 / 1.30 |
| `ls` | -x -w 200 over 4000 names | 2.722 / 2.730 | 2.951 / 2.984 | 3.799 / 3.697 | -8.39 / -9.30 | 1.29 / 1.24 |
| `ls` | -m -w 200 over 4000 names | 2.961 / 2.644 | 2.914 / 2.776 | 3.426 / 3.379 | 1.59 / -5.01 | 1.18 / 1.22 |
| `ls` | -i -s over 4000 names | 9.789 / 10.550 | 9.047 / 10.128 | 8.495 / 8.411 | 7.58 / 4.00 | 0.94 / 0.83 |
| `ls` | -F over 1500 mixed entries | 1.847 / 1.669 | 1.848 / 1.666 | 2.080 / 1.960 | -0.08 / 0.15 | 1.13 / 1.18 |
| `ls` | -l over 1500 mixed entries | 4.070 / 4.237 | 4.499 / 4.045 | 5.131 / 5.551 | -10.54 / 4.55 | 1.14 / 1.37 |
| `ls` | --color=always over 1500 mixed | 1.128 / 1.131 | 1.125 / 1.187 | 1.553 / 1.650 | 0.20 / -4.91 | 1.38 / 1.39 |
| `ls` | -R over a 40-deep tree | 0.721 / 0.729 | 0.713 / 0.707 | 1.185 / 1.232 | 1.07 / 3.10 | 1.66 / 1.74 |
| `ls` | -lR over a 40-deep tree | 3.231 / 3.286 | 3.543 / 3.334 | 5.881 / 6.560 | -9.65 / -1.47 | 1.66 / 1.97 |
| `ls` | -b over 4000 names | 2.671 / 2.702 | 2.891 / 2.816 | 3.306 / 3.048 | -8.26 / -4.20 | 1.14 / 1.08 |
| `ls` | --quoting-style=shell-escape | 2.764 / 2.777 | 2.807 / 3.221 | 2.902 / 3.160 | -1.54 / -16.00 | 1.03 / 0.98 |
| `ls` | --time-style=full-iso -l | 11.695 / 14.051 | 11.830 / 12.269 | 13.242 / 13.661 | -1.15 / 12.68 | 1.12 / 1.11 |
| `dir` | 4000 names, no stat | 3.266 / 3.254 | 3.382 / 3.406 | 3.563 / 3.964 | -3.56 / -4.68 | 1.05 / 1.16 |
| `dir` | -l over 4000 names | 10.721 / 11.848 | 11.368 / 11.580 | 12.697 / 13.891 | -6.03 / 2.27 | 1.12 / 1.20 |
| `dir` | -U (unsorted) over 4000 names | 2.409 / 2.388 | 2.418 / 2.303 | 2.646 / 2.578 | -0.38 / 3.56 | 1.09 / 1.12 |
| `dir` | -v (filevercmp) over 4000 names | 5.188 / 5.253 | 4.738 / 4.485 | 5.760 / 5.116 | 8.68 / 14.61 | 1.22 / 1.14 |
| `dir` | -t over 4000 names | 9.931 / 10.032 | 10.588 / 10.528 | 7.679 / 8.206 | -6.62 / -4.95 | 0.73 / 0.78 |
| `dir` | -S over 4000 names | 8.922 / 11.785 | 9.130 / 11.074 | 7.358 / 9.909 | -2.33 / 6.04 | 0.81 / 0.89 |
| `dir` | -C -w 200 over 4000 names | 3.169 / 3.623 | 3.410 / 4.044 | 4.002 / 4.343 | -7.60 / -11.63 | 1.17 / 1.07 |
| `dir` | -x -w 200 over 4000 names | 3.135 / 3.504 | 3.227 / 3.785 | 3.775 / 4.106 | -2.93 / -8.02 | 1.17 / 1.08 |
| `dir` | -m -w 200 over 4000 names | 3.640 / 4.397 | 3.980 / 3.958 | 3.170 / 3.736 | -9.32 / 9.99 | 0.80 / 0.94 |
| `dir` | -i -s over 4000 names | 9.366 / 14.179 | 12.087 / 15.951 | 10.214 / 13.547 | -29.05 / -12.49 | 0.85 / 0.85 |
| `dir` | -F over 1500 mixed entries | 2.148 / 2.092 | 2.128 / 2.165 | 2.268 / 2.682 | 0.90 / -3.49 | 1.07 / 1.24 |
| `dir` | -l over 1500 mixed entries | 4.301 / 4.637 | 4.594 / 4.493 | 5.302 / 6.270 | -6.81 / 3.11 | 1.15 / 1.40 |
| `dir` | --color=always over 1500 mixed | 1.207 / 1.402 | 1.377 / 1.416 | 1.876 / 1.911 | -14.05 / -0.96 | 1.36 / 1.35 |
| `dir` | -R over a 40-deep tree | 0.871 / 0.921 | 0.925 / 0.899 | 1.392 / 1.473 | -6.14 / 2.38 | 1.51 / 1.64 |
| `dir` | -lR over a 40-deep tree | 3.443 / 3.696 | 3.887 / 3.651 | 7.383 / 6.367 | -12.93 / 1.22 | 1.90 / 1.74 |
| `dir` | -b over 4000 names | 3.462 / 3.416 | 3.357 / 3.553 | 4.661 / 3.828 | 3.06 / -3.99 | 1.39 / 1.08 |
| `dir` | --quoting-style=shell-escape | 4.114 / 3.727 | 4.321 / 3.960 | 3.560 / 3.584 | -5.02 / -6.25 | 0.82 / 0.90 |
| `dir` | --time-style=full-iso -l | 13.759 / 13.210 | 13.860 / 13.746 | 14.855 / 13.827 | -0.73 / -4.06 | 1.07 / 1.01 |
| `vdir` | 4000 names, no stat | 12.464 / 12.563 | 13.107 / 12.825 | 15.549 / 13.660 | -5.16 / -2.08 | 1.19 / 1.07 |
| `vdir` | -l over 4000 names | 13.131 / 13.244 | 12.320 / 13.104 | 14.161 / 14.952 | 6.18 / 1.06 | 1.15 / 1.14 |
| `vdir` | -U (unsorted) over 4000 names | 11.909 / 10.480 | 10.534 / 10.248 | 12.137 / 13.279 | 11.55 / 2.21 | 1.15 / 1.30 |
| `vdir` | -v (filevercmp) over 4000 names | 15.134 / 14.486 | 12.959 / 13.545 | 15.058 / 15.414 | 14.37 / 6.49 | 1.16 / 1.14 |
| `vdir` | -t over 4000 names | 12.583 / 12.662 | 13.057 / 13.472 | 13.908 / 13.484 | -3.76 / -6.40 | 1.07 / 1.00 |
| `vdir` | -S over 4000 names | 13.145 / 12.579 | 13.190 / 13.293 | 15.298 / 16.214 | -0.35 / -5.68 | 1.16 / 1.22 |
| `vdir` | -C -w 200 over 4000 names | 3.893 / 3.633 | 3.738 / 3.801 | 3.969 / 4.209 | 3.98 / -4.64 | 1.06 / 1.11 |
| `vdir` | -x -w 200 over 4000 names | 3.258 / 3.669 | 3.389 / 3.801 | 3.576 / 3.922 | -4.00 / -3.60 | 1.06 / 1.03 |
| `vdir` | -m -w 200 over 4000 names | 3.549 / 3.727 | 4.456 / 4.166 | 3.758 / 4.017 | -25.57 / -11.77 | 0.84 / 0.96 |
| `vdir` | -i -s over 4000 names | 14.080 / 14.016 | 13.365 / 13.310 | 14.960 / 14.972 | 5.07 / 5.04 | 1.12 / 1.12 |
| `vdir` | -F over 1500 mixed entries | 4.620 / 4.963 | 4.607 / 5.208 | 5.558 / 6.151 | 0.29 / -4.92 | 1.21 / 1.18 |
| `vdir` | -l over 1500 mixed entries | 3.938 / 4.299 | 4.449 / 4.363 | 5.398 / 5.568 | -12.98 / -1.49 | 1.21 / 1.28 |
| `vdir` | --color=always over 1500 mixed | 4.286 / 4.795 | 4.044 / 4.676 | 5.244 / 5.673 | 5.63 / 2.50 | 1.30 / 1.21 |
| `vdir` | -R over a 40-deep tree | 3.937 / 3.561 | 3.561 / 3.719 | 6.012 / 6.132 | 9.56 / -4.45 | 1.69 / 1.65 |
| `vdir` | -lR over a 40-deep tree | 3.773 / 3.648 | 3.546 / 3.696 | 5.760 / 6.122 | 6.02 / -1.32 | 1.62 / 1.66 |
| `vdir` | -b over 4000 names | 12.191 / 12.358 | 13.025 / 13.086 | 13.159 / 14.571 | -6.84 / -5.90 | 1.01 / 1.11 |
| `vdir` | --quoting-style=shell-escape | 11.532 / 12.227 | 11.545 / 12.781 | 13.320 / 14.586 | -0.11 / -4.53 | 1.15 / 1.14 |
| `vdir` | --time-style=full-iso -l | 14.452 / 13.156 | 13.080 / 13.303 | 13.133 / 14.097 | 9.49 / -1.12 | 1.00 / 1.06 |

## Raw archive


The archive contains 645 manifest entries. Its size is 3772844 bytes.


SHA256: `33b5cf9f7d8af2742882953efeaa2dd2f9a10675f64ccd76e22b43b994854401`.


The parts preserve the original gzip bytes. Concatenate them in filename order.


```sh
cat docs/coreutils-log/2026-11-09-version-prefix-data.tar.gz.part-* > /tmp/version-prefix-data.tar.gz
sha256sum /tmp/version-prefix-data.tar.gz
tar -xzf /tmp/version-prefix-data.tar.gz -C /tmp
```

The adjacent parts manifest records each size and hash. `archive-manifest.json` records every evidence file hash.

