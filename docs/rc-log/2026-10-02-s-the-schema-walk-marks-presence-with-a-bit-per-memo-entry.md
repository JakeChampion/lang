# 2026-10-02 — the schema walk marks presence with a bit per memo entry

`semsource.Schemas`, `schemas`, `schema_of`, `with_known_record`,
`with_new_record`, `with_known_enum`, `with_new_enum`. Refs #8171. No
emitted byte changes: the `selfhost-emit-hashes` sweep is 1,965 rows per
compiler with 0 differing against a compiler built from main at
d23fff3f, and the `checker.fern` binaries are byte-identical.

## What the profile named

`schema_of` was 738 M inclusive on the 30.93 G stage-2 compile of
`checker.fern`, and 201 M of it was `NameIndex.added`: the per-function
`Schemas` table carried its own `record_names` index so a type already
in the function's table was found by name, and every record the walk
appended to the table, memo hit or not, went through `added` on a
borrowed receiver, copying the index's `names` and `next` lists for each
append. A second `find_enum` over the function's own enums (25 M) and
the `find_named` over its own records (35 M) paid for the same question,
"is this entry in the table yet", and `enum_members` (247 M) asked it
once per variant field.

## What changed

Every entry a function's table holds is an entry of the module memo
(the memo is appended to exactly when the table is), so presence in the
table is a bit per memo index: `Schemas` carries `seen_r` / `seen_e`,
sized to the memo on entry, and `schema_of` tests the bit at the memo
hit instead of searching the table. A new record sets its bit by
appending `true` at the memo's end, where the memo appends it. The
per-function `record_names` index is gone; the memo keeps its own.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at d23fff3f and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 30.93 G | 30.69 G (−0.79%) |
| stage 2, `schema_of` inclusive Ir | 738 M | 291 M |
| stage 2, `enum_members` inclusive Ir | 247 M | 62 M |
| stage 2, `NameIndex.added` inclusive Ir | 201 M | 39 M |
| stage 2, `with_record` → `with_known_record` + `with_new_record` + `no_flags` inclusive Ir | 184 M | 23 M |
| stage 2, `build_module` inclusive Ir | 3.90 G | 3.65 G |

## A trap

The first attempt kept the index and moved the table instead: `out`
became an `own` parameter through `schema_of` and the `with_*`
functions, so the appends would reuse the lists in place. It measured
slower, 31.06 G, with `schema_of` at 970 M. The walk reads fields of
`out` after the move in the same call (E050), so each site had to bind
the field to a local first; what those binds cost at the append was not
traced further, because the bit replaced the question. Measure an `own`
threading before building on it.

## Witnessed

`TestSelfHostSemanticSource*`, `TestSelfHostSemanticProduction`,
`TestSelfHostSemanticAllocationCounts`, `TestSelfHostSemanticInferredCycle`,
the lint ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

`semrecords.find_enum` is 196 M inclusive, from `ssarc.supported_nominal`
(56 M), `ssaunits.supported` (56 M) and `schema_of` (37 M): a union is
found in the memo's enums by a linear scan on its name
(`find_union`). An enum name index on the memo, as `record_names` is
for records, is the same shape as the record side. `find_named` on the
memo is 49 M. `complete` is 1.09 G, `schemas` 436 M.
