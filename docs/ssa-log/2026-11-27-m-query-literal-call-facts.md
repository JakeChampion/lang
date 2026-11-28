# Check each list of literal call facts once

Part of #11534 under #8171. Follows #17490.

Literal inference records a source line, column, and argument number with each scalar tag or composite type. A composite fact can also describe the receiver. Before the rewrite visits a call, it checks for an applicable fact. The previous check searched both lists for each argument. The new check visits each list at most once.

It accepts argument numbers from zero through the last actual argument. Only a composite fact can match the receiver at index `-1`. Calls at the same coordinates can have different argument counts. The bounds check excludes facts outside the current call. The subsequent rewrite still reads the first exact match for each argument.

## Stage-2 measurements

The pinned stage 0 built both stage-1 compilers. Each built its own stage 2. Callgrind counted x86-64 instructions for the same source with the same arguments. The base compiler used main at `54f4792c8af5`.

| Input | Main | Branch | Difference |
| --- | ---: | ---: | ---: |
| Full checker compile | 99,877,783,886 | 99,877,200,302 | -583,584 |
| Check only | 15,099,653,943 | 15,098,494,119 | -1,159,824 |
| 8 calls with 0 arguments | 25,854,286 | 25,853,506 | -780 |
| 8 calls with 1 argument | 25,997,199 | 25,996,347 | -852 |
| 8 calls with 8 arguments | 29,111,636 | 29,103,056 | -8,580 |
| 32 calls with 16 arguments | 69,354,723 | 69,149,223 | -205,500 |
| 32 typed calls with no inference | 24,852,260 | 24,852,260 | 0 |
| 32 array receivers | 64,128,758 | 64,126,814 | -1,944 |
| 32 array receivers and 32 generic calls | 83,832,659 | 83,837,563 | +4,904 |

Each call-count fixture pairs a fixed-arity function with a generic call on integer literals. The generic result takes its width from an `i64` local. The program checks the sum of the results. The receiver control appends to an array literal with a declared element type. The mixed control also uses generic calls.

| Heap allocations | Main | Branch | Difference |
| --- | ---: | ---: | ---: |
| Full checker compile | 74,708,022 | 74,708,022 | 0 |
| 32 calls with 16 arguments | 105,556 | 105,556 | 0 |
| 8 calls with 0 arguments | 57,860 | 57,860 | 0 |

Both compilers freed every allocation. The stage-2 executable changed from 22,096,560 to 22,096,688 bytes. Both compilers reproduced themselves.

## Verification

Both emit-hash reports had SHA-256 `87294ed007b1fe7db6ba574b3e3ec1943d363b66607d7980593e071a2c3fb432`. The reports matched for all 6,288 outcomes. They contained 4,470 identical binaries and 1,818 refusals. All 530 expected-error JSON results matched. The six fixed checker binaries, the HTTP component, and both bare-metal assembly outputs also matched.

A direct probe compared the call query with repeated exact lookups. It covered 756 combinations per build and target. The probe checked signed and zero source lines, duplicate facts, invalid indices, and calls with zero through eight arguments. It also changed the line or column independently. Both builds passed on x86-64 Linux, ARM64 Linux, and WASI.

The two compilers passed 51 call-related binary and execution comparisons across the three targets. All 14 expected messages from three negative programs matched in order. The range and scope fixtures passed another 51 execution comparisons and three negative comparisons. The checker driver matched 11 ownership and scope cases on each target.

Two imported modules used nested calls at the same source positions with different literal widths. Both builds passed the valid program and produced the same E038 diagnostic for its invalid variant on each target. The format check and both local lint checks passed.

## Ownership and array copies

Both compilers passed all 115 certifier cases and all five strict ownership rows. All 2,379 per-function mutation records matched.

Both stage-1 compilers copied 57,872,800 shared-array bytes in 1,006,541 copies on the main source. Both copied 57,872,160 bytes in 1,006,517 copies on the branch source. Each source passed its two strict benchmark rows.

## Reproduce the 16-argument fixture

This script makes the 16-argument input. Run it with Python 3 and save its output as a Fern file.

```python
count = 16
params = ", ".join(f"a{i}: i32" for i in range(count))
args = ", ".join(str(i + 1) for i in range(count))

print("@noinline function choose[T](a: T, b: T): T { return b; }")
print(f"@noinline function fixed({params}): i32 {{ return a0; }}")
print("function main(): i32 {")
print("  let total: i32 = 0;")
for i in range(32):
    print(f"  let x{i}: i64 = choose(1, 2);")
    print(f"  total += fixed({args}) + (x{i} as i32);")
print("  return total - 96;")
print("}")
```
