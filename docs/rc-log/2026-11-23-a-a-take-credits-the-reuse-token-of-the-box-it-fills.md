# A take credits the reuse token of the box it fills

#16809 fixed a false refusal of the certifier (#16803).

## The shape

A function tests its parameter for a reuse, and then tests a field of the
parameter for a second reuse. Each test makes a reuse token. When the
parameter is unique, the field gets its unit later: the drop of the parameter
stores zero in the slot of the field (a take), and then drops the other
fields.

`client_received` of `tests/stdlib/http_h3q_test.fern` has this shape:

```
function client_received(e: Ends, d: u8[]): h3q.Conn {
  if (e.raw) {
    return h3q.Conn { ...e.client, quic: e.client.quic.receive(d, at_ns(e.now)) };
  }
  return e.client.receive(d, at_ns(e.now));
}
```

The certifier refused it:

```
client_received: leaked where it dies of v46 (borrow ld_at) in b26: 1 unit(s) still held
```

`v46` is `e.client`. With `FERN_RC_CERTIFY=0` and `FERN_SANITIZE=1`, a reduced
program of the same shape gives `allocs=8 frees=8 live_bytes=0`.

## The cause

At the token phi of the field, the field held no unit, because the take
comes after the phi. So the twin of the field keyed on the token had 0 on
both sides, and `twin` made it a plain 0. The take in `apply_store` then gave
the unit to the field on each path. On the path where the token is the box,
the unit belongs to the token, which the function returns. The field died
with a unit that it did not hold.

A drop that releases each field inline has no take, and the certifier
accepted that form. The bug needs the drop helper (`__sem_drop_Ends`), which
the lowering uses for a record with many fields.

## The fix

A take looks for a live token whose box is the taken value
(`live_token_of`). When the token and the box hold plain counts, the take
splits the unit. The token gets the unit on the side where it is the
box. The box gets it on the side where the token is zero.
`token_side_on_edge` decides the two twins at each test of the token.

## Measurements

`TestSelfHostCertifiesATakeAfterTheReuseToken` holds the reduced shape on
x86-64, arm64 and wasm32-wasi, with the memo off. The certifier mutant test
and the other certifier tests pass.

The fix does not move the census. On the tree of #16809, `scripts/certify-census`
gives `fern_main.unproved` 1045, `checker.refuted` 5305, `checker.weakened` 241
and `checker.missed` 225.

## A trap

The pull request re-banked `checker.refuted` from 5298 to 5305, and its commit
message gives the fix as the cause. That is wrong. The Perf lane of main at
`9e21bead`, which does not have the fix, also measured 5305. The comparison was
with the pin, not with a census of main without the fix. Before a pull request
gives itself a move, measure its base.

`client_received` is spliced into its caller in the usual build. The refusal
showed on main only while an inliner bug stopped that splice (#16794). To
see the shape again, put `@noinline` on the function.
