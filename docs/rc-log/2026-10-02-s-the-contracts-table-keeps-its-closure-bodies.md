# 2026-10-02 — the contracts table keeps its closure bodies

`ssasem.Contracts.envs`, `semsource.env_rows`,
`asmcore.infer_call_method_type`. Refs #8171. No emitted byte changes: the
stage0-built compiler before and after emits the fixed older tree
(`examples/self_host/fern.fern` at 1ae9cad, against that tree's stdlib) and
that tree's `checker.fern` byte for byte, and the `selfhost-emit-hashes`
sweep is 1,965 rows per compiler with 0 differing, against the previous
entry's tree.

## What the profile named

- `semsource.env_rows` found the closure bodies a function type can hold
  by asking every contract in the module's table whether its first
  parameter is an environment, once per function type reached, per round
  of `env_rows_closed`: 35.7 M `is_env` calls, 2.33 G, for the few hundred
  contracts that are closure bodies.
- `asmcore.infer_call_method_type` fell through its builtin arms to a scan
  of every declaration for a method of the field's name: 1.49 G, almost
  all of it in that loop, for 20 k calls.

## What changed

- `Contracts` carries `envs`, the rows whose first parameter is an
  environment, built by `contracts_of` in the list's order; `env_rows`
  walks that.
- The method scan walks the chain of the field's name in the state's
  function index, which stays ascending, so it meets the same first match.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 166.90 G | 163.68 G (−1.9%) |
| `semsource.env_rows_closed`, inclusive | 6.83 G | 4.96 G |
| `semsource.env_rows`, inclusive | 2.33 G | 0.46 G |
| `semtypes.is_env`, inclusive | 1.05 G | 0.06 G |
| `asmcore.infer_call_method_type`, inclusive | 1.49 G | 0.14 G |

## Witnessed

The checker, planner, semantic, closure and method set (`TestSelfHostChecker*`,
`TestSelfHostSemantic*`, the own diagnostics tests, `Bracket*`, `Grow*`,
`Lent*`, `Handback*`, `FieldAppend*`, `InPlace*`, `ArrPushCliff*`,
`SharedCount*`, `TestSelfHostSSAPhysicalRC*`, `TestSelfHostSSAUnits*`,
`TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostIRPerModuleDriver`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus`, and every
test naming closures, lambdas, environments, methods or inference; 267
tests, green), `make check-sources`, the lint ratchet, both emit identities
and the sweep.

## Next

`env_rows_closed` still costs 4.96 G: `schemas` and the two
`named_by_schemas` walks per round. Taking `out` owned down the schema
walk (`schema_of`, `with_record`, `with_enum`, `enum_members`, the four
`*_entry` helpers) was measured and dropped: 163.64 G to 164.29 G, with
`env_rows_closed` up to 5.61 G and the `Schemas` drop doubled, since each
spread into a new `Schemas` releases the old one field by field whether or
not its arrays were shared. `checker.Scope.lookup` scans the flat
scope newest first and builds an error string on each of its 1.4 M misses
(1.38 G); `checker.ow_type_is_pointer_name` scans the module's structs per
call (1.0 G); `fnsigs.push_str_unique` dedupes its accumulators by scan
(0.99 G); `checker.sig_name_bucket` is asked 4.6 M times, most of them by
`UnionTable.variant_chain_head`, `Scope.lookup_sig` and `has_sig`
(1.45 G).
