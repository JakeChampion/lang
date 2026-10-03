# 2026-10-02 — a schema entry is checked once per module

`ssaunits.schema_fields_error`, `ssarc.schema_types_error`, `ssarc.schema_views`.
Refs #8171.

Both schema checks walked every field of every record and enum in a
function's schema table, for each of a module's 2,450 produced functions,
though most tables hold the same entries. The verdict on an entry has two
parts. Whether each field's shape is admitted, and which struct and union
types it names, is a property of the entry. Whether those named types are in
the table is a property of the function.

Each predicate (`ssaunits.supported`, `ssarc.supported`, `ssarc.walkable`)
now folds a type into a `Verdict`: whether its shape is admitted, and the
nominal types it names. Folded with `lookup` it reads the table as it goes,
which is what `supported` and `walkable` do, so each predicate has one body.
`ssarc.schema_views` folds every distinct entry once per module, keyed by
name and confirmed with `semtypes.equal` as a table lookup is, and hands each
function a view of its entries' verdicts in table order. A function's check
looks up each verdict's named types in its own table, so the table is still
read per function: an entry admitted in one table is admitted in another
only when the types it names are there too. `plan`, `plan_analyzed` and
`lower` take the view. `semlower` builds one per module; every other caller
passes `ssaunits.no_view()`, which folds each entry in place as before.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, stage 2
built from main at 033d446.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 27.83 G | 27.76 G (−0.23%) |
| `ssaunits.supported` self Ir | 60.9 M | 5.6 M |
| `ssarc.walkable` + `supported` + `supported_nominal` self Ir | 59.3 M | 5.6 M |
| `schema_fields_error` + `schema_types_error` self Ir | 32.1 M | 12.6 M |
| `ssarc.view_of` self Ir | — | 29.6 M |

Byte-identical against a compiler built from main: the `checker.fern` binary,
the stage-2 compiler's output, and all 1,965 `selfhost-emit-hashes` rows.

The membership lookups stay per function, and `semrecords.find_enum` is a
linear scan over the enum list (10 M here, about 225 M across the whole
compile): a name index on the enum table is the next step for both.
