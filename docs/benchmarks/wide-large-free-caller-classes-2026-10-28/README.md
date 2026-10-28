# Current classes for large-free callers

Issue #13750 adds standalone classification and partition adapters for the current large-free helper.
The old caller models admit 4 GiB allocations but classify them with the old table through 1 GiB.
The current helper uses class 52 at 4 GiB.
The old models and their assertions remain unchanged.
Retained-driver integration and machine call summaries remain separate work.

The adapters use all 61 current capacities through 16 GiB.
They compare word counts with capacity quotients and avoid signed byte multiplication.
Uniform small string allocations retain sentinel minus three.
Uniform oversized allocations retain sentinel minus two.
Invalid or mixed intervals return sentinel minus one.

The new grids refine each old row at current class boundaries.
The array grid contains 70 rows instead of 54.
Each string grid contains 103 rows instead of 87.
All old boundaries, residue ranges, and input domains remain unchanged.
The fused grid extends through 2305843009213693948 words.
The separate grid retains its final 1152921504606846976 singleton with residues one through seven.

An independent Python integer reference defines 1566 cases.
The reference uses byte arithmetic and set-based partition boundaries.
Each class record includes the old result as explicit legacy provenance.
A framed worker and strict collector require exact integer records and nonzero balanced zero-live censuses.
Private controls restore the old ceiling, omit a partition, and swap the fused offset.
The collector must reject all three controls.

The fresh retained-machine failure remains open under #13717.
Full recovery, native milestones, M5, and epic acceptance remain open.

## Final qualification

The final campaigns use source `7872a0970739ed6cfc100b48750f1bfb5716a4bb`.
The clean standard compiler build passed make, version, and smoke checks in 84.982 seconds with pin 919.
The raw compiler matches the prior qualified production compiler byte for byte.
The CLI records its actual source version.

All three targets completed the same 1566 cases with exact integer results.
All 588 fresh workers exited successfully with nonzero balanced censuses and zero live bytes.
Every result matches exactly across native, Wasm, and ARM64.
All 3849 campaign source hashes and all compiler and worker binary hashes remained unchanged.
The tool snapshots precede the final campaigns and confirm unchanged bytes after all checks.

Final stock driver and packet checks passed 29 nodes, and unchanged complexity checks passed three nodes, with zero skips.
The source gate passed all five runtime sets, fixture checks, compiler modules, standard library modules, and all 84 standalone drivers.
The nonverbose source-gate command does not establish a separate test-node count.
Format covered 2109 inputs.
Build, vet, workflow lint, test names, CI self-tests, and all 219 source-audit nodes passed.
A fresh Go oracle from the qualified source passed check and interpreter commands.

The fixed reference reproduced exactly, and all eight collector self-tests passed.
The three private faults compiled and ran normally with nonzero balanced zero-live censuses.
The strict collector rejected the old ceiling at 4 GiB, the missing first partition, and the incorrect fused offset.
The real collector preserved 40 completed records, its checkpoint, and the failed batch output.
A second attempt rejected existing output without changes.

The first independent grid construction added an incorrect cut at 256.
Its assertion stopped before reference output or Fern execution.
The correction preserves the original interval from one through 256.
The generator also reproduces the canonical source format without a numeric change.
An initial formatter command lacked the secondary cache exports and failed before format work.
A later build setup command found an existing pin directory and stopped before the build.

The archive preserves those setup failures, original scripts, corrections, provisional results, and the published policy.
The local policy draft received a formula clarification before publication.
The fresh stock retained-machine command still fails with exit 166.
The source-pinned diagnostic confirms the admitted 4 GiB counterexample and the distinct fused and separate grid endpoints.
Its 1310 allocations balanced with zero live bytes.
The early-exit stock census alone does not establish a persistent compiler leak.

`evidence.json` binds the archive hashes and qualification facts.
The compressed target records preserve their exact original bytes.
Executables and unchanged private source copies remain at their original paths with identities in `omitted-file-identities.json`.
All old models, interfaces, domains, assertions, production instructions, baselines, and thresholds remain unchanged.

The raw production compiler is unchanged, so this scope does not repeat the separate image corpus from PR #13734.
Retained-driver integration and machine call summaries require later work after this merge.
Full recovery, native milestones, actual CI recovery, M5, and epic acceptance remain open.
