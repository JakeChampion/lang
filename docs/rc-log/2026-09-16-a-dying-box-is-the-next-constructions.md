# 2026-09-16 — a dying box is the next construction's

The typed path emitted no reuse at all. `ssarc` lowered every `record_new` as a
fresh `__fern_alloc`, so a `with`-style update allocated a second box of the
same shape one instruction after the first became garbage. Compiling the
compiler with `FERN_SEM_IR=` fired `__fern_alloc_reuse` 449 times; compiling it
on the typed path fired it zero times. The physical RC was correct and the
allocation traffic was the AST path's from before reuse existed.

**The pairing.** Perceus places a drop at a value's last use, so the record an
update supersedes dies BEFORE the record replacing it is built. That gap is the
whole opportunity: instead of returning the box to the allocator and taking
another of the same shape back, the drop keeps it as a token and the
construction builds in it. `reuse_pairs` walks a block once and matches an
earlier drop with a later `record_new` of an equal type that does not read the
dying value — this is Koka's ParcReuse, run over the unit plan's dying set
rather than over syntax. One pairing is live at a time and one token slot
serves the block; a second candidate arriving while one is pending is simply
left to drop normally.

**The token.** In place of the drop, `reuse_token` emits the recipe of
`docs/SELFHOST-PERCEUS-REUSE.md` §6.2: the donor's box when this frame holds
its only count, and null otherwise. The runtime helper is pure — it hands a
token back when the size class matches and allocates when the token is null —
so the shared arm releases the donor's count and passes null, and the
construction below it is one code path either way. The static plan already
proved the donor dead; the count test is what makes a hole in that proof
degrade to a fresh box rather than corrupt one another holder still reads
(#4350). The donor's slot is cleared after the test so nothing can reach the
box through the name it arrived under, and the step's remaining drops are
emitted by `drops_less`, which skips the one the construction took.

**The restriction.** This slice pairs only records with no reference fields
(`reuse_shape`). Such a box is pure storage: the reuse arm has no old field
value to release before the new stores land, and no unit the plan carried out
of the donor can still be reading one. A record that owns references needs both
— and needs to agree with the supply the plan chose for each carried field.
That is the next slice, and it is where `ssarc`'s generic `drop_record_fields`
should replace the ten per-field-kind branches `irlower` carries for the same
job on the AST path.

## Measured

Firings of `__fern_alloc_reuse` compiling the compiler on the typed path: 0 →
3. The number is small because almost every record the compiler itself builds
owns a string or an array, so it belongs to the next slice, not this one. The
fixtures show the mechanism working where the shape qualifies: `pure_copy` and
`override_scalar` fire once each where the typed path fired zero, and
`general_reuse_struct`, `general_reuse_crossblock`, `general_reuse_crosstype`
and `own_struct_update_reuse` correctly still do not — they own reference
fields.

## Pinned

`TestSelfHostSemanticSourceRC` is the gate that carries the signal here:
allocations equal frees on all four targets, so a token handed to a
construction that was not entitled to it shows as a double free rather than as
a wrong answer. `TestSelfHostReuseDifferentialX86_64` holds the other half —
the program's observable behaviour is identical with `FERN_SELFHOST_NO_REUSE=1`
and without — which is what says a reuse that fires changed only where the
storage came from.
