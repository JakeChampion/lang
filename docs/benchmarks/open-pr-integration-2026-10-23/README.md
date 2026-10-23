# Open PR integration performance

[Perf run 40015080180](https://github.com/JakeChampion/lang/actions/runs/40015080180)
measured combined source commit `0b03aff20426b3ccc831a337f03de58884a9bc67`.
This temporary integration includes the fourteen pull requests open when the
merge task began, with their conflict resolutions and focused fixes.

The native reports retain the exact instruction counts and emitted-text counts
on x86-64 and aarch64. We use these reports to set the deterministic baseline
rows. The per-process string hash seed keeps its current baseline and
tolerance. The strict comparator passes for both reports after we update the baselines.

The combined decoder retires 93,261,573 instructions on x86-64 and 92,625,615
on aarch64 for this benchmark input. This includes the fast decoder change.
The static aarch64 sort_ints count is 2,072 instructions, 1.22% above the old
baseline. Its dynamic count is 33,602,359. The reports include both figures.

`selfhost-new-benchmarks.txt` retains the measured emitted sizes of the newly
added deflate benchmarks and the replaced sort_inplace benchmark on all three
targets. Other earlier advisory size rows are outside this baseline update.

`selfhost-cold-calls.txt` records the 20 ARM rows that the cold-call change affects.
Their values use the combined source report.
`alloc-report.txt` records 28,679,178 allocations and the same number of frees.
These counts describe that earlier source snapshot.

These reports describe the combined source scope.

Apply the values to the branch only after it contains these sources.
Validate that branch in CI.

[Perf run 40026009089](https://github.com/JakeChampion/lang/actions/runs/40026009089)
measured source commit `160186731f229749a09aaaaf288e5e82c2767189`.
This source includes the receiver diagnostic fix and the latest FIP table.
All native gates pass. The emitted-size report has no changes from the earlier run.

`latest-alloc-report.txt` records 28,702,174 allocations and the same number of frees.
The source growth adds 22,996 events, or 0.08 percent, to the earlier total.
`latest-cliff-report.txt` records 30,973,448 copied bytes and 270,615 append crossings.
The source growth adds 5,712 bytes and nine crossings to the earlier report.
The allocation and cliff baselines use these latest counts.
`latest-selfhost-size-report.txt` retains the full emitted-size report.
