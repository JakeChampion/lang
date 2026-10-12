# 2026-10-12 — a block holds a token per donor

`ssaunits.reuse_pairs`, `verify_block_tokens`; `ssarc.block_body`.

## The shape

```fern
match (t) {
    Two(Two(a, b), c) => { return Two(a, Two(b, c)); },
    _ => { return t; }
}
```

A rotation takes two dying nodes apart and builds two. The parent `t`
dies first, and the next construction, the inner `Two(b, c)`, is not one
it can serve while its child is dying beside it. A block held one token at
a time, in the frame's one token slot, so the parent's token waited for a
construction and the child's was released, or the reverse. Every rotation
allocated once; rbtree's `balance` arms build three nodes from two dying
ones and allocated twice.

## The rule

A token taken at a drop now waits in its donor's own local, as a carried
token already does across blocks. The construction it is handed to moves
it into the token slot just before it builds. The slot is only ever filled
right before a construction, so any number of tokens can be held at once.
`reuse_pairs` keeps a list of held donors. A construction takes the first
one of its slot count that it does not read, else a box dying at the
construction itself, and each position takes at most one new donor to
hold.

The plan replay follows the same discipline:
- a construction spends a token it was handed, and no construction spends
  two;
- a token is taken once, and is spent within its block.

The planted fault "a token is taken while another is held" becomes "a
token is taken twice".

## Measured

`TestX86_64RotationReuse` (and arm64, wasm) rotates a three-node tree
there and back each round, census balanced:

| | before | this change |
|---|--:|--:|
| unique tree, 100 rounds | 201 | 2 |
| unique tree, 400 rounds | 801 | 2 |
| old tree kept, 400 rounds | 1,200 | 800 |

The kept tree is copied by `rot` alone, two nodes a round; `unrot` has
`rot`'s fresh result to itself.

The rbtree benchmark at 420,000 inserts, x86-64:

| | previous entry | this change |
|---|--:|--:|
| rbtree allocations | 4,270,564 | 3,850,589 |

`scripts/perf-bench`, x86-64: perceus_rbtree retires 1.5% fewer
instructions and perceus_rbtree_ck 1.4% fewer. Each grows 2.7% in code,
the moves from a donor's local into the token slot.

A stage 2 compiles the compiler with the plan replay on and the memo off,
and stage 3 is byte-identical to it. A sanitizer stage 1 compiles
`checker.fern` clean.
