# Fern against Koka after the static release

2026-11-17. x86-64. No code changes. This entry records the three workloads
where Fern was slower than Koka before #16352, #16371 and #16379.

## Measured

User time on this Skylake-derived Xeon, alternating runs. The Koka binaries
are the ones that `bench/perceus/koka` builds. The old Fern is main at
`50625de8a8`, and the new Fern is main after #16379.

| workload | Koka | old Fern | new Fern |
|---|---:|---:|---:|
| binarytrees, depth 21, median | 15454 ms | 17190 ms | 14501 ms |
| binarytrees, depth 21, best | 15028 ms | 16914 ms | 13525 ms |
| msort, 1e6, median | 520 ms | 497 ms | 487 ms |
| qsort, 1e6, median | 583 ms | 596 ms | 592 ms |
| qsort, 1e6, best | 496 ms | 530 ms | 529 ms |

Fern is now faster than Koka on binarytrees and on msort. qsort is the one
workload of the five where Koka is still faster, by 2% in the median and
by 7% in the best run.

## Where qsort spends its time

The PC sampler gives 92% of the samples to `sort`, into which the compiler
inlines `partition` and `append`. Most of those samples fall on one branch in
the loop of `partition` and on one in the loop of `append`. Each branch
comes after the load of the tag of the next node, and that load misses the
cache. So the time of qsort
depends on where the nodes are in memory, more than on the instructions of
the loop.

The loop of `partition` still has two tests that the path of a unique node
does not need. The token is tested against null, and its shape is compared
with the shape of the new node. `ssa.split_tokens` cannot remove them,
because the test of `x < p` is between the phi of the token and the tests.
The arms of the uniqueness test also load the tail, so the join has two phis.

## Next

- Make the token split go through a branch that does not read the token,
  and accept a join with more than one phi.
- Compare the placement of the nodes of qsort with the placement that the
  allocator of Koka gives.
