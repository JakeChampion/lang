# 2026-10-12 — an operator chain is typed once

`checker.call_diags_binary`, `ir.const_propagate`, `ir.drop_dead_stores`.
Refs #11534, #8171. No emitted byte changes.

## The shape

On the whole compiler's self-compile, `checker.stmts_call_diags` is 5.47 G
Ir inclusive, 7.5% of the compile. By comparison, `check_func_body`, the
typing of the bodies themselves, is 0.58 G. The diagnostic walks type the
expressions they visit again and again.
`call_diags_binary` walked both operands and then typed them with
`check_expr`, which types each operand's whole subtree. It did this up to
six times per node, across the operator checks, `binop_lit_range_diags` and
`tvar_operator_diags`. A left-associated chain `a + b + c + ...` of n terms
therefore cost O(n²) in typing, and most string building in this compiler is
such chains. The edge `call_diags_binary -> check_expr` was 557 M.

## What changed

- **`binary_diags_typed` walks an operator and also returns its type.** The
  type is computed from the operands' types through `binary_result_type`,
  which is `check_binary_expr` over types already checked, so it is the type
  `check_expr` gives. An operand that is itself an operator is walked the
  same way, so a chain types each leaf once. The two helpers take the operand
  types, and the diagnostics and their order are unchanged.
- **`const_propagate` and `drop_dead_stores` copy only from their first
  change.** Both rebuilt the op list on every call, with an append and a
  release per op. `cleanup` runs them to a fixpoint for every function: 17 M
  `Op` releases.

## Measured

The whole compiler under callgrind. Each pair of compilers compiled the same
frozen copy of the tree, each on its own warm memo.

| change | before | after |
|---|--:|--:|
| operator chains typed once | 73.005 G | 72.555 G (−0.62%) |
| cleanup copies only on change | 72.409 G | 72.294 G (−0.16%) |

## Measured and not kept

- **Scanning each body once for settle, pretype and the type-argument
  retag.** Each asked its question once to learn whether any body needed the
  rewrite, then again per body. Folding the two into one hit list measured
  −0.02% on the whole compile, so it was not kept.
- **An early exit from `ssaunits.held_elements` when no read is anchored to
  a source.** It skipped 440 of 1,943 liveness computations on
  `checker.fern`, about 3 M Ir. Yet that compile measured 93 M more,
  reproducibly. `held_elements` is spliced into `units_of`, and changing its
  size moved what else fit under `seminline`'s budget there. A change to the
  compiler's own sources also changes the code the compiler is built into.
  Measure the total, not the function you changed.
