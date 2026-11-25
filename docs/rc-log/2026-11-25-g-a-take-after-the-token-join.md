# A take after the token join

2026-11-25. x86-64. No change to the compiler. This entry records the cause
of the unproved values of `ssaunits.carried_pairs`, a trap, and the next
lead.

## The shape

`ssaunits__carried_pairs` has 18 unproved values, the most of any function.
In the walk with the twins of `token_loads`, one join still meets two plain
counts as maybe. In the compacted ids of the walk:

```
v76 = call ssaunits__step_of ...
v79 = phi v76 v78                // the token: v76 or 0
v80 = ld_at 32 rc v76
brif (v79 == 0) b112 b114
b112: v83 = call __fern_rc_inc v80
b114:                            // v80 meets 0 and 1
brif (v79 != 0) b115 b116
b115: st_at 32 v76 0             // the take of the slot
      call __sem_drop_ssaunits__Step v76
      call __fern_rc_dec v76
b116: call __sem_release_ssaunits__Step v76
```

Where the token is null, the field is retained. Where the token is the box,
a store of null takes the slot, so the field gets the unit of the slot. But
that store comes after the join `b114`. At the join the field holds 0 units
on one arm and 1 on the other.

`token_loads` does not see this shape. Under the token holding the box, the
load is still a borrow. The unit comes from the store, not from the release
of a shell.

## A trap

A join can make a twin of the word that its dominator tests (`join_key`).
`join_key` refuses a word that is a token. A test let it take a token in the
walk with the twins of `token_loads` only. The twin of `v80` was then
`twin(v79, 0, 1)`, and the test of `v79` and the take settled it.

But the census of `fern_main` went from 604 to 768 unproved values, and 34
functions went up, for example `semsource__try_expr` (5 to 31) and
`checker__ow_match_stmt` (0 to 23). There was no finding. A token key at a
join is wrong in other shapes too. Do not try it again in this form.

## Next lead

Under the token holding the box, a store of null can take the slot on every
path from the load. There, count the unit at the load, and do not count the
take again. This needs to know that the store comes after the load on each
path of the region. The certifier has no query for that yet.
