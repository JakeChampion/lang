# 2026-10-29 — the loop uniqueness check takes every array

`semunique` placed one uniqueness check before a loop only for a byte
array. Thus a loop that wrote an `i64[]` from an owned parameter kept a count
test at every `.with`. Now the check takes every array that is not a view.

## The change

- `semunique.wanted` accepts an owned array of any element type.
- Each array type has its own check, `__sem_unique` followed by the type key,
  because the SSA verifier compares each call with its contract's types.
  `ssasem.unique_check_name` makes the name and `ssasem.is_unique_check`
  recognises it.
- `ssarc.array_unique` copies a shared byte array with `__fern_bytes_owned`,
  as before. It copies any other array through `copied_base`, which retains
  the elements of a reference type.
- `ssaunits.unique_receivers` also marks a check whose receiver the plan
  proves sole, and `array_unique` then emits no test.

`semunique` runs before the interprocedural fresh rows. Thus it cannot see
that an array from `__alloc_i32` or from a fresh helper is sole. Without the
mark, the six proved cases of `TestSelfHostSoleLoopWithX86_64` each gained
one test.

## Measurement

`coreutils/fmt.fern`'s break chooser, `choose`, writes nine arrays that it
takes from its `own Words` parameter. x86-64, `-O`, instructions from
callgrind on 4 MB slices of the `scripts/coreutils-bench.d/fmt.sh` inputs:

| Workload | Before | After | GNU 9.12 |
|---|--:|--:|--:|
| `fmt` of the prose file | 615,001,233 | 584,087,307 | 524,794,391 |
| `fmt -w 40` of the prose file | 645,175,008 | 612,118,185 | 462,681,455 |
| `fmt -c -w 60` of the wrapped file | 691,770,703 | 657,881,244 | 578,761,503 |

The "After" column also includes the same branch's fix to the break
chooser's early exit. Without that fix, `-w 40` measures 607,461,010.

The emitted `choose` has 7 count-test sites, not 13, and 1129 lines of
assembly, not 1495.

## Tests

`TestSelfHostUniqueLoop` adds `scale`, an `i64[]` loop with two writes, and
`shout`, a `string[]` loop. Each has the one check before its loop and copies
a shared array with `__fern_arr_slice`. The program gives each a shared array
and a sole array. It runs under the leak check and the sanitizer.

## Next lead

The `fmt` gap to GNU is now mostly the register path. The inner loop of
`choose` keeps seven loop values in stack slots and bounds-checks every load.
Also, each `__scan_set_bytes` call in the reader pays about 25 instructions
on entry. A typical word has about four bytes.
