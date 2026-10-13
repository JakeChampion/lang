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

## Controlled contender timings

After the interrupted suite stopped, the idle x86-64 host ran Hyperfine 2.0.0
with 5 warmups and 100 measurements per eligible implementation, without a
shell or concurrent heavy tests/profiles. Every contender first matched GNU's
stdout, stderr and exit status under `LC_ALL=C`, `LANG=C`, `TZ=UTC`. Versions
and contender build configurations are those in the listing report: GNU 9.12,
uutils 0.12.0, BusyBox 1.38.0 and Toybox 0.8.15.

Means ± standard deviations in microseconds, including process startup:

| Invocation | Fern | GNU | uutils | BusyBox | Toybox |
| --- | ---: | ---: | ---: | ---: | ---: |
| default, available CPUs | 214.6 ± 120.9 | 664.1 ± 65.7 | 784.1 ± 167.5 | differs | differs |
| `--all`, installed CPUs | 141.3 ± 57.3 | 597.8 ± 70.0 | 654.7 ± 55.0 | 708.1 ± 45.2 | 758.5 ± 262.7 |

The default reference answer is four, and `--all` is five. BusyBox and Toybox
answer five by default here, ignoring the quota, so their default timings are
excluded rather than credited for different behavior. Both match `--all`, but
neither beats GNU or uutils on that row. These two local rows are not a global
coreutils ranking or isolated algorithm latencies; the short runs have real
startup noise. No before/after speed comparison is made against Fern's wrong
default answer.

The measured Fern source is #12335's fix at `922f7762b`, compiled with
`-O -target x86-64-linux` by the frozen same-producer baseline compiler
SHA-256 `2284ebaea9001502884865aeaeca76045cf879ba72bfa745cc59fea404900d0b`.
Executable size: 65152 bytes; executable SHA-256:
`95aec6dc0970ee7381c446810e6095936b315bba6ae4321154da9d4bac9fc5f4`.
Compressed raw exports and the eligibility summary are in
`docs/benchmarks/nproc-2026-10-13/`.

This repairs functionality and incurs necessary procfs/cgroup reads. Remaining
host-specific `pr`/`who` ABI/zone-data discrepancies are recorded separately;
they were not removed by selecting an older GNU release. The ownership suite
uses a disposable filesystem and a GNU 9.12 build against CI's Ubuntu/glibc ABI.
