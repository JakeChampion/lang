# Reuse settled types when rebuilding literal-inference scopes

Part of #11534 and #8171.

Literal inference rebuilds a scope with the current group widths. Value blocks, open receivers and floating-point expressions read that scope. It used to rebuild every tuple and array in that scope, including types with no pending literals. A local that was itself a pending literal also required two reads of its group's width.

The checker now keeps a settled scope entry. It reads the width once for each pending local. If the group has no width yet, the checker uses the scope’s polymorphic integer default and records the local name. Tuples and arrays with pending elements still go through the recursive resolver. The final rewrite supplies a concrete `i32` default for its type tags and parameter types.

## Stage-2 measurements

The pinned stage 0 compiled each source tree. Each stage 1 then built its own stage 2. Callgrind counted x86-64 instructions on fixed inputs from `33417d514116` with IR verification and ownership certification enabled.

| Fixed source | Main instructions | Branch instructions | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 95,048,235,942 | 95,048,810,304 | +574,362 |
| Checker, check-only | 15,020,268,034 | 15,020,129,699 | -138,335 |
| 0 settled tuple locals | 29,086,398 | 29,077,867 | -8,531 |
| 32 settled tuple locals | 54,539,290 | 54,222,576 | -316,714 |
| 128 settled tuple locals | 83,876,082 | 82,635,683 | -1,240,399 |
| Mixed-width control | 57,620,300 | 57,621,756 | +1,456 |
| 128 integer-local groups | 164,066,412 | 164,066,038 | -374 |

For the scope measurement, the fixture declares 0, 32 or 128 locals of type `(i32[], i32)`. It then binds a tuple of numeric literals, reads its components as `u64` and `f32`, and reads the floating value in eight conditional expressions. Both branches execute. All three variants returned the expected result on x86-64 Linux, ARM64 Linux and WASI.

The fixed checker compile produced the same binary. It allocated 74,456,552 objects on main and 74,456,337 on the branch. The 128-tuple fixture allocated 189,041 on main and 179,822 on the branch. Both compilers freed all allocations. The stage-2 compiler grew by 576 bytes.

A separate trial stored numeric type IDs in a table for whole inference groups. On a function with 128 integer-local groups it added 105,308 stage-2 compile instructions. This change keeps the existing group representation. The parser node-ID and general type-interning work remains open.

## Output and diagnostics

Both stage-2 compilers reproduced themselves. The conformance reports matched across 6,258 cases and targets, with 4,458 identical binaries and 1,800 matching refusals. All 526 expected-error JSON diagnostics, six fixed checker binaries, a WASI HTTP component and both freestanding assembly outputs matched.

All 34 focused programs passed on three targets, and 13 focused refusals matched. Seven additional programs from the literal and value-block end-to-end tests produced identical binaries and expected results on all three targets. These included integer multiplication above 32 bits and an integer local used in a floating-point branch. The format check and both lint ratchets passed.

All 2,360 per-function mutant records matched. Both builds refuted 5,347 planted imbalances, weakened 228 and missed 227. The WASI checker had 71 unproved functions on each build. The native driver had 733 on main and 733 on the branch.

Both compilers measured 58,471,768 shared-array bytes across 1,021,764 copies on the fixed main input. On the branch input, both measured 58,469,044 bytes across 1,021,720 copies. The branch pins the two measurements for its checker source.
