# Main integration after the rejected lookup trials

Status: [record] source qualification from 2026-11-10.

Main is `e23f590d1e523a2a90c7b202c7631b80aa4635da`.
The integrated retained source is `1fb070786cd3bf799b78873d9dbaa6f2c9500ed3`.
The compiler and standard library exactly match that main revision.
The branch retains two regression files for generic calls and same-count session replacement.
The rejected lazy lookup and argument prepass remain absent.

The integration retains the upstream string-view copy repair, revised HTTP path check, TLS repairs, task-handle guards, kernel networking, and video metadata changes.
The stage0 pin remains `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
Both fresh production builds use that qualified producer with debug variables, embedded source, profiles, and allocation counters off.
Ownership certification remains on, with the proof cache disabled.

## Correctness controls

Pinned source check, read-only format check, and Go vet exit zero.
Twelve targeted suites report 413 test and subtest passes, with zero skips.
The package completes in 287.669 seconds.
The suites include x86-64, ARM64 under QEMU, WebAssembly, generic calls, session snapshots, and the upstream function-count guard.
They retain the nested-owned-field positive and negative controls.

Both TLS interoperability selectors also exit zero, with 14 test and subtest passes and zero skips.
Their package completes in 218.397 seconds.
The [validation record](validation/validation.json) and raw logs retain every command and outcome.

The fresh main and retained production executables are byte-identical.
Their SHA-256 is `f5f15374a3e622ba97ac3809619570d62891b80ae031ff505369de77204a29ec`.
The [build records](validation/build-full.json) retain their distinct source roots and complete flags.

The rebuilt normal compiler completes all 1,896 reference records with exit zero.
Its full TAP hash is `07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161`.
This correctness control overlaps targeted validation and establishes no timing comparison.
Normal full-corpus execution does not measure allocation balance.
The diagnostic limit remains 1,980 seconds, and the CI package budget remains 33 minutes.

## Baseline conflict resolution

Three baseline files require conflict resolution.
Every numerical metric and tolerance follows incoming main `e23f590d`.
The [resolution record](integration/resolution.json) preserves both measurement histories.
Earlier diagnostic-size notes explicitly describe their archived values.
The incoming HTTP reductions retain their original upstream evidence.

The archive preserves both baseline inputs, all three index stages, marked conflicts, and the final resolution.
The [source audit](source-audit.json) verifies the product-source equality and baseline rows.
These inherited baseline changes establish no performance gain from this PR.

## CI and limits

The previous head `aeb3d564` has a cancelled CI verdict in run `42783012696`.
Its 15 checks comprise three successes, one conditional skip, two neutral results, and nine cancellations.
No actual failure appears in that run.
The [check records](integration/checks-aeb.json) and [job records](integration/jobs-aeb.json) retain every page.
A cancelled verdict is not a passing merge gate.

The earlier performance archives remain unchanged and retain their original source identities.
Their timing losses, work counts, and rejection decisions do not describe the new compiler image.
This integration adds no compiler optimization.
Complete exact-head CI remains the merge gate.

The [manifest](manifest.json) hashes the retained data files.
The README and manifest itself are outside that list.
The [compression identities](compression-identities.json) preserve raw bytes through lossless gzip where required.
Executables and caches remain in the ignored original build directory.

Verify local files with `python3 integration/verify.py`.
Verify committed blobs with `python3 integration/verify.py --commit HEAD`.
