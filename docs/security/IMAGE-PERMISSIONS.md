# Image permissions, W^X, NX stacks and RELRO

Status: audit and fix for [SEC-028](https://github.com/JakeChampion/lang/issues/13530).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The position-independent image is described in [PIE.md](PIE.md), and the `.note.gnu.property` emission in `docs/HARDENING.md`.

## The layouts

| Image | Segments | Stack |
|---|---|---|
| x86-64-linux and arm64-linux executable | code and unwind tables read-execute, constant boxes and address tables in a separate read-only load segment, globals and BSS read-write | `PT_GNU_STACK` without execute |
| the same with `-pie` | code read-execute, data read-write at load time, constants read-only after relocation under `PT_GNU_RELRO` | the same |
| arm64-linux with `-harden` | the same plus `PT_GNU_PROPERTY` asking for BTI | the same |
| x86-64-linux and arm64-linux `-shared` | code read-execute, data read-write, the dynamic tables read-write under `PT_GNU_RELRO`, `PT_DYNAMIC` | `PT_GNU_STACK` without execute |

These final permissions also hold with `-harden`, `-g`, `-sanitize`, `-backtrace` and their combinations. PIE uses the relocation row.
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

The data bytes follow the code in the file at a 16-byte boundary. Each load segment has the same offset within a page in the file and in memory.

For a fixed-address image, the kernel maps the constant region read-only from the start. No protection syscall or auxiliary-vector scan is needed.
A virtual gap separates the read-only and writable load segments. Its size is the target's load alignment, so their virtual pages cannot overlap on any supported page size.
The file omits that gap. A write through a writable file-page alias creates a private copy and cannot change the separate read-only mapping.

PIE maps its constants in a separate writable segment until startup completes relocation.
Mapped zero-fill space separates their virtual pages from the globals. The file omits that space.
`PT_GNU_RELRO` names the mapped range through its final 4096-byte boundary.
After relocation, `_start` reads `AT_PAGESZ` and rounds both ends down to that page size.
It then calls `mprotect` with read permission on the pages between them.

Android shared images retain the dynamic loader's page-aligned RELRO layout.
Constant headers use eight-byte alignment on both native targets.

A failed call ends the process with `fern: protection of the constant data failed`, status 134.
The region is empty in a `-shared` object, on Darwin and on the freestanding targets. There the emitters keep the constants in `.data`.

The constant boxes carry a negative reference count. The count paths skip a negative count, so the runtime never writes a constant box, and the read-only mapping holds.

A builtin that writes into an array argument in place never reaches a constant. Those builtins are `reactor_wait`, `ring_wait`, `ring_recv`, `tcp_recv_into`, `tcp_addr_into`, `udp_recvfrom` and `__arr_set_len`, named in `ssasem.raw_array_args`.
The unit plan builds a literal on such a path as a fresh heap box ([#14013](https://github.com/JakeChampion/lang/issues/14013)).
`ssarawflow.reached` traces storage through projections, containers, cells and call results.
`ssaunits.write_rows` propagates parameter effects to callers and return demands to callees.
Indirect calls expose their storage arguments conservatively.
A demanded indirect result reaches every function with the same result type.
Raw reads through a cell demand every store with the same cell type.
The worklist revisits those stores before it converges.
The plan cache compares exact parameter flags and the complete sets of return and cell demands.
Constant placement reuses the final reach for the same input graphs.
The length setter skips a box with a negative count, so the shared empty array stays a constant that no code writes.
The array literals and the empty array are in the region with the other constants.

## Verification

`TestSelfHostImagePermissions` builds seven variants on both targets, reads the program headers, runs the program, and runs three probes.
It requires `PT_GNU_RELRO` to match the read-only load segment in a fixed-address image.
It also checks that the read-only and writable virtual pages cannot overlap at the target's load alignment.
For PIE, the mapped constant segment ends on a 4096-byte boundary before the globals.
The constant bytes and globals must occupy separate virtual pages.
Their file ranges remain adjacent, with no copy of the virtual gap.
Empty-image controls check every variant too.

The first probe finds its own executable mapping in `/proc/self/maps` and stores a byte into it.
The second finds the read-only mapping of its data segment and stores a byte into it.
The third patches the image's entry point to a data segment and writes an exit system call over its first bytes. It takes the writable segment, or the read-only one when the writable segment has no file bytes.
All three must end with `SIGSEGV`. The suite supports native Linux ARM64 and x86-64, with QEMU for the other target.

`TestSelfHostWrittenLiteralImage` runs a program that hands a literal to `reactor_wait`, directly and through a callee, and sets the length of the empty array.
It must exit 0 on a fixed-address image and on a PIE image. `TestSelfHostWrittenLiteralIsHeapX86_64` pins the plan: such a literal is a heap box, and a literal a callee only reads stays a constant.

`TestSharedObjectLayout` builds a shared object for each target. It requires a read-execute code segment first, no writable executable segment, a non-executable stack and `PT_DYNAMIC`. It also requires a page-aligned `PT_GNU_RELRO` that covers the dynamic section inside a non-executable load segment.
The dlopen tests of `internal/testing/e2e` load the x86-64 object through glibc and call its exports.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostImagePermissions|TestSelfHostX86WXExecutable|TestSelfHostPIE' -count=1 -timeout 0
go test ./internal/testing/e2e -run 'TestSharedObjectLayout|TestSharedLibX86|TestCLIShared' -count=1 -timeout 0
```

## Remaining limits

- PIE and Android protection rounds the length down to the kernel's page size. A kernel with 16 KiB or 64 KiB pages can protect fewer pages than a kernel with 4 KiB pages. Fixed-address images have a separate read-only load segment on each supported page size.
- The arm64-android image has no `PT_GNU_PROPERTY`, and the Darwin image is Mach-O with its own rules.
- QEMU user mode enforces page permissions through the host, so the arm64 probes are emulated evidence.
- A `-shared` object has no `PT_GNU_PROPERTY`, so a BTI host does not guard its pages.
