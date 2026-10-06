# 2026-10-02 — the grow table travels with its index

`ssaunits.GrowTable`, `grows_buffer`, `grow_fields_of`, `grow_rows_agree`,
`ssarc.bracketed`. Refs #8171. No emitted byte changes: the stage0-built
compiler before and after emits the fixed older tree
(`compiler/fern.fern` at 1ae9cad) and `checker.fern` byte for
byte, and the `selfhost-emit-hashes` sweep is 1,965 rows per compiler
with 0 differing, against #11051's merged head at 26491347.

## What the profile named

The previous entry indexed the grow table inside its fixpoint and handed the
finished table on as the bare `GrowRow[]` it always was, so its two readers
in `ssarc.bracketed`, `grows_buffer` and `grow_fields_of`, still scanned
every row of the module's table per borrowed argument of every call: 2.29 G
for `grow_fields_of` and the `has_row` under `grows_buffer`.

## What changed

`grow_table` returns a `GrowTable`, the rows with the callee index the
fixpoint already kept beside them. `semlower.Rows.grows`, `produced_body`
and the four `ssarc` signatures that thread it carry the table; `grows_buffer`
and `grow_fields_of` walk the callee's chain, and `grow_rows_agree` asks
each table along its chain. `no_grows()` is the empty table. The fixture
that reads the table reads `.rows`.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the branch of #11051 at 21c452c3 and this
change on top.

| | before | this change |
|---|--:|--:|
| total Ir | 192.70 G | 190.02 G (−1.4%) |
| `ssaunits.grow_table`, inclusive | 6.13 G | 4.64 G |
| `ssaunits.grow_fields_of`, inclusive | 2.29 G | 0.04 G |
| `ssaunits.has_row`, inclusive | 0.49 G | 0.01 G |
| `ssarc.bracketed`, inclusive | 1.31 G | 0.13 G |

## Witnessed

The `Bracket*`, `Grow*`, `Lent*`, `Handback*`, `FieldAppend*`, `InPlace*`,
`ArrPushCliff*` and `SharedCount*` tests, `TestSelfHostSSAPhysicalRC*`,
`TestSelfHostSemanticReuseDifferential*`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostIRPerModuleDriver`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus` (54
tests, green), the lint ratchet, both emit identities and the sweep.

## Next

`carried_params` allocates a `bits(nvals)` and a work list per array operand
it walks (1.70 G); `lift_prod_mem` tests a memory op's kind by a chain of name
compares per op (1.16 G); `semsource.define` and `util.NameIndex.added`
copy on append where the state is shared (1.62 G and 1.22 G).
