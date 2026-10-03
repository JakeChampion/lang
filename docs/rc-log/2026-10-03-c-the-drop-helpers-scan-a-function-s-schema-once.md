# 2026-10-03 — the drop helpers scan a function's schema once

`ssarc.helpers_not_in` and the three column walks it feeds. Refs #8171.
No emitted byte changes: the stage0-built compiler before and after emits
the fixed older tree (`examples/self_host/fern.fern` at 1ae9cad, its
bindings spelled `let`) and that tree's `checker.fern` byte for byte.

## What the profile named

`with_drop_helpers` runs once per produced body and asks `helpers_not_in`
for the helpers that body's schema needs. That built `schema_types(f)`,
a copy of every value type of the function with every record and variant
field type appended, four times per body: once for the byte-view check and
once in each of `boxed_columns`, `routed_box_columns` and
`routed_keyed_maps`. Those three only read the map types among them, and
most functions have none. 2.43 G inclusive on the whole-compiler emit;
2.0% of the stage-2 compile of `checker.fern`.

## What changed

`schema_scan` walks the values and the schema fields once, keeping the map
types in the order the old list met them and whether any type holds a byte
view. The three column walks take that map list.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver
the pinned stage0 builds from each tree, emitting the fixed older tree to
x86-64 asm text, against main with the previous entry's change.

| | before | this change |
|---|--:|--:|
| total Ir | 142.68 G | 141.48 G (−0.84%) |
| `ssarc.helpers_not_in`, inclusive | 2.43 G | 1.22 G |

The compiler each of those drivers builds from its own tree (stage 2),
emitting the fixed tree's `checker.fern`: 21.58 G before, 21.38 G after
(−0.95%).

## Witnessed

Both emit identities; the 709 targeted `internal/e2eselfhost` tests of
the earlier entries; `make check-sources` and the lint ratchet.
