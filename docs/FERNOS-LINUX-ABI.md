# The Linux system calls of the compiler

Status: [record] the measured Linux system calls of the compiler on 2026-11-08.

Milestone 6 of `docs/BARE-METAL-PLAN.md` (#14265) runs the `arm64-linux` compiler, unmodified, on FernOS. FernOS then must implement the Linux system calls that the compiler makes. This document records that set, measured on 2026-11-08. The first step of #14265 asks for this measurement.

## How to measure

The compiler compiles itself, as `make build` does. The first run has an empty `HOME`, so it extracts the embedded stdlib (a cold run). The second run finds the stdlib in the cache (a warm run).

```
mkdir -p /tmp/h
HOME=/tmp/h strace -f -o cold.trace ./bin/fern -target x86-64-linux -embed build/embed -o /tmp/f1 compiler/fern_main.fern
HOME=/tmp/h strace -f -o warm.trace ./bin/fern -target x86-64-linux -embed build/embed -o /tmp/f2 compiler/fern_main.fern
HOME=/tmp/h qemu-aarch64 -strace ./fern-arm64 -target arm64-linux -embed build/embed -o /tmp/f3 compiler/fern_main.fern
```

`fern-arm64` is the compiler built with `-target arm64-linux`. Count the calls with `awk '{ split($2,a,"("); print a[1] }' cold.trace | sort | uniq -c`.

## The result

- The compiler has one thread. No trace has `clone`, `futex` or `wait4`.
- A self-compile takes 66 s on a 4-core x86-64 host and 5 min 17 s under `qemu-aarch64`. Its peak RSS is 2950 MiB on x86-64.
- The output of a cold run and of a warm run is the same. The output of the arm64 compiler is the same as the output of the x86-64 compiler for `-target arm64-linux`.

The counts are for a self-compile, after the three fixes in "Costs that the measurement found". They come from the x86-64 trace. The arm64 trace has the same calls, with the arm64 numbers:

| Call | x86-64 | arm64 | Cold | Warm | What the compiler does with it |
|---|---|---|---|---|---|
| `read` | 0 | 63 | 1314 | 1318 | Reads each source file, the cache files and its own image |
| `openat` | 257 | 56 | 1345 | 922 | Opens a file to read, or creates one with `O_WRONLY\|O_CREAT\|O_TRUNC` |
| `close` | 3 | 57 | 1111 | 691 | |
| `fstat` | 5 | 80 | 657 | 659 | Gets the size of a file before it reads it |
| `statx` | 332 | 291 | 468 | 468 | Stats a path or a descriptor, also with `AT_EMPTY_PATH` and `AT_SYMLINK_NOFOLLOW` |
| `mkdirat` | 258 | 34 | 432 | 0 | Makes the directories of the stdlib cache |
| `write` | 1 | 64 | 423 | 1 | Writes the cache files and the output |
| `getcwd` | 79 | 17 | 141 | 141 | Makes a module directory absolute |
| `getdents64` | 217 | 61 | 60 | 60 | Lists the `-embed` directory |
| `mmap` | 9 | 222 | 3 | 3 | Reserves the stack, the signal stack and the heap at start |
| `rt_sigaction` | 13 | 134 | 3 | 3 | Sets the fault handler for `SIGSEGV`, `SIGILL` and `SIGBUS` |
| `mprotect` | 10 | 226 | 1 | 1 | Makes the stack guard |
| `sigaltstack` | 131 | 132 | 1 | 1 | Sets the signal stack of the fault handler |
| `ioctl` | 16 | 29 | 1 | 1 | `TCGETS` on fd 2, to know if stderr is a terminal; `ENOTTY` is correct |
| `fchmod` | 91 | 52 | 1 | 1 | Makes the output executable, `0755` |
| `renameat` | 264 | 38 | 1 | 0 | Puts the extracted stdlib in place in one step |
| `getrandom` | 318 | 278 | 1 | 0 | 6 bytes for the name of the extract directory |
| `exit_group` | 231 | 94 | 1 | 1 | |

Two more calls occur on other paths:

- `clock_gettime` (228, 113) gives the time to `std/time` and to the compiler's own timers. The runtime uses the vDSO when the auxiliary vector names one, and the system call when it does not. So FernOS needs no vDSO.
- `writev` (20, 66) writes a diagnostic to stderr.

## What FernOS must give the process

- **Memory.** Three `mmap` calls at start make reservations. The stack gets 1 GiB with `MAP_NORESERVE|MAP_STACK`, and the signal stack gets 64 KiB. The heap gets 16 GiB at the hint `0x10000000` with `MAP_NORESERVE`. The runtime takes the address that `mmap` returns. After that, the compiler makes no `mmap`, `munmap` or `brk` call. So the kernel gives a frame when the process first touches a reserved page. A touch of any other page ends the process.
- **An environment.** The compiler needs `FERN_CACHE_DIR`, `XDG_CACHE_HOME` or `HOME` in `envp`, and a directory there that it can write. With none of the three it stops with `fern: no cache directory for the stdlib`.
- **`/proc/self/exe`.** The compiler opens it, stats it and reads all of it, 25 MB, to make the key of its cache of certified functions.
- **Files.** The sources, `fern.toml` probes from each module directory up to `/`, and `$HOME/.cache/fern`.
- **Faults.** The runtime sets a handler for `SIGSEGV`, `SIGILL` and `SIGBUS`. A first kernel can record the handler and end the process on a fault, and not deliver the signal.

A first kernel can leave out what eggshell leaves out: `ptrace`, SysV IPC, inotify, mounts and namespaces. The compiler uses no sockets, no `poll` or `epoll`, and no `io_uring`.

## Costs that the measurement found

Each call is a trap into the kernel on FernOS, so the measurement found three costs in the compiler. The same change fixed each one:

- **`create_dir_all` made each prefix of the path.** The cold run made 4921 `mkdirat` calls for 424 files, and 33 of them made a directory. Now it makes the full path first, and it walks the prefixes only when that call gives `ENOENT`. The cold run makes 432.
- **The bundle searched for the `fern.toml` of each module.** That gave 2136 failed opens and 1106 `statx` calls, because each search climbs to `/`. Now it searches each directory once.
- **The SSA backend read the clock four times for each function.** It needs the times only for `FERN_SSA_REPORT`. The x86-64 compiler reads the clock through the vDSO, so the trace does not show it. The arm64 compiler under qemu has no vDSO, so it made 42752 `clock_gettime` calls. Now it reads the clock only when the report is on.

Together the fixes take a cold self-compile from 13571 calls to 5965, and a warm one from 7383 to 4272.
