# 2026-10-21 — whole_values spreads through copies and phis by a worklist

`sempair.whole_values`, `seeded`, `through_joins`. Refs #8171.

## The shape

`whole_values` marks the values a function needs whole, then spreads the
mark through copies and phis: whatever a copy or phi needed whole copies is
needed whole too. `through_joins` did that by walking every instruction of
the function again until a round marked nothing new, so a chain of copies
and phis cost one whole walk per link. On the whole compiler's self-compile
that was 191 M instructions, twelve times the `checker.fern` figure for a
subject five times smaller.

## What changed

The closure is the same set in any order, so it is a worklist: each value
marked whole spreads once to the operands of the copy or phi defining it,
through the definition table `seeded` already built and now shares.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the leaf-index slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.526 G | 76.457 G (−0.09%) |

The whole-compiler binary is byte-identical to the base compiler's.
