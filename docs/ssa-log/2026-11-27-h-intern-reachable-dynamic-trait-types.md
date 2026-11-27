# Intern reachable dynamic-trait types

Part of #11534 and #8171.

Semantic lowering discovers concrete implementations for each reachable dynamic-trait type. A new implementation can introduce fields that refer to another trait. The compiler repeats discovery until the environment rows stop growing.

Previously, each occurrence of a dynamic-trait type caused another scan of the implementations. The scope and implementations stay fixed across these rounds. Each function now has a table of dynamic-trait types in discovery order. An index in this table identifies one type. Each round starts at the previous table length. Only new entries need an implementation scan.

The table uses the existing type interner. Spelling selects a hash bucket. Structural equality checks the trait string. The field traversal and the order of concrete implementations are unchanged. Each generic instance starts its own discovery.

## Stage-2 measurements

The pinned stage 0 came from release `stage0-20261125-84426d4`. Each stage 1 built its own stage 2. Both stage-2 compilers built the same inputs from main at `d33a4a2b8f5c`. Callgrind counted x86-64 instructions with IR verification and ownership certification enabled.

| Input | Main instructions | Branch instructions | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 95,425,707,600 | 95,428,598,940 | +2,891,340 |
| 1 fields with an array of dynamic values | 58,734,826 | 58,636,994 | -97,832 |
| 32 fields with an array of dynamic values | 63,953,161 | 62,372,989 | -1,580,172 |
| 128 fields with an array of dynamic values | 83,022,776 | 76,855,967 | -6,166,809 |
| 1 fields with a dynamic value | 56,924,470 | 56,906,121 | -18,349 |
| 32 fields with a dynamic value | 59,411,984 | 59,393,644 | -18,340 |
| 128 fields with a dynamic value | 69,680,074 | 69,661,847 | -18,227 |
| Two concrete shapes | 40,673,706 | 40,661,390 | -12,316 |
| Scalar control | 17,789,397 | 17,789,528 | +131 |

Both compilers applied `-target x86-64-linux` to `compiler/checker.fern` with `internal/stdlib`. The full compile used 2,891,340 more instructions (0.0030%) and 3,948 more allocations. The checker binary matched byte for byte. The main compiler allocated 74,469,774 objects. The branch compiler allocated 74,473,722. Each freed every object.

The repeated-field programs store the same dynamic value or array in each field of a record. Two implementations return different results for the same input. The program checks both results. The 128-array compile allocated 189,032 objects on main and 178,165 on the branch. On x86-64, the stage-2 executable changed from 22,015,936 to 22,013,152 bytes.

## Verification

Both stage-2 compilers reproduced themselves. All 6,276 corpus outcomes matched, with 4,467 identical binaries and 1,809 matching refusals. All 529 expected-error JSON diagnostics matched. The fixed checker matched on six targets. The HTTP component and both freestanding assembly outputs also matched.

The 15 focused programs passed 44 paired executions on their supported targets. The three array fixtures passed nine more comparisons on x86-64 Linux, ARM64 Linux and WASI. Another 52 programs passed 156 paired executions with leak checks. They cover trait intersections in both orders, mixed implementations, imported trait aliases and implementations in different source modules. All allocations balanced. The compiler format, feature and complexity checks passed.

A test inspected the actual dynamic environment rows after semantic lowering. The first function started from `dyn Outer`. It discovered `Wrap` and `Plain` in that order. The two array fields in `Wrap` exposed `dyn Inner` on the next round. Both compilers then emitted `Leaf` followed by `Bud`, once each. A second function started from `dyn Inner` and emitted only its two implementations.

Both compilers matched all 2,372 per-function mutation records. Each refuted 5,373 planted imbalances, weakened 228 and missed 229. The native driver had 763 unproved obligations on both builds. The WASI checker had 74 on both. The base commit pins 733 and 71. The branch keeps these inherited pins.

The shared-array benchmark measured 58,518,488 bytes across 1,022,778 copies on main. The branch measured 58,521,008 bytes across 1,022,784 copies. Both compilers reproduced their respective counts on both checker inputs. The six additional copies account for 2,520 bytes during semantic construction. The branch pins its two measured counts.
