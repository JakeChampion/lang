# Thread-local storage, unwind tables, properties and stubs

Status: audit for [SEC-029](https://github.com/JakeChampion/lang/issues/13531).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The image layouts are in [IMAGE-PERMISSIONS.md](IMAGE-PERMISSIONS.md), and the hardening notes in `docs/HARDENING.md`.

## What the images carry

| Item | State |
|---|---|
| Thread-local storage | None. The runtime has no threads, `clone` serves `fork`-style spawning only, and neither backend touches `%fs` or `tpidr_el0`. Every runtime global is process-global. |
| Unwind tables | Both native targets emit `.eh_frame` and `.eh_frame_hdr`, and the writer maps them read-only under `PT_GNU_EH_FRAME`. The CFI records every frame, including the signed-return state on arm64. |
| GNU property note | The arm64-linux `-harden` image carries `PT_GNU_PROPERTY` with the BTI bit. The x86-64 image carries none, because Linux enforces no user-space IBT and the shadow stack is requested through `arch_prctl` ([SHADOW-STACK.md](SHADOW-STACK.md)). |
| Procedure linkage table | None. Every image is static and every call is direct, apart from the indirect calls the backends emit and check ([CFI.md](CFI.md)). A shared object exports functions through its dynamic symbol table and imports nothing. |
| Veneers | None. The arm64 assembler refuses a branch out of range instead of inserting a veneer, so no generated code lacks a landing pad or a signature. |
| Linker-generated stubs | The only stubs are the `-harden` landing-pad stubs, which the compiler emits and which carry the pad themselves. |

## Verification

- `TestSelfHostUnwindData` pins the unwind tables of both targets, and `TestSelfHostMachOPlacesEhFrame` the Darwin placement.
- `TestSelfHostAbortBacktrace*` and `TestSelfHostAbortBacktraceCorruptFrame` walk frames through hardened calls, and the arm64 walk strips the signature before it prints a return address.
- `TestSelfHostHardenArm64` requires the property note in the in-process image and in the image gcc links from the emitted text.
- `TestSelfHostImagePermissions` and `TestSharedObjectLayout` pin every program header.

The emitted text linked with gcc keeps the property only when every input carries it.
An `-emit asm` build linked with a C object built without BTI drops the note, which is the linker's rule, and `docs/HARDENING.md` records it.

## Remaining limits

- A shared object carries no property note, so a BTI host does not guard its pages.
- The arm64-android image carries no property note.
- The runtime's hand-written frames store an unsigned return address on arm64.
- There is no test that links the emitted text with an incompatible object and inspects the dropped note.
