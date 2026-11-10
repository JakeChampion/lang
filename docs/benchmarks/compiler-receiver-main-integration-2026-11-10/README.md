# Receiver test integration and entropy capacity control

Status: [record], 2026-11-10.

Main `bcd223bae6ab2d8dc14b3b152027fa7be90e6779` adds checked owned results and retains the PTX output repair.
Integration `63d33384bb4261faa38f4436b4abf4d52a9b9542` preserves every main change.
Final test source `74c84520a2925bf40089b640a728813f46fca3c7` also repairs the stale entropy-capacity fixture.
The compiler and standard library match main exactly at both revisions.

The [summary](summary.json) records the source scopes, controls, and claim.
The [manifest](manifest.json) records 93 data hashes.
The [source map](source-files.json) records 89 original file snapshots.
The [compression identities](compression-identities.json) record 61 lossless gzip identities.
The earlier [rejected experiment](../compiler-receiver-union-index-2026-11-10/README.md) remains unchanged.

## Fresh integration controls

Main and retained normal compilers match byte for byte.
Their SHA-256 is `76f026a1ffd7f056d6e862d1dd4f514d4077abc5c9a99c692ebafc7444cf3b9c`.
Both builds use the qualified A19 producer, without debug data, embedded sources, profiles, or allocation counters.
Certification remains enabled, and the proof memo remains disabled.

All 23 targeted suites pass, with 248 test and subtest results, 227 leaf results, and zero skips.
The package completes in 577.820 seconds.
These suites include the receiver contract, both scheduler fixtures, and the new owned-result positive and negative controls.
They execute relevant programs on x86-64, QEMU ARM64, and WASI.

The Go parser and checker report 2,867 test and subtest passes with zero skips.
The selected formatter control adds one pass.
The grammar package has no selected test in that filtered command.
Pinned source checks, read-only format checks, Go vet, and complete CI self-tests pass.

The normal retained compiler checks its own source without errors.
Current native Opus compilation and execution and the complete normal interpreter produce the same reference TAP output.
The full interpreter retains its original 1980-second diagnostic limit.
These controls overlap other correctness tests.
They establish no paired timing result, speed factor, or normal-run allocation balance.

## Entropy fixture repair

Historical main CI run `42850278294` fails the negative-length capacity expectation on both native targets.
Its complete decoded logs and exact UTF-8 hashes remain intact.
The other historical E009 failures retain their existing owners.
Claim `6776197395` on issue #15118 covers only this entropy fixture.

Current main reproduces exactly the two negative-length leaf failures.
The runtime correctly exits 134 with `fern: allocation size out of range`.
The stale fixture expects a zero-word allocation instead.
All 16 nonnegative leaf controls pass before the repair.

The repaired fixture requires refusal before allocator entry.
Its allocator hook still checks exact packed capacity for every original valid count.
The negative cases cover `-1` and the minimum i32 value on x86-64 and QEMU ARM64.
Entry into the allocator hook cannot satisfy the expected refusal.
No runtime code, allocation rule, assertion budget, or diagnostic changes.

The repaired capacity test and related entropy-read, string-capacity, and large-fill controls report 58 test and subtest passes.
There are zero skips or failures.
The package completes in 7.464 seconds.
Go vet and format checks also pass.

The source audit separates the unchanged receiver and kernel tests from the additional entropy test repair.
The raw old failure, complete repaired controls, source patch, scripts, compiler identities, and main integration records remain intact.
The exporter also captures its initially empty output streams before it writes its final local summary.
No raw data normalization or proof relaxation occurs.

## Verification

Run the committed-data verifier from the repository:

```sh
python3 docs/benchmarks/compiler-receiver-main-integration-2026-11-10/verify.py --commit HEAD
```

The verifier checks the selected archive and the exact historical source identities.
It does not transfer a performance result to later main revisions.
The retained compiler executables and caches remain local, with recorded hashes.

The union-index optimization remains rejected and absent.
These integration and fixture controls add no compiler speedup or external rank claim.
