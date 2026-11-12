# 2026-11-12 — the token split reuses loads and sinks write-backs

x86-64 and arm64. `ssa.split_tokens`: `loads_above`, `same_load`,
`without_stored` and `write_backs`.

## The shape

After `split_tokens` gave each side of a uniqueness test its own block, two
things were left on the unique side of `ins` in the red-black tree workloads.

The release of a field of the reused box loaded the field again:

```
  b449: v988 = ld_at 8 rc v263
        v989 = call __fern_rc_dec v988
```

The match had loaded the same field above the test (`v285`).

After the join, the rebuild stored each scalar field that the match read:

```
  b111: v1004 = phi v263 v995
        v321 = st_at 24 v1004 v287      v287 = ld_at 24 v263
```

On the unique side that store writes a field of the box back to itself.

## What changed

**A load that holds above the test is not made again.** `loads_above`
collects the `ld_at` instructions of the block that ends in the uniqueness
test, and of the blocks above it that one block alone enters. A later `st_at`
to the same field of the same base removes its load. A copy that loads such
a field takes the earlier result.

The match read the earlier value, so `ssadeps.boxed_values` also removes the
box test of its release.

**A store that writes a field back goes to the shared side.** `write_backs`
looks at the stores that lead the block after the join. It takes each one
whose base is a phi that has the box on the unique side, and whose value is
a load of the same field of the box. It moves that store to the shared side,
with the base of that side. The unique side must hold no store to that field.

## Measured

x86-64 container, native, Callgrind `Ir`, against main at `8b5a5cf1`.

| Workload | Size | Before | After |
| --- | ---: | ---: | ---: |
| rbtree-ck | 100,000 | 216,163,197 | 211,334,169 (-2.2%) |
| rbtree | 100,000 | 215,086,140 | 215,262,370 (+0.1%) |

deriv, nqueens and cfold do not change. Each answer and each allocation
census is the same.

rbtree does not improve. The two steps remove about six instructions for
each level of the tree. The register allocator then keeps the color in a
register across the loop and puts a key in a stack slot.

A stage-2 compiler runs 2,555,948,883 instructions to compile
`coreutils/tr.fern`, down from 2,588,967,638 (-1.3%). Stage 2 and stage 3 of
the compiler are byte-identical.

## Gate

`TestSelfHostSSATokenSplitReusesLoads` reads the register form of `ins`. The
loads of the color field go from 22 to 17. The blocks that store the value
field with no allocation go from 13 to 11. The test holds both counts.

## Next lead

Eleven blocks of `ins` still write a field back. They are in the paths that
call `balance_left` and `balance_right`, where the store is not in the block
after the join of a token split.
