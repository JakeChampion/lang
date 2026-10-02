# 2026-10-02 — the fold decides readability once per position

`ir.fold_const_binaries`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 539e4e6.

## What the profile named

The previous entry left `const_i32_readable` at 194.8 M self Ir on the
stage-2 compile of `checker.fern` because six arms of the folder still
asked it about the same `cur[i]` before any of them folded, and two arms
asked `const_i64_readable` the same way: a constant that folds nothing
paid the digit scan once per arm per round.

## What changed

The loop body decides both verdicts once at the top of each position,
`r32` and `r64`, and every arm reads them. The second operand's check in
the two binary arms stays where it was, since it runs only behind a true
first verdict. Nothing else in the pass moved.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 539e4e6 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 39.79 G | 39.59 G (−0.49%) |
| stage 2, `fold_const_binaries` self Ir | 526.3 M | 491.8 M |
| stage 2, `const_i32_readable` self Ir | 197.1 M | 57.9 M |
| stage 2, `const_i64_readable` self Ir | 43.1 M | 22.3 M |

The three rows account for the whole of the total's 194.6 M.

## Witnessed

`TestSelfHostIRStrengthPeephole` (the fold goldens), `TestSelfHostIRLICM`,
`TestSelfHostTypedFoldValues` / `Shape` / `TypedLICM`,
`TestSelfHostIRLower*`, `TestSelfHostSemanticSourceRC`, the lint ratchet,
and the emit-hash sweep.

## Next

On the same profile, self cost: `__fern_alloc` 1.12 G, `ssa_lift.lift_impl`
1.09 G, `__fern_str_eq` 1.02 G, `util.hash_bucket` 963 M, `__fern_arr_push`
816 M, `__fern_str_concat` 758 M, `x86_native.x86_str_indexof` 727 M,
`__fern_arr_dec` 691 M.

`hash_bucket` is 618 M under `NameIndex.chain`, reached 1.22 M times through
`NameIndex.find` (1.14 M of those through `has`: `ssarc.append_helpers`
136 k, `checker.type_from_ref_names` 340 k, `names_resource_handle` 450 k),
699 k through `Scope.lookup_struct` and 362 k through
`semrecords.find_struct`; `ownership.find` hashes 352 k times on its own.
`str_eq` has 996 callers; the largest are `Scope.lookup` 1.65 M compares,
`semtypes.named_args_equal` 1.84 M, the x86 assembler's `x86_gas_unary_ext`,
`x86_gas_mov_size`, `x86_gas_lea_size` and `x86_gas_rep_pfx` 4.8 M between
them (mnemonic text compared per instruction), `semrecords.find_union` 1.26 M
(a linear scan of the enums by name, where `find_struct` walks a chain), and
`checker.check_binary_expr` 864 k. `semtypes.is_env` is called 2.02 M times
from `semsource.env_rows`. The call counts, not the hash or the compare, are
the cut.
