# 2026-10-03 — the schema verifiers take membership from the analysis

`ssaunits.schema_fields_error`, `ssarc.schema_types_error`, `ssaunits.nominal`.
Refs #8171.

After `2026-10-02-zb`, each function's two schema checks still looked up, in
that function's table, every struct and union type its entries' fields name
(`ssaunits.admitted` over a `Verdict`'s `needs`). That lookup could not
refuse. Both checks run only on a function whose `ssasem.analyze` passed:
`plan_analyzed` refuses on a failed analysis, and `ssarc.lower` verifies
through `verify_planned`, whose contract is the very function the plan was
formed from. That analysis runs `semrecords.verify` on the same table, and
its `resolved` walk follows every field through arrays, tuples, maps, cells
and function types, requiring each struct and union it reaches to be in the
table. The `needs` are a subset of that walk.

An entry's verdict is now a boolean: whether its fields' shapes are
admitted. `Verdict`, `admit`, `refuse` and `admitted` are gone, and a
`SchemaView` holds one flag per entry. A value's, result's or contract's type
still has its struct and union types looked up (`ssaunits.nominal` with
`lookup`), since `semrecords.verify` checks only the table's own fields.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, stage 2
built from main at be489e3.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.445 G | 25.196 G (−0.98%) |
| `semrecords.find_union` + `find_enum` + `find_struct` self Ir | 82.1 M | 47.0 M |
| `semtypes.equal` + `named_equal` + `named_args_equal` + `equal_list` self Ir | 219.0 M | 165.7 M |
| `ssaunits.has_nominal` + `admitted` self Ir | 34.9 M | 4.0 M |
| `Verdict` release and drop self Ir | 34.7 M | — |

Byte-identical against a compiler built from main: the `checker.fern` binary,
the stage-2 compiler's output, and all 1,965 `selfhost-emit-hashes` rows.

## Not taken

`2026-10-02-zb` named a name index on the enum table as the next step. A
lookup through `util.NameIndex` hashes the name at about 200 Ir
(`util.hash_bucket`), while `find_union`'s scan costs about 233 Ir a call
including the `semtypes.equal` an index would still need, because the tables
are short: almost every call matches on its first `equal`. The index would
not pay for itself; cutting the number of lookups, as here, does.
