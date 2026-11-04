# 2026-11-04: skip AST map visitors for unrelated nodes

An AST map visitor returns a boxed triple even when its expression stays
unchanged. The map now accepts a predicate before the visitor call. Asset
and target-name passes select calls. Literal-branch pruning selects lambdas.
Other passes retain a predicate that admits every expression. Recursive
descent, statement callbacks and accumulator order remain the same.

Main already contains the shared last-use, flat-edge, name and integer
repairs from #14190. This integration retains those implementations and
adds the filtered map visits. The generic-function census stays at 16.

Two x86-64 leak-check compilers use the same qualified producer. One contains
main 05a98461. The other contains the integrated source. Both compile the
candidate's checker and imports, so the subject stays fixed.

| Subject | Main compiler allocations | Filtered compiler allocations |
|---|---:|---:|
| Minimal program | 21,381 | 21,379 |
| Candidate checker | 36,293,688 | 35,901,919 |

Every count balances with an equal free count and zero live bytes. Each pair
emits identical assembly. These are allocation events under QEMU, with no
native timing claim. The small control passes before the checker campaign.

The allocation limit decreases from the earlier branch value of 35,915,045
to the measured 35,901,919. It remains below main's 36,037,176 limit.

The separate stdlib scheduling change retains its measured 1 GiB case
weight. The earlier 246-case run passed on a 4-core, 16 GB host with four
cases in flight. Its recorded duration is 1,355.790 seconds.
