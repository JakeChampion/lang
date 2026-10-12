# 2026-10-12 — a body ending in an exhaustive match splices

`seminline.spliceable`, `seminline.splice`; `ssaunits.reuse_pairs`.

## The shape

```fern
function balance_left(l: Tree, k: i32, v: boolean, r: Tree): Tree {
  match (l) {
    Node(_, Node(Red(), lx, kx, vx, rx), ky, vy, ry) => { return Node(Red, ...); },
    ...
    Leaf => { return Leaf; }
  }
}
```

Every arm returns, so the block after the match is unreachable, and the
graph ends it with an `unreachable` terminator. `spliceable` admitted only
return and branch terminators, so a function ending in an exhaustive match
was never spliced, whatever its size and however few its callers.

rbtree's `ins` takes a black node apart and calls `balance_left` with its
fields. The node dies in `ins`, before the call, and a reuse token cannot
cross a call, so the rotation `balance_left` builds allocated while the
node's box was freed. Every insert that rebalanced allocated.

## The rule

`spliceable` admits an `unreachable` terminator, and `splice` copies it as
it stands. It has no successors to renumber, and the unreachable block is
as unreachable in the caller. `balance_left` and `balance_right` are each
called once and are 57 instructions, so they splice into `ins`, and the
rotations build in the boxes that die there.

## A carried token beside held ones

The Koka benchmark's checkpointed tree writes `ins` with the `Leaf` arm
first. Its node is then dropped above the rebalancing arms and its token
carried into them, while the rotation's own dying boxes die there and are
held. `reuse_pairs` still cleared the held donors while a carry waited for
its construction, a rule from when one token slot served the block. A
carried token now waits in its donor's local like any held one, so the
rule had nothing left to protect, and each rotation built only in the
carried token and allocated twice: rbtree_ck went from 10,870 allocations
for 2,000 inserts to 12,854 once its balance functions spliced. The rule
is gone, and the same run allocates 2,000.

## Measured

`TestX86_64SpliceExhaustive` (and arm64, wasm) inserts n keys into the
benchmark's red-black tree, census balanced:

| | main | this change |
|---|--:|--:|
| 1,000 inserts | 4,938 | 1,000 |
| 4,000 inserts | 23,734 | 4,000 |

The rbtree benchmark at 420,000 inserts, x86-64:

| | main | this change |
|---|--:|--:|
| rbtree allocations | 3,850,589 | 420,006 |
| rbtree-ck benchmark allocations | 222,080 | 123,360 |

rbtree_ck's ephemeral run is one allocation per insert, and the run that
keeps every tree is unchanged at 30,834 for 2,000 inserts.

An insert now allocates its leaf and nothing else. The compiler compiles
itself in the same time, stage 3 is byte-identical to stage 2, the
binary is 3 KB larger, and a sanitizer stage 1 compiles `checker.fern`
clean.
