# 2026-10-11 — the reuse tokens are units of the plan

`ssaunits.with_reuse`, `verify_reuse`; `ssarc.lower`. Closes #11909.

## What moved

The pairing of a dying box with the construction that builds in it was
decided in `ssarc`, the physical lowering, after the unit plan had been
planned and replayed: `reuse_pairs` within a block, `carried_pairs` across
blocks with its route search, `block_keeps` for the fields an update keeps,
and `links_of`, the appends threaded through a borrowed parameter that no
pairing may take as a donor. So the replay never saw a reuse decision, the
carry had a soundness argument nothing re-checked, and reuse could not be
reasoned about beside the takes, holds and links the plan already modelled.

The same code now runs in `ssaunits` (`with_reuse`), once per function when
its plan is final, a static box never a donor (the lowering skips its
drop): `Plan.links` is the links table and `Plan.reuse`
the pairings per block (`ReusePairs`), the kept fields per block (`Keeps`)
and the carry (`Carry`). `ssarc.lower` asks for the tables at its entry,
reads them and emits what they say; it makes no pairing of its own. Making
them at the end of `plan_analyzed` instead cost 5.2% of the compiler's
allocations compiling `checker.fern`: ownership inference plans a function
several times and keeps one. `irtables.reuse_layer_disabled` became
`ssaunits.reuse_layer_disabled`, read where the plan is made.

## What the replay checks

`verify_reuse` runs with the rest of the replay (`FERN_IR_VERIFY`). A token
is taken from a value the step drops and never while the block's one slot is
held; a construction spends the token it was handed and reads nothing of the
box it builds in; a kept donor is its construction's token and its interval
in the slot is no other pairing's; and the carries are a forward flow of the
tokens held at each block's entry: every edge into a block delivers the same
tokens (a null edge delivering the donor as a token all the same), a
construction spends only a token it holds, an edge frees only one it holds,
and a block that leaves the function holds none.
`TestSelfHostSSAPhysicalRCRejects` plants each fault into a good plan of a
two-record function, in one block and split across two, and requires the
named refusal.

## Measured

The lowering is unchanged by construction: `checker.fern` compiled by the
compiler before and after this change emits byte-identical x86-64 assembly,
and the nine reuse suites (`TestSelfHostCrossBlockReuse`,
`TestSelfHostLoopReuseIRX86_64`, `TestSelfHostSemanticReuseDifferentialX86_64`,
`TestSelfHostSSAPhysicalRC`, `TestSelfHostPayloadTakeIR`,
`TestSelfHostFieldAppendInPlaceX86_64`, `TestSelfHostSoleLoopWithX86_64`,
`TestSelfHostLentArrayBracket`, the rejection driver) pin their allocation
counts as before. The compiler compiling `checker.fern` allocates
23,464,642 times against main's 23,405,300 on the same host (+0.25%), the
step index the tables are made over; made at the end of `plan_analyzed`
it was 24,115,055, and over a copy of the plan without its static drops
23,628,988. `ssarc.fern` is 1,170 lines shorter and `ssaunits.fern`
1,508 longer: the moved code, the replay, and the three small helpers the
moved code read from the lowering (`step_of`, `record_fields`,
`tuple_elements`).

## What is left

The tables are decisions written down and checked, not units of the step
walk: a token is still a side table the walk does not plan around, so a
construction cannot yet trade a take or a hold against a reuse. #11910's
one ownership model is where that would go.
