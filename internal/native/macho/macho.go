// Package macho writes minimal arm64 Mach-O executables — the container half
// of the native arm64-darwin path, the counterpart of internal/native/elf for
// Linux. It replaces the clang/ld64 link step in cmd/fern for the
// self-contained programs the code generator emits (raw `svc #0x80` syscalls,
// no libc).
//
// The layout is __PAGEZERO, a r-x __TEXT segment holding the Mach-O header +
// load commands + machine code, an optional r/w __DATA segment (string
// constants + writable globals, merged), and a __LINKEDIT segment carrying the
// rebase stream, the symbol table and an ad-hoc code signature.
//
// # What Apple Silicon requires, and how each requirement announces itself
//
// This writer originally emitted a static, dyld-free, non-PIE LC_UNIXTHREAD
// image. Every such binary is SIGKILLed at exec on Apple Silicon — including
// one built by Apple's own ld64, which is how we know it is a platform rule
// and not a defect here. There is no crash report and nothing in the system
// log; the process exits 137, indistinguishable from an OOM kill. The
// requirements, in the order they bite:
//
//   - MH_PIE. Without it the kernel refuses the image before dyld runs at all.
//     ld64 cannot even produce a non-PIE arm64 executable — it warns "-no_pie
//     ignored for arm64*" — which is the tell that this is mandatory.
//   - LC_LOAD_DYLINKER + LC_LOAD_DYLIB + LC_MAIN. Every arm64 main executable
//     is dyld-loaded. Raw syscalls are fine (a dyld-loaded image making
//     `svc #0x80` calls runs correctly); it is the dyld-free CONTAINER that is
//     rejected.
//   - LC_SYMTAB whenever LC_DYSYMTAB is present, else dyld aborts with
//     "LC_DYSYMTAB but no LC_SYMTAB load command".
//   - LC_BUILD_VERSION, naming the platform.
//   - An ad-hoc LC_CODE_SIGNATURE. "Ad-hoc" means no CMS/certificate — the
//     code directory's page hashes are the identity.
//
// PIE has a consequence the code generator has to answer for: dyld slides the
// image, so any ABSOLUTE address baked into __DATA is stale on arrival.
// Fern's code is otherwise entirely PC-relative (adrp/@PAGEOFF, b/bl), so the
// complete set is the assembler's `.quad <symbol>` slots — jump tables and the
// like. Those are declared to dyld as LC_DYLD_INFO_ONLY rebase opcodes. Miss
// them and the image loads fine and then segfaults deep inside a program that
// happens to use a jump table, which reads as a codegen bug rather than a
// container one.
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
	mhDyldLink = 0x4
	mhTwoLevel = 0x80
	mhPIE      = 0x200000

	lcSegment64 = 0x19
	// lcUnixThread is retained only so TestMachODyldCommandSet can assert this
	// image does NOT carry it: a static thread-state entry is what made the
	// output unlaunchable on Apple Silicon. Nothing emits it.
	lcUnixThread    = 0x5
	lcCodeSignature = 0x1D
	lcSymtab        = 0x2
	lcBuildVersion  = 0x32
	lcLoadDylinker  = 0xE
	lcLoadDylib     = 0xC
	lcMain          = 0x80000028 // LC_MAIN | LC_REQ_DYLD
	lcDysymtab      = 0xB
	lcDyldInfoOnly  = 0x80000022 // LC_DYLD_INFO_ONLY | LC_REQ_DYLD

	// Rebase opcodes (mach-o/loader.h). Only the three needed to say "rebase
	// this one pointer, at this offset in this segment".
	rebaseOpDone           = 0x00
	rebaseOpSetTypeImm     = 0x10
	rebaseOpSetSegOffULEB  = 0x20
	rebaseOpDoRebaseImmTms = 0x50
	rebaseTypePointer      = 1

	dyldPath      = "/usr/lib/dyld"
	libSystemPath = "/usr/lib/libSystem.B.dylib"

	// LC_BUILD_VERSION payload. platformMacOS identifies the image to the
	// kernel; minos / sdk are nibble-encoded XXXX.YY.ZZ. 11.0.0 is the first
	// macOS with Apple Silicon, which is the floor for an arm64 executable.
	platformMacOS = 0x1
	minOSVersion  = 0x000B0000 // 11.0.0
	sdkVersion    = 0x000B0000 // 11.0.0

	vmProtRead    = 0x1
	vmProtWrite   = 0x2
	vmProtExecute = 0x4
)

// layout fixes the file/virtual-address layout of a Mach-O image given
// the code, unwind and data sizes. The assembler and the container must
// agree on it, so both go through layoutFor.
type layout struct {
	textOff         int // file offset of code within __TEXT (after header+loadcmds)
	textLen         int
	ehOff           int // file offset of __eh_frame within __TEXT (0 when none)
	ehLen           int
	dataLen         int
	textVMSize      int // page-aligned __TEXT segment size
	dataFileLen     int // page-aligned __DATA segment size (0 if no data)
	linkeditFileOff int // start of __LINKEDIT (fixups, symtab, strtab, then sig)
	rebaseOff       int // file offset of the LC_DYLD_INFO_ONLY rebase stream
	rebaseLen       int
	textVAddr       uint64 // address of the first code byte (== entry)
	ehVAddr         uint64 // address of __eh_frame (0 when none)
	dataVAddr       uint64 // __DATA segment base
	// Symtab placement inside __LINKEDIT (zero-valued when no syms).
	symOff int // file offset of the nlist_64 array
	strOff int // file offset of the string table
	sigOff int // file offset where the code signature begins (== codeLimit)
}

// layoutFor computes the layout for the given code/unwind/data sizes and,
// when hasSyms, an LC_SYMTAB whose nlist array (symtabLen bytes) + string
// table (strtabLen bytes) sit at the front of __LINKEDIT, before the code
// signature. The extra load command shifts textOff (hence every address), and
// so does the __eh_frame section header, so the assembler must lay out against
// a layout with the SAME hasSyms and the same has-unwind answer — see
// SegmentMap.
func layoutFor(textLen, ehLen, dataLen, symtabLen, strtabLen, rebaseLen int, hasSyms bool) layout {
	hasData := dataLen > 0
	textOff := machHeaderLen + loadCommandsLen(hasData, ehLen > 0, hasSyms)
	textEnd := textOff + textLen
	ehOff := 0
	if ehLen > 0 {
		// __eh_frame follows the code inside __TEXT, 8-aligned: its FDEs
		// carry 8-byte pcrel pointers, and being in the same segment keeps
		// the R+X mapping libunwind reads it through.
		ehOff = alignUp(textEnd, 8)
		textEnd = ehOff + ehLen
	}
	textVMSize := alignUp(textEnd, pageSize)
	dataFileLen := 0
	if hasData {
		dataFileLen = alignUp(dataLen, pageSize)
	}
	linkeditFileOff := textVMSize + dataFileLen
	rebaseOff := linkeditFileOff
	symOff := alignUp(rebaseOff+rebaseLen, 8)
	strOff := symOff + symtabLen
	sigOff := alignUp(strOff+strtabLen, 16)
	lo := layout{
		rebaseOff:       rebaseOff,
		rebaseLen:       rebaseLen,
		textOff:         textOff,
		textLen:         textLen,
		ehOff:           ehOff,
		ehLen:           ehLen,
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
	if ehLen > 0 {
		lo.ehVAddr = baseVAddr + uint64(ehOff)
	}
	return lo
}

// ImageMap is where the container will put the three things the assembler
// resolves addresses against: the code, the __eh_frame it describes, and the
// __DATA segment. EhFrame is 0 when the image carries no unwind data.
type ImageMap struct {
	Text    uint64
	EhFrame uint64
	Data    uint64
}

// SegmentMap is the single authority on the Mach-O address map, for an image
// whose code is textLen bytes, which carries ehLen bytes of __eh_frame (0 for
// none), whose data blob is dataLen bytes, and which does or does not carry
// the `-g` LC_SYMTAB. Both the __eh_frame section header and the LC_SYMTAB
// command shift textOff, hence every address, so the assembler must resolve
// adrp @PAGE / @PAGEOFF against the map of the SAME answers to both — the
// symtab/strtab sizes themselves live at the end, in __LINKEDIT, and move
// nothing.
//
// The __eh_frame image is itself rendered against Text and EhFrame (pcrel
// FDE pointers), and neither depends on ehLen — only Data does — so a caller
// asks once with a placeholder length to render it and again with the real
// one to place the data.
func SegmentMap(textLen, ehLen, dataLen int, syms bool) ImageMap {
	lo := layoutFor(textLen, ehLen, dataLen, 0, 0, 0, syms)
	return ImageMap{Text: lo.textVAddr, EhFrame: lo.ehVAddr, Data: lo.dataVAddr}
}

// StaticExecutable wraps machine code, an optional __eh_frame image and a
// data blob into a runnable, ad-hoc-signed static arm64 Mach-O executable.
// Code and unwind data occupy the r-x __TEXT segment; data (read-only
// constants + writable globals, merged by the assembler) occupies a r/w
// __DATA segment. Execution begins at the first code byte (the code
// generator's `_main`). The sizes must match those passed to SegmentMap so
// addresses line up.
func StaticExecutable(text, eh, data []byte, identifier string, rebases []int) []byte {
	return staticExecutable(text, eh, data, identifier, nil, rebases)
}

// StaticExecutableSyms is StaticExecutable plus a static symbol table
// (LC_SYMTAB): the nlist_64 array + string table naming each function sit at
// the front of __LINKEDIT, ahead of the code signature (which hashes them, so
// they stay covered). Emitted under `fern -g` so lldb / nm / a backtrace can
// symbolicate arm64-darwin binaries. The text/data must have been laid out
// against SegmentMap with syms set (the LC_SYMTAB command shifts every
// address).
func StaticExecutableSyms(text, eh, data []byte, identifier string, syms []Sym, rebases []int) []byte {
	return staticExecutable(text, eh, data, identifier, syms, rebases)
}

func staticExecutable(text, eh, data []byte, identifier string, syms []Sym, rebases []int) []byte {
	hasSyms := len(syms) > 0
	var nlists, strtab []byte
	if hasSyms {
		nlists, strtab = buildSymtab(syms)
	}
	// __DATA is segment index 2 (__PAGEZERO 0, __TEXT 1); with no data blob
	// there is nothing to rebase.
	var rebase []byte
	if len(data) > 0 {
		rebase = rebaseOpcodes(2, rebases)
	}
	lo := layoutFor(len(text), len(eh), len(data), len(nlists), len(strtab), len(rebase), hasSyms)
	codeLimit := lo.sigOff

	sig := codeSignature(nil, identifier, codeLimit, lo.textVMSize) // size probe
	sigLen := len(sig)
	linkeditFileLen := (lo.sigOff - lo.linkeditFileOff) + sigLen
	linkeditVMSize := alignUp(linkeditFileLen, pageSize)
	linkeditVAddr := lo.dataVAddr + uint64(lo.dataFileLen)

	buf := make([]byte, lo.sigOff)
	mh := newImage(buf)
	mh.machHeader()
	mh.segmentText(uint64(lo.textOff), len(text), uint64(lo.ehOff), len(eh))
	if len(data) > 0 {
		mh.segmentData(lo.dataVAddr, uint64(lo.dataFileLen), uint64(lo.textVMSize), len(data))
	}
	mh.segmentLinkedit(linkeditVAddr, uint64(linkeditVMSize), uint64(lo.linkeditFileOff), linkeditFileLen)
	mh.dyldInfo(uint32(lo.rebaseOff), uint32(lo.rebaseLen))
	mh.symtab(uint32(lo.symOff), uint32(len(syms)), uint32(lo.strOff), uint32(len(strtab)))
	mh.dysymtab(uint32(len(syms)))
	mh.buildVersion()
	mh.lcStr(lcLoadDylinker, dyldPath)
	mh.lcStr(lcLoadDylib, libSystemPath)
	mh.main(uint64(lo.textOff))
	mh.codeSig(uint32(lo.sigOff), uint32(sigLen))
	mh.done()

	copy(buf[lo.rebaseOff:], rebase)
	copy(buf[lo.textOff:], text)
	if len(eh) > 0 {
		copy(buf[lo.ehOff:], eh)
	}
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
	machHeaderLen   = 32
	segCmdLen       = 72 // LC_SEGMENT_64 with no sections
	sectLen         = 80
	codeSigCmdLen   = 16
	symtabCmdLen    = 24 // LC_SYMTAB: cmd/cmdsize + symoff/nsyms/stroff/strsize
	nlistLen        = 16 // nlist_64
	buildVersionLen = 24 // LC_BUILD_VERSION with ntools == 0
	mainCmdLen      = 24 // LC_MAIN: cmd/cmdsize + entryoff + stacksize
	dysymtabCmdLen  = 80 // LC_DYSYMTAB: cmd/cmdsize + 18 uint32 index/count pairs
	dyldInfoCmdLen  = 48 // LC_DYLD_INFO_ONLY: cmd/cmdsize + 5 off/size pairs
)

// lcStrFixedLen is the size of an lc_str command's fixed part, before the path.
func lcStrFixedLen(cmd uint32) int {
	if cmd == lcLoadDylib {
		return 24
	}
	return 12
}

// lcStrCmdLen is the padded size of an lc_str command carrying `path`: the
// fixed part, the NUL-terminated path, rounded up to 8 bytes.
func lcStrCmdLen(cmd uint32, path string) int {
	return alignUp(lcStrFixedLen(cmd)+len(path)+1, 8)
}

// loadCommandsLen returns the total size of all load commands:
// __PAGEZERO + __TEXT (__text, plus __eh_frame when the image carries unwind
// data) + optional __DATA (1 section) + __LINKEDIT + LC_SYMTAB +
// LC_BUILD_VERSION + LC_LOAD_DYLINKER + LC_LOAD_DYLIB + LC_MAIN +
// LC_CODE_SIGNATURE.
func loadCommandsLen(hasData, hasEh, hasSyms bool) int {
	n := segCmdLen + (segCmdLen + sectLen) + segCmdLen
	if hasEh {
		n += sectLen
	}
	if hasData {
		n += segCmdLen + sectLen
	}
	// LC_SYMTAB is unconditional: dyld rejects an image carrying LC_DYSYMTAB
	// without it, and LC_DYSYMTAB is itself required on a two-level image. With
	// no `-g` symbols the command is present with zero counts.
	_ = hasSyms
	n += symtabCmdLen + buildVersionLen + dysymtabCmdLen + dyldInfoCmdLen + dyldInfoCmdLen
	n += lcStrCmdLen(lcLoadDylinker, dyldPath)
	n += lcStrCmdLen(lcLoadDylib, libSystemPath)
	n += mainCmdLen + codeSigCmdLen
	return n
}

// uleb appends `v` in unsigned LEB128, the encoding the rebase opcode stream
// uses for offsets and counts.
func uleb(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			c |= 0x80
		}
		b = append(b, c)
		if v == 0 {
			return b
		}
	}
}

// rebaseOpcodes builds the LC_DYLD_INFO_ONLY rebase stream telling dyld to add
// the slide to each 8-byte slot at `offs` within segment `segIdx`. Absolute
// addresses are the only thing in a Fern image that a slide invalidates: the
// code is entirely PC-relative, so the `.quad <symbol>` slots the assembler
// reports are the complete set. Without this the image loads and then jumps
// through a stale pointer — which presents as a segfault deep in a program
// that uses a jump table, not as a load failure.
func rebaseOpcodes(segIdx int, offs []int) []byte {
	if len(offs) == 0 {
		return nil
	}
	b := []byte{rebaseOpSetTypeImm | rebaseTypePointer}
	for _, off := range offs {
		b = append(b, byte(rebaseOpSetSegOffULEB|segIdx))
		b = uleb(b, uint64(off))
		b = append(b, rebaseOpDoRebaseImmTms|1)
	}
	b = append(b, rebaseOpDone)
	for len(b)%8 != 0 {
		b = append(b, 0)
	}
	return b
}
