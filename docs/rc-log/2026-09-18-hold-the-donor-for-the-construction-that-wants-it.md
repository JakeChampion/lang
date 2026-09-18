# 2026-09-18 — hold the donor for the construction that wants it

`2026-09-18-a-firing-is-not-a-reuse.md` made the typed path's reuse pairing
match on the slot count `__fern_alloc_reuse` compares, which took it from 839
pairings (229 of them spendable) to 287 that all are. It also made the donor a
choice rather than an accident: taken for the construction in front of it, via
`next_wants`.

**That reading of "in front of it" was the wrong one**, and the probe written
to rank the next increment said so before anything was built. `next_wants`
answers the slot count of the NEXT construction, so a dying box the next
construction cannot take is left to drop — and the one two instructions further
down that could have taken it allocates instead.

## The measurement

A probe running a greedy pool of tokens beside the shipped pairing, over the
whole compiler:

| rule | pairings |
|---|---|
| shipped: one slot, held for the NEXT construction | 287 |
| one slot, held while ANY later construction claims the count | **390** |
| two slots | 401 |
| three slots | 402 |
| unbounded | 402 |

So the second slot is worth 11 and the third one. **The slot was never the
constraint** — the rule for filling it was. `docs/rc-log/2026-09-16-a-dying-box-is-the-next-constructions.md`
and the entry before this one both named a second token slot as the next lead
on the strength of the donor-side histogram; it is worth 3%.

## The rule

`block_claims` records, in one pass, the LAST position at which a construction
claims each slot count, so "is this donor's count claimed further down?" is a
lookup rather than a scan per candidate. A pending donor a construction cannot
serve is now left held instead of dropped.

One more line was worth 44 of the 103: the slot a construction frees by
spending its token can be refilled by a value dying at the SAME instruction.
The emission order already allowed it — `reuse_construct` reads the token slot,
then `reuse_token` writes it — and the pairing was refusing it only because the
drop scan sat in an `else`. Without that, 346.

## Measured

Compiling the compiler, `-emit asm`, x86-64:

| pairing | firings | of which can reuse | emitted lines |
|---|---|---|---|
| same-type records | 147 | 147 | 3,479,894 |
| any shape | 839 | 229 | 3,521,663 |
| matched slots, held for the next construction | 287 | 287 | 3,501,427 |
| **matched slots, held while claimed** | **390** | **390** | **3,506,632** |
| the AST lowering, same sources | 449 | 449 | 2,761,415 |

**+243 reuses for +26,738 emitted lines**, against the shape-only rule's +82
for +41,769.

## Next lead

Not another token slot: the pool measurement above prices two at +11 and
prices out everything beyond three. What is left is the pairing's own reach —
it is per-BLOCK, so a donor dying in one block and a construction in a
successor pair with nothing, which is what the AST path's `xblock_scan_body`
covers and this does not. Price that against a probe before building it; the
donor-side histograms in this series have now mis-ranked the work twice.
