# 2026-10-18 — a phi cycle is not a reader

`seminline.drop_unread`, `seminline.needed_values` (#12820).

## The shape

A record with a counted field, built in a function and updated in the inner
of two loops, cost one allocation per update:

```
let q: Q = Q { buf: mk(), n: 0 };
while (r < 10) { let i: i32 = 0; while (i < 10) { q = Q { ...q, n: q.n + 1 }; i = i + 1; } r = r + 1; }
```

`split_constructions` takes every field read of `q` off the construction, so
each field is carried through phis of its own and the update becomes a
`record_new` of those phis that names its base only in `imm`. What is left of
the box is two phis, `outer = phi(init, inner)` at the outer header and
`inner = phi(outer, update)` at the inner one. Each is read by the other, so
`drop_unread`, which dropped a value only when its read count was zero, kept
both and the update they carry. In one loop the box phi is read by nothing
and goes. The physical lowering then released the old box in one block and
built the new one in the next, with no reuse token between them, so every
update allocated.

## The rule

`drop_unread` marks what is needed instead of counting reads: an operand of
an instruction it would not drop, a returned value or a branch condition, and
whatever a needed droppable instruction reads in turn. A construction, copy or
phi of a built type that is not needed is dropped, so a cycle of phis that
read only each other and the update goes in one sweep. A record update's base
in `imm` is still not a read here, as before.

## Measured

Self-host x86-64, `__heap_alloc_count()`, origin/main against this change:

| shape | before | after |
|---|---|---|
| the issue's `local_nested` (100 updates, record unread after) | 100 | 0 |
| the same, a field read after the loops | 100 | 0 |
| the same with a record of scalars only | 100 | 0 |
| `bench/record_update` with `put` a free function, whole run | 1,638,409 | 8 |
| `bench/perceus_rbtree_ck`, whole run | 123,360 | 41,120 |

`perceus_rbtree_ck` is the same cycle on a variant: `make_tree_aux`, spliced
into `main`'s loop, threads its `Trees` accumulator through a phi that reads
itself and the `Cons` built from it, and `count_head` only reads the head
tree, so the list was built and never read. Its counts: x86_64 `.ir`
65,400,121 to 55,908,847 (-14.5%) and `.text` 2,906 to 2,878; aarch64 `.text`
3,178 to 3,145. Every other `bench` row on x86-64 is unchanged.

## Witnessed

`TestSelfHostSemanticSplit` gains `nested_rounds` (the record read after the
loops) and `nested_unread_rounds` (nothing reads it), on arm64, x86-64, x86-64
under the sanitizer and wasm: each allocates only its string, 1, where the
old pass made 101. `TestSelfHostSemanticSplitOff` pins the same probes with
the pass off, at 2 each (the string and one box the updates reuse in place).
