package arm64

import "fmt"

// Assembler collects a stream of instructions plus named labels and
// PC-relative branches to them, then resolves the branch offsets in a
// final back-patching pass (Bytes). It's the piece that turns the
// fixed-width per-instruction encoders into real control flow —
// loops, conditionals, and calls — without the caller having to
// hand-compute branch displacements.
//
// Fixed instructions are emitted with Emit (any uint32 from the
// encoders above). Branches that target a label are emitted with B /
// BL / Bcond / CBZ / CBNZ; they record a fixup and a placeholder word,
// which Bytes patches once every label position is known.
type Assembler struct {
	insns  []uint32
	labels map[string]int
	fixups []fixup

	// Data section + symbol addressing (used by AssembleProgram).
	rodata     []byte
	syms       map[string]symbol
	adrpFixups []symFixup
	lo12Fixups []symFixup
}

// symbol is a named location: in .text it's an instruction index, in
// .rodata a byte offset within the rodata blob.
type symbol struct {
	inText bool
	val    int
}

// symFixup records an adrp / add-#:lo12: instruction that references a
// symbol by name, to be resolved once section virtual addresses are
// fixed. rd/rn hold the instruction's registers.
type symFixup struct {
	at    int
	label string
	rd    uint32
	rn    uint32 // add-lo12 only
}

type branchKind int

const (
	branchImm26 branchKind = iota // b / bl: 26-bit offset in bits[25:0]
	branchImm19                   // b.cond / cbz / cbnz: 19-bit offset in bits[23:5]
)

type fixup struct {
	at    int // index into insns of the branch placeholder
	label string
	kind  branchKind
}

// NewAssembler returns an empty assembler.
func NewAssembler() *Assembler {
	return &Assembler{labels: map[string]int{}, syms: map[string]symbol{}}
}

// TextLabel marks a .text symbol at the current instruction position
// (also usable as a branch target).
func (a *Assembler) TextLabel(name string) {
	a.labels[name] = len(a.insns)
	a.syms[name] = symbol{inText: true, val: len(a.insns)}
}

// RodataLabel marks a .rodata symbol at the current rodata offset.
func (a *Assembler) RodataLabel(name string) {
	a.syms[name] = symbol{inText: false, val: len(a.rodata)}
}

// AppendRodata appends raw bytes to the .rodata blob.
func (a *Assembler) AppendRodata(b []byte) { a.rodata = append(a.rodata, b...) }

// AlignRodata pads .rodata to a multiple of n bytes.
func (a *Assembler) AlignRodata(n int) {
	for n > 0 && len(a.rodata)%n != 0 {
		a.rodata = append(a.rodata, 0)
	}
}

// ADRPsym emits `adrp Xrd, sym` as a placeholder, resolved by
// BytesProgram once addresses are known.
func (a *Assembler) ADRPsym(rd uint32, sym string) {
	a.adrpFixups = append(a.adrpFixups, symFixup{at: len(a.insns), label: sym, rd: rd})
	a.insns = append(a.insns, ADRP(rd, 0))
}

// AddLo12 emits `add Xrd, Xrn, #:lo12:sym` as a placeholder.
func (a *Assembler) AddLo12(rd, rn uint32, sym string) {
	a.lo12Fixups = append(a.lo12Fixups, symFixup{at: len(a.insns), label: sym, rd: rd, rn: rn})
	a.insns = append(a.insns, ADDimm(rd, rn, 0, false))
}

// Emit appends a fully-encoded instruction word.
func (a *Assembler) Emit(insn uint32) {
	a.insns = append(a.insns, insn)
}

// Label marks the current position with a name that branches can
// target. A label may be defined after the branches that reference it
// (forward branch) or before (backward branch).
func (a *Assembler) Label(name string) {
	a.labels[name] = len(a.insns)
}

// B emits `b label` — unconditional PC-relative branch.
func (a *Assembler) B(label string) {
	a.branch(0x14000000, label, branchImm26)
}

// BL emits `bl label` — PC-relative call (link in x30).
func (a *Assembler) BL(label string) {
	a.branch(0x94000000, label, branchImm26)
}

// Bcond emits `b.<cond> label` — conditional branch. cond is one of
// the Cond* constants.
func (a *Assembler) Bcond(cond uint32, label string) {
	a.branch(0x54000000|(cond&0xf), label, branchImm19)
}

// CBZ emits `cbz Xt, label` — branch if Xt == 0 (64-bit).
func (a *Assembler) CBZ(rt uint32, label string) {
	a.branch(0xB4000000|(rt&regMask), label, branchImm19)
}

// CBNZ emits `cbnz Xt, label` — branch if Xt != 0 (64-bit).
func (a *Assembler) CBNZ(rt uint32, label string) {
	a.branch(0xB5000000|(rt&regMask), label, branchImm19)
}

func (a *Assembler) branch(base uint32, label string, kind branchKind) {
	a.fixups = append(a.fixups, fixup{at: len(a.insns), label: label, kind: kind})
	a.insns = append(a.insns, base)
}

// Bytes resolves every branch fixup and returns the assembled
// little-endian machine code. It errors if a branch targets a label
// that was never defined.
func (a *Assembler) Bytes() ([]byte, error) {
	for _, f := range a.fixups {
		target, ok := a.labels[f.label]
		if !ok {
			return nil, fmt.Errorf("arm64: branch to undefined label %q", f.label)
		}
		// Offset is measured in instructions (= bytes/4), signed,
		// relative to the branch itself. Two's-complement low bits go
		// straight into the immediate field.
		off := uint32(target - f.at)
		switch f.kind {
		case branchImm26:
			a.insns[f.at] |= off & 0x03ffffff
		case branchImm19:
			a.insns[f.at] |= (off & 0x7ffff) << 5
		}
	}
	var buf []byte
	for _, insn := range a.insns {
		buf = Put(buf, insn)
	}
	return buf, nil
}

// BytesProgram resolves branches AND symbol references (adrp / add
// #:lo12:), laying .text at textVAddr and .rodata immediately after
// (8-byte aligned). It returns the final .text and .rodata blobs.
func (a *Assembler) BytesProgram(textVAddr uint64) (text, rodata []byte, err error) {
	rodataVAddr := textVAddr + uint64(len(a.insns)*4)
	if rem := rodataVAddr % 8; rem != 0 {
		rodataVAddr += 8 - rem
	}

	symVAddr := func(name string) (uint64, bool) {
		s, ok := a.syms[name]
		if !ok {
			return 0, false
		}
		if s.inText {
			return textVAddr + uint64(s.val)*4, true
		}
		return rodataVAddr + uint64(s.val), true
	}

	// Branch fixups (text-relative), same as Bytes.
	for _, f := range a.fixups {
		target, ok := a.labels[f.label]
		if !ok {
			return nil, nil, fmt.Errorf("arm64: branch to undefined label %q", f.label)
		}
		off := uint32(target - f.at)
		switch f.kind {
		case branchImm26:
			a.insns[f.at] |= off & 0x03ffffff
		case branchImm19:
			a.insns[f.at] |= (off & 0x7ffff) << 5
		}
	}

	// adrp: page(sym) - page(insn), in 4 KiB units.
	for _, f := range a.adrpFixups {
		sv, ok := symVAddr(f.label)
		if !ok {
			return nil, nil, fmt.Errorf("arm64: adrp to undefined symbol %q", f.label)
		}
		insnVAddr := textVAddr + uint64(f.at)*4
		pageDelta := int32(int64(sv>>12) - int64(insnVAddr>>12))
		a.insns[f.at] = ADRP(f.rd, pageDelta)
	}

	// add #:lo12:sym → low 12 bits of the symbol address.
	for _, f := range a.lo12Fixups {
		sv, ok := symVAddr(f.label)
		if !ok {
			return nil, nil, fmt.Errorf("arm64: :lo12: of undefined symbol %q", f.label)
		}
		a.insns[f.at] = ADDimm(f.rd, f.rn, uint16(sv&0xfff), false)
	}

	for _, insn := range a.insns {
		text = Put(text, insn)
	}
	return text, a.rodata, nil
}
