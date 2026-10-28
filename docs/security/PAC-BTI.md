# AArch64 pointer authentication and branch target identification

Status: audit for [SEC-032](https://github.com/JakeChampion/lang/issues/13534) and [SEC-033](https://github.com/JakeChampion/lang/issues/13535).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The mechanisms are described in `docs/HARDENING.md`, the indirect-call check in [CFI.md](CFI.md), and the process report in [MITIGATION-REPORT.md](MITIGATION-REPORT.md).

## What -harden emits on arm64-linux

- Every function with a frame signs its return address with `paciasp` before the frame record stores it. Every exit authenticates it with `autiasp`, ahead of the `ret` or the tail call's `b`.
- Every address-taken function has a stub that starts with `bti c`, and a function value points at the stub. A function's own entry carries no pad, so the BTI target set is exactly the set of address-taken functions.
- The runtime entry points the kernel reaches, such as the signal catcher, carry `bti jc`.
- The image carries `PT_GNU_PROPERTY` with the BTI bit, so a kernel with BTI maps the code as guarded pages.
- The backtrace walk strips the signature with `xpaclri` before it prints a return address.

Each instruction is in the hint space, so a hardened image runs on a core without the feature.

## Evidence

| Claim | Where it is recorded |
|---|---|
| The instructions are present and counted | `TestSelfHostHardenArm64` counts the pads, the signatures and the authentications in the in-process image |
| A removed pad faults under BTI | the test's BTI fault control, which also turns the software indirect-call compare off, under QEMU 8.2.2 and on the native ARM shard |
| A removed signature faults under PAC | the test's PAC fault control, under QEMU and on the native ARM shard (run 40405443296, kernel 6.17.0-1022-azure, `PAC fault control passed (native)`) |
| A forged function pointer is refused before the branch | `TestSelfHostForwardEdgeCFI` under QEMU |
| What a process runs with | `std/mitigations` reads `/proc/self/status`, the auxiliary vector, the memory map flags and `prctl` |

QEMU user mode emulates PAC and BTI, so a QEMU result is emulated evidence.
The native ARM shard gives physical evidence for PAC on one host and one function sample.
BTI enforcement on physical hardware is not yet recorded, because the native runner did not request the BTI control.

## Remaining limits

- The runtime's hand-written frames (`__fern_*` helpers written as assembly text) store an unsigned return address.
- A shared object and the Android image carry no property note.
- The PAC key is the kernel's per-process key. There is no key rotation on `fork`, which the kernel handles.
- The physical BTI fault test waits for a runner that requests it, and the physical PAC record covers one host.
