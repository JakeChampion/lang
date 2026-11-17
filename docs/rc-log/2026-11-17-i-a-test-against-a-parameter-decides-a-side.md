# A test against a parameter decides a side of any value

2026-11-17. x86-64, arm64 and wasm32. The change is in `rccertify`.

## The gap

A function that grows a borrowed array and returns it, such as
`androidxml.node`, has the same contract as `arr_push`. Its result is its
parameter's box or a new box, and it holds a unit in each case. The caller
tests the result against its own argument. On the equal edge it releases
the unit, and then it pushes as it does after a shared `arr_push`.

The twin rule of `2026-11-17-h` applied only to push results. The result of
the call was a plain count. After the release on the equal edge, the join
met 0 with 1, so each push after it was unproved. Over `fern_main`,
`androidxml.encode_element` had 102 unproved values of 150.

## The change

- A test of a value with a plain count against a parameter makes the value
  a twin with one side (`side_of_test`). On the equal edge the value is the
  box of the parameter. On the other edge it is another box. Each side has
  the count that the value had, so the twin reads as the same count.

- A spend or a retain of a twin with one side changes the count of that side
  (`one_sided`). Before, it read the twin as one count, and the twin lost
  what it knew about the box.

- The rule of `__fern_arr_concat` moves the units of the operand to the
  result on the equal edge. It now runs before the new rule, so it sees the
  plain count of the result.

## Is it sound

A twin with one side states a fact that the test proves on that edge, and
its count is the count of the value. A spend or a retain of such a twin is
the same spend or retain of a plain count. The twin names a parameter, and
the walk never defines a parameter again.

Two new cases in `self_host_rccertify_test.go` check the shape and one
planted fault: a second release on the equal edge.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 8,329 | 8,014 |
| `wasm32/checker.unproved` | 2,268 | 2,257 |
| `x86_64/checker.refuted` | 4,972 | 4,972 |
| `x86_64/checker.weakened` | 166 | 172 |
| `x86_64/checker.missed` | 477 | 471 |

Six mutants that the certifier missed before are now weaker.
`androidxml.encode_element` has 1 unproved value. The functions with
more unproved values than before are three in `rccertify`, whose source the
change edits.

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on each target:

| target | unproved before | unproved after |
|---|---:|---:|
| x86-64 | 96,362 | 93,544 |
| arm64 | 93,275 | 90,656 |
| wasm32 | 68,108 | 66,262 |

## What is left

Over `fern_main`, the two functions with the most unproved values show the
two shapes that are left. Each has 129.

- `ssa_lift.lift_impl` keeps arrays in the phis of a loop. A phi joins a
  value and an owned push of that same value, and the walk reads the value
  after the phi. A rule for it must know that the two inputs can be one box
  across the back edge.

- `verify.server` splices callees that return a `Result` in two words. A
  join keeps a phi of the constant tags and a phi of each payload with a
  zero. A branch after an empty block reads the tag phi. The walk does
  not connect the tag to its payload. `semthread.split_constant_joins` splits
  such a join, but not this one. A small program with the same chain of `?`
  on `std/tls/der` gives no such join, so the cause needs the full callee.
