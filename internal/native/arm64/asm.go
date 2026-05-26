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
	return &Assembler{labels: map[string]int{}}
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
