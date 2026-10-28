# 2026-10-28 — a link goes out in a pair, and a held read reflows

`ssaunits.constructs`, `ssaunits.pair_call_root`, `ssarc.owe_constructed`,
and the reflow rule in `ssaunits.units_of`. Fixes #13797, refs #12887.

## The shape

The two-fetch server copied its whole flight table once or twice for each
request. At 4,096 connections the table holds about 4,000 flights. Thus each
copy retained 4,000 elements (`__fern_arr_inc_elems`), and the serve loop then
released 4,000 elements of the old table (`__sem_release_serve____Flight`).

gdb at the copy in `__handler_wake` showed a count of 2 on each of 3,256 copies,
with spare capacity in each. The chain is:

1. `__serve_loop` owns the table and lends it to `__flight_wake`.
2. `__flight_wake` gives it to `__flights_remove`, which truncates in place and
   returns the same box.
3. `__flight_wake` gives the result to `__handler_wake`, which appends.

An update in place on a lent array is linked to its parameter. A linked value
takes no count of its own while it goes out of the function as the link. Two
points broke the link here:

- `__flight_wake` returns `(c, fls)`. `sempair` lowers that return to a
  `__pair_set` call. A linked value moved into that call was an unlinked supply,
  so `prune` removed the link. The result of `__flights_remove` then kept its
  own count, and the next append saw a count of 2.

- The arms of `__flight_wake` join at a phi. The arrays that come back in a pair
  from `__serve_answer`, `__h2_producer_wake` and the other wake functions are
  read with `__pair_word` and `__word_as`. They were not linked, so the phi was
  not linked either.

A third shape copied without these calls. A helper that removes with
`if (at != last) { ps = ps.with(at, ps[last]); }` and is spliced into a loop that
then reads `p.name` copied on each removal. The element read `p` was held, but
`p.name` still reached the array through its dependency chain. The reflow
(`through_takes`) that moves such a chain onto the held read ran only for a
forced hold.

## What changed

- `linked_supply` accepts a move into a tuple, a record, a variant or a
  `__pair_set` call (`ssaunits.constructs`). The link ends there.
  `ssarc.owe_constructed` gives the constructed value a counted reference
  with `take_owed`, as a return of a linked value does.

- `propagate` links an array that `__word_as` reads out of a call's pair as it
  links the call's own result (`ssaunits.pair_call_root`). The pair words are
  captured right after the call. `settle_call_link` then settles the callee's
  counted reference, as it does for an array result.

- `units_of` reflows when the plan holds any read, not only a forced one.

## Measured

| Shape | Before | After |
|---|--:|--:|
| two-fetch server, 4,096 connections, callgrind | 448k instructions per request | 283k |
| two-fetch handler, census | 89 allocations per request | 88 |
| removal through a join, 3,000 removals | 2,999 allocations | 0 |

The flight-table copy is gone from the profile at 4,096 connections. What grows
with the connection count now is the flight lookup (`__flight_pair`,
`__flight_index`, `__flight_at`), `fetch.__without_fd`,
`fetch.Idle.read_interest` and `__fern_task_new`.

`TestSelfHostArrayTruncate/held-element-join` pins the join shape on x86-64,
arm64 and wasm. `TestSelfHostServeAllocsPerRequestTwoFetch` pins 88.

## Traps

A reproduction outside serve did not copy. A helper that returns a tuple with
the appended array, or a two-arm wake with a phi, made no copies with the old
compiler. Only the serve chain, with a call result read out of a pair and
joined at a phi, showed the count of 2. gdb at the copy site found it. The
allocation census found it too, but only as one allocation per request at low
concurrency.

`FERN_SEM_INLINE=0` did not remove the copy. Inlining was not the cause.
