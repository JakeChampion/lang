# 2026-10-02 — the plan carries its definitions

`ssaunits.Plan.defs`, `ssaunits.carried_params`, `ssa_lift.KindTable`.
Refs #8171. No emitted byte changes: the stage0-built compiler before and
after emits the fixed older tree (`compiler/fern.fern` at 1ae9cad)
and `checker.fern` byte for byte, and the `selfhost-emit-hashes` sweep is
1,965 rows per compiler with 0 differing, against #11080's head at
c416b990 and again, rebased, against main at 189a9417.

## What the profile named

Three per-call rebuilds of a table that does not change between the calls:

- `ssaunits.definitions` builds a function's value-to-instruction table,
  an nvals-long array filled by append and then one pass over the
  instructions. Five callers each built their own: the planner, its unit
  verifier, `owned_values` through `phi_ownership`, `view_retain_error`,
  and `grow_rows`, which is called once per plan per round of the grow
  fixpoint. 249 k builds, 1.45 G.
- `ssaunits.carried_params` built a `bits(nvals)` set per array operand it
  walked, 137 k of them: 1.27 G in `bits`.
- `ssa_lift.lift_prod_mem` asked `prod_host_kind` and `prod_call_argc`,
  twice, per memory op; each builds its name lists as array literals and
  scans them. 322 k ops, 0.73 G in the two.

## What changed

- `Plan` carries `defs`. The planner builds the table once, hands it to
  `owned_values`, `phi_ownership` and `view_retain_error`, and the unit
  verifier and `grow_rows` read the plan's.
- `grow_rows` builds one clear scratch set per call and `carried_params`
  takes it owned, marks what it visits, clears the same entries off its
  work list, and hands it back beside the parameters (`Carried`). The
  dying-operand path, which is rare, still builds its own.
- `KindTable` gains `argc` and `host`, each kind's `prod_call_argc` and
  `prod_host_kind` answer read once per module; `lift_prod_mem` takes the
  table and the tag and reads them.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from #11080's head at c416b990 and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 171.19 G | 168.22 G (−1.7%) |
| `ssaunits.definitions`, inclusive | 1.45 G | 0.09 G |
| `ssaunits.carried_params`, inclusive | 1.70 G | 0.52 G |
| `ssaunits.grow_table`, inclusive | 4.64 G | 2.81 G |
| `ssa_lift.lift_prod_mem`, inclusive | 1.16 G | 0.45 G |

## Witnessed

The lift and planner set (`TestSelfHostSSALift*`, `TestSelfHostSSAUnits*`,
`TestSelfHostSSAPhysicalRC*`, `Bracket*`, `Grow*`, `Lent*`, `Handback*`,
`FieldAppend*`, `InPlace*`, `ArrPushCliff*`, `SharedCount*`,
`TestSelfHostSemanticReuseDifferential*`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostIRPerModuleDriver`,
`TestSelfHostConstAggregateIRX86_64`, `TestSelfHostArm64LinuxBuilds`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus`; 63 tests,
green), `make check-sources`, the lint ratchet, both emit identities and
the sweep.

## Next

`semsource` threads its `State` borrowed through every one of its
producers, so `define` copies the values array per value (1.62 G) and the
copy's drop releases every type in it (2.11 G under
`__sem_drop_semsource__State`); `with_record` pays `NameIndex.added`'s
copy per record (0.89 G). An owning cascade from `define` and `emit` stalls
after four rounds on 45 E051 sites that move the state out of a borrowed
`Value` and 41 E050 reads of a state already moved; `Value { s, id }` is
the shape to change first. Smaller: `fnsigs.push_str_unique` dedupes its
accumulators by scan (0.99 G); `checker.sig_bucket_from` and
`ownership.find` hash long names per lookup (1.53 G and 1.49 G).
