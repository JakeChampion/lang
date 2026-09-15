# Parallel whole-compiler emission batches

The shared `emitAllWholeCompiler` test helper emitted its batches serially.
In successful CI run 34979953900, the byte-identity gate spent 70.7 seconds
emitting 75 units in ten batches. The fixpoint gate spent 88.9 seconds on its
Go-built driver and 86.9 seconds on its self-built successor. These are
observed CI workloads, not predictions of the improvement below.

## Scheduling and memory

Independent batches now run through a worker pool bounded by `GOMAXPROCS`
and the harness's existing process-wide memory budget. Workers write disjoint
unit files and result slots. The parent waits for every worker before reading
the output inventory, reporting errors, linking, or allowing cleanup.

The batch size remains eight units, and the function-window budget remains
100. Driver arguments, per-batch exit diagnostics, unit inventory, sorted
link order, byte-identity comparisons and both bootstrap generations remain
covered. The worker pool creates no subtests inside shared compiler setup,
so existing caller subtest filters cannot accidentally omit compiler batches.

The two compiler generations require different reservations:

| Driver origin | Measured CI child peak | Reservation per batch |
| --- | ---: | ---: |
| Go-built | 2.43 GiB | 5 GiB |
| Self-built | 10.63 GiB | 16 GiB |

The self-built reservation covers its full arena. On a normal 16 GiB runner,
these large batches remain serial. Larger memory budgets can admit more.
Reservations coordinate scheduling; they are not OS memory limits.
The opt-in `FERN_SELFHOST_INTERP` path retains serial batches because its
whole-compiler memory footprint has not been measured.
A native two-batch probe with that mode flag and two available workers
confirmed one active child and byte-identical output to the serial baseline.

## Native measurement

Host: Mac15,6, Apple M3 Pro, 12 CPUs, 36 GiB RAM, Go 1.26.0 darwin/arm64.
The native ARM64 Darwin driver was built from main `394d0edd6` and emitted
x86-64 Linux assembly. The same driver, sources, batch size and helper were
used throughout. No emulation or generated programs ran during timing.

A 16-unit, two-batch race pilot produced identical manifests with one and
two workers. A third pilot used the 16 GiB reservation with two available
workers: it measured one active child under the 12,000 MiB fallback budget
and produced the same bytes.

After those pilots and all other local work finished, four fresh non-race
test processes ran the full 75-unit workload in 1/2/2/1 worker order.
`GOMAXPROCS` selected worker count; the default memory budget admitted two
5 GiB reservations. All output filenames, byte counts and SHA-256 hashes
matched across all four trials.

| Workers | Batch wall seconds | Whole process seconds | Whole process CPU seconds | Maximum child RSS bytes |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 50.522 | 56.43 | 55.50 | 3862724608 |
| 2 | 26.236 | 31.41 | 56.97 | 2722824192 |
| 2 | 26.099 | 31.30 | 56.79 | 2880503808 |
| 1 | 50.243 | 55.59 | 55.29 | 4386783232 |

Mean batch execution is 1.93x faster. Including staging, planning and output
verification, the process is 1.79x faster, with 2.7% more CPU time. Compiler
construction and Go test-binary compilation are excluded. Maximum child RSS
comes from each subprocess's resource usage; it is not aggregate concurrent
memory. These measurements do not establish a whole-CI speedup.

The retained parallel output also linked into a Linux x86-64 compiler. Under
QEMU, that compiler emitted a separate program which assembled, linked and
exited with the expected status 7. This is correctness evidence only.

## Validation and reproduction

Package vet and full source lint pass. The existing complete two-generation
fixpoint passed on Linux ARM64 with QEMU x86-64, race detection, four available
workers and a 12,000 MiB memory budget. All 75 units were byte-identical
between generations, with no OOM. Maximum child RSS was 2.62 GiB for gen0
and 11.65 GiB for gen1. The 767.670-second package time is correctness
evidence, not a native performance measurement.

After rebasing onto main `5a1247235`, a native ARM64 Darwin verifier rebuilt
from those compiler sources passed a 16-unit serial/parallel pilot and then
the full 75-unit comparison with race detection. Filenames, sizes and SHA-256
hashes match exactly between one and two workers. The full runs observed one
and two active children respectively. Source lint, package vet, formatting
and test selectors also pass on the updated tree. These are integration
checks; the controlled timing results above retain their recorded revision,
and the updated tree still requires the complete CI bootstrap gates.

On a native Linux x86-64 host, run:

```sh
GOMAXPROCS=4 FERN_BUILD_MEM_BUDGET_MB=12000 \
  go test ./internal/e2eselfhost \
  -run '^TestSelfHostPerModuleEmitAllFixpointX86_64$' \
  -race -count=1 -v -timeout 30m
```

For performance comparisons, use the same warmed driver cache and compiled
test binary, alternate `GOMAXPROCS=1` and `GOMAXPROCS=2`, and retain the same
memory budget. Capture whole-test time, batch time, CPU use, concurrent
memory, unit counts and byte identity. QEMU runs are correctness-only.
