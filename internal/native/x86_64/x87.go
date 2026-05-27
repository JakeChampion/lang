package x86_64

import "fmt"

// x87 floating-point support — the FPU instructions the x86-64 code
// generator emits for the f64 transcendentals (sin/cos/exp/log/pow):
// constant loads (fld1/fldl2e/fldln2…), the transcendental primitives
// (fsin/fcos/fyl2x/f2xm1/fscale/frndint), stack moves (fld/fst/fstp/fxch),
// and register/memory arithmetic (fadd/fmul/fsub…(p)). All encodings here
// are matched byte-for-byte against the GNU assembler's output (verified
// via objdump) — the fsub/fsubr and *p operand-order conventions are
// notoriously easy to get wrong by reasoning alone.

// x87Fixed maps the no-operand FPU instructions to their two opcode bytes.
var x87Fixed = map[string][2]byte{
	"fld1": {0xD9, 0xE8}, "fldl2t": {0xD9, 0xE9}, "fldl2e": {0xD9, 0xEA}, "fldpi": {0xD9, 0xEB},
	"fldlg2": {0xD9, 0xEC}, "fldln2": {0xD9, 0xED}, "fldz": {0xD9, 0xEE},
	"f2xm1": {0xD9, 0xF0}, "fyl2x": {0xD9, 0xF1}, "fptan": {0xD9, 0xF2}, "fpatan": {0xD9, 0xF3},
	"fxtract": {0xD9, 0xF4}, "fprem1": {0xD9, 0xF5}, "fdecstp": {0xD9, 0xF6}, "fincstp": {0xD9, 0xF7},
	"fprem": {0xD9, 0xF8}, "fyl2xp1": {0xD9, 0xF9}, "fsqrt": {0xD9, 0xFA}, "fsincos": {0xD9, 0xFB},
	"frndint": {0xD9, 0xFC}, "fscale": {0xD9, 0xFD}, "fsin": {0xD9, 0xFE}, "fcos": {0xD9, 0xFF},
	"fchs": {0xD9, 0xE0}, "fabs": {0xD9, 0xE1}, "ftst": {0xD9, 0xE4}, "fxam": {0xD9, 0xE5},
	"fnop": {0xD9, 0xD0}, "fcompp": {0xDE, 0xD9}, "fucompp": {0xDA, 0xE9},
}

// x87arithBase holds, per arithmetic mnemonic, the +i opcode base for the
// three register forms: dstSt0 = "f… st(0), st(i)" (D8 escape), srcSt0 =
// "f… st(i), st(0)" (DC escape), pop = "f…p st(i), st(0)" (DE escape), and
// the ModRM /digit for the memory form. fsub/fsubr and fdiv/fdivr swap
// bases between the D8 and DC/DE escapes — these values mirror GNU as.
var x87arithBase = map[string]struct {
	dstSt0, srcSt0, pop, memDigit byte
}{
	"fadd":  {0xC0, 0xC0, 0xC0, 0},
	"fmul":  {0xC8, 0xC8, 0xC8, 1},
	"fsub":  {0xE0, 0xE8, 0xE8, 4},
	"fsubr": {0xE8, 0xE0, 0xE0, 5},
	"fdiv":  {0xF0, 0xF8, 0xF8, 6},
	"fdivr": {0xF8, 0xF0, 0xF0, 7},
}

// x87 dispatches an FPU instruction. Returns handled=false if the
// mnemonic is not an x87 instruction (so the caller can report it).
func (a *Assembler) x87(mnem string, ops []operand) (handled bool, err error) {
	if b, ok := x87Fixed[mnem]; ok && len(ops) == 0 {
		a.emit(b[0], b[1])
		return true, nil
	}
	switch mnem {
	case "fld":
		return true, a.x87mov(ops, 0xD9, 0xC0, 0xDD, 0xD9, 0)
	case "fst":
		return true, a.x87mov(ops, 0xDD, 0xD0, 0xDD, 0xD9, 2)
	case "fstp":
		return true, a.x87mov(ops, 0xDD, 0xD8, 0xDD, 0xD9, 3)
	case "fxch":
		return true, a.x87st(ops, 0xD9, 0xC8, 0xC9)
	case "ffree":
		return true, a.x87st(ops, 0xDD, 0xC0, 0xC0)
	case "fadd", "fmul", "fsub", "fsubr", "fdiv", "fdivr":
		return true, a.x87arith(mnem, ops)
	case "faddp", "fmulp", "fsubp", "fsubrp", "fdivp", "fdivrp":
		return true, a.x87arithP(mnem, ops)
	}
	return false, nil
}

// x87mov encodes fld/fst/fstp: a stack-register form (stEsc + stBase + i;
// stEsc is D9 for fld but DD for fst/fstp) and a memory form (escape DD
// for qword / D9 for dword, ModRM /digit).
func (a *Assembler) x87mov(ops []operand, stEsc, stBase, qEsc, dEsc, memDigit byte) error {
	if len(ops) != 1 {
		return fmt.Errorf("x87 load/store expects one operand")
	}
	o := ops[0]
	switch o.kind {
	case opSt:
		a.emit(stEsc, stBase+byte(o.reg))
		return nil
	case opMem:
		esc := qEsc
		if o.memSize == 32 {
			esc = dEsc
		}
		a.emit(esc)
		a.encodeMem(int(memDigit), o)
		return nil
	}
	return fmt.Errorf("x87 load/store: bad operand")
}

// x87st encodes the single-stack-register ops (fxch/ffree): escape +
// base + i, with a bare form defaulting to st(1).
func (a *Assembler) x87st(ops []operand, esc, base, bare byte) error {
	if len(ops) == 0 {
		a.emit(esc, bare)
		return nil
	}
	if len(ops) != 1 || ops[0].kind != opSt {
		return fmt.Errorf("x87 op expects st(i)")
	}
	a.emit(esc, base+byte(ops[0].reg))
	return nil
}

// x87arith encodes the non-popping fadd/fmul/fsub/fsubr/fdiv/fdivr in
// their register (st(0),st(i) / st(i),st(0)) and memory forms.
func (a *Assembler) x87arith(mnem string, ops []operand) error {
	b := x87arithBase[mnem]
	if len(ops) == 1 {
		o := ops[0]
		if o.kind == opMem { // f… m32fp/m64fp
			esc := byte(0xDC)
			if o.memSize == 32 {
				esc = 0xD8
			}
			a.emit(esc)
			a.encodeMem(int(b.memDigit), o)
			return nil
		}
		if o.kind == opSt { // f… st(i)  ==  f… st(0), st(i)
			a.emit(0xD8, b.dstSt0+byte(o.reg))
			return nil
		}
		return fmt.Errorf("x87 %s: bad operand", mnem)
	}
	if len(ops) == 2 && ops[0].kind == opSt && ops[1].kind == opSt {
		dst, src := ops[0], ops[1]
		if dst.reg == 0 { // f… st(0), st(i)  → D8 escape
			a.emit(0xD8, b.dstSt0+byte(src.reg))
			return nil
		}
		if src.reg == 0 { // f… st(i), st(0)  → DC escape
			a.emit(0xDC, b.srcSt0+byte(dst.reg))
			return nil
		}
	}
	return fmt.Errorf("x87 %s: unsupported operands", mnem)
}

// x87arithP encodes the popping faddp/fmulp/fsubp/fsubrp/fdivp/fdivrp:
// DE escape, "f…p st(i), st(0)"; a bare form defaults to st(1).
func (a *Assembler) x87arithP(mnem string, ops []operand) error {
	b := x87arithBase[mnem[:len(mnem)-1]] // strip trailing 'p'
	i := byte(1)                          // bare default st(1)
	if len(ops) >= 1 {
		if ops[0].kind != opSt {
			return fmt.Errorf("x87 %s expects st(i)", mnem)
		}
		i = byte(ops[0].reg)
	}
	a.emit(0xDE, b.pop+i)
	return nil
}
