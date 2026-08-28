# The tuple struct-field row was an admission gate, not a missing pair

`tuple_mixed__structfield__local_store` flips **leak → clean** on both
architectures. The change is two lines. The previous entry
(`2026-08-28-tuple-callarg-instruments.md`) scoped this row as a
co-extensive retain + deep-drop pair across five files and three backends;
that model was wrong, and the way it was wrong is the useful part.

## What the previous entry claimed

> `lower_expr_struct_lit`'s `fav_alias_inc` fires for arrays, nested
> structs, enums and strings and has no tuple arm at all, and
> `emit_ir_struct_drop_one` has no `k_tuple`. So today's self-host is
> internally consistent in the LEAKING direction: no inc, no dec, the
> tuple leaks with the struct, sound.

Both halves of that are true as statements about those two functions. The
conclusion drawn from them is not.

## What is actually there

`fav_ok` is a **bail** gate, not an enabler: the block ends with
`if (!fav_ok) { return s.fail(); }`. And the block is entered only when the
field type is array-shaped. A tuple field type never passed that outer
test, so it never entered — it got neither the validation nor the reclaim
routing, and fell through to the generic field store. That is the leak.

Admitting the tuple field type to the block is sufficient. The drop it
needs already exists; nothing about `emit_ir_struct_drop_one` had to
change, and no backend transcription was written.

## Why it balances with no retain

A tuple reaching a struct literal is an escape, so `rctuple_esc_expr`
refuses the local its `"TUPRCS:"` exit-sweep credit
(`slot_is_reclaimable_rctuple_sweep`). The local is therefore never swept,
and the struct's field drop is the only free. One owner, one release.

`fav_alias_inc` was tried and left out. It gates a move analysis —
`moves_local_at` → `note_moved_elided`, "the box takes over the local's
reference" — and on both the move shape (`Hold { t: k }` as a last use) and
the alias shape (`k.1[1]` read after the store) the emitted binary is
byte-identical with and without it. A line that cannot be shown to do
anything is not shipped.

## Measurement

| | x86-64 | arm64 |
|---|---|---|
| `tuple_mixed__structfield__local_store` before | clean / leak, 23 = 23 | clean / leak, 23 = 23 |
| after | clean / clean, 23 = 23 | clean / clean, 23 = 23 |
| `tuple_mixed__callarg__read` (guard row) | clean / clean | clean / clean |
| `tuple_mixed__callarg__stored_struct` | clean / leak (unchanged) | clean / leak (unchanged) |

All 134 arm64 matrix cells: zero errors, zero failures. Stage-2 fixpoint
green.

`callarg__stored_struct` is untouched, as expected — it is the
interprocedural row, and its credit gate is steps 2-3 of the wave. Those
steps are unaffected by this entry except that step 1, as previously
scoped, no longer exists.

## The instrument trap this sat on

`bin/fern` is the **native Go** compiler. It does not consume
`examples/self_host/*.fern`, so it is unchanged by any edit to the
self-host sources. Probes run through it measure the wrong compiler and
agree with each other for that reason — including a byte-comparison that
read as "this arm never fires" when it was comparing a binary with itself.
Measure a self-host change through the leak matrix, or through a compiler
built from the modified sources. Nothing else counts.
