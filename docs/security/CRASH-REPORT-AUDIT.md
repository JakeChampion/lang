# Abort, signal and crash report audit

Status: [reference] audit record for [SEC-021](https://github.com/JakeChampion/lang/issues/13523).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

The record reads the native failure paths as they stand: the aborts the runtime raises, the fault handler, and the backtrace walk.
It names what each path does, what it must not do, the tests that pin each rule, and the gaps.
It changes one thing, which the last section records.

## The report path

Every fatal condition ends in `__fern_report` (`emit_report` in `compiler/asm_ir.fern`, the same name on arm64).
The caller hands it the exit status, the cause line, and its length.
It writes the cause line to descriptor 2 with one `write` system call.
With the backtrace on, it continues into `__fern_bt_exit`, which writes `backtrace:` and one line per frame and exits.
With the backtrace off (`-backtrace=false` or `FERN_BACKTRACE=0`), it exits at once.

Every step is a register move, a stack buffer of 32 bytes, or a system call.
No step allocates, takes a lock, or calls back into the program.

The cause lines live in `compiler/aborts.fern`: array index, string index, slice range, stack overflow, stack guard, busy flag, allocation size, out of memory, and map failure.
The bounds and allocation aborts exit with status 134.
`docs/security/BOUNDS-AUDIT.md` pins the abort of every checked path on x86-64, arm64, and Wasm.

## The fault handler

Startup maps a 64 KiB alternate signal stack and installs `__fern_segv` for `SIGSEGV` with `SA_ONSTACK`, `SA_SIGINFO`, and `SA_RESTORER`.
The handler reads the fault address from the signal information.
A fault inside the 1 MiB guard of the runtime's own stack is a stack overflow.
The runtime keeps the kernel's stack when its own mapping fails. There a fault within 1 MiB of the context's stack pointer counts as one.

The handler then builds a frame record on the alternate stack from the context's frame pointer and return address.
It enters the report path with `fern: stack overflow` and status 134.

Any other fault resets the disposition to the default and returns.
The instruction faults again, and the kernel ends the process with a bare `SIGSEGV`.
`TestSelfHostStackOverflow` pins all three outcomes on both targets: the overflow report, a million-deep control, and a bare fault from a wild read.

The handler does not set `SA_NODEFER` or `SA_RESETHAND`.
A second fault while the handler runs is delivered with `SIGSEGV` blocked, so the kernel ends the process with the default action.
There is no recursion and no second report.
A fault on the alternate stack itself has the same outcome.

## The backtrace walk

`__fern_bt_exit` walks the frame-pointer chain from the current frame.
It stops after 64 frames.
It also stops at a frame pointer below the stack pointer, or not 8-byte aligned, and at a return address below 64 KiB.
It now also stops at a frame pointer at or above the application stack's top.

The top is the stack pointer after the startup switch, stored in `__fern_stack_top`.
A shared object runs on its caller's stack. Each export wrapper stores its own frame address there on entry, so the walk ends at the wrapper.
The first frame of a walk is the reporter's own record and is not checked against the top.
The overflow handler builds that record on the alternate signal stack, which the kernel can map above the application stack.
It leaves the faulting stack pointer in `__fern_bt_floor`, and the walk takes that as the lower bound of the frames after the first.
Under `-harden` on arm64 the walk strips the pointer authentication code from each return address before it prints it.

## What the tests pin

| Rule | Test |
|---|---|
| Every abort cause line and exit status on x86-64 and arm64 | `TestSelfHostAbortReportsX86_64`, `TestSelfHostAbortReportsArm64` |
| The frames of an abort name the caller chain | `TestSelfHostAbortBacktraceX86_64`, `TestSelfHostAbortBacktraceArm64`, `TestSelfHostAbortHelperFrameAgrees` |
| A frameless leaf still reports its caller first | `TestSelfHostAbortBareFrameX86_64` |
| The walk off leaves the cause line alone | `TestSelfHostAbortBacktraceOffX86_64`, `TestSelfHostAbortBacktraceOffArm64` |
| Stack overflow, a deep control, and an unrelated fault | `TestSelfHostStackOverflow` |
| A guard the kernel refuses stops startup before `main` | `TestSelfHostStackGuardDenial` (`docs/security/STACK-GUARD.md`) |
| A frame pointer above the stack top ends the walk, and the report keeps its cause line and status 134 | `TestSelfHostAbortBacktraceCorruptFrame`, on x86-64 and arm64 under QEMU |
| An abort in a shared object prints the Fern frames and stops at the export wrapper | `TestSharedLibX86AbortBacktrace`, `TestArm64SharedLibAbortBacktrace` |
| An overflow report walks past the handler's record into the application stack | `TestSelfHostStackOverflow` requires two frames or more |
| The sanitizer's use-after-free report carries a backtrace | `TestSelfHostSanitizeReportCarriesBacktrace` |

## Gaps

| Gap | Consequence |
|---|---|
| A corrupt frame pointer inside the stack range | The walk prints whatever the frame holds, up to 64 lines, and stops at the first failed check; it cannot fault, because every address it reads is inside the mapped stack |
| A frame the backend turned into a tail jump, or an x86-64 function that needs no frame pointer | The walk cannot see it; `docs/security/ABI-AUDIT.md` records both rules |
| Page probes and canaries | Not started; `docs/security/STACK-GUARD.md` records the remaining SEC-015 work |
| `SIGBUS` and `SIGILL` | No handler is installed; the kernel's default action ends the process with no cause line |
| A report from a thread | The runtime has one context, so no second thread exists to report |

## Changes in this record

The walk had no upper bound.
A frame pointer above the stack, which a corrupted frame or a foreign caller can leave, made the walk read unmapped memory.
On the overflow path that fault reached the handler with `SIGSEGV` blocked.
On the abort path it re-raised as a bare `SIGSEGV`.
In both cases the report lost its status.

The startup switch now stores the stack top, and the walk stops there.
The corrupt-frame test shows the old outcome against the previous compiler and the report against this one.
Against the previous compiler, the patched build on each target ended by signal with no exit status, and both controls passed.
With this change, both patched builds exit 134 with the cause line and `backtrace:`.
The walk prints at most the one frame below the corrupt pointer.

The walk had no upper bound in a shared object, and the overflow handler dropped the bound for its report.
A corrupt frame pointer above the stack then faulted inside the walk.
Each export wrapper now records its frame as the stack top on entry.
The handler keeps the top and leaves the faulting stack pointer as the floor of the frames after its record.
The walk checks the top on every frame after the first, so a report ends at the wrapper or at the stack top.

## Commands

```
go test ./internal/testing/e2ecompiler -run 'TestSelfHostAbortBacktraceCorruptFrame$|TestSelfHostAbort|TestSelfHostStackOverflow$'
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
