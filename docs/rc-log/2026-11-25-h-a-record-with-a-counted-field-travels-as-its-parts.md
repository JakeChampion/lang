# A record with a counted field travels as its parts

2026-11-25. Each target. The change is in `sempair.scalar_params` and
`semscalarreturn.param_parts`. Refs #14440.

## The shape

`handshake.__server_run` reads each TLS record and gives it to `__absorb`:

```fern
let rec = __next_record(records, i, wire, at)?;
let a = __absorb(r.server.conn, rec, ccs, r.server.state == S_CONNECTED, r.out)?;
```

`record.Record` is `{ content_type: ContentType, version: i32, fragment: u8[] }`.
`__absorb` takes it `own` and gives it on to `Protection.open`. The record
needs no box, but each request boxed one in `__server_run`.

## The cause

`scalar_params` passed a record as its fields only when each field was a
scalar. `fragment` is an array, so the record stayed a box.

## The change

- `param_parts` also takes a record whose fields are scalars or references
  that are not views. A field that `laid_parts` lays out stays out,
  because a read of it whole makes a box. An `Option` of a
  record still needs a record of scalars, because its `None` passes zeros.
- A reference part keeps the mode and the `own` mark of the record
  (`part_mode`, `part_spelling`). A scalar part has the value mode.

Two more steps keep the parts free of copies and of lost reuse:

- `part_copies_forwarded` replaces each copy of a reference part with the
  part. The field reads of the old parameter became copies, and the plan
  counted each copy as one more holder.
- A record with a reference part stays whole when the result of the callee
  has as many fields. The plan can reuse the box of the parameter for the
  result. `serve.__lent_to` and `h2.__step` are such callees.

The bounds facts of a field also go to the part:

- `ssabounds.caller_facts` gives each array parameter the bound of its
  elements from the arguments of its callers (`ParamFacts.elems`). A record
  field had that bound from `field_lens`, which reads a `record_get`. A
  part has no `record_get`, so without this bound `bits_of` in
  `TestSelfHostFieldLength` kept its bounds check.

## Measured

Allocations a request, x86-64:

| Probe | main `838e2f4a` | the change |
|---|--:|--:|
| `TestSelfHostServeAllocsPerRequestTLS` | 6.00 | 5.00 |
| `TestSelfHostServeAllocsPerRequestH2` | 20.00 | 20.00 |

In the TLS census, the site `handshake.__server_run` goes. The
`ServeAllocsTLSCeiling` goes from 6 to 5.

Two tests changed their programs to keep what they test.
`TestSelfHostPairReturn/record` made 50 of its 100 allocations in
`consume(whole(i, b))`, which now splits. Its `consume` now gives the record
on to `borrowed`, which keeps it whole. `TestSelfHostOwnershipInference`
gives `Sealer` five fields, so that it stays a box at a call.

`TestSelfHostCountedRecordParams` holds the change on the three targets.
Over 1,000 calls the record crosses the call with 1,000 allocations, where
main makes 2,000. Its second loop holds the rule for the result: 0
allocations over 1,000 calls.

## The traps

- The plan reads the `own` of a parameter from the `str` of its `PARAM`
  instruction (`ssaunits.likely_unique`), not only from the modes. A part
  without that mark is retained on entry, and its first change copies.
Two traps came from the other passes:

- A part that is a laid-out field breaks the layout. `lib.due(w: Wait)` in
  `TestSelfHostLaidFields` read `w.timeout` whole, and each call made a box
  for the `Option[Duration]`.
- The rule for the result compares field counts, not types. `h2.__step`
  reuses the box of a `__Run` for a `Step`: two records of three fields.
  A rule that compared any record that the callee builds also stopped the
  TLS case: the spliced `open` builds an `Opened` of three fields, and main
  never reused the box of the `Record` for it.

## Next lead

The TLS census at 5.00 has five sites at 1.00 each. The `Step` that
`server.__read` returns is a record that a call returns, which is #16964.
