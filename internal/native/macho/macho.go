// Package macho writes minimal static arm64 Mach-O executables — the
// container half of the native arm64-darwin path, the counterpart of
// internal/native/elf for Linux. It aims to replace the clang/ld64 link
// step in cmd/fern for the simple, self-contained programs the code
// generator emits (-static, no dyld, raw `svc #0x80` syscalls).
//
// The file is a fixed-address, non-PIE executable: __PAGEZERO, a r-x
// __TEXT segment holding the Mach-O header + load commands + machine code,
// an optional r/w __DATA segment (string constants + writable globals,
// merged), and a __LINKEDIT segment carrying an ad-hoc code signature.
// Apple Silicon's
// kernel refuses to execute an unsigned arm64 binary, so the signature is
// mandatory; "ad-hoc" means there is no CMS/certificate — the code
// directory's page hashes are the identity.
//
// Execution starts via LC_UNIXTHREAD (the kernel sets PC directly), not
// LC_MAIN (which needs dyld). The code generator's `_main` entry calls
// `main` and then exits with a raw syscall, so it never returns — exactly
// what a thread-state entry needs.
//
// References: Apple's loader.h / cs_blobs.h. Code-signing blobs are
// big-endian; Mach-O headers are little-endian (arm64 host).
package macho

const (
	pageSize  = 0x4000      // 16 KiB Mach-O segment alignment on arm64
	baseVAddr = 0x100000000 // __TEXT load address (just past __PAGEZERO)

	mhMagic64  = 0xFEEDFACF
	cpuArm64   = 0x0100000C // CPU_TYPE_ARM64
	cpuSubAll  = 0x00000000
	mhExecute  = 0x2
	mhNoUndefs = 0x1

	lcSegment64     = 0x19
	lcUnixThread    = 0x5
	lcCodeSignature = 0x1D
	lcSymtab        = 0x2

	vmProtRead    = 0x1
	vmProtWrite   = 0x2
	vmProtExecute = 0x4

	armThreadState64    = 6
	armThreadState64Cnt = 68 // count in uint32 units (272 bytes)
)

// layout fixes the file/virtual-address layout of a Mach-O image given
// the code and data sizes. The assembler and the container must agree on
// it, so both go through layoutFor.
type layout struct {
	textOff         int // file offset of code within __TEXT (after header+loadcmds)
	textLen         int
	dataLen         int
	textVMSize      int    // page-aligned __TEXT segment size
	dataFileLen     int    // page-aligned __DATA segment size (0 if no data)
	linkeditFileOff int    // start of __LINKEDIT (symtab, then strtab, then sig)
	textVAddr       uint64 // address of the first code byte (== entry)
	dataVAddr       uint64 // __DATA segment base
	// Symtab placement inside __LINKEDIT (zero-valued when no syms).
	symOff int // file offset of the nlist_64 array
	strOff int // file offset of the string table
	sigOff int // file offset where the code signature begins (== codeLimit)
}

// layoutFor computes the layout for the given code/data sizes and, when
// hasSyms, an LC_SYMTAB whose nlist array (symtabLen bytes) + string table
// (strtabLen bytes) sit at the front of __LINKEDIT, before the code signature.
// The extra load command shifts textOff (hence every address), so the assembler
// must lay out against a layout with the SAME hasSyms — see SegmentAddrsSyms.
func layoutFor(textLen, dataLen, symtabLen, strtabLen int, hasSyms bool) layout {
	hasData := dataLen > 0
	textOff := machHeaderLen + loadCommandsLen(hasData, hasSyms)
	textVMSize := alignUp(textOff+textLen, pageSize)
	dataFileLen := 0
	if hasData {
		dataFileLen = alignUp(dataLen, pageSize)
	}
	linkeditFileOff := textVMSize + dataFileLen
	symOff, strOff := 0, 0
	sigOff := linkeditFileOff
	if hasSyms {
		symOff = linkeditFileOff
		strOff = symOff + symtabLen
		sigOff = alignUp(strOff+strtabLen, 16)
	}
	return layout{
		textOff:         textOff,
		textLen:         textLen,
		dataLen:         dataLen,
		textVMSize:      textVMSize,
		dataFileLen:     dataFileLen,
		linkeditFileOff: linkeditFileOff,
		textVAddr:       baseVAddr + uint64(textOff),
		dataVAddr:       baseVAddr + uint64(textVMSize),
		symOff:          symOff,
		strOff:          strOff,
		sigOff:          sigOff,
	}
}

// SegmentAddrs returns the code (== entry) address and the __DATA segment
// base for the given code/data sizes. The assembler resolves adrp @PAGE /
// @PAGEOFF references against these before StaticExecutable lays the same
// blobs out at the same addresses.
func SegmentAddrs(textLen, dataLen int) (textVAddr, dataVAddr uint64) {
	lo := layoutFor(textLen, dataLen, 0, 0, false)
	return lo.textVAddr, lo.dataVAddr
}

// SegmentAddrsSyms is SegmentAddrs for the `-g` path: the LC_SYMTAB load
// command shifts textOff by its 24 bytes, so a symbol-table build must resolve
// adrp against these (shifted) addresses. The symtab/strtab sizes don't affect
// the code/data addresses (they live at the end, in __LINKEDIT), so zero
// suffices here.
func SegmentAddrsSyms(textLen, dataLen int) (textVAddr, dataVAddr uint64) {
	lo := layoutFor(textLen, dataLen, 0, 0, true)
	return lo.textVAddr, lo.dataVAddr
}

// StaticExecutable wraps machine code and a data blob into a runnable,
// ad-hoc-signed static arm64 Mach-O executable. Code occupies the r-x
// __TEXT segment; data (read-only constants + writable globals, merged by
// the assembler) occupies a r/w __DATA segment. Execution begins at the
// first code byte (the code generator's `_main`). The text/data sizes
// must match those passed to SegmentAddrs so addresses line up.
func StaticExecutable(text, data []byte, identifier string) []byte {
	return staticExecutable(text, data, identifier, nil)
}

// StaticExecutableSyms is StaticExecutable plus a static symbol table
// (LC_SYMTAB): the nlist_64 array + string table naming each function sit at
// the front of __LINKEDIT, ahead of the code signature (which hashes them, so
// they stay covered). Emitted under `fern -g` so lldb / nm / a backtrace can
// symbolicate arm64-darwin binaries. The text/data must have been laid out
// against SegmentAddrsSyms (the LC_SYMTAB command shifts every address).
func StaticExecutableSyms(text, data []byte, identifier string, syms []Sym) []byte {
	return staticExecutable(text, data, identifier, syms)
}

func staticExecutable(text, data []byte, identifier string, syms []Sym) []byte {
	hasSyms := len(syms) > 0
	var nlists, strtab []byte
	if hasSyms {
		nlists, strtab = buildSymtab(syms)
	}
	lo := layoutFor(len(text), len(data), len(nlists), len(strtab), hasSyms)
	codeLimit := lo.sigOff

	sig := codeSignature(nil, identifier, codeLimit, lo.textVMSize) // size probe
	sigLen := len(sig)
	linkeditFileLen := (lo.sigOff - lo.linkeditFileOff) + sigLen
	linkeditVMSize := alignUp(linkeditFileLen, pageSize)
	linkeditVAddr := lo.dataVAddr + uint64(lo.dataFileLen)

	buf := make([]byte, lo.sigOff)
	mh := newImage(buf)
	mh.machHeader()
	mh.segmentText(uint64(lo.textOff), len(text))
	if len(data) > 0 {
		mh.segmentData(lo.dataVAddr, uint64(lo.dataFileLen), uint64(lo.textVMSize), len(data))
	}
	mh.segmentLinkedit(linkeditVAddr, uint64(linkeditVMSize), uint64(lo.linkeditFileOff), linkeditFileLen)
	if hasSyms {
		mh.symtab(uint32(lo.symOff), uint32(len(syms)), uint32(lo.strOff), uint32(len(strtab)))
	}
	mh.unixThread(lo.textVAddr)
	mh.codeSig(uint32(lo.sigOff), uint32(sigLen))
	mh.done()

	copy(buf[lo.textOff:], text)
	if len(data) > 0 {
		copy(buf[lo.textVMSize:], data)
	}
	if hasSyms {
		copy(buf[lo.symOff:], nlists)
		copy(buf[lo.strOff:], strtab)
	}

	sig = codeSignature(buf[:codeLimit], identifier, codeLimit, lo.textVMSize)
	return append(buf, sig...)
}

func alignUp(n, a int) int { return (n + a - 1) &^ (a - 1) }

const (
	machHeaderLen = 32
	segCmdLen     = 72 // LC_SEGMENT_64 with no sections
	sectLen       = 80
	unixThreadLen = 16 + armThreadState64Cnt*4
	codeSigCmdLen = 16
	symtabCmdLen  = 24 // LC_SYMTAB: cmd/cmdsize + symoff/nsyms/stroff/strsize
	nlistLen      = 16 // nlist_64
)

// loadCommandsLen returns the total size of all load commands:
// __PAGEZERO + __TEXT (1 section: __text) + optional __DATA (1 section) +
// __LINKEDIT + optional LC_SYMTAB + LC_UNIXTHREAD + LC_CODE_SIGNATURE.
func loadCommandsLen(hasData, hasSyms bool) int {
	n := segCmdLen + (segCmdLen + sectLen) + segCmdLen
	if hasData {
		n += segCmdLen + sectLen
	}
	if hasSyms {
		n += symtabCmdLen
	}
	n += unixThreadLen + codeSigCmdLen
	return n
}
