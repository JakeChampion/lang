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
	linkeditFileOff int    // == codeLimit; signature starts here
	textVAddr       uint64 // address of the first code byte (== entry)
	dataVAddr       uint64 // __DATA segment base
}

func layoutFor(textLen, dataLen int) layout {
	hasData := dataLen > 0
	textOff := machHeaderLen + loadCommandsLen(hasData)
	textVMSize := alignUp(textOff+textLen, pageSize)
	dataFileLen := 0
	if hasData {
		dataFileLen = alignUp(dataLen, pageSize)
	}
	return layout{
		textOff:         textOff,
		textLen:         textLen,
		dataLen:         dataLen,
		textVMSize:      textVMSize,
		dataFileLen:     dataFileLen,
		linkeditFileOff: textVMSize + dataFileLen,
		textVAddr:       baseVAddr + uint64(textOff),
		dataVAddr:       baseVAddr + uint64(textVMSize),
	}
}

// SegmentAddrs returns the code (== entry) address and the __DATA segment
// base for the given code/data sizes. The assembler resolves adrp @PAGE /
// @PAGEOFF references against these before StaticExecutable lays the same
// blobs out at the same addresses.
func SegmentAddrs(textLen, dataLen int) (textVAddr, dataVAddr uint64) {
	lo := layoutFor(textLen, dataLen)
	return lo.textVAddr, lo.dataVAddr
}

// StaticExecutable wraps machine code and a data blob into a runnable,
// ad-hoc-signed static arm64 Mach-O executable. Code occupies the r-x
// __TEXT segment; data (read-only constants + writable globals, merged by
// the assembler) occupies a r/w __DATA segment. Execution begins at the
// first code byte (the code generator's `_main`). The text/data sizes
// must match those passed to SegmentAddrs so addresses line up.
func StaticExecutable(text, data []byte, identifier string) []byte {
	lo := layoutFor(len(text), len(data))
	codeLimit := lo.linkeditFileOff

	sig := codeSignature(nil, identifier, codeLimit, lo.textVMSize) // size probe
	sigLen := len(sig)
	linkeditVMSize := alignUp(sigLen, pageSize)
	linkeditVAddr := lo.dataVAddr + uint64(lo.dataFileLen)

	buf := make([]byte, lo.linkeditFileOff)
	mh := newImage(buf)
	mh.machHeader()
	mh.segmentText(uint64(lo.textOff), len(text))
	if len(data) > 0 {
		mh.segmentData(lo.dataVAddr, uint64(lo.dataFileLen), uint64(lo.textVMSize), len(data))
	}
	mh.segmentLinkedit(linkeditVAddr, uint64(linkeditVMSize), uint64(lo.linkeditFileOff), sigLen)
	mh.unixThread(lo.textVAddr)
	mh.codeSig(uint32(lo.linkeditFileOff), uint32(sigLen))
	mh.done()

	copy(buf[lo.textOff:], text)
	if len(data) > 0 {
		copy(buf[lo.textVMSize:], data)
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
)

// loadCommandsLen returns the total size of all load commands:
// __PAGEZERO + __TEXT (1 section: __text) + optional __DATA (1 section) +
// __LINKEDIT + LC_UNIXTHREAD + LC_CODE_SIGNATURE.
func loadCommandsLen(hasData bool) int {
	n := segCmdLen + (segCmdLen + sectLen) + segCmdLen
	if hasData {
		n += segCmdLen + sectLen
	}
	n += unixThreadLen + codeSigCmdLen
	return n
}
