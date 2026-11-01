# 2026-11-01 — the caller holds the payload it lends beside the root it hands over

`ssaunits.retainable_read`, `boxed_payload_read`. Refs #13990.

## The shape

```fern
function (c: Conn) __protected(datagram: [u8], h: packet.Header, space: i32, kind: i32, now: i64): Conn {
  let s: Space = c.spaces[space];
  let Some(keys) = s.recv else {
    return c.__hold(datagram[h.at:h.end]);
  };
  if (space == recovery.APPLICATION) {
    return c.__protected_application(datagram, h, keys, now);
  }
  ...
}
```

`__protected_application` takes `c` counted. It only reads `keys` when
`__receive_key` is spliced into it, so the inference leaves `keys` borrowed.
The caller then hands `c` over and lends a payload read out of `c` at the
same call.

## The rule

The hold pass retains a projection that is still in hand where its source is
consumed. An argument of the call that consumes the source counts as in hand.
The pass only held an array, record or tuple read. A payload read was not a
candidate, so the plan found no hold and pinned the root instead:
`lent_owners` kept `c` with a retain before the call and a release after it.

Every update of `c` in the callee and in the methods it called then found
the box shared. Each one copied it.

A payload read out of a union whose member has its own box now joins the
candidates. A member union lays the member in the union's own box. A count on
that read counts the union, so it stays out. The hold retains the payload at
the read and releases it after the call. The root moves, and the callee's
updates write in place.

## Measured

- `TestSelfHostHeldPayloadBesideMovedRoot`: 1,000 rounds of a method that
  hands its receiver over and lends a payload of it allocate 0 boxes,
  against 1,000 before. The sanitized run is balanced on all three targets.
- The serial HTTP/3 probe of #13990 with every method spliced into its
  caller reads 50.7 allocations a request against 68.6 before. The splice of
  `quic.Conn.__receive_key` alone cost 18 of them, and that cost is gone.
- With the one-block rule of 2026-11-01-a in place, the serial probe reads
  46.8 a request and `TestSelfHostServeAllocsPerRequestH3Mux` 28.8, both
  unchanged. The five coreutils rows of 2026-11-01-a are flat.

## Next lead

With every method spliced, the serial probe still reads 4 a request above
the one-block rule. The mux probe reads 4 above main. The bisect of #13990
names no single method for the rest. The one-block rule stays until that
spread is found.
