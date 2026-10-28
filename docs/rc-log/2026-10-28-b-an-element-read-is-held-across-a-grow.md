# 2026-10-28 — an element read is held across a write in place

`ssaunits.grow_holds`, `ssaunits.plan_held`, `Plan.forced`, and the second
planning round in `semlower.rows_planned`. Refs #12887.

## The shape

`std/serve.__flight_wake`:

```fern
let fl: __Flight = fls[fi];
__flight_unwatch(drv, fl, ready);
fls = __flights_remove(fls, fi);
match (fl.job) {
```

`__flights_remove` can write `fls` in place, so `fls` has a grow row there.
The plan kept `fl` as a borrow of `fls`. Thus `fls` was still read after the
call, and the call took a bracket: a retain before it and a release after it.
The callee saw a count of two and copied the whole flight table on each wake.

The reproduction reads an element, removes it in a callee, and then uses the
element. 60,000 removals took 8.3 s. When it read only a scalar field, they
took 5 ms.

## What changed

The plan now counts such a read itself, and lets the array go to the write.

- `grow_holds` looks at each call position with a grow row and at each box
  update. Take an array parameter or an owned array that goes there and is not
  read again. Its live reads are held, and `outliving` names them.

- `semlower.rows_planned` builds the grow table, then plans each function that
  has such reads again with `plan_held`. The holds do not change a function's
  own grow rows, so one round is enough. The table is built again after the
  round.

- `Plan.forced` keeps the holds, so `verify_planned` replays the same units.

- A forced hold asks for a reflow (`through_takes`). Without it, a read of a
  field of the held element (`p.name`) still reached the array through its
  dependency chain, and the array stayed live.

A held read is retained where it is read and released at its last use. The
array's last use is now the call. Thus `hands` gives it to the callee with no
bracket, and the callee writes in place.

## Measured

| Shape | Before | After |
|---|--:|--:|
| reproduction, 60,000 removals, callee not inlined | 8.3 s | 4 ms |
| the same, inlined into the loop | 8.3 s | 4 ms |
| reproduction census | 120,017 allocations | 60,017 |

In the two-fetch server at 1,024 connections, under callgrind, the copy in
`__flights_remove` is gone: `__fern_arr_inc_elems` and `__fern_arr_slice` no
longer grow with the flight table. With the read-size change of this pull request,
the server goes from 112.9k to 96.6k instructions per request.

The `held-element-removal` rows of `TestSelfHostArrayTruncate` pin the shape
on x86-64, arm64 and wasm with an allocation bound. The stage2 compiler builds
a stage3 that builds a stage4, and stage3 and stage4 are identical.

## Next

A helper that removes with `if (at != last) { ps = ps.with(at, ps[last]); }`
and is spliced into a loop that reads `p.name` after the call still copies.
The same loop does not copy with the `with` and the `truncate` written in it.
The helper does not copy when only `p.n` is read. The join of the two arms and
the field read together keep the array live. The serve shape does not take
this path, because `__flights_remove` stays a call.
