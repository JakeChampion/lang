# TUPELEM: gets a field

#7253 step 2, slice 2 — the second un-multiplex, after the CLO family opened
the door. Byte-identical asm on a probe with two tuple locals carrying
retained bare-ident and fresh-literal elements across sibling scopes.

The `"TUPELEM:"` rows — `"<slot>|<kinds>"`, the per-element rc kinds
`bind_var_slot` records so the exit release decs exactly the positions the
construction retain inc'd (#7226) — move from `reclaimable_names` to a
dedicated `tup_elem_kinds: string[]` field. Two accessors, two constructors,
nothing else consumed the prefix. The family was already SLOT-keyed (it is
the one whose doc explains why a name key hands one sibling block's kinds to
the other), so unlike CLO it carries no follow-up: this family is done —
keyed correctly AND un-multiplexed.

`"TUPELEMOK:"` deliberately stays: despite the name it is a different family
(the escape-forgiveness credit `reclaimable_names_of` emits into its fn-level
OUTPUT, filtered at 17838/32453), produced before LowerState exists — moving
it is a collector-shape change, not this mechanical slice.

Gates: the tuple suites plus the leak matrix (162 s, 0 failures), the asm
diff, gen1 build; fixpoint to CI.
