# Parallel semantic compiler tests

## Problem and change

`TestSelfHostSSASemantic` took 64.09 seconds on native x86 CI in
[run 34936726276, shard 5](https://github.com/JakeChampion/lang/actions/runs/34936726276/job/104287870207).
It compiled a separate driver for each semantic case sequentially. Each case
already owns its source directory and output binary; no source or assertion
needs to change to run these cases concurrently.

The cases now use `t.Parallel()`. The harness retains its shared immutable
build cache and memory reservations. A synchronous `cases` group waits for
all children before returning. This is essential for shard weights: without
the group, Go reports only the parent's setup time, excluding parallel
children. The two-case race pilot reported 0.01 seconds without the group
and 5.39 seconds with it. The top-level name used by CI selection and weights
is unchanged; individual paths gain `/cases/`.

## Native compilation measurement

Measured September 15, 2026 at compiler revision `81c0a9f4d`, using Go 1.26.0
on darwin/arm64, model Mac15,6, 12 CPUs and 36 GiB RAM. The probe below builds
the same 80 semantic drivers using the native Go bootstrap compiler and its
in-process x86 ELF assembler. It never executes generated machine code or
QEMU. This isolates the compilation cost from emulated execution.

Each trial is a fresh test process with the disk driver cache disabled.
The order is serial, parallel, parallel, serial to expose warm-up drift.
All 80 cases completed in every trial. A four-case pilot passed first; the
full trial changes only the case-selection regex. Parallelism uses the
unchanged default build-memory limiter, including its macOS budget fallback.

| Trial | Parallelism | Wall seconds | CPU seconds (user + system) | Peak RSS bytes |
| --- | ---: | ---: | ---: | ---: |
| 1 | 1 | 28.21 | 44.79 | 137117696 |
| 2 | 4 | 11.11 | 55.22 | 202014720 |
| 3 | 4 | 11.03 | 55.04 | 206716928 |
| 4 | 1 | 28.26 | 45.06 | 137592832 |

The mean wall time is 2.55 times faster for this native compilation workload.
Parallelism uses more aggregate CPU time and memory. These are local compiler
measurements, not a whole-CI speedup or native x86 runtime benchmark. CI uses
its runner's Go parallelism and memory limits, so its actual improvement must
be measured after rollout. Do not lower shard weights from this local result.

Correctness: the complete semantic corpus passed under the race detector
with two-way parallelism, then again with four-way parallelism and the final
timing group. Those runs used Linux ARM64 with QEMU for generated x86 program
execution and are correctness evidence only. Every case still has its own
source, compiler invocation, execution and exact diagnostic/output assertion.

## Reproduce the compilation-only probe

In a disposable worktree, save the following as
`internal/e2eselfhost/ci_native_profile_test.go`. It deliberately does not
use the x86 execution-tooling gate: only the native compiler and assembler
run. The `gcc` argument is the harness's ordinary fallback if native assembly
fails; such a fallback must be investigated before comparing measurements.

```go
package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCISemanticBuildProfile(t *testing.T) {
	t.Run("cases", func(t *testing.T) {
		for i, tc := range semanticCases() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				copySelfHostDriver(t, dir, "ssasem.fern")
				source, _ := semanticSource([]int{i})
				if err := os.WriteFile(filepath.Join(dir, "semantic.fern"), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				buildSelfHostBin(t, "gcc", dir, "semantic.fern", "semantic")
			})
		}
	})
}
```

Build once, then run the binary from the package directory so fixture paths
resolve. On macOS, `/usr/bin/time -l` records wall time, CPU time and peak RSS
in bytes. On Linux use GNU time's `-v` and account for its RSS unit difference.

```sh
go test -c -o /tmp/lang-ci-native-profile.test ./internal/e2eselfhost
cd internal/e2eselfhost
/usr/bin/time -l env FERN_SELFHOST_BUILD_CACHE= /tmp/lang-ci-native-profile.test \
  -test.run '^TestCISemanticBuildProfile$' -test.count=1 -test.parallel=1 -test.v
```

Repeat in fresh processes with `-test.parallel=4`, then reverse the order.
For the small pilot, select
`^TestCISemanticBuildProfile$/cases/(nested-projections|phi|call|projection-result)$`.
Remove the temporary probe afterward. The measurement probe is excluded from
normal CI because it duplicates compilation while intentionally omitting
runtime correctness checks.
