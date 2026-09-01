package x86_64

import (
	"fmt"
	"strconv"
	"strings"
)

// Call-frame information: `.cfi_*` directives in, `.eh_frame` out.
//
// Before this the x86-64 assembler REJECTED every `.cfi_*` directive as an
// unsupported directive, which meant it could not assemble a compiler-generated
// .s file at all — gcc and clang emit CFI by default. (The arm64 side took the
// other wrong turn and listed them with `.align` as no-ops, which produces a
// binary that silently cannot be unwound.)
//
// The output format is pinned from GNU as, never from the DWARF spec's prose:
// `as --64` on a minimal prologue, then `objdump -s -j .eh_frame` for the bytes
// and `readelf --debug-dump=frames` to read them back. That is what fixes the
// CIE's shape — augmentation "zR", code alignment 1, data alignment -8, return
// address column 16, FDE encoding 0x1b (pcrel|sdata4) — rather than any of the
// several encodings the spec would also allow.
//
// # Offsets and relaxation
//
// A CFA rule takes effect at a .text offset, and the FDE stores the DISTANCE
// between consecutive rules, so every delta is invalidated when branch
// relaxation shrinks the code between them. The offsets recorded here are
// therefore pre-relaxation, and relax() remaps them through the same mapNew it
// already uses for `.loc` rows. Recording final deltas at emission time would
// have been the silent-corruption version of this feature: the bytes would look
// well-formed and unwind to the wrong instruction.

// DWARF call-frame opcodes. Only the ones this assembler emits.
const (
	dwCFANop             = 0x00
	dwCFAAdvanceLoc1     = 0x02
	dwCFAAdvanceLoc2     = 0x03
	dwCFAAdvanceLoc4     = 0x04
	dwCFAOffsetExtended  = 0x05
	dwCFARestoreExtended = 0x06
	dwCFAUndefined       = 0x07
	dwCFASameValue       = 0x08
	dwCFARegister        = 0x09
	dwCFARememberState   = 0x0a
	dwCFARestoreState    = 0x0b
	dwCFADefCFA          = 0x0c
	dwCFADefCFARegister  = 0x0d
	dwCFADefCFAOffset    = 0x0e
	dwCFAAdvanceLoc      = 0x40 // | delta
	dwCFAOffset          = 0x80 // | register
	dwCFARestore         = 0xc0 // | register
)

// cfiDataAlign is the CIE's data alignment factor. A saved-register offset is
// stored divided by it, so `.cfi_offset %rbp, -16` records 2.
const cfiDataAlign = -8

// cfiRule is one CFA rule: the opcode bytes it contributes, and the .text
// offset it takes effect at. The offset is pre-relaxation; relax() remaps it.
type cfiRule struct {
	off  int
	body []byte
}

// cfiFDE is one `.cfi_startproc` … `.cfi_endproc` span.
type cfiFDE struct {
	start int // .text offset of .cfi_startproc, pre-relaxation
	end   int // .text offset of .cfi_endproc, pre-relaxation
	rules []cfiRule
}

// cfiState is the assembler's CFI accumulator.
type cfiState struct {
	fdes []cfiFDE
	open bool
}

// uleb128 appends the unsigned LEB128 encoding of v.
func uleb128(b []byte, v uint64) []byte {
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

// sleb128 appends the signed LEB128 encoding of v.
func sleb128(b []byte, v int64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		done := (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0)
		if !done {
			c |= 0x80
		}
		b = append(b, c)
		if done {
			return b
		}
	}
}

// dwarfRegNums is DWARF's x86-64 register numbering (System V psABI figure
// 3.36), which is NOT the ModRM encoding order: rdx and rcx are swapped, and
// so are rbx and rsp. Getting this wrong produces unwind data that names the
// wrong saved register and is otherwise well-formed.
var dwarfRegNums = map[string]uint64{
	"rax": 0, "rdx": 1, "rcx": 2, "rbx": 3,
	"rsi": 4, "rdi": 5, "rbp": 6, "rsp": 7,
	"r8": 8, "r9": 9, "r10": 10, "r11": 11,
	"r12": 12, "r13": 13, "r14": 14, "r15": 15,
	"rip": 16,
}

// cfiRegNum resolves a `.cfi_*` register operand. GNU as accepts the DWARF
// number (`6`) and the register spelling in whichever syntax is in force —
// `%rbp` in AT&T, bare `rbp` under `.intel_syntax noprefix`. This assembler
// reads Intel, but compiler output and hand-written asm use all three, so all
// three are accepted.
func cfiRegNum(tok string) (uint64, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return 0, fmt.Errorf("empty register operand")
	}
	if n, err := strconv.ParseUint(tok, 0, 16); err == nil {
		return n, nil
	}
	name := strings.TrimPrefix(tok, "%")
	n, ok := dwarfRegNums[name]
	if !ok {
		return 0, fmt.Errorf("unknown CFI register %q", tok)
	}
	return n, nil
}

// cfiOperands splits a directive's operand list on commas.
func cfiOperands(rest string) []string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// cfiDirective handles one `.cfi_*` line. `off` is the current .text length,
// which is the offset the rule takes effect at: a CFI directive emits no bytes,
// so the next instruction begins here.
func (a *Assembler) cfiDirective(d, rest string, off int) error {
	ops := cfiOperands(rest)
	num := func(i int) (int64, error) {
		if i >= len(ops) {
			return 0, fmt.Errorf("%s: missing operand %d", d, i+1)
		}
		return strconv.ParseInt(ops[i], 0, 64)
	}
	reg := func(i int) (uint64, error) {
		if i >= len(ops) {
			return 0, fmt.Errorf("%s: missing register operand", d)
		}
		return cfiRegNum(ops[i])
	}
	add := func(body []byte) error {
		if !a.cfi.open {
			return fmt.Errorf("%s outside .cfi_startproc/.cfi_endproc", d)
		}
		f := &a.cfi.fdes[len(a.cfi.fdes)-1]
		f.rules = append(f.rules, cfiRule{off: off, body: body})
		return nil
	}

	switch d {
	case ".cfi_startproc":
		if a.cfi.open {
			return fmt.Errorf(".cfi_startproc inside an open .cfi_startproc")
		}
		// `.cfi_startproc simple` suppresses the CIE's initial rules, which
		// this emitter's fixed CIE cannot express. Rejected rather than
		// silently assembled with the wrong initial state.
		if len(ops) > 0 && ops[0] != "" {
			return fmt.Errorf(".cfi_startproc %s is not supported", ops[0])
		}
		a.cfi.fdes = append(a.cfi.fdes, cfiFDE{start: off, end: off})
		a.cfi.open = true
		return nil
	case ".cfi_endproc":
		if !a.cfi.open {
			return fmt.Errorf(".cfi_endproc without .cfi_startproc")
		}
		a.cfi.fdes[len(a.cfi.fdes)-1].end = off
		a.cfi.open = false
		return nil
	case ".cfi_def_cfa":
		r, err := reg(0)
		if err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		return add(uleb128(uleb128([]byte{dwCFADefCFA}, r), uint64(n)))
	case ".cfi_def_cfa_register":
		r, err := reg(0)
		if err != nil {
			return err
		}
		return add(uleb128([]byte{dwCFADefCFARegister}, r))
	case ".cfi_def_cfa_offset":
		n, err := num(0)
		if err != nil {
			return err
		}
		return add(uleb128([]byte{dwCFADefCFAOffset}, uint64(n)))
	case ".cfi_offset":
		r, err := reg(0)
		if err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		if n%cfiDataAlign != 0 {
			return fmt.Errorf(".cfi_offset %s: offset %d is not a multiple of the data alignment %d",
				ops[0], n, cfiDataAlign)
		}
		f := n / cfiDataAlign
		if f < 0 {
			// A positive CFA-relative offset needs the extended_sf form,
			// which this emitter does not produce; refuse rather than
			// encode the factored value as unsigned and unwind wrongly.
			return fmt.Errorf(".cfi_offset %s: positive offset %d is not supported", ops[0], n)
		}
		if r < 64 {
			return add(uleb128([]byte{byte(dwCFAOffset | r)}, uint64(f)))
		}
		return add(uleb128(uleb128([]byte{dwCFAOffsetExtended}, r), uint64(f)))
	case ".cfi_register":
		r1, err := reg(0)
		if err != nil {
			return err
		}
		r2, err := reg(1)
		if err != nil {
			return err
		}
		return add(uleb128(uleb128([]byte{dwCFARegister}, r1), r2))
	case ".cfi_restore":
		r, err := reg(0)
		if err != nil {
			return err
		}
		if r < 64 {
			return add([]byte{byte(dwCFARestore | r)})
		}
		return add(uleb128([]byte{dwCFARestoreExtended}, r))
	case ".cfi_undefined":
		r, err := reg(0)
		if err != nil {
			return err
		}
		return add(uleb128([]byte{dwCFAUndefined}, r))
	case ".cfi_same_value":
		r, err := reg(0)
		if err != nil {
			return err
		}
		return add(uleb128([]byte{dwCFASameValue}, r))
	case ".cfi_remember_state":
		return add([]byte{dwCFARememberState})
	case ".cfi_restore_state":
		return add([]byte{dwCFARestoreState})
	}
	return fmt.Errorf("unsupported CFI directive %q", d)
}

// cfiAdvance appends the DW_CFA_advance_loc form that covers delta.
func cfiAdvance(b []byte, delta int) []byte {
	switch {
	case delta == 0:
		return b
	case delta < 0x40:
		return append(b, byte(dwCFAAdvanceLoc|delta))
	case delta < 0x100:
		return append(b, dwCFAAdvanceLoc1, byte(delta))
	case delta < 0x10000:
		return append(b, dwCFAAdvanceLoc2, byte(delta), byte(delta>>8))
	default:
		return append(b, dwCFAAdvanceLoc4,
			byte(delta), byte(delta>>8), byte(delta>>16), byte(delta>>24))
	}
}

// padCFI pads an entry's payload with DW_CFA_nop so the entry INCLUDING its
// 4-byte length field is a multiple of 8 — the address size, which is what
// GNU as aligns these to on x86-64.
func padCFI(payload []byte) []byte {
	for (len(payload)+4)%8 != 0 {
		payload = append(payload, dwCFANop)
	}
	return payload
}

// cfiCIE is the single CIE every FDE here points at, byte-identical to the one
// GNU as emits for x86-64: augmentation "zR" carrying an FDE pointer encoding
// of DW_EH_PE_pcrel|DW_EH_PE_sdata4, code alignment 1, data alignment -8,
// return address in column 16 (rip), and initial rules putting the CFA at
// rsp+8 with the return address saved at CFA-8.
func cfiCIE() []byte {
	var p []byte
	p = append(p, 0, 0, 0, 0) // CIE id
	p = append(p, 1)          // version
	p = append(p, 'z', 'R', 0)
	p = uleb128(p, 1)            // code alignment factor
	p = sleb128(p, cfiDataAlign) // data alignment factor
	p = uleb128(p, 16)           // return address column (rip)
	p = uleb128(p, 1)            // augmentation data length
	p = append(p, 0x1b)          // DW_EH_PE_pcrel | DW_EH_PE_sdata4
	p = append(p, dwCFADefCFA)
	p = uleb128(p, 7) // CFA is rsp+8 on entry
	p = uleb128(p, 8)
	p = append(p, byte(dwCFAOffset|16))
	p = uleb128(p, 1) // return address at CFA-8
	p = padCFI(p)
	out := []byte{byte(len(p)), byte(len(p) >> 8), byte(len(p) >> 16), byte(len(p) >> 24)}
	return append(out, p...)
}

// EhFrame renders the recorded CFI as a .eh_frame image: one CIE followed by
// one FDE per .cfi_startproc span, terminated by a zero length.
//
// textVAddr is the virtual address .text is loaded at and ehVAddr the address
// of the section being built, because the CIE declares pcrel FDE pointers: each
// FDE's initial_location is written as the signed distance from the field
// itself to the function it describes. In a relocatable object GNU as leaves
// that field zero and emits a relocation; this assembler produces final images,
// so the value is computed here.
func (a *Assembler) EhFrame(textVAddr, ehVAddr uint64) []byte {
	if len(a.cfi.fdes) == 0 {
		return nil
	}
	out := cfiCIE()
	for _, f := range a.cfi.fdes {
		var p []byte
		// CIE pointer: the distance BACK from this field to the CIE start.
		ciePtr := uint32(len(out) + 4)
		p = append(p, byte(ciePtr), byte(ciePtr>>8), byte(ciePtr>>16), byte(ciePtr>>24))
		// initial_location, pcrel from this field.
		fieldVA := ehVAddr + uint64(len(out)) + 8
		rel := int32(int64(textVAddr+uint64(f.start)) - int64(fieldVA))
		p = append(p, byte(rel), byte(rel>>8), byte(rel>>16), byte(rel>>24))
		size := uint32(f.end - f.start)
		p = append(p, byte(size), byte(size>>8), byte(size>>16), byte(size>>24))
		p = uleb128(p, 0) // augmentation data length
		at := f.start
		for _, r := range f.rules {
			p = cfiAdvance(p, r.off-at)
			at = r.off
			p = append(p, r.body...)
		}
		p = padCFI(p)
		out = append(out,
			byte(len(p)), byte(len(p)>>8), byte(len(p)>>16), byte(len(p)>>24))
		out = append(out, p...)
	}
	return append(out, 0, 0, 0, 0) // terminator
}
