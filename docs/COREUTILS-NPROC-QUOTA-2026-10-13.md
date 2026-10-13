# nproc and Linux cgroup v2 quotas

Refs #8278; fixes #12335. GNU is 9.12, not the distribution's older binary.

The unchanged Fern baseline `3243400380ec94cc3ff825d21b1894b8c5d93587`
reports five available processors on this host, where GNU reports four:
the affinity mask contains five CPUs, but `/sys/fs/cgroup/cpu.max` is
`400000 100000`. The existing `TestNprocParity` corpus exposes the discrepancy
for ordinary invocations, `--ignore`, and unset/invalid OpenMP variables.

Binary probes, including `strace -e openat,read,sched_getaffinity`, show GNU
reading the unified `0::PATH` entry of `/proc/self/cgroup`, then
`/sys/fs/cgroup/PATH/cpu.max`. Docker probes with `--cpus` establish rounding:

| cpu.max | GNU result |
| --- | ---: |
| 25000 100000 | 1 |
| 125000 100000 | 1 |
| 150000 100000 | 2 |
| 175000 100000 | 2 |
| 275000 100000 | 3 |

The count rounds to the nearest integer, halves up, with a floor of one;
it is not a ceiling. `OMP_NUM_THREADS=9` returns nine even under a 1.25-CPU
quota, whereas `OMP_THREAD_LIMIT=9` leaves the quota result at one. `--all`
returns the installed count, five in that container, regardless of the quota.
GNU's public nproc manual explicitly identifies cgroup **version 2** quotas.
No GNU implementation source was consulted.

`coreutils/lib/cpuquota.fern` parses the unified path and bandwidth fields
using existing string/number operations and `read_file`; no syscall or builtin
is added. Unknown, unlimited, malformed or unreadable quota data leaves the
affinity count uncapped. Quotient/remainder arithmetic avoids overflow at
u64's maximum, including odd periods. Only the default path without an
OpenMP thread-count override reads the quota. The installed-count fallback
remains separate, so `--all` never starts applying a quota.

Permanent pure-helper tests cover rounding, unlimited/invalid/overflow fields,
u64 boundaries, root and nested unified paths, and absence of a v2 entry on
x86-64, ARM64 and WASI. The ordinary GNU corpus also covers OpenMP variables,
`--all`, `--ignore`, option errors and write failures. Additional gates check
standalone/self-host/multicall agreement and balanced allocations.

Probe host: Linux 6.18.44 x86-64, AMD EPYC 9V74, five visible CPUs. Docker
28.4.0 ran `debian:trixie-slim` digest
`sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f`,
with the built GNU 9.12 nproc mounted read-only. Cross-backend correctness
uses QEMU 10.0.2 and Wasmtime 46.0.1; tests use Go 1.27.1 and the verified
`stage0-20261009-823f7b1` producer.

Reproduce the container probe with `docker run --rm --cpus 1.25`, mounting
the GNU binary into `/usr/local/bin/nproc-gnu`, and run `nproc-gnu`,
`OMP_NUM_THREADS=9 nproc-gnu`, `OMP_THREAD_LIMIT=9 nproc-gnu`, and
`nproc-gnu --all`. Reproduce local gates with:

```
FERN_GNU_COREUTILS=/path/to/gnu-9.12/bin go test ./internal/testing/coreutils -run '^TestNproc' -count=1
go test ./internal/testing/coreutils -run '^(TestSelfHostCoreutilsParity|TestMulticallParity|TestCorpusFreesWhatItAllocates)$/^nproc$' -count=1
go test ./internal/testing/sourcelint -count=1
go vet ./internal/testing/coreutils
```

This repairs functionality and incurs the necessary procfs/cgroup file reads.
It is not a speed claim; release wall-time comparisons are deferred until the
concurrently running ownership full suite completes. Other baseline `pr`/`who`
timezone discrepancies and environment-specific fixture limitations remain
separate validation blockers for that ownership work.
