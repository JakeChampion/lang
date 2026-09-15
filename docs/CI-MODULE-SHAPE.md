# One query for whole-compiler planning

`planWholeCompilerUnits` previously started the compiler three times: module
count, post-lift function counts, and the module manifest. The planner used
only the manifest's namespaces, paying for signature hashing it discarded.

The driver now accepts `-per-module-shape`, returning ordered
`namespace|function-count` rows immediately after lambda lifting. The existing
`-per-module-func-counts` query also returns at this point. Both avoid building
emission side tables. Legacy metadata flags remain available.

The Go planner reads the shape once, then independently computes windows
from the function counts and staged source sizes. The compiler still computes
its own unit windows. Neither implementation substitutes the other's final
unit list, preserving the existing whole-compiler cross-checks. Malformed,
negative-count and duplicate-namespace rows are rejected.

## Controlled native measurement

Host: Apple M3 Pro, 12 CPUs, 36 GiB RAM, Go 1.26.0 darwin/arm64. Both drivers
ran natively and queried identical whole-compiler sources. The baseline driver
was built from `394d0edd6`; the changed driver was built from `32ccc2bc5` with
this patch. There are no compiler-source changes between those base commits.
Compiler construction, test-binary compilation and source staging are outside
the measured query interval. No other local builds or tests ran during timing.

A small plain-module and captured-lambda pilot passed first. Whole-compiler
parity then confirmed identical ordered namespaces and post-lift counts for
30 modules, producing the same 75-unit Go window plan. Four fresh test
processes ran the whole workload in before/after/after/before order:

| Queries | Wall seconds | Child CPU seconds | Maximum child RSS bytes |
| --- | ---: | ---: | ---: |
| Three legacy queries | 4.525572 | 4.507501 | 672006144 |
| One shape query | 1.857944 | 1.849114 | 661897216 |
| One shape query | 1.861286 | 1.851537 | 661897216 |
| Three legacy queries | 4.569544 | 4.533047 | 672006144 |

Mean query time fell from 4.547558 to 1.859615 seconds: 2.45x faster, with
59.1% less child CPU time. All trials retained the same module count, function
counts, staged source sizes and unit count. These are planning measurements,
not whole-CI improvements. The maximum RSS is per child, not cumulative
allocation across all processes.

## Validation

Native fixture, parser, whole-compiler metadata and window-plan comparisons
pass. The existing CI shard selector includes both permanent protocol
validation and compiler integration tests.
Linux x86-64 integration and parser tests passed with race detection on
Linux ARM64 using QEMU for x86-64 execution (52.994 seconds). Package vet,
source lint, project formatting and test-selector checks pass. The complete
two-generation bootstrap test also passed with race detection: all 75 emitted
units were byte-identical between the Go-built and self-built drivers, with
no OOM. The package took 898.295 seconds under QEMU; maximum child RSS was
2.62 GiB for gen0 and 11.65 GiB for gen1. These are correctness results,
not native timing measurements.

After rebasing onto main `30db8e86d`, both native drivers were rebuilt from
the updated compiler sources, with the shape change applied to one. The
plain-module and captured-lambda pilot and all parser cases passed with race
detection. Full-source comparison then matched the ordered namespaces and
function counts for all 30 modules, along with the independent 75-unit window
plan. Full source lint, package vet, formatting and selectors also pass.
This is integration evidence; the controlled timing and two-generation
fixpoint results above retain their recorded revisions. Current-head CI
must still validate the complete bootstrap after publication.

On Linux with the x86 toolchain and runner available:

```sh
go test ./internal/e2eselfhost -run '^TestSelfHostPerModuleShape' -race -count=1
GOMAXPROCS=4 FERN_BUILD_MEM_BUDGET_MB=12000 \
  go test ./internal/e2eselfhost \
  -run '^TestSelfHostPerModuleEmitAllFixpointX86_64$' \
  -race -count=1 -v -timeout 30m
```

For timing, build baseline and changed native drivers first. Against the
same staged sources, alternate the baseline's three metadata queries and
the changed driver's shape query. Compare ordered namespace/count rows and
the resulting unit windows, and capture wall time, child CPU and RSS.
Emulated runs establish correctness only.
