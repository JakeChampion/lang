# 2026-09-18 — a box is slots, not a type

The typed path's reuse pairing (`ssarc.reuse_pairs`, 2026-09-16) matched a
dying box to a later construction only when `semtypes.equal` held between the
two. That test is the whole difference between 147 firings and 640: measured
over the compiler compiling itself, **603 of the 750 constructions that had a
donor dying in front of them were refused by it alone**, and nothing else.

A box is slots. `__fern_alloc_reuse(token, n)` compares the donor block's
recorded cap against the count the construction asks for, hands the block back
when they agree, and pushes it onto its size-class freelist and allocates
fresh when they do not. So a mispaired donor was already only ever slower,
and a static type equality on top of that runtime check bought nothing it did
not already give.

Two things had to move with the test.

**The shape word.** A struct box is `[shape, f0, f1, …]`, and `struct_make`
writes slot 0. A construction building through a token never runs that op, so
the pairing had been reading the word off the DONOR at the token — live on
both arms — and writing it into whichever box arrived. That is correct exactly
while the two types agree. The recipient writes its own now
(`ir.op_struct_set_shape`, the op the AST path's enum-donor reuse already
used), which is both the general answer and one instruction shorter: the fresh
arm's box carries the length `__fern_arr_box` wrote at slot 0 and needs
overwriting either way.

**The union donor.** `reuse_shape` admitted a union value along with a record
— `named()` answers for both — and a union could never be SPENT, because a
`record_new`'s result type is a struct and the equality test refused it every
time. So 1,036 of the pendings taken over the compiler's own sources were
dead weight in the one token slot the block has, and the drops behind them
(the probe counts 231 union-blocked) never got a turn. They pair now.

## Measured

**Corrected by `2026-09-18-a-firing-is-not-a-reuse.md`: a firing is a CALL,
and most of the ones this entry counts decline at run time.** The numbers below
stand as measured; what they measure is not what this entry took them for.

Firings of `__fern_alloc_reuse` compiling the compiler
(`bin/fern-selfhost -target x86-64-linux -emit asm examples/self_host/fern.fern`):

| path | firings |
|---|---|
| typed, before | 147 |
| typed, after | **640** |
| AST lowering, same sources | 449 |

The probe behind the 603: a temporary report in `reuse_pairs` naming each
construction's kind and, when a donor was pending, why it was not spent.

| construction | outcome | count |
|---|---|---|
| `record_new` | paired | 147 |
| `record_new` | donor pending, types differ | 603 |
| `record_new` | no donor pending | 3,424 |
| `array_new` / `append` / `with` / `tuple_new` | donor pending, not a `record_new` | 1,044 |
| `tuple_new` | no donor pending | 642 |

`readsdonor` — a construction whose own operands name the dying value — fired
**zero** times over the whole compiler, which is worth recording: that guard
costs nothing and catches nothing here.

The donor side of the same probe: 7,432 struct drops and 1,036 union drops
were taken as pendings, and 11,470 struct drops were passed over because one
was already pending. That last number is the next lead and it is not what it
looks like — most pendings are never spent at all, so a second token slot buys
a pairing only where a second construction follows.

## Pinned

`TestSelfHostSemanticReuseDifferentialX86_64` gains `cross-type`: two structs
of equal arity that are members of one struct-union, so the `match` READS the
shape word each construction wrote — a recipient inheriting the donor's takes
the wrong arm rather than leaking, which is what makes the case witness the
word and not merely the storage. Beside them a three-field recipient offered a
two-field donor, so the runtime's decline path runs on a cross-type pairing.
Three firings on the typed path, zero on the AST one, so the count cannot be
satisfied by `irlower`.

`TestSelfHostSemanticSourceRC` gains the same shapes (`cross_step`,
`cross_back`, `cross_back_code`, `cross_loop`, `cross_wide`), balanced on all
four targets.

## Trap

The first draft of the RC fixture called `sigil_code(cross_back(5))` from
`main`, which leaked 48 bytes on every target. Not this change: an AST-lowered
caller handing a produced callee's union result straight to another produced
callee is the documented gap `docs/SELFHOST-SEMANTIC-SOURCE.md` §Remaining
already names, and every other enum shape in that fixture routes around it.
The hand-off happens in a produced body now. A leak that appears when a
fixture lands is worth attributing before it is worth fixing.
