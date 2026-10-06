# 2026-10-02 — the lift indexes a function's declarations once

`lift.VarDecls`, `lift.binding_param_is_fn`. Refs #8171. No emitted byte
changes: the stage0-built compiler before and after emits the fixed older
tree (`compiler/fern.fern` at 1ae9cad, against that tree's
stdlib) and that tree's `checker.fern` byte for byte, and the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing,
against the previous entry's tree.

## What the profile named

`lift.binding_param_is_fn` asks whether a call's callee is a binding of the
enclosing function whose parameter is fn-typed, once per call argument the
module table does not answer. It found the binding by folding the whole
body for `var` declarations of that one name: 103 k folds over 5.7 M
statements, 1.45 G, for a question the function's declarations answer the
same way every time.

## What changed

`lift_inline_closures` builds `VarDecls` once per function, every `var` of
the body in spine order with a name index over the bound names, and the
inline-closure pass carries it beside the declaration it already carried
(`ilc_expr_at`, `ilc_stmt_at`, `lift_inline_closures_expr`,
`lift_iife_body`, `lift_iife_arm_values`, `try_fn_field_value`,
`box_iife_arm_array_elems`, `box_iife_arm_values`). Where the pass scopes
the declaration to an iife (`iife_scope_fd`), `iife_scope_vars` extends the
index with the iife's declarations in the same order. `binding_param_is_fn`
reads the first declaration of the name off the index, which is the first
the fold met.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 150.71 G | 149.38 G (−0.9%) |
| `lift.binding_param_is_fn`, inclusive | 1.46 G | 0.05 G |
| `lift.lift_inline_closures`, inclusive | 3.08 G | 1.75 G |

## Witnessed

The checker, planner, semantic, closure, method and lift set (the three
name lists of 2026-10-02-q, -r and -s together; 621 tests, green),
`make check-sources`, the lint ratchet, both emit identities and the
sweep.

## Next

`var_param_is_fn` still folds the body through `fd_bound_names` when a
binding's initialiser is another name, which is rare. The entries before
this one list the rest: `type_from_spelling`, `semrecords.verify`,
`find_union`, and `hash_bucket`'s emitted loop.
