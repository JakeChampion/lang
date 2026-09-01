// Package cfi turns `.cfi_*` directives into a `.eh_frame` image.
//
// Shared by the x86-64 and arm64 assemblers, because the CIE differs between
// them in four ways and every one of them is a chance to hand-derive something
// plausible and wrong:
//
//	                        x86-64                     arm64
//	code alignment          1                          4
//	return address column   16 (rip)                   30 (LR)
//	initial rules           def_cfa rsp+8, RA at -8    def_cfa sp+0, no RA rule
//	entry alignment         8                          4
//
// Writing that twice is the divergence class #7903 exists to kill, so the
// machinery lives here once and each target supplies a Profile.
//
// Every profile is pinned from GNU as, never from the DWARF spec's prose:
// `as` on a minimal prologue, then `objdump -s -j .eh_frame` for the bytes and
// `readelf --debug-dump=frames` to read them back. The spec leaves each of
// those fields open, so an implementation can be internally consistent across
// all of them and still unwind nothing — the consumer is libgcc/libunwind
// reading what gas conventionally produces.
//
// # Offsets and relaxation
//
// A CFA rule takes effect at a .text offset, and an FDE stores the DISTANCE
// between consecutive rules, so every delta is invalidated when branch
// relaxation moves the code between them. Offsets recorded here are therefore
// pre-relaxation and the assembler calls Remap afterwards. Recording final
// deltas at emission time would be the silent-corruption version of this
// feature: the bytes stay well-formed and unwind at the wrong instruction.
package cfi

import (
	"fmt"
	"strconv"
	"strings"
)

// DWARF call-frame opcodes. Only the ones this package emits.
const (
	opNop             = 0x00
	opAdvanceLoc1     = 0x02
	opAdvanceLoc2     = 0x03
	opAdvanceLoc4     = 0x04
	opOffsetExtended  = 0x05
	opRestoreExtended = 0x06
	opUndefined       = 0x07
	opSameValue       = 0x08
	opRegister        = 0x09
	opRememberState   = 0x0a
	opRestoreState    = 0x0b
	opDefCFA          = 0x0c
	opDefCFARegister  = 0x0d
	opDefCFAOffset    = 0x0e
	opAdvanceLoc      = 0x40 // | delta
	opOffset          = 0x80 // | register
	opRestore         = 0xc0 // | register
)

// Profile is one target's CIE shape and register vocabulary.
type Profile struct {
	// CodeAlign is the code alignment factor. An advance is encoded in units
	// of it, so on a fixed-width ISA the deltas divide exactly by 4.
	CodeAlign uint64
	// DataAlign is the data alignment factor: a saved-register offset is
	// stored divided by it, so with -8 a `.cfi_offset` of -16 records 2.
	DataAlign int64
	// RAColumn is the column the return address lives in.
	RAColumn uint64
	// InitialRules is the CIE's initial CFA program, as gas emits it.
	InitialRules []byte
	// Regs maps a register spelling to its DWARF number. Callers may write
	// the number directly, the AT&T `%name`, or the bare Intel `name`.
	Regs map[string]uint64
}

// rule is one CFA rule: the opcode bytes it contributes and the .text offset
// it takes effect at.
type rule struct {
	off  int
	body []byte
}

// fde is one `.cfi_startproc` … `.cfi_endproc` span.
type fde struct {
	start int
	end   int
	rules []rule
}

// State accumulates the CFI an assembler has parsed.
type State struct {
	fdes []fde
	open bool
}

// Empty reports whether no CFI was recorded, so the caller can skip the
// section entirely.
func (s *State) Empty() bool { return len(s.fdes) == 0 }

// Remap rewrites every recorded offset through f, which maps a pre-relaxation
// .text offset onto its final position.
func (s *State) Remap(f func(int) int) {
	for i := range s.fdes {
		e := &s.fdes[i]
		e.start = f(e.start)
		e.end = f(e.end)
		for j := range e.rules {
			e.rules[j].off = f(e.rules[j].off)
		}
	}
}

// ULEB appends the unsigned LEB128 encoding of v.
func ULEB(b []byte, v uint64) []byte {
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

// SLEB appends the signed LEB128 encoding of v.
func SLEB(b []byte, v int64) []byte {
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

// regNum resolves a `.cfi_*` register operand. GNU as accepts the DWARF number
// (`6`) and the register spelling in whichever syntax is in force — `%rbp` in
// AT&T, bare `rbp` under `.intel_syntax noprefix`, `x29` on aarch64 — and
// compiler output uses all of them.
func (p *Profile) regNum(tok string) (uint64, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return 0, fmt.Errorf("empty register operand")
	}
	if n, err := strconv.ParseUint(tok, 0, 16); err == nil {
		return n, nil
	}
	n, ok := p.Regs[strings.TrimPrefix(tok, "%")]
	if !ok {
		return 0, fmt.Errorf("unknown CFI register %q", tok)
	}
	return n, nil
}

// operands splits a directive's operand list on commas.
func operands(rest string) []string {
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

// Directive handles one `.cfi_*` line. `off` is the current .text length,
// which is the offset the rule takes effect at: a CFI directive emits no
// bytes, so the next instruction begins there.
//
// A directive this package cannot encode correctly is REFUSED rather than
// approximated. Silently wrong unwind data is worse than a build error,
// because it is only read once something has already gone wrong.
func (s *State) Directive(p *Profile, d, rest string, off int) error {
	ops := operands(rest)
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
		return p.regNum(ops[i])
	}
	add := func(body []byte) error {
		if !s.open {
			return fmt.Errorf("%s outside .cfi_startproc/.cfi_endproc", d)
		}
		f := &s.fdes[len(s.fdes)-1]
		f.rules = append(f.rules, rule{off: off, body: body})
		return nil
	}

	switch d {
	case ".cfi_startproc":
		if s.open {
			return fmt.Errorf(".cfi_startproc inside an open .cfi_startproc")
		}
		// `.cfi_startproc simple` suppresses the CIE's initial rules, which a
		// fixed CIE cannot express.
		if len(ops) > 0 && ops[0] != "" {
			return fmt.Errorf(".cfi_startproc %s is not supported", ops[0])
		}
		s.fdes = append(s.fdes, fde{start: off, end: off})
		s.open = true
		return nil
	case ".cfi_endproc":
		if !s.open {
			return fmt.Errorf(".cfi_endproc without .cfi_startproc")
		}
		s.fdes[len(s.fdes)-1].end = off
		s.open = false
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
		return add(ULEB(ULEB([]byte{opDefCFA}, r), uint64(n)))
	case ".cfi_def_cfa_register":
		r, err := reg(0)
		if err != nil {
			return err
		}
		return add(ULEB([]byte{opDefCFARegister}, r))
	case ".cfi_def_cfa_offset":
		n, err := num(0)
		if err != nil {
			return err
		}
		return add(ULEB([]byte{opDefCFAOffset}, uint64(n)))
	case ".cfi_offset":
		r, err := reg(0)
		if err != nil {
			return err
		}
		n, err := num(1)
		if err != nil {
			return err
		}
		if n%p.DataAlign != 0 {
			return fmt.Errorf(".cfi_offset %s: offset %d is not a multiple of the data alignment %d",
				ops[0], n, p.DataAlign)
		}
		f := n / p.DataAlign
		if f < 0 {
			// A positive CFA-relative offset needs the extended_sf form,
			// which this package does not produce; refuse rather than encode
			// the factored value as unsigned and unwind wrongly.
			return fmt.Errorf(".cfi_offset %s: positive offset %d is not supported", ops[0], n)
		}
		if r < 64 {
			return add(ULEB([]byte{byte(opOffset | r)}, uint64(f)))
		}
		return add(ULEB(ULEB([]byte{opOffsetExtended}, r), uint64(f)))
	case ".cfi_register":
		r1, err := reg(0)
		if err != nil {
			return err
		}
		r2, err := reg(1)
		if err != nil {
			return err
		}
		return add(ULEB(ULEB([]byte{opRegister}, r1), r2))
	case ".cfi_restore":
		r, err := reg(0)
		if err != nil {
			return err
		}
		if r < 64 {
			return add([]byte{byte(opRestore | r)})
		}
		return add(ULEB([]byte{opRestoreExtended}, r))
	case ".cfi_undefined":
		r, err := reg(0)
		if err != nil {
			return err
		}
		return add(ULEB([]byte{opUndefined}, r))
	case ".cfi_same_value":
		r, err := reg(0)
		if err != nil {
			return err
		}
		return add(ULEB([]byte{opSameValue}, r))
	case ".cfi_remember_state":
		return add([]byte{opRememberState})
	case ".cfi_restore_state":
		return add([]byte{opRestoreState})
	}
	return fmt.Errorf("unsupported CFI directive %q", d)
}

// advance appends the DW_CFA_advance_loc form covering a byte delta, in units
// of the code alignment factor.
func (p *Profile) advance(b []byte, deltaBytes int) ([]byte, error) {
	if uint64(deltaBytes)%p.CodeAlign != 0 {
		return nil, fmt.Errorf("CFA rule at a %d-byte offset is not a multiple of the code alignment %d",
			deltaBytes, p.CodeAlign)
	}
	d := uint64(deltaBytes) / p.CodeAlign
	switch {
	case d == 0:
		return b, nil
	case d < 0x40:
		return append(b, byte(opAdvanceLoc|d)), nil
	case d < 0x100:
		return append(b, opAdvanceLoc1, byte(d)), nil
	case d < 0x10000:
		return append(b, opAdvanceLoc2, byte(d), byte(d>>8)), nil
	default:
		return append(b, opAdvanceLoc4,
			byte(d), byte(d>>8), byte(d>>16), byte(d>>24)), nil
	}
}

// pad4 fills an entry's payload with DW_CFA_nop to a multiple of 4.
//
// GNU as pads every entry's payload to 4 and then pads the LAST entry further
// so the whole section is a multiple of 8. That one rule reproduces both
// targets; the per-entry totals it produces differ between them (24/32 on
// x86-64, 20/36 on aarch64) only because their CIEs are different lengths,
// which is what made it look for a while like two different padding policies.
func pad4(payload []byte) []byte {
	for len(payload)%4 != 0 {
		payload = append(payload, opNop)
	}
	return payload
}

func le32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// CIE renders the single CIE every FDE points at.
func (p *Profile) CIE() []byte {
	var b []byte
	b = le32(b, 0) // CIE id
	b = append(b, 1)
	b = append(b, 'z', 'R', 0)
	b = ULEB(b, p.CodeAlign)
	b = SLEB(b, p.DataAlign)
	b = ULEB(b, p.RAColumn)
	b = ULEB(b, 1)      // augmentation data length
	b = append(b, 0x1b) // DW_EH_PE_pcrel | DW_EH_PE_sdata4
	b = append(b, p.InitialRules...)
	b = pad4(b)
	return append(le32(nil, uint32(len(b))), b...)
}

// EhFrame renders the recorded CFI: one CIE, one FDE per `.cfi_startproc`
// span, then a zero-length terminator.
//
// textVAddr is where .text is loaded and ehVAddr where this image will be,
// because the CIE declares pcrel FDE pointers: each initial_location is the
// signed distance from the field itself to the function it describes. A
// relocatable object leaves that zero and emits a relocation; these assemblers
// produce final images, so it is computed here.
func (s *State) EhFrame(p *Profile, textVAddr, ehVAddr uint64) ([]byte, error) {
	if s.open {
		return nil, fmt.Errorf(".cfi_startproc without .cfi_endproc")
	}
	if len(s.fdes) == 0 {
		return nil, nil
	}
	out := p.CIE()
	payloads := make([][]byte, 0, len(s.fdes))
	// Entry starts have to be known before the payloads are written, since an
	// FDE's CIE pointer and pcrel initial_location both measure from its own
	// position. Lengths are fixed by the 4-alignment, so a first pass over the
	// payloads gives both.
	at := len(out)
	for _, f := range s.fdes {
		var b []byte
		// CIE pointer: the distance BACK from this field to the CIE start.
		b = le32(b, uint32(at+4))
		fieldVA := ehVAddr + uint64(at) + 8
		b = le32(b, uint32(int32(int64(textVAddr+uint64(f.start))-int64(fieldVA))))
		b = le32(b, uint32(f.end-f.start))
		b = ULEB(b, 0) // augmentation data length
		pc := f.start
		for _, r := range f.rules {
			var err error
			if b, err = p.advance(b, r.off-pc); err != nil {
				return nil, err
			}
			pc = r.off
			b = append(b, r.body...)
		}
		b = pad4(b)
		payloads = append(payloads, b)
		at += 4 + len(b)
	}
	// The section as a whole lands on 8; only the last entry absorbs it, and
	// four more nops keep that entry's own 4-alignment.
	if at%8 != 0 {
		last := len(payloads) - 1
		payloads[last] = append(payloads[last], opNop, opNop, opNop, opNop)
	}
	for _, b := range payloads {
		out = append(le32(out, uint32(len(b))), b...)
	}
	return append(out, 0, 0, 0, 0), nil
}
