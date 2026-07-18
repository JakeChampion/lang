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
	baseVAddr = 0x400000 // load address; multiple of pageAlign (both arches)
	pageAlign = 0x10000  // arm64 max page size (64 KiB) for p_align
	// x86-64 pages are always 4 KiB, so its segments align to 0x1000 instead
	// of the arm64 max-page 0x10000. This matters for the W^X / PIE layouts,
	// where the data segment starts at pageUp(textEnd): at 64 KiB a tiny CLI
	// pads ~64 KiB of zeros between .text and .rodata (a hello-world is 16×
	// too big); at 4 KiB the padding — and the binary — shrink accordingly,
	// while W^X (code and data never share a page) still holds on x86-64's
	// 4 KiB pages. arm64 keeps 64 KiB so the image loads on 4/16/64 KiB-page
	// kernels alike. (#4380 / #4382)
	pageAlignX86 = 0x1000
	emAArch64    = 183 // EM_AARCH64 (e_machine)
	emX86_64     = 62  // EM_X86_64 (e_machine)
)

// pageAlignFor is the segment/p_align page size for the target machine: 4 KiB
// on x86-64 (its only page size), the arm64 64 KiB max-page elsewhere.
func pageAlignFor(machine uint16) uint64 {
	if machine == emX86_64 {
		return pageAlignX86
	}
	return pageAlign
}

// pageUpFor rounds v up to the target machine's page boundary. The W^X data
// segment begins here so it never shares a page — and thus never shares page
// protections — with the R+X code segment.
func pageUpFor(v uint64, machine uint16) uint64 {
	a := pageAlignFor(machine)
	return (v + a - 1) &^ (a - 1)
}

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

// PIE (position-independent / ET_DYN) constants. A Fern PIE is a static,
// no-PLT/GOT image whose only load-base-dependent values are the
// `.quad <symbol>` function-pointer slots; those carry R_*_RELATIVE
// relocations the loader (or the program's own self-relocation prologue)
// applies as `*(base + r_offset) = base + r_addend`.
const (
	etDyn = 3 // e_type = ET_DYN (position-independent)

	ptDynamic          = 2    // p_type = PT_DYNAMIC
	relAArch64Relative = 1027 // R_AARCH64_RELATIVE
	relX86_64Relative  = 8    // R_X86_64_RELATIVE

	// .dynamic d_tag values.
	dtNull      = 0
	dtRela      = 7
	dtRelaSz    = 8
	dtRelaEnt   = 9
	dtRelaCount = 0x6ffffff9 // DT_RELACOUNT: count of R_*_RELATIVE entries

	relaEntSize = 24 // ELF64 Elf64_Rela: r_offset, r_info, r_addend
	dynEntSize  = 16 // ELF64 Elf64_Dyn:  d_tag, d_un
	phNumPIE    = 3  // PT_LOAD(R+X) + PT_LOAD(R+W) + PT_DYNAMIC
)

// Shared-object (.so) dynamic-symbol-table constants. A Fern .so is the
// same ET_DYN image as a static PIE, plus a dynamic symbol table
// (.dynsym/.dynstr), a SysV hash table (.hash), and the matching .dynamic
// tags — so a dynamic loader (dlopen / Android's linker via
// System.loadLibrary) can resolve the exported symbols. No section headers
// are needed: the loader uses PT_DYNAMIC, not the section table.
const (
	dtHash   = 4  // DT_HASH:   .hash vaddr
	dtStrtab = 5  // DT_STRTAB: .dynstr vaddr
	dtSymtab = 6  // DT_SYMTAB: .dynsym vaddr
	dtStrsz  = 10 // DT_STRSZ:  .dynstr size
	dtSyment = 11 // DT_SYMENT: Elf64_Sym size (24)
	dtSoname = 14 // DT_SONAME: soname offset in .dynstr

	symEntSize     = 24   // ELF64 Elf64_Sym
	stInfoGlobFunc = 0x12 // st_info = (STB_GLOBAL<<4) | STT_FUNC
	shnText        = 1    // st_shndx: any non-UNDEF/non-ABS marks "defined" so
	//                       the loader adds the load bias to st_value.
)

// Export is one exported symbol in a .so: Name resolves (via dlsym /
// the dynamic linker) to load_base + Value. Value is the symbol's
// link-time address relative to a load base of 0 (e.g. TextVAddrPIE +
// its .text offset); Size is its byte size (may be 0).
type Export struct {
	Name  string
	Value uint64
	Size  uint64
}

// TextVAddrPIE is the .text address in the static-PIE image, measured from
// a load base of 0 (just past the ELF header + three program headers).
// Pass it to arm64.AssembleProgramPIE / x86_64.AssembleProgramPIE as the
// textVAddr so every PC-relative fixup is laid out base-relative.
const TextVAddrPIE = ehSize + phNumPIE*phSize

// Reloc is one R_*_RELATIVE entry: at load time `*(base + Offset) =
// base + Addend`. Both fields are relative to a load base of 0, matching
// the ET_DYN image StaticPieExecutable produces. The assemblers return
// their own Reloc; callers map them onto this type (kept here so the elf
// package does not depend on the assembler packages).
type Reloc struct {
	Offset uint64
	Addend uint64
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
	return imageWX(text, data, machine, 0)
}

// StaticExecutableDataWXEntry is StaticExecutableDataWX with an explicit
// entry point: entryOff is the byte offset of the entry instruction within
// .text (e_entry = TextVAddrWX + entryOff). The offset-0 default assumes
// `_start` is the first thing in .text — true for the Go backends' output,
// but the SELF-HOST emitters place `_start` after other functions, so a
// binary linked from their asm with entry 0 starts executing mid-function
// and crashes. arm64 (EM_AARCH64).
func StaticExecutableDataWXEntry(text, data []byte, entryOff uint64) []byte {
	return imageWX(text, data, emAArch64, entryOff)
}

// StaticExecutableDataX86WXEntry is the x86-64 sibling of
// StaticExecutableDataWXEntry (EM_X86_64).
func StaticExecutableDataX86WXEntry(text, data []byte, entryOff uint64) []byte {
	return imageWX(text, data, emX86_64, entryOff)
}

// imageWX emits an ELF header (e_phnum = 2) + two PT_LOAD program headers
// followed by .text and, on the next page boundary, the data blob. The
// code segment (headers + .text) is mapped R+X; the data segment is mapped
// R+W. File offsets equal virtual-address offsets (both measured from
// baseVAddr) so the page-aligned data offset is congruent to its load
// address mod the page size — what mmap requires.
func imageWX(text, data []byte, machine uint16, entryOff uint64) []byte {
	const headers = ehSize + 2*phSize        // 64 + 112 = 176
	textEnd := uint64(headers + len(text))   // end of the R+X segment
	dataOff := pageUpFor(textEnd, machine)   // file offset == vaddr offset of data
	codeVAddr := uint64(baseVAddr)           // headers + .text
	dataVAddr := uint64(baseVAddr) + dataOff // .rodata + writable globals
	entry := uint64(TextVAddrWX) + entryOff  // entry instruction within .text
	codeSz := textEnd                        // headers + .text live in one segment
	dataSz := uint64(len(data))              // p_memsz: the full segment (incl. .bss)
	// .bss is materialised as trailing zero bytes in `data`, and a PT_LOAD with
	// p_filesz < p_memsz has the kernel zero-fill [filesz, memsz). So store only up
	// to the last non-zero byte in the file and let the loader supply the rest —
	// the in-memory image is byte-identical, but the file (and the on-disk binary)
	// drops the zero-fill regions (the bump heap, strbuf, args globals). Trimming
	// any trailing zero is safe whether it came from .bss or a rodata run that
	// happens to end in zeros; the loaded bytes are the same either way.
	dataFileSz := uint64(trailingTrimZeros(data))
	total := dataOff + dataFileSz

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
	buf = le32(buf, 1)                     // p_type  = PT_LOAD
	buf = le32(buf, 5)                     // p_flags = PF_R | PF_X
	buf = le64(buf, 0)                     // p_offset
	buf = le64(buf, codeVAddr)             // p_vaddr
	buf = le64(buf, codeVAddr)             // p_paddr
	buf = le64(buf, codeSz)                // p_filesz
	buf = le64(buf, codeSz)                // p_memsz
	buf = le64(buf, pageAlignFor(machine)) // p_align

	// ---- Program header 1 (56 bytes): R+W data (.rodata + globals) ----
	buf = le32(buf, 1)                     // p_type  = PT_LOAD
	buf = le32(buf, 6)                     // p_flags = PF_R | PF_W
	buf = le64(buf, dataOff)               // p_offset
	buf = le64(buf, dataVAddr)             // p_vaddr
	buf = le64(buf, dataVAddr)             // p_paddr
	buf = le64(buf, dataFileSz)            // p_filesz (initialised prefix only; .bss is NOBITS)
	buf = le64(buf, dataSz)                // p_memsz  (full segment incl. zero-filled .bss)
	buf = le64(buf, pageAlignFor(machine)) // p_align

	// ---- body: .text, then page padding, then the data blob's initialised
	// prefix (the trailing .bss zeros are supplied by the loader via memsz). ----
	buf = append(buf, text...)
	for uint64(len(buf)) < dataOff {
		buf = append(buf, 0)
	}
	return append(buf, data[:dataFileSz]...)
}

// trailingTrimZeros returns the length of b with trailing zero bytes removed —
// the size of the initialised prefix that must live in the file (the rest is
// .bss / zero-fill the loader provides).
func trailingTrimZeros(b []byte) int {
	n := len(b)
	for n > 0 && b[n-1] == 0 {
		n--
	}
	return n
}

// StaticPieExecutable wraps .text + data + relocations into a static
// position-independent (ET_DYN) arm64 executable: the W^X two-segment
// layout, laid out from a load base of 0, plus a .rela.dyn section (one
// R_AARCH64_RELATIVE per reloc) and a .dynamic section / PT_DYNAMIC so the
// relocations are discoverable. The kernel maps it at an arbitrary base;
// PC-relative code runs as-is. Programs whose only absolute addresses are
// resolved at startup need a self-relocation prologue to apply .rela.dyn
// (a later slice); reloc-free programs run unchanged. Pair with
// arm64.AssembleProgramPIE(.., TextVAddrPIE).
func StaticPieExecutable(text, data []byte, relocs []Reloc) []byte {
	return staticPie(text, data, relocs, emAArch64)
}

// StaticPieExecutableX86 is the x86-64 counterpart (EM_X86_64,
// R_X86_64_RELATIVE). Pair with x86_64.AssembleProgramPIE.
func StaticPieExecutableX86(text, data []byte, relocs []Reloc) []byte {
	return staticPie(text, data, relocs, emX86_64)
}

func staticPie(text, data []byte, relocs []Reloc, machine uint16) []byte {
	const headers = ehSize + phNumPIE*phSize // 64 + 168 = 232
	textEnd := uint64(headers + len(text))   // end of the R+X segment
	dataOff := pageUpFor(textEnd, machine)   // page boundary: start of R+W data

	// Within the R+W segment: data blob, then (8-aligned) .rela.dyn, then
	// .dynamic. All offsets/vaddrs are relative to a load base of 0.
	relaOff := dataOff + uint64(len(data))
	if rem := relaOff % 8; rem != 0 {
		relaOff += 8 - rem
	}
	relaSz := uint64(len(relocs) * relaEntSize)
	dynOff := relaOff + relaSz
	dynSz := uint64(5 * dynEntSize) // RELA, RELASZ, RELAENT, RELACOUNT, NULL
	dataEnd := dynOff + dynSz
	dataSegSz := dataEnd - dataOff

	relType := uint64(relAArch64Relative)
	if machine == emX86_64 {
		relType = relX86_64Relative
	}

	buf := make([]byte, 0, dataEnd)

	// ---- ELF-64 header ----
	buf = append(buf, 0x7f, 'E', 'L', 'F')
	buf = append(buf, 2, 1, 1, 0)
	buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0)
	buf = le16(buf, etDyn)        // e_type = ET_DYN
	buf = le16(buf, machine)      // e_machine
	buf = le32(buf, 1)            // e_version
	buf = le64(buf, TextVAddrPIE) // e_entry (base-relative; loader adds bias)
	buf = le64(buf, ehSize)       // e_phoff
	buf = le64(buf, 0)            // e_shoff
	buf = le32(buf, 0)            // e_flags
	buf = le16(buf, ehSize)       // e_ehsize
	buf = le16(buf, phSize)       // e_phentsize
	buf = le16(buf, phNumPIE)     // e_phnum
	buf = le16(buf, 0)            // e_shentsize
	buf = le16(buf, 0)            // e_shnum
	buf = le16(buf, 0)            // e_shstrndx

	// ---- PH 0: R+X code (headers + .text) ----
	buf = le32(buf, 1)                     // PT_LOAD
	buf = le32(buf, 5)                     // PF_R | PF_X
	buf = le64(buf, 0)                     // p_offset
	buf = le64(buf, 0)                     // p_vaddr
	buf = le64(buf, 0)                     // p_paddr
	buf = le64(buf, textEnd)               // p_filesz
	buf = le64(buf, textEnd)               // p_memsz
	buf = le64(buf, pageAlignFor(machine)) // p_align

	// ---- PH 1: R+W data (.rodata + globals + .rela.dyn + .dynamic) ----
	buf = le32(buf, 1)                     // PT_LOAD
	buf = le32(buf, 6)                     // PF_R | PF_W
	buf = le64(buf, dataOff)               // p_offset
	buf = le64(buf, dataOff)               // p_vaddr
	buf = le64(buf, dataOff)               // p_paddr
	buf = le64(buf, dataSegSz)             // p_filesz
	buf = le64(buf, dataSegSz)             // p_memsz
	buf = le64(buf, pageAlignFor(machine)) // p_align

	// ---- PH 2: PT_DYNAMIC (view into the R+W segment) ----
	buf = le32(buf, ptDynamic) // PT_DYNAMIC
	buf = le32(buf, 6)         // PF_R | PF_W
	buf = le64(buf, dynOff)    // p_offset
	buf = le64(buf, dynOff)    // p_vaddr
	buf = le64(buf, dynOff)    // p_paddr
	buf = le64(buf, dynSz)     // p_filesz
	buf = le64(buf, dynSz)     // p_memsz
	buf = le64(buf, 8)         // p_align

	// ---- body: .text, page padding, data blob, .rela.dyn, .dynamic ----
	buf = append(buf, text...)
	for uint64(len(buf)) < dataOff {
		buf = append(buf, 0)
	}
	buf = append(buf, data...)
	for uint64(len(buf)) < relaOff {
		buf = append(buf, 0)
	}
	// .rela.dyn: one Elf64_Rela per reloc (r_offset, r_info, r_addend).
	for _, r := range relocs {
		buf = le64(buf, r.Offset) // r_offset
		buf = le64(buf, relType)  // r_info: sym=0, type=R_*_RELATIVE
		buf = le64(buf, r.Addend) // r_addend
	}
	// .dynamic.
	buf = le64(buf, dtRela)
	buf = le64(buf, relaOff)
	buf = le64(buf, dtRelaSz)
	buf = le64(buf, relaSz)
	buf = le64(buf, dtRelaEnt)
	buf = le64(buf, relaEntSize)
	buf = le64(buf, dtRelaCount)
	buf = le64(buf, uint64(len(relocs)))
	buf = le64(buf, dtNull)
	buf = le64(buf, 0)
	return buf
}

// SharedLibrary wraps .text + data + relocations into a loadable arm64
// shared object (.so): the ET_DYN PIE image plus a dynamic symbol table
// (.dynsym/.dynstr), a SysV .hash, and the .dynamic tags a loader needs to
// resolve `exports` (each Name -> load_base + Value). `soname` is recorded
// as DT_SONAME (may be ""). This is the foundation for JNI: an Android app
// loads it with System.loadLibrary and the JVM resolves the JNI entry
// points from .dynsym. No section headers (the loader uses PT_DYNAMIC).
func SharedLibrary(text, data []byte, relocs []Reloc, exports []Export, soname string) []byte {
	return sharedLib(text, data, relocs, exports, soname, emAArch64)
}

// SharedLibraryX86 is the x86-64 counterpart of SharedLibrary.
func SharedLibraryX86(text, data []byte, relocs []Reloc, exports []Export, soname string) []byte {
	return sharedLib(text, data, relocs, exports, soname, emX86_64)
}

func align8(v uint64) uint64 { return (v + 7) &^ 7 }
func align4(v uint64) uint64 { return (v + 3) &^ 3 }

func sharedLib(text, data []byte, relocs []Reloc, exports []Export, soname string, machine uint16) []byte {
	const headers = ehSize + phNumPIE*phSize // 232: ELF header + 3 program headers
	textEnd := uint64(headers + len(text))
	dataOff := pageUpFor(textEnd, machine) // R+W segment starts on a page boundary

	// Build .dynstr: index 0 is the empty string; then the soname (if any)
	// and each export name, NUL-terminated. Record their offsets.
	dynstr := []byte{0}
	var sonameOff uint64
	if soname != "" {
		sonameOff = uint64(len(dynstr))
		dynstr = append(dynstr, soname...)
		dynstr = append(dynstr, 0)
	}
	nameOff := make([]uint64, len(exports))
	for i, e := range exports {
		nameOff[i] = uint64(len(dynstr))
		dynstr = append(dynstr, e.Name...)
		dynstr = append(dynstr, 0)
	}

	// Layout within the R+W segment (vaddr == file offset, base 0):
	// data blob, .rela.dyn, .dynsym, .dynstr, .hash, .dynamic.
	relaOff := align8(dataOff + uint64(len(data)))
	relaSz := uint64(len(relocs) * relaEntSize)
	symOff := align8(relaOff + relaSz)
	nsym := uint64(1 + len(exports)) // index 0 is the reserved null symbol
	symSz := nsym * symEntSize
	strOff := symOff + symSz
	strSz := uint64(len(dynstr))
	hashOff := align4(strOff + strSz)
	// SysV hash: nbucket=1 (one chain), nchain=nsym.
	const nbucket = 1
	hashSz := uint64((2 + nbucket + int(nsym)) * 4)
	dynOff := align8(hashOff + hashSz)

	// .dynamic entries.
	type dyn struct{ tag, val uint64 }
	dyns := []dyn{
		{dtHash, hashOff},
		{dtStrtab, strOff},
		{dtSymtab, symOff},
		{dtStrsz, strSz},
		{dtSyment, symEntSize},
	}
	if soname != "" {
		dyns = append(dyns, dyn{dtSoname, sonameOff})
	}
	if len(relocs) > 0 {
		dyns = append(dyns,
			dyn{dtRela, relaOff}, dyn{dtRelaSz, relaSz},
			dyn{dtRelaEnt, relaEntSize}, dyn{dtRelaCount, uint64(len(relocs))})
	}
	dyns = append(dyns, dyn{dtNull, 0})
	dynSz := uint64(len(dyns) * dynEntSize)
	dataEnd := dynOff + dynSz
	dataSegSz := dataEnd - dataOff

	relType := uint64(relAArch64Relative)
	if machine == emX86_64 {
		relType = relX86_64Relative
	}

	buf := make([]byte, 0, dataEnd)

	// ---- ELF-64 header (ET_DYN; e_entry 0 — a library has no entry) ----
	buf = append(buf, 0x7f, 'E', 'L', 'F')
	buf = append(buf, 2, 1, 1, 0)
	buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0)
	buf = le16(buf, etDyn)
	buf = le16(buf, machine)
	buf = le32(buf, 1)
	buf = le64(buf, 0) // e_entry
	buf = le64(buf, ehSize)
	buf = le64(buf, 0)
	buf = le32(buf, 0)
	buf = le16(buf, ehSize)
	buf = le16(buf, phSize)
	buf = le16(buf, phNumPIE)
	buf = le16(buf, 0)
	buf = le16(buf, 0)
	buf = le16(buf, 0)

	// ---- PH 0: R+X code ----
	buf = le32(buf, 1)
	buf = le32(buf, 5)
	buf = le64(buf, 0)
	buf = le64(buf, 0)
	buf = le64(buf, 0)
	buf = le64(buf, textEnd)
	buf = le64(buf, textEnd)
	buf = le64(buf, pageAlignFor(machine))

	// ---- PH 1: R+W data (.rela.dyn / .dynsym / .dynstr / .hash / .dynamic) ----
	buf = le32(buf, 1)
	buf = le32(buf, 6)
	buf = le64(buf, dataOff)
	buf = le64(buf, dataOff)
	buf = le64(buf, dataOff)
	buf = le64(buf, dataSegSz)
	buf = le64(buf, dataSegSz)
	buf = le64(buf, pageAlignFor(machine))

	// ---- PH 2: PT_DYNAMIC ----
	buf = le32(buf, ptDynamic)
	buf = le32(buf, 6)
	buf = le64(buf, dynOff)
	buf = le64(buf, dynOff)
	buf = le64(buf, dynOff)
	buf = le64(buf, dynSz)
	buf = le64(buf, dynSz)
	buf = le64(buf, 8)

	// ---- body ----
	buf = append(buf, text...)
	for uint64(len(buf)) < dataOff {
		buf = append(buf, 0)
	}
	buf = append(buf, data...)
	for uint64(len(buf)) < relaOff {
		buf = append(buf, 0)
	}
	for _, r := range relocs {
		buf = le64(buf, r.Offset)
		buf = le64(buf, relType)
		buf = le64(buf, r.Addend)
	}
	// .dynsym: null symbol, then one global STT_FUNC per export.
	for uint64(len(buf)) < symOff {
		buf = append(buf, 0)
	}
	buf = append(buf, make([]byte, symEntSize)...) // index 0: null symbol
	for i, e := range exports {
		buf = le32(buf, uint32(nameOff[i])) // st_name
		buf = append(buf, stInfoGlobFunc)   // st_info
		buf = append(buf, 0)                // st_other
		buf = le16(buf, shnText)            // st_shndx (defined -> bias added)
		buf = le64(buf, e.Value)            // st_value
		buf = le64(buf, e.Size)             // st_size
	}
	// .dynstr.
	buf = append(buf, dynstr...)
	// .hash (SysV): nbucket, nchain, bucket[1], chain[nsym].
	for uint64(len(buf)) < hashOff {
		buf = append(buf, 0)
	}
	buf = le32(buf, nbucket)
	buf = le32(buf, uint32(nsym))
	if nsym > 1 {
		buf = le32(buf, 1) // bucket[0] = first real symbol index
	} else {
		buf = le32(buf, 0) // no exports: empty bucket
	}
	// chain: index 0 unused; each real symbol points at the next, last -> 0.
	for i := uint64(0); i < nsym; i++ {
		next := i + 1
		if next >= nsym {
			next = 0
		}
		if i == 0 {
			next = 0
		}
		buf = le32(buf, uint32(next))
	}
	// .dynamic.
	for uint64(len(buf)) < dynOff {
		buf = append(buf, 0)
	}
	for _, d := range dyns {
		buf = le64(buf, d.tag)
		buf = le64(buf, d.val)
	}
	return buf
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
	buf = le32(buf, 1)                     // p_type  = PT_LOAD
	buf = le32(buf, flags)                 // p_flags
	buf = le64(buf, 0)                     // p_offset
	buf = le64(buf, baseVAddr)             // p_vaddr
	buf = le64(buf, baseVAddr)             // p_paddr
	buf = le64(buf, total)                 // p_filesz
	buf = le64(buf, total)                 // p_memsz
	buf = le64(buf, pageAlignFor(machine)) // p_align

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
