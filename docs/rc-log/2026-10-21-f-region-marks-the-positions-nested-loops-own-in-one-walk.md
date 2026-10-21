# 2026-10-21 — region marks the positions nested loops own in one walk

`ssarc.region`, the physical lowering's block structure. Refs #8171.

## The shape

A region opens a `block` for every position inside it that no nested loop
owns, walking from the last position back to the first and asking `inner`
whether a loop headed in the region covers that position; `inner` walked the
region again to answer, so the question was quadratic in the region's
length, and the compiler inlined it into `region`, where the whole-compiler
profile showed 470 M of `region`'s 544 M self cost on its two loop lines.

## What changed

`nested` makes the walk once per region, marking by offset each position a
loop headed inside the region owns (the region's own loop aside, as before);
the block-opening loop reads the mark.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the x86 name-bucket slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.107 G | 75.639 G (−0.61%) |

The whole-compiler binary is byte-identical to the base compiler's.
