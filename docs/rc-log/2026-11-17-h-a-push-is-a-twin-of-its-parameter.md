# A push onto a parameter is a twin of that parameter

2026-11-17. x86-64, arm64 and wasm32. The change is in `rccertify`.

## The gap

`arr_push` grows its array in place when the box has one holder and room,
and it returns a new box when it does not. Its row in `rcsigs` gives its
result an unknown origin, so each push result was unproved. A function that
pushes onto a borrowed array, such as `elf.elf_shdr`, was unproved from its
first push to its return.

The lowering uses two shapes for a push onto a borrowed array:

- It pushes, and tests the result against the parameter. On the equal edge
  it pushes again with `arr_push`. On the other edge it pushes with
  `arr_push_owned`. A phi joins the two results, and the next push tests the
  phi against the parameter again. At the return, the equal edge retains
  the result.
- It retains the parameter and tests it with `__fern_rc_is_unique`. The
  unique arm pushes with `arr_push_owned`. The other arm pushes with
  `arr_push` and releases the parameter.

The earlier rule for `__fern_arr_concat` matches neither shape. The test of
the first shape is of a phi, and the second shape has no test of the result.

## The change

- A push onto a parameter gives a twin of the parameter. A twin is a state
  with two sides. It holds no unit if it is the box of the parameter, and
  one unit if it is a new box (`rccertify.twin`). A push onto a value that is
  known to be the box of the parameter gives a twin of the parameter too.

- A test of a twin against its parameter decides the side on each edge
  (`twin_on_edge`). On the equal edge, the twin takes the units of the
  parameter. An owned push onto a value that is not the box of the
  parameter gives a value that is not the box either.

- A join meets the two sides apart (`meet_twin`). A plain count is a twin
  with that count on both sides. A phi input hands one unit of each side.

- A spend, a retain, a store, a redefinition and a death read a twin as one
  count (`flat`). A twin with the same count on both sides is that count.
  A twin with two different counts is unproved.

- A twin names the root of its parameter, not a tracked key. A borrowed
  parameter that nothing retains or releases has no key.

- On the arm where `__fern_rc_is_unique` found another holder, a push that
  is the first call of the arm copies the box. A box with a negative count
  also fails the test, and the push keeps such a box in place. No retain or
  release changes that box. Thus the result counts as a new box on both
  paths (`first_on_shared_arm`).

## Is it sound

The two sides are the two results that the runtime can give, and the walk
keeps each side apart until a test decides it. A twin names a parameter,
and a parameter is never defined again, so a twin cannot refer to an old
value of a loop. Each use that the rule does not model reads the twin as
one count. Where the two sides differ, that count is unproved, and no
unit is lost or made.

The rule for the shared arm depends on the runtime. `__fern_rc_is_unique`
is false for a count other than 1. The push grows a box in place only for a
count of 1 or a negative count. The rule needs the push to come
before any other call on the arm, so that no release can change the count
after the test.

Four new cases in `self_host_rccertify_test.go` check the two shapes and one
planted fault for each: a release of the pushed box on the equal edge, and
a retain on the shared arm that no release balances.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 17,349 | 8,332 |
| `wasm32/checker.unproved` | 3,777 | 2,271 |
| `x86_64/checker.refuted` | 4,949 | 4,976 |
| `x86_64/checker.weakened` | 164 | 166 |
| `x86_64/checker.missed` | 509 | 480 |

Of the 29 mutants that the certifier missed before, it refutes 27 and finds
2 weaker.

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on each target:

| target | unproved before | unproved after |
|---|---:|---:|
| x86-64 | 140,039 | 96,239 |
| arm64 | 136,952 | 93,152 |
| wasm32 | 106,594 | 67,985 |

## What is left

- A push onto a value that is not a parameter, such as the phi of a loop
  that starts at a parameter, is still unproved. A rule for it must drop
  each twin of such a value when the walk defines the value again.
- `__fern_arr_concat` keeps its own rule (`took_operand`). It can become a
  twin of its operand when the operand is a parameter.
