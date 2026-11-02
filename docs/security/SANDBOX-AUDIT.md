# Sandbox, no_new_privs and privilege

Status: audit and test for [SEC-039](https://github.com/JakeChampion/lang/issues/13541).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The package capability gates are recorded in `docs/PACKAGE-CAPABILITIES-BRIEF.md`, and the target gates in `docs/FREESTANDING-CORE.md`.

## What exists

`FERN_SANDBOX=1` makes the x86-64-linux and arm64-linux backends install a seccomp filter at `_start`, before any other code runs.
The filter is an allowlist of the system call numbers the emitted program contains. The backend records them at every `syscall` or `svc #0` instruction whose number is a constant.
A program that passes a number at run time is refused at compile time, so the list stays exact.
The installer sets `PR_SET_NO_NEW_PRIVS` first, so the filter needs no privilege and an `execve` of a setuid or setcap program cannot gain privilege.
A denied call kills the process with `SIGSYS`.

A child process inherits the filter across `execve`, so a program the sandboxed program runs is held to the same list.

| Test | What it pins |
|---|---|
| `TestSeccompFilterIsLoadedAtRuntime` | `/proc/<pid>/status` reports `Seccomp: 2` under the flag and `0` without it |
| `TestSeccompDoesNotBreakWorkingPrograms` | arithmetic, constant data, heap and strings, allocation churn and the clock run to the same result under the filter |
| `TestSeccompFilterDenies` | an `execve` spliced into the program dies with `SIGSYS` under the filter and returns `EFAULT` without it |
| `TestSeccompFixtureCorpus` | the conformance corpus under the filter, on request |
| `TestSelfHostSandboxFilterShape` | the emitted filter on both targets checks the audit architecture first, lists the program's calls in ascending order, and leaves out `prctl` and `seccomp` |
| `TestSelfHostSandboxRefusesDarwin`, `TestSelfHostSandboxRefusesRunTimeSyscallNumber`, `TestSelfHostSandboxNoBareSyscallEmit` | an `arm64-darwin` build refuses the flag, a run-time syscall number is refused on both targets, and no backend writes a `syscall` or `svc #0` outside the recorders |
| `TestSeccompNoNewPrivsBlocksSetuidEscalation` | a sandboxed program that dropped to user 65534 runs a setuid-root helper, and the helper reports 65534. Without the sandbox the same helper reports 0, which is the control. The child inherits the filter, so the helper can only make the calls the driver makes. The test needs root to install the helper and skips where the host refuses setuid |

The runtime provides `setuid`, `setgid` and `setgroups` for a privilege drop, and `disable_core_dumps` for the core policy. `rlimit_nofile` reads the descriptor ceiling, and `-heap-limit` bounds the arena ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)).
`std/mitigations` reports the hardware mitigations the kernel enables ([MITIGATION-REPORT.md](MITIGATION-REPORT.md)).

## Remaining limits

- The sandbox is Linux only. An `arm64-darwin` build refuses the flag, and a wasm image has no process to confine. QEMU user mode does not emulate seccomp, so the arm64 run-time tests and the arm64 fixture corpus run on the native arm64 runner alone.
- The allowlist is by number alone. It does not constrain arguments, so an allowed `openat` can open any path the process can reach.
- There is no namespace, chroot-by-default, or mount isolation. `chroot` exists as a builtin for a program that is already root.
- A privilege drop is the program's own call. Nothing drops privilege at startup, and nothing verifies that a drop happened before the program reads untrusted input.
- The resource quotas are the arena and the descriptor ceiling. There is no CPU time or child process quota.
