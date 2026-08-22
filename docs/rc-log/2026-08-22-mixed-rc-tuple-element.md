# The mixed rc tuple, released — one bare ident, one fresh literal

#7281. `var t: (i32[], i32[]) = (xs, [i + 2, i + 3])` released **nothing**: not
either buffer, not the box.

| rounds | 100 | 200 | 400 |
| --- | --- | --- | --- |
| self-host, before | `300/0` **12000** | `600/0` **24000** | `1200/0` **48000** |
| after | `300/300` **0** | `600/600` **0** | `1200/1200` **0** |
| native / interp | `300/300` **0** | — | — |

Exactly 2.0x per doubling: 120 B/round, unbounded. `frees=0` throughout the
before row — not "the element strands", nothing at all is released. Answers
agreed with both oracles at every count and `__rc_underflow_count()` read 0, so
the byte count was the only dissent.

## The mix is the whole of it, and it fell between two working classes

Each half of the shape has a class that handles it, and the two are kept
disjoint, so the mixture landed in neither:

| shape | class | before |
| --- | --- | --- |
| `(xs, ys)` — both idents | `"TUP:"` + `"TUPELEM:"` (#7226) | balanced |
| `([..], [..])` — both fresh | `"TUPRCS:"` (#6127) | balanced |
| `(i, xs)` — scalar + ident | `"TUP:"` | balanced |
| **`(xs, [..])`** | **neither** | **12000** |

`tuple_lit_rc_reclaimable` admits a bare-ident element, so the tuple is
`"TUPRC:"` and therefore out of `"TUP:"`. But `"TUPRC:"` is consumed only by the
StmtVar rebind path; the scope-exit sweep needs `"TUPRCS:"`, and that credit
required `tuple_arg_payload_fresh` — EVERY rc position a fresh literal. Position
0 is a live local, so no credit, no release site, and `xs`'s own sweep could not
finish the job either: the construction retain left its buffer at rc 2 and one
dec cannot reach zero.

Making either position rc-free fixes it, which is what says the mix is the cause
rather than any one element form.

## The gate was asking for more than the drop needs

`emit_tuple_type_child_drops` dec's every rc-typed position blind, and
`tuple_arg_payload_fresh` was the proof that made blind sound. Its stated
question is **sole ownership**. The question the drop actually needs is weaker:
does the tuple hold a **counted reference** to the position?

A bare ident does. `lower_expr`'s ExprTuple arm rc_inc's an element naming an
rc-container local — unconditionally, from `slot_is_rc_container`, since #4350 —
so the tuple is a second owner and the drop's dec gives exactly that retain back
while the local's own sweep still spends its own. For a leak-safe array position
the two answers are even the same instruction: `__fern_rc_dec` either way, taking
rc 1 → 0 on a fresh literal and 2 → 1 on a retained ident.

So the predicate splits in two rather than moving: `tuple_arg_payload_fresh`
(sole-owner) stays the gate for an Option's `Some` payload and an
array-of-tuples element, where the payload is freed **without** the tuple's own
box in the same breath; `tuple_arg_payload_retained` (counted-reference) is the
gate for the scope-exit sweep, which frees both together. One core with a flag,
threaded through the nested-tuple recursion so an inner mix is admitted with the
outer one.

## Three sites owed the same give-back, and the issue names one

The sweep is the one #7281 measures. Auditing the release for it found the other
two had the identical stale skip, in `emit_tuple_child_drops`'s bare-ident arm —
*"a bare-ident pointer aliases a live local the site never inc'd"*, which stopped
being true when the construction retain went in:

| site | shape | before | after |
| --- | --- | --- | --- |
| scope-exit sweep | `var t: (i32[], i32[]) = (xs, [..])` | `300/0` 12000 | `300/300` 0 |
| rebind store | `t = (xs, [..])` in a loop | `900/600` 12000 | `900/900` 0 |
| discarded literal | `(xs, [..]);` as a statement | `300/200` 4000 | `300/300` 0 |

The bare-ident arm now routes through `tuple_union_arg_freefn`, which is the
resolver the union-payload site already used for this exact question and already
carried the two refusals that matter: a `string[]` element (a flat dec would free
the buffer and strand every element box) and a string local with no `"STR:"`
credit (one dec cannot reach zero without the local's own sweep) both answer
`""`. Those stay on the leak side rather than the over-release side, and they do
so in ONE place rather than in a second copy of the rule.

## What is still refused, and measured as still leaking

`var keep: i32[] = t.0` extracts an owned pointer element, so
`rctuple_payload_escapes` denies the credit and the shape keeps its 12000 with
the underflow counter at 0. Pinned by `elem_escapes_still_leaks`, asserted on
the exit code alone: the point of the row is that it must not start
OVER-releasing while it waits, which is the direction a careless widening of the
escape gate takes it.

## The over-release this could have been, and why it is not

The widening grants a credit that was previously denied, and #7335's entry the
same day records what that costs on a name-keyed class: the credit is inherited
by a same-named aliasing sibling in another block, which then frees a live
buffer — with `allocs == frees` at `live_bytes == 0` throughout, because a
doubly-released block goes straight back to the freelist. Only
`__rc_underflow_count()` dissents.

The tuple family has been site-keyed since #7272, so the prerequisite was already
in place. `sibling_alias` pins it: two `v` in sibling `if` arms, one mixed and one
all-ident, hence two different classes under one name. It measures 28 with
`151/151`, the same as the program that never collided.

## A NATIVE leak found alongside

`discarded_literal` is the one row where the self-host now runs ahead of its
oracle. Native measures `allocs=300 frees=200 live_bytes=3200` on
`(xs, [i+2, i+3]);` — 32 B/round, the discarded tuple literal's box — against
`300/300` here. Answers agree, so only the census sees it — **#7345**, which
inverts the usual convergence direction. The attribution is a one-element diff:
growing `xs` moves the leak 3200 → 4800, growing the fresh literal does not, and
`([..], [..]);` with no ident is clean, so the stranded block is the bare-ident
element's buffer. This entry records it because the test's `want` is taken from
native's exit code and its byte balance deliberately is not.

## Non-vacuity

Nine of the fourteen cases in
`internal/e2eselfhost/self_host_tuple_mixed_rc_elem_test.go` fail with the change
reverted and the compiler rebuilt — the leak signature, not an answer change:

```
--- FAIL: TestSelfHostTupleMixedRcElemX86_64/mixed_sweep
    mixed_sweep: leakcheck: allocs=300 frees=0 live_bytes=12000 — must balance
    at live_bytes 0. A mixed rc tuple in neither class releases nothing at all,
    box included
```

Four of the remaining five are controls that were already correct and must stay
so — `all_ident`, `all_fresh`, `scalar_ident`, and `dup_ident`, the same ident at
both positions, which takes two retains and so needs two decs. They are what a
"fix" that worked by pulling every shape into one class would move.

**Only the x86-64 leg carries the leak signal.** The wasm and arm64 legs assert
exit codes, which a leak does not move; what they catch is a new release that
frees a LIVE box on those backends, which does change the answer.
