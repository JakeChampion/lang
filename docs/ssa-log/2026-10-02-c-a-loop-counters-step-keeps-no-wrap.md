# 2026-10-02 — a loop counter's step keeps no wrap

`ssa.counter_step`, part of `ssa.drop_low_wraps`. Refs #8171. Builds on
`2026-10-02-a-wrap-nothing-reads-is-dropped.md`.

## The shape

That pass keeps a wrap whenever something reads the value whole, and a loop
counter is read whole by its own test, so `i = i + 1` under `while (i < n)`
kept its sign extension on every iteration. On main at 29a15546, 295 M of the
738 M executed `movslq` on the stage-2 compile of `checker.fern` directly
followed an `addq $1`.

## What changed

The wrap of `v + 1` at 32 bits is dropped, whoever reads it, when v is a phi
whose block branches on `v < n` (or out on `v >= n`) at 32 bits to a successor
entered from nowhere else, and that successor dominates the add. On that path
v is below a 32-bit value, so `v + 1` cannot pass INT32_MAX: the 64-bit sum
already equals its sign-extended low half, and every later read, the loop's
next test included, sees the value the wrap would have produced.

Dominance is a walk over predecessors back from the add's block that may not
step onto the successor: reaching the entry means some path skips the test.
A walk forward from the entry cost 36 M over the compile, since it visits the
whole function whenever the answer is yes; walking back visits only the loop
body, and costs 7 M.

`le_s` is not taken: under `i <= n` the step can pass INT32_MAX, which
`counter_le_keeps_wrap` pins with a counter that overflows at run time.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 29a15546 plus `2026-10-02-a`, and this change on that.

| | main | 2026-10-02-a | this change |
|---|--:|--:|--:|
| stage 2, total Ir | 27.822 G | 27.763 G | 27.427 G (−1.2% on a) |
| `ssa.drop_low_wraps`, inclusive Ir | — | 101 M | 140 M |
| `movslq` in the stage-2 compiler's text | 11,466 | 10,646 | 7,413 |

Emitted bytes change: 598 of the 1,965 rows of the `selfhost-emit-hashes`
sweep differ from `2026-10-02-a`, with the same 257 refused.

## Witnessed

`TestSelfHostOptimisationShapes` (with `counter_step_unwrapped`, whose loop
has an `if` between the test and the step, and `counter_le_keeps_wrap`),
`TestSelfHostI32OverflowIR`, `TestSelfHostRedundantWrapFlowsThrough`,
`TestSelfHostSubwordWrapIR`, `TestSelfHostU32Wrap*`, `TestSelfHostSSA*`,
`TestSelfHostSemantic*`, `TestSelfHostX86*`, `TestSelfHostRc*`,
`TestFernFixturesSelfHostX86_64` (557 passing, no skips), the stage-2 build
and its compile of `checker.fern`, the lint ratchet and `make fmt-check`.

## Next

The arm64 hash loop loads its multiplier from the literal pool on every
iteration, and both targets reload a string's data pointer every byte: a
constant and a load the loop never changes.
