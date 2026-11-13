# 2026-11-13 — a reuse allocation keeps what its token has

x86-64 and arm64. `ssatokenkeep.keeps`, run by `ssaprepare.with_selects`
after `ssa.split_tokens`. Part of #15610.

## The shape

`ins` of the red-black tree workloads matches a black node and tests that
the node is unique. It then branches on the key and on the color of a child.
Each path builds a node from the token of the matched node:

```
  b79:  v365 = call __fern_rc_is_unique v262
        brif v365 b450 b451
  b153: v380 = phi v262 v997          the token: the box, or null
        ...
  b53:  v404 = call __fern_alloc_reuse v380 v403
        st_at    v404 shape Node
        st_at 8  v404 Black
        st_at 16 v404 0
        st_at 24 v404 v286            the key that b85 loaded from v262
        st_at 32 v404 v287            the value, also from v262
        st_at 40 v404 v999            the right child of v262
```

`ssa.split_tokens` does not take this shape. The blocks between the test and the allocation do not test the token. A copy
for each side is a copy of most of the function.

On the unique side, the allocation returns the box. The box already has that
shape, key, value and right child. The six stores and the token test of the allocation ran for each level of
the tree. Four of the stores wrote what was there.

## What changed

`keeps` finds a reuse allocation whose token is such a phi. The block of the
allocation gets a null test of the token and three parts:

```
  b53:  brif (v380 == 0) b478 b479
  b478: v1156 = call __fern_alloc_reuse v380 6     marked: the token is null
        st_at    v1156 shape Node
        st_at 24 v1156 v286
        st_at 32 v1156 v287
        st_at 40 v1156 v999
  b479: v404 = phi v380 v1156
        st_at 8  v404 Black
        st_at 16 v404 0
```

A store moves to the side that allocates when it is redundant for the box:

- the store of the shape that the box has
- a store of a word that a load read from the same offset of the box before
  the arms of the uniqueness test meet, or the phi of such loads of the two
  arms

The pass applies these conditions:

- The size of the allocation is a constant, and the block stores a shape to
  the result.
- No store to the box can run before the uniqueness test with the same box,
  or after the arms meet.
- The result has one store for each offset in the block.

A block above the uniqueness test can prove the shape of the box. Such a
block compares the first word of the box with the shape address, and only
its true side leads to the test. When no block does, the token goes through one more
block, which compares its first word with the shape. A token of another shape
then takes the side that allocates, where the inline reuse allocation is
complete. `sink` of `TestSelfHostSSATokenKeep` has that form: its match tests
only that the value is not a leaf.

The side that allocates is next to the block it came from. An allocation for
a null token has the `COLD_BRANCH` kind, so it saves the caller registers
that hold live values.

## Measured

x86-64, Callgrind instructions, against the branch before this change. The
answers and the allocation censuses are equal on x86-64, and the answers on
arm64 under qemu.

| Workload | Size | before | after | |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 100,000 | 183,292,328 | 171,290,804 | -6.55% |
| rbtree-ck | 100,000 | 186,488,235 | 175,982,539 | -5.63% |
| deriv | 6 | 2,772,789 | 2,754,965 | -0.64% |
| nqueens | 8 | 1,165,486 | 1,165,486 | 0 |
| cfold | 12 | 2,194,615 | 2,182,468 | -0.55% |

Against main at `a509fb08`, before the two changes of this day, rbtree is
-11.28% and rbtree-ck is -9.63%.

## Traps

- A first form gave the shared side of the uniqueness test a fresh copy of
  the box, so the token was a box on each path. rbtree fell to 166,783,966.
  deriv rose 2.8% and made 13,900 allocations where it made 12,246: a path
  that releases its token paid for a copy that it then freed.

- The side that allocates was first at the end of the function. The
  allocator gives each value one interval in block order. Each value that
  the new block reads was thus live to the end. rbtree rose to 194,084,502, and
  `ins` kept its loop variable in a stack slot.

- The join has a phi of the token and the fresh box. The token has later
  uses on other paths, so the allocator cannot give the phi the register of
  the token. Each site pays one move and one jump over the new block.

## Not done

The planner pairs tokens with constructions and takes no account of the
fields.
`balance_left` builds `Node(Black, Node(Red, lx, kx, vx, rx), k, v, r)` from
two tokens. The inner node gets the token of the outer match. The outer node gets the
token that owns `lx`, `kx`, `vx` and `rx`. The opposite pairs need two
stores where these need ten.
