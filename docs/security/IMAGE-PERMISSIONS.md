# Image permissions, W^X, NX stacks and RELRO

Status: audit and fix for [SEC-028](https://github.com/JakeChampion/lang/issues/13530).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The position-independent image is described in [PIE.md](PIE.md), and the `.note.gnu.property` emission in `docs/HARDENING.md`.

## The layouts

| Image | Segments | Stack |
|---|---|---|
| x86-64-linux and arm64-linux executable | code and unwind tables read-execute, the constant boxes, aggregates and address tables read-only after startup under `PT_GNU_RELRO`, globals and BSS read-write | `PT_GNU_STACK` without execute |
| the same with `-pie` | the same at base 0, loaded at a random base | the same |
| arm64-linux with `-harden` | the same plus `PT_GNU_PROPERTY` asking for BTI | the same |
| x86-64-linux and arm64-linux `-shared` | code read-execute, data read-write, the dynamic tables read-write under `PT_GNU_RELRO`, `PT_DYNAMIC` | `PT_GNU_STACK` without execute |

Every variant of an executable (`-pie`, `-harden`, `-g`, `-sanitize`, `-backtrace` and their combinations) keeps the first row's shape.
`-harden` stores its stubs in the code segment, so the allowed indirect targets are read-only ([CFI.md](CFI.md)).

## Change in this record

The x86-64 shared object used one load segment with read, write and execute permission for the whole image.
A write through a dangling pointer could change its code, and a jump into its data could run.
The writer now uses the two-segment layout the arm64 shared object already had.

The code-write probe found a second defect in the x86-64 `-pie` image.
The kernel maps the image next to the runtime's stack mapping. The fault handler called any fault within 1 MiB of the stack pointer a stack overflow.
The handler now tests the guard range of the runtime's stack, and keeps the stack-pointer window only for the kernel's stack ([CRASH-REPORT-AUDIT.md](CRASH-REPORT-AUDIT.md)).

Both shared objects now carry `PT_GNU_RELRO` over the dynamic tables. Those are the relocation records, the symbol table, the string table, the hash table and the dynamic section.
The dynamic loader applies the relocations and then maps that page range read-only.
The range is page-aligned, because the loader rounds its end down to a page and protects nothing otherwise.

## RELRO in the executables

A Fern executable has no dynamic loader, no global offset table and no procedure linkage table.
The words a loader-like step patches are the absolute addresses the `-pie` startup loop relocates: the string boxes, the constant aggregates and the address tables.
The emitters place them in `.data.rel.ro`. The assembler puts that section at the front of the data segment.

The data bytes follow the code in the file. The segment maps one page later at the same offset within the page, as the loader requires. The driver inserts zero bytes after the region so that it ends on a 4096-byte boundary. That gap takes the place of the page padding the segment start paid before, so an x86-64 image costs no file bytes on average. An arm64 image paid 64 KiB of padding before, for its 64 KiB load alignment, and loses it.

`PT_GNU_RELRO` names the region. After the relocation loop, `_start` reads `AT_PAGESZ` from the auxiliary vector and rounds the start and the end of the region down to that page. It then calls `mprotect` with read permission on the pages between them.
A failed call ends the process with `fern: protection of the constant data failed`, status 134.
The region is empty in a `-shared` object, on Darwin and on the freestanding targets. There the emitters keep the constants in `.data`.

The constant boxes carry a negative reference count. The count paths skip a negative count, so the runtime never writes a constant box, and the read-only mapping holds.

The array literals of bytes and scalars stay in `.data`. `reactor_wait` and `tcp_recv_into` write into their array argument in place, and a literal argument is the shared constant itself ([#14013](https://github.com/JakeChampion/lang/issues/14013)).
The shared empty array that `__alloc_u8(0)` answers stays in `.data` for the same reason: `__arr_set_len` stores the length into it.
Those blocks hold no address. They join the region when the builtins own their argument first.

## Verification

`TestSelfHostImagePermissions` builds seven variants on both targets, reads the program headers, runs the program, and runs three probes.
It requires a `PT_GNU_RELRO` that starts with the data segment and ends on a 4096-byte boundary inside its file bytes.
The first probe finds its own executable mapping in `/proc/self/maps` and stores a byte into it.
The second finds the read-only mapping of its data segment and stores a byte into it.
The third patches the image's entry point to the data segment and writes an exit system call over its first bytes.
All three must end with `SIGSEGV`, natively on x86-64 and under QEMU on arm64.

`TestSharedObjectLayout` builds a shared object for each target. It requires a read-execute code segment first, no writable executable segment, a non-executable stack and `PT_DYNAMIC`. It also requires a page-aligned `PT_GNU_RELRO` that covers the dynamic section inside a non-executable load segment.
The dlopen tests of `internal/testing/e2e` load the x86-64 object through glibc and call its exports.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostImagePermissions|TestSelfHostX86WXExecutable|TestSelfHostPIE' -count=1 -timeout 0
go test ./internal/testing/e2e -run 'TestSharedObjectLayout|TestSharedLibX86|TestCLIShared' -count=1 -timeout 0
```

## Remaining limits

- The array literals of bytes and scalars and the shared empty array stay writable until [#14013](https://github.com/JakeChampion/lang/issues/14013) lands.
- The protected length is rounded down to the kernel's page size. A kernel with 16 KiB or 64 KiB pages protects fewer pages than a kernel with 4 KiB pages.
- The arm64-android image has no `PT_GNU_PROPERTY`, and the Darwin image is Mach-O with its own rules.
- QEMU user mode enforces page permissions through the host, so the arm64 probes are emulated evidence.
- A `-shared` object has no `PT_GNU_PROPERTY`, so a BTI host does not guard its pages.
