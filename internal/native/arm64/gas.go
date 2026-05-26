package arm64

import (
	"fmt"
	"strconv"
	"strings"
)

// Assemble parses a subset of GAS AArch64 assembly text — the dialect
// internal/codegen/arm64 emits — into machine code, routing each
// instruction through the encoders and the label-aware Assembler.
//
// This is the integration seam of the native-binary path: it reuses
// the existing (proven) code generator unchanged and turns its textual
// output into bytes, validated byte-for-byte against aarch64-linux-gnu-as.
//
// Coverage grows brick by brick. This first slice handles labels, the
// no-op-for-.text directives, and the move / arithmetic / logical /
// compare / multiply / branch instruction forms. Anything not yet
// supported returns an explicit error (with the offending line) rather
// than silently miscompiling — so coverage gaps are loud.
func Assemble(src string) ([]byte, error) {
	a := NewAssembler()
	for lineno, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		// Peel any leading "label:" prefixes; a line may carry both a
		// label and an instruction.
		for {
			label, rest, ok := splitLabel(line)
			if !ok {
				break
			}
			a.Label(label)
			line = strings.TrimSpace(rest)
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			if err := handleDirective(line); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineno+1, err)
			}
			continue
		}
		if err := assembleInsn(a, line); err != nil {
			return nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), err)
		}
	}
	return a.Bytes()
}

func stripComment(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// splitLabel peels a single leading "name:" off the front of line.
func splitLabel(line string) (label, rest string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return "", "", false
	}
	name := strings.TrimSpace(line[:i])
	if !isIdent(name) {
		return "", "", false
	}
	return name, line[i+1:], true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '.':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// handleDirective accepts (and ignores) the assembler directives that
// don't affect a single-.text-section program, and errors on the rest
// (e.g. data directives) so the gap is visible.
func handleDirective(line string) error {
	d := strings.Fields(line)[0]
	switch d {
	case ".text", ".arch", ".global", ".globl", ".type", ".size",
		".align", ".p2align", ".balign", ".cfi_startproc", ".cfi_endproc":
		return nil
	default:
		return fmt.Errorf("unsupported directive %q", d)
	}
}

func assembleInsn(a *Assembler, line string) error {
	mnem, rest := splitMnemonic(line)
	ops := splitOperands(rest)

	// Conditional branches: b.<cond> or the b<cond> aliases.
	if cond, ok := condOf(mnem); ok {
		if len(ops) != 1 {
			return fmt.Errorf("%s expects 1 operand", mnem)
		}
		a.Bcond(cond, ops[0])
		return nil
	}

	switch mnem {
	case "mov":
		return asmMov(a, ops)
	case "movz", "movk":
		return asmMoveWide(a, mnem, ops)
	case "add", "sub":
		return asmAddSub(a, mnem, ops)
	case "and", "orr", "eor", "mul":
		return asm3Reg(a, mnem, ops)
	case "lsl", "lsr", "asr":
		return asmShift(a, mnem, ops)
	case "sxtb", "sxth", "sxtw":
		return asmExtend(a, mnem, ops)
	case "cmp":
		return asmCmp(a, ops)
	case "ldr", "str", "ldrb", "strb", "ldrh", "strh":
		return asmLoadStore(a, mnem, ops)
	case "stp", "ldp":
		return asmPair(a, mnem, ops)
	case "b":
		return one(ops, func(s string) { a.B(s) })
	case "bl":
		return one(ops, func(s string) { a.BL(s) })
	case "cbz":
		return regLabel(a, ops, a.CBZ)
	case "cbnz":
		return regLabel(a, ops, a.CBNZ)
	case "br":
		return oneReg(ops, func(r uint32) { a.Emit(BR(r)) })
	case "blr":
		return oneReg(ops, func(r uint32) { a.Emit(BLR(r)) })
	case "ret":
		rn := uint32(30)
		if len(ops) == 1 {
			r, err := parseReg(ops[0])
			if err != nil {
				return err
			}
			rn = r
		}
		a.Emit(RET(rn))
		return nil
	case "svc":
		if len(ops) != 1 {
			return fmt.Errorf("svc expects 1 operand")
		}
		imm, err := parseImm(ops[0])
		if err != nil {
			return err
		}
		a.Emit(SVC(uint16(imm)))
		return nil
	default:
		return fmt.Errorf("unsupported instruction %q", mnem)
	}
}

func asmMov(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("mov expects 2 operands")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	if strings.HasPrefix(ops[1], "#") {
		imm, err := parseImm(ops[1])
		if err != nil {
			return err
		}
		if imm < 0 || imm > 0xffff {
			return fmt.Errorf("mov #%d out of movz range (use movz/movk)", imm)
		}
		a.Emit(MOVZ(rd, uint16(imm), 0))
		return nil
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(MOVreg(rd, rm))
	return nil
}

func asmMoveWide(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 2 {
		return fmt.Errorf("%s expects at least 2 operands", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	imm, err := parseImm(ops[1])
	if err != nil {
		return err
	}
	if imm < 0 || imm > 0xffff {
		return fmt.Errorf("%s immediate %d out of 16-bit range", mnem, imm)
	}
	shift, err := parseShiftField(ops, 2, "lsl")
	if err != nil {
		return err
	}
	if mnem == "movz" {
		a.Emit(MOVZ(rd, uint16(imm), shift))
	} else {
		a.Emit(MOVK(rd, uint16(imm), shift))
	}
	return nil
}

func asmAddSub(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 3 {
		return fmt.Errorf("%s expects 3 operands", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	if strings.HasPrefix(ops[2], "#") {
		imm, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		sh, err := parseShiftField(ops, 3, "lsl")
		if err != nil {
			return err
		}
		shift12 := sh == 12
		if sh != 0 && sh != 12 {
			return fmt.Errorf("%s immediate shift must be 0 or 12", mnem)
		}
		if mnem == "add" {
			a.Emit(ADDimm(rd, rn, uint16(imm), shift12))
		} else {
			a.Emit(SUBimm(rd, rn, uint16(imm), shift12))
		}
		return nil
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	if mnem == "add" {
		a.Emit(ADDreg(rd, rn, rm))
	} else {
		a.Emit(SUBreg(rd, rn, rm))
	}
	return nil
}

func asm3Reg(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects 3 operands", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	switch mnem {
	case "and":
		a.Emit(ANDreg(rd, rn, rm))
	case "orr":
		a.Emit(ORRreg(rd, rn, rm))
	case "eor":
		a.Emit(EORreg(rd, rn, rm))
	case "mul":
		a.Emit(MUL(rd, rn, rm))
	}
	return nil
}

// asmShift handles lsl/lsr/asr in both the register-variable form
// (`<op> Xd, Xn, Xm`) and the immediate form (`<op> Xd, Xn, #shift`).
func asmShift(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects 3 operands", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	if strings.HasPrefix(ops[2], "#") {
		imm, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		sh := uint32(imm)
		switch mnem {
		case "lsl":
			a.Emit(LSLimm(rd, rn, sh))
		case "lsr":
			a.Emit(LSRimm(rd, rn, sh))
		case "asr":
			a.Emit(ASRimm(rd, rn, sh))
		}
		return nil
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	switch mnem {
	case "lsl":
		a.Emit(LSLV(rd, rn, rm))
	case "lsr":
		a.Emit(LSRV(rd, rn, rm))
	case "asr":
		a.Emit(ASRV(rd, rn, rm))
	}
	return nil
}

// asmExtend handles the sign-extends `sxt<b|h|w> Xd, Wn`.
func asmExtend(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects 2 operands", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	switch mnem {
	case "sxtb":
		a.Emit(SXTB(rd, rn))
	case "sxth":
		a.Emit(SXTH(rd, rn))
	case "sxtw":
		a.Emit(SXTW(rd, rn))
	}
	return nil
}

// asmLoadStore handles the single-register unsigned-offset loads and
// stores: `<op> Rt, [Xn{, #imm}]`. Pre/post-index single forms aren't
// encoded yet, so they error rather than miscompile.
func asmLoadStore(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects a register and a memory operand", mnem)
	}
	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	m, err := parseMem(ops[1])
	if err != nil {
		return err
	}
	if m.pre {
		return fmt.Errorf("%s pre-index addressing not supported yet", mnem)
	}
	if m.off < 0 {
		return fmt.Errorf("%s negative offset needs the unscaled (ldur) form, not supported yet", mnem)
	}
	off := uint32(m.off)
	switch mnem {
	case "ldr":
		a.Emit(LDRimm(rt, m.base, off))
	case "str":
		a.Emit(STRimm(rt, m.base, off))
	case "ldrb":
		a.Emit(LDRBimm(rt, m.base, off))
	case "strb":
		a.Emit(STRBimm(rt, m.base, off))
	case "ldrh":
		a.Emit(LDRHimm(rt, m.base, off))
	case "strh":
		a.Emit(STRHimm(rt, m.base, off))
	}
	return nil
}

// asmPair handles the load/store-pair frame idiom in its pre-index
// (`stp Rt, Rt2, [Xn, #imm]!`) and post-index (`ldp Rt, Rt2, [Xn], #imm`)
// forms — the only pair forms the encoders cover.
func asmPair(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 3 {
		return fmt.Errorf("%s expects two registers and a memory operand", mnem)
	}
	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rt2, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	m, err := parseMem(ops[2])
	if err != nil {
		return err
	}
	var off int64
	switch {
	case m.pre && len(ops) == 3:
		off = m.off // [Xn, #imm]!
	case !m.pre && len(ops) == 4:
		// post-index: base is [Xn], displacement is the trailing operand.
		if m.off != 0 {
			return fmt.Errorf("%s post-index base must be plain [Xn]", mnem)
		}
		v, err := parseImm(ops[3])
		if err != nil {
			return err
		}
		off = v
	default:
		return fmt.Errorf("%s supports only pre-index ([Xn,#imm]!) or post-index ([Xn],#imm)", mnem)
	}
	if mnem == "stp" {
		if !m.pre {
			return fmt.Errorf("stp post-index not supported yet")
		}
		a.Emit(STPpre(rt, rt2, m.base, int32(off)))
	} else {
		if m.pre {
			return fmt.Errorf("ldp pre-index not supported yet")
		}
		a.Emit(LDPpost(rt, rt2, m.base, int32(off)))
	}
	return nil
}

func asmCmp(a *Assembler, ops []string) error {
	if len(ops) < 2 {
		return fmt.Errorf("cmp expects 2 operands")
	}
	rn, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	if strings.HasPrefix(ops[1], "#") {
		imm, err := parseImm(ops[1])
		if err != nil {
			return err
		}
		a.Emit(CMPimm(rn, uint16(imm), false))
		return nil
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(CMPreg(rn, rm))
	return nil
}

func one(ops []string, f func(string)) error {
	if len(ops) != 1 {
		return fmt.Errorf("expects 1 operand")
	}
	f(ops[0])
	return nil
}

func oneReg(ops []string, f func(uint32)) error {
	if len(ops) != 1 {
		return fmt.Errorf("expects 1 register operand")
	}
	r, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	f(r)
	return nil
}

func regLabel(a *Assembler, ops []string, f func(uint32, string)) error {
	if len(ops) != 2 {
		return fmt.Errorf("expects a register and a label")
	}
	r, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	f(r, ops[1])
	return nil
}

func splitMnemonic(line string) (mnem, rest string) {
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], strings.TrimSpace(line[i+1:])
	}
	return line, ""
}

// splitOperands splits the operand list on commas, but keeps commas
// that sit inside a memory operand's brackets together (so
// "[x1, #8]" stays one operand).
func splitOperands(rest string) []string {
	if rest == "" {
		return nil
	}
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(rest[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(rest[start:]))
	return out
}

// memOperand is a parsed "[Xn]" / "[Xn, #imm]" / "[Xn, #imm]!" form.
type memOperand struct {
	base uint32
	off  int64
	pre  bool // pre-index ("[Xn, #imm]!"): writeback before access
}

// parseMem parses a bracketed memory operand. A trailing "!" marks
// pre-index writeback. A missing offset means 0.
func parseMem(s string) (memOperand, error) {
	s = strings.TrimSpace(s)
	var m memOperand
	if !strings.HasPrefix(s, "[") {
		return m, fmt.Errorf("expected memory operand, got %q", s)
	}
	if strings.HasSuffix(s, "]!") {
		m.pre = true
		s = strings.TrimSuffix(s, "]!")
	} else if strings.HasSuffix(s, "]") {
		s = strings.TrimSuffix(s, "]")
	} else {
		return m, fmt.Errorf("unterminated memory operand %q", s)
	}
	inner := splitOperands(strings.TrimPrefix(s, "["))
	if len(inner) == 0 || len(inner) > 2 {
		return m, fmt.Errorf("bad memory operand")
	}
	base, err := parseReg(inner[0])
	if err != nil {
		return m, err
	}
	m.base = base
	if len(inner) == 2 {
		off, err := parseImm(inner[1])
		if err != nil {
			return m, err
		}
		m.off = off
	}
	return m, nil
}

// parseShiftField reads an optional "lsl #N" operand at index i. A
// missing field means shift 0.
func parseShiftField(ops []string, i int, kind string) (uint32, error) {
	if i >= len(ops) {
		return 0, nil
	}
	f := strings.Fields(ops[i])
	if len(f) != 2 || f[0] != kind {
		return 0, fmt.Errorf("expected %q shift, got %q", kind, ops[i])
	}
	n, err := parseImm(f[1])
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}

// parseReg parses x0..x30, plus sp/xzr/wzr and the w-aliases (which
// share register numbers with x for the forms covered so far).
func parseReg(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "sp", "xzr", "wzr":
		return 31, nil
	case "lr":
		return 30, nil
	}
	if len(s) >= 2 && (s[0] == 'x' || s[0] == 'w') {
		n, err := strconv.Atoi(s[1:])
		if err == nil && n >= 0 && n <= 30 {
			return uint32(n), nil
		}
	}
	return 0, fmt.Errorf("bad register %q", s)
}

// parseImm parses a "#<value>" immediate (decimal or 0x-hex, optionally
// negative).
func parseImm(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	v, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("bad immediate %q", s)
	}
	return v, nil
}

// condOf maps a conditional-branch mnemonic (b.eq or the beq alias) to
// its condition code.
func condOf(mnem string) (uint32, bool) {
	name := ""
	switch {
	case strings.HasPrefix(mnem, "b.") && len(mnem) > 2:
		name = mnem[2:]
	case len(mnem) == 3 && mnem[0] == 'b':
		name = mnem[1:]
	default:
		return 0, false
	}
	c, ok := condCodes[name]
	return c, ok
}

var condCodes = map[string]uint32{
	"eq": CondEQ, "ne": CondNE, "hs": CondHS, "cs": CondHS,
	"lo": CondLO, "cc": CondLO, "mi": CondMI, "pl": CondPL,
	"vs": CondVS, "vc": CondVC, "hi": CondHI, "ls": CondLS,
	"ge": CondGE, "lt": CondLT, "gt": CondGT, "le": CondLE,
}
