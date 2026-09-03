# The tuple alias chain: a credited source the retain gate could not see

#7750, the pair left refused by `2026-08-29-alias-chain-credit.md`. Pointing
the tuple limbs at `alias_chain_sites_of` had measured an over-release under a
clean census, and that entry filed it rather than guessing at the mechanism.
This one instruments it.

## What the trace says

`var t: (i32, i32[]) = (i, [i, i+1]); var v = t; var u = v; return u.1.len() +
u.0;`, ONE round, self-host x86-64 built with the bare chain credit,
`FERN_RC_TRACE=1 FERN_LEAKCHECK=1`:

```
rctrace a 0000000010000000 28   # the element buffer
rctrace a 0000000010000028 28   # the tuple box
rctrace f 0000000010000028 28   # the BOX freed first
rctrace f 0000000010000000 28   # then the buffer
leakcheck: allocs=2 frees=2 live_bytes=0     exit 99
```

The emitted `round` has **no `__fern_rc_inc` at all** — neither hop retained —
and the exit sweep releases the box twice: `u` (slot -40) takes the shallow
`"TUP:"` dec first, which frees the box at rc 1, then `v` (slot -32) runs the
`"TUPRCS:"` deep free, which loads `.1` out of the box it just freed, decs the
buffer, and decs the box again. `t` is elided (moved). Under `FERN_SANITIZE=1`
the same build exits 124 with `use-after-free (touched a quarantined block)` —
the deep free's box read, not only the second dec.

So the earlier reading ("one retain for two links") was off by one in the same
direction: there is one release role too many, and it is at hop two.

## Why hop two lost its retain

The tuple limbs' move-on-alias surgery in `lower_stmt_var`'s ExprIdent ladder
migrates the deep class: at a move it drops the alias's `"TUP:"` row and
appends `"TUPRCS:"` on the alias's site. After hop one, `v` therefore holds
`"TUPRCS:"` ALONE. The ladder's own retain gate for hop two asked
`slot_is_reclaimable_tuple || slot_is_reclaimable_rctuple` — `"TUP:"` or
`"TUPRC:"` — of its source, and `v` had neither. The clause did not fire: no
retain, no `note_moved_elided(v)`, no surgery. Meanwhile the credit pass had
already granted `u` its `"TUP:"` row. That is the invariant the ladder's
comments state three times — THE RETAIN AND THE CREDIT MUST BE CO-EXTENSIVE —
broken by the ladder's own output: it created a credited state it did not
recognise.

A one-link alias never reaches that state (the moved-into alias is never a
source), and a scalar tuple chain never enters it (no deep class, so no
surgery), which is exactly the pattern the 08-29 entry recorded without an
explanation.

## The fix

`slot_is_credited_tuple` — any of `"TUP:"`, `"TUPRC:"`, `"TUPRCS:"` on the
slot — is now the one question the ladder and `tuple_dead_alias_bind` ask of
an alias source. With it, hop two takes the same path hop one did: a move
migrates the deep class again and elides `v`; a duplication (the middle link
read after the last bind) retains against the `"TUPRCS:"`-only source, so `u`'s
shallow dec and `v`'s deep free meet a box of rc 2. Both tuple limbs then point
at `alias_chain_sites_of`, the same all-or-nothing closure the string and
struct limbs use.

The surgery still leaves `"TUPRC:"` (the rebind class) on the source rather
than migrating it. That is only observable if a moved-into link is REASSIGNED,
and `alias_chain_sites_of` refuses any reassigned link before crediting.

## Measured

x86-64, `FERN_LEAKCHECK=1`, 100 rounds, self-host built by `make selfhost-cli`;
native and `bin/fern -interp` agree on every exit code. "bare chain" is the
tuple limbs pointed at `alias_chain_sites_of` with the old gate.

| row | native | before | bare chain | after |
| --- | --- | --- | --- | --- |
| rc chain, last link read (`u.1.len() + u.0`) | 200/200/0 | 200/0 live 8000 | 200/200/0 **exit 99** | 200/200/0 |
| rc chain, `u.0 + i` (the old refused row) | 200/200/0 | 200/0 live 8000 | 200/200/0 **exit 99** | 200/200/0 |
| rc chain, SOURCE read | 200/200/0 | 200/0 live 8000 | — | 200/200/0 |
| rc chain, MIDDLE link read | 200/200/0 | 200/0 live 8000 | — | 200/200/0 |
| rc chain, three links | 200/200/0 | 200/0 live 8000 | 200/200/0 **exit 99** | 200/200/0 |
| rc chain in an if-arm | 200/200/0 | 200/0 live 8000 | 200/200/0 | 200/200/0 |
| rc chain, last link RETURNED | 200/200/0 | 200/0 live 8000 | 200/0 | 200/0 (refused) |
| rc chain, middle link HELD in `(i32, i32[])[]` | 300/300/0 | 300/100 live 8000 | 300/100 | 300/100 (refused) |
| scalar chain | 100/100/0 | 100/0 live 4000 | 100/100/0 | 100/100/0 |
| scalar chain, middle link read | 100/100/0 | 100/0 live 4000 | — | 100/100/0 |
| scalar chain, three links | 100/100/0 | 100/0 live 4000 | 100/100/0 | 100/100/0 |
| scalar chain in an if-arm | 100/100/0 | 100/0 live 4000 | 100/100/0 | 100/100/0 |
| scalar chain, last link returned | 100/100/0 | 100/0 live 4000 | 100/0 | 100/0 (refused) |
| scalar chain, middle link held | 200/200/0 | 200/100 live 4000 | 200/100 | 200/100 (refused) |

Every balancing row is 0 on `__rc_underflow_count()` and clean under
`FERN_SANITIZE=1`; the four refused rows report only their leak there. The
if-arm rows were already clean under the bare chain credit because moves are
top-level only — no surgery happens inside a block, so the duplication path
never entered the unrecognised state.

The two refusals both hold under the chain closure without any tuple-specific
vetting: a returned last link and a container-held middle link are the
bare-ident escapes `body_unsafe_for_alias` already refuses, and all-or-nothing
costs the whole chain, source included.

## What gates it

All fourteen shapes above are rows in `internal/e2eselfhost/
self_host_container_alias_bind_test.go` (`tuple_alias_chain*`,
`tuple_alias_scalar_chain*`), with `tuple_alias_chain_refused` moved to its
balancing form. The x86-64 leg now runs EVERY row a second time under
`FERN_SANITIZE=1` and fails on an over-release or use-after-free finding, since
the census is blind to this family by construction and the underflow counter
reports the deep free's box read late or not at all. Verified against the
parent: with the parent's `irlower.fern` checked out, all nine balancing rows
fail on frees (`200/0` and `100/0`, the leak) and the four refusals pass
unchanged. The over-release direction is verified by hand rather than by a
checkout — the bare-chain build above is what the sanitize leg of
`tuple_alias_chain` catches, at exit 124.

## Traps

- The bare-chain build's census is `200/200 live_bytes 0` on every failing
  row. Only the exit code and the sanitizer dissent. Gate this family on
  `__rc_underflow_count()` and a sanitize leg, never on bytes.
- The self-host tracer's `f` sites both resolve to the same release helper
  (`0x400538`/`0x400577`), so the trace says WHICH block and in what ORDER, not
  which slot's sweep freed it. The emitted asm for `round` is what named the
  slots; read it alongside the trace.
- `rc_ml_compute_toplevel` records a move for EVERY top-level bare-ident bind
  at the source's last use, chain links included, so every hop of a
  straight-line chain is a move and every hop after the first has a
  `"TUPRCS:"`-only source. Reading the source or a middle link after the last
  bind is what makes a hop a duplication instead; both forms are rows.
