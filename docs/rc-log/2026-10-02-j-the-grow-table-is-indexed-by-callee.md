# 2026-10-02 — the grow table is indexed by callee

`ssaunits.grow_table`, `grow_rows`, `dying_lent_rows`, `has_row_at`. Refs
#8171. No emitted byte changes: the stage0-built
compiler before and after emits the fixed older tree
(`examples/self_host/fern.fern` at 1ae9cad) and `checker.fern` byte for
byte, and the `selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0
differing, on the branch of #11038 at c7e86339.

## What the profile named

The previous entry's base sat 9 G above the one before it. Between main at
9cc02c45 and 219635ac, 38c60010 ("a call result lent the parameter is a link
of it") added `ssaunits.dying_lent_rows` to the grow-table fixpoint, and the
fixpoint's shape made it expensive: for every step of every plan, every
round, it found the step's block by scanning `f.graph.blocks` for the id
(`block_index`), and asked `has_row` about the module-wide table by scanning
every row. `grow_table` itself asked `has_row` the same way for every row a
body produced. On the whole-compiler emit: `grow_table` 4.75 G to 12.41 G,
of which `dying_lent_rows` 6.47 G, `block_index` 0.55 G to 4.38 G and
`has_row` 0.94 G to 2.99 G.

## What changed

- `grow_table` keeps a `util.NameIndex` over the table's callees beside it,
  one entry per row in order, and `has_row_at` walks the callee's chain
  instead of the table; `grow_rows` and `dying_lent_rows` take the index
  with the table. `has_row` stays for the per-body `with_row` and for
  `grows_buffer`, whose rows are a body's own.
- `grow_rows` resolves each step's instruction once, through the block
  position map main's 3619451b gave it (`ssa.block_positions`), and hands it
  to `dying_lent_rows`, which no longer looks the block up itself. The
  measurement below predates 3619451b, so its `block_index` row is that
  commit's share and this one's together.

The fixture that calls `grow_rows` on an empty table passes an empty index.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the branch of #11038 at c7e86339 with the
kind table (previous entry) and this change on top.

| | before | this change |
|---|--:|--:|
| total Ir | 205.28 G | 199.00 G (−3.1%) |
| `ssaunits.grow_table`, inclusive | 12.41 G | 6.13 G |
| `ssaunits.dying_lent_rows`, inclusive | 6.47 G | see `grow_rows`, 12.15 G to 6.02 G |
| `ssaunits.block_index`, inclusive | 4.38 G | 0.48 G (+0.25 G for the position map) |
| `ssaunits.has_row`, inclusive | 2.99 G | 0.49 G (+0.07 G `has_row_at`) |

## Witnessed

`TestSelfHostSSAUnits*`, the `Grow*`, `Lent*`, `Handback*`, `Bracket*`,
`FieldAppend*` and `InPlace*` tests, `TestSelfHostSSAPhysicalRC*`,
`TestSelfHostSemanticReuseDifferential*`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostIRPerModuleDriver`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus` (57 tests,
green), the lint ratchet, both emit identities and the sweep.

## Next

`grow_fields_of` and `grows_buffer` read the finished table from
`ssarc.bracketed` by scanning it (2.3 G and under 1 G); the index could
travel with the table. `carried_params` allocates a `bits(nvals)` per array
operand it walks.
