# The Option/Result struct payload takes the construction retain

`Some(p)` / `Ok(p)`, where `p` is a struct LOCAL, stored the box into the option
with no retain. The container-sink grid recorded that as a leak. It was a live
use-after-free.

## The reproducer

```fern
struct P { xs: i32[], k: i32 }

function some_of(i: i32): Option[P] {
    var p: P = P { xs: [i, i + 1], k: i };
    var o: Option[P] = Some(p);
    return o;                       // p's exit sweep frees the box this points at
}

function clobber(i: i32): i32 {
    var q: P = P { xs: [9999, 9999], k: 9999 };
    return q.k + q.xs[0];           // reuses the freed cell
}
```

Reading `p2.k` back out of the returned option gives 9999. 100 of 100 rounds
disagreed with native. `Ok(p)` behaves identically.

The user variant ctor `E.A(p)` on the same shape returns the RIGHT answer and
leaks — its source is refused a credit rather than dangling. So the grid's
`variant__*` and `option__*` cells both read `leak`, and only one of them was
sound. **A `leak` verdict says nothing about correctness**: the grid measures
census, and a census cannot tell a leak from a free that happened too early.
Exit codes are what separated them, which is why every cell compares those too.

## The fix is the missing retain, not a refused credit

`lower_opt_make_payload` already emitted the Perceus alias-inc — for
`slot_is_rc_container` slots only: array, string, tuple. That predicate is the
whole bug. A struct box is as rc-tracked as any of them; it was simply not in the
list. This is #2649 one payload kind over, in the same function, and the guard for
it now sits beside #2649's own in `self_host_match_payload_rc_test.go`.

Refusing `p` its credit (the posture `struct_box_sink_stored_expr` takes for the
variant ctor) also stops the UAF, and was the first thing tried. It is the wrong
fix here: native retains at this site, so refusing would have moved the self-host
further from the thing it is being ported to in order to buy a leak.

With the retain the option is a counted co-owner, so the rest follows the
protocol #7503 established:

- `optstruct_init_is_fresh` admits a bare-ident payload — the retain balances the
  release whether or not the source stays live, so no move analysis is consulted.
  (A move-based admission was tried first and could not work: neither native nor
  the self-host records a construction move at a builtin union ctor, so `msites`
  is empty at that site by construction.)
- `emit_optstruct_deep_free` walks the payload's fields under
  `__fern_rc_is_unique`, and `struct_counted_share_expr` marks the source
  `SINKSHARE:` so its own sweep does too. Two static walks free every buffer twice
  — allocs == frees at live_bytes 0, with only `__rc_underflow_count()`
  dissenting.

Both `option_stmt` cells go clean, moved and live together. The counted-share
half comes for free here, where the tuple and variant slices could only do the
moved half: those positions store no counted reference at all.

## Result was the same shape one spelling over, and measured differently

`Result[P, E]` reached none of it. `optstruct_ann_is` and every consumption site
read the payload with `opttup_inner_type`, which accepts `Option[` and nothing
else, so a `Result` local fell out before any credit was consulted. Switching
them to `opt_payload_type(t, "Some")` — which already read both spellings —
closes it, and `result_stmt__moved` / `__live` are pinned separately so the two
cannot drift apart again unnoticed. That is the second time this grid has caught
one spelling of a shape working and another not (`variant_unqual`, #7526).

Localising it took four experiments on the compiler rather than reading, after
three earlier guesses measured identically to no edit. The instrument that worked
was diffing the emitted asm for the two spellings and counting release calls:
`__struct_drop_P` appeared 3x for Option and 1x for Result, which named the
consumption site directly.

## What this costs, and what it does not fix

Two probe shapes free LESS than before:

| shape | before | after |
|---|---|---|
| a returned `Some(p)` (`option_escape`) | 300/200 | 300/0 |
| `Some(p)` read via a match EXPRESSION (`option_moved`) | 300/200 | 300/0 |

Those 200 frees were the over-release — the same one the reproducer above turns
into a wrong answer. There is no version of this fix that keeps them.

The second row is a SEPARATE gap, and the reason `option__moved` and
`option__live` stay pinned as `leak`. `collect_fresh_optstruct_names` gates on
`sole_top_level_match_idx`, which scans for a `StmtMatch`; a match EXPRESSION
inside a `return` is a `StmtReturn`, so the Option credit is refused outright and
NOTHING is released — `option_fresh` measures 300/0 with a fresh literal payload
and the machinery fully working. Unlike the enum family, which has the `RCENUMS:`
sweep credit for the no-consuming-match half, Option has only the consuming-match
credit.

That gap is wider than this one and needs a release PLACEMENT, not a predicate:
the free has to land after the match value is computed and before the return.
It is why the `option_stmt` / `result_stmt` cells exist — without them the grid
measures gap 2 masking this one, and no fix to the store could ever show up.

Worth asking of the rest of the grid: `variant__*` reads through a match
expression too and went clean in #7526, because `collect_fresh_rcenum_names` has
a non-consuming-match path Option lacks. Wherever a match appears in a cell, the
two axes are mixed.
