# 2026-11-12 — a cons-tail loop carries the index of its hole

All targets. `ssasem.tail_recursion` (`site_holes`, `Chain`, `fill`,
`hole_indexes`) and `ssarc.hole_site`.

## The shape

`ssasem.tail_recursion` makes a loop of a tail call modulo cons. Each round
builds a box with an open hole and stores it in the hole of the round before.
The store was to one payload index for the whole function. The pass thus
required that every construction put its recursive result at the same index
(`agreed_hole`). A function that did not agree kept all its calls.

A tree insert does not agree:

```
if (k < kx) { return Node(Red, ins(l, k, v), kx, vx, r); }
if (k > kx) { return Node(Red, l, kx, vx, ins(r, k, v)); }
```

`ins` in `bench/perceus/rbtree.fern` and in `rbtree_ck.fern` has four such
constructions, two at payload 1 and two at payload 4. All four stayed calls.

## What changed

Each block that ends in a cons tail keeps its own hole index (`site_holes`).
When a function uses one index, the graph and the code are the same as
before. When it uses more than one:

- The loop header has one more phi, the index of the open hole. A cons-tail
  block gives it the constant index of its site.
- `hole_fill` has a fourth operand, that phi, and lists the possible indexes
  in `str` (`hole_indexes`).
- `ssarc.hole_site` compares the operand with each listed index but the last
  and stores to the payload that matches.

`ssasem.hole_error` checks the type of the fourth operand and that each listed
index holds the type of the result.

The calls that are not in a cons tail stay calls. In `ins` these are the
calls whose result goes to `balance_left` or `balance_right`.

## Measured

x86-64 container, native, Callgrind `Ir` of `bench/perceus/fern/run_*.fern`.
The base is the same tree without this change.

| Workload | Size | Before | After |
| --- | ---: | ---: | ---: |
| rbtree | 100,000 | 271,064,146 | 257,448,370 (-5.0%) |
| rbtree-ck | 100,000 | 268,995,237 | 255,418,337 (-5.0%) |
| deriv | 7 | 17,010,722 | 17,010,722 |
| nqueens | 10 | 28,212,142 | 28,212,142 |
| cfold | 14 | 10,660,407 | 10,660,407 |

The allocation census does not change: 100,005 allocations and frees at
100,000 inserts for each tree, with zero live bytes.

Wall time at 4,200,000 inserts did not separate from the noise of the
container. The minimums of seven interleaved runs:

| Workload | Before | After |
| --- | ---: | ---: |
| rbtree | 1424 ms | 1585 ms |
| rbtree-ck | 1546 ms | 1349 ms |

Stage 2 and stage 3 of the compiler are byte-identical.

## Gate

`put` in `TestSelfHostSemanticTrmc` inserts into a tree 300,000 nodes deep.
The path to the key goes right at each even depth and left at each odd one.
The assembly must hold no call from `put` to itself. The answer must agree on
x86-64, x86-64 with the sanitizer, arm64 and wasm. The caller holds the tree
of one insert. That insert must copy the path and must not change the tree.

## Next lead

`ins` still runs about 100 instructions for each level of the tree. The
profile shows three costs:

- The reference-count traffic on `Color`, which has no payloads.
- The three-part guard before each count on a value that a match already read.
- The repeated tests of one reuse token.
