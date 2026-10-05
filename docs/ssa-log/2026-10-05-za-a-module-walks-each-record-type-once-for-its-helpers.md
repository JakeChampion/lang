# 2026-10-05 — a module walks each record type once for its drop helpers

Self-host typed lowering, every target. Refs #8171.

## The shape

`ssarc.with_drop_helpers` collects a module's drop and release helpers, body
by body. For each body, `helpers_not_in` walks every record and enum type of
that body's schema table. For each type, `append_helpers` built both helper
symbols (`__sem_drop_` and `__sem_release_` plus the type's key), looked both
up in the module's done index, and called `has_children` for a type with no
helpers.

A type's helpers are settled by the first body whose table holds it: both
are added then, or it has none. Compiling `checker.fern`, 1,874 bodies made
87,700 of these checks for 198 helpers.

`Helpers.seen` now indexes the record and enum types an earlier body's table
held, by `ssasem.type_key`. A later body skips them before building any
symbol. Within one body, the check reads the index as it stood before the
body, so a type listed twice is handled as before.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at deeb3625 against this branch. The two compilers build
`checker.fern` for x86-64 and arm64, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | types walked once |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.861 G | 18.775 G (−0.45%) |
| `with_drop_helpers`, inclusive | 211 M | 126 M |
| `append_helpers`, inclusive | 115 M | 5.0 M |

Of the remaining 126 M, 90 M is `schema_scan`, which scans each body's
values and its table's field types for maps and byte views. What it finds
feeds the routed map helpers, which depend on the body (`f.map_module`), so
it is not skipped by the same rule. Most of the other 31 M is
`helpers_not_in` building a type key for each of the 87,700 record and enum
entries and looking it up in `seen`.
