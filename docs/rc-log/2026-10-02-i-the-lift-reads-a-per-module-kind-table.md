# 2026-10-02 — the lift reads a per-module kind table

`ssa_lift.kind_table`, `lift_impl`, `asmcore.EmitState.kinds`. Refs #8171. No
emitted byte changes: the stage0-built compiler before and after emits the
fixed older tree (`examples/self_host/fern.fern` at 1ae9cad) and
`checker.fern` byte for byte, and the `selfhost-emit-hashes` sweep is
1,965 rows per compiler with 0 differing, on the branch of #11038 at
c7e86339.

## What the profile named

`ssa_lift.lift_impl` classified each op kind the first time a function met
it: `kind_class` built and scanned four name lists (`prod_kind`,
`prod_mem_kind`, `prod_float_kind`, `bin_sym`), about 5.5 k instructions a
call, and the per-function cache meant the same kind was classified once per
function that used it. 286 k calls over 18.2 k functions: 1.57 G, with
another 0.2 G in the cache's `with` writes.

## What changed

`ssa_lift.KindTable` holds every kind's name and class by id, built once by
`kind_table()` (the same census as `ssa_lift_admits_run.fern`: every id below
1024 that `kind_name` answers for, since the ids run past `kind_count()`).
`EmitState` carries it from `new_state`, `unit_leaves` builds one for its
loop, and `lift_from_ir_prod` and `lift_impl` take it and read a kind's name
and class by tag; the lazy per-function cache and its `top_tag` scan are
gone. A tag outside the table is the same refusal a negative tag was.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the branch of #11038 (main at 219635ac
with its two commits) and this change on top.

| | before | this change |
|---|--:|--:|
| total Ir | 207.09 G | 205.28 G (−0.9%) |
| `ssa_lift.lift_impl`, inclusive | 11.39 G | 9.52 G |
| `ssa_lift.kind_class`, inclusive | 1.58 G | 0.03 G |

## Witnessed

`TestSelfHostSSALift*` (the two block-id fixtures now pass the table),
`TestSelfHostSSAPhysicalRC*`, `TestSelfHostSSAUnits*`,
`TestSelfHostIRPerModuleDriver`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostConstAggregateIRX86_64`, `TestSelfHostArm64LinuxBuilds`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus`, the lint
ratchet, both emit identities and the sweep. `TestSelfHostCLIX86_64`'s
`wasm-component-rejects-unsupported` case fails on main at b23034a8 as it
does here.

The base is 9 G above the previous entry's 198.10 G: main between 9cc02c45
and 219635ac added `ssaunits.dying_lent_rows` (6.47 G) and grew
`ssaunits.block_index` (0.55 G to 4.38 G) and `has_row` (0.94 G to 2.99 G)
under `grow_table`, which went from 4.75 G to 12.41 G. That is the next
entry's subject.

## Next

`lift_impl`'s own body is the largest self cost on the profile at 7.0 G over
18.2 k functions, 385 k instructions each, which no callee accounts for:
that is the stack-machine replay itself, and a line profile (the self-host
emits no DWARF lines) is what would split it. `lift_prod_mem` 1.16 G over
322 k calls is the same chain of name compares per memory op.
