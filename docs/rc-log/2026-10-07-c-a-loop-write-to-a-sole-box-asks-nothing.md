# 2026-10-07 — a loop write to a sole box asks nothing

`ssaunits.unique_receivers`, the plan's list of `with` writes that need no
run-time uniqueness test. Refs #8171.

## What changed

A `with` on a receiver the frame holds tests the box's count at run time
(`sole_owned_base`): in place when it is 1, a copy otherwise. The plan
already skipped the test where it could prove the answer, but only for a
receiver that was itself a `with`'s result used nowhere else, which a loop
never is: there the receiver is a phi of the array the loop started with and
the previous iteration's write. Every fill loop, `out = out.with(i, v)` over
an array the function just allocated, therefore asked the count once per
element, three compares and two branches around the store.

`sole_boxes` now marks the values whose box no unit but the frame's own one
ever holds. A box starts that way when a `with` hands it back or a
zero-filled allocation builtin (`__alloc_i32`, `__alloc_i64`, `__alloc_bool`,
`__alloc_u8`) makes it, and a phi keeps it when every operand does. It stops
at any unit a step retains on it, and at any use other than reading an
element or the length, being a `with`'s receiver, flowing into a phi that
keeps it, or being returned: a call, a record or array field, a second name.
A `with` whose receiver is marked and moved into it skips the test.

A zero-filled allocation of length 0 is the static empty box, which the
run-time test would send down the copy path. Skipping the test there is
still sound: every `with` on an empty array is out of bounds, and both paths
reach that check before writing, with no element read or released first
because the builtins' elements are scalars.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`: each side's stage 3, built by its own stage
2, built from the side's sources by the stage0-built stage 1. Main is
c9eb1b08b. Both stage 3s rebuild themselves byte for byte.

| | main | this change |
|---|--:|--:|
| total Ir | 15.956 G | 15.853 G (−0.64%) |
| stage 3 size | 11,166,752 | 11,157,952 |

The analysis itself is 15 M of that compile (`plan_analyzed`, into which it
inlines, 159.9 M to 175.1 M on a `-g` stage 2 that does not yet carry the
change in its own code).

The inlined uniqueness test is 992 M on the same compile (summed from a
per-instruction profile over every `test`/`cmp`/`cmpl` window the inline form
emits, 50 k sites). This change takes about a tenth of it.

## What is left

The rest of the test's cost is mostly in two places. Release paths test the
count before freeing, and the plan cannot know it there. `with` writes whose
receiver is a parameter or comes from a call stay outside this proof: their
box may have holders the frame cannot see. An `append`'s receiver is tested
by the push itself (`append_push`), which this proof does not reach yet. The
push's result is a fresh box in every path, so it could seed the same
analysis, as long as the field-grow and linked forms are excluded.

The test is `TestSelfHostSoleLoopWithX86_64`: three shapes the proof takes
(a fill, a fill that reads back, two loops over one box) carry no test, and
four it must refuse (a second name, a call, a record, a parameter) keep it,
each run against the interpreter's exit under the sanitizer.
