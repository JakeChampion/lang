// Package macho writes minimal static arm64 Mach-O executables — the
// container half of the native arm64-darwin path, the counterpart of
// internal/native/elf for Linux. It aims to replace the clang/ld64 link
// step in cmd/fern for the simple, self-contained programs the code
// generator emits (-static, no dyld, raw `svc #0x80` syscalls).
//
// The file is a fixed-address, non-PIE executable: __PAGEZERO, a r-x
// __TEXT segment holding the Mach-O header + load commands + machine code
// (+ read-only constants), an optional r/w __DATA segment, and a
// __LINKEDIT segment carrying an ad-hoc code signature. Apple Silicon's
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

	vmProtRead    = 0x1
	vmProtWrite   = 0x2
	vmProtExecute = 0x4

	armThreadState64    = 6
	armThreadState64Cnt = 68 // count in uint32 units (272 bytes)
)

// TextVAddr is the __TEXT segment base address, passed to the assembler so
// it can resolve absolute (adrp @PAGE) references. Integer/control-flow
// programs don't use absolute data addressing, so they're independent of
// it; @PAGE/@PAGEOFF support (which needs the precise __text/__data
// addresses) is a later phase.
const TextVAddr = baseVAddr

// StaticExecutable wraps machine code (and optional read-only const data
// and writable data) into a runnable, ad-hoc-signed static arm64 Mach-O
// executable. text/rodata occupy the r-x __TEXT segment; data (if any)
// occupies a r/w __DATA segment. Execution begins at the first byte of
// text (the code generator's `_main`). identifier is the code-signing
// identifier (any short stable string, e.g. the program name).
func StaticExecutable(text, rodata, data []byte, identifier string) []byte {
	// __TEXT section layout: [header+loadcmds][__text][__const].
	hdrSize := machHeaderLen + loadCommandsLen(len(data) > 0)
	textOff := hdrSize
	constOff := alignUp(textOff+len(text), 8)
	textVMSize := alignUp(constOff+len(rodata), pageSize)

	dataFileLen := 0
	if len(data) > 0 {
		dataFileLen = alignUp(len(data), pageSize)
	}

	linkeditFileOff := textVMSize + dataFileLen
	codeLimit := linkeditFileOff

	sig := codeSignature(nil, identifier, codeLimit, textVMSize) // size probe (hashes filled later)
	sigLen := len(sig)
	linkeditVMSize := alignUp(sigLen, pageSize)

	textVAddr := uint64(baseVAddr)
	dataVAddr := textVAddr + uint64(textVMSize)
	linkeditVAddr := dataVAddr + uint64(dataFileLen)
	entry := textVAddr + uint64(textOff)

	// ---- assemble the file image up to the signature ----
	buf := make([]byte, linkeditFileOff)
	// header + load commands
	mh := newImage(buf)
	mh.machHeader(len(data) > 0)
	mh.segmentText(textVAddr, uint64(textVMSize), uint64(textOff), len(text), uint64(constOff), len(rodata))
	if len(data) > 0 {
		mh.segmentData(dataVAddr, uint64(dataFileLen), uint64(textVMSize), len(data))
	}
	mh.segmentLinkedit(linkeditVAddr, uint64(linkeditVMSize), uint64(linkeditFileOff), sigLen)
	mh.unixThread(entry)
	mh.codeSig(uint32(linkeditFileOff), uint32(sigLen))
	mh.done()

	copy(buf[textOff:], text)
	copy(buf[constOff:], rodata)
	if len(data) > 0 {
		copy(buf[textVMSize:], data)
	}

	// ---- code signature over file[0:codeLimit] ----
	sig = codeSignature(buf[:codeLimit], identifier, codeLimit, textVMSize)
	return append(buf, sig...)
}

func alignUp(n, a int) int { return (n + a - 1) &^ (a - 1) }

const (
	machHeaderLen = 32
	segCmdLen     = 72 // LC_SEGMENT_64 with no sections
	sectLen       = 80
	unixThreadLen = 16 + armThreadState64Cnt*4
	codeSigCmdLen = 16
)

// loadCommandsLen returns the total size of all load commands.
func loadCommandsLen(hasData bool) int {
	// __PAGEZERO (no sects) + __TEXT (2 sects) + __LINKEDIT (no sects)
	n := segCmdLen + (segCmdLen + 2*sectLen) + segCmdLen
	if hasData {
		n += segCmdLen + sectLen // __DATA (1 sect)
	}
	n += unixThreadLen + codeSigCmdLen
	return n
}
