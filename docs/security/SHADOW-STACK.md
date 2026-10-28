# The x86-64 shadow stack

Status: activation and test for [SEC-030](https://github.com/JakeChampion/lang/issues/13532), with the IBT entry points of [SEC-031](https://github.com/JakeChampion/lang/issues/13533).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The hardening flag as a whole is described in `docs/HARDENING.md`, and the report of what a process runs with in [MITIGATION-REPORT.md](MITIGATION-REPORT.md).

## What the hardware offers

Intel CET and the AMD equivalent provide two mechanisms.
The shadow stack keeps a second copy of every return address. The processor pushes it on `call` and compares it on `ret`, so an overwritten return address faults.
Indirect branch tracking requires every indirect call or jump to land on `endbr64`.

Linux enables the shadow stack for a user process that asks for it through `arch_prctl`, since kernel 6.6 with `CONFIG_X86_USER_SHADOW_STACK`.
Linux has no user-space support for indirect branch tracking.
A kernel can enable it for itself (`CONFIG_X86_KERNEL_IBT`), and that protects the kernel, not the program.

## What -harden does

After the stack switch and the canary read, a hardened x86-64-linux program calls `arch_prctl(ARCH_SHSTK_ENABLE, ARCH_SHSTK_SHSTK)`.
When the call succeeds, the kernel allocates the shadow stack and the processor checks every return from then on. The program then calls `arch_prctl(ARCH_SHSTK_LOCK, ARCH_SHSTK_SHSTK)`, so nothing can turn it off again.
When the call fails, which a kernel or a processor without the feature answers with `EINVAL`, the program continues as before.
Nothing is emitted for a program without `-harden`.

The emitted code is compatible with an active shadow stack.
Every call and return is balanced, a tail call is a direct jump that keeps the caller's return address, and the runtime has no `longjmp`.
A signal handler returns through `rt_sigreturn`, which restores the shadow stack pointer the kernel saved.
The fault reports end the process with `exit_group` from the handler and return through no frame.
The foreign-call shims call C code, which is also balanced unless it unwinds across the shim.

The `endbr64` that each `-harden` stub carries is the entry indirect branch tracking requires ([CFI.md](CFI.md)).
The image carries no `.note.gnu.property`, because no loader reads one for a static image and the kernel enforces nothing for user space.
A claim that the program runs with IBT is therefore never true on Linux today, and `std/mitigations` reports it from the kernel's own records.

## Verification

`TestSelfHostShadowStack` runs on x86-64-linux.

- The plain image carries no `arch_prctl` request, and the hardened image carries it.
- The hardened program prints the `x86_Thread_features` line of its own `/proc/self/status`.
- A hijack probe patches the emitted assembly of a framed function. The function overwrites its own return address with the address of code that exits with status 7. Without a shadow stack the hijack succeeds and the process exits 7, which is the control. With an active shadow stack the return faults with `SIGSEGV`.

On this host the kernel is built without `CONFIG_X86_USER_SHADOW_STACK` and the processor lists no `user_shstk`. The request answers `EINVAL`, the status line is absent, and the hijack control exits 7.
The fault test therefore records the shadow stack as unverified here.
A host whose kernel and processor have the feature runs the same test with the fault expectation.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostShadowStack|TestSelfHostStackCanary|TestSelfHostForwardEdgeCFI' -count=1 -timeout 0
```

## Remaining limits

- Physical verification of the fault is unavailable on this host. The record of a host that enforces it goes in the issue.
- A debugger that rewrites return addresses cannot trace a program with an active shadow stack. A C library that unwinds across a Fern frame faults.
- Indirect branch tracking has no user-space enforcement on Linux. The entry points are in place, and the report says so honestly.
- The unwind tables describe the ordinary stack only. A profiler that reads the shadow stack is not supported.
