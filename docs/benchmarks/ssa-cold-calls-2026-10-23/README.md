# Cold-call performance on the CI runners

[Perf run 40017692452](https://github.com/JakeChampion/lang/actions/runs/40017692452)
measures commit `547f841d442ed6a3529c08480cf5a8b619992159`.
The native reports cover x86-64 and aarch64.
Both strict native comparisons pass after we update the deterministic rows.
The string hash seed keeps its baseline and tolerance.

The aarch64 pmap_insert count is 65,826,289 retired instructions.
The aarch64 pvec_with count is 59,217,299 retired instructions.
These improvements are 1.13% and 1.08% relative to their previous baselines.

The self-host reports retain the emitted sizes, allocations, and append-cliff counts.
To identify the rows this PR changes, we compare with
[run 40010926319](https://github.com/JakeChampion/lang/actions/runs/40010926319)
at `49c290377dfe6a930d3939efa99126c32be79880`.
That reference has the same compiler and standard library as main at `3968020c9`.
Its common benchmark inputs also match.
The two extra deflate inputs are outside this comparison.

The comparison identifies 20 ARM emitted-size rows.
Their baselines use the measured values.
The allocation and free counts each change from 28,014,277 to 28,023,029.
The difference is 8,752 allocations, or 0.031%.
The append-cliff counts do not change.
Earlier advisory size differences remain outside this update.
