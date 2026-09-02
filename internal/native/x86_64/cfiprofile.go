package x86_64

import (
	"strings"

	"github.com/jakechampion/lang/internal/native/cfi"
)

// x86CFI is the CIE shape GNU as emits for x86-64, pinned from `as --64` on a
// minimal prologue read back with `readelf --debug-dump=frames`: code
// alignment 1, data alignment -8, return address in column 16 (rip), and
// initial rules putting the CFA at rsp+8 with the return address at CFA-8.
//
// The register numbering is DWARF's (System V psABI figure 3.36), which is NOT
// the ModRM encoding order: rdx and rcx are swapped, and so are rbx and rsp.
// Getting it wrong names the wrong saved register while staying well-formed.
var x86CFI = &cfi.Profile{
	CodeAlign: 1,
	DataAlign: -8,
	RAColumn:  16,
	PtrEnc:    0x1b, // DW_EH_PE_pcrel | DW_EH_PE_sdata4
	PtrSize:   4,
	FDEAlign:  4,
	InitialRules: []byte{
		0x0c, 0x07, 0x08, // DW_CFA_def_cfa rsp, 8
		0x90, 0x01, // DW_CFA_offset r16 (rip) at CFA-8
	},
	Regs: map[string]uint64{
		"rax": 0, "rdx": 1, "rcx": 2, "rbx": 3,
		"rsi": 4, "rdi": 5, "rbp": 6, "rsp": 7,
		"r8": 8, "r9": 9, "r10": 10, "r11": 11,
		"r12": 12, "r13": 13, "r14": 14, "r15": 15,
		"rip": 16,
	},
}

// cfiDirective routes one `.cfi_*` line into the shared recorder.
func (a *Assembler) cfiDirective(d, line string, off int) error {
	return a.cfi.Directive(x86CFI, d, strings.TrimSpace(strings.TrimPrefix(line, d)), off)
}

// DebugFrame renders the recorded CFI as the `.debug_frame` section of a
// final binary whose .text is at textVAddr.
func (a *Assembler) DebugFrame(textVAddr uint64) ([]byte, error) {
	if err := a.relax(); err != nil {
		return nil, err
	}
	return a.cfi.DebugFrame(x86CFI, textVAddr)
}

// EhFrame renders the recorded CFI as a .eh_frame image for a final binary
// whose .text is at textVAddr and whose .eh_frame is at ehVAddr.
func (a *Assembler) EhFrame(textVAddr, ehVAddr uint64) ([]byte, error) {
	return a.cfi.EhFrame(x86CFI, textVAddr, ehVAddr)
}

// EhFrameHdrLen is the size of the .eh_frame_hdr this program needs, or 0 if
// it carries no CFI. Known before either image is rendered, so the ELF writer
// can place both.
func (a *Assembler) EhFrameHdrLen() int { return a.cfi.EhFrameHdrLen() }

// EhFrameHdr renders the .eh_frame_hdr search table PT_GNU_EH_FRAME points at,
// for a binary whose .text, .eh_frame_hdr and .eh_frame land at the given
// addresses.
func (a *Assembler) EhFrameHdr(textVAddr, ehVAddr, hdrVAddr uint64) ([]byte, error) {
	return a.cfi.EhFrameHdr(x86CFI, textVAddr, ehVAddr, hdrVAddr)
}
