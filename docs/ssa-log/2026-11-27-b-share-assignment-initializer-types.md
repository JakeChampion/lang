# Share the initializer type in assignment diagnostics

Part of #11534 and #8171.

The assignment diagnostic pass checks each inferred initializer in the scope before its declaration.
The map and pattern diagnostics use that type.
The same type binds the tuple, record, or inferred local for later statements.
Nested lambda diagnostics finish before the new names enter scope.
Declared locals and recursive functions retain their binding rules.

## Stage-2 measurements

Each compiler used the pinned stage 0 and one self-compile.
Callgrind counted x86-64 instructions on the same input from `398cf588778d` with verification enabled.

| Fixed source | Main instructions | Branch instructions | Instructions saved |
| --- | ---: | ---: | ---: |
| Full checker compile | 95,003,146,035 | 94,962,238,825 | 40,907,210 |
| Checker with `-check` | 15,013,814,300 | 14,973,034,039 | 40,780,261 |
| Tuple and record fixture | 19,976,565 | 19,962,475 | 14,090 |

The full compile produced the same checker binary.
It used 74,391,368 heap allocations on main and 74,367,473 on the branch.
Both compilers freed every allocation.
Each stage-2 compiler reproduced itself.

## Output and diagnostics

The two reports from `scripts/selfhost-emit-hashes` matched on all 6,258 rows.
The compilers emitted 4,458 identical binaries and refused the same 1,800 inputs.
They produced identical binaries for six checker targets.
The HTTP component and both freestanding assembly outputs also matched.

All 42 focused programs passed on x86-64 Linux, ARM64 Linux, and WASI.
The 126 output pairs matched byte for byte.
All 526 error fixtures retained their JSON diagnostics.
A separate program tested an unresolved map, a type error inside a lambda, and an unknown record field.
Both compilers reported E020, E003, and E043 in that order.

## Performance baselines

On fixed main source the branch copied 58,369,216 shared-array bytes in 1,020,471 copies.
Main copied 58,369,872 bytes in 1,020,485 copies.
An earlier draft held the old and new scopes across the lambda walk and copied 59,362,800 bytes.
On the branch source the final compiler copied 58,371,508 bytes in 1,020,515 copies.
The main compiler copied 58,372,164 bytes in 1,020,529 copies on that input.

The certifier refuted 5,347 planted imbalances on the branch and 5,346 on main.
One imbalance in `stmts_assign_diags_mode` moved from weakened to refuted.
The other 2,359 per-function records matched.
Both builds missed 227 imbalances.
