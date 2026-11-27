# Use local declaration IDs for literal inference

Part of #11534 and #8171.

The parser numbers ordinary `let` declarations from one in each module. It gives an interpolation the outer counter and returns the updated counter with the expression. A speculative parse returns its own cursor. Discarded cursors cannot advance the caller's counter.

The literal checker records the module, declaration ID and name of a parsed local. Its rewrite uses the same site to find the inferred type. Specialization and other copies retain the ID. A fresh generated local keeps ID zero and uses its source coordinates and name. A range keeps the loop line, lower bound coordinates and variable name. The table compares every field and returns the first exact match.

## Stage-2 measurements

The pinned stage 0 built both compilers from main `fd7a30075fb0` and the branch. Each stage 1 then built its own stage 2. Callgrind counted x86-64 instructions on identical source inputs. The two compiler commands used identical arguments. A separate build with allocation counters compiled the same inputs.

| Input | Main | Branch | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 96,762,966,555 | 96,752,715,015 | -10,251,540 |
| Checker check only | 15,102,390,882 | 15,088,354,798 | -14,036,084 |
| 8 distinct names | 53,728,010 | 53,715,776 | -12,234 |
| 64 distinct names | 71,056,687 | 70,995,873 | -60,814 |
| 8 shadowed locals | 56,004,643 | 55,992,512 | -12,131 |
| 64 shadowed locals | 97,288,359 | 97,227,148 | -61,211 |
| 256 shadowed locals | 285,274,124 | 284,988,977 | -285,147 |

The width fixture declares local number `i` with the integer literal `i + 1`. Each local supplies one argument to a function with an `i64` parameter. The shadow fixture uses the name `same` in each of its conditional blocks. Both fixtures check the sum at runtime.

The fixed checker took 74,684,663 allocations on main and 74,676,207 on the branch. Both compilers freed every allocation. The executable for stage 2 shrank by 3,536 bytes.

Two controls used the same name index for literal sites. The control with source coordinates took 15,095,236,216 instructions to check the fixed checker. The control with declaration IDs took 15,096,149,557. The counter with shared parser tables took 15,090,196,446 instructions. The branch uses numeric hash buckets and compares every field of a site. The control runs used the original CLI paths.

## Verification

Both stage-2 compilers reproduced themselves. All 6,288 conformance outcomes matched. They include 4,470 identical binaries and 1,818 refusals. Both emit-hash reports have SHA-256 `539323c3ad4f5d4d06977166099559a0a3e9fc0217b62058cfdc9532c717105a`. All 530 JSON error diagnostics matched. The six fixed checker binaries, the HTTP component and the assembly for both bare-metal targets matched.

The parser suite passed all 34 cases on x86-64 Linux, ARM64 Linux and WASI. The direct index probe covered collisions, duplicate sites, negative coordinates, renames and repeated IDs in different modules. The first exact site won each lookup.

The focused programs passed 69 binary and execution comparisons. Nine covered repeated names in separate blocks. Three covered two instantiations in each of two imported generic functions. Four negative fixtures gave the same JSON diagnostics. Both lint ratchets passed.

Main `41267db5643d` added a standard library change and new performance baselines. Its compiler sources match the fixed base. The eight ML-KEM tests passed on all three targets. Both stage-1 compilers produced identical output for each target.

The shared-array benchmark uses stage 1. Both compilers copied 57,717,424 bytes in 1,006,177 copies on the fixed main source. On the branch source, both copied 57,879,240 bytes in 1,006,396 copies. The branch pins the values for its source.

The separate stage-2 check counted 49,759,512 bytes across 1,013,417 copies on main source. It counted 49,888,808 bytes across 1,013,607 copies on branch source. Both compilers gave the same counts on each input.

The certifier refuted 5,373 planted imbalances on main and 5,387 on the branch. The six new drop and release functions account for twelve additional refutations. The two site functions account for one additional refutation each. Both compilers weakened 228 and missed 229. The WASI checker left 74 obligations unproved on both builds. The native driver left 775 unproved on main and 775 on the branch.

## Integration with the certifier change

Main `6daa0f1b7330` updated the certifier for reuse tokens that meet in a phi. Each integrated compiler reached its stage-2 fixed point. The certifier left 737 native driver obligations and 71 WASI checker obligations unproved on main. It left 737 and 71 on the branch. The mutation counts were 5,373 refuted, 228 weakened and 229 missed on main. They were 5,387, 228 and 229 on the branch.

Both builds passed all 115 cases in the new certifier test driver. Both compilers produced the same binary on the fixed driver source. Each build also gave the same findings and unproved values on its own driver source. The held native benchmark rows are tracked in #17472.

All five ownership rows and both shared-array rows passed the strict comparison on each build. The integrated branch matched all seven pins exactly. The integrated compilers gave identical results for all 530 JSON diagnostics and all nine output targets.
