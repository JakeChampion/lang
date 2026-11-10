# Rejected lazy lookup after the fixed compiler timing control

Status: [record] rejected experiment from 2026-11-10.

The lazy argument-target shortcut is rejected.
It reduces allocation events and guest instructions on interpreter subsets.
The complete-compilation timing studies retain higher candidate medians with substantial paired variation.
Those work-count benefits do not justify the unresolved compilation cost.
The timing cause remains unknown.

The final product retains eager lookup and the complete upstream `Funcs` Map index.
The two regression files still cover generic calls and same-count REPL replacement.
The [decision record](decision.json) and [removed patch](removed-trial.patch.gz) preserve the rejected code and its rationale.
Earlier measured archives remain unchanged with their exact source identities.

## Fixed design

The baseline compiler source is `c9547605f513b81cb1c26d1cb4deaf37b20648bd`.
The lazy candidate source is `c05b5ce874fc4a6ec6b0be45ee71d3ed1bd5f8f1`.
Both executables use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The fixed Opus reference program and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
The [earlier comparison](../compiler-interpreter-lazy-main-2026-11-10/README.md) retains the original timing loss and near-equal complete-compilation instruction totals.

This control uses copies of those exact executables at equal-length `base/fern` and `cand/fern` paths.
The collector verifies their SHA-256 before every run.
It fixes eight pairs per proof-cache mode before collection starts.
Four pairs use each compiler order.
No result changes the sample count or stopping rule.

Each executable primes its own proof cache and runs a separate probe.
Both probes report 112 hits and zero misses.
Actual measurements use normal verifier defaults without the report flag.
Cold compilation disables the proof cache.
This is not a cold operating-system page-cache study.

Normal executables omit debug variables, embedded source, profiles, and allocation counters.
Ownership certification remains on.
Per-child `wait4` reports CPU time and peak RSS.
The host remains shared, without exclusive CPU or frequency control.
Small read-only CI observations can overlap.

## Complete-compilation results

All 36 commands exit zero.
These commands comprise 32 timed samples, two cache primes, and two probes.
Every command emits identical native output with SHA-256 `e8ddd643bdc9d72008d930fc42ee8f1574d6f39ae3628891ea0f828f3c67b46f`.
The [raw records](results.json) retain every sample, command, resource measurement, and identity.

| Measurement | Main baseline | Lazy candidate |
| --- | ---: | ---: |
| Cold median wall time | 5.450318910 s | 5.641994974 s |
| Cold median CPU time | 5.449747500 s | 5.638404500 s |
| Cold median peak RSS | 160,528 KiB | 160,400 KiB |
| Cold wall range | 4.646941831–6.603098000 s | 4.575578656–7.135696955 s |
| Warm median wall time | 5.238635150 s | 5.286722946 s |
| Warm median CPU time | 5.237144500 s | 5.286296000 s |
| Warm median peak RSS | 160,016 KiB | 160,016 KiB |
| Warm wall range | 4.760095331–6.388420644 s | 4.980594075–5.911233291 s |

Paired cold CPU changes are -1.5320%, +2.8350%, +7.2271%, +3.5484%, -3.1734%, +6.8545%, +16.8504%, -15.3563%.
Paired warm CPU changes are -7.9303%, +13.0405%, -4.5630%, -3.2848%, +5.2332%, +0.5889%, +5.0748%, -17.9286%.
Negative values indicate less candidate CPU time.
Five cold pairs and four warm pairs use more candidate CPU time.
The paired changes have large variation.
Both candidate medians remain higher.

The results do not establish a specific code-layout or residency cause.
They do not establish a general compiler speedup.
No earlier timing or work-count sample is removed.
This study remains separate from the earlier comparison.

## Retained source and correctness

Retained source `9288f36037cd951751a4907c90879f6901483526` removes the lazy shortcut.
Its compiler and standard library exactly match main `c9547605`.
The [source audit](post-run-source-check.json) verifies that equality.
The fresh production rebuild is byte-identical to the measured main baseline.
Its SHA-256 is `bead6a81a9a0d0f31d5b84afc85e907e6cc28cfc9f07e488e0d94d9baf0d62c0`.

Pinned source check, read-only format check, and Go vet exit zero.
The [targeted validation](retained-main/validation.json) reports 413 passing test and subtest results and zero skips.
The suites cover x86-64, ARM64 under QEMU, WebAssembly, the function-count guard, and nested-owned-field negative controls.
The generic-call and same-count session regressions remain active.

The rebuilt normal compiler completes all 1,896 reference records with exit zero.
Its full TAP hash is `07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161`.
The [full correctness control](retained-main/build-full.json) overlaps targeted validation.
Its duration does not represent a performance comparison.
Normal full-corpus execution does not measure allocation balance.

The diagnostic limit remains 1,980 seconds, and the CI package budget remains 33 minutes.
Corpus, exclusions, proof rules, negative controls, benchmarks, and tolerances stay unchanged.
Complete exact-head CI remains the merge gate.

## Reproduction and records

The [collector](collect.py) retains the exact commands, order, cache setup, and fixed sample count.
Its paths identify the original frozen worktrees and copied executables.
The [validation script](retained-main/validate.py) and [rebuild control](retained-main/build-and-full.py) retain the final source checks.
Executables and proof caches remain outside this archive.

The [manifest](manifest.json) hashes the retained data files.
The README and manifest itself are outside that data hash list.
The [compression identities](compression-identities.json) preserve original raw bytes with lossless gzip.
Verify local files with `python3 verify.py`.
Verify committed blobs with `python3 verify.py --commit HEAD`.

The ignored originals remain under `build/interpreter-compiler-path-control-20261110-0812` and `build/interpreter-retained-main-validation-20261110-0820`.
These records establish no external rank.
The next optimization needs a separate matched comparison from actual main.
