# 2026-10-02 — the schema walk queues only the types that can name a record

`semsource.named_by_schemas`, `semsource.names_schema`. Refs #8171. No
emitted byte changes: the stage0-built compiler before and after emits the
fixed older tree (`compiler/fern.fern` at 1ae9cad, against that
tree's stdlib) and that tree's `checker.fern` byte for byte, and the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing,
against the previous entry's tree.

## What the profile named

`semsource.schemas` builds a function's schema table by walking a queue of
types: each record or enum it adds has every field type appended, and
`schema_of` is asked of each. Scalars and strings name no schema, and
`schema_of` hands them back after a chain of type tests and a release:
3.4 M `schema_of` calls for 11 k tables, 3.24 G, with roughly half the
queue made of types that could add nothing.

## What changed

`named_by_schemas` appends a field type only when `names_schema` says
`schema_of` can walk into it or record it: an array, a tuple, a map, a
function value, a struct or a union. Everything else was a no-op in the
walk, so the table built is the same.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 149.38 G | 148.20 G (−0.8%) |
| `semsource.schemas`, inclusive | 3.24 G | 2.56 G |
| `semsource.schema_of`, inclusive | 2.35 G | 2.10 G |
| `semsource.env_rows_closed`, inclusive | 4.96 G | 3.78 G |

## Witnessed

The checker, planner, semantic, closure, method and lift set (the three
name lists of 2026-10-02-q, -r and -s together; 621 tests, green),
`make check-sources`, the lint ratchet, both emit identities and the
sweep.

## Next

The walk still asks `schema_of` of 1.9 M types, most of them records
already in the table. #11105, which landed before this entry, answers that
with a presence bit per memo entry instead of the table's own name index,
so the measurement above predates it. `env_rows_closed` rebuilds the
table per round of its fixpoint; a round could extend the previous
round's table instead of starting from the named types again.
