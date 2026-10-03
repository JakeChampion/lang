# 2026-10-02 — the annotation reads an operand's type off its own walk

`checker.annotate_typed`, `binary_result_type`, `unary_result_type`,
`index_result_type`, `field_access_result_type`. Refs #8171. No emitted
byte changes: the `selfhost-emit-hashes` sweep is 1,965 rows per
compiler with 0 differing against a compiler built from main at
1745ab36. (The `checker.fern` binaries differ, as they must: the change
is in `checker.fern`.)

## What the profile named

`annotate_expr` was 2.15 G of the 31.82 G stage-2 compile of
`checker.fern`, and 717 M of it was `check_expr` called at every node
it stamped: a binary node checked its left operand, its right operand
and then itself, and checking itself checked both operands again, as
did every node above it. A subtree was checked once per enclosing node.

## What changed

`annotate_typed` stamps an expression bottom up and hands back its
checked type (`Typed`, with `known` for the arms that settle one): an
identifier's from `check_ident_expr`, a call's from `check_expr` as
before, and a binary, unary, field read or index from its operands'
types through the tail of the checker that typed it. Those tails are
the checkers' own bodies split at the point they have the operand
types (`binary_result_type` and its siblings); `check_binary_expr`
and the others call them after checking their operands, so the two
paths type a node from one body. An arm the walk does not settle (an
array, tuple, struct literal, lambda, slice or literal) is read by its
parent with `check_expr`, as every node was.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 1745ab36 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.82 G | 31.64 G (−0.57%) |
| stage 2, `annotate_module` inclusive Ir | 1.42 G | 1.22 G |
| stage 2, `check_expr` inclusive Ir | 2.68 G | 1.72 G |

## Witnessed

`TestSelfHostChecker*`, `TestSelfHostAnnotate*`, `TestSelfHostGeneric*`,
`TestSelfHostLiteral*`, `TestSelfHostSemanticSourceRC`, the lint
ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

A call is still checked whole at its node after its arguments were
annotated (`check_call_expr` 1.11 G under `check_expr`), and the
checker's own pass checks each expression again from the statement
(`stmts_call_diags` 1.32 G, `check_module_pass` 3.62 G). An array,
tuple or struct literal could settle its type as the operands do.
