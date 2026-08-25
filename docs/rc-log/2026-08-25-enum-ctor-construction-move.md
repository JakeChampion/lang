# The enum-ctor construction move — a port gap the differential names exactly

Native's `markConstructionMoves` marks an owned rc local consumed at its last use
inside a container construction, so the construction's inc and the local's sweep
dec cancel. The self-host's `rc_ml_construction_moves` ported four of its five
arms — struct literals, array literals, tuple literals, and `append` / `with`
arguments — and not the fifth.

`internal/ir/rc_analysis.go`, in that switch:

> Slice 1b: an enum variant constructor — emitEnumNew now inc's an aliased pointer
> payload and the enum's deep drop dec's it, so a moved last-use OWNED-LOCAL
> payload balances

## Measured by the instrument built for this question

A new `TestSelfHostRcPlanDiff` case, `enum-ctor-payload-move`, on

```
var p: P = P { xs: [1, 2], n: 3 };
var e: E = E.A(p);
```

reports, before:

```
f: movedLocals diverge — native "p" vs self-host ""
f: moveSites   diverge — native "5:17" vs self-host ""
```

and passes after, with no pinned divergence anywhere in the suite needing an
update. The case carries no anchor and no `diverge` entry on purpose: it asserts
only that the two compilers agree, which is the whole claim.

## What this does and does not buy

It does NOT move any leak census. Every probe measures exactly as before, on both
spellings of the ctor and across the array, tuple and struct families — the port
adds the move VERDICT, and the self-host has no ctor-payload retain to elide with
it yet. That is the honest reading: this is parity in the table, banked for the
consumer that needs it.

The consumer is the `variant__moved` cell of the container-sink matrix, which
cannot be closed without it. `variant_struct_payloads_fresh` refuses a bare-ident
struct payload, and the admission that would let it through is "the source's value
is dead after this store" — a move site, which until now the self-host never
recorded for a ctor argument.

## The other half, found on the way and NOT fixed here

The two ctor spellings disagree, and the disagreement is a latent over-release.
`struct_box_sink_stored_expr`'s ExprFieldAccess arm knows only `append` and
`with`, so the QUALIFIED `E.A(p)` is not seen as a counted sink while the bare
`A(p)` is. Measured, 100 rounds:

| spelling | self-host | native |
|---|---|---|
| `E.A(p)` | 300 / 200 | 300 / 300 |
| `A(p)`   | 300 / 0   | 300 / 300 |

On the qualified spelling `p` keeps its struct credit and is swept; on the bare
one it is refused and nothing releases it. Today nothing double-frees, because the
enum has no payload drop for a bare-ident payload. The moment it earns one — which
is exactly what closing `variant__moved` adds — both `p`'s sweep and the enum's
payload drop would release the same box.

So that fix and the payload admission have to land together, and neither belongs
in this commit: the spelling fix alone takes `E.A(p)` from 300/200 to 300/0, which
is more consistent and measurably worse.
