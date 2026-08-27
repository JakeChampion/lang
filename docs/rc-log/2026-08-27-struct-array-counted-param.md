# The struct-array arm of the counted-param tier — the last `__param` cell

*2026-08-27* — `struct_arr__param`, the fifth and last of the construction-retain
`__param` cells. Follows `2026-08-27-enum-array-counted-param.md` (the enum arm)
and `2026-08-27-precise-drop-array-of-boxes.md` (#7610).

## One tier, two element kinds

The enum arm landed as its own key, `"DCNT:"`, on the reasoning that `ACNT:`'s
credit routes a plain `arr_dec` while an array of BOXES owes an element walk. The
same record then argued the point that makes this slice small:

> All four still merge into one `CNT:` flag string, which is correct: at a call
> argument the question is whether THIS position is counted, and what the caller
> then RELEASES is decided by its own local's type, not by which tier proved the
> store.

So the struct arm needed no fifth key. `DCNT:` admits both element kinds; the two
escape walkers differ only in which deep free they route —
`emit_arrenum_deep_free` or `emit_arrstruct_deep_free` — and the question they
ask the registry is identical.

The slice is therefore two edits:

- `param_counted_of`'s `DCNT:` candidacy gains a struct-array arm, its
  walk-exists guard asked with the predicate the RELEASE side asks:
  `struct_has_reclaim_array_field`, what `slot_is_reclaimable_arrstruct` checks
  before routing the deep free. (The enum arm asks `enum_arr_elems_walk_ok`,
  what the backends check before emitting `__enum_arr_elems_drop_<E>`.)
- `arrstruct_elem_esc_expr` gains the `"CNT:"` exemption its arrenum twin
  already had, beside the `"ELB:"` one.

The element-handout guard needed no code in either arm: `arrparam_use_ok` credits
`p[i]` only for `SCNT:`, so an element read disqualifies the param outright.

## Measured

x86-64, `FERN_LEAKCHECK=1`, local from `mkv(seed())`. Both oracles agreed on
every exit and neither was read off the self-host run.

| probe | native | interp | self-host before | after |
|---|---|---|---|---|
| `counted_store` — the cell | 6, 104/104 | 6 | 6, **104/102** | 6, **104/104 clean** |
| `callee_returns_param` | 3, 4/4 | 3 | 3, 4/2 | 3, 4/2 refused |
| `callee_extracts_element` | 9, 4/4 | 9 | 9, 4/2 | 9, 4/2 refused |
| `callee_stores_element` | 9, 104/104 | 9 | 9, 104/102 | 9, 104/102 refused |

Only the target moved; the three refusals are byte-identical before and after.

## Three pins moved, and one of them was a warning

**`struct_arr__param`** in the construction-retain matrix: `clean/clean` against a
`leak` pin. Flipped. **Matrix: 1 leaking cell of 35** — `str_arr__fieldread`,
alone.

**`struct_array_twin_still_leaks`** in the precise-drop suite, written one slice
earlier and pinned LEAKING with a guard that fails if the row starts balancing.
It fired exactly as intended. Renamed and flipped to `balance: true`; the guard
stays, because the reasoning applies to whatever is pinned next.

**`callee_stores_field`** in `self_host_arrstruct_borrowed_arg_test.go` — this one
carried a warning rather than a plain pin:

> balances, so the gate stopped refusing this shape. That is the box-flag
> weakening; check TestSelfHostStage2FixpointArm64 for gen2 segfaults

Checked before touching it, because a balance here has two possible causes and
only one of them is good. It was the good one:

- every handout shape in that same suite still refuses —
  `element_handed_out_in_struct`, `_bare`, `element_field_handed_out`,
  `element_appended_elsewhere` — and so do `callee_extracts_element` and
  `callee_returns_param`. Only the counted-STORE shape moved.
- `TestSelfHostStage2FixpointArm64` green, 157 s, no gen2 segfault.

The element question is intact; the shape is admitted by a different tier, not by
a weakened flag.

## A stale row the enum slice left behind

`callee_stores_field` in the ARRENUM borrowed-arg suite asserted only its exit
code, so when the enum arm landed it kept passing while its comment — "stays the
leak it was" — became false. Corrected, and given `balance: true` so the row now
pins the behaviour instead of only the exit, and cannot go stale silently again.
Its struct twin had the stronger pin, which is why that one spoke up and this one
did not.

## Gates

- per-module emit-all fixpoint — green, 397 s, 0 skips, foreground
- `TestSelfHostStage2FixpointArm64` — green, 157 s, 0 skips (the check the
  box-flag guard names)
- all four matrices — construction-retain (flipped), container-sink, both leak
  matrices — green, no other row moved
- every ArrEnum / ArrStruct / ArrTup suite, the counted-param release suite, and
  the precise-drop suite — green
- the new suite, 4 cases

Matrix: **1 leaking cell of 35** — `str_arr__fieldread`, the RewriteCtx
`string[]` field-read shape, which is a different class from the five `__param`
cells and is where this line of work goes next.
