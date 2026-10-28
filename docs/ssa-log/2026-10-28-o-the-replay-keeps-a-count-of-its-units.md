# 2026-10-28 — the replay keeps a count of its units

`ssaunits.holds_invariant` and the unit-plan replay. Refs #8171.

## The shape

The replay checks each edge against the invariant of the block it
enters. `holds_invariant` tests that each wanted unit is present, and
that no other unit is present. For the second test it counted the set
bits at every owned value of the function, on each edge. The count cost
about 530 M Ir in the self-compile.

## What changed

The replay keeps the count as it changes the row. `invariant` counts the
bits that it sets. `apply_step` and `enter_phis` change the count only
when a bit changes. `holds_invariant` compares the count with the wanted
count.

A replay row sets a bit only for an owned value. So this count is the
same as the old one. The list of owned values on `ReplayIndex` is
deleted.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at 2cdae579d with
the first five slices of #13785.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 106.369 G | 105.856 G (−0.48%) |

The whole-compiler binary is byte-identical to the base compiler's.
