# 2026-09-30 — an unannotated array-of-arrays binding keeps its rows (#10497)

AST lowering (`irlower.fern`: `arrarr_from_init_shape`,
`call_ret_arrarr_type`, `vb_tail_arrarr_fresh`).

The issue read `let d = { let q = [[1, 2], [3, 4]]; q }` as a value block
whose binding lost the tail's `is_arrarr`. That was only half of it. A block
that captures nothing is lambda-lifted to `__lam_N()` before lowering, so the
binding is a call result, and `arrarr_from_init_shape` had no call arm:
`let d = mk()` with `mk(): i32[][]` leaked the same way. Only a capturing
block is inlined and reaches the `vbl` carry the issue named.

## Measured (x86-64, AST lowering, allocs / frees)

| shape | before | after |
|---|---|---|
| lifted block, `i32[][]` | 3 / 1 | 3 / 3 |
| lifted block, `f64[][]`, `string[][]` | 3 / 1 | 3 / 3 |
| `let d = mk()` | 3 / 1 | 3 / 3 |
| inlined (capturing) block | 3 / 1 | 3 / 3 |
| `vblock_arrarr` row, five rounds | 70 / 25 | 70 / 70 |

The typed lowering balanced all of them already. `vblock_arrarr` runs on both
lowerings and three targets; all are sanitizer-clean on x86-64.

## Still leaking

A plain `T[][]` local rebound inside an `if` (`q = [["x"]]`) keeps the
literal credit off, with or without a value block: 3 / 2.
