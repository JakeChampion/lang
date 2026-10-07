# 2026-10-07 — a dying box serves every arm

`ssarc.carried_pairs`. Closes the last two shapes of #10798 that the typed
lowering did not reuse.

## The shapes

```fern
let a: P = P { x: i, xs: id([1]) };
let s: i32 = a.x + a.xs[0];
if (i % 2 == 0) { let b: P = P { ... }; } else { let c: P = P { ... }; }
```

`a` dies before the branch and each arm builds a record of its count. The
carry of a dying box to a later block's construction searched every block
claiming the count, but kept the first route it found, so `b` took `a`'s box
and `c` allocated.

```fern
match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } }
if (i > 0) { let b: M = M { tag: i, st: On(3) }; ... }
```

`d` dies in each arm of the match, and `b` is built after the arms rejoin. A
route had one block holding the donor, and every block on the way had to be
entered from that block or from the way, so the join, entered from both
arms, refused it and `b` allocated.

## The rule

A carry is now a donor value, the blocks holding it, the constructions it is
handed to, and the edges where it is dropped instead. The holding blocks are
every block the value dies in after its last construction, as a box of the
same count, that holds no other donor and has an edge out. A block that
returns has nowhere to drop a box it held: taking one as a holding block
leaks the box on that path (48 bytes in `returning-arm-holds-nothing`). The
constructions are every block claiming the count first on a path out of the
holding blocks whose way is entered only from a holding block or the way,
and that no earlier carry serves; the first construction in such a block
that does not read the donor takes it.

The ways may share blocks. What makes a carry sound is that every block on
any way has all its predecessors on a way or among the holding blocks, so
whatever path enters a way carries the box some holding block kept. Each way
has that property, so their union has it. A path leaves the union at a
construction, which spends the box, or at an edge to any other block, which
drops it, and can enter it again only through a holding block, which holds
a new value. The donor is not live past a block it dies in, so no path from one
holding block reaches another without passing the donor's definition, which
no way can hold: its dominators reach back to the function's entry, which
nothing ahead of a holding block reaches.

A target an earlier carry already serves is left to that carry, and an edge
into it drops the later donor. The previous rule dropped both carries.

An edge leaving several carries' ways drops every donor it leaves, where it
used to drop the first.

## Measured

Allocations, semantic inlining off (`FERN_SEM_INLINE=`), x86-64 and wasm
agree:

| | main | this change |
|---|--:|--:|
| a donor to both arms, loop of 4 | 6 | 4 |
| the same, outside a loop (`record-into-both-arms`) | 3 | 2 |
| `else if` arms sharing a way, one arm returning | 7 | 6 |
| a donor shared by `keep` in two iterations | 17 | 15 |
| `d` dying in both match arms (`#10798`'s enum-field case) | 11 | 8 |
| `d` dying in both arms of an `if` | 8 | 6 |
| `a` dying in three arms, one returning | 8 | 6 |
| match arms of different counts (control) | 6 | 6 |

The bench corpus, retired instructions: `pmap_insert` -21.2% on aarch64 and
-13.4% on x86-64, `string_build` -3.6% / -4.0%, `struct_drop` -2.3% / -2.9%.
Emitted size falls on six rows and rises on `pmap_insert` (+1.9% / +2.2%)
and `pvec_with` (+1.2% / +1.1%), where the reuse sequence is emitted in each
arm. The analysis costs the compiler 43,531 more allocations compiling
`checker.fern` than main's (+0.19%, `scripts/selfhost-alloc-bench`).

The compiler compiling itself (x86-64, `FERN_LEAKCHECK`, semantic inlining
on): 99,285,055 allocations with main's carry in its own code, 99,284,338
with this one, 717 fewer. Both stage 3s are byte-identical. The self-built
compiler is 10,648 bytes larger (+0.1%). The compiler's own code has few of
these shapes once inlining has run: the gain is in code shaped like the
table above, which a function too large to splice keeps.

## What is left

A donor dying in arms that hold boxes of different counts is not carried: the
join is entered from an arm whose box does not fit, and a token that is the
box on one path and null on the other is not a form the carry has. A
construction whose value is never read still allocates and drops its box at
once (`cross-block-both-arms` in `self_host_loop_reuse_ir_test.go` builds `a`
and never reads it).
