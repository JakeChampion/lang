# The counted tuple struct-field store — and the false green that hid under it

`tuple_mixed__structfield__local_store` flips `clean leak` → `clean clean`. The
shape is `var k: (i32, i32[]) = …; var h = Hold { t: k, n: i };` — a tuple local
stored into a direct tuple field, both in one body.

## Three halves, one predicate

The store is now COUNTED, and all three halves read `struct_has_deep_tuple_field`
so none can widen without the others (#7253):

| half | site | what it does |
| --- | --- | --- |
| retain | `lower_expr_struct_lit`'s tuple arm | incs a non-literal field value; a fresh `ExprTuple` is handed over uncounted |
| release | `emit_struct_tuple_field_drops` | walks the tuple's rc children under `__fern_rc_is_unique`, then decs the box — **once per owner, outside the gate** |
| credit | `rctuple_counted_field_share` → `body_unsafe_for_alias_ret_counted` | the store stops reading as an escape, so the SOURCE keeps its own deep free |

Arithmetic for the instrument: rc 1 → store inc 2 → `k`'s deep free 1 → the
holder's field drop 0.

`struct_routes_field_reclaim_at` gained the tuple clause, which is what made the
whole thing reachable: it is the shared verdict the construction retain
(`slit_reclaim`) and `emit_struct_field_drops` both consult, so one clause turns
on both sides at once. The drop is IR-level for the same reason the enum-payload
sibling is: **no `k_*` arm of the asm `__struct_drop_<sty>` ladder matches a
`"(…)"` type**, so nothing below the IR would ever release the field.

## The false green

The release half landed first, alone. The matrix row read `clean clean`, both
oracles agreed on the exit, the sanitizer said nothing, and `allocs == frees` at
`live_bytes 0` — on the two-holder shape as well as the one-holder shape.

All of that was wrong. With no retain the store is a MOVE, so two holders share
one box at rc 1. Reading the emitted asm is what caught it:

```
__fern_rc_is_unique / arr_dec / arr_dec / __struct_drop_Hold / arr_dec   <- h1
__fern_rc_is_unique / arr_dec / arr_dec / __struct_drop_Hold / arr_dec   <- h2
```

Two full drop sequences, **no `rc_inc` anywhere**. `h1` finds rc 1, walks the
children and frees the box; `h2` then calls `__fern_rc_is_unique` on a header
`h1` already returned to the freelist. The totals levelled only because that
read happened to return false, so `h2` skipped a walk it had no right to skip.
A different freelist state frees the payload twice.

Two lessons worth keeping:

- **`allocs == frees` plus a silent sanitizer is not a proof of counting.** It is
  consistent with a use-after-free whose garbage read lands the convenient way.
  When a change is supposed to introduce a count, confirm the `rc_inc` is
  actually emitted.
- **`__fern_rc_is_unique` is a last-owner test, not a null-or-shared guard.**
  Gating the box dec on it means every owner of a shared box declines to dec and
  the box is pinned forever; only the CHILD WALK belongs inside the gate. This is
  what the `k_enum` arm does — payload walk gated, box dec not — and the tuple
  emitter now matches it.

## What stays refused

- A `{ ...base }` update: the copied fields carry no inc.
- A MOVED share (`moved_ident_at`). `return Hold { t: k, … }` is that shape — the
  return is `k`'s last use, so the retain is elided and the box takes over the
  local's reference. Crediting the source there frees a box whose ownership left
  the frame; for the array sibling that exact case segfaulted gen2 on the arm64
  stage-2 fixpoint while every x86-64 gate was green.
- A struct literal nested in a CONTAINER (`[Hold { t: k, … }, …]`).
  `rctuple_counted_field_share` matches a literal in a value position only, so
  the source keeps its escape verdict and the tuple leaks. Conservative; pinned
  at 300 frees by `array_of_holders_stays_refused` so a silent widening moves a
  number rather than only a verdict.

## Measurements

Six probes, exits confirmed on both oracles (`bin/fern -interp` and native
x86-64) before ever running the self-host:

| probe | drop only | + retain | + credit (landed) |
| --- | --- | --- | --- |
| `local_store` | 300/300 (false green) | 300/100 | 300/300 |
| `two_holders` | 400/400 (false green, UAF) | 400/200 | 400/400 |
| `read_after` | 300/300 | 300/100 | 300/300 |
| `fresh_literal` | 300/300 | 300/300 | 300/300 |
| `struct_escapes` | 300/300 | 300/300 | 300/300 |
| `two_structs_arr` | 500/500 | 500/300 | 500/300 (refused) |

The middle column is the honest leak the retain introduces on its own — the box
pinned at the shared count with nobody crediting the source. It is what the
credit half exists to settle, and it is the state a knockout of
`rctuple_counted_field_share` returns to.

Exactly one matrix cell moved, on both x86-64 and arm64.

## Knockouts

Each half removed in turn, instrument row read off `FERN_LEAK_MATRIX_DUMP=1`
with the EXITS, not just the verdicts — leg C is why the exits matter:

| knocked out | row | exit (native / self-host) |
| --- | --- | --- |
| the `rctuple_esc_expr` struct-lit forgiveness | `clean leak` | 23 / 23 |
| the credit-gate forgiveness (`rctuple_counted_store_sites`) | `clean leak` | 23 / 23 |
| the construction retain | `clean clean` | **23 / 99** |

Both forgiveness halves are load-bearing: the payload scan
(`rctuple_payload_escapes_alias_ret_ok` → `rctuple_esc_expr`) and the name scan
(`body_unsafe_for_alias_ret_counted`) each refuse the store independently, so
the credit needs both to say yes.

Leg C is the co-extensive discipline paying out. With the credit granted and the
retain gone, the source frees a box it does not own a count in and
`__rc_underflow_count()` fires — 99. The verdict column still reads `clean`,
because a balanced-looking census is exactly what an over-release produces. Read
the exit.
