# arm64 meets every condition the default flip is held to

All three of `docs/SELFHOST-SSA-BACKEND.md`'s targets, measured on the
compiler building itself at the head of the call-result and
spill-at-interval work. This is the evidence for step 4; the flip itself is
a decision, not a measurement, so nothing here changes a default.

## Faster output

The compiler built with `-backend ssa` against the one built with
`-backend flat`, each compiling a self-host module, arm64-darwin, best of
three:

| | flat-built | ssa-built | |
|---|---|---|---|
| `checker.fern` | 12,733 ms | 8,876 ms | **30% faster** |
| `irlower.fern` | 4,211 ms | 2,135 ms | **49% faster** |

## Smaller output

Whole compiler, arm64-linux:

| | flat | ssa | ratio |
|---|---|---|---|
| instructions | 4,051,791 | 3,815,776 | **0.94x** |
| linked binary | 17,805,056 | 16,624,896 | **0.93x** |

The condition is "at or under flat's". It was 1.12x on both counts when the
target was written.

## Faster compile

Self-build, arm64-linux, best of two: 108,590 ms on `-backend flat`,
112,547 ms on `-backend ssa`. **1.04x**, against a stated ceiling of 1.5x
with parity the aim. It was 1.11x when the target was written.

## The corpus

519 conformance programs on arm64-linux and 521 on x86-64-linux, each
built on both backends and run: same exit code and same output on every
one. Stage two is byte-identical on both targets.

## x86-64 is not there

| | flat | ssa | ratio |
|---|---|---|---|
| instructions | 3,048,987 | 3,798,189 | 1.25x |
| linked binary | 14,496,040 | 18,199,688 | 1.26x |

It meets the corpus condition and fails the size one. The reason is the
register budget rather than anything selection can fix: over half the
excess is frame traffic, because nearly everything spills over six
caller-saved and five callee-saved registers where arm64 has eight and ten.
`2026-09-17-spill-the-longer-interval.md` records the two things measured
and found not to help there.

So the flip, if it is taken, is per target: arm64 on the register path and
x86-64 left on the stack machine until its size condition is met.
