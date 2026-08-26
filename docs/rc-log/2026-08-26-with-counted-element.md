# `with` closes the last store-side cell in the container-sink grid

`with__moved` and `with__live` are clean. Every remaining `leak` in the grid —
`option__moved` and `option__live` — is the match-EXPRESSION placement gap from
#7528, not a store problem. The store-side sweep that began with #7253 is done.

## Two halves, and only one of them was familiar

The four slices before this one (#7503, #7528, #7531, #7532) were all the same
shape: a missing construction retain. `with` needed that too, but it also needed
something none of them did.

**Half 1 — `with` was not a sanctioned rebind.** `arrstruct_self_append_elem`
matched `fa.field == "append"` and nothing else, so `out = out.with(0, p)` read as
an ordinary reassignment, and the literal-built credit refuses a reassigned name
outright. The container therefore earned no element walk at all: four of the five
allocations per round leaked, not one. Widening that predicate is a single point —
`stmt_arrstruct_unsafe_for` (the credit gate) and `arrstruct_owned_elem_sites`
(the stamping pass) both consult it — so one change sanctions the rebind
everywhere. It is now `arrstruct_self_store_elem`, with
`arrstruct_self_store_elem_index` naming which operand is the element (0 for
append, 1 for with) so the gate, the stamps and the lowering cannot disagree.

**Half 2 — `with` REPLACES, so the superseded element must be released.**
`append` displaces nothing; `with` overwrites, and the bare `arr_set` drops the
old element pointer on the floor, after which the exit sweep's walk never sees it.
`lower_strarr_with_store` already solved exactly this for `string[]`, so
`emit_arrstruct_with_store` is its sibling: retain the stored element (move-aware,
`APOWNED:`-stamped, as the self-append arm does), release the old one guarded on
`old != 0 && old != new`, then store. The release differs in shape — a struct
element's is a FIELD WALK plus a box dec, not one free helper — and the walk is
rc-gated for the reason every walk in this family is.

## What the underflow guard caught

With both halves in and the source's credit lifted, `with_arg` measured **500 of
500 freed and exit 99**. A perfect census, and wrong: `struct_counted_share_expr`
recognised `append` but not `with`, so the source's sweep walked its fields
STATICALLY while the container's walk was gated, and both freed every buffer.

That is the third time in this run the same omission has appeared (#7531 and
#7532 were the others). The pattern is worth stating plainly: **whenever a store
becomes a counted share, two edits are needed, not one** — lift the sink refusal
so the source keeps its credit, AND mark it `SINKSHARE:` so its sweep is gated.
Doing only the first is not a partial fix; it is an over-release, and
`allocs == frees` at `live_bytes 0` will not tell you.

## The hazards from the plan, resolved

- **The cow path.** An aliased or borrowed receiver routes to
  `lower_arr_with_value` ABOVE this, cloning into a sole-owned buffer and leaving
  the aliased one holding the old element. Releasing there would free a value the
  original array still reads. The new emitter sits after that check, so it never
  runs on that path — verified with `with_aliased`, which reads the aliased array
  back after the store: exit 0 on both compilers, no underflow, and its census is
  byte-identical before and after this change.
- **The rc plan.** Checked rather than assumed, after #7531 went green partly off
  a gap in `free_eligible_sites_of`. `rc_fe_walk_expr`'s `with` arm routes a
  non-`string` element type to `rc_fe_escape_owned` (counted), so a struct element
  was already on the correct side. No plan change.

## Measured

`with__moved` and `with__live` leak → clean; the `with_arg` probe 500/100 →
500/500 and `with_moved` likewise. Every other probe in the corpus unchanged,
every exit code matches native, nothing exits 99.

## What is left

`option__moved` / `option__live` only. `collect_fresh_optstruct_names` gates on
`sole_top_level_match_idx`, which scans for a `StmtMatch`; a match EXPRESSION
inside a `return` is a `StmtReturn`, so the credit is refused and NOTHING is
released — `option_fresh` measures 300/0 with a fresh literal payload and the
machinery fully working. Closing it needs the free PLACED after the match value is
computed and before the return, which is a different kind of change from the five
that came before it.
