# Share the checked pattern type with semantic lowering

Part of #11534 and #8171.

Semantic lowering needs two forms of the initializer type for a tuple or record declaration.
The checker binds aliases and pattern names with the raw type in the original scope.
The lowerer resolves and settles that type for the current template and runtime representation.
The lowerer now uses one checker result for both operations.

## Stage-2 measurements

Each compiler used the pinned stage 0 and one self-compile.
Callgrind counted x86-64 instructions with verification enabled on fixed sources from `d4560a34c11f`.

| Fixed source | Main instructions | Branch instructions | Instructions saved |
| --- | ---: | ---: | ---: |
| Full checker compile | 94,968,014,811 | 94,964,428,492 | 3,586,319 |
| 32 record and 32 tuple declarations | 36,945,776 | 36,817,189 | 128,587 |
| Generic record and tuple | 42,131,920 | 42,124,105 | 7,815 |

The full checker compile produced the same binary.
It used 74,370,772 heap allocations on main and 74,368,664 on the branch.
Both compilers freed all allocations and reproduced their stage-2 binaries.
The generic fixture binds a record alias and then a tuple inside a function instantiated with integers and strings.
The larger fixture binds a record from a call and then a tuple in each of 32 steps.
A control with 32 scalar locals used 42 more instructions on the branch and produced the same binary.

## Verification

The two `scripts/selfhost-emit-hashes` reports matched on all 6,258 rows.
Both compilers emitted 4,458 identical binaries and refused the same 1,800 inputs.
All 526 error fixtures retained their JSON diagnostics.
Six fixed checker binaries, the HTTP component and both freestanding assembly outputs matched.

All 42 focused programs ran on x86-64 Linux, ARM64 Linux and WASI with identical outputs.
All 36 field and parameter comparisons passed.
A separate generic record test bound two callbacks with an owned parameter and called them in sequence.
Both compilers gave the expected result on each target.

Both compilers gave the same five certifier counts on their own sources.
They refuted 5,347 planted imbalances, weakened 228 and missed 227.
Each compiler copied 58,371,508 shared-array bytes in 1,020,515 copies on both inputs.
