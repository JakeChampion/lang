# Native x86 test workers

The native x86 backend suite uses two independent processes through the same
`ci-test-workers` runner as the ARM64 suite. The runner inventories and assigns
every selected top-level test once, divides the available CPU budget between
workers, and checks complete terminal outcomes. Each process retains its own
test-package state, including tests that change environment or working directory.

The selector remains `^TestX86_64`. Each worker receives a 25-minute test
timeout; both run concurrently under one shared 25-minute-30-second deadline
that also bounds inventory collection. Native assembler fixtures and the
seccomp corpus keep their existing execution paths. Worker outcomes are
retained for seven days on both success and failure.

## Controlled native measurement

[Run 35022735930](https://github.com/JakeChampion/lang/actions/runs/35022735930)
used fixed revision `7daaa5f70c850496e76f587ecd2fc332a97345cf` on a four-CPU
Linux x86-64 host. Order was baseline, two workers, two workers, baseline.
The baseline used the production Go/gotestsum path. Every trial used the same
source, toolchain, and selected tests; two workers share four CPUs.

| Mode | Wall seconds | CPU seconds |
| --- | ---: | ---: |
| Baseline 1 | 805.239 | 1397.487 |
| Two workers 1 | 602.517 | 1206.712 |
| Two workers 2 | 615.232 | 1238.736 |
| Baseline 2 | 804.620 | 1400.248 |

Each trial completed 516 parents and the same 4,697 unique outcomes. Four
existing skips were identical: an unsupported ownership-model case, the
opt-in heap benchmark, and two old SSA refusal cases now covered elsewhere.
No additional test was skipped or omitted by the worker path.

Mean suite time fell from 804.930 to 608.875 seconds. This measures the native
suite, not checkout/setup, artifact overhead, queue time, or whole-CI latency.
The artifact `native-x86-worker-benchmark-full-1` includes environment metadata,
raw test events, worker summaries, and the four-trial summary.

Production uses the subsequently merged signal-environment correction from
PR #9403. Its runner tests check cancellation, signal inheritance, real process
exit status, CPU allocation, test assignment, and complete outcomes. Validate
the current-head rollout in full CI before merging this workflow change.
