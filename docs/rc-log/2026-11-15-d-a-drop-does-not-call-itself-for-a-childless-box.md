# A drop does not call itself for a childless box

2026-11-15. All targets. The change is in the drop of a field
(`ssarc.drop_children`). It continues
`2026-11-15-c-a-drop-makes-one-call-for-each-level.md`.

## The shape

The drop helper of `Expr` in cfold releases a field of its own type inline.
It tests the count, and for a node with one count it calls itself. The
called helper then reads the shape of the node. For `Val(i64)` and `Var(i64)`
it finds no field that holds a count, frees nothing, and returns. That is a
call, a full frame and a chain of compares for no work.

## The change

Before the call, the drop now reads the shape of the node. When the shape
is a variant whose fields hold no count, the drop does not call the helper
(`childless_shapes`). A variant with no field is a static box, which the
test of the count refuses, so it gets no test of its shape. At most two
shapes get a test, and an enum with a `drop` gets none, because its helper
calls the `drop`.

## A finalizer on each node

The tests found an older bug (#16128). The drop helper of a recursive enum
walks its last field of the same type in a loop. The helper calls the
`drop` of the enum at its entry, but the loop did not call it for the next
nodes. `self_tail_enum` is now false for an enum with a `drop`. Its helper
releases each field through the release helper, so each node gets its
`drop`.

## Measured

Cachegrind instructions, x86-64:

| Workload | Before | After |
|---|---:|---:|
| cfold 16 | 33,367,480 | 32,384,458 (-2.9%) |
| deriv 8 | 103,215,856 | 102,389,468 (-0.8%) |

rbtree, rbtree-ck and nqueens give the same binaries. Many of the `Val`
leaves of deriv are shared or static, so fewer drops reach the new test.

## Tests

`TestSelfHostDropSkipsAChildlessBox` checks that the drop of an enum tests
the shape of a unique child before it calls itself. It also runs an enum
with a `drop` and checks that each node prints from its `drop`, in order,
on each target. The census is balanced on x86-64, arm64 and wasm32.
