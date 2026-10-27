# Process startup and system call audit

Status: [reference] audit record for [SEC-018](https://github.com/JakeChampion/lang/issues/13520).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

The record reads the native process entry and the runtime helpers that read and write.
It names the rule each helper follows for an interrupted call, a short transfer, and a zero result, and the test that pins each rule.
It changes the rules that lost data, which the last section records.

## Process entry

`_start` is emitted per program by `emit_process_entry` in `compiler/asm_ir.fern` and its arm64 twin.
It reads the argument count, the argument vector, and the environment pointer from the initial stack, and stores each only when the program needs it.
It scans the auxiliary vector for one entry, `AT_SYSINFO_EHDR`, and only when a vDSO clock is wanted.
It maps the 1 GiB application stack and the 1 MiB guard, checks the guard's `mprotect` result, and switches the stack pointer (`docs/security/STACK-GUARD.md`).
The sandbox filter, when requested, is installed before any of that.
No libc runs at any point, and the exit path is the `exit_group` system call.

The program reads no other auxiliary entry.
`AT_RANDOM` is unused because entropy comes from `getrandom` (`docs/security/ENTROPY.md`).
`AT_SECURE` is unused, so a set-user-ID program reads its environment like any other.

## System call surface

Each helper names its system call by number for its target.
`TestSelfHostProfileSyscallABI` pins the profiling numbers per target.
The x86-64 runtime issues 40 distinct numbers.
The most frequent are `read`, `write`, `close`, `openat`, `fcntl`, `sendto` and `recvfrom`.
A negative return is an errno.

`__fern_io_error` in `compiler/runtime/linux/` maps `ENOENT`, `EACCES`, `EEXIST`, `EINTR` and `EILSEQ` to their variants and every other value to `Other` with its message and number.
`TestSelfHostErrnoOf` and the `chroot` and `chdir` tests pin that mapping.

## Interrupted calls

A handler installed by `signal_catch` carries `SA_RESTART`, so the kernel restarts a read or write the signal lands in.
A handler installed by `signal_catch_interrupting` does not, so the call returns `EINTR`.
`signal_catch_interrupting_test.go` pins that a single chunk read then answers `Interrupted`, and that a restarting catch lets the read complete.

Before this record, every loop helper returned `Interrupted` on `EINTR` at any point of the loop.
A write-all that wrote part of the buffer reported `Interrupted` and dropped the count.
The caller could neither resume nor know what reached the descriptor.
A read-all dropped every byte it read.
The subprocess drain treated `EINTR` as the end of the output.

The rule is now the same in every loop.

| Helper | Interrupted before the first byte | Interrupted after progress | Zero result with bytes left |
|---|---|---|---|
| `Writer.write`, `Writer.write_bytes`, `Writer.write_bytes_u8` | `Interrupted` | continues | `Other("", "Input/output error", 5)` |
| `write_file`, `write_file_bytes`, `write_file_bytes_u8`, `write_file_exec` | `Interrupted` | continues | `Other(path, "Input/output error", 5)` |
| `read_file`, `read_file_bytes`, `read_file_bytes_u8`, `mounts` | `Interrupted` | continues | end of file |
| the subprocess drain | continues | continues | end of output |
| `Writer.write_some`, `Writer.write_some_bytes`, `Reader.read_chunk*`, the socket helpers | `Interrupted` | one call, no loop | the count |

A caller that sees `Interrupted` from a loop helper knows nothing was transferred.
A caller that wants to stop a long transfer on a signal uses the single-call helpers.

## What the tests pin

| Rule | Test |
|---|---|
| A write-all interrupted while blocked after its first page continues to the end, and the signal is still taken | `TestSelfHostWriteAllContinuesAfterInterrupt` |
| A write-all interrupted while blocked before its first byte answers `Interrupted`, and the pipe holds exactly the bytes written before it | `TestSelfHostWriteAllInterruptedBeforeFirstByte` |
| A read-all of a FIFO interrupted after its first bytes continues to the end | `TestSelfHostReadAllContinuesAfterInterrupt` |
| A write that the kernel answers with zero is an error with errno 5, never a success | `TestSelfHostWriteAllRefusesZeroProgress`, through the seccomp launcher with errno 0 |
| A short chunk read returns the bytes read | `signal_catch_interrupting_test.go`, the `restart` step |
| An entropy read that is short, interrupted, or empty | `docs/security/ENTROPY.md` |

The first three run natively on the host and under QEMU for arm64-linux.
The zero-write probe runs on the host only, because the seccomp launcher is a host process.

Against the previous runtime, the same four tests show the defects.
The write-all test ended with exit 3, `interrupted` on stderr, and 582 of 1000006 bytes in the pipe, on both targets.
The read-all test ended with exit 3 on both targets.
The zero-write probe ended with exit 7, the success arm.
The before-first-byte test passed on both targets, because that rule did not change.

## Gaps

| Gap | Consequence |
|---|---|
| Socket sends are single calls | A caller that needs a complete send loops in Fern; `std/net` does |
| `EAGAIN` on a non-blocking descriptor | Surfaces as `Other` with errno 11, which `std/net` matches by number |
| No arm64-darwin test for the loop rules | The Darwin helpers carry the same text with Darwin numbers; the Darwin CI shards run the fixture corpus and not these signal tests |
| A `wait4` the signal interrupts | `signal_catch_interrupting_test.go` pins `Interrupted` from the wait; a restarting catch restarts it |

## Commands

```
go test ./internal/testing/e2e -run 'TestSelfHost(WriteAllContinuesAfterInterrupt|WriteAllInterruptedBeforeFirstByte|ReadAllContinuesAfterInterrupt|WriteAllRefusesZeroProgress)$' -v
go test ./internal/testing/e2e -run 'TestSelfHostSignalCatchInterrupting|TestSelfHostEntropy'
make runtime-check
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
