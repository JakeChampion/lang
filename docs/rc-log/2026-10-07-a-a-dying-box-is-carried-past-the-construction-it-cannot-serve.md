# A dying box is carried past the construction it cannot serve

2026-10-07: `ssarc.carried_pairs`, `seminline.leaf_word`, `std/serve`. The
serve loop's hello request, `TestSelfHostServeAllocsPerRequest`, 1 to 0.

## The shape

```fern
t = Tab { ...t, a: t.a.with(at, i), c: t.c.with(at, i + 1) };
let kind: Kind = Idle;
if (i % 3 == 0) { kind = Pip; } else if (i % 3 == 1) { kind = Bad(i); }
return (Tab { ...t, b: t.b.with(at, i) }, keep, kind);
```

The first rebuild of `t` dies after the arms join and the second takes its
box over, as a rebuild does. Between them, one arm builds `Bad(i)`, a
two-slot box where the table has three. The carry of a dying box to a
later block's construction stopped at the first block that built anything:
`route_graph` marked blocks that build, `carry_route` took the first such
block as the target and `route_to` refused a way through one. With `Bad`
on the way the route was to `Bad`'s block, whose first construction claims
two slots, so no donor matched and the table was dropped where it died;
the second rebuild allocated, 100 of 136 allocations in the probe. With
`Pip` in `Bad`'s place, the way built nothing and the table was carried.

In `std/serve` the shape was `__serve_took`: the connection table rebuilt
twice around the parse of what is behind the request, the `Illformed(status)`
box built on the malformed arm between them, and the table copied once per
hello request, 176 bytes, the first thing the loop allocated once the
`Framed` box was gone.

## The rule

The carry is by slot count. `route_graph` records the counts each block's
constructions claim (`block_claims`); a search for a donor of `n` slots
treats a block as a target where it claims `n` and as passable where it does
not, whatever else it builds, and `route_to` admits such a block on the way.
The donor is handed to the first construction claiming `n` in the target
(`first_claim_of`), not the first construction. A block on the way may now
build, and its own pairings stand: the held donor stays in its local until
the target's construction writes the token slot, and a pairing on the way
writes and spends the slot within its block.

The donor is chosen before the route rather than after: for each block, the
first dying value whose count some construction ahead claims, each count
tried once. One donor leaves a block, as before, for the same slot.

This is the across-block half of
`2026-09-18-hold-the-donor-for-the-construction-that-wants-it.md`, whose
within-block rule already holds a donor past a construction that cannot
take it.

## Two more on the way

`seminline` leaves were scalar only: a function computing on integers,
floats and tuples of them, reading a record parameter's scalar fields at
most. A leaf may now take a variant parameter apart and build a tuple or
variant of words (`leaf_word`): its values are scalars, references a word
carries whole with no view in them, and tuples of those, a record among
them a parameter or a payload read. Splicing it still moves no count, and
it puts a match on a call's result at the call, where `sempair` can return
the value in two words. `__serve_behind`, the match that turns the parse's
`HttpFraming` into what the loop carries, is such a leaf; before, taking the
framing as a parameter was a whole use at each of its six calls, and the
parse built the `Framed` box for every caller to take apart at once.

The loop itself carried the framing whole: `(conns, keep_alive, tail)` from
`__serve_took`, `tail` into `__serve_start` and `__serve_respond`, and a
flight's `tail` field. It carries a `__Behind` (`Pipelined`, `Idle`,
`Expecting`, `Illformed(status)`) and the framed request beside it instead.
A fresh `http_framed_none()` as the request where there is none costs three
boxes a parse (its stream's type has a finalizer, so none of its nest is a
static box), so the loop passes a request it already holds, read only
behind `Pipelined`.

`FERN_SEM_DUMP=<function>` prints the semantic graph the count planner is
handed, which is what found the carry: the graphs of the two shapes above
differ in one instruction.

## Measured

`TestSelfHostServeAllocsPerRequest`: 0 allocations per hello request.
`TestSelfHostReuseCarry`: 37 allocations over 100 rounds (the 33 `Bad`
boxes and the probe's own four), from 136. `TestSelfHostSemanticInline`'s
`kind` case covers the leaf. The perf corpus: `sort_strings.ir` -5.4% and
`ordmap_insert.ir` -2.1% on x86-64, `sort_strings.text` +1.8%.
