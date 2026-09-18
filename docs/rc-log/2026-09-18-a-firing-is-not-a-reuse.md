# 2026-09-18 — a firing is not a reuse

The two entries before this one widened the typed path's reuse pairing from
same-type records to any dying record, union or tuple, and reported 147 → 640 →
839 firings of `__fern_alloc_reuse` compiling the compiler. **Most of those
firings do not reuse anything**, and the instrument said so the moment it was
asked the right question.

`__fern_alloc_reuse(token, n)` compares the donor block's recorded cap against
the slot count the construction asks for. Counts agree: the block is handed
back. Counts disagree: the block goes to its size-class freelist and a fresh
one is allocated. Sound either way — which is what made dropping the static
type test correct — but the second arm has bought a `__fern_rc_is_unique`
call, a branch and a freelist push, and ends where an ordinary drop and an
ordinary allocation would have ended.

Re-probing the paired sites by the slots each side has:

| paired sites, shape-only pairing | 839 |
|---|---|
| donor and recipient agree on slots | 229 |
| they disagree | 522 |
| donor is a union whose variants disagree, so no count is known | 88 |

So 610 of 839 were machinery that could never fire. The emitted text says the
same thing: the compiler's own x86-64 emit went 3,479,894 lines at 147
pairings to 3,521,663 at 839, **+41,769 lines for +82 reuses**. On this
project's numbers — `docs/LOCAL-DEV-LOOP.md`'s ~21,425 Ir per emitted line
through the in-process assembler — that is most of a second of assembler work
per self-compile, bought with nothing.

## The rule

`reuse_slots` answers the number `__fern_alloc_reuse` compares, and the
pairing matches it:

- a record: one shape word and one word per field;
- a tuple: one word per element, no shape word;
- a builtin union: a tag word and a payload word, whatever the variant;
- a declared enum: the LIVE variant's box, so the count is this frame's to know
  only where every variant agrees — the question native's own enum reuse asks
  as `enum_all_variants_same_field_count`;
- anything else, including an array, whose cap is a CAPACITY rather than a
  length: unknown, so not a donor.

The second half matters as much as the first. A donor used to be whatever died
first and fit the shape; it is now taken FOR the construction in front of it
(`next_wants`, one backward pass over the block), and dropped if that
construction does not take it. One token slot serves the block either way, so
holding a box for a construction further down is holding it away from the one
in front.

## Measured

Compiling the compiler, `-emit asm`, x86-64:

| pairing | firings | of which can reuse | emitted lines |
|---|---|---|---|
| same-type records (before this branch) | 147 | 147 | 3,479,894 |
| any shape | 839 | 229 | 3,521,663 |
| **matched slots** | **287** | **287** | **3,501,427** |
| the AST lowering, same sources | 449 | 449 | 2,761,415 |

Choosing the donor for the construction is worth the difference between 229 and
287: a first-come donor of the right size is not the same as the right-sized
donor. Against the starting point the branch is **+140 real reuses for +21,533
emitted lines**, where the shape-only rule was +82 for +41,769.

## Trap

Both earlier entries and the PR body quoted the firing count as the result.
It is a CALL count. Nothing in the reuse suites could have caught the
difference: the differential asserts firings, the RC suite asserts balance, and
a declined pairing is balanced and correct — only slower and fatter. The
question to ask of a reuse number is how many of them the runtime can honour,
and the fixtures written for the shape-only rule had the same fault, two of
five pinning pairings that always declined. They pin matched counts now, and
`cross-type` keeps one deliberate mismatch so its count of 2 over three
candidate sites is what says the pairing asks.
