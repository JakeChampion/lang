# Use parser declaration IDs for range inference

Part of #11534 and #8171.

The parser gives each source `StmtFor` a positive ID. It uses the same module counter as ordinary `let` declarations. The parser lowers C-style loops directly to `while`. Each copy of the loop keeps that ID. The literal checker records its inferred width under the module, loop ID and name. The rewrite can look up this fact before it reads either bound.

A generated loop has ID zero. Some later passes erase a copied loop's source line. For either case the checker keeps the loop line, lower bound coordinates and name.

## Stage-2 measurements

The pinned stage 0 built each compiler from main `07b36bc0a302` or the branch. Each stage 1 then built its own stage 2. Callgrind counted x86-64 instructions on fixed main input with the same compiler arguments.

| Input | Main | Branch | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 99,909,911,287 | 99,910,172,990 | +261,703 |
| Check only | 15,101,154,075 | 15,101,358,108 | +204,033 |
| No loops | 18,364,763 | 18,364,762 | -1 |
| 1 loop | 19,914,630 | 19,914,554 | -76 |
| 32 loops | 56,880,692 | 56,877,470 | -3,222 |
| 128 loops | 203,933,905 | 203,922,284 | -11,621 |

The range fixture uses the same loop name in successive inclusive ranges. Each loop passes its index to a function with an `i64` parameter. The program checks the sum at runtime.

The full checker compile used 0.00026% more instructions. Each compiler allocated 74,711,284 times on the fixed checker workload and 254,856 times on the 128-loop fixture. Both builds freed every allocation. The stage-2 executables were 22,094,624 and 22,096,512 bytes.

## Verification

Both stage-2 compilers reproduced themselves. All 6,288 conformance outcomes matched. They include 4,470 identical binaries and 1,818 refusals. The two reports have SHA-256 `d06ac236c74023c0bfd3e9b4e0a688e754e749f5179c1a0cb1f9beda36efc461`. All 530 expected-error JSON diagnostics matched. The six checker binaries, the HTTP component and the two bare-metal assembly outputs matched.

All 34 parser cases passed on each of x86-64 Linux, ARM64 Linux and WASI. The parser test checks nine distinct declaration IDs across two functions, an interpolation and nested loops. It also checks that a new parse starts at one.

The paired compilers passed 51 execution comparisons and three negative cases. One WASI fixture checks its return value inside the program because the value exceeds the runner's exit-status limit. Three further execution comparisons used two imported generic functions, each with two instantiations. The direct table probe covered module identity, renamed loops, duplicate entries, generated ranges and copies with no source line.

The checker drivers gave the same diagnostics for all four range-bound ownership cases and seven literal-scope cases on each target. The formatter and both local lint ratchets passed.

## Integration with main

Main `c8d0d319427e` includes the zero-fill optimization and the native driver baseline of 738. The pinned stage 0 built both integrated compilers.

The stage-1 certifier refuted 5,387 planted ownership errors on main and 5,388 on the branch. Each compiler weakened 228 cases and missed 229. Both counted 738 unproved sites in the native driver and 71 in the WASI checker. The new `lit_parsed_site` function accounts for the extra refutation. One case each in `lit_range_site` and `parse_for_stmt` changed classifications in opposite directions.

On the same main source, each stage-1 compiler copied 57,873,316 bytes in 1,006,418 shared-array copies. On the branch source each copied 57,902,380 bytes in 1,006,490 copies. All five certifier rows and both array-copy rows passed the strict comparison on each build.

The integrated compilers produced identical output on all nine checker, HTTP and bare-metal targets. All 530 expected-error diagnostics matched again. The branch passed all 34 parser cases on each of the three execution targets. Both builds passed the zero-fill test and its bounds-error control on each target.

The four benchmarks with size differences against main's pins are `draw_scaled`, `jpeg_decode`, `hex_encode` and `hex_decode`. Both compilers produced the same native assembly, WAT and binary outputs for all four. All twelve measured assembly or WAT sizes matched the hosted report. Native binaries and WASI core modules returned the expected checksums on all three targets.
