// Package x86_64 is a pure-Go assembler for the subset of x86-64 the
// Fern code generator (internal/codegen/x86_64) emits, in Intel syntax
// (`.intel_syntax noprefix`). It is the x86-64 counterpart of
// internal/native/arm64: AssembleProgram turns the emitted `.s` text
// into a .text blob (plus .rodata) ready to drop into a static ELF-64
// executable via internal/native/elf.StaticExecutableDataX86 — no
// external assembler or linker.
//
// Phase 1 covers the integer / control-flow / call surface (enough to
// assemble and run recursion + arithmetic + comparisons end to end).
// SSE scalar floats, x87 transcendentals, string ops (rep movs/stos)
// and rip-relative data addressing are later phases; an unsupported
// instruction surfaces as a clear error rather than a miscompile.
package x86_64

import (
	"fmt"
	"strings"
)

// Operand kinds.
const (
	opReg = iota
	opImm
	opMem
	opLabel
)

type operand struct {
	kind int
	reg  int   // register number 0..15
	size int   // operand size in bits: 8, 16, 32, 64
	imm  int64 // immediate value
	// memory operand [base + disp] (no scaled index in phase 1):
	base    int // base register number, or -1
	disp    int64
	memSize int // access size in bits from a "qword ptr" prefix, or 0 if unspecified
	sym     string
}

type relFixup struct {
	at  int    // offset in text of the 4-byte rel32 field
	sym string // target text label
}

// Assembler accumulates encoded machine code and resolves text-label
// branch/call targets in a final pass.
type Assembler struct {
	text         []byte
	rodata       []byte
	textLabels   map[string]int
	rodataLabels map[string]int
	relFixups    []relFixup
}

func newAssembler() *Assembler {
	return &Assembler{
		textLabels:   map[string]int{},
		rodataLabels: map[string]int{},
	}
}

func (a *Assembler) emit(b ...byte) { a.text = append(a.text, b...) }

func (a *Assembler) emit32(v uint32) {
	a.emit(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AssembleProgram assembles the Intel-syntax program text into .text and
// .rodata blobs. textVAddr is where .text will be loaded (used to resolve
// branch targets); pass elf.TextVAddr.
func AssembleProgram(src string, textVAddr uint64) (text, rodata []byte, err error) {
	a := newAssembler()
	sec := "text"
	for lineno, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		// Peel any leading labels ("foo:" / ".Lx:").
		for {
			label, rest, ok := splitLabel(line)
			if !ok {
				break
			}
			if sec == "text" {
				a.textLabels[label] = len(a.text)
			} else if sec == "rodata" {
				a.rodataLabels[label] = len(a.rodata)
			}
			line = strings.TrimSpace(rest)
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			sec, err = a.directive(line, sec)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), err)
			}
			continue
		}
		if sec != "text" {
			return nil, nil, fmt.Errorf("line %d: %q: instruction outside .text", lineno+1, strings.TrimSpace(raw))
		}
		if err := a.insn(line); err != nil {
			return nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), err)
		}
	}
	// Resolve rel32 branch/call targets now that all labels are placed.
	for _, f := range a.relFixups {
		dst, ok := a.textLabels[f.sym]
		if !ok {
			return nil, nil, fmt.Errorf("undefined label %q", f.sym)
		}
		rel := int32(dst - (f.at + 4))
		a.text[f.at] = byte(rel)
		a.text[f.at+1] = byte(rel >> 8)
		a.text[f.at+2] = byte(rel >> 16)
		a.text[f.at+3] = byte(rel >> 24)
	}
	return a.text, a.rodata, nil
}

// directive handles section switches and the no-op metadata directives
// the code generator emits. Returns the section in effect afterwards.
func (a *Assembler) directive(line, sec string) (string, error) {
	fields := strings.Fields(line)
	switch fields[0] {
	case ".text":
		return "text", nil
	case ".intel_syntax", ".globl", ".global", ".type", ".size", ".p2align", ".align", ".balign", ".file", ".ident":
		return sec, nil
	case ".section":
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch {
		case strings.Contains(arg, ".text"):
			return "text", nil
		case strings.Contains(arg, ".rodata"):
			return "rodata", nil
		default:
			return "ignore", nil // e.g. .note.GNU-stack
		}
	case ".rodata":
		return "rodata", nil
	}
	if sec == "ignore" {
		return "ignore", nil
	}
	return sec, fmt.Errorf("unsupported directive %q", fields[0])
}

// insn parses and encodes one .text instruction.
func (a *Assembler) insn(line string) error {
	mnem, rest := splitMnemonic(line)
	opStrs := splitOperands(rest)
	ops := make([]operand, len(opStrs))
	for i, s := range opStrs {
		o, err := parseOperand(s)
		if err != nil {
			return err
		}
		ops[i] = o
	}
	switch mnem {
	case "ret":
		a.emit(0xC3)
		return nil
	case "syscall":
		a.emit(0x0F, 0x05)
		return nil
	case "cdq":
		a.emit(0x99)
		return nil
	case "cqo":
		a.emit(0x48, 0x99)
		return nil
	case "cld":
		a.emit(0xFC)
		return nil
	case "push":
		return a.pushPop(ops, 0x50)
	case "pop":
		return a.pushPop(ops, 0x58)
	case "mov":
		return a.mov(ops, false)
	case "movabs":
		return a.mov(ops, true)
	case "add":
		return a.alu(ops, 0x00, 0)
	case "or":
		return a.alu(ops, 0x08, 1)
	case "and":
		return a.alu(ops, 0x20, 4)
	case "sub":
		return a.alu(ops, 0x28, 5)
	case "xor":
		return a.alu(ops, 0x30, 6)
	case "cmp":
		return a.alu(ops, 0x38, 7)
	case "test":
		return a.test(ops)
	case "imul":
		return a.imul(ops)
	case "idiv":
		return a.unaryF7(ops, 7)
	case "div":
		return a.unaryF7(ops, 6)
	case "neg":
		return a.unaryF7(ops, 3)
	case "inc":
		return a.incDec(ops, 0)
	case "dec":
		return a.incDec(ops, 1)
	case "sar":
		return a.shift(ops, 7)
	case "shl":
		return a.shift(ops, 4)
	case "shr":
		return a.shift(ops, 5)
	case "lea":
		return a.lea(ops)
	case "movzx":
		return a.movzx(ops, false)
	case "movsx", "movsxd":
		return a.movzx(ops, true)
	case "xchg":
		return a.xchg(ops)
	case "jmp":
		return a.jmp(ops)
	case "call":
		return a.call(ops)
	}
	if cc, ok := jccCode(mnem); ok {
		return a.jcc(ops, cc)
	}
	if cc, ok := setccCode(mnem); ok {
		return a.setcc(ops, cc)
	}
	return fmt.Errorf("unsupported instruction %q", mnem)
}

// rexFor returns the REX prefix byte (or 0 to omit it) for an instruction
// with the given 64-bit-operand flag, ModRM.reg field, and ModRM.rm /
// SIB.base register. needB8 forces REX when an 8-bit operand uses one of
// spl/bpl/sil/dil (regs 4..7), which require REX to address.
func rexFor(w bool, reg, rm int, needB8 bool) byte {
	var r byte
	if w {
		r |= 0x08
	}
	if reg >= 8 {
		r |= 0x04
	}
	if rm >= 8 {
		r |= 0x01
	}
	if r != 0 || needB8 {
		return 0x40 | r
	}
	return 0
}

// modrmReg encodes a register-direct ModRM byte (mod=11).
func modrmReg(reg, rm int) byte {
	return 0xC0 | byte((reg&7)<<3) | byte(rm&7)
}

// encodeMem appends the ModRM byte (and SIB / displacement as needed) for
// a memory operand addressed as [base + disp], with the given ModRM.reg
// field value.
func (a *Assembler) encodeMem(regField int, m operand) {
	base := m.base
	mod, dispBytes := memMod(m)
	switch base & 7 {
	case 4: // rsp / r12: ModRM.rm=100 means a SIB byte follows
		a.emit(byte(mod<<6) | byte((regField&7)<<3) | 4)
		a.emit(0x24) // SIB: scale=0, index=100 (none), base=rsp/r12
	default:
		a.emit(byte(mod<<6) | byte((regField&7)<<3) | byte(base&7))
	}
	switch dispBytes {
	case 1:
		a.emit(byte(m.disp))
	case 4:
		a.emit32(uint32(m.disp))
	}
}

// memMod picks the ModRM mod field and displacement width for [base+disp].
// rbp/r13 have no disp-less form, so they always carry at least a disp8.
func memMod(m operand) (mod, dispBytes int) {
	rbpLike := (m.base & 7) == 5
	if m.disp == 0 && !rbpLike {
		return 0, 0
	}
	if m.disp >= -128 && m.disp <= 127 {
		return 1, 1
	}
	return 2, 4
}

func (a *Assembler) pushPop(ops []operand, base byte) error {
	if len(ops) != 1 || ops[0].kind != opReg {
		return fmt.Errorf("push/pop expects one register")
	}
	r := ops[0].reg
	if r >= 8 {
		a.emit(0x41) // REX.B
	}
	a.emit(base + byte(r&7))
	return nil
}

func (a *Assembler) mov(ops []operand, abs bool) error {
	if len(ops) != 2 {
		return fmt.Errorf("mov expects two operands")
	}
	dst, src := ops[0], ops[1]
	switch {
	case dst.kind == opReg && src.kind == opImm:
		if abs || dst.size == 64 && !fitsInt32(src.imm) {
			// movabs r64, imm64
			if rex := rexFor(true, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xB8 + byte(dst.reg&7))
			u := uint64(src.imm)
			for i := 0; i < 8; i++ {
				a.emit(byte(u >> (8 * i)))
			}
			return nil
		}
		if dst.size == 64 {
			// mov r/m64, imm32 (sign-extended): REX.W C7 /0 id
			a.emit(rexFor(true, 0, dst.reg, false))
			a.emit(0xC7)
			a.emit(modrmReg(0, dst.reg))
			a.emit32(uint32(src.imm))
			return nil
		}
		// mov r32, imm32 : B8+rd id
		if rex := rexFor(false, 0, dst.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xB8 + byte(dst.reg&7))
		a.emit32(uint32(src.imm))
		return nil
	case dst.kind == opReg && src.kind == opReg:
		return a.rmReg(0x88, dst, src) // MR form: r/m, r
	case dst.kind == opMem && src.kind == opReg:
		return a.memReg(0x88, src, dst)
	case dst.kind == opReg && src.kind == opMem:
		return a.regMem(0x8A, dst, src)
	case dst.kind == opMem && src.kind == opImm:
		w := dst.memSize == 64
		if w {
			a.emit(rexFor(true, 0, dst.base, false))
		} else if rex := rexFor(false, 0, dst.base, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xC7)
		a.encodeMem(0, dst)
		a.emit32(uint32(dst.imm))
		return nil
	}
	return fmt.Errorf("unsupported mov form")
}

// rmReg encodes "op r/m, r" (MR) for register-direct operands. opBase is
// the 8-bit opcode; the 32/64-bit opcode is opBase|1.
func (a *Assembler) rmReg(opBase byte, rm, reg operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if rex := rexFor(w, reg.reg, rm.reg, byteRegNeedsRex(reg.size, reg.reg, rm.reg)); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.emit(modrmReg(reg.reg, rm.reg))
	return nil
}

func (a *Assembler) memReg(opBase byte, reg, mem operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if rex := rexFor(w, reg.reg, mem.base, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.encodeMem(reg.reg, mem)
	return nil
}

func (a *Assembler) regMem(opBase byte, reg, mem operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if rex := rexFor(w, reg.reg, mem.base, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.encodeMem(reg.reg, mem)
	return nil
}

// alu encodes add/or/and/sub/xor/cmp. opBase is the family's MR 8-bit
// opcode (add=0x00, …); ext is the /digit for the imm forms.
func (a *Assembler) alu(ops []operand, opBase byte, ext int) error {
	if len(ops) != 2 {
		return fmt.Errorf("binary op expects two operands")
	}
	dst, src := ops[0], ops[1]
	switch {
	case dst.kind == opReg && src.kind == opReg:
		return a.rmReg(opBase, dst, src)
	case dst.kind == opMem && src.kind == opReg:
		return a.memReg(opBase, src, dst)
	case dst.kind == opReg && src.kind == opMem:
		return a.regMem(opBase|0x02, dst, src)
	case dst.kind == opReg && src.kind == opImm:
		w := dst.size == 64
		if fitsInt8(src.imm) && dst.size != 8 {
			if rex := rexFor(w, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x83)
			a.emit(modrmReg(ext, dst.reg))
			a.emit(byte(src.imm))
			return nil
		}
		if rex := rexFor(w, 0, dst.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x81)
		a.emit(modrmReg(ext, dst.reg))
		a.emit32(uint32(src.imm))
		return nil
	case dst.kind == opMem && src.kind == opImm:
		w := dst.memSize == 64
		if fitsInt8(src.imm) {
			if rex := rexFor(w, 0, dst.base, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x83)
			a.encodeMem(ext, dst)
			a.emit(byte(src.imm))
			return nil
		}
		if rex := rexFor(w, 0, dst.base, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x81)
		a.encodeMem(ext, dst)
		a.emit32(uint32(dst.imm))
		return nil
	}
	return fmt.Errorf("unsupported binary-op form")
}

func (a *Assembler) test(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opReg {
		return fmt.Errorf("test expects reg, reg")
	}
	return a.rmReg(0x84, ops[0], ops[1])
}

func (a *Assembler) imul(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("imul expects reg, r/m")
	}
	dst := ops[0]
	w := dst.size == 64
	if ops[1].kind == opReg {
		if rex := rexFor(w, dst.reg, ops[1].reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0xAF)
		a.emit(modrmReg(dst.reg, ops[1].reg))
		return nil
	}
	if ops[1].kind == opMem {
		if rex := rexFor(w, dst.reg, ops[1].base, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0xAF)
		a.encodeMem(dst.reg, ops[1])
		return nil
	}
	return fmt.Errorf("unsupported imul form")
}

// unaryF7 encodes the F7-group one-operand ops (idiv /7, div /6, neg /3).
func (a *Assembler) unaryF7(ops []operand, ext int) error {
	if len(ops) != 1 {
		return fmt.Errorf("unary op expects one operand")
	}
	o := ops[0]
	if o.kind == opReg {
		w := o.size == 64
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.emit(modrmReg(ext, o.reg))
		return nil
	}
	if o.kind == opMem {
		w := o.memSize == 64
		if rex := rexFor(w, 0, o.base, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.encodeMem(ext, o)
		return nil
	}
	return fmt.Errorf("unsupported unary-op form")
}

func (a *Assembler) incDec(ops []operand, ext int) error {
	if len(ops) != 1 || ops[0].kind != opReg {
		return fmt.Errorf("inc/dec expects one register")
	}
	o := ops[0]
	w := o.size == 64
	if rex := rexFor(w, 0, o.reg, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0xFF)
	a.emit(modrmReg(ext, o.reg))
	return nil
}

func (a *Assembler) shift(ops []operand, ext int) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("shift expects reg, imm/cl")
	}
	o := ops[0]
	w := o.size == 64
	if ops[1].kind == opImm {
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		if ops[1].imm == 1 {
			a.emit(0xD1)
			a.emit(modrmReg(ext, o.reg))
			return nil
		}
		a.emit(0xC1)
		a.emit(modrmReg(ext, o.reg))
		a.emit(byte(ops[1].imm))
		return nil
	}
	if ops[1].kind == opReg && ops[1].reg == 1 { // cl
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xD3)
		a.emit(modrmReg(ext, o.reg))
		return nil
	}
	return fmt.Errorf("shift count must be an immediate or cl")
}

func (a *Assembler) lea(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opMem {
		return fmt.Errorf("lea expects reg, mem")
	}
	if ops[1].sym != "" {
		return fmt.Errorf("rip-relative lea (data addressing) is a later phase")
	}
	dst := ops[0]
	w := dst.size == 64
	if rex := rexFor(w, dst.reg, ops[1].base, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x8D)
	a.encodeMem(dst.reg, ops[1])
	return nil
}

// movzx / movsx from an 8- or 16-bit source (and movsxd from 32-bit).
func (a *Assembler) movzx(ops []operand, signed bool) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opReg {
		return fmt.Errorf("movzx/movsx expects reg, reg")
	}
	dst, src := ops[0], ops[1]
	w := dst.size == 64
	if src.size == 32 && signed { // movsxd r64, r/m32
		a.emit(rexFor(true, dst.reg, src.reg, false))
		a.emit(0x63)
		a.emit(modrmReg(dst.reg, src.reg))
		return nil
	}
	var op2 byte
	switch {
	case src.size == 8 && !signed:
		op2 = 0xB6
	case src.size == 16 && !signed:
		op2 = 0xB7
	case src.size == 8 && signed:
		op2 = 0xBE
	case src.size == 16 && signed:
		op2 = 0xBF
	default:
		return fmt.Errorf("unsupported movzx/movsx widths")
	}
	if rex := rexFor(w, dst.reg, src.reg, byteRegNeedsRex(src.size, dst.reg, src.reg)); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, op2)
	a.emit(modrmReg(dst.reg, src.reg))
	return nil
}

func (a *Assembler) xchg(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opReg {
		return fmt.Errorf("xchg expects reg, reg")
	}
	return a.rmReg(0x86, ops[0], ops[1])
}

func (a *Assembler) setcc(ops []operand, cc byte) error {
	if len(ops) != 1 || ops[0].kind != opReg || ops[0].size != 8 {
		return fmt.Errorf("setcc expects an 8-bit register")
	}
	r := ops[0].reg
	if rex := rexFor(false, 0, r, r >= 4); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, 0x90+cc)
	a.emit(modrmReg(0, r))
	return nil
}

func (a *Assembler) jmp(ops []operand) error {
	if len(ops) != 1 || ops[0].kind != opLabel {
		return fmt.Errorf("jmp expects a label")
	}
	a.emit(0xE9)
	a.relFixups = append(a.relFixups, relFixup{at: len(a.text), sym: ops[0].sym})
	a.emit32(0)
	return nil
}

func (a *Assembler) call(ops []operand) error {
	if len(ops) != 1 || ops[0].kind != opLabel {
		return fmt.Errorf("call expects a label")
	}
	a.emit(0xE8)
	a.relFixups = append(a.relFixups, relFixup{at: len(a.text), sym: ops[0].sym})
	a.emit32(0)
	return nil
}

func (a *Assembler) jcc(ops []operand, cc byte) error {
	if len(ops) != 1 || ops[0].kind != opLabel {
		return fmt.Errorf("jcc expects a label")
	}
	a.emit(0x0F, 0x80+cc)
	a.relFixups = append(a.relFixups, relFixup{at: len(a.text), sym: ops[0].sym})
	a.emit32(0)
	return nil
}

// byteRegNeedsRex reports whether an 8-bit operand forces a REX prefix
// (to reach spl/bpl/sil/dil instead of ah/ch/dh/bh).
func byteRegNeedsRex(size, reg, rm int) bool {
	return size == 8 && (reg >= 4 && reg <= 7 || rm >= 4 && rm <= 7)
}

func fitsInt8(v int64) bool  { return v >= -128 && v <= 127 }
func fitsInt32(v int64) bool { return v >= -2147483648 && v <= 2147483647 }
