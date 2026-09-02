# A state local consumed into another is released at that statement (#6644 distcheck, slice 7)

`2026-09-02-snapshot-locals-and-consume-safety-cycle.md` left parser.fern at
1.05 GB live with the grow buffers still 493 MB of it, and named the survivor:
`var sr: LowerState = lower_expr(b.right, sl0)` with `sl0` never mentioned
again. Nothing rebinds `sl0`, so no rebind release ever runs on it, and the
generation it holds — its box and the fields `sr` did not carry on — was
leaked at every statement of the kind. The compiler has 57 such sites in
`irlower` alone, most of them chains (`s41` … `s55` in `lower_call_named`).

## Why it was refused

Two admission rules kept `sl0` out of the snapshot-local set:

- the consume scan (`own_rebinds` true) counts `var sr: T = g(.., sl0, ..)` as
  an escape, because a later rebind of `sl0` could free a generation `sr`
  still names — sound, and irrelevant when there is no later mention;
- a local never reassigned had nothing to reclaim BY, since the rebind was the
  only release site.

## The rule

`last_use_consumer_in`: in the statement list that declares `name`, the
`var q2: T = g(.., name, ..)` / `var q2: T = name.m(…)` of name's own type
after which no statement of the list mentions name. Block scoping makes the
list sufficient — nothing outside it can read name — and a lambda mentioning
it is a mention. That site is a move-out for the consume scan, as a bare
`return name` is, and admits a never-rebound local on its own.

At the site (`release_last_use_source`) name's box is released the way a
rebind would release it, with `q2` standing in for the new value:
`__field_reclaim_T(q2, name, snap)` keeps a field q2 carries on and frees the
replaced ones and the box; a shared box gives up this slot's count. One arm
the rebind path has no use for: when g handed name's own box back, q2 holds
its own count on it (the call result is counted) and name's is released.
The slot is then zeroed, so the exit sweep of a credited local finds nothing.

`q2`'s own snapshot (`seed_snapshot_local`) was name's box; it now inherits
name's snapshot, or none, since the box it would have compared against is
gone. That is also the correct answer: what q2 inherited from name it now
owns alone, and what both inherited from name's source is still the
source's.

An `own` position is excluded from the release (`arg_moved_into_callee`):
the callee took the box, reused it in place or freed it, and nothing here
may read it; the slot is still zeroed. A method's argument positions are
excluded the same way, since the registry cannot see a method's `own`.

## Measured

`struct_state_last_use`, 8 rounds of 30 (a pair, a four-step chain with a
`string[]` step, and a rebound-then-consumed local):

| | leaked blocks |
|---|---|
| before | 2168 |
| after | 248 |

The 248 are the `names: string[]` buffers each step replaces, which
`__field_reclaim` frees only under the string[]-field admission — the same
residue as `struct_alias_thread_reclaim`'s 16.

The sanitized self-built stage1 assembling natively:

| module | before | after |
|---|---|---|
| lexer.fern | 81 MB live | 75 MB |
| parser.fern | 1.05 GB | 973 MB |
| checker.fern | 2.82 GB | 2.69 GB |

No sanitizer finding on the three, nor on the `examples/tests` inputs.
Smaller than the site count promised: the consumed generation's box goes,
but the buffers the callee's `emit` appended in place are carried on into
q2 and stay live until q2's own release — and q2 is most often returned,
so they are the caller's problem, one frame up, where the same rule fires
again only if that frame binds the result under a new name. The rest of
the 493 MB of grow buffers is the `emit` copies inside `lower_expr` calls
that return their state through a struct field (`LowerResult`-shaped
results), which no rebind and no last use of a same-typed local ever
touches.

## Pinned

`conformance/cases/struct_state_last_use`, with a leak-census row.
