# 2026-10-15 — a body is lifted once

Both native backends. `asmcore.unit_leaves`, `ssaprepare.finish_formed`,
`ssa.calls_leaf`. Refs #8171.

## The shape

Before a unit emits anything, `unit_leaves` lifts each body of 160 ops or
fewer and puts it in register form to find the leaves a caller may inline.
Then it threw both away, and the emit lifted and register-formed each of
those bodies again. Compiling `checker.fern` lifts 1,028 bodies this way, so
the work is done twice for each of them.

## What changed

`unit_leaves` keeps each lift by cache position (`asmcore.Prelift`). It also
keeps the register form when the body calls none of the unit's leaves,
because inlining those leaves would not change it. The emit loop passes a
body's prelift alongside its `selected` row. The emit takes the lift and, when
there is one, the register form, rather than making them again.

A body whose emit tracks its slots (`-g`) lifts again, because its lift
records the slots. The runtime helpers emit from a cache of their own, so
they take no prelift.

## Measured

Stage-2 compile of `checker.fern`, measured with callgrind with a warm memo:

| | Ir |
|---|--:|
| main + #12431 | 14.072 G |
| this | 13.985 G (−0.62%) |

The whole compiler compiles to the same bytes on x86-64 and arm64, and the
stage-2 compiler reproduces itself.
