# A concatenation returns its operand or a new box

2026-11-17. x86-64, arm64 and wasm32. The change is in `rcsigs` and
`rccertify`.

## The gap

`__fern_arr_concat` grows its array operand in place and returns it, or
copies it into a new array and returns that. The lowering then tests the
result against the operand. When they differ, the code releases the
operand. The certifier gave the operand an unsure effect, so the operand was
unproved after each concatenation, and a release of it was never checked.
Over `compiler/checker.fern`, most of the missed mutants were such releases.

## The change

- `rcsigs.effect_kept_or_same` describes the call. The operand keeps its
  unit, and the result gets a fresh unit.

- On the edge where the test finds that the result is the operand, the two
  names are one box. The result takes the units of the operand, the operand
  holds none, and the fresh unit of the result goes
  (`rccertify.same_box_on_edge`). The rule applies only while the result
  still holds its fresh unit and both values are tracked. The concatenation
  into a field of a record reads its operand as a borrowed load, which is
  not tracked, so its own rules stay.

## Measured after

Over `compiler/fern_main.fern` on x86-64, 17,289 values are unproved, from
17,811. Over `compiler/checker.fern`, the certifier misses 509 mutants, from
529, and refutes 4,939, from 4,919. On wasm32, 3,777 values of
`compiler/checker.fern` are unproved, from 3,857.

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on each target:

| target | unproved before | unproved after |
|---|---:|---:|
| x86-64 | 143,185 | 139,834 |
| arm64 | 140,089 | 136,747 |
| wasm32 | 107,755 | 106,392 |

## What is left

- `arr_push` onto a borrowed array has the same shape, and its result has an
  unknown origin, so it is unproved from the start. The same rule for it
  takes the unproved values over `fern_main` from 17,293 to 8,387, but it
  gives 208 false findings. The lowering joins the results of two pushes in
  a phi, and then it tests the phi against the operand again. So the rule
  must follow such phis.

- A first form gives each value the box it can be, through pushes, moves
  that can return their operand, and phis whose inputs agree, with an
  optimistic start for the phis of a loop. It takes the findings to 56, and
  the unproved values over `fern_main` to 8,343. The findings left need a
  set of boxes for each value: a phi of the operand itself and a push of
  that phi can be the operand on either input. That form is the next lead,
  and it needs its own argument that it is sound.
