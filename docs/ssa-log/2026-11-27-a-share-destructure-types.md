# Reuse the initializer type in destructuring declarations

Part of #11534 and #8171.

The checker gets the initializer type before it binds a tuple or record pattern.
The binder now uses that type for the whole pattern and for each field.
It resolves field names in the scope before the declaration.
It retains the qualified enum lookup, generic field substitution, and tuple index checks.

## Stage-2 measurements

Two compilers built the same sources from `84ea3a5782e7` for x86-64 Linux.
Each compiler used the pinned stage 0 and one self-compile.
Callgrind counted instructions with verification enabled.

| Fixed source | Main instructions | Branch instructions | Instructions saved |
| --- | ---: | ---: | ---: |
| `compiler/checker.fern` (full compile) | 94,852,435,607 | 94,831,737,564 | 20,698,043 |
| `compiler/checker.fern` (check only) | 15,057,150,205 | 15,036,071,205 | 21,079,000 |
| `destructure_tuple_and_struct_forms/main.fern` | 20,074,347 | 19,972,482 | 101,865 |

Both compilers produced the same native binary for the fixture.
The focused suite covered 24 programs on x86-64 Linux, ARM64 Linux, and WASI.
Both compilers produced the same bytes and execution results for all 72 comparisons.
For the WASI case with exit code 136, a wrapper checked the full return value.
All 12 error fixtures produced identical JSON diagnostics.

Each stage-2 compiler reproduced itself.
The full fixed checker compile used 74,301,801 heap allocations on main and 74,294,367 on the branch.
Both compilers freed every allocation.
The two reports from `scripts/selfhost-emit-hashes` matched on all 6,258 rows.
The compilers emitted 4,458 identical binaries and refused the same 1,800 inputs.

The six fixed checker targets produced identical binaries.
The HTTP component and both freestanding assembly outputs also matched.
The 526 error fixtures retained the same JSON diagnostics and exit codes.

## Performance baselines

Both compilers copied 58,274,576 bytes in 1,020,179 shared-array copies on the main checker source.
On the branch source both copied 58,278,272 bytes in 1,020,358 copies.

The certifier refuted 5,346 mutants on the branch and 5,347 on main.
It weakened 229 and 230 respectively.
Eight of 2,360 per-function records changed.
The generated code for `bind_stmt` had two fewer sites for the certifier to mutate.
Seven other functions changed the classification of one mutant each.
Both compilers missed 227 mutants.
