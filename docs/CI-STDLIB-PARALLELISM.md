# Parallel stdlib end-to-end validation

`TestSelfHostStdTestE2E` and its ARM64 counterpart run the same 148 cases.
Each case runs the Go reference interpreter, compiles through the Fern
selfhost compiler, assembles and links the result, then compares its output
and exit code with the interpreter. The synthetic failing suite still checks
the failure path, and the existing per-case output normalization is unchanged.

## Change

Independent cases run with `t.Parallel()`. Each has a separate output directory;
the driver and interpreter binaries are immutable shared inputs. Filesystem
fixtures create temporary directories. The one fixed-path buffered-I/O fixture
runs only once in each target group, and the two target groups remain sequential.
The synchronous `cases` group keeps the complete wall time in the top-level
test duration consumed by `scripts/ci-test-weights`.

Every subprocess reserves 5 GiB against the harness's existing process-wide
memory budget. The full native corpus observed a 4.25 GB maximum child RSS;
the reservation leaves room above that observation.
It limits concurrency on smaller hosts while allowing Go's `-parallel` setting
to use additional cores where the budget permits. This is a reservation based
on measurements, not an OS memory limit. Linking uses its own reservations,
so subprocess reservations are released before linking to avoid nested waits.

## Evidence and reproduction

A successful native CI run, 34936726276, spent 157.01 seconds in the x86
stdlib test. Its 148 cases account for 154.46 seconds. This is the baseline
workload motivating the change, not a predicted CI speedup.

On a Mac15,6 with 12 CPUs and 36 GiB RAM, a native two-stage benchmark ran
all 148 inputs through the Go interpreter and an ARM64 Darwin build of the
selfhost compiler emitting x86 assembly. Go was 1.26.0. Both binaries used
the same compiler sources as main 20507ffae; the benchmark executes neither
generated x86 programs nor QEMU. Separate full-corpus trials measured:

| Workers | Wall seconds | Child CPU seconds | Maximum child RSS bytes |
| ---: | ---: | ---: | ---: |
| 1 | 89.630 | 100.163 | 4245028864 |
| 2 | 50.116 | 106.834 | 4186832896 |

That is 1.79x faster for the measured interpreter-plus-compilation workload,
with 6.7% more CPU time. Maximum child RSS is the largest single child, not
aggregate concurrent memory. This is one full-corpus comparison, not a
whole-CI or generated-program execution speedup.

All 148 assembly hashes and oracle exit statuses matched between trials.
The two timing-comment cases were normalized; 147 oracle output hashes
matched. The remaining case emits a random golden-file path, which the
actual Go gate already normalizes. Applying that same normalization in a
separate repeat of the case produced matching hashes. The benchmark script
now reads every normalization prefix from the Go case table.

The macOS oracle reports the buffered-I/O case's two `/dev/full` checks as
failed because that device is Linux-specific. This is recorded alongside
the deliberately failing synthetic suite, not counted as successful target
validation. The full Linux end-to-end tests must pass independently.

The initial three-case race pilot passes on both x86-64 and ARM64. It covers
arithmetic, filesystem operations and the expected-failure suite. Local x86
programs execute under QEMU in the Linux ARM64 devbox, so local correctness
durations are not native performance evidence.

For target correctness, run:

```sh
scripts/devbox env FERN_SELFHOST_BUILD_CACHE=/work/build/ci-stdtest-cache \
  go test ./internal/e2eselfhost \
  -run '^TestSelfHostStdTestE2E(Arm64)?$/^cases$/^(arithmetic|filesystem_ops|synthetic_fail)$' \
  -race -parallel 4 -count=1 -v
```

Then scale only the case selector to all cases, retaining both targets:

```sh
scripts/devbox env FERN_SELFHOST_BUILD_CACHE=/work/build/ci-stdtest-cache \
  go test ./internal/e2eselfhost \
  -run '^TestSelfHostStdTestE2E(Arm64)?$' \
  -race -parallel 4 -count=1 -v -timeout 30m
```

For an actual CI timing comparison, run the same test binary and cache state
on the same native Linux host with `-parallel 1` and `-parallel 4`. Capture
test2json output, total CPU use and peak memory, and compare every subtest's
verdict. Use the top-level elapsed time; parallel children's durations overlap.
Do not change shard weights from QEMU timings or compilation-only measurements.

## Rejected interpreter pilot

A CPU profile of the persistent-map oracle attributed 72.54% of sampled CPU
to recursive reference-count adjustment. Skipping zero-delta adjustments
did not improve this workload: native baseline trials took 8.7063 and 8.6191
seconds; modified trials took 9.2275 and 8.6998 seconds, with identical output.
That change was discarded. Exact nested ownership counts remain unchanged.
