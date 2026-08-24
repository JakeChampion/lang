# Per-site plan verdicts — the union gates retire

The measured binding constraint from the Option-family routing: a
name-level verdict cannot split a shadow-sibling pair or a binder
collision, so the routed gates carried UNION halves reading the old credit
proof. This closes it.

## One engine, two keying modes

The fe cluster is now site-aware: taint identity is a BINDING-SITE id, a
scope environment (`env_n`/`env_s`, threaded as parameters so block
recursion scopes it for free) resolves each use to its in-scope binding,
and every recorded assign carries an env snapshot so the fixpoint
re-resolves in the right scope. In NAME mode the site id IS the bare name
— every resolution falls back to the name and the analysis is
byte-identical to the historical one, which is what `free_eligible_of`
(the dump path) still runs: the rcplan diff gate held with zero movement,
so native parity is untouched. `free_eligible_sites_of` runs the same
engine in SITE mode (ids are `reclaim_site_key_of` keys; for-in binders
and match-arm bindings taint arm-scoped synthetic sites), and that is
what the routed release gates consume.

## What retired

With site keys plus the noesc oracle, the plan half passes every suite
the credit halves were carried for — `binder_forin_collide` (the one
structurally name-level failure) now splits per binding, and the OPTAARR
single-site guard is subsumed outright. The routed families' gates are
plan-only under `FERN_SELFHOST_RC_PLAN`; `body_unsafe_for` is no longer
consulted by SCENUMS / OPTARR / OPTSTR / OPTAARR when the plan drives.
Still family knowledge, correctly: `name_is_match_scrutinee` (the plan
does not model the consuming-match release channel) and
`name_is_alias_bound` (the Option kinds carry no alias-bind retain) —
each retires with its own port, not with keying.

## Measured

Name-mode parity: rcplan diff zero movement. Site mode: the full battery
green — leak matrix (all witness cells), OptionCreditSiteKey (collide /
renamed / binder_forin all at their pinned values), CallBoundEnumReclaim,
OptErrStringRelease, UnmatchedOptStrReclaim, ScalarEnumBlockHazards,
ReuseDifferential, ContainerAliasBind, TupleOwnedRetRelease, round trip.
