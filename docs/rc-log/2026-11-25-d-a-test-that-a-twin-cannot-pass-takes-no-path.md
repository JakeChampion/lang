# A test that a twin cannot pass takes no path

2026-11-25. Each target. The change is in `rccertify`.

## The gap

`arm64_native.macho_sect` had 44 unproved values, the most of any function.
It inlines a loop of pushes onto its borrowed parameter `buf` (`v0`). The
loop phi `v282 = phi v0 v294` starts as the parameter. A test against `v0`
selects the push: a copy where the array is still `v0`, and an owned push
where it is not.

In the first round, `v282` is a twin of `v0` with one side: it is `v0` on
each path. The edge where the test finds `v282 != v0` carries no path, and
`twin_on_edge` makes `v282` absent there. But the walk still ran the block of
that edge. Its owned push defined a result with one fresh unit, and the join
met that unit with the twin of the copy (0 where it is `v0`, 1 where it is
not). The meet was maybe, and the maybe went round the loop into each push
after it.

## The change

- An edge where a test of a twin against its key selects a side that the
  twin does not have carries no path (`twin_dead_on_edge`). The walk does
  not hand on such an edge, and the phi inputs of the edge are absent
  (`kill_edge`).
- A block that only such edges reach does not run in that round, and its
  edges carry no path too (`reached`).
- Each round decides again. When the twin gets its second side round the
  loop, the edge carries a path again.

`loop-pushes-onto-its-parameter` of `TestSelfHostRcCertifyCases` holds the
shape. Without the change it has 2 unproved values. Its variant without the
retain before the return stays unproved, as
`push-joins-its-parameter-unretained` does.

## Measured

The census of `fern_main` on x86-64, on the same tree, with the certifier of
main and with the change:

| function | before | after |
|---|---:|---:|
| `arm64_native__macho_sect` | 44 | 0 |
| `ssarawflow__reached` | 10 | 0 |
| `ssa__regalloc_linear_fixed_costs` | 8 | 6 |
| `wit_compose__wire_world` | 6 | 0 |
| `parser__parse_assign_chain` | 4 | 0 |
| `ownership__owned_root` | 3 | 1 |
| `checker__ocl_place` | 2 | 0 |
| `semndarray__uncopy` | 2 | 0 |
| all functions | 763 | 691 |

`wasm32/checker.unproved` goes from 91 to 85. Both rows are re-banked in
`.github/certify-baseline.txt`.

No function goes up, and no function has a finding. The mutant census of
`checker.fern` is the same with both certifiers: 5,475 refuted, 237
weakened, 219 missed.

## Next lead

`watbin__async_functype` now has the most unproved values: 24. Its loop phi
`v217` carries an array that `__fern_arr_concat` grows
(`v334 = __fern_arr_concat ... v278`). A test `v334 != v278` releases the
old box where the call made a new one. This is the concat form of the loop
above. Examine how the result of the concat meets the loop phi.
