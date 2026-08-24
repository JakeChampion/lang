# The struct counted-sink refusal narrows — killer-drops slice 7

`struct__local` was the last `local` row pinned leak. It needed no new
analysis: the routed struct gate refused every name stored into a counted
sink, and its own comment named the condition for lifting that —

> it retires with the killer-drops-fields release-protocol port, not with keying

The port is half done here, and the refusal narrows by exactly that half.

## What the refusal was

Native classifies a StructLit / TupleLit / append slot as a COUNTED sink: the
store INCs, so it does not taint its source. The self-host's plan agreed —
`freeEligible` matched on both sides — but the gate carried an extra conjunct
(`!struct_box_sink_stored_stmts`) refusing every such name anyway, because a
struct's release is a deep FIELD WALK plus a box dec and both walks were
rc-blind: two owners of one box meant two walks over its fields.

## The gate, and what it unlocked

The exit sweep releases through `emit_struct_field_drops_gated`, which runs the
field walk under `__fern_rc_is_unique` — the gate the holder side
(`__struct_drop_<T>`'s k_struct arm) always applied, and the shape slice 5 gave
the enum family. The walk belongs to whichever owner finds rc 1; the box dec
stays unconditional and hands the last owner its turn.

It is applied ONLY to the names the forgiveness newly admits, marked
`"SINKSHARE:"` by `struct_field_shared_stmts`. Gating every struct slot broke
the established alias-bind arithmetic in BOTH directions, and CI caught it on
`struct_alias_in_a_conditional` after the targeted battery had missed the
suite:

- with the alias's `"NODEEP:"` box-only role still in place, the source skipped
  its walk at rc 2 and the alias was forbidden one, so NOTHING freed the fields
  — 150 frees where 200 are due;
- with that role removed so both owners could walk, the shape went to exit 99.

The static pairing (source deep, alias box-only) is measured and load-bearing;
the runtime gate is for the new admissions, which have no such pairing. Keeping
them separate is what makes both correct.

## Why only the struct-literal FIELD position is forgiven

Dropping the conjunct wholesale was tried and is UNSOUND, which the suites said
plainly: exit 99 on both bound-element rows, and a use-after-free on the
block-scoped escape-into-a-container probe (it read freed elements). The
emitted code named the cause — the function contained no `rc_inc` at all, so
the release had no retain to balance.

The two sink kinds are not equivalent:

- A struct-literal FIELD retains UNCONDITIONALLY, at the nested-struct / enum
  arms of the ExprStructLit lowering. Source and holder both hold a counted
  claim, and both releases are now rc-gated, so this position is forgiven.
- A CONTAINER sink (append/with arg, array or tuple element, variant payload)
  retains only under `slot_is_reclaimable_struct`, which REFUSES a retired
  slot. A block-scoped `var s` appended inside a loop therefore gets no inc,
  while the scoped sweep — reading the retirement-TOLERANT sibling — would
  still release it. Retain and release disagree about retired slots, so these
  sinks stay refused.

Closing that asymmetry is the remaining half of the port, and it is a
co-extensiveness fix (#7253), not another gate.

## Measured, x86, 100 rounds, against native

| probe | before | after | native |
|---|---|---|---|
| conditional field share (`if { P { f: src } }`) | 250 / 50 | **250 / 250** | 250 / 250 |
| the full `struct__local` cell | 550 / 350 | **550 / 550** | — |
| block-scoped escape into a container | correct | correct | correct |
| append-rebind | 600 / 200 | 600 / 200 (unchanged) | 600 / 600 |

The append-rebind shape measures 600/600 under the wholesale retirement and is
the prize for finishing the port; it stays leaking here rather than shipping
the version that also dangles.

## Why the conditional case is the one that leaked

The unconditional share is clean without any of this, and the emitted code says
why: it has NO `rc_inc`. The #6726 move fires, ownership transfers to the
holder, and the source's sweep is suppressed as moved_elided. Put the same
construction inside an `if` and the move correctly refuses — it does not
dominate — so the retain fires and the source needs a release of its own. That
is the one the refusal was withholding.

## What moved

- `struct__local` flips to clean; the whole `struct` row is clean end to end.
- `struct_box_sink_stored` keeps only its container arms, and its doc now
  records which sink retains and which does not.
- The rcplan diff's `dead-alias-struct-loop-body-moved-source-excluded` case is
  unchanged (its sink is an append) — its comment now says what the two
  divergences are waiting for.

Remaining matrix leaks: str and str_arr (the strfldok admission axis),
`enum__param`, `enum_arr__local` / `param` / `fieldread`, `struct_arr__local` /
`param`.
