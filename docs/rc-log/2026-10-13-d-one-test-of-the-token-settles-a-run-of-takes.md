# 2026-10-13 — one test of the token settles a run of takes

`ssarc`: `Owed`, `settle_owed`, `settles_later`, `returnable`, `freed_less`;
`classified_death` and `reuse_construct` settle what they are handed.
Refs #11944.

## The shape

```fern
match (t) {
  Node(Black(), l, kx, vx, r) => {
    if (k < kx) { ... return Node(Black, ins(l, k, v), kx, vx, r); }
```

After #12230 a classified or kept donor's uniqueness is read once, into the
token slot. Each take then still tested that slot on its own: `if token == 0
{ retain child }`, one diamond per take, and the donor's death or its
construction tested it once more. rbtree's black arm took the colour, `l`
and `r` and then died as the construction's token: four tests of one
register on every level of every insert, where one answers them all. The
colour was also retained on the shared arm and released straight after its
test on both, six instructions of release on a static box.

## The rule

A take's retain is owed on the token's answer rather than emitted at the
read. Owed retains wait over instructions that only read — a field, a
payload, a variant test, a length, a constant, arithmetic on scalars — and
are settled in one test of the token before anything else: a call, a
construction, a store, another donor's token. Where the donor's death or its
keep's construction comes first, they go into the shared arm of that
operation's own test, so nothing is tested twice. A keep's construction and
its donor's release are one test of the token too: the fresh arm releases a
shared donor, the reused arm a unique one's other fields.

A child a classified donor's take read and the frame then dropped before the
donor dies is given back to the box: no retain is owed on the shared arm, and
on the unique arm the death releases it with the fields nothing took, its
slot cleared from `freed`. Releasing it from the frame instead is unsound by
the certifier's reading and its own: the child was read before the token's
test, so the frame holds no unit of it, and a death that walks the box
through its drop helper nulls the slot as taken.

## Measured

x86-64 retired instructions, outputs and censuses identical:

| entry | before | this change |
|---|--:|--:|
| perceus_rbtree | 60,273,249 | 57,631,329 (−4.4%) |
| perceus_rbtree_ck | 70,784,847 | 67,640,047 (−4.4%) |
| perceus_cfold | 65,868,467 | 64,002,867 (−2.8%) |
| perceus_nqueens | 105,327,536 | 104,479,136 (−0.8%) |
| perceus_deriv | 92,504,008 | 92,019,208 (−0.5%) |

`TestSelfHostClassifiedTakeShapeX86_64`'s `ins` tests its token once, against
three before. The stage-3 compiler is byte-identical to stage 2.

## Not covered

`bump` in the same test keeps a second test: its take is consumed by the
loop the tail call became, so the retain is owed before the next turn.

A paired donor (a token for a construction, not a keep) still reads its
uniqueness at each take (`steal`), since the frame has one token slot: in
rbtree's `balance_left`, `ins(l)`'s result is tested three times.

`Color`'s release survives on the unique arm. Every value of an enum whose
variants are all payloadless is a static box, so the type needs no count at
all, but `ssasem.reference` sees only the type's name and the ~70 places
that ask it have no enum table to hand.
