# 2026-09-18 — the in-block pairing is close to exhausted

**WRONG, and corrected by
`2026-09-18-the-donor-that-dies-at-its-own-construction.md`.** Every probe
below asks which constructions have a donor dying at an EARLIER position, and
none asks about one dying at the construction's own. There are 795 of those,
twice the pairing count this entry calls close to exhausted. The numbers stand;
the conclusion does not.

No code change. This entry exists because the leads it prices are three
near-nulls and one expensive maybe, and the log's own rule is that a null
result recorded is what stops the next person repeating it. The reuse work of
2026-09-18 left three candidate next steps; all four numbers below were
measured before anything was built, and three of them killed their lead.

Measured on the whole compiler (`FERN_SEM_XBLOCK_PROBE`, a scratch probe in
`reuse_pairs` and `ssarc.lower`, not committed):

| lead | what it would add |
|---|---|
| in-block pairing today | 348 (390 firings; the probe reruns the rule and skips 40 large functions) |
| `variant_new` as a recipient | **+1** |
| `array_new` as a recipient | **+6** |
| cross-block, donor's block is the construction's ONLY predecessor | **+31** |
| cross-block, any DOMINATING block | **+144** (ceiling) |
| constructions with no matching donor in any dominating block | 4,129 |

## What each one turned out to be

**`variant_new`** is the last construction form a token cannot be spent at, and
closing it is a completeness argument rather than a yield one: 17 sites exist
and 16 of them have no donor in front of them at all.

**`array_new`** looked like the big one and is not. An array cannot DONATE —
its cap is a capacity rather than a length, so the slots the block has are not
the slots its type names — but it can be a RECIPIENT, because `arr_make`
allocates `__fern_arr_box(n)` for n elements, the same unit
`__fern_alloc_reuse` compares. The shape-only histogram of
`a-box-is-slots-not-a-type.md` ranked this at 343 (`array_new`, donor pending,
not a recipient). Once the donor and the construction have to agree on the
COUNT, it is 6. The 343 were pairings that would have declined, which is the
same lesson as that entry's correction, arriving through a different door.

**Cross-block** is the only lead with headroom, and the cheap form does not
reach it: restricted to a donor whose block is the construction's sole
predecessor — one path between them, nothing to thread a token past — it is 31
of the 144. The full form needs the token carried in a frame slot across
arbitrary control flow, which means proving no other pairing clobbers the slot
on any path from the drop to the construction, and that a loop back edge
cannot reach the construction with the token already spent. That is a real
mechanism on a memory-safety-critical path for at most +41%, and the 40
functions the probe skips (more than 240 blocks, where the dominance matrix
gets expensive) mean 144 is a lower bound of unknown tightness.

## The finding under the findings

**4,129 constructions have no donor of a matching slot count anywhere in a
block that dominates them** — twelve times what every remaining lead adds
together. The binding constraint is no longer which forms the pairing admits;
it is that a dying box and a later construction rarely want the same number of
slots. Widening the admitted forms further is close to done.

That also means the gap to the AST lowering's 449 is not a missing form. Where
it comes from is the next thing worth measuring, and it should be measured by
naming the sites — the histogram in this area has now mis-ranked the work four
times (the second token slot, the tuple leaf's size, the array recipient, and
the firing count itself), and every one was caught by probing the specific
question rather than reading the ranking.
