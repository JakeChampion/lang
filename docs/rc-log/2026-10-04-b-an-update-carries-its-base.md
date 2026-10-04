# An update carries its base

2026-10-04: `semsource.record_update`, `ssasem.update_base`,
`ownership.instruction_carried`. Slice 8.2 of `docs/NET-P0-MESSAGE-LAYER-PLAN.md`
(#9853).

## The shape

```fern
pub function (h: HeaderMap) append(name: string, value: string): HeaderMap {
  return HeaderMap { ...h, names: h.names.append(name), values: h.values.append(value) };
}
```

`ownership.escaping` counts a parameter when it, or something anchored to it,
is carried out. An update lowers to a plain `record_new` over the written
fields, plus a `record_get` off the base for each field it keeps. So an update
that keeps a reference field carries the base through that projection, and
the base is counted.

One that replaces every reference field has no such projection. It keeps only
a scalar, or nothing at all, and an `append` result is a fresh unit. Nothing
of `h` leaves, so `h` stays borrowed. A borrowed base has no unit to give up,
so the update took a fresh box every call.

## The rule

The update consumes its base's box wherever it can. That is a property of the
update, not of which fields it happens to keep. So the construction now names
its base: `imm` is the base's value plus one (`ssasem.update_base`), and
`instruction_carried` carries the base out whatever the update keeps.

The base is deliberately not an operand. An operand would be a use the planner
gives a unit to, and every `record_new` consumer reads `args` as the fields.

Once `h` is counted, the existing pairing does the reuse:
- the update is `h`'s last use;
- `reuse_token`'s `__fern_rc_is_unique` test writes in place when the caller
  moved its last reference in;
- the test copies when the caller kept one.

The lent-slot rung still applies. Carrying is never the only condition, so a
base whose value reaches a lent slot stays borrowed as before.

`mapped_inst` remaps the base along with the operands, for the self-tail-call
splice that renumbers a body. The inference reads the produced graphs, where
the base is the one `semsource` wrote.

## Measured

x86-64, `FERN_LEAKCHECK`. Each update shape runs as 100 calls in a loop.

| Shape | Before | After | Go compiler |
| --- | ---: | ---: | ---: |
| Method appending to a field, scalar kept | 105 | 5 | 5 |
| Same shape as a free function | 101 | 1 | 1 |
| Method changing only a scalar (already counted) | 0 | 0 | 0 |
| `update-base-is-counted`, 30 updates and a kept-receiver copy | 41 | 12 | n/a |
| Framing probe parse, every target | 40 | 37 | n/a |

The leak census balances in each run.
