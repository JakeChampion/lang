# Native deep-stack crash profiling

This branch is a measurement experiment, not a production rollout.

[Run 35037653826](https://github.com/JakeChampion/lang/actions/runs/35037653826)
at `99f40fc46054d6c001172ee50c592b3efa58e5b1` kept the original two deep-stack
tests, their 300,000-element inputs, 16 MiB stack limit and assertions. Both
passed. The successful programs took 24.421 and 33.163 ms. The expected
segmentation faults took 83.799 and 83.401 seconds, including about 13.3
seconds of child system CPU each. Whole tests took 83.86 and 83.48 seconds.
Thus the cost is in the crash path, not normal execution or compilation.

The runner had a zero core-file limit and a piped `systemd-coredump` handler.
Linux ignores that limit for piped dumps. Its per-process `coredump_filter`
selects memory mappings to include and survives fork/exec. See the
[Linux core manual](https://man7.org/linux/man-pages/man5/core.5.html).

## Controlled follow-up

Run baseline, candidate, candidate, baseline on one native runner with one
fixed test binary. The candidate changes only the expected-crash subprocess's
memory filter to zero. It does not change the successful program's filter,
the test process's filter, or the global core pattern. No root access is used.

The harness verifies actual child filter values, complete identical outcomes,
successful on-legs and segmentation faults in off-legs. A tiny exit-zero pilot
checks the full measurement pipeline and filter inheritance before selecting
both original deep-stack tests at full scale. The native comparison is still
required before claiming a speedup or selecting a production change.

Filtering mappings reduces crash-dump diagnostic content. It is deliberately
limited to the subprocess whose crash is the test's expected result. The
successful comparison retains normal diagnostics if it unexpectedly crashes.
Child CPU measurements do not include CPU used by the external dump handler.
