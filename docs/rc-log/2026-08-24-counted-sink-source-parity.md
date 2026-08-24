# countedSinkSource parity — eight pins become anchors

Promotion-plan step-0 burn-down, analysis-only: `free_eligible_of` drives no
emitted byte (its sole consumer is `rc_plan_dump`), so nothing moves at
runtime — what moves is the plan's agreement with native, which is the
promotion's oracle.

## The gap

`rc_fe_escape_owned` tainted EVERY bare ident stored into a counted sink
(struct/tuple literal, rc-payload variant ctor, `.append`/`.push`, non-string
`.with`). Native's `escapeOwned` stopped doing that in #7345: the sink dups a
`needsRcIncOnAlias` source and the container's deep drop releases that dup,
so a COUNTED source keeps a reference of its own and stays free-eligible.
Tainting it strands exactly that reference.

## The port

The ident arm now consults the recorded type and skips the taint when the
type is in the counted set — the mirror of `needsRcIncOnAlias`'s type switch:
`rc_fe_ptr_type` (arrays, strings, tuple/closure `(`-led names, Map, user
structs and enums) plus builtin `Option`/`Result` and the inferred-lambda
`"fn"` marker. An unknown `""` type keeps the conservative taint — match
bindings and unresolvable inits land there (native reads the checker; its
consuming-binding exemption is part of the same bucket until that port).

## What left the diff gate

The seven `countedSinkSource` pins (move-on-construction, move-on-array-store,
move-on-nested-array-store, four loop-body-move variants) and the
freeEligible/lastUses half of `dead-alias-struct-loop-body-moved-source-
excluded` — all converted to anchors at native's values, factory deleted.
That case's `aliasBindIncs`/`nestedDrops` pins remain: they are the
retain-plan port gap, not this one.

Two of the converted rows are not inert bookkeeping: where the move declines
(`loop-body-move-try-between-decl-and-push`, the loop-body-moved struct
case), native's eligibility is a real per-iteration reclaim the self-host's
EMITTED sweep still misses — the credit table refuses what the plan now
grants. Those reclaims arrive when promotion step 2 routes releases through
`free_eligible_of`; the anchors are the acceptance test waiting for it.
