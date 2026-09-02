package arm64

import (
	"strings"

	"github.com/jakechampion/lang/internal/native/cfi"
)

// arm64CFI is the CIE shape GNU as emits for aarch64, pinned from
// `aarch64-linux-gnu-as` on a minimal prologue read back with
// `readelf --debug-dump=frames`. It differs from x86-64's in every field that
// the DWARF spec leaves to the producer:
//
//   - code alignment 4, not 1. Advances are encoded in INSTRUCTIONS, so a
//     four-byte step is DW_CFA_advance_loc 1 and a byte offset that is not a
//     multiple of 4 is a bug rather than a wider encoding.
//   - the return address is column 30 (LR), not 16.
//   - the CFA starts at sp+0 with NO rule for the return address: on entry it
//     is in a register, not spilled, so there is nothing to describe.
var arm64CFI = &cfi.Profile{
	CodeAlign:    4,
	DataAlign:    -8,
	RAColumn:     30,
	InitialRules: []byte{0x0c, 0x1f, 0x00}, // DW_CFA_def_cfa sp, 0
	Regs:         arm64CFIRegs(),
}

// arm64CFIRegs is the DWARF aarch64 register numbering: x0..x30 are 0..30 and
// sp is 31, with the ABI's aliases for the frame pointer and link register,
// which is how compiler-generated CFI names them.
func arm64CFIRegs() map[string]uint64 {
	m := map[string]uint64{"sp": 31, "fp": 29, "lr": 30}
	for i := uint64(0); i <= 30; i++ {
		m["x"+itoa(i)] = i
	}
	return m
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// cfiDirective routes one `.cfi_*` line into the shared recorder. A CFI
// directive emits no instructions, so the current .text length in bytes is the
// offset the rule takes effect at.
func (a *Assembler) cfiDirective(d, line string) error {
	return a.cfi.Directive(arm64CFI, d, strings.TrimSpace(strings.TrimPrefix(line, d)), len(a.insns)*4)
}

// EhFrame renders the recorded CFI as a .eh_frame image for a final binary
// whose .text is at textVAddr and whose .eh_frame is at ehVAddr.
func (a *Assembler) EhFrame(textVAddr, ehVAddr uint64) ([]byte, error) {
	return a.cfi.EhFrame(arm64CFI, textVAddr, ehVAddr)
}

// EhFrameHdrLen is the size of the .eh_frame_hdr this program needs, or 0 if
// it carries no CFI. Known before either image is rendered, so the ELF writer
// can place both.
func (a *Assembler) EhFrameHdrLen() int { return a.cfi.EhFrameHdrLen() }

// EhFrameHdr renders the .eh_frame_hdr search table PT_GNU_EH_FRAME points at,
// for a binary whose .text, .eh_frame_hdr and .eh_frame land at the given
// addresses.
func (a *Assembler) EhFrameHdr(textVAddr, ehVAddr, hdrVAddr uint64) ([]byte, error) {
	return a.cfi.EhFrameHdr(arm64CFI, textVAddr, ehVAddr, hdrVAddr)
}
