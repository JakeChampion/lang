# 2026-10-12 — a function value lends what its type lends

`ownership.indirect_carried`. Refs #8171, #11303. Changes emitted code.

## The shape

A call through a function value consumes its argument only at a slot its
type spells `own`. Every other reference argument is lent: the caller keeps
its unit and the callee's code does not release it. Two places already read
the call that way:
- `typeinfo.TypeFunc.param_own`;
- the unit planner (`ssaunits.call_value_supplied`).

The ownership inference did not. `indirect_carried` took every reference
operand as consumed, so a parameter handed to a function value was inferred
COUNTED. Each caller then retained the argument, and the callee released it.

`astwalk.fold_expr_nodes` is the shape that costs most. It hands every node to
`visit_expr` and `descend`, both function values that lend it. So `e` was
counted, and so was the loop variable the self tail call becomes:
- each recursive call retained the child it walked into;
- each step of the loop released the node it left.

Every walk over every body paid a retain and a release per node.

## What changed

`indirect_carried` reads the callee's function type. It carries an operand only
at an `own` slot, and lends the callee box and every other reference.

`visit_depth` in `TestSelfHostOwnershipInference` walks a tree and hands each
node to a function value. It must not call `__sem_release_Tree`, and before
this change it did, twice. `hand_to` gives its tree to an `own` slot, and the
program checks the tree it gets back.

## Measured

The whole compiler under callgrind. Both compilers were built from the same
tree and compiled the same frozen copy of it, each on a warm memo of its own.

| | Ir |
|---|--:|
| before | 69.723 G |
| a function value lends | 69.339 G (−0.55%) |

`astwalk` −245 M (`fold_expr_nodes[boolean]` −127 M, `fold_stmt_nodes[boolean]`
−44 M, `fold_expr_nodes[i32]` −40 M), `__sem_release_ast__Expr` −86 M,
`__sem_release_ast__Stmt` −19 M. The x86-64 bench corpus stays within its
tolerances.

## Still counted

A walk whose accumulator is a reference, such as `fold_expr_nodes[string[]]`,
still releases each node. Its function value lends `e` now. But the walk
returns a reference, so it is on no quiet cycle (#10941). The greatest fixpoint
keeps the recursive call's slot counted, and that slot carries the child,
which carries `e`. That rule is there for a walk that rebuilds its input;
these walks only read theirs.
