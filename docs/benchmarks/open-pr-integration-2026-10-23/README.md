# Open PR integration performance

[Perf run 40015080180](https://github.com/JakeChampion/lang/actions/runs/40015080180)
measured combined source commit `0b03aff20426b3ccc831a337f03de58884a9bc67`.
This temporary integration includes the fourteen pull requests open when the
merge task began, with their conflict resolutions and focused fixes.

The native reports retain the exact instruction counts and emitted-text counts
on x86-64 and aarch64. Deterministic baseline rows are banked from those
reports. The per-process string hash seed keeps its existing baseline and
tolerance. The strict comparator passes for both reports after banking.

The combined decoder retires 93,261,573 instructions on x86-64 and 92,625,615
on aarch64 for this benchmark input. This includes the fast decoder change.
The static aarch64 sort_ints count is 2,072 instructions, 1.22% above the old
baseline; its dynamic count is 33,602,359. Both figures are recorded explicitly.

`selfhost-new-benchmarks.txt` retains the measured emitted sizes of the newly
added deflate benchmarks and the replaced sort_inplace benchmark on all three
targets. Other pre-existing advisory size rows are outside this baseline update.

These reports describe the combined source scope. Apply their values to a
landing branch only after it contains these sources, and validate that branch
in CI.
