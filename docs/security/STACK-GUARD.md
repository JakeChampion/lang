# Stack guards, probes and canaries

Status: partial implementation for [SEC-015](https://github.com/JakeChampion/lang/issues/13517).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
This record covers the startup checks, the page probes, and the stack canary.

## Existing stack protection

The native backends already map a 1 GiB application stack and a 1 MiB guard.
They request `PROT_NONE` for the guard before they switch the stack pointer.
They also map a 64 KiB alternate signal stack and install a stack-overflow handler.
These facilities exist in the original security audit baseline.
The original `absent` classification for SEC-015 was incorrect.

`TestSelfHostStackOverflow` checks recursion overflow, a valid million-deep call chain, and an unrelated memory fault.
It executes x86-64 programs natively and AArch64 programs with QEMU on an x86 host.

## Guard protection failure

Startup previously ignored the return value from the guard's `mprotect` call.
The program entered `main` with an accessible guard after a kernel refusal.
Startup now checks for a zero return value before it switches the stack pointer.
A nonzero return prints this fixed diagnostic and exits with status 134:

```text
fern: could not protect the stack guard
```

The failure path uses raw write and exit syscalls.
It requires no allocation, frame walk, application stack, or installed signal handler.
It does not call `main`.

## Signal stack and fault handler failures

Startup maps a 64 KiB alternate signal stack, installs it with `sigaltstack`, and installs the fault handler with `rt_sigaction`.
Startup previously ignored a failed map and skipped the two installs without a message.
It also ignored the result of each install.
A fault in such a process ended by signal without a report.
Each of the three calls now stops the program before `main` on failure.

The diagnostics are fixed strings, and each exits with status 134:

```text
fern: could not map the signal stack
fern: could not install the signal stack
fern: could not install the fault handler
```

The Linux arm64 and x86-64 startups carry the checks.
The Darwin startup keeps its previous behavior, which this Linux program does not change.

## Frame probes

A function whose frame is larger than one page could move the stack pointer past the guard in one step.
Nothing then stops a later store from landing in whatever mapping lies below the guard.
Both native backends now emit a probe loop for a frame above 4096 bytes.
The loop moves the stack pointer one page at a time and stores a zero at each page.
A page inside the guard faults, and the fault handler reports the stack overflow.

A frame of one page or less keeps the single subtraction.
The compiler itself has four such functions on x86-64, with frames up to 8120 bytes, and none on arm64.

## Stack canary

The `-harden` flag adds a stack canary on arm64-linux and x86-64-linux.
The hardening reference in `docs/HARDENING.md` describes the layout, the startup read, and the failure path.

## Regression evidence

`TestSelfHostStackGuardDenial`, `TestSelfHostSignalStackMapDenial`, `TestSelfHostSignalStackDenial` and `TestSelfHostFaultHandlerDenial` use a test-only seccomp launcher.
The launcher takes a mode and denies one startup call: the guard `mprotect`, the 64 KiB `mmap`, `sigaltstack`, or `rt_sigaction` for signal 11.
It verifies its own errno injection before it executes the program.

The positive control must print its marker and return 42 without the launcher.
The denied program must print only the diagnostic and return 134.
The test requires an empty stdout stream on the denied path.

QEMU user mode emulates `sigaltstack` and `rt_sigaction` without a host system call.
Under QEMU the two tests verify the emitted check instruction and leave the run to a native arm64 host.
The native ARM self-host shards run the denial.

`TestSelfHostFrameProbes` builds a function that keeps 600 call results live, so its frame is 4760 bytes on x86-64.
The test requires the probe loop in the emitted text and runs the program on both targets.
It then widens the probed frame to 2 MiB, twice the guard, and recurses 600 levels.
The widened program must report `fern: stack overflow` and exit 134.

Before this change, both targets printed the marker and returned 42 under each of the three new denials.
Neither target emitted a probe, and the widened frame without probes faulted on the first push below the mapping.
The fault handler reported that fault as an overflow because it lay within one MiB of the stack pointer.
A mapping below the guard receives such writes without a report.

After the change, native x86-64 and QEMU arm64 pass every denial, probe, canary, overflow, corrupt-frame and hardening test.
The C launcher belongs to the test harness only.
The emitted runtime retains its no-libc contract.

## Remaining limits

This change does not complete SEC-015.
Stack mapping failure still selects the original process stack.
That stack has the kernel's guard gap, and the fault handler then reports a fault near the stack pointer as an overflow.
Fern has no dynamic frames, so the probes cover every frame the compiler emits.

The hand-written runtime frames, leaf functions and bare frames hold no canary.
The canary is opt-in through `-harden`.
This record does not establish complete stack-clash protection.

The AArch64 backend shares this startup code with Darwin.
Its syscall adapter converts Darwin errors to negative errno values.
The Darwin startup skips the two new install checks.
The Darwin CI tests must verify normal startup and stack-overflow behavior.
Local QEMU results do not replace that native CI evidence.
