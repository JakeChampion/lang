# 2026-10-28 — an array shrinks in place

`ssasem.truncate`, `semsource.builtin_truncate`, `ssarc.truncate_update`,
`ssarc.update_base`, and the Go checker and interpreter. Refs #12887.

## The shape

An array had three primitives: `append`, `with` and `len`. Nothing made an
array shorter in place. Each removal in the standard library built a new
array with an append loop: `std/serve.__flights_remove`,
`std/fetch.__kept_without`, `std/fetch.__without_fd`, and `std/array.take`
and `drop_last`. Each copy retains every element it keeps, and the old array
then releases each one.

The two-fetch census at 1,024 connections shows the cost. It grows with the
number of handlers in flight. These rows are instructions per request, from
callgrind:

| Function | 8 connections | 1,024 connections |
|---|--:|--:|
| `serve.__flights_remove` | 247 | 3,151 |
| `serve.__flight_pair` | 219 | 1,804 |
| `fetch.__kept_without` | 243 | 1,824 |
| `__fern_arr_push_owned` | 286 | 1,480 |

The total goes from 102.4k to 112.9k. At 4,096 connections, the nightly row
records 117 µs of user time for each request. hyper uses 40 µs.

## What changed

`xs.truncate(n)` keeps the first `n` elements. A length below zero keeps none,
and a length past the end keeps all, as `std/array.take` does. The receiver is
consumed like the receiver of `with`.

- `semsource` lowers the method to the semantic op `truncate` (`0 - 56`).
  `ssa.semantic_kind_names` now also names `array_reserve`, `arr_concat` and
  `bytes_new`, which printed as `invalid`.

- The planner treats it as a box update (`ssaunits.box_update`,
  `operation_supplies`, `hands_out`, `semsink.grows`, `semunique`), so a link
  through a borrowed array parameter covers it as it covers `with`.

- `ssarc.update_base` is the base choice that `with` used: a link, a field
  grow row, a deferred retain, a unique receiver, or a sole owner. `with` and
  `truncate` share it now.

- `truncate_update` tests the base. A unique box releases its tail elements
  and sets its length. A box that is still shared is copied up to the new
  length.

- The Go checker registers `Array.truncate`, and both interpreters copy the
  prefix.

`std/serve.__flights_remove` moves the last flight into the removed slot and
truncates by one.

## Measured

A loop that pops 200,000 strings one at a time runs in 16 ms, and the census
balances. `TestSelfHostArrayTruncate` pins eleven shapes on x86-64, arm64 and
wasm against the interpreter, with a balanced census on the natives. The
shapes include a shared receiver, a borrowed parameter, a record field and a
swap remove.

In the two-fetch server, `__flights_remove` goes from 3,151 to 176
instructions per request at 1,024 connections. The total does not move
(112.9k to 111.9k), because the flight array is still copied on each
removal. `__fern_arr_inc_elems` grows by 1,642 and `__fern_arr_slice` appears.

## Traps

The copy is the plan's, not the primitive's. `__flight_wake` reads
`fl = fls[fi]` and uses `fl` after `__flights_remove`. The plan keeps the
element as a borrow of the array. Thus a retain keeps the array past the
removal, and the removal sees a count of two and copies.

The reproduction reads an element, removes it in a callee, and then uses the
element. It runs in 8.3 s for 60,000 removals. When it reads only a scalar
field, it runs in 5 ms. A removal in place needs a plan that retains the
element and lets the array go. That is the next entry.

`std/fetch`, `std/net` and `std/array` are in the compiler's import closure.
They can call `truncate` only after the pin is a stage0 that knows the
method (`docs/BOOTSTRAP.md`, "Refreshing the pin"). `std/serve` is outside
the closure.

A planner proof of uniqueness gives no run-time test. With no test, the
certifier refuses a release of an element read from the box ("spent without
a unit"). The tail drop therefore sits under its own `__fern_rc_is_unique`.
