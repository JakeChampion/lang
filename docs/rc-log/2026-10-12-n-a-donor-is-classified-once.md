# 2026-10-12 — a donor is classified once

`ssaunits.keep_slots`, `ssaunits.classified`; `ssarc.classified_death`,
`ssarc.keep_release`. Refs #11944.

## The shape

```fern
match (t) {
  Node(l, k, r) => {
    if (x < k) { return Node(ins(l, x), k, r); }
    ...
```

`t` dies in the match block, after both its children are taken. Each take
was a `steal`: a uniqueness test on `t`, a null stored in the slot on the
unique arm, a retain on the shared one. The token `t` became was a third
test, and its unique arm called `__sem_drop_Tree`, which walked the two
slots the takes had just emptied. The same three tests and the walk ran in
cfold's `reassoc`, deriv's `d`, the rotations rbtree's `ins` splices from
`balance_left`, and every match that takes a box apart and lets it die.

Nothing can change the answer between the first test and the death when
the frame only reads the box in between: the frame holds the box's one
unit, so nothing else can take a unit of it, and a retain of a child does
not touch the parent's count.

## The rule

Two rules, one mechanism: the keep, which already read a donor's
uniqueness once into the token slot when a construction rebuilt the
donor's record in place.

- A keep now rebuilds a declared enum's variant as it does a record: its
  box is the record layout behind the variant's shape word, and a read
  naming the variant the construction builds says the donor holds that
  shape. Any other take from the kept donor follows the keep's answer too
  (`moved`, at the slots `freed` marks): on the unique arm the child is
  the frame's with no null, since the construction overwrites the slot.
  rbtree's red arm, `Node(Red, ins(l), kx, vx, r)`, now builds in its own
  box with `r`, `kx` and `vx` in place.
- A donor whose payloads a block takes and which dies in that block, by
  its drop or as a token held for a construction (in the block or carried
  out of it), is **classified**: a keep with no construction. Its first
  take reads its uniqueness into the token slot, every take follows it,
  and its death releases only the fields nothing took before the box is
  freed or held as the token. Past two such fields the taken slots are
  nulled and the type's drop helper walks the rest, which keeps the code a
  death emits to a call. A donor read as shared is released whole by the
  ordinary test at its death, which also covers its count having fallen
  since. A type with a finalizer is left to its drop helper.

The window from the first take to the death holds the one token slot, so
a donor is classified only where no pairing or keep holds it, and only
when every instruction there that names the donor reads it: a take of
one variant's slot read once, or a test.

## Measured

Uniqueness tests in the lowered function, before, with the variant keep
alone, and with both:

| function | main | variant keep | classified |
|---|--:|--:|--:|
| rbtree `ins` | 62 | 50 | 42 |
| cfold `reassoc` | 10 | 6 | 4 |
| cfold `cfold` | 20 | 20 | 12 |
| deriv `d` | 10 | 10 | 5 |
| nqueens `find_solutions` | 2 | 2 | 1 |

x86-64 instructions for the `bench/perceus_*.fern` entries (callgrind,
outputs identical):

| entry | main | this change |
|---|--:|--:|
| perceus_cfold | 106,693,267 | 68,863,667 |
| perceus_deriv | 103,611,208 | 99,761,608 |
| perceus_nqueens | 117,980,736 | 105,327,536 |
| perceus_rbtree | 101,187,329 | 77,498,369 |
| perceus_rbtree_ck | 112,470,774 | 84,384,527 |

The compiler compiles itself in the same time, stage 3 is byte-identical
to stage 2, the binary is 26 KB larger (an inline walk of every field
cost 308 KB before the two-field limit), and a sanitizer stage 1 compiles
`checker.fern` clean.

`TestSelfHostClassifiedTakeShapeX86_64` pins one test in a tree insert
that rebuilds in a later block and in one that rebuilds the variant in
place (three each on main); the `ClassifiedTake` e2e rows run a carried
token, a dropped donor, and a record released inline and through its
helper, each on a unique donor and a shared one, sanitized and balanced
on x86-64, arm64 and wasm.

## Next

The token slot is one per frame, so of two donors whose windows overlap
only the first is classified: the rotations rbtree splices take three
nodes apart interleaved, and two of them still test per take. An answer
local per classified donor would lift that.
