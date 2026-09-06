package x86_64

import (
	"fmt"
	"strconv"
	"strings"
)

// Prefix is the instruction prefix an Inst carries: none, a string-op
// repeat, or lock.
type Prefix uint8

const (
	PrefixNone Prefix = iota
	PrefixRep
	PrefixRepne
	PrefixLock
)

// Inst is one .text instruction as a value: the mnemonic the dispatch
// switch names, its operands, and an optional prefix. It is what
// ParseInst produces from a line of Intel-syntax text and what a code
// generator can hand to Assembler.Inst directly, skipping the text.
type Inst struct {
	Mnem   string
	Prefix Prefix
	Ops    []Operand
}

// Reg is a general-purpose register operand: num is the hardware number
// (0..15), size the operand width in bits (8, 16, 32 or 64).
func Reg(num, size int) Operand {
	return Operand{kind: opReg, reg: num, size: size}
}

// HighByte is one of ah/ch/dh/bh: register number 4..7 as an 8-bit
// operand that must not carry a REX prefix.
func HighByte(num int) Operand {
	return Operand{kind: opReg, reg: num, size: 8, highByte: true}
}

// Xmm is the SSE register xmmN.
func Xmm(n int) Operand {
	return Operand{kind: opReg, reg: n, size: 128}
}

// RegNamed resolves a register by its spelling ("rax", "r8d", "xmm3");
// ok is false for anything else.
func RegNamed(name string) (Operand, bool) {
	o, err := parseOperand(name)
	if err != nil || o.kind != opReg {
		return Operand{}, false
	}
	return o, true
}

// Imm is an immediate operand.
func Imm(v int64) Operand {
	return Operand{kind: opImm, imm: v}
}

// Mem is a [base + index*scale + disp] memory operand. A register that is
// absent is -1; scale is 1, 2, 4 or 8; size is the access width in bits a
// `qword ptr` prefix would give, or 0 when the other operand decides it.
func Mem(base, index, scale int, disp int64, size int) Operand {
	if index < 0 {
		scale = 1
	}
	return Operand{kind: opMem, base: base, index: index, scale: scale, disp: disp, memSize: size}
}

// RIPRel is a [rip + sym + addend] memory operand.
func RIPRel(sym string, addend int64, size int) Operand {
	return Operand{kind: opMem, base: -1, index: -1, scale: 1, sym: sym, disp: addend, memSize: size}
}

// Sym is a label operand, the target of a jmp or call.
func Sym(name string) Operand {
	return Operand{kind: opLabel, sym: name}
}

// ParseInst parses one instruction line — a mnemonic with its operands,
// behind an optional rep/lock prefix — into an Inst. Labels and
// directives are not instructions; ParseProgram peels them first.
func ParseInst(line string) (Inst, error) {
	mnem, rest := splitMnemonic(line)
	var in Inst
	switch mnem {
	case "rep", "repe", "repz":
		in.Prefix = PrefixRep
		mnem, rest = splitMnemonic(rest)
	case "repne", "repnz":
		in.Prefix = PrefixRepne
		mnem, rest = splitMnemonic(rest)
	case "lock":
		in.Prefix = PrefixLock
		mnem, rest = splitMnemonic(rest)
	}
	in.Mnem = mnem
	if rest != "" {
		opStrs := splitOperands(rest)
		in.Ops = make([]Operand, len(opStrs))
		for i, s := range opStrs {
			o, err := parseOperand(s)
			if err != nil {
				return Inst{}, err
			}
			in.Ops[i] = o
		}
	}
	return in, nil
}

// String renders the instruction back to the Intel-syntax line ParseInst
// reads, so a program built from Inst values can be written out as text.
func (in Inst) String() string {
	var b strings.Builder
	switch in.Prefix {
	case PrefixRep:
		b.WriteString("rep ")
	case PrefixRepne:
		b.WriteString("repne ")
	case PrefixLock:
		b.WriteString("lock ")
	}
	b.WriteString(in.Mnem)
	for i, o := range in.Ops {
		if i == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteString(", ")
		}
		b.WriteString(o.String())
	}
	return b.String()
}

var regNames = map[int][]string{
	64:  {"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"},
	32:  {"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"},
	16:  {"ax", "cx", "dx", "bx", "sp", "bp", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"},
	8:   {"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"},
	128: {"xmm0", "xmm1", "xmm2", "xmm3", "xmm4", "xmm5", "xmm6", "xmm7", "xmm8", "xmm9", "xmm10", "xmm11", "xmm12", "xmm13", "xmm14", "xmm15"},
	256: {"ymm0", "ymm1", "ymm2", "ymm3", "ymm4", "ymm5", "ymm6", "ymm7", "ymm8", "ymm9", "ymm10", "ymm11", "ymm12", "ymm13", "ymm14", "ymm15"},
}

var highByteNames = []string{"ah", "ch", "dh", "bh"}

// String renders the operand in the Intel syntax parseOperand reads.
func (o Operand) String() string {
	switch o.kind {
	case opReg:
		if o.highByte {
			return highByteNames[o.reg-4]
		}
		return regNames[o.size][o.reg]
	case opImm:
		return strconv.FormatInt(o.imm, 10)
	case opLabel:
		return o.sym
	case opMem:
		var b strings.Builder
		switch o.memSize {
		case 8:
			b.WriteString("byte ptr ")
		case 16:
			b.WriteString("word ptr ")
		case 32:
			b.WriteString("dword ptr ")
		case 64:
			b.WriteString("qword ptr ")
		case 128:
			b.WriteString("xmmword ptr ")
		}
		b.WriteByte('[')
		terms := 0
		term := func(s string) {
			if terms > 0 {
				b.WriteString(" + ")
			}
			b.WriteString(s)
			terms++
		}
		if o.sym != "" {
			term("rip")
			term(o.sym)
		}
		if o.base >= 0 {
			term(regNames[64][o.base])
		}
		if o.index >= 0 {
			term(regNames[64][o.index] + "*" + strconv.Itoa(o.scale))
		}
		if o.disp != 0 || terms == 0 {
			switch {
			case terms == 0:
				b.WriteString(strconv.FormatInt(o.disp, 10))
			case o.disp < 0:
				b.WriteString(" - " + strconv.FormatInt(-o.disp, 10))
			default:
				b.WriteString(" + " + strconv.FormatInt(o.disp, 10))
			}
		}
		b.WriteByte(']')
		return b.String()
	}
	return fmt.Sprintf("<operand kind %d>", o.kind)
}
