# 2026-10-12 — a decided branch goes where it would

`semthread` (new), run before `semsink` and `semdead`.

## The shape

```fern
match (t) {
  Node(Red(), l, kx, r) => { ... },
  Node(Black(), l, kx, r) => { ... },
  Leaf => { ... }
}
```

The parser desugars a match whose arms nest sub-patterns to a done-flag
chain (#7524): each arm tests `!done`, then the scrutinee's variant, then
the payload's. Two things in that chain are decided before they run.

- The flag. Every arm sets it and then returns, so wherever it is read it is
  the constant `false`. Nothing folded the branch on it: rbtree's `ins` kept
  the flag in a stack slot it stored and tested on every call.
- The second arm's tests. Arm one fails on a black node at its colour test,
  after `t` has passed its `Node` test, and the failure joins the path where
  `t` was a `Leaf`. Arm two tests `Node` again and reads all five fields
  again, then tests `Black`, which the failed `Red` test already decided.

## The rule

A branch on a constant becomes a jump. A branch on a variant test becomes
one when the tests that dominate its block decide it: a test of the same
variant on the same side, a different variant on the true side, or every
other variant of the enum on the false side. An edge into an empty block
that jumps, or into a block that only tests a variant, goes on to the side
its own path's tests decide, so a join that loses the facts of its
predecessors does not stop them deciding the test after it. An edge moves
only into a block without phis.

A tested value is known by the value it was read out of and the reads that
reached it, so the colour arm two reads is the colour arm one tested. The
fields are not merged: each arm keeps its own reads in the block where its
box dies, which is where a classified donor wants them.

A function with no branch on a constant, and no two variant tests reading
one value or parts of one, is left after one scan.

## Measured

x86-64 instructions, outputs identical:

| entry | before | this change |
|---|--:|--:|
| perceus_rbtree | 74.38M | 60.27M (−19%) |
| perceus_rbtree_ck | 82.34M | 70.78M (−14%) |
| perceus_deriv | 95.83M | 92.50M (−3.5%) |
| perceus_cfold | 67.14M | 65.87M (−1.9%) |
| perceus_nqueens | 105.33M | 105.33M |

rbtree's binary is 10,600 bytes against 12,904. `kind` in
`TestSelfHostDecidedBranchShapeX86_64` tests `Node` once and `Red` once and
no longer tests `Black` or `Leaf`; its code is 33 lines of assembly
against 89.

Compiling `checker.fern`, a sanitizer stage 1 allocates 0.3% more
(23,386,211 against 23,317,234), and the compiler compiles it in the same
time within noise. Stage 3 is byte-identical to stage 2.

## Not covered

A decision that reaches its branch through a phi is left to the lowered
graph's `thread_bool_joins`, which moves every arm of a boolean join and
branches on a comparison arm directly. Taking only the constant arms here,
as a first version did, left the join one comparison operand that was
materialised and tested again: nqueens' `safe` ran 20% longer.

On arm64 `ordmap_insert` retires 2.1% more (122.2M to 124.8M) although
`om_insert`, the only function that changes, is 52 instructions shorter. The
linear-scan allocator hands the callee-saved register that held the left
child across the recursive call to another value, so the child is spilled
at its read and reloaded at the call. x86-64 allocates it as before and its
count is flat. The other aarch64 entries: rbtree −15%, rbtree_ck −10.5%,
deriv −4.9%, sort_ints −1.7%, cfold −1.4%.
