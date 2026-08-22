# The tuple class credits, keyed on the binding

#7272, and the first application of #7253's step 1 to a real fact family.

Two same-named tuple locals in sibling blocks, one carrying a bare-ident rc
element and the other an array literal:

| 100 rounds, x86-64 | answer | leakcheck |
| --- | --- | --- |
| native | 10 | `400/400`, `live=0` |
| interp | 10 | — |
| self-host, before | **99** (rc underflow) | `400/400`, `live=0` |
| self-host, after | 10 | `400/400`, `live=0` |

**The byte count is identical in all four rows.** A doubly-released block goes
straight back to the freelist, so `live_bytes` reads 0 either way and the
arithmetic balances. Only `__rc_underflow()` dissents. This is the trap #7271's
entry records, in the form where it costs the most: nothing else in the toolchain
would have noticed.

## The isolation was a one-word diff

The issue filed the mechanism as suspected and asked for the class-attribution
step to be confirmed. What confirmed it: renaming the second block's local from
`t` to `u`, changing nothing else, makes the same program correct.

| probe | the two locals | self-host |
| --- | --- | --- |
| repro | `t`, `t` — different classes | **99** |
| same class, ident element both | `t`, `t` | 40 |
| same class, array literal both | `t`, `t` | 27 |
| **the rename** | **`t`, `u` — different classes** | **10** |

The alloc counts are identical across all four, so the rename changes neither
what is allocated nor which credits are granted — only which slot each credit
resolves to. That rules out the class *combination* (the rename keeps it) and the
block nesting (identical in both).

A second, independent confirmation before the fix: guarding the exit sweep's
rctuple loop against a slot that is also `slot_is_reclaimable_tuple` turned 99
into 10 — and leaked 4000 bytes doing it. Both classes really are landing on both
slots, and the box is dec'd twice. That guard is *not* the fix; it trades an
over-release for a leak, which is the special case shielding a symptom rather
than the cause.

## What changed

`collect_fresh_scalar_tuple_names`, `collect_fresh_rc_tuple_names`,
`collect_sweepable_rc_tuple_names` and `collect_fresh_tuple_ret_call_names` emit
`name@line:col` — a `StmtVar` carries both — and `bind_var_slot` records the same
key on the slot. The four tuple predicates read it. The credit loops split the
bare name back out for the escape gates, which all ask about the variable.

Only the tuple family moves. `reclaim_slot_name` keeps its meaning for the other
~70 namespaces multiplexed into `reclaimable_names`; #7253 is the issue for the
rest, and its prescribed discipline is one family per step.

A bonus that falls out: the site key survives `retire_locals`' block-exit rename,
which the name does not — the same property that made #7271 re-key the
element-kinds registry on the slot.

## The regression this caused first, and why it is the interesting part

The first version fixed #7272 and broke **every** cross-tuple reuse shape #7275
had just closed — the whole corpus went from `live=0` to leaking, 4000–12000
bytes a probe.

`emit_cross_tuple_reuse` binds its recipient with its own `add_local`, not
through `bind_var_slot`. That slot therefore had **no** site key, and a siteless
slot resolves no credit at all: not the wrong class, *none*. Fourteen further
`StmtVar` binding paths in `lower_stmt_var` had the same gap.

The lesson is narrower than "re-keying is risky". A key migration has two failure
modes and they look nothing alike: the one you are fixing (two bindings sharing a
key) is loud in the underflow counter, and the one you introduce (a binding with
no key) is silent there and shows only as a leak. **A change to a lookup key has
to enumerate every producer of that key, not every consumer** — the consumers all
went on compiling.

The reuse recipient takes the key threaded in beside `c_type`; the other fourteen
record it at their own `add_local`.

## Non-vacuity

Six cases in `internal/e2eselfhost/self_host_tuple_class_slot_key_test.go`, on
three backends. Two are controls that were already correct and must stay so:
same-class sibling blocks, and the distinct-names program that isolated the
cause. They are what a "fix" that worked by *denying* the classes would fail —
which is exactly what the first version did.

Both orderings are pinned. `tagged_value_of` returns the FIRST match, so which
block comes first decides which credit a name resolves to; a fix that only
reordered the lookup would pass one and fail the other.
