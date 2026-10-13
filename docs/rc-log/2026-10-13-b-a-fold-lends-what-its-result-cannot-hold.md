# 2026-10-13 — a fold lends what its result cannot hold

`ownership`: `Escapes.quiet`, `quiet_slot`, `call_carried`, `slot_carried`;
`semsource.quiet_cycles` and `quiet_slots`; `ssasem.can_hold`. Refs #8171.

## The shape

Ownership inference is a greatest fixpoint. Every reference parameter starts
consumed and is demoted once a round finds no use that carries it out of the
frame. In a recursive walk the recursive call is such a use until the walk's
own parameter is demoted, so the cycle justifies itself. #10352 and #10941
broke that for a quiet cycle, where a call carries nothing. A cycle was quiet
only if every function on it returned no reference.

So a walk whose result holds a reference kept its input counted through its
own recursive call. The compiler's AST folds are the heavy case:
`fold_expr_nodes` instantiated at `string[]`, `util.Diag[]` or a record
accumulator retained every child node before recursing into it and released
it afterwards. The `Diag[]` instance's code held 24 retains and 23 release
calls. The `boolean` instance's, whose result holds no reference, held none.

## The rule

Quietness is now per parameter. A slot is quiet when the function's result
cannot hold a value of the parameter's type (`ssasem.can_hold`). A call
inside a cycle carries nothing at the callee's quiet slots. Every reference
slot of a function whose result holds no reference is quiet, which is the old
rule.

The reason to count a parameter across a recursion is to take from it: move
it, or a part of it, into what the walk returns and reuse its box. That is
`std/pvec`'s `__pv_with_in`, which rebuilds a tree into a tree. A result that
cannot hold the parameter's type cannot receive the parameter. A part of it,
a string field say, is still retained where it is stored.

Lending is always sound here. A slot quietness skips decides only the
caller's own mode, and the unit planner retains at every callee slot that
ends up counted.

`can_hold` walks the types reachable from the result: elements, tuple
members, map keys and values, record fields, and every variant's fields. A
union and its members name the same box, because a match binds a member to
the union's own box. A function value, a dyn value, or a type the schema does
not describe may hold anything.

## Measured

The whole compiler under callgrind, with both compilers built from the same
tree and compiling the same frozen copy of main, each on a warm memo.

| | Ir |
|---|--:|
| main at fef3701 | 69.956 G |
| quiet per parameter | 68.672 G (−1.84%) |

The AST folds fall 385 M, the generated drop and release helpers 316 M
(`__sem_release_ast__Expr` 161 M), the checker 158 M and the unit planner
95 M. `can_hold` and the per-slot rows cost about 35 M. The 535 M that
`semlower.inferred_rows` loses reappears in `semsource.with_inferred_modes`.
On main the inliner spliced that function into its one caller; it now stays
a separate function.
The compiler's binary is 100 KB smaller.

The bench corpus does not move: none of its programs walks recursively into a
result that holds references. `TestSelfHostOwnershipInference` gains
`names_of`, which folds a tree into a list of strings and must not drop the
tree. On main it drops it twice.
