# Stack guard startup checks

Status: partial implementation for [SEC-015](https://github.com/JakeChampion/lang/issues/13517).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

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

## Regression evidence

`TestSelfHostStackGuardDenial` uses a test-only seccomp launcher.
The launcher denies only a 1 MiB `mprotect` request with `PROT_NONE`.
It verifies its own EPERM injection before it executes the program.
The positive control must print its marker and return 42 without the launcher.
The denied program must print only the guard diagnostic and return 134.
The test requires an empty stdout stream on the denied path.

Before the fix, both targets printed the marker and returned 42 after denial.
After the fix, native x86-64 and QEMU AArch64 passed the denial and positive controls.
Stack-overflow and PAC/BTI tests also passed locally.
The test uses the host-native compiler and also runs on native ARM self-host shards.
The C launcher belongs to the test harness only.
The emitted runtime retains its no-libc contract.

## Remaining limits

This change does not complete SEC-015.
Stack mapping failure still selects the original process stack.
Alternate signal stack and signal handler setup require separate failure-path audits.
Large and dynamic frames need page probes to prevent guard skips.
Stack canaries remain absent.
This guard does not establish complete stack-clash protection.

The AArch64 backend shares this startup code with Darwin.
Its syscall adapter converts Darwin errors to negative errno values.
The Darwin CI tests must verify normal startup and stack-overflow behavior.
Local QEMU results do not replace that native CI evidence.
