# 2026-11-01 — the take follows a read through the phi that carries the shell

`rccertify.between`, and `rcmemo` moves to version 12. Fixes #13989.

## The shape

```fern
for space in 0..3 {
  let plan: Plan = cur.plans[space];
  let bytes: u8[] = cur.__sealed(plan, 0);
  if (bytes.len() > 0) {
    cur = cur.__recorded(plan, now, bytes.len(), false);
  }
  cur = Conn { ...cur, plans: cur.plans.with(space, __emptied(cur.plans[space])) };
}
```

Once methods join the once set, `__sealed`, `__recorded` and `__emptied`
are all spliced into `Conn.__seal`. The read of `cur.plans` for `plan` is a
borrow: no call boundary holds it. The spliced `__recorded` tests `cur` for
uniqueness and reads every slot again, with a retain where the box is
shared. The last update reads every slot again, off the phi that joins the two
values of `cur`. Then it reuses that box.

## The rule

A load is the shell's payload only when no later load of the same slot sits
before the take (the entry of 2026-10-12). `between` compared the base of
each later load with the base of the first, by root. A phi is its own root.
On the path that skips `__recorded`, every later read is off the phi. The
certifier saw none, named the borrow the payload, and reported it leaked
where it died. The same refusal named the `spaces`,
`token`, `scid` and `dcid` reads of the spliced `__sealed`.

A load off a phi the base carries into now counts as a later read of the
slot (`same_load_box`). `w.carried` already lists those phis for the shell
match itself.

The compiled code was right all along: `tests/stdlib/quic_conn_test.fern`
and `tests/stdlib/http_h3q_test.fern` run under `FERN_SANITIZE=1` with every
allocation freed, 1,068,688 and 414,428 of them.

## Measured

- `shell-slot-read-through-a-reused-parent-phi` and
  `shell-slot-read-again-through-a-carrying-phi` in
  `TestSelfHostRcCertifyCases` certify. The second is the loop above in
  SSA. Without the change it was "leaked where it dies of v1 (child ld_at
  of a shell)". `reused-parent-phi-keeps-its-payload` still names the
  later read as leaked when nothing releases it.
- `scripts/net-quic-interop.d/client.fern` certifies with the once-set
  change of the same day.
