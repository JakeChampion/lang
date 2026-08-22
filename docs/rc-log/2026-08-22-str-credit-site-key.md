# The string credit, keyed on the binding

#7292, closed by way of #7253's step 1 for the `"STR:"` family — after a first
attempt that fixed the leak and introduced an over-release.

| shape (100 rounds, x86-64) | native | before | after |
| --- | --- | --- | --- |
| `{ var s = w("ab"); … }` — a plain block | `live=0` | `200/0` **3200** | `200/200` **0** |
| the same in a `while` body | `live=0` | `600/400` **3200** | `600/600` **0** |
| the same in an `if` arm | `live=0` | **3200** | **0** |
| the same at FUNCTION scope | `live=0` | **0** | unchanged |

## The issue is wrong about the shape

#7292 reports the loop form and reads it as n-1-of-n. A plain `{ }` block with no
loop measures `frees=0` — **nothing** is released, ever. The loop only looked like
an off-by-one because its rebind store frees each superseded box on the way
through, leaving the final one as the lone survivor: two mechanisms, one working.

## The first attempt, and why it was wrong

The obvious fix is the one the issue proposes and that nine other classes already
use (#6285): resolve the credit through `reclaim_slot_name`, which strips
`retire_locals`' `"!retired!"` prefix. That closes every row above.

It also **over-releases**, at exit 99 on all three backends, on
`str-bind-sfrrecv-same-name-alias-liveness` — whose own comment names the hazard:

> The credit is keyed by NAME, so a second `var v` in another block shares it
> while holding a plain alias — here a struct FIELD, which no slot compare can name.

```fern
if (base.len() > 0) { var v: str = base.tail(2); … }   // earns the credit
if (base.len() > 0) { var v: str = h.name;      … }   // a bare alias
```

**The exact-match miss was accidentally shielding that collision.** Making the
lookup retirement-aware fixes the first bug and exposes the second — the two are
the same defect (a scopeless key) seen from opposite ends, so no lookup change
alone can be correct.

That first version was withdrawn from its PR rather than shipped behind a
narrower gate. Trading a bounded leak for a freed-live-field is the wrong
direction, and gating around a broken key is not a fix.

## What actually works

Key the credit on the BINDING SITE (`name@line:col`), the mechanism #7298 built
for the tuple classes. Both problems go away at once, for one reason each:

- the key lives **on the slot** (`LocalInfo.reclaim_site`), so the block-exit
  rename cannot hide it — no retirement-aware lookup is needed, and the scoped
  variant this started as is unnecessary;
- two same-named bindings resolve to their **own** credit, so the aliasing `v`
  never inherits its sibling's.

Scope: 11 collectors, 11 gate loops, ONE credit read, plus `"SFRCAND:"` (which
`bind_var_slot` promotes to `"STR:"`, so it moves with the family) and
entry-zeroing in `arr_slots_of`.

## The audit that made it safe

Each of the 11 collectors was checked for external callers before being changed.
**Every one has exactly one** — its gate loop in `reclaimable_names_of`; the rest
of the call sites are the collector's own recursive arms. Nothing else consumes
those lists, so nothing else was expecting bare names.

**The audit was not sufficient, and the gap is worth naming.** Asking "who calls
this collector?" found eleven answers and missed a TWELFTH producer:
`collect_str_fresh_ret_call_names` has no gate loop of its own — it appends
directly into `strs`, one line after `collect_fresh_string_names` fills it. So
`strs` became a MIX of site keys and bare names, every mixed entry silently lost
its credit, and four suites went to leak (`TupleStrElemRetain`,
`StrSourceMethodReceiver` on all three backends). Caught by the broad sweep, not
by the targeted ones.

The right question was not "who calls each collector" but "what can append to a
list this credit reads". A key migration has to enumerate WRITERS of the list,
and a writer need not be a caller of the thing you are changing.

Two further details the audit did turn up, which a blind substitution would have
got wrong:

- `collect_str_fresh_ret_call_names` and `collect_fresh_str_ret_call_names` are
  DIFFERENT functions with near-identical names. Only the second feeds this
  credit.
- The cross-list dedupes (`index_of_str(strs, lits[i])`) compare one collector's
  output against another's, so they must stay key-to-key — and they get *sharper*
  for it: two same-named bindings no longer collapse into one another. Only the
  comparisons against `reassigned`, and the escape gates, take bare names.

## Entry-zeroing was not already there

`slot_is_reclaimable_struct_scoped`'s header states the rule: a newly-sweepable
class must also be zeroed at entry by `arr_slots_of`, "without which the sweep
would dec stack garbage in a function whose block never ran".

Strings had none. `str_slots_of` collects every `is_str` slot and looks exactly
like that set; its own comment says *"Nothing on the emit path reads it"* — it is
diagnostic, consumed only by `irlower_run.fern`'s dump. Assuming it was the
zeroing set would have shipped a crash rather than a leak. `untaken_branch` in
the suite is that case.

## A pinned leak closed on the way

`TestSelfHostLoopVarReclaim`'s `string-concat-temps-still-leak` (#6606's string
half — "1000 allocs / 798 frees, 202 boxes stranded and growing") now reports
**reclaimed**. Renamed to `string-concat-temps-reclaimed`, want 3 → 7.

That row has now been renamed three times as its status moved, and its own
comment already says why that matters: a name asserting a bug persists becomes a
lie the moment the bug is fixed, and the test then fails for the right reason
while reading as a regression.
