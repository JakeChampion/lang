# Reuse the first call fact when rewriting literal arguments

Part of #11534 under #8171. Follows #17496.

Literal inference records scalar tags and composite types in separate lists. Each fact names a source line, column, and argument number.
The checker finds the first applicable fact in each list before it rewrites a call.
It now passes those two offsets to the rewrite. A list with no applicable fact supplies its length.

Every earlier entry has different coordinates or an argument number outside the bounds of this call.
The first exact match stays the same when several entries describe one argument.
The composite list also accepts the receiver at index `-1`.

## Stage-2 measurements

Callgrind counted x86-64 instructions on fixed main source at `74fb73e2136d`.
Each compiler used the same input paths and arguments. The emitted programs matched byte for byte.

| Fixed input | Main instructions | Branch instructions | Difference |
| --- | ---: | ---: | ---: |
| Full checker compile | 97,177,041,596 | 97,177,041,596 | 0 |
| Check only | 15,084,999,432 | 15,084,999,432 | 0 |
| 1 generic call, 16 arguments | 22,461,563 | 22,461,966 | +403 |
| 8 generic calls, 16 arguments | 31,232,764 | 31,192,980 | -39,784 |
| 32 generic calls, 16 arguments | 63,478,546 | 62,729,586 | -748,960 |
| 32 fixed calls paired with 32 generic calls | 69,312,660 | 69,303,764 | -8,896 |
| 32 array receivers | 64,343,655 | 64,338,855 | -4,800 |
| 32 array receivers and 32 generic calls | 83,999,833 | 83,962,265 | -37,568 |
| 32 typed calls | 24,895,162 | 24,895,162 | 0 |

## Verification

Both stage-2 compilers reproduced themselves. The branch executable grew by 208 bytes.

Each compiler allocated and freed 74,929,958 objects on the full compile. Each allocated and freed 90,390 objects on the 32-call generic fixture.

The complete emit-hash reports matched for all 6,288 target and fixture pairs.
They contained 4,470 identical binaries and 1,818 refusals. Their SHA-256 was `007a0ab7c410f19f4ecfa372bf98eadbd279e47a09622f0870554c016119a71d`.
All 530 expected-error JSON diagnostics matched. Six checker targets, the HTTP component, and both freestanding assembly outputs matched.

A direct comparison checked 945 combinations of call positions, argument bounds, receiver indices, and duplicate facts on each target.
For every valid argument, the lookup from the saved offset returned the same first index as a search from zero.
The composite receiver also returned its original index.
The 51 call-focused comparisons, nine generic workload comparisons, and three imported-call comparisons passed across x86-64 Linux, ARM64 Linux, and WASI.
Three focused negative programs gave identical diagnostics.

All 115 certifier cases passed. All 2,379 per-function mutant records matched.
Both compilers counted 738 native driver and 71 WASI checker values as unproved on the fixed source pair.
Both refuted 5,387 planted imbalances, weakened 229, and missed 229.

Both stage-1 compilers copied 57,838,016 shared-array bytes in 1,006,122 copies on the fixed main source.
Both copied 57,840,448 bytes in 1,006,145 copies on the branch source. The branch pins its two measured rows.

## Integration with the package capability change

Main at `a5c8b593370b` includes #17518. Its package and function lookups return an optional index.
Both integrated compilers reproduced themselves. Both passed the package capability driver and all 17 printer checks on each of the three targets.

The six pairs of driver binaries matched. All 51 call-focused cases and the three imported-call cases also matched on these compilers.
The strict ownership and shared-array comparisons passed on both sides. All 2,379 mutation records matched.

The later loop test in #17521 added a second ARM64 addressing mode to its assembly check.
Both integrated compilers emitted the same assembly for the test on both native targets.
Seven loop functions had the expected element-read and bounds-check counts on each native target.
The program passed with the sanitizer and allocation checks on all three targets.

## Generic call workload

Set `n` to 1, 8, or 32. Each generic call takes the width of the local that receives its result.
The program returns zero when all calls produce the expected value.

```python
n = 32
parameters = ", ".join(f"a{i}: T" for i in range(16))
arguments = ", ".join(str(i + 1) for i in range(16))
print(f"@noinline function pick[T]({parameters}): T {{ return a15; }}")
print("function main(): i32 {")
print("  let sum: i64 = 0;")
for i in range(n):
    print(f"  let x{i}: i64 = pick({arguments});")
    print(f"  sum += x{i};")
print(f"  return (sum - {16*n}) as i32;")
print("}")
```
