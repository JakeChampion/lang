# 2026-10-12 — a closure call reads its body unchecked

`ssarc.function_site`, `ssarc.map_in_place` and `ssarc.drop_captures`.
Refs #8171.

## The shape

A call through a function value loads the body's address from slot 0 of the
closure box, then calls through it. The load was an ordinary `arr_get`, so
every indirect call compared index 0 against the box's length and branched to
`__fern_oob_abort` first:

```
cmp    (%r12),%rdi
jae    __fern_oob_abort
mov    0x8(%r12,%rdi,8),%r8
call   *%r8
```

Every closure box has slot 0. `closure_new` builds the body's address there,
and a box that captures nothing is a static block with the same layout. The
only function value without a box is the zero the defer desugar starts a
lifted slot at, and nothing reads that before the assignment that replaces it.
So the check could never fire. It still cost a load and a branch on every
indirect call, and the compiler's AST folds make two indirect calls per node.

## What changed

`body_address` reads slot 0 with `arr_get_nc`, the unchecked read every
backend already has. The closure call, the mapped callback, and the release
path that matches a box's body against the environments its type may carry
all read through it.

## Measured

The whole compiler under callgrind, both compilers built from the same tree and
compiling the same frozen copy of it, each on a warm memo of its own.

| | Ir |
|---|--:|
| main at 89e48a4 | 69.956 G |
| unchecked body read | 69.764 G (−0.28%) |

Nearly all of it is in `astwalk`, whose folds make two indirect calls per
node: the module's self time falls by 170 M, 40 M of it in the boolean
instance of `fold_expr_nodes`.

The bench corpus on x86-64:

| | before | after | |
|---|--:|--:|--:|
| `closure_call.ir` | 103.91 M | 94.31 M | −9.24% |
| `map_int.ir` | 42.07 M | 41.32 M | −1.78% |
| `map_probe_chain.ir` | 390.90 M | 385.17 M | −1.46% |
| `map_string.ir` | 65.96 M | 65.32 M | −0.98% |

No row grew. `TestSelfHostOptimisationShapes/closure_call_unchecked` pins the
missing check on x86-64 and arm64.
