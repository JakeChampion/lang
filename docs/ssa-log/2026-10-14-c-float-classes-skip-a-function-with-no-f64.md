# 2026-10-14 — the float classes skip a function with no f64

Both native backends. `ssa.float_values`. Refs #8171.

## The shape

`float_values` marks the values that live in the float register class. It
starts from every value an f64 could be: f64 constants, arithmetic, math and
conversions, and also every parameter, phi, word load and marked call result.
Those last four are *derived*. Each one then stays in the class only where an
f64 operation reads it, directly or through a phi of the class. The fixed
point that decides this runs over the whole function, and nests a second
fixed point (`float_strong`).

Almost every function has a parameter, a phi or a load. So almost every
function ran both fixed points, though in a function with no f64 operation
and no value only an f64 can be, no derived value can stay. The answer was
always empty, and on the stage-2 compile of `checker.fern` it cost 81 M
instructions.

## What changed

The first scan now also notes whether the function has an f64 operation, or
a value only an f64 can be, the value that seeds the class. Without one,
`float_values` returns the empty class straight away, which is the answer
the fixed point would reach.

## Measured

Stage-2 compile of `checker.fern`, callgrind, same stage-1, warm memo:

| | Ir |
|---|--:|
| main (c78ff289) | 14.219 G |
| this | 14.157 G (−0.43%) |

`float_values` falls from 81 M to 19 M. The binary built from `checker.fern`
is byte-identical, and the stage-2 compiler reproduces itself.

## Tried and dropped: a copy of a call-crossing value at a call-free loop

Frame-slot traffic is 10.3% of the compile's instructions: 871 M loads,
289 M stores and 300 M other slot operands. Most of it is values that are
live across a call, as the backend notes already say. Such a value has one
home for its whole interval, a slot or a callee-saved register, so a hot
loop reads it from the slot even where the loop makes no call.

The prototype split those intervals. For each loop with no call and one way
in from the block laid out before its header, it gave every value the loop
reads, defined before it and live across a call, a copy at the end of that
block, and rewrote the loop's reads to the copy. The copy's interval crosses
no call, so it could take a caller-saved register.

It was correct, and the stage-2 compiler reproduced itself, but the compile
came out 1.2% slower: 14.389 G against 14.219 G.
- The pass cost about 190 M: 107 M for the pass itself, and about 85 M for a
  second `float_values`, which it needed to leave float values alone.
- The loops it improved saved only about 20 M.

Few of the compiler's hot loops qualify. Most make a call. An inlined rc
check counts as a call, since its slow path calls into the runtime, so
nothing in a caller-saved register can live across it. A split at loop
boundaries pays only once those checks stop clobbering, or with a pass cheap
enough to run on every function.
