# Image permissions, W^X, NX stacks and RELRO

Status: audit and fix for [SEC-028](https://github.com/JakeChampion/lang/issues/13530).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The position-independent image is described in [PIE.md](PIE.md), and the `.note.gnu.property` emission in `docs/HARDENING.md`.

## The layouts

| Image | Segments | Stack |
|---|---|---|
| x86-64-linux and arm64-linux executable | code and unwind tables read-execute, constants, globals and BSS read-write | `PT_GNU_STACK` without execute |
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

## Where RELRO does not apply

A Fern executable has no dynamic loader, no global offset table and no procedure linkage table.
The only words a loader-like step patches are the absolute addresses the `-pie` startup loop relocates.
They lie in the read-write data segment with the mutable globals, and they stay writable after the loop.
The equivalent of RELRO for them is a separate segment that the startup loop maps read-only after the relocation.
The constant string boxes in that set carry a reference count that the runtime writes. The design has to settle how a static box is counted before it moves the tables.

## Verification

`TestSelfHostImagePermissions` builds seven variants on both targets, reads the program headers, runs the program, and runs two probes.
The first probe finds its own executable mapping in `/proc/self/maps` and stores a byte into it.
The second patches the image's entry point to the data segment and writes an exit system call over its first bytes.
Both must end with `SIGSEGV`, natively on x86-64 and under QEMU on arm64.

`TestSharedObjectLayout` builds a shared object for each target. It requires a read-execute code segment first, no writable executable segment, a non-executable stack and `PT_DYNAMIC`. It also requires a page-aligned `PT_GNU_RELRO` that covers the dynamic section inside a non-executable load segment.
The dlopen tests of `internal/testing/e2e` load the x86-64 object through glibc and call its exports.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostImagePermissions|TestSelfHostX86WXExecutable|TestSelfHostPIE' -count=1 -timeout 0
go test ./internal/testing/e2e -run 'TestSharedObjectLayout|TestSharedLibX86|TestCLIShared' -count=1 -timeout 0
```

## Remaining limits

- The relocated words of a `-pie` executable stay writable, as described above.
- The arm64-android image has no `PT_GNU_PROPERTY`, and the Darwin image is Mach-O with its own rules.
- QEMU user mode enforces page permissions through the host, so the arm64 probes are emulated evidence.
- A `-shared` object has no `PT_GNU_PROPERTY`, so a BTI host does not guard its pages.
