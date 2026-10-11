# 2026-10-11 — a dying box is carried past a join from arms of different counts

`ssarc.carried_pairs`. Closes #11911, the shape the 2026-10-07 carry entry
left: a donor dying in match arms whose boxes have different counts was never
carried.

## The shape

```fern
match (s) { One(v) => { t = v; }, Two(v, w) => { t = v + w; } }
let m: M = M { tag: t, xs: id([n]) };
```

`s` dies in both arms. `One` is a two-slot box and `Two` a three-slot one;
`M` claims three. The carry's route search needs every edge into the
construction's block to come from a holding block or a block on the way, so
the join, entered from the `One` arm, refused it and `m` always allocated.

## The rule

A predecessor of the construction's block in which the donor dies as a box
of another count is a null edge. That block drops the donor where it dies,
as it did before; the edge into the construction's block zeroes the donor's
local; and the carried construction tests the local before the uniqueness
test, taking a null token on that path and the box's own test on the
others. `__fern_alloc_reuse` allocates on a null token, as it always has.

Only a direct predecessor of the construction's block can be a null edge,
and only one that is not itself a holding block. A block on the way between
holding blocks and the construction still has to be entered from a holding
block or the way: a null edge into a way would carry a zeroed local past
blocks whose exits drop the donor. The holding blocks, the ways and the exit
edges are as the 2026-10-07 entry describes them; a null edge adds a fourth
part to a carry (`Carry.null_from`, `null_to`, `nulled`) and nothing else
changes.

The certifier reads the join's local as a phi of the box from the holding
arm and null from the null edge, and the null test before the uniqueness
test as the region that proves it a box: `TestSelfHostCrossBlockReuse` runs
under the certifier's default refusal mode, so the shape compiling at all is
the certifier accepting it.

## Measured

`TestSelfHostCrossBlockReuse`, allocations on x86-64, arm64 and wasm, under
the sanitizer and the leak census:

| | main | this change |
|---|--:|--:|
| `arms-of-different-counts` (`f(2) + f(3)`) | 6 | 5 |
| `three-arms-two-fit` (`f(3) + f(4) + f(5)`) | 9 | 7 |
| `arms-of-different-counts-churn` (a million joins, alternating) | 3,000,000 | 2,500,000 |

Every other row of the suite is unchanged, as are `TestSelfHostLoopReuse*`,
`TestSelfHostSemanticReuseDifferential*` and `TestSelfHostRcCertifyCases`.

The certifier census over `compiler/fern.fern` on x86-64 with this carry in
the compiler: 6875 functions, 154,030 tracked, 28,713 unproved, 0 findings.
The compiler compiling `checker.fern` (`scripts/selfhost-alloc-bench`,
x86-64) performs 22,987,903 allocations against main's 22,985,837: the
null-edge split of each target's predecessors costs 2,066 more, +0.009%.
The per-module fixpoint (`TestSelfHostPerModuleEmitAllFixpointX86_64`) holds.

## What is left

A null edge is a direct predecessor of the construction's block. A donor
dying as a box of another count two blocks before the join, with a block
between that claims nothing, is still refused: the block between would need
to be on the way and entered from the null edge, and the way's exits would
drop a donor that path already dropped. Letting a way be entered from a null
edge means the exits of that way test the local before dropping, which is
the same test the construction now makes; it is not done here because no
measured shape asked for it.
