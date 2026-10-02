# 2026-10-02 — a schema is read off its declaration once per module

`semsource.schema_of`, `schemas`, `env_rows_closed`, `complete`, `build`,
`build_module`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 219635ac, and the `checker.fern`
binaries the two stage-2 compilers emit are byte-identical.

## What the profile named

`env_rows_closed` was 1.42 G of the 35.28 G stage-2 compile of
`checker.fern`, `schemas` 1.01 G of that. Each produced function closes
its own schema table over the records and enums its values, contracts
and result reach, and read every entry off the declarations again:
`record_entry` 16,329 times, `enum_entry` 4,889 times (each a
`union_layout` and a `variant_shape_sig` per variant, 109 k instructions
an entry), `env_entry` 456 times, over 1,827 functions of a module that
declares a few dozen of each. An entry is a property of the type and of
the module's declarations, not of the function whose walk reached it.

## What changed

`SchemaMemo` holds every entry the module's productions have reached so
far; `build_module` hands it to each `build` and takes it back
(`Made`), in order. `schema_of` looks a nominal type up in the memo
before reading its declaration, and an entry it does read goes into the
memo (`with_record`, `with_enum`). A memo hit appends the same entry at
the same point of the function's walk, and a member-layout enum walks
its members on both paths (`enum_members`), so each function's table
is what it was, entry for entry and in order. A refused entry is not
memoised.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 219635ac and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 35.28 G | 34.85 G (−1.21%) |
| stage 2, `env_rows_closed` inclusive Ir | 1.42 G | 1.08 G |
| stage 2, `schema_of` inclusive Ir | 1.16 G | 0.73 G |
| stage 2, `enum_entry` inclusive Ir | 564 M | 0.8 M |
| stage 2, `record_entry` inclusive Ir | 333 M | 1.2 M |
| `record_entry` / `enum_entry` / `env_entry` calls | 16,329 / 4,889 / 456 | 98 / 5 / 30 |

## Witnessed

`TestSelfHostSemanticSource*`, `TestSelfHostSemanticProduction`,
`TestSelfHostSemanticAllocationCounts`,
`TestSelfHostSemanticInferredCycle`,
`TestSelfHostSemanticSourceCensusLoadsTheStdlib`, the lint ratchet,
`make fmt-check`, and the emit-hash sweep.

## Next

The walk itself is what is left of `env_rows_closed`: `schemas` 669 M,
of which `with_record` 180 M is mostly `NameIndex.added` (178 M, a
hash per append and a rebuild at each doubling), `find_named` 71 M,
`named_by_schemas` 125 M, `env_rows` 150 M, `func_types_in` and
`dyn_types_in` 165 M between them. Each function still hashes a
reached name three times (its own table, the memo, the append).
`ssaunits.schema_fields_error` then walks the finished table once more
per function, 426 M, asking `supported` of every field; the table is
closed over its fields, so that answer could be settled per entry.
Elsewhere, self cost: `util.hash_bucket` 0.80 G, `__fern_str_eq`
0.49 G, `__sem_release_typeinfo__Type` 0.51 G.
