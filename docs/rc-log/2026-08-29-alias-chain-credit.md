# The alias chain: a set question asked one bind at a time

#7386. `alias_bind_sites_of` vets each `var v = src` on its own, and a bare-ident
re-alias reads as an escape — so `var t = p; var v = t; var u = v;` refuses `v`
(because `var u = v` is an escape of `v`), which costs `p` its credit too, and
nothing releases the box the three names share.

## Measured

x86-64, `FERN_LEAKCHECK=1`, 100 rounds unless stated, `bin/fern -interp` and the
native x86-64 backend agreeing on every exit code.

| shape | native | before | after |
| --- | --- | --- | --- |
| struct chain, last link read | 200/200/0 | **200/0 live 4800** | 200/200/0 |
| struct chain, SOURCE read | 200/200/0 | **200/100** (half) | 200/200/0 |
| struct chain, 400 rounds | 400/400/0 | **400/0 live 19200** | 400/400/0 |
| struct chain, three links | 200/200/0 | **200/0** | 200/200/0 |
| struct with an rc FIELD, chain | 200/200/0 | **200/0 live 8800** | 200/200/0 |
| string chain | — | **200/0 live 3200** | 200/200/0 |
| string chain in an if-arm | — | **200/0** | 200/200/0 |

Unbounded — the 100/400 pair is the same program at four times the rounds.

The source-read row is the one that says the fix is whole rather than partial:
it freed exactly HALF before, and the test corpus had already written down the
prescription — *"a chain widening has to move BOTH to 200/200; moving either
PAST 200 frees is the over-release direction"*.

## The shape of the fix

`alias_chain_sites_of` walks the closure of bare-ident binds RAW — a link cannot
be vetted before the links below it are known, which is the whole reason the
per-site question refuses chains — and then vets the set at once with the chain's
own binds forgiven (`body_unsafe_for_alias`).

**All-or-nothing.** One box is shared by every link, so a link that escapes leaves
a reference nothing accounts for and taints every release in the set. Crediting
the members that happen to check out would release the box under the escapee.
`string_alias_chain_link_returned_refused` (the escape at the END) and
`string_alias_chain_middle_link_held_refused` (the escape in the MIDDLE) pin both
ends of that.

No new walker: the closure reuses `bare_alias_bind_sites_of`, added for the
Option gate in `2026-08-29-opt-alias-bind-boxonly.md`. It is the raw shape both
alias questions are asked about.

## Why it is wired per limb and not into alias_bind_sites_of

Because the tuple limbs measured an OVER-RELEASE under it, and the census is
blind to it.

`var t: (i32, i32[]) = (i, [i, i+1]); var v = t; var u = v;` exits **99**
(`__rc_underflow()`) with a census reading a perfectly clean `200/200
live_bytes 0`. `FERN_RC_TRACE=1` on a single round settles the mechanism: two
allocations, two frees, and only ONE retain for the two links — three decs
landing on an rc of two.

The cause is not established. Both tuple limbs perform move-on-alias credit
SURGERY — at a move the deep `"TUPRCS:"` class migrates from the source to the
alias row, and `"TUP:"` is dropped — and that interacts with a chain credit in a
way one round of tracing does not settle. A one-link tuple alias is fine; a
two-link chain is not; a two-link SCALAR tuple chain is fine.

So the tuple chain stays refused, `tuple_alias_chain_refused` pins the exclusion
with that reasoning, and it is filed rather than guessed at. Shipping the credit
for it would have been a build that trips the underflow counter and balances the
census — the exact combination this log has recorded three times now.

## Span still refused

Measured leaking, each behind its own alias-site walker, each needing its own
vetting decision:

| limb | shape | self-host | native |
| --- | --- | --- | --- |
| rc tuple | `(i32, i32[])` chain | 200/0 live 8000 | 200/200/0 |
| scalar tuple | `(i32, i32)` chain | 100/0 live 4000 | 100/100/0 |
| strarr | `string[]` chain | 500/100 live 6400 | 100/100/0 |
| rc enum | `E.A(i32[])` chain | 200/0 live 8000 | 200/200/0 |
| Option | `Option[i32[]]` chain | 200/0 live 8000 | 200/200/0 |

The enum and Option limbs carry the payload-out hazard
`2026-08-29-opt-alias-bind-boxonly.md` documents, so each is its own piece of
work rather than a sweep.

## What moved that was not a leak count

- `dead-alias-string-chain-refused` in `TestSelfHostRcPlanDiff` was a pinned
  NATIVE DIVERGENCE: native kept the third link's retain (`4:2=c`) and the
  self-host granted none. The self-host emits it now, so the pin is retired and
  the row is kept as a converged one.
- `leak_alias_chain` in the wasm and arm64 leakcheck suites is an INSTRUMENT row
  — it proves the census can report "leaky" — and used this shape only because it
  leaked. Both now use a reassigned alias, refused as a property of the class
  rather than conservatively, so the instrument keeps a shape that will not be
  fixed under it.

## Already fixed before this

#7386's headline half. The `@` binder desugars to a chain (`build_struct_match`
caches the scrutinee), and the issue measured `if let w @ P { .. }` leaking and
suppressing a plain `if let` sibling. Both measure clean on current main, closed
somewhere in the arc since the filing; only the explicit chain was left.
