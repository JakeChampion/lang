# The owned-return admission forgives payload-confined alias binds

Flips `tuple_mixed__ownedret_alias__bind_local` — the cell pinned on 08-24 as
"the admission boundary most tempting to widen next", with the warning that a
careless widening buys over-release, not just clean.

## The refusal

`tuple_ret_local_is_frame_fresh` passed an empty `alias_ok` to
`body_unsafe_for_alias_ret_ok`, so the cell's `var a = t` read as a bare-ident
escape of `t`, the callee never entered `tuple_fresh_ret_fns`, and both halves
were lost at once: no `TUP:`/`ARRF:` credit in the caller, and (through
`tuple_ret_local_names_of` sharing the predicate) `bare_ret_ok` false at the
callee's own credit gates. Constant 230 allocs / 0 frees / 9,320 live bytes
per 100 rounds on the live-arm probe.

## The fix, and why it is not the careless widening

`rctuple_param_alias_bind_sites` (#7667) already collects bare-ident alias
binds vetted through the rc-tuple payload scan on the ALIAS's own name, with
empty registries. Feeding its sites into the frame-fresh walk forgives exactly
the dead-ended reader alias and nothing else:

- `return a` — the alias's own init is an ident, not a literal, so it is
  never frame-fresh itself, and the payload scan on `a` reads the return as
  an escape;
- `var b = a` — a bare-ident escape of `a` under the scan;
- `var e = a.1` — a non-scalar element extraction;
- `peek(a)` — any call arg under the empty registry.

Each sinks the site, so the admission stays refused wherever a second
reference could outlive the frame or a payload could leave it. The forgiven
alias dies with the frame; every admitted return path still hands the caller
exactly one reference.

## Probe table

`internal/e2eselfhost/self_host_tuple_ownedret_alias_test.go` — five cases,
exits confirmed on `-interp` AND native x86-64, every early arm dynamically
live (the 08-24 trap: the matrix cell's own `a.0 < 0` arm is dead), each with
a `FERN_SANITIZE=1` leg asserting identical exit and no over-release /
use-after-free:

| case | exit | pin |
|---|---|---|
| reader alias (the cell, live arm) | 31 | balanced, live 0 |
| alias returned on one path | 4 | frees pinned 0 |
| element extracted through the alias | 58 | frees pinned 0 |
| chained alias | 31 | frees pinned 0 |
| alias through a call arg | 31 | frees pinned 0 |

Non-vacuity: with the `irlower.fern` diff reverted the flipped case reads
230 / 0 / 9,320 and fails the balance assertion; restored, it balances.

## What remains

`tuple_mixed__elemret__payload_refused` (closes via dup-at-extract, not an
admission widening), `opt_str__callarg__read`, and the two
alias-consumed-by-match denials whose notes call the denial sound.
