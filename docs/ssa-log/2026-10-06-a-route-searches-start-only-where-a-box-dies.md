# 2026-10-06 — route searches start only where a box dies

Self-host rc planning, every target. Refs #8171.

## The shape

`ssarc.carried_pairs` hands a box dying in one block to the first
construction of a later block, along a way no other path enters. It searched
for that way from every block of every function, then looked for a box to
hand on. On `checker.fern` that was 30,124 searches, and three things made
each one cost more than its walk:

- Both `carry_route` and `route_to` started from a copy of an all-zero mark
  array as long as the function's block list.
- `route_to` walked back from the target over every block ahead that builds
  nothing, about 136 blocks per call, and only then checked whether anything
  else entered the way.
- Most blocks have nothing dying after their last construction, so the route
  found was thrown away.

Three changes, each keeping the output as it was:

- `carried_pairs` collects a block's dying values that a link does not keep
  (`dying_after_claims`) before it searches, and skips the search when there
  are none. The slot and argument tests run over that list in the same order
  as before, so the first donor found is unchanged.
- Each block the walk takes is a predecessor of the target or of a block on
  the way. The old final check passed exactly when each of those was `from`,
  on the way, or a block ahead that builds nothing, so the walk now stops at
  the first one that is none of these.
- The marks are stamps in two arrays per function (`RouteMarks`), threaded
  `own` and held in locals for the length of a search. A fresh stamp per
  search replaces the cleared copy.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 21b61ba6a against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,004 rows:

| | main | this branch |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.959 G | 19.873 G (−5.18%) |
| `ssarc.carried_pairs`, inclusive | 1,164 M | 78 M |
| `carry_route` calls | 30,124 | 3,107 |
| `route_to`, inclusive | 812 M | 14 M |

## The trap

The stamps alone measured 21.350 G, 1.9% slower than main. Copying the marks
was not where the time went: the walk was. Rebuilding a `RouteMarks` record
for each mark cost about 60 instructions, against about 10 for a write to a
local array. With the walk cut short and the arrays in locals, the same scheme
pays for itself.
