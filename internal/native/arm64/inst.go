package arm64

import (
	"fmt"
	"strconv"
	"strings"
)

// OpKind names what an Operand holds. The set is closed: every operand
// spelling the dispatch's arms accept parses to exactly one of these, and
// TestInstRoundTripsThroughTheModel holds that over the gas-pinned corpus.
type OpKind uint8

const (
	// OpUnknown is the zero value and never appears in a parsed Inst: an
	// operand that matches no kind is a parse error, not this.
	OpUnknown OpKind = iota
	OpReg            // x0, w3, sp, xzr, lr, fp
	OpFPReg          // b0, h1, s2, d3, q4 — a scalar SIMD register
	OpVecReg         // v0.16b — a vector register with an arrangement
	OpVecLane        // v0.b[3] — one lane of a vector register
	OpVecList        // {v0.16b, v1.16b} — a table/structure register list
	OpImm            // #42, #0x10, #96 + 48
	OpFPImm          // #1.0 — an fmov immediate
	OpShift          // lsl #3 — a shift, always its own operand
	OpExtend         // uxtw #2 — an extend, always its own operand
	OpMem            // [x0], [x0, #8], [x0, #8]!, [x0, x1, lsl #3]
	OpCond           // eq, ne, hs, … — a condition operand
	OpLabel          // a symbol: the target of b/bl/adrp/cbz
	OpSysReg         // the mrs/msr system-register name
	OpBarrier        // sy, ish, ishst, … — the dmb/dsb/isb option
)

// Operand is one arm64 instruction operand as a value.
//
// Fields are unexported and read through accessors so the representation
// can change under the arms as families migrate onto it. `text` is the
// operand exactly as it was written; the arms that still parse strings
// read it, and it goes when the last of them stops. It is a slice of the
// source line, so carrying it costs no allocation.
type Operand struct {
	kind OpKind
	text string

	reg    uint32 // register number 0..31, or the vector/FP register number
	is32   bool   // w-register rather than x
	isSP   bool   // 31 spelled sp rather than xzr/wzr
	fpCls  uint32 // OpFPReg: 0=b 1=h 2=s 3=d 4=q
	arr    vecArr // OpVecReg / OpVecList: the arrangement
	lane   uint32 // OpVecLane: the lane index
	imm    int64  // OpImm: the value
	fpImm  float64
	shift  uint32   // OpShift: 0=lsl 1=lsr 2=asr 3=ror
	ext    uint32   // OpExtend: the extend option 0..7
	amt    uint32   // OpShift / OpExtend: the amount
	hasAmt bool     // an explicit "#amt" was written
	regs   []uint32 // OpVecList: the register numbers
	mem    memOperand
	cond   uint32
	sym    string
}

// Kind reports which operand form this is.
func (o Operand) Kind() OpKind { return o.kind }

// Text is the operand as it was spelled. Migration scaffolding: the arms
// that have not moved to the typed accessors read this, and it is
// removed with the last of them (#8510).
func (o Operand) Text() string { return o.text }

// RegNum is the register number for OpReg, OpFPReg, OpVecReg and
// OpVecLane.
func (o Operand) RegNum() uint32 { return o.reg }

// Is32 reports a w-register.
func (o Operand) Is32() bool { return o.is32 }

// IsSP reports register 31 spelled `sp` rather than `xzr`/`wzr`. The two
// are the same number and different operands.
func (o Operand) IsSP() bool { return o.isSP }

// Imm is the value of an OpImm.
func (o Operand) Imm() int64 { return o.imm }

// Sym is the symbol of an OpLabel, or the name of an OpSysReg / OpBarrier.
func (o Operand) Sym() string { return o.sym }

// Cond is the condition code of an OpCond.
func (o Operand) Cond() uint32 { return o.cond }

// Arr is the arrangement of an OpVecReg or OpVecList.
func (o Operand) Arr() vecArr { return o.arr }

// Mem is the addressing form of an OpMem.
func (o Operand) Mem() memOperand { return o.mem }

// Shift reports the shift type and amount of an OpShift.
func (o Operand) Shift() (uint32, uint32) { return o.shift, o.amt }

// Extend reports the option and amount of an OpExtend.
func (o Operand) Extend() (uint32, uint32) { return o.ext, o.amt }

// HasAmt reports whether an OpShift / OpExtend was written with an
// explicit `#amt`. A bare `uxtw` is a different operand from `uxtw #0`
// for byte accesses, where the amount's presence sets S.
func (o Operand) HasAmt() bool { return o.hasAmt }

// Reg is a general-purpose register operand. Number 31 is the zero
// register; RegSP names the stack pointer, which shares that number and
// is a different operand.
func Reg(num uint32, is32 bool) Operand {
	return Operand{kind: OpReg, reg: num, is32: is32, text: regSpelling(num, is32, false)}
}

// RegSP is the stack pointer: register 31 read as sp rather than as the
// zero register.
func RegSP() Operand {
	return Operand{kind: OpReg, reg: 31, isSP: true, text: "sp"}
}

// Imm is an immediate operand.
func Imm(v int64) Operand {
	return Operand{kind: OpImm, imm: v, text: "#" + strconv.FormatInt(v, 10)}
}

// Sym is a branch target or relocation symbol.
func Sym(name string) Operand {
	return Operand{kind: OpLabel, sym: name, text: name}
}

// Cond is a condition operand named by its gas spelling ("eq", "hs", …);
// ok is false for anything else.
func Cond(name string) (Operand, bool) {
	c, err := condOperand(name, false)
	if err != nil {
		return Operand{}, false
	}
	return Operand{kind: OpCond, cond: c, sym: name, text: name}, true
}

// Shift is a shift operand ("lsl", "lsr", "asr", "ror") with its amount;
// ok is false for any other kind name.
func Shift(kind string, amt uint32) (Operand, bool) {
	st, ok := shiftNames[kind]
	if !ok {
		return Operand{}, false
	}
	o := Operand{kind: OpShift, shift: st, amt: amt, hasAmt: true}
	o.text = o.String()
	return o, true
}

// Extend is an extend operand ("uxtb" … "sxtx") with its amount; ok is
// false for any other kind name.
func Extend(kind string, amt uint32) (Operand, bool) {
	opt, ok := extendNames[kind]
	if !ok {
		return Operand{}, false
	}
	o := Operand{kind: OpExtend, ext: opt, amt: amt, hasAmt: true}
	o.text = o.String()
	return o, true
}

// Mem is the `[Xn, #off]` addressing form. pre selects the pre-index
// writeback spelling `[Xn, #off]!`.
func Mem(base uint32, off int64, pre bool) Operand {
	o := Operand{kind: OpMem, mem: memOperand{base: base, off: off, pre: pre, indexOpt: 3}}
	o.text = o.mem.String()
	return o
}

// MemIndex is the register-offset form `[Xn, Xm{, lsl #amt}]`. GNU as
// takes only `lsl` for a 64-bit index here, so that is what this builds;
// the extending forms go through ParseOperand.
func MemIndex(base, index uint32, amt uint32, hasAmt bool) Operand {
	o := Operand{kind: OpMem, mem: memOperand{
		base: base, index: index, hasIndex: true,
		indexOpt: 3, indexAmt: amt, indexHasAmt: hasAmt,
	}}
	o.text = o.mem.String()
	return o
}

// Inst is one .text instruction as a value: the mnemonic the dispatch
// names and its operands. It is what ParseInst produces from a line of
// assembly and what a code generator can build directly, skipping the
// text.
type Inst struct {
	Mnem string
	Ops  []Operand
}

// ParseInst parses one instruction line into an Inst. Labels and
// directives are not instructions; the assemble loop peels them first.
func ParseInst(line string) (Inst, error) {
	mnem, rest := splitMnemonic(strings.TrimSpace(line))
	in := Inst{Mnem: mnem}
	for _, s := range splitOperands(rest) {
		o, err := ParseOperand(s)
		if err != nil {
			return Inst{}, err
		}
		in.Ops = append(in.Ops, o)
	}
	return in, nil
}

// String renders the instruction back to the line ParseInst reads.
func (in Inst) String() string {
	var b strings.Builder
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

// ParseOperand classifies and parses one operand.
//
// Order matters where spellings overlap. A bare `sp` is a register, not
// the `sy`-family barrier; `lsl #12` with no register is a shift rather
// than a malformed shifted register; and a condition name is only
// reachable after the register forms have declined, because `hs` and
// `s0` differ only in that one names a register file.
func ParseOperand(s string) (Operand, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Operand{}, fmt.Errorf("empty operand")
	}
	o := Operand{text: s}

	if strings.HasPrefix(s, "[") {
		m, err := parseMem(s)
		if err != nil {
			return Operand{}, err
		}
		o.kind, o.mem = OpMem, m
		return o, nil
	}
	if strings.HasPrefix(s, "{") {
		regs, t, err := parseVecList("", s)
		if err != nil {
			return Operand{}, err
		}
		o.kind, o.regs, o.arr = OpVecList, regs, t
		return o, nil
	}
	if strings.HasPrefix(s, "#") {
		body := strings.TrimSpace(strings.TrimPrefix(s, "#"))
		if strings.ContainsAny(body, ".eE") && !strings.HasPrefix(body, "0x") {
			if f, err := strconv.ParseFloat(body, 64); err == nil {
				o.kind, o.fpImm = OpFPImm, f
				return o, nil
			}
		}
		v, err := parseImm(s)
		if err != nil {
			return Operand{}, err
		}
		o.kind, o.imm = OpImm, v
		return o, nil
	}

	// splitOperands cuts on commas outside brackets, so a shift or extend
	// written after a register (`add x0, x1, x2, lsl #3`) arrives here as
	// its own operand — never glued to the register it modifies. The only
	// multi-token operands are therefore the modifiers themselves.
	if f := strings.Fields(s); len(f) >= 2 {
		return parseModifier(o, f)
	}
	if st, ok := shiftNames[s]; ok {
		o.kind, o.shift = OpShift, st
		return o, nil
	}
	if opt, ok := extendNames[s]; ok {
		o.kind, o.ext = OpExtend, opt
		return o, nil
	}

	if isVecArrOperand(s) {
		if reg, size, lane, err := parseVecLane(s); err == nil {
			o.kind, o.reg, o.arr, o.lane = OpVecLane, reg, vecArr{size: size}, lane
			return o, nil
		}
		reg, t, err := parseVecArr(s)
		if err != nil {
			return Operand{}, err
		}
		o.kind, o.reg, o.arr = OpVecReg, reg, t
		return o, nil
	}
	if cls, n, err := parseScalarReg(s); err == nil {
		o.kind, o.fpCls, o.reg = OpFPReg, cls, n
		return o, nil
	}
	if strings.HasPrefix(s, "q") {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 0 && n <= 31 {
			o.kind, o.fpCls, o.reg = OpFPReg, 4, uint32(n)
			return o, nil
		}
	}
	if reg, err := parseReg(s); err == nil {
		o.kind, o.reg = OpReg, reg
		o.is32 = is32(s)
		o.isSP = s == "sp"
		return o, nil
	}
	if c, err := condOperand(s, false); err == nil {
		// The spelling is kept as well as the code: hs/cs and lo/cc are
		// one code each, so the number alone cannot render back what was
		// written.
		o.kind, o.cond, o.sym = OpCond, c, s
		return o, nil
	}
	if barrierOptions[s] {
		o.kind, o.sym = OpBarrier, s
		return o, nil
	}
	if isLabelRef(s) {
		o.kind, o.sym = OpLabel, s
		return o, nil
	}
	return Operand{}, fmt.Errorf("unrecognised operand %q", s)
}

// parseModifier parses a shift or extend written with an amount — `lsl
// #3`, `uxtw #2`. The amount is optional in the grammar but present
// whenever there is a second token.
func parseModifier(o Operand, f []string) (Operand, error) {
	st, isShift := shiftNames[f[0]]
	opt, isExtend := extendNames[f[0]]
	if !isShift && !isExtend {
		return Operand{}, fmt.Errorf("unrecognised operand %q", o.text)
	}
	if len(f) != 2 {
		return Operand{}, fmt.Errorf("malformed shift or extend %q", o.text)
	}
	n, err := parseImm(f[1])
	if err != nil {
		return Operand{}, err
	}
	if n < 0 {
		return Operand{}, fmt.Errorf("negative shift amount %q", o.text)
	}
	o.amt, o.hasAmt = uint32(n), true
	if isShift {
		o.kind, o.shift = OpShift, st
	} else {
		o.kind, o.ext = OpExtend, opt
	}
	return o, nil
}

var shiftNames = map[string]uint32{"lsl": 0, "lsr": 1, "asr": 2, "ror": 3}

var shiftSpellings = [4]string{"lsl", "lsr", "asr", "ror"}

var extendNames = map[string]uint32{
	"uxtb": 0, "uxth": 1, "uxtw": 2, "uxtx": 3,
	"sxtb": 4, "sxth": 5, "sxtw": 6, "sxtx": 7,
}

var extendSpellings = [8]string{"uxtb", "uxth", "uxtw", "uxtx", "sxtb", "sxth", "sxtw", "sxtx"}

// barrierOptions is the dmb/dsb operand vocabulary. `isb` takes `sy`
// alone, which this set covers.
var barrierOptions = map[string]bool{
	"sy": true, "st": true, "ld": true,
	"ish": true, "ishst": true, "ishld": true,
	"nsh": true, "nshst": true, "nshld": true,
	"osh": true, "oshst": true, "oshld": true,
}

var fpClassSpellings = [5]byte{'b', 'h', 's', 'd', 'q'}

// String renders the operand in the syntax ParseOperand reads.
func (o Operand) String() string {
	switch o.kind {
	case OpReg:
		return regSpelling(o.reg, o.is32, o.isSP)
	case OpFPReg:
		return string(fpClassSpellings[o.fpCls]) + strconv.FormatUint(uint64(o.reg), 10)
	case OpVecReg:
		return "v" + strconv.FormatUint(uint64(o.reg), 10) + "." + o.arr.String()
	case OpVecLane:
		return "v" + strconv.FormatUint(uint64(o.reg), 10) + "." +
			string("bhsd"[o.arr.size]) + "[" + strconv.FormatUint(uint64(o.lane), 10) + "]"
	case OpVecList:
		parts := make([]string, len(o.regs))
		for i, r := range o.regs {
			parts[i] = "v" + strconv.FormatUint(uint64(r), 10) + "." + o.arr.String()
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case OpImm:
		return "#" + strconv.FormatInt(o.imm, 10)
	case OpFPImm:
		return "#" + strconv.FormatFloat(o.fpImm, 'g', -1, 64)
	case OpShift:
		return modifierSpelling(shiftSpellings[o.shift], o.amt, o.hasAmt)
	case OpExtend:
		return modifierSpelling(extendSpellings[o.ext], o.amt, o.hasAmt)
	case OpMem:
		return o.mem.String()
	case OpCond:
		return o.sym
	case OpLabel, OpSysReg, OpBarrier:
		return o.sym
	}
	return fmt.Sprintf("<operand kind %d>", o.kind)
}

// modifierSpelling renders a shift or extend with its amount, keeping a
// bare modifier bare: `uxtw` and `uxtw #0` are the same shift by zero and
// different operands, because for a byte access the amount's presence is
// what sets S.
func modifierSpelling(name string, amt uint32, hasAmt bool) string {
	if !hasAmt {
		return name
	}
	return name + " #" + strconv.FormatUint(uint64(amt), 10)
}

// isLabelRef reports whether the token can name a branch target.
//
// Beyond an identifier: a numeric local label reference (`1f` / `2b`, the
// gas forward/backward form), and any token carrying the `.`, `$` or `@`
// a mangled symbol or a Mach-O `sym@PAGE` relocation suffix uses.
func isLabelRef(s string) bool {
	if isIdent(s) || strings.ContainsAny(s, ".$@:") {
		return true
	}
	if len(s) >= 2 && (s[len(s)-1] == 'f' || s[len(s)-1] == 'b') {
		for i := 0; i < len(s)-1; i++ {
			if s[i] < '0' || s[i] > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// regSpelling names a GPR. Register 31 is two different operands
// depending on how it was written, so the flag decides rather than the
// number.
func regSpelling(n uint32, is32, isSP bool) string {
	if n == 31 {
		switch {
		case isSP:
			return "sp"
		case is32:
			return "wzr"
		default:
			return "xzr"
		}
	}
	if is32 {
		return "w" + strconv.FormatUint(uint64(n), 10)
	}
	return "x" + strconv.FormatUint(uint64(n), 10)
}

// String renders a memory operand in the bracketed syntax parseMem reads.
func (m memOperand) String() string {
	base := regSpelling(m.base, false, true)
	switch {
	case m.hasIndex:
		idx := regSpelling(m.index, m.indexIs32, false)
		s := "[" + base + ", " + idx
		if m.indexOpt != 3 || m.indexHasAmt {
			// Option 3 is LSL/UXTX architecturally, but GNU as accepts only
			// `lsl` in this addressing mode and rejects `uxtx` outright, so
			// that is the spelling that round-trips.
			opt := extendSpellings[m.indexOpt]
			if m.indexOpt == 3 {
				opt = "lsl"
			}
			s += ", " + opt
			if m.indexHasAmt {
				s += " #" + strconv.FormatUint(uint64(m.indexAmt), 10)
			}
		}
		return s + "]"
	case m.off != 0:
		s := "[" + base + ", #" + strconv.FormatInt(m.off, 10) + "]"
		if m.pre {
			s += "!"
		}
		return s
	default:
		if m.pre {
			return "[" + base + ", #0]!"
		}
		return "[" + base + "]"
	}
}
