# 2026-10-02 — five scans become lookups

`semsource.contracts`, `checker.redeclares`, `checker.OwnFuncs`,
`ssaunits.grow_rows_agree`, `ssarc.prune`. Refs #8171. No emitted byte
changes: the stage0-built compiler before and after emits the fixed older
tree (`examples/self_host/fern.fern` at 1ae9cad) and `checker.fern` byte for
byte, and the `selfhost-emit-hashes` sweep is 1,965 rows per compiler with
0 differing, against main at 5b933cc3.

## What the profile named

After the previous entry the self-cost ranking is led by the lift, the
hash and the runtime, and then by five scans that each walk a whole table
per question:

- `semsource.contracts` asked `ssasem.find_contract_in` whether a
  declaration's or a builtin's contract was already in the table, once per
  declaration per round and once per builtin: 2.22 G, 29 k calls, most of
  them the builtins, which are the bulk of every module's table.
- `checker.redeclares` compared each function against every earlier one
  for E006: 2.18 G under `collect_decl_diags`.
- `checker.ow_is_own_func` and `ow_func_flags` scanned the own-taking
  functions by name per call expression: 1.48 G across `ow_guard_call`,
  `ow_call_arg_flags` and `lu_call`.
- `ssaunits.grow_rows_agree` asked `names_has` whether each row's callee
  was among a body's callees, per row of both tables: 1.08 G, 21 M calls.
- `ssarc.prune` found each step's block by scanning the function's blocks,
  per supply: 0.71 G in `block_index` under `linked_supply`.

## What changed

- `contracts` indexes the table's names once per round and asks that; a
  key several declarations share also checks the round's own additions,
  so a repeated key is still taken once, in the order it always was. The
  builtins are indexed once, and a builtin joins when no declaration and
  no earlier builtin has its name. (A first version grew one index with
  `NameIndex.added` per contract: on a borrowed receiver `added` copies
  the names array, and 18 k copies of a table thousands of names long cost
  more than the scans they replaced.)
- `collect_decl_diags` indexes the module's function names once and
  `redeclares` walks the chain of the function's own name, which stays
  ascending, so it stops at the function itself.
- `collect_own_funcs` returns `OwnFuncs`, the rows with a name index, and
  `OwnCtx`, `LuFn` and the signatures that carried `OwnFunc[]` carry it;
  `ow_is_own_func` and `ow_func_flags` are a lookup.
- `grow_rows_agree` iterates the callees and walks each table's chain for
  the callee (`rows_within`); `names_has` is gone.
- `prune` maps block ids to positions once (`ssa.block_positions`) and
  `linked_supply` reads the map.
- `ssa.index_of_i32`, a copy of `util.index_of_i32` with no callers, is
  deleted.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from #11059's merged head at a65ed37f and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 181.45 G | 173.62 G (−4.3%) |
| `semsource.contracts`, inclusive | 2.15 G | 0.15 G |
| `checker.collect_decl_diags`, inclusive | 2.30 G | 0.27 G |
| `checker.ow_is_own_func` + `ow_func_flags`, inclusive | 1.94 G | 0.08 G |
| `ssaunits.grow_rows_agree`, inclusive | 1.38 G | 0.01 G |
| `ssarc.prune`, inclusive | 0.88 G | 0.22 G |

## Witnessed

The checker, planner, bracket and semantic set (`TestSelfHostChecker*`,
`TestSelfHostSemantic*`, the own diagnostics tests, `Bracket*`, `Grow*`, `Lent*`, `Handback*`,
`FieldAppend*`, `InPlace*`, `ArrPushCliff*`, `SharedCount*`,
`TestSelfHostSSAPhysicalRC*`, `TestSelfHostSSAUnits*`,
`TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostIRPerModuleDriver`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus`; 112 tests,
green), `make check-sources`, the lint ratchet, both emit identities and
the sweep. The for..in census floor moves to the measurement, 1979: the
four scans this entry deletes were for..in loops.

## Next

`ssaunits.carried_params` builds a `bits(nvals)` set per array operand it
walks (1.27 G in `bits`); a dedupe on its work list instead costs more, as
the lists run to hundreds of values, so the fix is a cheaper set, not no
set. `util.NameIndex.added` copies the names array on its borrowed
receiver, and `semsource.with_record` pays for that per record (0.79 G);
an index that grows in place wants an owned receiver, and the parser reads
`own` on a receiver without recording it. `semsource.define` copies the
state's values array per value for the same reason (1.62 G), the whole of
`semsource` threading its `State` borrowed.
