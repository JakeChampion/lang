# Version prefix qualification on current main, 2026-11-09

This addendum qualifies native x86 code from main `4a04428264dd4641ca1032cf5edc84762a883dd5` plus the #15032 product and test patch.

The [primary report](2026-11-09-version-prefix.md) uses the earlier controlled source. Its measurements remain separate.

## Build and correctness

The corrected bootstrap uses the fresh main worktree. Bootstrap and smoke checks pass.

Compiler SHA256: `48978406291c39f67a96b1a6e428e511c5cd1614c7ed0c2788f343811b8677ff`.

The stage0 is `stage0-20261109-a19e872`. Its SHA256 is `a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751`.

All six native utility binaries differ from the primary report. No earlier time column transfers to these binaries.

A later rebuild without `-O` exactly reproduces all six measured binary hashes.

Fresh native GNU 9.12 checks pass 683 cases each for `ls`, `dir`, and `vdir`, plus help/version.

They also pass 498 ordinary sort cases, 173 sort byte cases, and 173 real-GNU command-prefix cases. The command has zero skips.

Each byte corpus retains six expected filename refusals. Those six cases remain outside GNU equality evidence.

This new local qualification covers native x86. It does not transfer earlier ARM or WASI checks to the new compiler.

Native ARM and Darwin execution remain outside this local result. Hosted platform gates remain necessary.

## Fresh matched measurements

The nine original direct commands cover three version rows, three long-format controls, and three name controls.

Each contender has 40 samples and five warmups in both orders. This gives 2,160 new observations.

Each command matches GNU 9.12 stdout, stderr, and status before measurement. No tests or builds run during measurement.

The commands, options, direct launcher, and fixtures match the original benchmark. The shared host has no exclusive CPU.

| Utility | Original row | Base ms, forward / reverse | Candidate ms | GNU ms | Time reduction % | GNU / candidate |
|---|---|---:|---:|---:|---:|---:|
| `ls` | -v (filevercmp) over 4000 names | 4.998 / 4.318 | 3.902 / 3.786 | 4.554 / 4.491 | 21.93 / 12.30 | 1.17 / 1.19 |
| `ls` | -l over 4000 names | 12.130 / 12.570 | 11.408 / 11.978 | 13.867 / 13.477 | 5.95 / 4.70 | 1.22 / 1.13 |
| `ls` | 4000 names, no stat | 2.363 / 2.465 | 2.560 / 2.596 | 3.336 / 3.106 | -8.36 / -5.32 | 1.30 / 1.20 |
| `dir` | -v (filevercmp) over 4000 names | 4.972 / 5.037 | 4.255 / 4.407 | 4.811 / 5.501 | 14.42 / 12.50 | 1.13 / 1.25 |
| `dir` | -l over 4000 names | 10.114 / 11.914 | 10.894 / 11.908 | 11.870 / 13.433 | -7.70 / 0.04 | 1.09 / 1.13 |
| `dir` | 4000 names, no stat | 2.974 / 4.571 | 3.538 / 3.393 | 3.346 / 3.520 | -18.97 / 25.77 | 0.95 / 1.04 |
| `vdir` | -v (filevercmp) over 4000 names | 13.912 / 13.629 | 14.068 / 13.061 | 15.760 / 15.310 | -1.12 / 4.16 | 1.12 / 1.17 |
| `vdir` | -l over 4000 names | 11.605 / 10.822 | 12.882 / 11.332 | 14.346 / 13.240 | -11.00 / -4.71 | 1.11 / 1.17 |
| `vdir` | 4000 names, no stat | 12.214 / 11.436 | 12.274 / 10.686 | 13.461 / 12.816 | -0.49 / 6.56 | 1.10 / 1.20 |

The version rows have lower candidate means for `ls` and `dir` in both orders. The `vdir` increment remains inconclusive.

The `dir` name control moves -18.97% and +25.77%. This replay does not isolate its incremental benefit.

Control movement and standard deviations remain visible in the raw data. No complete utility or full-catalogue win follows.

The #15031 raw-filename gap remains unresolved. The six sort refusal cases remain ineligible as GNU equality evidence.

The 2026-11-04 full scoreboard remains unchanged. The all-utility stop condition remains false.

## Preserved setup failure

The first additional bootstrap used the PR checkout as its current directory. It rebuilt the earlier compiler, not main `4a044282`.

The complete wrong-directory logs, binaries, and source explanation remain local under `version-prefix-integrated-wrong-cwd`.

The archive includes its logs and exact binary identities. No speed or integrated-source claim uses that build.

The corrected build explicitly uses `lang-coreutils-version-integrated`. Its patch and source inventory accompany the new measurements.

## Raw data

The archive has 94 manifest entries and 459863 bytes.

SHA256: `91eb45e20230416e854c5278ac34be1e8827facb86babf276a8b9d03a88ba0b4`.

The export inspection finds no credential-pattern matches. The archive contains task source, synthetic fixture outputs, test logs, and measurements.

Concatenate the parts in filename order. Verify the archive and manifest hashes before use.

```sh
cat docs/coreutils-log/2026-11-09-version-prefix-integrated-data.tar.gz.part-* > /tmp/version-prefix-integrated.tar.gz
sha256sum /tmp/version-prefix-integrated.tar.gz
tar -xzf /tmp/version-prefix-integrated.tar.gz -C /tmp
```
