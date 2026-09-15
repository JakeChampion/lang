# Native ARM64 test workers

Run the existing native ARM64 test selection in two isolated processes on
the existing runner. Compile the test binary once. Split its actual top-level
test inventory into disjoint groups, then execute both groups concurrently.
Divide the runner's CPU budget between processes through `GOMAXPROCS` and
`-test.parallel`. No additional CI jobs or dependency barriers are introduced.

Several expensive fixture comparisons mutate compiler package globals,
including ownership and reference-counting switches. Adding `t.Parallel`
to those tests would race their configurations. Separate processes retain
that isolation while using more of the runner's cores.

## Measurement

Native Linux ARM64 Docker on an M3 Pro host, four CPUs and a 12 GiB memory
limit, Go 1.26.8. A single prebuilt test binary and copied source checkout
were held fixed across all trials. The input tree matches `d46d14918`, based
on `b4c04aee2` with the test-binary cache candidate applied (tree hash
`6bc9b0b85585639fddcfda81aa77aeccf68ec3c6`). Compilation is outside the interval.
Small Git operations and read-only analysis occurred on the host during the
experiment; there were no concurrent local builds or tests.

A ten-test pilot validated the full pipeline in under one minute before
scaling to the complete `^TestArm64` inventory. Only selection scale changed.
The full experiment used the order one worker, two workers, two workers,
one worker. The total CPU budget remained four in every trial.

| Workers | Wall seconds | Child CPU seconds | Container peak bytes |
| ---: | ---: | ---: | ---: |
| 1 | 351.068925 | 613.799410 | 486649856 |
| 2 | 207.759828 | 553.873378 | 651456512 |
| 2 | 205.656349 | 555.643818 | 559157248 |
| 1 | 326.450848 | 568.691645 | 481681408 |

Mean wall time fell from 338.760s to 206.708s: 1.639x throughput, or 38.98%
less test execution time. This is a local test-phase measurement, not an
end-to-end CI reduction. It excludes build, upload and queue costs. The
experiment used a prototype runner; validate the production runner separately.

All four trials selected 612 top-level tests, with 598 passes and 14 skips.
Every one of the 4,931 test/subtest outcomes matched across all trials. Nine
skips were Apple Silicon execution cases; two required clang/llvm-dwarfdump
absent from the local image. Three terminal tests incorrectly skipped native
ARM64 because they mistook an empty emulator command for unavailable tooling.
Those gaps are tracked separately; matching outcomes does not establish
coverage of the skipped tests.

## Failure and coverage handling

The runner rejects empty, malformed or duplicate inventories. It preserves
the selection of all top-level tests and their subtests. Each worker gets
the same environment, working directory and ten-minute test timeout as the
original invocation, with its CPU budget set explicitly.

`gotestsum` retains compact failure diagnostics in the job console and full
JSON in separate files. Verification requires a single successful package
outcome and a terminal outcome for every selected test and every started
subtest. Unexpected, duplicate, missing, unfinished or failed tests fail the
job. Skipped tests remain visible as skips. A subprocess failure is fatal even
if its output claims success. Cancellation kills the process group on Linux
and macOS, including test and compiler descendants.

Per-worker inventories, CPU budgets, wall times, outcomes and errors are
retained with full test JSON for seven days. No results are reused: every
selected test executes with `-test.count=1`. The production rollout must
measure build and artifact overhead as well as final job completion before
making an end-to-end savings claim.

## Production runner validation

Table-driven tests cover inventory partitioning, malformed input, duplicate
or missing outcomes, unfinished subtests and package failures. Real gotestsum
subprocess tests exercise pass, failure and crash behavior and verify worker
CPU budgets. A cancellation test checks that a child retaining the output
pipe is terminated with its process group. The package also passes Go's race
detector.

The production runner then passed a native Linux ten-test pilot and the full
612-test inventory using the same fixed input tree as the experiment. Every
one of the 4,931 outcomes matched. Worker completion times were 193.786s and
213.581s. Other local validation ran during this integration check, so these
times are not another controlled performance comparison.
