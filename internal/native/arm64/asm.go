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
	rodata        []byte
	syms          map[string]symbol
	adrpFixups    []symFixup
	lo12Fixups    []symFixup
	quadSymFixups []quadSymFixup

	// Literal pool (ldr Xt, =value): pending literals awaiting the next
	// flush (.ltorg or end), and the placed literals to relocate.
	pendingLits []litRef
	litFixups   []litResolve

	// DWARF .debug_line rows: the source line active at a .text byte offset,
	// recorded when the code generator emits a `.loc` directive under -g.
	locRows []LineRow

	// Branch veneers (see veneer.go): veneerSeq names each synthetic
	// trampoline label, veneerReach overrides the b/bl span in tests,
	// veneerPasses records how many rounds it took to reach a fixed
	// point, and veneerErr carries a failure from MachOTextLen (which
	// plants veneers but cannot return an error) to LinkMachO.
	veneerSeq    int
	veneerReach  int
	veneerPasses int
	veneerErr    error
}

// LineRow is one DWARF .debug_line row: the source line active at a .text
// byte offset (the next instruction's, since `.loc` emits no bytes). arm64
// instructions are fixed 4 bytes, so the offset is the instruction index × 4.
// The linker converts Offset to an absolute vaddr. Mirror of the x86-64
// assembler's LineRow.
type LineRow struct {
	Offset int
	Line   int
}

type litRef struct {
	at   int    // index of the ldr-literal instruction
	val  uint64 // the literal value
	wide bool   // 8-byte (x) vs 4-byte (w)
}

type litResolve struct {
	at      int  // ldr-literal instruction index
	poolIdx int  // index of the literal's first word in insns
	wide    bool // 8-byte literal: it occupies poolIdx and poolIdx+1
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

// quadSymFixup records a `.quad <symbol>` slot in .rodata: the 8 bytes
// at offset `at` must hold the absolute virtual address of `label`,
// resolved once section addresses are fixed. Used for function-pointer /
// closure tables (a non-PIE static executable, so absolute is fine).
type quadSymFixup struct {
	at    int // byte offset within rodata
	label string
}

type branchKind int

const (
	branchImm26 branchKind = iota // b / bl: 26-bit offset in bits[25:0]
	branchImm19                   // b.cond / cbz / cbnz: 19-bit offset in bits[23:5]
	branchImm14                   // tbz / tbnz: 14-bit offset in bits[18:5]
)

type fixup struct {
	at    int // index into insns of the branch placeholder
	label string
	kind  branchKind
}

// NewAssembler returns an empty assembler.
func NewAssembler() *Assembler {
	return &Assembler{labels: map[string]int{}, syms: map[string]symbol{}, veneerReach: envVeneerReach()}
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

// AppendQuadSym appends an 8-byte .rodata slot that will hold the
// absolute virtual address of sym, filled in by BytesProgram. Backs
// `.quad <symbol>` (function-pointer / closure tables).
func (a *Assembler) AppendQuadSym(sym string) {
	a.quadSymFixups = append(a.quadSymFixups, quadSymFixup{at: len(a.rodata), label: sym})
	a.AppendRodata(make([]byte, 8))
}

// AppendRodata appends raw bytes to the .rodata blob.
func (a *Assembler) AppendRodata(b []byte) { a.rodata = append(a.rodata, b...) }

// AlignRodata pads .rodata to a multiple of n bytes.
func (a *Assembler) AlignRodata(n int) {
	for n > 0 && len(a.rodata)%n != 0 {
		a.rodata = append(a.rodata, 0)
	}
}

// LDRLiteral emits `ldr Rt, =value` — a PC-relative load from a
// literal pool. The value is stashed until the next FlushLiterals
// (.ltorg or program end), then the load's 19-bit offset is resolved
// in BytesProgram. wide selects an 8-byte (x) vs 4-byte (w) literal.
func (a *Assembler) LDRLiteral(rt uint32, val uint64, wide bool) {
	base := uint32(0x18000000) // ldr (literal), 32-bit
	if wide {
		base = 0x58000000 // ldr (literal), 64-bit
	}
	a.pendingLits = append(a.pendingLits, litRef{at: len(a.insns), val: val, wide: wide})
	a.insns = append(a.insns, base|(rt&regMask))
}

// FlushLiterals places the pending literal-pool entries into the
// instruction stream at the current position (the .ltorg point), 8-byte
// aligning the wide ones. Each load's offset is resolved later by
// BytesProgram. Placing the pool after a ret/branch keeps it off the
// execution path.
func (a *Assembler) FlushLiterals() {
	for _, l := range a.pendingLits {
		if l.wide && len(a.insns)%2 != 0 {
			a.insns = append(a.insns, 0) // pad to 8-byte alignment
		}
		poolIdx := len(a.insns)
		a.insns = append(a.insns, uint32(l.val))
		if l.wide {
			a.insns = append(a.insns, uint32(l.val>>32))
		}
		a.litFixups = append(a.litFixups, litResolve{at: l.at, poolIdx: poolIdx, wide: l.wide})
	}
	a.pendingLits = nil
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

// CBZW emits `cbz Wt, label` — branch if the 32-bit Wt == 0 (sf=0). The
// 32-bit form compares only the low word, matching GNU as for a `w`
// operand; the 64-bit CBZ (sf=1) would test all 64 bits.
func (a *Assembler) CBZW(rt uint32, label string) {
	a.branch(0x34000000|(rt&regMask), label, branchImm19)
}

// CBNZW emits `cbnz Wt, label` — branch if the 32-bit Wt != 0 (sf=0).
func (a *Assembler) CBNZW(rt uint32, label string) {
	a.branch(0x35000000|(rt&regMask), label, branchImm19)
}

// TBZ emits `tbz Rt, #bit, label` — branch if bit `bit` of Rt is 0.
// TBNZ is the branch-if-set form. The 6-bit position splits across
// bit 31 (b5) and bits 23:19 (b40); the 14-bit offset is filled by the
// fixup pass.
func (a *Assembler) TBZ(rt, bit uint32, label string) {
	a.testBranch(0x36000000, rt, bit, label)
}

// TBNZ emits `tbnz Rt, #bit, label`.
func (a *Assembler) TBNZ(rt, bit uint32, label string) {
	a.testBranch(0x37000000, rt, bit, label)
}

func (a *Assembler) testBranch(base, rt, bit uint32, label string) {
	b5 := (bit >> 5) & 1
	b40 := bit & 0x1f
	a.branch(base|(b5<<31)|(b40<<19)|(rt&regMask), label, branchImm14)
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
		if err := checkBranchRange(f.kind, target-f.at, f.label); err != nil {
			return nil, err
		}
		off := uint32(target - f.at)
		switch f.kind {
		case branchImm26:
			a.insns[f.at] |= off & 0x03ffffff
		case branchImm19:
			a.insns[f.at] |= (off & 0x7ffff) << 5
		case branchImm14:
			a.insns[f.at] |= (off & 0x3fff) << 5
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
// (8-byte aligned). It returns the final .text and .rodata blobs. This is
// the single-segment layout (elf.StaticExecutableData / R+W+X).
func (a *Assembler) BytesProgram(textVAddr uint64) (text, rodata []byte, err error) {
	a.FlushLiterals()
	if err := a.insertVeneers(); err != nil {
		return nil, nil, err
	}
	rodataVAddr := textVAddr + uint64(len(a.insns)*4)
	if rem := rodataVAddr % 8; rem != 0 {
		rodataVAddr += 8 - rem
	}
	return a.bytesProgramAt(textVAddr, rodataVAddr, nil, nil)
}

// BytesProgramWX is BytesProgram for the W^X two-segment ELF layout
// (elf.StaticExecutableDataWX): .rodata is placed on the first 16 KiB page
// boundary at or after the end of .text — a separate R+W PT_LOAD — rather
// than contiguously after it, so the code segment can be mapped R+X. The
// page size matches elf.pageAlign; pass elf.TextVAddrWX as textVAddr.
func (a *Assembler) BytesProgramWX(textVAddr uint64) (text, rodata []byte, err error) {
	a.FlushLiterals()
	if err := a.insertVeneers(); err != nil {
		return nil, nil, err
	}
	const page = 0x10000 // must match elf.pageAlign
	rodataVAddr := (textVAddr + uint64(len(a.insns)*4) + page - 1) &^ (page - 1)
	return a.bytesProgramAt(textVAddr, rodataVAddr, nil, nil)
}

// Reloc is one R_AARCH64_RELATIVE dynamic relocation for a position-
// independent executable: at load time the loader (or the program's own
// self-relocation prologue) computes `*(base + Offset) = base + Addend`.
// Both fields are link-time values relative to a load base of 0, matching
// the ET_DYN image elf.StaticPieExecutable produces. The only absolute
// addresses a Fern binary embeds are the `.quad <symbol>` function-pointer
// table slots (vtables, closures); adrp/:lo12: code references are
// PC-relative and need no relocation.
type Reloc struct {
	Offset uint64 // where the 8-byte slot lives (relative to load base)
	Addend uint64 // the target's address (relative to load base)
}

// TextLabelVAddr returns the load-base-relative virtual address of a .text
// symbol (textVAddr + its instruction offset), or false if name is not a
// defined .text label. Used to resolve .so export addresses.
func (a *Assembler) TextLabelVAddr(name string, textVAddr uint64) (uint64, bool) {
	s, ok := a.syms[name]
	if !ok || !s.inText {
		return 0, false
	}
	return textVAddr + uint64(s.val)*4, true
}

// TextLabelVAddrs returns every .text label mapped to its absolute virtual
// address (textVAddr + instruction-index × 4) — the function-symbol set the
// ELF writer emits into .symtab under `-g`.
func (a *Assembler) TextLabelVAddrs(textVAddr uint64) map[string]uint64 {
	out := make(map[string]uint64, len(a.syms))
	for name, s := range a.syms {
		if s.inText {
			out[name] = textVAddr + uint64(s.val)*4
		}
	}
	return out
}

// BytesProgramPIE resolves the program for a static position-independent
// executable (elf.StaticPieExecutable): the same W^X two-segment layout,
// but laid out relative to a load base of 0 (pass elf.TextVAddrPIE as
// textVAddr) and returning the list of R_AARCH64_RELATIVE relocations for
// the `.quad <symbol>` slots. PC-relative fixups (branches, adrp/:lo12:,
// ldr-literals) are base-independent and need no relocation.
func (a *Assembler) BytesProgramPIE(textVAddr uint64) (text, rodata []byte, relocs []Reloc, err error) {
	a.FlushLiterals()
	if err := a.insertVeneers(); err != nil {
		return nil, nil, nil, err
	}
	const page = 0x10000 // must match elf.pageAlign
	rodataVAddr := (textVAddr + uint64(len(a.insns)*4) + page - 1) &^ (page - 1)

	// Synthetic symbols the self-relocation prologue references via
	// adrp/:lo12:. Their addresses are relative to a load base of 0 and
	// MUST match where elf.StaticPieExecutable lays the corresponding
	// bytes: .rela.dyn is 8-aligned after the data blob, one Elf64_Rela
	// (24 bytes) per `.quad <symbol>` slot; __ehdr_start is the ELF header
	// at vaddr 0, so adrp/:lo12: of it yields the runtime load base.
	relaStart := rodataVAddr + uint64(len(a.rodata))
	if rem := relaStart % 8; rem != 0 {
		relaStart += 8 - rem
	}
	relaEnd := relaStart + uint64(len(a.quadSymFixups))*24
	pieSyms := map[string]uint64{
		"__ehdr_start": 0,
		"__rela_start": relaStart,
		"__rela_end":   relaEnd,
	}

	var rs []Reloc
	text, rodata, err = a.bytesProgramAt(textVAddr, rodataVAddr, &rs, pieSyms)
	return text, rodata, rs, err
}

// bytesProgramAt resolves every vaddr-dependent fixup for a layout where
// .text loads at textVAddr and the .rodata/.data blob loads at rodataVAddr
// (contiguous for BytesProgram, page-aligned for BytesProgramWX). Literals
// must already be flushed by the caller. When relocs != nil the layout is
// treated as base-relative (a PIE) and each `.quad <symbol>` slot records
// an R_AARCH64_RELATIVE entry into *relocs.
func (a *Assembler) bytesProgramAt(textVAddr, rodataVAddr uint64, relocs *[]Reloc, pieSyms map[string]uint64) (text, rodata []byte, err error) {
	// Resolve each ldr-literal's PC-relative offset.
	for _, f := range a.litFixups {
		insnAddr := textVAddr + uint64(f.at)*4
		litAddr := textVAddr + uint64(f.poolIdx)*4
		delta := (int64(litAddr) - int64(insnAddr)) / 4
		if delta < -(1<<18) || delta >= 1<<18 {
			return nil, nil, fmt.Errorf("arm64: ldr-literal at insn %d is %d bytes from its pool — outside the ±1 MB imm19 range (missing .ltorg?)", f.at, delta*4)
		}
		imm19 := uint32(int32(delta))
		a.insns[f.at] |= (imm19 & 0x7ffff) << 5
	}

	symVAddr := func(name string) (uint64, bool) {
		// PIE self-relocation symbols (__ehdr_start / __rela_start /
		// __rela_end) are synthetic — not labels in the program — and
		// resolve to fixed base-relative addresses.
		if pieSyms != nil {
			if v, ok := pieSyms[name]; ok {
				return v, true
			}
		}
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
		if err := checkBranchRange(f.kind, target-f.at, f.label); err != nil {
			return nil, nil, err
		}
		off := uint32(target - f.at)
		switch f.kind {
		case branchImm26:
			a.insns[f.at] |= off & 0x03ffffff
		case branchImm19:
			a.insns[f.at] |= (off & 0x7ffff) << 5
		case branchImm14:
			a.insns[f.at] |= (off & 0x3fff) << 5
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

	// .quad <symbol> → absolute 8-byte virtual address in .rodata. In a
	// PIE this value is base-relative (textVAddr/rodataVAddr were laid out
	// from base 0) and the slot also gets an R_AARCH64_RELATIVE entry so
	// the loader adds the runtime base.
	for _, f := range a.quadSymFixups {
		sv, ok := symVAddr(f.label)
		if !ok {
			return nil, nil, fmt.Errorf("arm64: .quad of undefined symbol %q", f.label)
		}
		for i := 0; i < 8; i++ {
			a.rodata[f.at+i] = byte(sv >> (8 * i))
		}
		if relocs != nil {
			*relocs = append(*relocs, Reloc{Offset: rodataVAddr + uint64(f.at), Addend: sv})
		}
	}

	for _, insn := range a.insns {
		text = Put(text, insn)
	}
	return text, a.rodata, nil
}

// checkBranchRange validates a branch fixup's instruction-count offset
// against its immediate field width. Truncating silently (the previous
// behaviour) turns an over-long branch into a jump to an unrelated
// address — a miscompile that only shows up at driver/self-compile
// scale, where .text outgrows the ±1 MB imm19 span. Loud errors keep
// the assembler's "error, never miscompile" contract.
//
// The imm26 (b/bl) case is handled before it gets here on the layout
// paths: insertVeneers plants a trampoline for any call that outruns
// ±128 MB (see veneer.go), so an imm26 report from here means either
// the raw Bytes path — which has no virtual addresses and so cannot
// resolve a veneer's adrp — or a veneering bug.
func checkBranchRange(kind branchKind, offInsns int, label string) error {
	var bits uint
	switch kind {
	case branchImm26:
		bits = 26
	case branchImm19:
		bits = 19
	case branchImm14:
		bits = 14
	default:
		return nil
	}
	lim := int(1) << (bits - 1)
	if offInsns < -lim || offInsns >= lim {
		return fmt.Errorf("arm64: branch to %q spans %d instructions — outside the signed %d-bit range", label, offInsns, bits)
	}
	return nil
}
