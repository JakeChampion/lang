# Hardware mitigation report

Status: partial implementation for [SEC-019](https://github.com/JakeChampion/lang/issues/13521), with the entropy half in [ENTROPY.md](ENTROPY.md).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

## Contract

`std/mitigations` reports the hardware mitigations of the process that calls it.
For each mitigation it says whether the processor lists it, whether the kernel enables it for user space, and whether the process runs with it.
`report()` returns one row per mitigation the target can carry.
Each row holds three states, `Yes`, `No`, or `Unknown`, for the processor, the kernel, and the process.
`Unknown` means that the source for the cell was unavailable.
It never means that the mitigation is absent.

| Target | Rows |
|---|---|
| x86-64 Linux | `shstk` (the CET shadow stack), `ibt` (indirect branch tracking) |
| arm64 Linux | `pac` (pointer authentication), `bti` (branch target identification), `mte` (memory tagging) |
| Other targets | none |

Each cell has one source, which the module documents in its own contract table.
The processor cells come from the feature line of `/proc/cpuinfo`.
The kernel cells come from the `x86_Thread_features` line of `/proc/self/status` on x86-64 and from `AT_HWCAP` and `AT_HWCAP2` in `/proc/self/auxv` on arm64.
On x86-64 the process cells come from the same status line.
On arm64 the BTI process cell comes from the `VmFlags` lines of `/proc/self/smaps`, and the PAC and MTE cells come from two kernel queries.
No cell is inferred from the instructions the compiler emitted or from the `-harden` flag.

The kernel queries use the new builtin `prctl_query(option: i32): i64`.
On Linux it calls `prctl(option, 0, 0, 0, 0)` and returns the kernel's answer or the negated errno.
Darwin has no `prctl`, so its runtime answers `-38`.
WASI and the freestanding targets refuse the builtin at compile time under the `sysinfo` capability, and the package capability is `env`.
The module calls it only inside its Linux branch, so the other targets never reach the gate.

## Observed values

The values below are what the tests observed.
They describe the hosts, not the language.

| Host | Row | Processor | Kernel | Process |
|---|---|---|---|---|
| x86-64, Linux 6.18.44-fc-v165, Intel Xeon 2.80 GHz | `shstk` | no | no | no |
| the same host | `ibt` | no | no | no |
| arm64 under QEMU 8.2.2 user mode on that host | `pac` | yes | yes | unknown |
| the same emulation | `bti` | yes | yes | no |
| the same emulation | `mte` | yes | yes | no |

The x86-64 host lists neither `user_shstk` nor `ibt`, and its kernel prints no `x86_Thread_features` line.
QEMU serves the guest its own `/proc/cpuinfo`, `/proc/self/auxv`, and `/proc/self/smaps`, so the processor and kernel cells are known on a foreign host.
QEMU answers `EINVAL` to `PR_PAC_GET_ENABLED_KEYS`, so the PAC process cell is unknown under emulation.

It answers zero to `PR_GET_TAGGED_ADDR_CTRL`, so the MTE process cell is `no`.
It marks no mapping with the `bt` flag, so the BTI process cell is `no` for a plain build and for a `-harden` build alike.
Physical arm64 hardware was unavailable.
No row records a physical processor, a kernel that enables the feature, or a fault test.
The Go oracle computes the same two x86-64 rows as the native build on the host.

## Verification

- `tests/stdlib/mitigations_test.fern` checks the parsers on positive and negative inputs. The auxiliary vector cases cover both hardware capability words, a vector that ends at `AT_NULL` before the key, a truncated vector, and an empty vector. The feature cases require that `btix` does not match `bti` and that `user_shstk` does not match `shstk`. The status case requires that the `x86_Thread_features_locked` line does not count as the features line. The mapping case requires that `b` does not match `bt`. The last case checks the shape of `report()` on the current target. The suite passes natively on x86-64 and on arm64 under QEMU. It also passes under the Go oracle and under the self-host interpreter on x86-64.
- `TestSelfHostMitigationsReport` compiles a program that prints the report. On x86-64 the output must equal the rows the Go test computes from the same files. The Go oracle must print the same rows. Under a seccomp filter that refuses `openat`, every processor and kernel cell must be unknown, and on x86-64 every process cell as well. On arm64 under QEMU the processor and kernel cells of the three rows must be known.
- `TestSelfHostPrctlQuery` checks `prctl_query(3)` answers 1 and `prctl_query(999999)` answers -22 on x86-64, on arm64 under QEMU, and under the Go oracle. `TestPrctlQueryRefusedOffNativeHosts` checks the compile-time refusal on the WASI and freestanding targets.
- The module's executable example runs under `fern -doctest`, and the standard-library import, doctest, and suite gates include the module.

```sh
go test ./internal/testing/e2e -run 'TestSelfHostMitigationsReport|TestSelfHostPrctlQuery|TestPrctlQueryRefusedOffNativeHosts' -count=1 -timeout 0
go test ./internal/testing/e2ecompiler -run 'TestSelfHostStdTestE2E$/cases/mitigations|TestSelfHostStandardLibraryDoctests/std/mitigations' -count=1 -timeout 0
```

## Remaining limits

- The report observes state. It does not enable a mitigation, and it does not prove enforcement. A fault test per mitigation belongs to SEC-030, SEC-032, SEC-033, and SEC-034.
- On arm64 the processor and kernel cells come from the same identification registers. The kernel sanitises those registers and derives the `Features` line from the hardware capability words. A processor cell can differ from the kernel cell only when one file is unreadable. A direct read of the identification registers also passes through the kernel's emulation and filter.
- Mainline Linux cannot enable indirect branch tracking for user space. The `ibt` row therefore reads its kernel and process cells from the same line, and both stay `no`.
- The self-host interpreter answers `prctl_query` from the kernel of the host it runs on. Thus `fern -interp` reports the interpreter's own process state, as the Go oracle does. The stage0 pin `stage0-20261028-9193717` compiles the dispatch (`docs/BOOTSTRAP.md`).
- The hardened standard-library sweep and the other `-harden` evidence stay in `docs/HARDENING.md` and `STACK-GUARD.md`.
