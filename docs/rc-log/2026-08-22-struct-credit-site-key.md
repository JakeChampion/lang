# The struct reclaim credit, keyed on the binding

#7253 step 1 for the bare-name struct class — the fourth family through, after
the tuple one (#7272), `"STR:"` (#7292) and `"SARR:"` / `"ARRARR:"` (#7253,
#7335). This is the class the note in `reclaim_slot_name` singles out as the one
not to touch without a narrower gate, and the only one whose collision is a
**use-after-free** rather than a leak or a counted double free.

## What it was

```fern
if (i % 2 == 0) { var v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
if (i % 2 == 1) { var v: P = base;                            t = t + v.xs.len(); }
```

| shape | interp / native | before | after |
| --- | --- | --- | --- |
| the two sibling `if` arms | 34, `102/102/0` | **99** `204/204/0` | 34 `204/200` |
| the same pair through a LOOP | 68, `202/202/0` | **99** `404/404/0` | 68 `404/400` |
| the rename control (`u` for the second) | 34 | 34 `204/200` | unchanged |

`slot_is_reclaimable_struct` resolved its credit with a bare-name
`index_of_str(reclaimable_names, slot_name)` — the one class whose entries carry
no `"TAG:"` prefix at all. The fresh literal earns the credit, the alias inherits
it, and the release frees the CALLER's box under a live parameter.

**The byte census is useless here, as for every over-release**: `allocs == frees`
at `live_bytes == 0` on both sides of the bug. Only `__rc_underflow()` dissents —
and it dissents on all three backends, which is why the wasm and arm64 legs of
the new suite fail at base too, unlike a leak fix.

## Why this class and not the others first

Probing it during #7343 is what found it. The producer-local widening #7343 asks
for takes the same `sibling` program from a leak to a **SIGSEGV** — not exit 99,
an actual fault, because the freed box is then read. So the ordering rule
#7335's entry states ("site-keying is a prerequisite for widening, not a parallel
cleanup") holds here with the severity one notch up, and the widening is
deliberately NOT in this change. `producer_local_still_refused` pins that: it
keeps its 12000-byte leak, and it is #7343's fails-before case.

## The set must not move otherwise, and one reader nearly did

A key migration has two failure modes that look nothing alike. The one being
fixed is loud in the underflow counter. The one it introduces — a binding whose
slot carries no site key, so it resolves *no* credit — is silent there and shows
only as a leak.

Two producers, in two different functions, and the second is the one a
caller-based audit misses. `reclaimable_names_of` publishes `fresh[i]` (from
`collect_fresh_struct_names` + `collect_fresh_ret_call_names`); `lower_func`
separately appends `snapshot_local_names_of`'s output as plain names, with its
own comment saying it does so *"so `slot_is_reclaimable_struct` fires"*. Same
list, same lookup, different file position. #7292's rule again: enumerate
WRITERS of the list, and a writer need not be a caller of the thing you change.

**The derived markers move with the credit, and forgetting their readers cost
every block-scoped struct local its deep drop.** `"NODEEP:"` and `"FLDCHECKED:"`
are built from the same entries, so re-keying the credit re-keyed them — while
their two readers still resolved a NAME. Measured with that half missing:

```
block_scoped   before 400/400 live=0      after 400/100 live=7200
```

Every exit code stayed correct. The suite's `block_scoped` row exists for exactly
this, because the deep drop for a block-scoped slot is gated on the
`"FLDCHECKED:"` witness and nothing else in the tree would have reported its
loss.

## The retirement refusal had to be written out

`slot_is_reclaimable_struct` used an EXACT `slot_name` compare, so
`retire_locals`' block-exit rename hid a block-scoped slot from it for free — and
`slot_is_reclaimable_struct_scoped` existed to reach those slots at two chosen
sites. A site key is carried on the SLOT and survives that rename, so resolving
it alone would have silently granted the strict predicate the scoped one's set:
fourteen consumers, including the loop-rebind stores, the reuse donor and
recipient releases and the precise drop. That is not a re-key — it is the flip
`reclaim_slot_name`'s class note records as segfaulting the gen1 self-compile.

So the refusal is now explicit (`slot_name_retired`), the two predicates share
one lookup, and what used to be the difference between them (an extra
prefix-stripping step) is now the difference between refusing a retired slot and
not.

## Eight call sites the type checker found

`emit_cross_struct_reuse` and `emit_self_overwrite_reuse` bind their recipient
with their own `add_local`, not through `bind_var_slot` — the same gap #7272 hit
on the tuple sibling — so each takes the site key as a parameter now. Adding the
parameter rather than deriving it inside is what made this safe: it turned every
call site into a compile error, and there were **eight**, not the three a grep of
the neighbourhood had found. A self-assign update passes `""` and keeps the
existing slot's own site.

The count is worth stating exactly, because a first pass of this entry said
seven — from reading a grep rather than counting one. The compiler found all
eight either way, which is the point: the technique does not depend on the
author's count being right.

## Non-vacuity

`internal/e2eselfhost/self_host_struct_credit_site_key_test.go`, 7 cases x 3
backends. With `irlower.fern` reverted and the drivers rebuilt, `collide_literal`
and `collide_loop` fail on **all three**:

```
collide_literal exited 99, want 34 (99 = rc underflow: a same-named struct
local inherited another binding's reclaim credit)
collide_loop exited 99, want 68
struct credit site-key wasm IR "collide_literal" = 99, want 34
```

The other five pass either way and are the controls that matter: `block_scoped`
and `fn_scoped` pin the two predicates keeping their own answers,
`collide_literal_renamed` is the pairwise witness (the fixed program measures
identically to the one that never collided, which is checkable without deciding
whether its residual 4 blocks are right), and `producer_local_still_refused` pins
that nothing was widened.

Those 4 blocks are worth attributing rather than waving at, because a first pass
of this entry filed them under #7282's alias shape and they are not that. They
are FLAT — 120 bytes at 100, 200 and 400 rounds alike — where #7282 is 80 B a
round, exactly doubling. Widening `b`'s array to 8 elements moves them to 168, so
they scale with `b` and not with the loop: they are `main`'s own `b`,
escape-flagged into the call argument and never released at exit. A residue that
does not move with the round count cannot be a per-round leak, and checking that
is one `sed` on the bound.
