# Use literal inference groups to hold agreed types

Part of #11534 and #8171. Follows #17485.

A declaration site can occur in more than one literal inference group. Its index returns the first group for that site. The checker now keeps the agreed type name at that group index. It removes a copy of the declaration keys and two arrays that mapped between groups and unique sites.

Anonymous groups and repeated sites leave their own slots empty. A later occurrence with a different width clears the first slot. A third occurrence cannot restore it. The checker emits the other facts in their original order. It still distinguishes an explicit `i32` from a first group with an unknown width.

## Stage-2 measurements

The pinned stage 0 built each stage 1. Each stage 1 then built its own stage 2. Callgrind counted x86-64 instructions on the same input with the same arguments. The compiler sources are from main `b17a2c46c36b` and the branch.

| Input | Main | Branch | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 99,919,007,169 | 99,918,637,726 | -369,443 |
| Check only | 15,101,759,325 | 15,101,043,045 | -716,280 |
| 8 shadowed bindings | 56,006,249 | 56,004,737 | -1,512 |
| 64 shadowed bindings | 97,477,390 | 97,472,484 | -4,906 |
| 256 shadowed bindings | 286,047,440 | 286,033,018 | -14,422 |
| 8 distinct bindings | 53,716,311 | 53,714,804 | -1,507 |
| 64 distinct bindings | 70,995,104 | 70,990,539 | -4,565 |
| 64 tuple elements | 21,458,804 | 21,460,755 | +1,951 |
| 128 typed bindings | 27,831,242 | 27,831,242 | 0 |

Each shadowed local occurs in a separate conditional block. Its integer literal passes to an `i64` parameter. The program checks the sum of the values. The tuple control uses one local with 64 integer elements. The all-typed control declares each local as `i32`.

| Heap allocations | Main | Branch |
| --- | ---: | ---: |
| Full checker compile | 74,730,746 | 74,727,551 |
| 256 shadowed bindings | 399,588 | 399,569 |
| 64 tuple elements | 49,278 | 49,283 |

Each build freed all allocations. The stage-2 executable changed from 22,097,648 to 22,096,560 bytes. Both compilers reproduced themselves.

## Verification

The two conformance reports matched on all 6,288 outcomes. They contain 4,470 identical binaries and 1,818 identical refusals. Both reports have SHA-256 `87294ed007b1fe7db6ba574b3e3ec1943d363b66607d7980593e071a2c3fb432`. All 530 expected-error JSON diagnostics matched. The six checker binaries, HTTP component and both bare-metal assembly outputs matched.

A direct probe compared the exact fact order on main and the branch. It covered different and repeated widths, unknown and explicit `i32` in both orders, anonymous sites and union parents. Both builds passed the probe on x86-64 Linux, ARM64 Linux and WASI.

The two compilers passed 51 focused binary and execution comparisons, three negative cases and three imported generic comparisons. One WASI fixture checks its return value in the program because the value exceeds the runner's exit-status limit. The four additional programs passed on all three targets. The checker drivers matched eleven ownership and literal-scope cases per target. Both local lint ratchets and the formatter passed.

## Ownership and integration

The source trees under test also match main `6b134401efde` and the integrated PR. All five ownership rows and both shared-array rows passed the strict comparison on each build.

The checker refuted 5,388 planted imbalances on main and 5,387 on the branch. It weakened 228 on main and 229 on the branch. Both missed 229. One case in `settle_lit_body` moved from refuted to weakened. The other 2,378 per-function records matched.

Both compilers measured 738 unproved native driver sites and 71 unproved WASI checker sites. All 115 certifier tests passed on both builds.

Each stage-1 compiler copied 57,907,716 shared-array bytes in 1,006,866 copies on the main source. Each copied 57,872,800 bytes in 1,006,541 copies on the branch source. The branch pins its own source.

The integrated compiler produced the same binaries as main for `draw_scaled`, `jpeg_decode`, `hex_encode` and `hex_decode` on three targets. The native executables and WASI core modules returned the same checksums. Both native diff3 binaries matched GNU diff3 for the four new option cases and the conflicting-mode control.
