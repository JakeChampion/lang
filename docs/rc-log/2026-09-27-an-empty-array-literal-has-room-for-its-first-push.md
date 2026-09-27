# 2026-09-27 — an empty array literal has room for its first push

## How it was found

A stage-2 compiler built with `-g` compiled `coreutils/tsort` under gdb, with
a breakpoint on each of `__fern_arr_push`'s grow branches:

| branch | hits |
|---|---|
| grow from capacity 0 | 792,257 |
| double a full buffer | 299,078 |
| shared-receiver copy (the cliff) | 85,018 |

Two thirds of the push growths were an array that `[]` had just allocated
empty, reallocated on its first push. `[]` lowered to a capacity-0 box, so
every array built from an empty literal cost two allocations, the first one
thrown away. Native does the same: `[]` then one append is two allocations.

## The change

`ssa_lift.lift_empty_arr` (x86-64 and arm64) and `emit_wasm_arr_make` give an
empty literal capacity 4, the size `__fern_arr_push` grows a capacity-0 box
to, and store its length as 0. It is one allocation, and the first push
lands in place.

## Measured

A stage-2 compiler built with and without the change:

| workload | before | after | change |
|---|---|---|---|
| compiling `tsort`, instructions | 3,372,728,739 | 3,281,743,051 | −2.7% |
| compiling the compiler, wall | 127.6 s | 122.6 s | −3.9% |
| compiling the compiler, peak RSS | 4.58 GB | 4.68 GB | +2.2% |
| compiling the compiler, heap bumped | 4.77 GB | 4.97 GB | +4.1% |

The memory rise is the trade. An array that stays empty now holds 32 bytes of
capacity it never uses.

A static immortal empty box, rc −1 in `.data`, would avoid both costs: the
first push finds it full and grows into a fresh box, and `arr_dec` and
`arr_push_owned` already skip rc −1. It was not taken because the lowering
treats `array_new` as a fresh heap block it may free directly by size class,
and freeing a `.data` block corrupts the heap. That makes it a separate change
across every free path.

`empty_push(10)` in `TestSelfHostSemanticSourceRC` pins ten allocations for
ten arrays, 10045, on all four legs. Without the change it reads 20045.
