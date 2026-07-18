package arm64

import (
	"fmt"
	"math"
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

// stripComment removes a trailing `//` line comment, but only when the
// `//` sits outside a double-quoted string literal — otherwise the `//`
// inside a `.asciz "a // b"` operand would truncate the string and leave
// an unterminated literal. Backslash escapes inside the string are
// honoured so an escaped quote (\") doesn't end the literal early.
func stripComment(s string) string {
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++ // skip the escaped byte
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			s = s[:i]
			break
		}
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
		".align", ".p2align", ".balign", ".cfi_startproc", ".cfi_endproc",
		".ltorg":
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
	case "add", "sub", "adds", "subs":
		return asmAddSub(a, mnem, ops)
	case "and", "orr", "eor", "mul", "udiv", "sdiv":
		return asm3Reg(a, mnem, ops)
	case "csel":
		return asmCsel(a, ops)
	case "cset":
		return asmCset(a, ops)
	case "cmn":
		return asmCmn(a, ops)
	case "neg":
		return asm2Reg(a, ops, NEG)
	case "clz":
		return asm2Reg(a, ops, CLZ)
	case "msub":
		return asmMsub(a, ops)
	case "fadd", "fsub", "fmul", "fdiv":
		return asmFloat3(a, mnem, ops)
	case "fneg":
		return asmFNeg(a, ops)
	case "fabs":
		return asmFUnaryD(a, "fabs", ops, FABS)
	case "fsqrt":
		return asmFUnaryD(a, "fsqrt", ops, FSQRT)
	case "frintm":
		return asmFUnaryD(a, "frintm", ops, FRINTM)
	case "frintp":
		return asmFUnaryD(a, "frintp", ops, FRINTP)
	case "frintz":
		return asmFUnaryD(a, "frintz", ops, FRINTZ)
	case "frinta":
		return asmFUnaryD(a, "frinta", ops, FRINTA)
	case "fcmp":
		return asmFcmp(a, ops)
	case "fmov":
		return asmFmov(a, ops)
	case "fcvt":
		return asmFcvt(a, ops)
	case "scvtf":
		return asmScvtf(a, ops)
	case "fcvtzs":
		return asmFcvtToInt(a, ops, FCVTZS, FCVTZSS)
	case "ucvtf":
		return asmUcvtf(a, ops)
	case "fcvtzu":
		return asmFcvtToInt(a, ops, FCVTZU, FCVTZUS)
	case "lsl", "lsr", "asr":
		return asmShift(a, mnem, ops)
	case "sxtb", "sxth", "sxtw", "uxtb", "uxth":
		return asmExtend(a, mnem, ops)
	case "rev16":
		return asm2Reg(a, ops, REV16)
	case "ubfx", "sbfx":
		return asmBitfieldExtract(a, mnem, ops)
	case "cmp":
		return asmCmp(a, ops)
	case "ldr", "str", "ldrb", "strb", "ldrh", "strh":
		return asmLoadStore(a, mnem, ops)
	case "ldur", "stur", "ldurb", "sturb":
		return asmUnscaled(a, mnem, ops)
	case "ldrsb", "ldrsh", "ldrsw":
		return asmLoadSigned(a, mnem, ops)
	case "stp", "ldp":
		return asmPair(a, mnem, ops)
	case "b":
		return one(ops, func(s string) { a.B(s) })
	case "bl":
		return one(ops, func(s string) { a.BL(s) })
	case "cbz":
		return regLabelWidth(a, ops, a.CBZ, a.CBZW)
	case "cbnz":
		return regLabelWidth(a, ops, a.CBNZ, a.CBNZW)
	case "tbz":
		return asmTestBranch(a, ops, a.TBZ)
	case "tbnz":
		return asmTestBranch(a, ops, a.TBNZ)
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
		w := is32(ops[0])
		return emitMovImm(a, rd, uint64(imm), w, ops[1])
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	// `mov` to/from sp is the `add Rd, Rn, #0` alias (ORR can't encode
	// the stack pointer); plain reg-reg mov (including from the zero
	// register xzr/wzr) is the ORR/MOVreg alias. sp and xzr share
	// register number 31, so this must key off the operand spelling: the
	// add-alias only applies to sp, since routing `mov Rd, xzr` through
	// it would read sp (=Rn 31 in add-immediate) instead of zero.
	isSP := func(s string) bool { return strings.TrimSpace(s) == "sp" }
	if isSP(ops[0]) || isSP(ops[1]) {
		a.Emit(clearSF(ADDimm(rd, rm, 0, false), is32(ops[0])))
		return nil
	}
	a.Emit(clearSF(MOVreg(rd, rm), is32(ops[0])))
	return nil
}

// emitMovImm encodes `mov Rd, #v` as a single instruction, mirroring
// the assembler's pseudo-op expansion: MOVZ when v is one 16-bit lane,
// MOVN when ~v is, else ORR-bitmask. Errors when v needs a multi-
// instruction movz/movk sequence (the backend uses `ldr =` for those).
func emitMovImm(a *Assembler, rd uint32, v uint64, w bool, disp string) error {
	if w {
		v = uint64(uint32(v))
	}
	maxShift := uint32(48)
	if w {
		maxShift = 16
	}
	if c, sh, ok := singleLane(v, maxShift); ok {
		a.Emit(clearSF(MOVZ(rd, c, sh), w))
		return nil
	}
	inv := ^v
	if w {
		inv = uint64(^uint32(v))
	}
	if c, sh, ok := singleLane(inv, maxShift); ok {
		a.Emit(clearSF(MOVN(rd, c, sh), w))
		return nil
	}
	if insn, ok := ORRimm(rd, 31, v, !w); ok { // mov Rd,#v = orr Rd,xzr,#v
		a.Emit(insn)
		return nil
	}
	// General case: materialise the lowest non-zero 16-bit lane with movz (which
	// zeroes the rest of the register), then overlay each remaining non-zero lane
	// with movk. Two lanes for a w-register, four for an x-register.
	lanes := uint32(4)
	if w {
		lanes = 2
	}
	first := true
	for i := uint32(0); i < lanes; i++ {
		sh := i * 16
		lane := uint16((v >> sh) & 0xFFFF)
		if lane == 0 {
			continue
		}
		if first {
			a.Emit(clearSF(MOVZ(rd, lane, sh), w))
			first = false
		} else {
			a.Emit(clearSF(MOVK(rd, lane, sh), w))
		}
	}
	if first {
		// v == 0 — already handled by singleLane above, but stay safe.
		a.Emit(clearSF(MOVZ(rd, 0, 0), w))
	}
	return nil
}

// singleLane returns (imm16, shift, ok): ok when v has at most one
// nonzero 16-bit lane, at a shift in {0,16,...,maxShift}.
func singleLane(v uint64, maxShift uint32) (uint16, uint32, bool) {
	for sh := uint32(0); sh <= maxShift; sh += 16 {
		if v&^(uint64(0xffff)<<sh) == 0 {
			return uint16(v >> sh), sh, true
		}
	}
	return 0, 0, false
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
	w := is32(ops[0])
	if mnem == "movz" {
		a.Emit(clearSF(MOVZ(rd, uint16(imm), shift), w))
	} else {
		a.Emit(clearSF(MOVK(rd, uint16(imm), shift), w))
	}
	return nil
}

func asmAddSub(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 3 {
		return fmt.Errorf("%s expects 3 operands", mnem)
	}
	// `adds` / `subs` are the flag-setting variants: every add/sub encoding
	// class (immediate, shifted-register, extended-register) places the S
	// bit at bit 29, so they share the base encoders with S OR'd in.
	isAdd := mnem == "add" || mnem == "adds"
	setS := mnem == "adds" || mnem == "subs"
	emit := func(insn uint32) {
		if setS {
			insn |= 1 << 29
		}
		a.Emit(insn)
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
		w := is32(ops[0])
		var i12 uint16
		var shift12 bool
		if len(ops) > 3 {
			// Explicit `, lsl #12`.
			sh, serr := parseShiftField(ops, 3, "lsl")
			if serr != nil {
				return serr
			}
			if sh != 0 && sh != 12 {
				return fmt.Errorf("%s immediate shift must be 0 or 12", mnem)
			}
			i12, shift12 = uint16(imm), sh == 12
		} else {
			var ok bool
			if i12, shift12, ok = addSubImm12(imm); !ok {
				return fmt.Errorf("%s immediate %s out of range", mnem, ops[2])
			}
		}
		if isAdd {
			emit(clearSF(ADDimm(rd, rn, i12, shift12), w))
		} else {
			emit(clearSF(SUBimm(rd, rn, i12, shift12), w))
		}
		return nil
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	w := is32(ops[0])
	// Optional shifted- or extended-register form. The backend emits
	// `add Xd, Xn, Xm, lsl #N` to scale an index by a power-of-two
	// element size, and `add Xd, Xn, Wm, uxtw/sxtw {#N}` to widen a
	// 32-bit index to a 64-bit address. Dropping either (as the plain
	// reg path did) corrupts every array element past [0].
	if len(ops) > 3 {
		kind := strings.Fields(ops[3])
		if len(kind) > 0 && (strings.HasPrefix(kind[0], "uxt") || strings.HasPrefix(kind[0], "sxt")) {
			opt, amt, eerr := parseExtend(ops[3])
			if eerr != nil {
				return eerr
			}
			if isAdd {
				emit(ADDextReg(rd, rn, rm, opt, amt))
			} else {
				emit(SUBextReg(rd, rn, rm, opt, amt))
			}
			return nil
		}
		st, amt, serr := parseRegShift(ops[3])
		if serr != nil {
			return serr
		}
		if isAdd {
			emit(clearSF(ADDregShift(rd, rn, rm, st, amt), w))
		} else {
			emit(clearSF(SUBregShift(rd, rn, rm, st, amt), w))
		}
		return nil
	}
	// `add/sub sp, ...` with a plain register operand: register number 31
	// denotes SP only in the EXTENDED-register form. The shifted-register
	// form (ADDreg/SUBreg) treats 31 as XZR, so it silently turns
	// `sub sp, sp, x16` into `sub xzr, xzr, x16` (`neg xzr, x16`) — a no-op
	// that never adjusts SP. Large stack frames (> 4095 bytes, e.g. the
	// self-host `lower_stmt`) materialise the frame size in a register and
	// emit `sub sp, sp, x16` / `add sp, sp, x16`; encoded as shifted-register
	// the frame was never allocated, so the operand-stack push/pops overran
	// the locals and corrupted them (issue #3598). Route SP-targeting plain-
	// register add/sub through the extended form with UXTX (a 64-bit no-op
	// extension), which encodes 31 as SP. Keyed on the operand SPELLING, since
	// "sp" and "xzr" share register number 31.
	if isSPReg(ops[0]) || isSPReg(ops[1]) {
		const uxtx = 3
		if isAdd {
			emit(ADDextReg(rd, rn, rm, uxtx, 0))
		} else {
			emit(SUBextReg(rd, rn, rm, uxtx, 0))
		}
		return nil
	}
	if isAdd {
		emit(clearSF(ADDreg(rd, rn, rm), w))
	} else {
		emit(clearSF(SUBreg(rd, rn, rm), w))
	}
	return nil
}

// isSPReg reports whether an operand is spelled "sp" (register 31 as the
// stack pointer). "sp" and "xzr"/"wzr" share register number 31, so SP-vs-XZR
// must be distinguished by spelling, not by the parsed register number.
func isSPReg(op string) bool { return strings.TrimSpace(op) == "sp" }

// parseRegShift parses a register-shift operand like "lsl #3" into a
// shift-type selector (0=LSL, 1=LSR, 2=ASR) and amount.
func parseRegShift(op string) (shiftType, amount uint32, err error) {
	f := strings.Fields(op)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("bad register shift %q", op)
	}
	switch f[0] {
	case "lsl":
		shiftType = 0
	case "lsr":
		shiftType = 1
	case "asr":
		shiftType = 2
	case "ror":
		shiftType = 3
	default:
		return 0, 0, fmt.Errorf("unsupported shift %q", f[0])
	}
	n, err := parseImm(f[1])
	if err != nil {
		return 0, 0, err
	}
	return shiftType, uint32(n), nil
}

// addSubImm12 selects the 12-bit immediate encoding shared by
// add/sub/cmp/cmn: a value <= 0xfff encodes directly; a value that is a
// multiple of 0x1000 and fits in 12 bits once shifted right by 12 uses
// the `lsl #12` form. GNU as performs this selection implicitly, so the
// backend can emit a bare large immediate like `cmp x0, #0x10000`
// (which must become #16, lsl #12 — not the truncated #0).
func addSubImm12(v int64) (imm uint16, shift12 bool, ok bool) {
	if v < 0 {
		return 0, false, false
	}
	u := uint64(v)
	if u <= 0xfff {
		return uint16(u), false, true
	}
	if u&0xfff == 0 && (u>>12) <= 0xfff {
		return uint16(u >> 12), true, true
	}
	return 0, false, false
}

// parseExtend parses an extended-register operand like "uxtw" or
// "sxtw #2" into the 3-bit option selector and optional left-shift
// amount.
func parseExtend(op string) (option, amount uint32, err error) {
	f := strings.Fields(op)
	switch f[0] {
	case "uxtb":
		option = 0
	case "uxth":
		option = 1
	case "uxtw":
		option = 2
	case "uxtx":
		option = 3
	case "sxtb":
		option = 4
	case "sxth":
		option = 5
	case "sxtw":
		option = 6
	case "sxtx":
		option = 7
	default:
		return 0, 0, fmt.Errorf("unsupported extend %q", f[0])
	}
	switch len(f) {
	case 1:
		return option, 0, nil
	case 2:
		n, perr := parseImm(f[1])
		if perr != nil {
			return 0, 0, perr
		}
		return option, uint32(n), nil
	default:
		return 0, 0, fmt.Errorf("bad extend operand %q", op)
	}
}

func asm3Reg(a *Assembler, mnem string, ops []string) error {
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
	w := is32(ops[0])
	// and/orr/eor take a logical (bitmask) immediate as the third
	// operand; the others are register-only.
	if strings.HasPrefix(ops[2], "#") && (mnem == "and" || mnem == "orr" || mnem == "eor") {
		imm, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		var insn uint32
		var ok bool
		switch mnem {
		case "and":
			insn, ok = ANDimm(rd, rn, uint64(imm), !w)
		case "orr":
			insn, ok = ORRimm(rd, rn, uint64(imm), !w)
		case "eor":
			insn, ok = EORimm(rd, rn, uint64(imm), !w)
		}
		if !ok {
			return fmt.Errorf("%s: %s is not an encodable bitmask immediate", mnem, ops[2])
		}
		a.Emit(insn)
		return nil
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	// Optional shifted-register form for the logical ops, e.g.
	// `orr w3, w1, w1, lsl #8`. mul/udiv/sdiv take no shift.
	if len(ops) > 3 {
		st, amt, serr := parseRegShift(ops[3])
		if serr != nil {
			return serr
		}
		switch mnem {
		case "and":
			a.Emit(clearSF(ANDregShift(rd, rn, rm, st, amt), w))
		case "orr":
			a.Emit(clearSF(ORRregShift(rd, rn, rm, st, amt), w))
		case "eor":
			a.Emit(clearSF(EORregShift(rd, rn, rm, st, amt), w))
		default:
			return fmt.Errorf("%s does not take a shifted register operand", mnem)
		}
		return nil
	}
	switch mnem {
	case "and":
		a.Emit(clearSF(ANDreg(rd, rn, rm), w))
	case "orr":
		a.Emit(clearSF(ORRreg(rd, rn, rm), w))
	case "eor":
		a.Emit(clearSF(EORreg(rd, rn, rm), w))
	case "mul":
		a.Emit(clearSF(MUL(rd, rn, rm), w))
	case "udiv":
		a.Emit(clearSF(UDIV(rd, rn, rm), w))
	case "sdiv":
		a.Emit(clearSF(SDIV(rd, rn, rm), w))
	}
	return nil
}

// asm2Reg handles two-register ops where both operands are registers
// and the encoder takes (a, b) — used by cmn (Xn, Xm) and neg (Xd, Xm).
func asm2Reg(a *Assembler, ops []string, enc func(x, y uint32) uint32) error {
	if len(ops) != 2 {
		return fmt.Errorf("expects 2 register operands")
	}
	x, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	y, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(clearSF(enc(x, y), is32(ops[0])))
	return nil
}

// asmCsel handles `csel Xd, Xn, Xm, <cond>`.
func asmCsel(a *Assembler, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("csel expects Xd, Xn, Xm, cond")
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
	cond, ok := condCodes[ops[3]]
	if !ok {
		return fmt.Errorf("bad condition %q", ops[3])
	}
	a.Emit(clearSF(CSEL(rd, rn, rm, cond), is32(ops[0])))
	return nil
}

// asmCset handles `cset Xd, <cond>`.
func asmCset(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("cset expects Xd, cond")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	cond, ok := condCodes[ops[1]]
	if !ok {
		return fmt.Errorf("bad condition %q", ops[1])
	}
	a.Emit(clearSF(CSET(rd, cond), is32(ops[0])))
	return nil
}

// asmMsub handles `msub Xd, Xn, Xm, Xa`.
func asmMsub(a *Assembler, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("msub expects Xd, Xn, Xm, Xa")
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
	ra, err := parseReg(ops[3])
	if err != nil {
		return err
	}
	a.Emit(clearSF(MSUB(rd, rn, rm, ra), is32(ops[0])))
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
		w := is32(ops[0])
		switch mnem {
		case "lsl":
			if w {
				a.Emit(LSLimmW(rd, rn, sh))
			} else {
				a.Emit(LSLimm(rd, rn, sh))
			}
		case "lsr":
			if w {
				a.Emit(LSRimmW(rd, rn, sh))
			} else {
				a.Emit(LSRimm(rd, rn, sh))
			}
		case "asr":
			if w {
				a.Emit(ASRimmW(rd, rn, sh))
			} else {
				a.Emit(ASRimm(rd, rn, sh))
			}
		}
		return nil
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	w := is32(ops[0])
	switch mnem {
	case "lsl":
		a.Emit(clearSF(LSLV(rd, rn, rm), w))
	case "lsr":
		a.Emit(clearSF(LSRV(rd, rn, rm), w))
	case "asr":
		a.Emit(clearSF(ASRV(rd, rn, rm), w))
	}
	return nil
}

// asmBitfieldExtract handles `ubfx/sbfx Xd, Xn, #lsb, #width`.
func asmBitfieldExtract(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("%s expects Xd, Xn, #lsb, #width", mnem)
	}
	if is32(ops[0]) {
		return fmt.Errorf("32-bit %s not supported yet", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	lsb, err := parseImm(ops[2])
	if err != nil {
		return err
	}
	width, err := parseImm(ops[3])
	if err != nil {
		return err
	}
	if mnem == "ubfx" {
		a.Emit(UBFX(rd, rn, uint32(lsb), uint32(width)))
	} else {
		a.Emit(SBFX(rd, rn, uint32(lsb), uint32(width)))
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
	case "uxtb":
		a.Emit(UXTB(rd, rn))
	case "uxth":
		a.Emit(UXTH(rd, rn))
	}
	return nil
}

// loadStoreSize maps a mnemonic to its access size (0=byte, 1=half,
// 3=doubleword) and load/store direction.
var loadStoreSize = map[string]struct {
	size uint32
	load bool
}{
	"ldr": {3, true}, "str": {3, false},
	"ldrb": {0, true}, "strb": {0, false},
	"ldrh": {1, true}, "strh": {1, false},
}

// asmLoadStore handles the single-register loads and stores in every
// addressing form: unsigned scaled offset, pre-index (`[Xn, #o]!`) and
// post-index (`[Xn], #o`) writeback (signed imm9, all sizes), and the
// register offset `[Xn, Xm{, lsl #3}]` (ldr/str only).
func asmLoadStore(a *Assembler, mnem string, ops []string) error {
	sz := loadStoreSize[mnem]
	is64LdrStr := mnem == "ldr" || mnem == "str"

	// FP register form: `ldr/str Dt, <mem>`. Routed separately since
	// SIMD&FP load/store has its own opcode space.
	if is64LdrStr {
		if vt, single, verr := parseVReg(ops[0]); verr == nil {
			return asmLoadStoreFP(a, mnem, vt, single, ops)
		}
	}

	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	// For ldr/str the access size follows the register width: a
	// w-register is a 32-bit word (size 2), an x-register 64-bit
	// (size 3). The narrow mnemonics (ldrb/ldrh) fix their own size.
	size := sz.size
	if is64LdrStr {
		if is32(ops[0]) {
			size = 2
		} else {
			size = 3
		}
	}

	// Post-index: `<op> Rt, [Xn], #imm` (3 operands).
	if len(ops) == 3 {
		m, err := parseMem(ops[1])
		if err != nil {
			return err
		}
		if m.pre || m.off != 0 || m.hasIndex {
			return fmt.Errorf("%s post-index base must be plain [Xn]", mnem)
		}
		off, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		a.Emit(IdxLoadStore(rt, m.base, int32(off), size, sz.load, false))
		return nil
	}

	if len(ops) != 2 {
		return fmt.Errorf("%s expects a register and a memory operand", mnem)
	}
	m, err := parseMem(ops[1])
	if err != nil {
		return err
	}

	// Register-offset: `<op> Rt, [Xn, Xm{, lsl #s}]`, for every access
	// width (LoadStoreReg is size-general). The scaled shift, when
	// present, must equal log2(access size) — for a byte load that is 0,
	// so `ldrb w0, [x22, x20]` takes the unscaled form.
	if m.hasIndex {
		var scaled bool
		switch {
		case m.indexShift == 0:
			scaled = false
		case m.indexShift == size:
			scaled = true
		default:
			return fmt.Errorf("%s register-offset shift must be lsl #0 or lsl #%d", mnem, size)
		}
		a.Emit(LoadStoreReg(rt, m.base, m.index, size, sz.load, scaled))
		return nil
	}

	// Pre-index writeback: `<op> Rt, [Xn, #imm]!` (all sizes).
	if m.pre {
		a.Emit(IdxLoadStore(rt, m.base, int32(m.off), size, sz.load, true))
		return nil
	}

	// Negative or non-size-aligned displacements can't be expressed by the
	// scaled unsigned-offset form (its 12-bit field is an unsigned multiple
	// of the access size) — route them to the unscaled LDUR/STUR family,
	// whose signed 9-bit byte offset covers -256..255. GAS does the same
	// mnemonic substitution. The self-host arm64 emitter addresses frame
	// locals as `[x29, #-N]`, so this form is pervasive in its output.
	if m.off < 0 || m.off%(1<<size) != 0 {
		if m.off < -256 || m.off > 255 {
			return fmt.Errorf("%s unscaled offset %d out of range (-256..255)", mnem, m.off)
		}
		a.Emit(LoadStoreUnscaled(rt, m.base, int32(m.off), size, sz.load))
		return nil
	}
	a.Emit(LoadStoreUnsigned(rt, m.base, uint32(m.off), size, sz.load))
	return nil
}

// asmLoadStoreFP encodes `ldr/str Dt, <mem>` for a 64-bit FP
// register in the unsigned-offset, post-index and pre-index modes
// the transcendental helpers use. Single-precision (S) loads aren't
// emitted by codegen, so they're a loud gap.
func asmLoadStoreFP(a *Assembler, mnem string, rt uint32, single bool, ops []string) error {
	if single {
		return fmt.Errorf("%s of a single-precision register not supported yet", mnem)
	}
	load := mnem == "ldr"

	// Post-index: `<op> Dt, [Xn], #imm9` (3 operands).
	if len(ops) == 3 {
		m, err := parseMem(ops[1])
		if err != nil {
			return err
		}
		if m.pre || m.off != 0 || m.hasIndex {
			return fmt.Errorf("%s post-index base must be plain [Xn]", mnem)
		}
		off, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		if load {
			a.Emit(LdrFP64PostIdx(rt, m.base, int32(off)))
		} else {
			a.Emit(StrFP64PostIdx(rt, m.base, int32(off)))
		}
		return nil
	}
	if len(ops) != 2 {
		return fmt.Errorf("%s expects a register and a memory operand", mnem)
	}
	m, err := parseMem(ops[1])
	if err != nil {
		return err
	}
	if m.hasIndex {
		return fmt.Errorf("%s register-offset addressing not supported for FP yet", mnem)
	}
	if m.pre {
		if load {
			a.Emit(LdrFP64PreIdx(rt, m.base, int32(m.off)))
		} else {
			a.Emit(StrFP64PreIdx(rt, m.base, int32(m.off)))
		}
		return nil
	}
	if m.off < 0 || m.off%8 != 0 {
		return fmt.Errorf("%s FP offset must be a non-negative multiple of 8, got %d", mnem, m.off)
	}
	imm12 := uint32(m.off) / 8
	if load {
		a.Emit(LdrFP64Unsigned(rt, m.base, imm12))
	} else {
		a.Emit(StrFP64Unsigned(rt, m.base, imm12))
	}
	return nil
}

// asmLoadSigned handles the sign-extending loads ldrsb/ldrsh/ldrsw.
// The destination register width (Wt vs Xt) selects the 32- vs 64-bit
// sign-extension; ldrsw is always 64-bit.
func asmLoadSigned(a *Assembler, mnem string, ops []string) error {
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
		return fmt.Errorf("%s negative offset not supported yet", mnem)
	}
	to64 := !is32(ops[0])
	off := uint32(m.off)
	switch mnem {
	case "ldrsb":
		a.Emit(LDRSB(rt, m.base, off, to64))
	case "ldrsh":
		a.Emit(LDRSH(rt, m.base, off, to64))
	case "ldrsw":
		a.Emit(LDRSW(rt, m.base, off))
	}
	return nil
}

// asmUnscaled handles the LDUR/STUR family: `<op> Rt, [Xn{, #off}]`
// with a signed 9-bit unscaled byte offset.
func asmUnscaled(a *Assembler, mnem string, ops []string) error {
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
		return fmt.Errorf("%s does not take pre-index addressing", mnem)
	}
	if m.off < -256 || m.off > 255 {
		return fmt.Errorf("%s offset %d out of signed 9-bit range", mnem, m.off)
	}
	off := int32(m.off)
	switch mnem {
	case "ldur", "stur":
		// Access size follows the register width (w=32-bit, x=64-bit).
		size := uint32(3)
		if is32(ops[0]) {
			size = 2
		}
		a.Emit(LoadStoreUnscaled(rt, m.base, off, size, mnem == "ldur"))
	case "ldurb":
		a.Emit(LDURB(rt, m.base, off))
	case "sturb":
		a.Emit(STURB(rt, m.base, off))
	}
	return nil
}

// asmPair handles stp/ldp in all three addressing modes: signed offset
// (`[Xn, #imm]`), pre-index (`[Xn, #imm]!`), and post-index
// (`[Xn], #imm`).
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
	load := mnem == "ldp"
	var off int64
	var mode uint32
	switch {
	case m.pre && len(ops) == 3: // [Xn, #imm]!
		off, mode = m.off, PairPre
	case !m.pre && len(ops) == 4: // [Xn], #imm
		if m.off != 0 {
			return fmt.Errorf("%s post-index base must be plain [Xn]", mnem)
		}
		v, err := parseImm(ops[3])
		if err != nil {
			return err
		}
		off, mode = v, PairPost
	case !m.pre && len(ops) == 3: // [Xn, #imm] or [Xn]
		off, mode = m.off, PairOffset
	default:
		return fmt.Errorf("%s: unsupported pair addressing", mnem)
	}
	a.Emit(PairLoadStore(rt, rt2, m.base, int32(off), load, mode))
	return nil
}

// isFReg reports whether an operand names a d-register.
// isFReg reports whether an operand names a floating-point register
// (d-double or s-single).
func isFReg(operand string) bool {
	s := strings.TrimSpace(operand)
	return len(s) > 0 && (s[0] == 'd' || s[0] == 's')
}

// parseVReg parses a d0..d31 or s0..s31 register, returning the number
// and whether it is single-precision (s).
func parseVReg(s string) (reg uint32, single bool, err error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == 'd' || s[0] == 's') {
		n, e := strconv.Atoi(s[1:])
		if e == nil && n >= 0 && n <= 31 {
			return uint32(n), s[0] == 's', nil
		}
	}
	return 0, false, fmt.Errorf("bad fp register %q", s)
}

// asmFloat3 handles fadd/fsub/fmul/fdiv in both precisions; the
// destination register's width selects single vs double.
func asmFloat3(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects 3 fp registers", mnem)
	}
	rd, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	rn, _, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	rm, _, err := parseVReg(ops[2])
	if err != nil {
		return err
	}
	dbl := map[string]func(a, b, c uint32) uint32{"fadd": FADD, "fsub": FSUB, "fmul": FMUL, "fdiv": FDIV}
	sgl := map[string]func(a, b, c uint32) uint32{"fadd": FADDS, "fsub": FSUBS, "fmul": FMULS, "fdiv": FDIVS}
	if single {
		a.Emit(sgl[mnem](rd, rn, rm))
	} else {
		a.Emit(dbl[mnem](rd, rn, rm))
	}
	return nil
}

// asmFNeg handles fneg Dd,Dn / Sd,Sn.
// asmFUnaryD handles a double-precision unary FP op `<op> Dd, Dn`
// (fabs/fsqrt/frint*). The backend only emits the double form; a
// single-precision operand is a loud gap.
func asmFUnaryD(a *Assembler, mnem string, ops []string, enc func(rd, rn uint32) uint32) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects 2 fp registers", mnem)
	}
	rd, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	rn, _, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	if single {
		return fmt.Errorf("%s single-precision form not supported yet", mnem)
	}
	a.Emit(enc(rd, rn))
	return nil
}

func asmFNeg(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("fneg expects 2 fp registers")
	}
	rd, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	rn, _, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	if single {
		a.Emit(FNEGS(rd, rn))
	} else {
		a.Emit(FNEG(rd, rn))
	}
	return nil
}

// asmFcmp handles fcmp Dn,Dm / Sn,Sm.
func asmFcmp(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("fcmp expects 2 fp registers")
	}
	rn, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	// `fcmp Dn, #0.0` — the compare-against-zero form (opc bit 3 set,
	// Rm=0). Only the literal zero exists as an immediate; anything else
	// is a parse error.
	if ops[1] == "#0.0" || ops[1] == "#0" {
		if single {
			a.Emit(FCMPS(rn, 0) | 0x08)
		} else {
			a.Emit(FCMP(rn, 0) | 0x08)
		}
		return nil
	}
	rm, _, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	if single {
		a.Emit(FCMPS(rn, rm))
	} else {
		a.Emit(FCMP(rn, rm))
	}
	return nil
}

// asmFmov handles fmov Dd,Dn / Sd,Sn (fp-fp) and the d<->x bit moves.
func asmFmov(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("fmov expects 2 operands")
	}
	dstF, srcF := isFReg(ops[0]), isFReg(ops[1])
	// `fmov Dd, #imm` — the FP-immediate form. The 8-bit VFP immediate
	// encodes ±(1 + frac/16) × 2^E for frac in 0..15 and E in -3..4;
	// anything outside that set is a loud error (GAS would materialise it
	// from a literal pool, which this single-section assembler doesn't do).
	if dstF && strings.HasPrefix(ops[1], "#") {
		rd, single, err := parseVReg(ops[0])
		if err != nil {
			return err
		}
		imm8, ok := vfpImm8(strings.TrimPrefix(ops[1], "#"))
		if !ok {
			return fmt.Errorf("fmov immediate %q not encodable as a VFP imm8", ops[1])
		}
		if single {
			a.Emit(0x1E201000 | uint32(imm8)<<13 | (rd & regMask))
		} else {
			a.Emit(0x1E601000 | uint32(imm8)<<13 | (rd & regMask))
		}
		return nil
	}
	switch {
	case dstF && srcF: // fmov Dd,Dn or Sd,Sn
		rd, single, err := parseVReg(ops[0])
		if err != nil {
			return err
		}
		rn, _, err := parseVReg(ops[1])
		if err != nil {
			return err
		}
		if single {
			a.Emit(FMOVS(rd, rn))
		} else {
			a.Emit(FMOV(rd, rn))
		}
	case dstF && !srcF: // fmov Dd, Xn
		rd, single, err := parseVReg(ops[0])
		if err != nil {
			return err
		}
		rn, err := parseReg(ops[1])
		if err != nil {
			return err
		}
		if single {
			a.Emit(FMOVSfromGPR(rd, rn))
		} else {
			a.Emit(FMOVfromGPR(rd, rn))
		}
	case !dstF && srcF: // fmov Xd, Dn
		rd, err := parseReg(ops[0])
		if err != nil {
			return err
		}
		rn, single, err := parseVReg(ops[1])
		if err != nil {
			return err
		}
		if single {
			a.Emit(FMOVStoGPR(rd, rn))
		} else {
			a.Emit(FMOVtoGPR(rd, rn))
		}
	default:
		return fmt.Errorf("fmov between two GPRs not supported")
	}
	return nil
}

// asmFcvt handles the precision converts fcvt Dd,Sn and fcvt Sd,Dn.
func asmFcvt(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("fcvt expects 2 fp registers")
	}
	rd, dstSingle, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	rn, srcSingle, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	switch {
	case !dstSingle && srcSingle: // fcvt Dd, Sn
		a.Emit(FCVTStoD(rd, rn))
	case dstSingle && !srcSingle: // fcvt Sd, Dn
		a.Emit(FCVTDtoS(rd, rn))
	default:
		return fmt.Errorf("fcvt operands must differ in precision")
	}
	return nil
}

// asmScvtf handles scvtf Dd,Xn / Sd,Xn (signed int -> float).
func asmScvtf(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("scvtf expects 2 operands")
	}
	rd, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	// A 32-bit (w) source clears the sf bit (bit 31) of the conversion.
	w := is32(ops[1])
	if single {
		a.Emit(clearSF(SCVTFS(rd, rn), w))
	} else {
		a.Emit(clearSF(SCVTF(rd, rn), w))
	}
	return nil
}

// asmUcvtf handles ucvtf Dd,Xn (unsigned int -> double).
func asmUcvtf(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("ucvtf expects 2 operands")
	}
	rd, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	// A 32-bit (w) source clears the sf bit (bit 31), mirroring
	// asmScvtf; the single-dest form uses the type=00 encoder.
	w := is32(ops[1])
	if single {
		a.Emit(clearSF(UCVTFS(rd, rn), w))
	} else {
		a.Emit(clearSF(UCVTF(rd, rn), w))
	}
	return nil
}

// asmFcvtToInt handles fcvtzs/fcvtzu Xd, Dn|Sn (float -> int, trunc).
// encS is the single-source encoder (nil if unsupported).
func asmFcvtToInt(a *Assembler, ops []string, encD, encS func(rd, rn uint32) uint32) error {
	if len(ops) != 2 {
		return fmt.Errorf("expects 2 operands")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, single, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	// The encoders (FCVTZS / FCVTZSS / FCVTZU / FCVTZUS) are the
	// sf=1 (64-bit destination) forms. A `w` destination is the
	// sf=0 form — clearing bit 31 — and saturates to the 32-bit
	// integer range. Without this every `fcvtzs w0, d0` was emitted
	// as the 64-bit conversion, so an out-of-range f→i32 saturated
	// to the i64 limit and the low 32 bits read back as -1 / 0
	// instead of INT32_MAX / INT32_MIN.
	w := is32(ops[0])
	if single {
		if encS == nil {
			return fmt.Errorf("single-precision source not supported yet")
		}
		a.Emit(clearSF(encS(rd, rn), w))
	} else {
		a.Emit(clearSF(encD(rd, rn), w))
	}
	return nil
}

// asmCmn handles `cmn Xn, Xm` and `cmn Xn, #imm`.
func asmCmn(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("cmn expects 2 operands")
	}
	rn, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	w := is32(ops[0])
	if strings.HasPrefix(ops[1], "#") {
		imm, err := parseImm(ops[1])
		if err != nil {
			return err
		}
		i12, sh12, ok := addSubImm12(imm)
		if !ok {
			return fmt.Errorf("cmn immediate %s out of range", ops[1])
		}
		a.Emit(clearSF(CMNimm(rn, i12, sh12), w))
		return nil
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(clearSF(CMN(rn, rm), w))
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
	w := is32(ops[0])
	if strings.HasPrefix(ops[1], "#") {
		imm, err := parseImm(ops[1])
		if err != nil {
			return err
		}
		i12, sh12, ok := addSubImm12(imm)
		if !ok {
			return fmt.Errorf("cmp immediate %s out of range", ops[1])
		}
		a.Emit(clearSF(CMPimm(rn, i12, sh12), w))
		return nil
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(clearSF(CMPreg(rn, rm), w))
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

// asmTestBranch handles tbz/tbnz: `<op> Rt, #bit, label`.
func asmTestBranch(a *Assembler, ops []string, f func(rt, bit uint32, label string)) error {
	if len(ops) != 3 {
		return fmt.Errorf("expects a register, a bit, and a label")
	}
	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	bit, err := parseImm(ops[1])
	if err != nil {
		return err
	}
	f(rt, uint32(bit), ops[2])
	return nil
}

// regLabelWidth assembles a register + label operand pair for the
// cbz/cbnz family, where the operand register's `w`/`x` prefix selects
// the 32-bit (sf=0) vs 64-bit (sf=1) compare — GNU as honours this, and a
// wrong sf bit silently compares the wrong number of bytes. parseReg
// drops the prefix, so we look at it here and dispatch to the 32-bit
// (`w`) or 64-bit (`x`) emitter accordingly.
func regLabelWidth(a *Assembler, ops []string, f64, f32 func(uint32, string)) error {
	if len(ops) != 2 {
		return fmt.Errorf("expects a register and a label")
	}
	r, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	if isWReg(ops[0]) {
		f32(r, ops[1])
	} else {
		f64(r, ops[1])
	}
	return nil
}

// isWReg reports whether a register operand names a 32-bit `w` register
// (including `wzr`), as opposed to a 64-bit `x` register.
func isWReg(s string) bool {
	s = strings.TrimSpace(s)
	return s == "wzr" || (len(s) >= 2 && s[0] == 'w')
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

	// Register-offset form ("[Xn, Xm{, lsl #s}]").
	hasIndex   bool
	index      uint32
	indexShift uint32
}

// parseMem parses a bracketed memory operand: [Xn], [Xn, #imm],
// [Xn, #imm]! (pre-index), or the register-offset [Xn, Xm{, lsl #s}].
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
	if len(inner) == 0 || len(inner) > 3 {
		return m, fmt.Errorf("bad memory operand")
	}
	base, err := parseReg(inner[0])
	if err != nil {
		return m, err
	}
	m.base = base
	if len(inner) == 1 {
		return m, nil
	}
	// Second operand: an immediate offset or a register index.
	if strings.HasPrefix(inner[1], "#") {
		if len(inner) != 2 {
			return m, fmt.Errorf("bad memory operand")
		}
		off, err := parseImm(inner[1])
		if err != nil {
			return m, err
		}
		m.off = off
		return m, nil
	}
	idx, err := parseReg(inner[1])
	if err != nil {
		return m, err
	}
	m.hasIndex = true
	m.index = idx
	if len(inner) == 3 {
		sh, err := parseShiftField(inner, 2, "lsl")
		if err != nil {
			return m, err
		}
		m.indexShift = sh
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

// is32 reports whether an operand names a 32-bit (w) register. For
// the data-processing ALU ops the 32-bit encoding is identical to the
// 64-bit one with the sf bit (bit 31) cleared, so the parser detects
// width from the operands and applies clearSF.
func is32(operand string) bool {
	s := strings.TrimSpace(operand)
	return len(s) > 0 && s[0] == 'w'
}

// clearSF drops the sf bit to turn a 64-bit ALU encoding into its
// 32-bit form, when w32 is set.
func clearSF(insn uint32, w32 bool) uint32 {
	if w32 {
		return insn &^ (1 << 31)
	}
	return insn
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
	return evalIntExpr(s)
}

// evalIntExpr evaluates a constant integer expression made of `+` / `-`
// terms (left to right), matching the subset of GAS expressions the
// backend emits in immediates — e.g. a frame offset like `#96 + 48`,
// which GNU as folds to 144. Each term is an integer literal in any
// base. Plain literals (including a leading sign / hex) take the fast
// path through ParseInt.
func evalIntExpr(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty immediate")
	}
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return v, nil
	}
	var total int64
	op := byte('+')
	first := true
	i, n := 0, len(s)
	for i < n {
		for i < n && s[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}
		if first && (s[i] == '+' || s[i] == '-') {
			op = s[i]
			i++
			for i < n && s[i] == ' ' {
				i++
			}
		}
		first = false
		start := i
		for i < n && s[i] != '+' && s[i] != '-' && s[i] != ' ' {
			i++
		}
		term := s[start:i]
		v, err := strconv.ParseInt(term, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("bad immediate term %q", term)
		}
		if op == '+' {
			total += v
		} else {
			total -= v
		}
		for i < n && s[i] == ' ' {
			i++
		}
		if i < n {
			if s[i] == '+' || s[i] == '-' {
				op = s[i]
				i++
			} else {
				return 0, fmt.Errorf("bad immediate %q", s)
			}
		}
	}
	return total, nil
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

// vfpImm8 computes the AArch64 8-bit VFP immediate for a float literal:
// value = (-1)^s × (1 + frac/16) × 2^E with frac in 0..15, E in -3..4.
// imm8 = s<<7 | ((E-1)&7)<<4 | frac (so 1.0 → 0x70, 2.0 → 0x00). Returns
// ok=false for values outside the encodable set.
func vfpImm8(lit string) (uint8, bool) {
	v, err := strconv.ParseFloat(lit, 64)
	if err != nil || v == 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	var sign uint8
	if v < 0 {
		sign = 1
		v = -v
	}
	fr, exp := math.Frexp(v) // v = fr × 2^exp, fr in [0.5, 1)
	m := fr * 2              // mantissa in [1, 2)
	e := exp - 1
	if e < -3 || e > 4 {
		return 0, false
	}
	frac := (m - 1) * 16
	if frac != math.Trunc(frac) {
		return 0, false
	}
	return sign<<7 | uint8((e-1)&7)<<4 | uint8(frac), true
}
