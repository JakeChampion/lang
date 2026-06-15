// Package elf writes minimal static, non-PIE ELF-64 executables —
// the container half of the native-binary path that aims to replace
// the aarch64-linux-gnu-gcc link step in cmd/fern (Phase 3 of
// docs/TOOLCHAIN-SELF-HOSTING.md).
//
// Because a Fern program links to one self-contained blob —
// `-static -nostdlib`, no archives, no shared objects, no
// relocations against external symbols, no PLT/GOT — the "linker"
// is trivial: lay the headers + .text out at a fixed virtual
// address and point e_entry at the first instruction. There are no
// section headers (the kernel loader needs only the program
// headers).
//
// Two layouts are offered. The original single-PT_LOAD image
// (StaticExecutable / StaticExecutableData) maps the whole file
// R+W+X — simplest, but a writable+executable mapping is rejected
// by W^X-enforcing loaders (notably Android's SELinux policy). The
// W^X layout (StaticExecutableDataWX) splits code and data into two
// PT_LOAD segments — an R+X segment for the headers + .text and a
// separate R+W segment for .rodata + the writable globals — so no
// mapping is ever both writable and executable. The data segment
// starts on the first page boundary past .text; the assembler
// resolves data-symbol references against that page-aligned address
// (arm64.BytesProgramWX / x86_64.AssembleProgramWX).
//
// Reference: ELF-64 gABI
// (https://refspecs.linuxfoundation.org/elf/gabi4+/contents.html).
package elf

const (
	ehSize    = 64       // ELF-64 header size (e_ehsize)
	phSize    = 56       // ELF-64 program-header entry size (e_phentsize)
	baseVAddr = 0x400000 // load address; multiple of pageAlign
	pageAlign = 0x10000  // arm64 max page size (16 KiB) for p_align
	emAArch64 = 183      // EM_AARCH64 (e_machine)
	emX86_64  = 62       // EM_X86_64 (e_machine)
)

// TextVAddr is the virtual address at which .text begins in the
// single-segment image (just past the ELF header + one program
// header). The assembler needs this to resolve PC-relative symbol
// references (adrp / :lo12:); pass it to arm64.AssembleProgram.
const TextVAddr = baseVAddr + ehSize + phSize

// TextVAddrWX is the virtual address at which .text begins in the
// W^X two-segment image: just past the ELF header + *two* program
// headers (code + data), so it sits one phSize further in than
// TextVAddr. Pass it to arm64.BytesProgramWX / x86_64.AssembleProgramWX
// as the textVAddr so PC-relative fixups line up with the layout
// StaticExecutableDataWX produces.
const TextVAddrWX = baseVAddr + ehSize + 2*phSize

// pageUp rounds v up to the next pageAlign (16 KiB) boundary. The
// W^X data segment begins here so it never shares a page — and thus
// never shares page protections — with the R+X code segment.
func pageUp(v uint64) uint64 {
	return (v + pageAlign - 1) &^ (pageAlign - 1)
}

// StaticExecutableData wraps .text + a data blob (rodata followed by
// the zero-initialised .bss globals, which AssembleProgram appends) into
// a runnable static ELF-64 executable. Both live in a single PT_LOAD,
// with the data placed 8-byte aligned after .text — matching the layout
// arm64.AssembleProgram assumes when it resolves adrp/:lo12: against
// TextVAddr. The segment is mapped R+W+X: the .bss globals (allocator
// cursors / freelist) must be writable, and a single segment keeps the
// loader simple; W^X separation is a future refinement for this
// no-PIE, fixed-address experimental backend. Entry is the first
// instruction of .text.
func StaticExecutableData(text, data []byte) []byte {
	return staticExecutableData(text, data, emAArch64)
}

// StaticExecutableDataX86 is the x86-64 counterpart of
// StaticExecutableData: identical single-segment layout, only the ELF
// e_machine field differs (EM_X86_64). The x86-64 native assembler lays
// .text at TextVAddr and .rodata immediately after, same as arm64.
func StaticExecutableDataX86(text, data []byte) []byte {
	return staticExecutableData(text, data, emX86_64)
}

func staticExecutableData(text, data []byte, machine uint16) []byte {
	body := padTo8(append([]byte(nil), text...))
	body = append(body, data...)
	return image(body, 7, machine) // PF_R | PF_W | PF_X
}

// StaticExecutableDataWX is the W^X counterpart of StaticExecutableData:
// it lays .text and the data blob into two separate PT_LOAD segments — an
// R+X segment (ELF/program headers + .text) and an R+W segment (.rodata +
// the writable globals: allocator cursors, freelist) — so the image never
// contains a writable+executable mapping. The data blob starts on the
// first 16 KiB page boundary past .text and is loaded at TextVAddrWX +
// (its page-aligned offset); the assembler must have resolved data-symbol
// references against that address (see arm64.BytesProgramWX). Entry is the
// first instruction of .text. arm64 (EM_AARCH64).
func StaticExecutableDataWX(text, data []byte) []byte {
	return staticExecutableDataWX(text, data, emAArch64)
}

// StaticExecutableDataX86WX is the x86-64 counterpart of
// StaticExecutableDataWX: identical two-segment W^X layout, only the ELF
// e_machine field differs (EM_X86_64). Pair it with
// x86_64.AssembleProgramWX.
func StaticExecutableDataX86WX(text, data []byte) []byte {
	return staticExecutableDataWX(text, data, emX86_64)
}

func staticExecutableDataWX(text, data []byte, machine uint16) []byte {
	return imageWX(text, data, machine)
}

// imageWX emits an ELF header (e_phnum = 2) + two PT_LOAD program headers
// followed by .text and, on the next page boundary, the data blob. The
// code segment (headers + .text) is mapped R+X; the data segment is mapped
// R+W. File offsets equal virtual-address offsets (both measured from
// baseVAddr) so the page-aligned data offset is congruent to its load
// address mod the page size — what mmap requires.
func imageWX(text, data []byte, machine uint16) []byte {
	const headers = ehSize + 2*phSize        // 64 + 112 = 176
	textEnd := uint64(headers + len(text))   // end of the R+X segment
	dataOff := pageUp(textEnd)                // file offset == vaddr offset of data
	codeVAddr := uint64(baseVAddr)            // headers + .text
	dataVAddr := uint64(baseVAddr) + dataOff  // .rodata + writable globals
	entry := uint64(TextVAddrWX)              // first instruction of .text
	codeSz := textEnd                         // headers + .text live in one segment
	dataSz := uint64(len(data))
	total := dataOff + dataSz

	buf := make([]byte, 0, total)

	// ---- ELF-64 header (64 bytes) ----
	buf = append(buf, 0x7f, 'E', 'L', 'F')    // EI_MAG
	buf = append(buf, 2, 1, 1, 0)             // class=ELF64, data=LE, version=1, osabi=SysV
	buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0) // EI_PAD
	buf = le16(buf, 2)                        // e_type    = ET_EXEC
	buf = le16(buf, machine)                  // e_machine
	buf = le32(buf, 1)                        // e_version = EV_CURRENT
	buf = le64(buf, entry)                    // e_entry
	buf = le64(buf, ehSize)                   // e_phoff (program headers follow the ELF header)
	buf = le64(buf, 0)                        // e_shoff (no section headers)
	buf = le32(buf, 0)                        // e_flags
	buf = le16(buf, ehSize)                   // e_ehsize
	buf = le16(buf, phSize)                   // e_phentsize
	buf = le16(buf, 2)                        // e_phnum (code + data)
	buf = le16(buf, 0)                        // e_shentsize
	buf = le16(buf, 0)                        // e_shnum
	buf = le16(buf, 0)                        // e_shstrndx

	// ---- Program header 0 (56 bytes): R+X code (headers + .text) ----
	buf = le32(buf, 1)         // p_type  = PT_LOAD
	buf = le32(buf, 5)         // p_flags = PF_R | PF_X
	buf = le64(buf, 0)         // p_offset
	buf = le64(buf, codeVAddr) // p_vaddr
	buf = le64(buf, codeVAddr) // p_paddr
	buf = le64(buf, codeSz)    // p_filesz
	buf = le64(buf, codeSz)    // p_memsz
	buf = le64(buf, pageAlign) // p_align

	// ---- Program header 1 (56 bytes): R+W data (.rodata + globals) ----
	buf = le32(buf, 1)         // p_type  = PT_LOAD
	buf = le32(buf, 6)         // p_flags = PF_R | PF_W
	buf = le64(buf, dataOff)   // p_offset
	buf = le64(buf, dataVAddr) // p_vaddr
	buf = le64(buf, dataVAddr) // p_paddr
	buf = le64(buf, dataSz)    // p_filesz
	buf = le64(buf, dataSz)    // p_memsz
	buf = le64(buf, pageAlign) // p_align

	// ---- body: .text, then page padding, then the data blob ----
	buf = append(buf, text...)
	for uint64(len(buf)) < dataOff {
		buf = append(buf, 0)
	}
	return append(buf, data...)
}

func padTo8(b []byte) []byte {
	for len(b)%8 != 0 {
		b = append(b, 0)
	}
	return b
}

// StaticExecutable wraps a flat .text blob (a sequence of encoded
// machine instructions) into a runnable static ELF-64 executable for
// arm64 Linux (R+X). Execution begins at the first byte of text; the
// program is responsible for its own startup and for exiting via a
// syscall (there is no libc / crt0).
func StaticExecutable(text []byte) []byte {
	return image(text, 5, emAArch64) // PF_R | PF_X
}

// image emits the ELF header + one PT_LOAD (with the given p_flags)
// covering the whole file, followed by the body.
func image(body []byte, flags uint32, machine uint16) []byte {
	total := uint64(ehSize + phSize + len(body))
	entry := uint64(baseVAddr + ehSize + phSize)

	buf := make([]byte, 0, total)

	// ---- ELF-64 header (64 bytes) ----
	buf = append(buf, 0x7f, 'E', 'L', 'F')    // EI_MAG
	buf = append(buf, 2, 1, 1, 0)             // class=ELF64, data=LE, version=1, osabi=SysV
	buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0) // EI_PAD
	buf = le16(buf, 2)                        // e_type    = ET_EXEC
	buf = le16(buf, machine)                  // e_machine
	buf = le32(buf, 1)                        // e_version = EV_CURRENT
	buf = le64(buf, entry)                    // e_entry
	buf = le64(buf, ehSize)                   // e_phoff (program headers follow the ELF header)
	buf = le64(buf, 0)                        // e_shoff (no section headers)
	buf = le32(buf, 0)                        // e_flags
	buf = le16(buf, ehSize)                   // e_ehsize
	buf = le16(buf, phSize)                   // e_phentsize
	buf = le16(buf, 1)                        // e_phnum
	buf = le16(buf, 0)                        // e_shentsize
	buf = le16(buf, 0)                        // e_shnum
	buf = le16(buf, 0)                        // e_shstrndx

	// ---- Program header (56 bytes): one PT_LOAD covering the file ----
	buf = le32(buf, 1)         // p_type  = PT_LOAD
	buf = le32(buf, flags)     // p_flags
	buf = le64(buf, 0)         // p_offset
	buf = le64(buf, baseVAddr) // p_vaddr
	buf = le64(buf, baseVAddr) // p_paddr
	buf = le64(buf, total)     // p_filesz
	buf = le64(buf, total)     // p_memsz
	buf = le64(buf, pageAlign) // p_align

	return append(buf, body...)
}

func le16(buf []byte, v uint16) []byte {
	return append(buf, byte(v), byte(v>>8))
}

func le32(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func le64(buf []byte, v uint64) []byte {
	return append(buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}
