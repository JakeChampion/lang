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

The filter also checks one argument of the calls below. Each rule admits the values the runtime passes. A program that reaches the call on its ordinary path is never killed, and a call with another value is.
A value rule reads both words of the argument. The kernel reads the low word alone, so a value with a high word set is killed too.
The rules are the table `arg_rules` in `compiler/asmcore.fern`, one per architecture, and the filter carries a rule's block only when the program contains its call.

| Call | Argument | Admitted |
|---|---|---|
| `mmap`, `mprotect`, `pkey_mprotect` | protection word | no `PROT_EXEC` bit |
| `socket` | domain | `AF_UNIX`, `AF_INET`, `AF_INET6` |
| `setsockopt` | level | `SOL_SOCKET`, `SOL_TCP`, `SOL_IPV6`, `SOL_TLS` |
| `setsockopt` | option | `SO_REUSEADDR`, `SO_BROADCAST`, `SO_KEEPALIVE`, `SO_REUSEPORT`, `SO_ATTACH_REUSEPORT_CBPF`, `TCP_NODELAY`, `TCP_ULP`, `IPV6_V6ONLY`, `TLS_TX`, `TLS_RX` |
| `ioctl` | request | `TCGETS`, `TCSETS`, `TCSETSW`, `TCSETSF`, `TIOCOUTQ`, `TIOCGWINSZ`, `TIOCSWINSZ`, `FICLONE` |
| `prctl` | option | `PR_SET_PDEATHSIG`, `PR_SET_DUMPABLE`, `PR_GET_TAGGED_ADDR_CTRL`, `PR_PAC_GET_ENABLED_KEYS` |
| `fcntl` | command | `F_DUPFD`, `F_GETFL`, `F_SETFL`, `F_SETPIPE_SZ`, `F_GETPIPE_SZ` |
| `openat` | flags | the bits the runtime and the open options set, so `O_ASYNC`, `O_PATH`, `O_TMPFILE` and an undefined bit are killed |
| `clone` | flags | `SIGCHLD` alone, the fork of the runtime |

So a sandboxed process cannot open a netlink or packet socket, and cannot reach a device through an `ioctl` the terminal helpers do not use. It cannot install a second filter or change its memory map through `prctl`. It cannot take a file lock or set a descriptor's owner or signal through `fcntl`. It cannot mark a socket, bind it to a device or change its priority through `setsockopt`. It cannot open a path without permission through `O_PATH`, make an unnamed file through `O_TMPFILE`, or ask for a signal on a descriptor through `O_ASYNC`. It cannot make a namespace.

The `setsockopt` rules hold the level and the option number each on its own, not the pair: at `SOL_SOCKET` the admitted numbers also name `SO_DEBUG` (1), `SO_ATTACH_FILTER` (26) and `SO_PEERSEC` (31).
`prctl` is on the list only when the program calls `disable_core_dumps`, `prctl_query` or a worker that sets its parent-death signal. `seccomp` is never on it.

A child process inherits the filter across `execve`, so a program the sandboxed program runs is held to the same list.

| Test | What it pins |
|---|---|
| `TestSeccompFilterIsLoadedAtRuntime` | `/proc/<pid>/status` reports `Seccomp: 2` under the flag and `0` without it |
| `TestSeccompDoesNotBreakWorkingPrograms` | arithmetic, constant data, heap and strings, allocation churn and the clock run to the same result under the filter |
| `TestSeccompFilterDenies` | an `execve` spliced into the program dies with `SIGSYS` under the filter and returns `EFAULT` without it |
| `TestSeccompFixtureCorpus` | the conformance corpus under the filter, on request |
| `TestSelfHostSandboxFilterShape` | the emitted filter on both targets, run by a BPF interpreter over seccomp_data records: it checks the audit architecture first, lists the program's calls in ascending order, allows each call with the runtime's argument, kills each call with a foreign argument or a high word, kills `execve`, `ptrace` and `seccomp`, and the installer loads the filter's length |
| `TestSelfHostSandboxRulesCoverRuntime` | every raw system call of the Linux and Android runtime sources passes an argument its rule admits, so a new constant in a runtime helper is a test failure before it is a kill |
| `TestSeccompDeniesArguments` | a `socket` for `AF_NETLINK`, a `socket` whose domain carries a high word, an `ioctl` for `TIOCSTI`, a `prctl` for `PR_SET_MM`, a `setsockopt` for `SOL_NETLINK` or for `SO_BINDTODEVICE`, a `fcntl` for `F_SETLK`, an `openat` with `O_PATH` and a `clone` with `CLONE_NEWUSER` each die with `SIGSYS` under the filter; the runtime's own argument passes, and the foreign call only fails without the filter |
| `TestSeccompDeniesExecutableMapping` | an `mprotect` with `PROT_EXEC` spliced into the program dies with `SIGSYS` under the filter, the same call with read and write passes, and the executable request passes without the filter |
| `TestSelfHostSandboxRefusesDarwin`, `TestSelfHostSandboxRefusesRunTimeSyscallNumber`, `TestSelfHostSandboxNoBareSyscallEmit` | an `arm64-darwin` build refuses the flag, a run-time syscall number is refused on both targets, and no backend writes a `syscall` or `svc #0` outside the recorders |
| `TestSeccompNoNewPrivsBlocksSetuidEscalation` | a sandboxed program that dropped to user 65534 runs a setuid-root helper, and the helper reports 65534. Without the sandbox the same helper reports 0, which is the control. The child inherits the filter, so the helper can only make the calls the driver makes. The test needs root to install the helper and skips where the host refuses setuid |

The runtime provides `setuid`, `setgid` and `setgroups` for a privilege drop, and `disable_core_dumps` for the core policy. `rlimit_nofile` reads the descriptor ceiling, and `-heap-limit` bounds the arena ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)).
`std/mitigations` reports the hardware mitigations the kernel enables ([MITIGATION-REPORT.md](MITIGATION-REPORT.md)).

## Remaining limits

- The sandbox is Linux only. An `arm64-darwin` build refuses the flag, and a wasm image has no process to confine. QEMU user mode does not emulate seccomp, so the arm64 run-time tests and the arm64 fixture corpus run on the native arm64 runner alone.
- The rules constrain nine arguments of eight calls. An allowed `openat` can open any path the process can reach with the flags the open options offer, `O_DIRECT`, `O_SYNC` and `O_NOATIME` included.
- `prctl_query` passes its caller's option to the kernel. Under the sandbox, an option other than `PR_GET_TAGGED_ADDR_CTRL` and `PR_PAC_GET_ENABLED_KEYS`, the two `std/mitigations` queries, is killed.
- There is no namespace, chroot-by-default, or mount isolation. `chroot` exists as a builtin for a program that is already root.
- A privilege drop is the program's own call. Nothing drops privilege at startup, and nothing verifies that a drop happened before the program reads untrusted input.
- The resource quotas are the arena and the descriptor ceiling. There is no CPU time or child process quota.
