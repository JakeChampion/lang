# Thread-local storage, unwind tables, properties and stubs

Status: audit for [SEC-029](https://github.com/JakeChampion/lang/issues/13531).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The image layouts are in [IMAGE-PERMISSIONS.md](IMAGE-PERMISSIONS.md), and the hardening notes in `docs/HARDENING.md`.

## What the images carry

| Item | State |
|---|---|
| Thread-local storage | None. The runtime has no threads, `clone` serves `fork`-style spawning only, and neither backend touches `%fs` or `tpidr_el0`. Every runtime global is process-global. |
| Unwind tables | Both native targets emit `.eh_frame` and `.eh_frame_hdr`, and the writer maps them read-only under `PT_GNU_EH_FRAME`. The CFI records every frame, including the signed-return state on arm64. |
| GNU property note | The arm64-linux and arm64-android `-harden` executables and shared objects carry `PT_GNU_PROPERTY` with the BTI bit. The x86-64 image carries none, because Linux enforces no user-space IBT and the shadow stack is requested through `arch_prctl` ([SHADOW-STACK.md](SHADOW-STACK.md)). |
| Procedure linkage table | None. Every image is static and every call is direct, apart from the indirect calls the backends emit and check ([CFI.md](CFI.md)). A shared object exports functions through its dynamic symbol table. A C import (`@import("liblog.so", "sym")`) adds a `DT_NEEDED` entry, an undefined symbol and a `GLOB_DAT` slot that the loader fills before any call. A weak import (`@import("libandroid.so", "sym", weak)`) makes the symbol weak. The library then loads where the symbol is absent, and the loader writes zero in the slot. A call goes through a stub that reads the slot. There is no lazy binding. |
| Veneers | None. The arm64 assembler refuses a branch out of range instead of inserting a veneer, so no generated code lacks a landing pad or a signature. |
| Linker-generated stubs | The only stubs are the `-harden` landing-pad stubs, which the compiler emits and which carry the pad themselves. |

## Verification

- `TestSelfHostUnwindData` pins the unwind tables of both targets, and `TestSelfHostMachOPlacesEhFrame` the Darwin placement.
- `TestSelfHostAbortBacktrace*` and `TestSelfHostAbortBacktraceCorruptFrame` walk frames through hardened calls, and the arm64 walk strips the signature before it prints a return address.
- `TestSelfHostHardenArm64` requires the property note in the in-process image and in the image gcc links from the emitted text.
- Its `mixed-link` subtest links the emitted text with a C object. The note stays with an object built with `-mbranch-protection=standard` and goes with one built with `-mbranch-protection=none`. Both images run.
- `TestSelfHostHardenShared` requires the property note in the arm64 `-harden` shared object and not in the plain one.
- `TestSelfHostHardenAndroid` requires the note in the arm64-android `-harden` executable and shared object and not in the plain ones, and runs both Android executables under `-harden`.
- `TestSelfHostImagePermissions` and `TestSharedObjectLayout` pin every program header.

The emitted text linked with gcc keeps the property only when every input carries it.
An `-emit asm` build linked with a C object built without BTI drops the note with no warning. That is the linker's rule, and `docs/HARDENING.md` records it.

## Remaining limits

- A link with an unprotected C object drops BTI with no warning. The `mixed-link` subtest pins the rule, and the link command must check the result itself.
