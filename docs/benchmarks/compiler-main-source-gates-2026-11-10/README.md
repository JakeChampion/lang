# Main source gate repairs

Status: [record] qualification from 2026-11-10.

Main `4fc43d6a119435babcbd46acbd21600a592c8349` contains two source integration failures.
The capability inventory omits `std/user_net` and the MMIO reach of `std/user_process`.
Both modules intentionally reach `mmio` and `privileged` through their kernel network imports.
The two corrected entries pass all 375 platform test and subtest results, with zero skips.

The counted-loop conversion also changes two source hashes in the frozen wide-class catalog.
The affected files are `compiler/ssacapacityvalue.fern` and `compiler/ssalargepushranges.fern`.
The complete catalog differs only in these two provenance hashes.
Every case, packet identity, partition grid, expected result, and generated driver byte remains identical.
The original reference remains in [reference-before.json.gz](reference-before.json.gz).
The qualified reference is [reference-current.json.gz](reference-current.json.gz).

The frozen qualification source is `93942727d19bcad77d7aef70da15b36c58056bf8`.
Its compiler and standard library match main `4fc43d6a` exactly.
Each target executes the complete unchanged catalog with sanitizers and allocation counters.

| Target | Exact records | Fresh workers |
| --- | ---: | ---: |
| x86-64 Linux | 1,566 | 196 |
| ARM64 Linux under QEMU | 1,566 | 196 |
| WASI under Wasmtime | 1,566 | 196 |

All 588 workers exit zero with nonzero allocations, balanced frees, and zero live bytes.
Each target retains its complete source inventory and compiler and worker identities.
The [worker manifest](worker-manifest.json) identifies every raw packet and output file in the three compressed archives.
The collector also requires complete records, exact integer syntax, unchanged binaries, and unchanged sources.
The eight protocol controls and the complete CI self-test command pass.
No proof rule, assertion, case, domain, or tolerance changes.

The first attempt used the compiler without an embedded standard library.
It stopped at compilation before any worker executed.
The [failure records](initial-compiler-failure/result.json) remain intact.
The retry builds the standard embedded CLI and uses the unchanged qualification collector.
The build and qualification commands, source identities, and tool identities remain in this archive.

The source hash update follows the complete qualification.
It preserves the incoming counted loops and the earlier exact reference records.
The [decision record](rebank-decision.json) contains both reference hashes and the exact scope.
The [source patch](source-loop-change.patch.gz) preserves the two incoming loop conversions.
These are correctness controls, with no timing comparison or performance claim.

Run the committed-data verifier from the repository root:

```sh
python3 docs/benchmarks/compiler-main-source-gates-2026-11-10/verify.py --commit 2907b52ae3c553fb4dd55f20a484c0e8756eb800
```

For reproduction, use a clean checkout of the qualified source above.
Build `compiler/fern.fern` with its pinned stage0 and explicit standard library directory.
Use `scripts/fern-embed-stage` and that compiler to build `compiler/fern_main.fern` with the embedded assets.
The exact commands are in [build.json](build.json).
Invoke `scripts/qualify_large_free_wide_classes.py` once for each target, with the current reference from this archive.
The [qualification record](result.json) retains every original command and its exit status.

Main later contains both source gate repairs through independent owner merges.
Main `e31c27c971145607f0a4071795b601d985b82b32` uses a gzip container with one different header byte.
Its decompressed reference exactly matches this archive.
The live reference retains main's container.
The [integration record](../compiler-main-source-gates-integration-2026-11-10/README.md) preserves that container and the exact comparison.
The verifier above checks the original publication commit without a relaxed comparison.
