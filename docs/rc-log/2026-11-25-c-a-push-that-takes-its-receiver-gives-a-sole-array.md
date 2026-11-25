# A push that takes its receiver gives a sole array

2026-11-25. All targets. The change is in the sole boxes of the unit plan
(`ssaunits.sole_boxes`). Refs #12105.

## The shape

The tape parser of `std/json` adds each node in two pushes:

```fern
function __json_t_node(own tape: u64[], kind: i32, flags: i32, a: i32, b: i32): u64[] {
  return tape.append(__json_t_word(kind, flags, a)).append((b as u32) as u64);
}
```

`tape` is an `own` parameter, so the first push tests its count. The second
push tested the count again, and it had a release of the old box on its
copy arm. `pair` in `TestSelfHostUniqueLoop` has the same shape.

## The cause

`sole_boxes` linked the result of a push to its receiver. A push result was
sole only when its receiver was sole. A parameter is not sole, so the result
of the first push was not sole.

A push that takes its receiver tests the receiver. When the receiver has
another holder, the push copies it. When the count is negative, the push
writes in place, and the result counts as a new box. So the result is sole
on each arm.

## The change

- A push that moves its receiver gives a sole result. `sole_boxes` removes
  the link from the receiver to that result.
- The rule does not apply to a receiver that can hold the box of an array
  parameter that is not `own` (`linkable_values`). A linked push can write
  that box in place, and the caller still holds it.

`sole_result` uses the same sole boxes. A function that returns such a push
now returns a fresh array, so its callers can skip a test too.

## Measured

Count-test labels in the x86-64 asm of `pair`: 6 before, 3 after.

Instructions (callgrind, x86-64) of five `json.parse_document` calls and the
read of the file, from simdjson's three test files:

| File | Before | After |
|---|---:|---:|
| twitter.json | 32,466,799 | 31,363,884 (-3.4%) |
| canada.json | 205,037,996 | 198,630,766 (-3.1%) |
| citm_catalog.json | 76,170,202 | 73,536,132 (-3.5%) |

## Tests

`TestSelfHostUniqueLoop` gets `pair`. It checks the result for a shared
input and an empty input, and it requires 3 count-test labels in `pair`.
Without the change, the test fails with 6 labels.
