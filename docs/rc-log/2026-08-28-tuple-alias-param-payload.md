# The TUPB tier learns the alias forgiveness — the last empty-alias_ok consumer

`tuple_mixed__fnscope__alias_param` and `tuple_mixed__if_block__alias_param`
sat at a constant 2 allocs / 0 frees / 80 live bytes: a callee that binds
`var x = src` and only READS through the alias cost every caller of that
callee its `TUPRCS:` deep free. #7553 closed exactly this asymmetry one tier
up — `borrowable_params_interproc` unions alias-bind and alias-reassign
proofs into the box verdict — but `tuple_payload_borrow_flags` still passed
an empty `alias_ok` to `rctuple_payload_escapes_alias`, so the payload tier
read the bind as an escape and `TUPB:round` param 0 stayed `'0'`.

## The vet is the class's own scan, not the box walker

`rctuple_param_alias_bind_sites` collects the param's bare-ident alias binds
(the `param_alias_bind_sites` recursion shape, `StmtDefer` arm included) and
vets each site with `rctuple_payload_escapes_alias` on the **alias's own
name** — the `strarr_alias_bind_sites_of` lesson, one class over. Box-level
vetting (`body_unsafe_for`) would bless `var x = src; return x.1;`: the box
flag only proves the callee never keeps the box, while the caller's `TUPRCS:`
deep free walks every rc position and would free the handed-out element —
the sanitizer-confirmed UAF (exit 124 / exit 99) that killed v1 of the TUPB
tier (`2026-08-24-tuple-borrowed-arg-payload-tier.md`).

Scanning the alias over the whole body with an empty `alias_ok` also keeps
the conservative refusals for free: a chained alias (`var y = x`) reads as a
bare-ident escape of `x`, and an onward pass of `x` is an escape under the
empty registry. A REASSIGNED alias stays forgivable when its uses pass the
payload scan — the rebind ends the aliasing, the same box-level reasoning
`param_alias_bind_sites` documents — and whatever the callee then leaks of
its own fresh tuple is a separate (pre-existing) gap: measured 202/2 on the
probe, exits matching both oracles.

Both call sites of `tuple_payload_borrow_flags` (the plain oracle and the
interproc one that actually runs) pick the change up for free, and the
collector uses empty registries only, so the flag stays registry-independent
and the interproc fixpoint's monotone-decreasing convergence argument is
untouched.

## Probe table

`internal/e2eselfhost/self_host_tuple_alias_param_test.go` — every exit
confirmed against `-interp` AND native x86-64; every case re-run under
`FERN_SANITIZE=1` (must exit identically, no over-release / use-after-free
report; a leak report on a refused row is the census's business):

| case | exit | counts |
|---|---|---|
| fnscope alias, reads only (the cell) | 43 | balanced, live 0 |
| if-block alias, reads only (the cell) | 75 | balanced, live 0 |
| alias hands element out (`return x.1`) | 20 | frees pinned 101 — must stay refused |
| chained alias (`var y = x`) | 43 | frees pinned 0 — refused |
| reassigned alias | 11 | frees pinned 2 — flag sound, callee's fresh tuple leaks (pre-existing) |

## Gates

Both leak-matrix rows flipped clean/clean and the full x86-64 dump (131
rows) moves nothing else; the arm64 dump, stage-2 arm64 fixpoint and the
emit-all fixpoints ran with the change (results in the PR). Feature census,
complexity ratchet and check-sources green.

## What remains

Five native-clean/selfhost-leak cells: `tuple_mixed__ownedret_alias__bind_local`,
`tuple_mixed__elemret__payload_refused` (closes with dup-at-extract, NOT a
TUPB widening — `2026-08-24-tuple-borrowed-arg-payload-tier.md`),
`opt_str__callarg__read`, and the two alias-consumed-by-match denials whose
notes call the denial sound.
