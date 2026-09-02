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
// Coverage grows brick by brick: labels, the no-op-for-.text
// directives, the integer / bitfield / conditional / scalar-FP /
// load-store (immediate, writeback, register and extended-register
// offset, pairs, exclusives and barriers) instruction forms, and the
// Advanced SIMD surface an integer/float lane kernel needs — the
// arithmetic/compare/min-max/bitwise ops, shifts (immediate, narrowing,
// widening), permutes and shuffles, lane moves, movi, lane-wise FP and
// converts, and ld1/st1/ld1r with post-indexing — across the .8b/.16b/
// .4h/.8h/.2s/.4s/.2d arrangements each encoding actually has. Anything
// not yet supported returns an explicit error (with the offending line)
// rather than silently miscompiling — so coverage gaps are loud.
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
			if err := handleDirective(a, line); err != nil {
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
	// A GAS numeric local label (`1:`) is a label, not an identifier — isIdent
	// rejects a leading digit — so it has to be admitted separately. Without
	// this the line fell through to the instruction dispatch and came back as
	// `unsupported instruction "1:"`, which is what kept the encoding
	// differential to a hand-written snippet instead of whole programs (#6075).
	if _, isNum := isNumericLabelDef(name); !isNum && !isIdent(name) {
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
		// '$' is legal in a GAS symbol (verified: aarch64-linux-gnu-as accepts
		// `foo$wrap0:` and a branch to it). The self-host emitter names every
		// lifted-lambda wrapper that way — `__fn_sort__sort_strings_asc_ci$wrap0`
		// — so rejecting it stopped this assembler from reading any whole
		// program the emitter produces, which is what #6062's alignment needs.
		case r == '_' || r == '.' || r == '$':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// handleDirective accepts (and ignores) the assembler directives that
// don't affect a single-.text-section program, records CFI, and errors on the
// rest (e.g. data directives) so the gap is visible.
//
// `.cfi_startproc` and `.cfi_endproc` used to sit in the ignore list while
// every other `.cfi_*` fell through to the error, which meant CFI-bearing
// input was rejected on its first rule directive — half-accepted is not a
// coherent position for a directive family that describes unwinding.
func handleDirective(a *Assembler, line string) error {
	d := strings.Fields(line)[0]
	if strings.HasPrefix(d, ".cfi_") {
		return a.cfiDirective(d, line)
	}
	switch d {
	case ".text", ".arch", ".global", ".globl", ".type", ".size",
		".align", ".p2align", ".balign", ".ltorg":
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

	// SIMD forms are recognised by the first operand being a vN.<arr>
	// register (or lane), so dual-form mnemonics — add, mvn, fadd, scvtf, … —
	// keep their scalar path when the operands are scalar.
	if len(ops) > 0 && isVecArrOperand(ops[0]) {
		if handled, err := asmVecForm(a, mnem, ops); handled {
			return err
		}
	}

	switch mnem {
	case "mov":
		return asmMov(a, ops)
	case "movz", "movk", "movn":
		return asmMoveWide(a, mnem, ops)
	case "add", "sub", "adds", "subs":
		return asmAddSub(a, mnem, ops)
	case "and", "orr", "eor", "mul", "udiv", "sdiv", "umulh", "adc", "sbc",
		"adcs", "sbcs", "smulh", "ands", "bic", "bics", "orn", "eon":
		return asm3Reg(a, mnem, ops)
	case "ngc":
		return asm2Reg(a, ops, NGC)
	case "ngcs":
		return asm2Reg(a, ops, NGCS)
	case "madd":
		return asm4Reg(a, mnem, ops, MADD)
	case "smull", "umull", "smaddl", "umaddl", "smsubl", "umsubl":
		return asmMulLong(a, mnem, ops)
	case "tst":
		return asmTst(a, ops)
	case "mvn":
		return asmMvn(a, ops)
	case "negs":
		return asmNeg(a, mnem, ops)
	case "extr":
		return asmExtr(a, ops)
	case "ror":
		return asmRor(a, ops)
	case "bfi", "bfxil", "ubfiz", "sbfiz":
		return asmBitfieldInsert(a, mnem, ops)
	case "ccmp", "ccmn":
		return asmCondCmp(a, mnem, ops)
	case "csinc", "csinv", "csneg":
		return asmCondSel(a, mnem, ops)
	case "cinc", "cinv", "cneg":
		return asmCondAlias(a, mnem, ops)
	case "csetm":
		return asmCsetm(a, ops)
	case "csel":
		return asmCsel(a, ops)
	case "cset":
		return asmCset(a, ops)
	case "cmn":
		return asmCmn(a, ops)
	case "neg":
		return asmNeg(a, mnem, ops)
	case "clz":
		return asm2Reg(a, ops, CLZ)
	case "cls":
		return asm2Reg(a, ops, CLS)
	case "rbit":
		return asm2Reg(a, ops, RBIT)
	case "rev":
		return asmRev(a, ops)
	case "rev32":
		return asmRev32(a, ops)
	case "addv", "smaxv", "sminv", "umaxv", "uminv", "saddlv", "uaddlv":
		return asmVecAcross(a, mnem, ops)
	case "umov":
		return asmUmov(a, ops)
	case "smov":
		return asmSmov(a, ops)
	case "movi":
		return asmMovi(a, ops)
	case "ld1", "st1":
		return asmLdSt1(a, mnem, ops)
	case "ld1r":
		return asmLd1r(a, ops)
	case "msub":
		return asm4Reg(a, mnem, ops, MSUB)
	case "mrs":
		return asmMrs(a, ops)
	case "msr":
		return asmMsrWrite(a, ops)
	case "fadd", "fsub", "fmul", "fdiv", "fnmul", "fmin", "fmax", "fminnm", "fmaxnm":
		return asmFloat3(a, mnem, ops)
	case "fmadd", "fmsub", "fnmadd", "fnmsub":
		return asmFMulAdd(a, mnem, ops)
	case "fcsel":
		return asmFcsel(a, ops)
	case "fccmp":
		return asmFccmp(a, ops)
	case "fneg":
		return asmFNeg(a, ops)
	case "fabs":
		return asmFUnary(a, "fabs", ops, FABS)
	case "fsqrt":
		return asmFUnary(a, "fsqrt", ops, FSQRT)
	case "frintm":
		return asmFUnary(a, "frintm", ops, FRINTM)
	case "frintp":
		return asmFUnary(a, "frintp", ops, FRINTP)
	case "frintz":
		return asmFUnary(a, "frintz", ops, FRINTZ)
	case "frinta":
		return asmFUnary(a, "frinta", ops, FRINTA)
	case "frintn":
		return asmFUnary(a, "frintn", ops, FRINTN)
	case "fcmp":
		return asmFcmp(a, ops, false)
	case "fcmpe":
		return asmFcmp(a, ops, true)
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
	case "ldur", "stur", "ldurb", "sturb", "ldurh", "sturh":
		return asmUnscaled(a, mnem, ops)
	case "ldrsb", "ldrsh", "ldrsw":
		return asmLoadSigned(a, mnem, ops)
	case "ldursb", "ldursh", "ldursw":
		return asmLoadSignedUnscaled(a, mnem, ops)
	case "stp", "ldp":
		return asmPair(a, mnem, ops)
	case "ldxr", "ldaxr", "ldxrb", "ldaxrb", "ldxrh", "ldaxrh":
		return asmLoadExclusive(a, mnem, ops)
	case "stxr", "stlxr", "stxrb", "stlxrb", "stxrh", "stlxrh":
		return asmStoreExclusive(a, mnem, ops)
	case "ldar", "ldarb", "ldarh":
		return asmAcqRel(a, mnem, ops, LDAR)
	case "stlr", "stlrb", "stlrh":
		return asmAcqRel(a, mnem, ops, STLR)
	case "dmb", "dsb":
		return asmBarrier(a, mnem, ops)
	case "isb":
		// Only the full-system option exists for isb; `isb` and `isb sy`
		// are the same instruction.
		if len(ops) > 1 || (len(ops) == 1 && ops[0] != "sy") {
			return fmt.Errorf("isb takes no operand (or sy)")
		}
		a.Emit(ISB())
		return nil
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
	case "nop":
		// The assembler emits `nop` itself, padding a veneer island to an
		// even instruction count, so it reads one back too.
		if len(ops) != 0 {
			return fmt.Errorf("nop takes no operands")
		}
		a.Emit(nopInsn)
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
	case "brk":
		if len(ops) != 1 {
			return fmt.Errorf("brk expects 1 operand")
		}
		imm, err := parseImm(ops[0])
		if err != nil {
			return err
		}
		a.Emit(BRK(uint16(imm)))
		return nil
	default:
		if sug := suggestMnemonic(mnem); sug != "" {
			return fmt.Errorf("unsupported instruction %q (did you mean %q?)", mnem, sug)
		}
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
	switch mnem {
	case "movz":
		a.Emit(clearSF(MOVZ(rd, uint16(imm), shift), w))
	case "movn":
		// move-wide-NOT: the complement of (imm16 << shift). The encoder has
		// been here all along; only the mnemonic was missing, so `movn` came
		// back as "unsupported instruction" and the self-host assembler's own
		// movn support had no oracle to check against (#6075).
		// Verified against GNU as: movn x0,#99 = 0x92800c60,
		// movn x3,#1,lsl #16 = 0x92a00023, movn w5,#7 = 0x128000e5.
		a.Emit(clearSF(MOVN(rd, uint16(imm), shift), w))
	default:
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
			// The 32-bit form has no 64-bit offset register to extend.
			if w && (opt == 3 || opt == 7) {
				return fmt.Errorf("%s: %s is not a valid extend for a w destination", mnem, kind[0])
			}
			if isAdd {
				emit(clearSF(ADDextReg(rd, rn, rm, opt, amt), w))
			} else {
				emit(clearSF(SUBextReg(rd, rn, rm, opt, amt), w))
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
	// umulh/smulh produce the high half of the 128-bit product; there is
	// no 32-bit form of the instruction at all.
	if (mnem == "umulh" || mnem == "smulh") && (w || is32(ops[1]) || is32(ops[2])) {
		return fmt.Errorf("%s takes only x registers", mnem)
	}
	// and/orr/eor/ands take a logical (bitmask) immediate as the third
	// operand; the others are register-only. In particular bic/orn/eon
	// have NO immediate encoding (the bitmask class has no invert bit) —
	// GAS aliases `bic Rd, Rn, #v` to `and Rd, Rn, #~v`, but silently
	// complementing here would hide which instruction was actually
	// encoded, so the alias is refused.
	if strings.HasPrefix(ops[2], "#") {
		var insn uint32
		var ok bool
		imm, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		switch mnem {
		case "and":
			insn, ok = ANDimm(rd, rn, uint64(imm), !w)
		case "orr":
			insn, ok = ORRimm(rd, rn, uint64(imm), !w)
		case "eor":
			insn, ok = EORimm(rd, rn, uint64(imm), !w)
		case "ands":
			insn, ok = ANDSimm(rd, rn, uint64(imm), !w)
		default:
			return fmt.Errorf("%s does not take an immediate operand", mnem)
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
	// `orr w3, w1, w1, lsl #8`. The multiplies, divides, and carry ops
	// take no shift.
	if len(ops) > 4 {
		return fmt.Errorf("%s: too many operands", mnem)
	}
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
		case "ands":
			a.Emit(clearSF(ANDSregShift(rd, rn, rm, st, amt), w))
		case "bic":
			a.Emit(clearSF(BICregShift(rd, rn, rm, st, amt), w))
		case "bics":
			a.Emit(clearSF(BICSregShift(rd, rn, rm, st, amt), w))
		case "orn":
			a.Emit(clearSF(ORNregShift(rd, rn, rm, st, amt), w))
		case "eon":
			a.Emit(clearSF(EONregShift(rd, rn, rm, st, amt), w))
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
	case "ands":
		a.Emit(clearSF(ANDSregShift(rd, rn, rm, 0, 0), w))
	case "bic":
		a.Emit(clearSF(BICregShift(rd, rn, rm, 0, 0), w))
	case "bics":
		a.Emit(clearSF(BICSregShift(rd, rn, rm, 0, 0), w))
	case "orn":
		a.Emit(clearSF(ORNregShift(rd, rn, rm, 0, 0), w))
	case "eon":
		a.Emit(clearSF(EONregShift(rd, rn, rm, 0, 0), w))
	case "mul":
		a.Emit(clearSF(MUL(rd, rn, rm), w))
	case "udiv":
		a.Emit(clearSF(UDIV(rd, rn, rm), w))
	case "sdiv":
		a.Emit(clearSF(SDIV(rd, rn, rm), w))
	case "umulh":
		// No 32-bit form exists, so this one keeps its SF bit.
		a.Emit(UMULH(rd, rn, rm))
	case "smulh":
		a.Emit(SMULH(rd, rn, rm))
	case "adc":
		a.Emit(clearSF(ADC(rd, rn, rm), w))
	case "adcs":
		a.Emit(clearSF(ADCS(rd, rn, rm), w))
	case "sbc":
		a.Emit(clearSF(SBC(rd, rn, rm), w))
	case "sbcs":
		a.Emit(clearSF(SBCS(rd, rn, rm), w))
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

// ---- Advanced SIMD (vector) forms ----
//
// The operand grammar is arrangement-driven: every op accepts exactly the
// element sizes its encoding has bits for, pinned against GNU as, and any
// other pairing is refused up front — within the SIMD classes most wrong
// size/Q combinations are a VALID encoding of a different instruction.

// vecArr is a parsed vector arrangement: element size (0=B, 1=H, 2=S, 3=D)
// and whether it is the 128-bit (Q) form.
type vecArr struct {
	size uint32
	q    bool
}

func (t vecArr) String() string {
	names := [4][2]string{{"8b", "16b"}, {"4h", "8h"}, {"2s", "4s"}, {"1d", "2d"}}
	if t.q {
		return names[t.size][1]
	}
	return names[t.size][0]
}

// esize is the element width in bits.
func (t vecArr) esize() int64 { return 8 << t.size }

var vecArrNames = map[string]vecArr{
	"8b": {0, false}, "16b": {0, true},
	"4h": {1, false}, "8h": {1, true},
	"2s": {2, false}, "4s": {2, true},
	"1d": {3, false}, "2d": {3, true},
}

// parseVecArr parses a `vN.<arr>` vector operand. `.1d` parses — ld1/st1
// have it — but checkArr refuses it everywhere else.
func parseVecArr(s string) (reg uint32, t vecArr, err error) {
	s = strings.TrimSpace(s)
	dot := strings.IndexByte(s, '.')
	if len(s) < 2 || s[0] != 'v' || dot < 0 {
		return 0, t, fmt.Errorf("bad vector register %q", s)
	}
	n, e := strconv.Atoi(s[1:dot])
	if e != nil || n < 0 || n > 31 {
		return 0, t, fmt.Errorf("bad vector register %q", s)
	}
	t, ok := vecArrNames[s[dot+1:]]
	if !ok {
		return 0, t, fmt.Errorf("unsupported vector arrangement %q", s)
	}
	return uint32(n), t, nil
}

// isVecArrOperand reports whether an operand is a vector register with an
// arrangement or lane suffix (`v3.16b`, `v0.s[1]`) — what routes a dual-form
// mnemonic (add, mvn, fadd, scvtf, …) to its SIMD form.
func isVecArrOperand(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) > 1 && s[0] == 'v' && s[1] >= '0' && s[1] <= '9' &&
		strings.IndexByte(s, '.') > 0
}

// Element-size masks for checkArr: which arrangements an op's encoding has.
const (
	arrB    = 1 << 0
	arrH    = 1 << 1
	arrS    = 1 << 2
	arrD    = 1 << 3
	arrBHS  = arrB | arrH | arrS
	arrBHSD = arrBHS | arrD
	arrSD   = arrS | arrD
)

// checkArr validates an arrangement against an op's allowed element sizes.
// `.1d` is refused across the data-processing classes: 64-bit lanes exist
// only in the full-width (Q) form.
func checkArr(mnem string, t vecArr, sizes uint32) error {
	if sizes&(1<<t.size) == 0 || (t.size == 3 && !t.q) {
		return fmt.Errorf("%s does not support the .%s arrangement", mnem, t)
	}
	return nil
}

// parseVecArrN parses n vector operands that must share one arrangement.
func parseVecArrN(mnem string, ops []string, n int) (regs []uint32, t vecArr, err error) {
	regs = make([]uint32, n)
	for i := 0; i < n; i++ {
		r, ti, err := parseVecArr(ops[i])
		if err != nil {
			return nil, t, err
		}
		if i == 0 {
			t = ti
		} else if ti != t {
			return nil, t, fmt.Errorf("%s operands must share an arrangement: %q vs %q", mnem, ops[0], ops[i])
		}
		regs[i] = r
	}
	return regs, t, nil
}

// The per-mnemonic tables: U bit, opcode, and which element sizes the
// encoding has. Every size set here — and every one deliberately absent
// (mul/min/max have no .2d, the bitwise ops and cnt are byte-only, …) —
// matches what GNU as accepts.

var vecInt3Ops = map[string]struct {
	u      bool
	opcode uint32
	sizes  uint32
}{
	"add":   {false, 0x10, arrBHSD},
	"sub":   {true, 0x10, arrBHSD},
	"mul":   {false, 0x13, arrBHS},
	"cmeq":  {true, 0x11, arrBHSD},
	"cmtst": {false, 0x11, arrBHSD},
	"cmgt":  {false, 0x06, arrBHSD},
	"cmge":  {false, 0x07, arrBHSD},
	"cmhi":  {true, 0x06, arrBHSD},
	"cmhs":  {true, 0x07, arrBHSD},
	"smax":  {false, 0x0C, arrBHS},
	"smin":  {false, 0x0D, arrBHS},
	"umax":  {true, 0x0C, arrBHS},
	"umin":  {true, 0x0D, arrBHS},
	"sshl":  {false, 0x08, arrBHSD},
	"ushl":  {true, 0x08, arrBHSD},
}

// The bitwise three-register ops put the operation in the size field, so
// they exist only in the byte arrangements.
var vecLogical3Ops = map[string]struct {
	u    bool
	size uint32
}{
	"and": {false, 0}, "bic": {false, 1}, "orr": {false, 2}, "orn": {false, 3},
	"eor": {true, 0},
}

// Compare against zero (two-register misc; #0 is part of the opcode, not an
// immediate field — a non-zero immediate is no instruction at all).
var vecCmpZeroOps = map[string]struct {
	u      bool
	opcode uint32
}{
	"cmeq": {false, 0x09}, "cmgt": {false, 0x08}, "cmge": {true, 0x08},
	"cmle": {true, 0x09}, "cmlt": {false, 0x0A},
}

var vecInt2MiscOps = map[string]struct {
	u      bool
	opcode uint32
	sizes  uint32
}{
	"neg":   {true, 0x0B, arrBHSD},
	"abs":   {false, 0x0B, arrBHSD},
	"not":   {true, 0x05, arrB},
	"mvn":   {true, 0x05, arrB}, // alias of not
	"cnt":   {false, 0x05, arrB},
	"rev16": {false, 0x01, arrB},
	"rev32": {true, 0x00, arrB | arrH},
	"rev64": {false, 0x00, arrBHS},
}

// The lane-wise FP ops read the size field as (szHi<<1 | sz) where sz picks
// S vs D lanes, so the tables carry szHi instead of a size mask; the
// arrangements are 2s/4s/2d throughout.
var vecFP3Ops = map[string]struct {
	u      bool
	opcode uint32
	szHi   bool
}{
	"fadd":  {false, 0x1A, false},
	"fsub":  {false, 0x1A, true},
	"fmul":  {true, 0x1B, false},
	"fdiv":  {true, 0x1F, false},
	"fmax":  {false, 0x1E, false},
	"fmin":  {false, 0x1E, true},
	"fcmeq": {false, 0x1C, false},
	"fcmge": {true, 0x1C, false},
	"fcmgt": {true, 0x1C, true},
}

// FP compare against zero (two-register misc; the operand is spelled #0.0).
// fcmle/fcmlt exist only in this form — their register-operand spellings are
// the swapped-operand fcmge/fcmgt.
var vecFPCmpZeroOps = map[string]struct {
	u      bool
	opcode uint32
}{
	"fcmeq": {false, 0x0D}, "fcmgt": {false, 0x0C}, "fcmge": {true, 0x0C},
	"fcmle": {true, 0x0D}, "fcmlt": {false, 0x0E},
}

var vecFP2MiscOps = map[string]struct {
	u      bool
	opcode uint32
	szHi   bool
}{
	"fneg":   {true, 0x0F, true},
	"fabs":   {false, 0x0F, true},
	"fsqrt":  {true, 0x1F, true},
	"scvtf":  {false, 0x1D, false},
	"ucvtf":  {true, 0x1D, false},
	"fcvtzs": {false, 0x1B, true},
	"fcvtzu": {true, 0x1B, true},
}

var vecShiftImmOps = map[string]struct {
	u      bool
	opcode uint32
	left   bool
}{
	"shl":  {false, 0x0A, true},
	"sli":  {true, 0x0A, true},
	"sshr": {false, 0x00, false},
	"ushr": {true, 0x00, false},
	"sri":  {true, 0x08, false},
}

var vecPermuteOps = map[string]uint32{
	"zip1": 3, "zip2": 7, "uzp1": 1, "uzp2": 5, "trn1": 2, "trn2": 6,
}

// asmVecForm dispatches the mnemonics whose SIMD form was recognised from
// the first operand. handled=false means the mnemonic has no such form and
// the caller falls through to the scalar dispatch.
func asmVecForm(a *Assembler, mnem string, ops []string) (handled bool, err error) {
	if _, ok := vecCmpZeroOps[mnem]; ok {
		// cmlt/cmle exist only against zero; cmeq/cmgt/cmge also have the
		// three-register form, told apart by the last operand.
		if mnem == "cmlt" || mnem == "cmle" ||
			(len(ops) == 3 && strings.HasPrefix(strings.TrimSpace(ops[2]), "#")) {
			return true, asmVecCmpZero(a, mnem, ops)
		}
	}
	if _, ok := vecFPCmpZeroOps[mnem]; ok {
		if mnem == "fcmlt" || mnem == "fcmle" ||
			(len(ops) == 3 && strings.HasPrefix(strings.TrimSpace(ops[2]), "#")) {
			return true, asmVecFPCmpZero(a, mnem, ops)
		}
	}
	if _, ok := vecInt3Ops[mnem]; ok {
		return true, asmVecInt3Same(a, mnem, ops)
	}
	if _, ok := vecLogical3Ops[mnem]; ok {
		return true, asmVecLogical3(a, mnem, ops)
	}
	if _, ok := vecFP3Ops[mnem]; ok {
		return true, asmVecFP3Same(a, mnem, ops)
	}
	if _, ok := vecInt2MiscOps[mnem]; ok {
		return true, asmVecInt2Misc(a, mnem, ops)
	}
	if _, ok := vecFP2MiscOps[mnem]; ok {
		return true, asmVecFP2Misc(a, mnem, ops)
	}
	if _, ok := vecShiftImmOps[mnem]; ok {
		return true, asmVecShiftImm(a, mnem, ops)
	}
	if _, ok := vecPermuteOps[mnem]; ok {
		return true, asmVecPermute(a, mnem, ops)
	}
	switch mnem {
	case "xtn", "xtn2":
		return true, asmVecXtn(a, mnem, ops)
	case "shrn", "shrn2":
		return true, asmVecShrn(a, mnem, ops)
	case "sshll", "ushll", "sshll2", "ushll2", "sxtl", "uxtl", "sxtl2", "uxtl2":
		return true, asmVecShll(a, mnem, ops)
	case "ext":
		return true, asmVecExt(a, ops)
	case "tbl":
		return true, asmVecTbl(a, ops)
	case "dup":
		return true, asmVecDup(a, ops)
	case "ins":
		return true, asmVecIns(a, ops)
	case "movi":
		return true, asmMovi(a, ops)
	}
	return false, nil
}

func asmVecInt3Same(a *Assembler, mnem string, ops []string) error {
	e := vecInt3Ops[mnem]
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, Vm.<T>", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops, 3)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, e.sizes); err != nil {
		return err
	}
	a.Emit(Vec3Same(r[0], r[1], r[2], e.opcode, t.size, t.q, e.u))
	return nil
}

func asmVecLogical3(a *Assembler, mnem string, ops []string) error {
	e := vecLogical3Ops[mnem]
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, Vm.<T>", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops, 3)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrB); err != nil {
		return err
	}
	a.Emit(Vec3Same(r[0], r[1], r[2], 0x03, e.size, t.q, e.u))
	return nil
}

// fpSize builds the FP size field: szHi<<1 | (D lanes).
func fpSize(t vecArr, szHi bool) uint32 {
	s := uint32(0)
	if t.size == 3 {
		s = 1
	}
	if szHi {
		s |= 2
	}
	return s
}

func asmVecFP3Same(a *Assembler, mnem string, ops []string) error {
	e := vecFP3Ops[mnem]
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, Vm.<T>", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops, 3)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrSD); err != nil {
		return err
	}
	a.Emit(Vec3Same(r[0], r[1], r[2], e.opcode, fpSize(t, e.szHi), t.q, e.u))
	return nil
}

func asmVecCmpZero(a *Assembler, mnem string, ops []string) error {
	e := vecCmpZeroOps[mnem]
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, #0", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops[:2], 2)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrBHSD); err != nil {
		return err
	}
	imm, err := parseImm(ops[2])
	if err != nil {
		return err
	}
	if imm != 0 {
		return fmt.Errorf("%s takes only the compare-against-zero form, got #%d", mnem, imm)
	}
	a.Emit(Vec2Misc(r[0], r[1], e.opcode, t.size, t.q, e.u))
	return nil
}

func asmVecFPCmpZero(a *Assembler, mnem string, ops []string) error {
	e := vecFPCmpZeroOps[mnem]
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, #0.0", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops[:2], 2)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrSD); err != nil {
		return err
	}
	if imm := strings.TrimSpace(ops[2]); imm != "#0.0" && imm != "#0" {
		return fmt.Errorf("%s takes only the #0.0 immediate, got %q", mnem, imm)
	}
	a.Emit(Vec2Misc(r[0], r[1], e.opcode, fpSize(t, true), t.q, e.u))
	return nil
}

func asmVecInt2Misc(a *Assembler, mnem string, ops []string) error {
	e := vecInt2MiscOps[mnem]
	if len(ops) != 2 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops, 2)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, e.sizes); err != nil {
		return err
	}
	a.Emit(Vec2Misc(r[0], r[1], e.opcode, t.size, t.q, e.u))
	return nil
}

func asmVecFP2Misc(a *Assembler, mnem string, ops []string) error {
	e := vecFP2MiscOps[mnem]
	if len(ops) != 2 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops, 2)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrSD); err != nil {
		return err
	}
	a.Emit(Vec2Misc(r[0], r[1], e.opcode, fpSize(t, e.szHi), t.q, e.u))
	return nil
}

func asmVecShiftImm(a *Assembler, mnem string, ops []string) error {
	e := vecShiftImmOps[mnem]
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, #shift", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops[:2], 2)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrBHSD); err != nil {
		return err
	}
	sh, err := parseImm(ops[2])
	if err != nil {
		return err
	}
	// Left shifts encode esize+shift (0..esize-1), right shifts
	// 2*esize-shift (1..esize); out of range the immh:immb split would
	// reinterpret the top bits as a different element size.
	es := t.esize()
	var immhb int64
	if e.left {
		if sh < 0 || sh >= es {
			return fmt.Errorf("%s .%s shift must be 0..%d, got %d", mnem, t, es-1, sh)
		}
		immhb = es + sh
	} else {
		if sh < 1 || sh > es {
			return fmt.Errorf("%s .%s shift must be 1..%d, got %d", mnem, t, es, sh)
		}
		immhb = 2*es - sh
	}
	a.Emit(VecShiftImm(r[0], r[1], e.opcode, uint32(immhb), t.q, e.u))
	return nil
}

// narrowWidePair validates the arrangement pair of the narrowing/widening
// ops: `narrow` is the half-width arrangement (its Q bit is the 2-suffix,
// selecting which half of the 128-bit side is touched), `wide` the
// full-width one a size up.
func narrowWidePair(mnem string, narrow, wide vecArr, hi bool) error {
	if narrow.size > 2 || narrow.q != hi || wide.size != narrow.size+1 || !wide.q {
		return fmt.Errorf("%s: unsupported arrangement pair .%s, .%s", mnem, narrow, wide)
	}
	return nil
}

// asmVecXtn handles `xtn{2} Vd.<T>, Vn.<Tw>` — extract narrow, halving each
// lane of the wider source; xtn writes the destination's low half, xtn2 the
// high half.
func asmVecXtn(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<Tw>", mnem)
	}
	rd, td, err := parseVecArr(ops[0])
	if err != nil {
		return err
	}
	rn, ts, err := parseVecArr(ops[1])
	if err != nil {
		return err
	}
	if err := narrowWidePair(mnem, td, ts, mnem == "xtn2"); err != nil {
		return err
	}
	a.Emit(Vec2Misc(rd, rn, 0x12, td.size, td.q, false))
	return nil
}

// asmVecShrn handles `shrn{2} Vd.<T>, Vn.<Tw>, #shift` — shift right and
// narrow. It is how a NEON kernel gets a comparison mask into a GPR at all:
// x86 has pmovmskb, NEON does not, so the idiom is shrn #4 to compress each
// 16-bit lane's flag nibble into a byte, then fmov the 64-bit half out.
func asmVecShrn(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<Tw>, #shift", mnem)
	}
	rd, td, err := parseVecArr(ops[0])
	if err != nil {
		return err
	}
	rn, ts, err := parseVecArr(ops[1])
	if err != nil {
		return err
	}
	if err := narrowWidePair(mnem, td, ts, mnem == "shrn2"); err != nil {
		return err
	}
	sh, err := parseImm(ops[2])
	if err != nil {
		return err
	}
	es := td.esize()
	if sh < 1 || sh > es {
		return fmt.Errorf("%s .%s shift must be 1..%d, got %d", mnem, td, es, sh)
	}
	a.Emit(VecShiftImm(rd, rn, 0x10, uint32(2*es-sh), td.q, false))
	return nil
}

// asmVecShll handles `sshll{2}/ushll{2} Vd.<Tw>, Vn.<T>, #shift` — shift
// left and widen — and the shift-0 aliases sxtl/uxtl: the lane-wise sign/
// zero extends.
func asmVecShll(a *Assembler, mnem string, ops []string) error {
	base := strings.TrimSuffix(mnem, "2")
	alias := base == "sxtl" || base == "uxtl"
	want := 3
	if alias {
		want = 2
	}
	if len(ops) != want {
		return fmt.Errorf("%s expects %d operands", mnem, want)
	}
	rd, td, err := parseVecArr(ops[0])
	if err != nil {
		return err
	}
	rn, ts, err := parseVecArr(ops[1])
	if err != nil {
		return err
	}
	hi := strings.HasSuffix(mnem, "2")
	if err := narrowWidePair(mnem, ts, td, hi); err != nil {
		return err
	}
	var sh int64
	if !alias {
		if sh, err = parseImm(ops[2]); err != nil {
			return err
		}
	}
	es := ts.esize()
	if sh < 0 || sh >= es {
		return fmt.Errorf("%s .%s shift must be 0..%d, got %d", mnem, ts, es-1, sh)
	}
	a.Emit(VecShiftImm(rd, rn, 0x14, uint32(es+sh), hi, base[0] == 'u'))
	return nil
}

func asmVecPermute(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Vd.<T>, Vn.<T>, Vm.<T>", mnem)
	}
	r, t, err := parseVecArrN(mnem, ops, 3)
	if err != nil {
		return err
	}
	if err := checkArr(mnem, t, arrBHSD); err != nil {
		return err
	}
	a.Emit(VecPermute(r[0], r[1], r[2], vecPermuteOps[mnem], t.size, t.q))
	return nil
}

// asmVecExt handles `ext Vd.<T>, Vn.<T>, Vm.<T>, #index` — byte extract
// from the Vn:Vm pair. Byte arrangements only; the index counts bytes.
func asmVecExt(a *Assembler, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("ext expects Vd.<T>, Vn.<T>, Vm.<T>, #index")
	}
	r, t, err := parseVecArrN("ext", ops[:3], 3)
	if err != nil {
		return err
	}
	if err := checkArr("ext", t, arrB); err != nil {
		return err
	}
	idx, err := parseImm(ops[3])
	if err != nil {
		return err
	}
	max := int64(7)
	if t.q {
		max = 15
	}
	if idx < 0 || idx > max {
		return fmt.Errorf("ext .%s index must be 0..%d, got %d", t, max, idx)
	}
	a.Emit(VecExt(r[0], r[1], r[2], uint32(idx), t.q))
	return nil
}

// asmVecTbl handles the single-table-register `tbl Vd.<T>, {Vn.16b}, Vm.<T>`.
// The table is always .16b; destination and index registers share a byte
// arrangement.
func asmVecTbl(a *Assembler, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("tbl expects Vd.<T>, {Vn.16b}, Vm.<T>")
	}
	rd, td, err := parseVecArr(ops[0])
	if err != nil {
		return err
	}
	tbl, tt, err := parseVecList("tbl", ops[1])
	if err != nil {
		return err
	}
	if len(tbl) != 1 || tt != (vecArr{0, true}) {
		return fmt.Errorf("tbl table must be a single {Vn.16b} register, got %q", ops[1])
	}
	rm, tm, err := parseVecArr(ops[2])
	if err != nil {
		return err
	}
	if td != tm {
		return fmt.Errorf("tbl operands must share an arrangement: %q vs %q", ops[0], ops[2])
	}
	if err := checkArr("tbl", td, arrB); err != nil {
		return err
	}
	a.Emit(VecTbl1(rd, tbl[0], rm, td.q))
	return nil
}

// vecImm5 builds the copy-class lane selector: the element size as the
// lowest set bit, the lane index above it.
func vecImm5(size, index uint32) uint32 {
	return (index<<1 | 1) << size
}

// asmVecDup handles both dup forms: `dup Vd.<T>, Rn` (broadcast a GPR — a W
// register below doubleword lanes, X for them) and `dup Vd.<T>, Vn.<Ts>[i]`
// (broadcast one lane).
func asmVecDup(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("dup expects Vd.<T>, Rn or Vd.<T>, Vn.<Ts>[index]")
	}
	rd, t, err := parseVecArr(ops[0])
	if err != nil {
		return err
	}
	if err := checkArr("dup", t, arrBHSD); err != nil {
		return err
	}
	if strings.Contains(ops[1], "[") {
		rn, size, idx, err := parseVecLane(ops[1])
		if err != nil {
			return err
		}
		if size != t.size {
			return fmt.Errorf("dup lane %q does not match arrangement .%s", ops[1], t)
		}
		a.Emit(VecDupElem(rd, rn, vecImm5(size, idx), t.q))
		return nil
	}
	if is32(ops[1]) == (t.size == 3) {
		return fmt.Errorf("dup .%s source must be a %c register, got %q", t, "wx"[t.size/3], ops[1])
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(VecDupGPR(rd, rn, 1<<t.size, t.q))
	return nil
}

// asmVecIns handles `ins Vd.<Ts>[i], Rn` (from a GPR — W below doubleword
// lanes, X for them) and `ins Vd.<Ts>[i], Vn.<Ts>[j]` (lane to lane).
func asmVecIns(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("ins expects Vd.<Ts>[i], Rn or Vd.<Ts>[i], Vn.<Ts>[j]")
	}
	rd, size, idx, err := parseVecLane(ops[0])
	if err != nil {
		return err
	}
	if isVecArrOperand(ops[1]) {
		rn, ssize, sidx, err := parseVecLane(ops[1])
		if err != nil {
			return err
		}
		if ssize != size {
			return fmt.Errorf("ins lanes must share an element size: %q vs %q", ops[0], ops[1])
		}
		a.Emit(VecInsElem(rd, rn, vecImm5(size, idx), sidx<<size))
		return nil
	}
	if is32(ops[1]) == (size == 3) {
		return fmt.Errorf("ins %q source must be a %c register, got %q", ops[0], "wx"[size/3], ops[1])
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(VecInsGPR(rd, rn, vecImm5(size, idx)))
	return nil
}

// asmUmov handles `umov Wd, Vn.<b|h|s>[i]` and `umov Xd, Vn.d[i]` — the
// zero-extending lane extract. The destination width is fixed by the lane
// size, so a mismatch is refused rather than reinterpreted.
func asmUmov(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("umov expects Rd, Vn.<Ts>[index]")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, size, idx, err := parseVecLane(ops[1])
	if err != nil {
		return err
	}
	if is32(ops[0]) == (size == 3) {
		return fmt.Errorf("umov %q does not match a .%c lane", ops[0], "bhsd"[size])
	}
	a.Emit(VecUmov(rd, rn, vecImm5(size, idx), size == 3))
	return nil
}

// asmSmov handles `smov Wd|Xd, Vn.<Ts>[i]` — sign-extending lane extract.
// W destinations take b/h lanes, X destinations b/h/s; extending a lane to
// its own width is umov's job, so those pairings do not exist here.
func asmSmov(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("smov expects Rd, Vn.<Ts>[index]")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, size, idx, err := parseVecLane(ops[1])
	if err != nil {
		return err
	}
	w := is32(ops[0])
	if (w && size > 1) || size > 2 {
		return fmt.Errorf("smov %q does not take a .%c lane", ops[0], "bhsd"[size])
	}
	a.Emit(VecSmov(rd, rn, vecImm5(size, idx), !w))
	return nil
}

var vecAcrossOps = map[string]struct {
	u      bool
	opcode uint32
	widen  bool
}{
	"addv":   {false, 0x1B, false},
	"smaxv":  {false, 0x0A, false},
	"sminv":  {false, 0x1A, false},
	"umaxv":  {true, 0x0A, false},
	"uminv":  {true, 0x1A, false},
	"saddlv": {false, 0x03, true},
	"uaddlv": {true, 0x03, true},
}

// asmVecAcross handles the across-lanes reductions (`addv Bd, Vn.16b`, the
// min/max reductions, and the widening saddlv/uaddlv): the destination is a
// scalar whose class matches the element size — one size up for the widening
// pair. `.2s` and `.2d` have no across-lanes form.
func asmVecAcross(a *Assembler, mnem string, ops []string) error {
	e := vecAcrossOps[mnem]
	if len(ops) != 2 {
		return fmt.Errorf("%s expects a scalar destination and Vn.<T>", mnem)
	}
	class, rd, err := parseScalarReg(ops[0])
	if err != nil {
		return err
	}
	rn, t, err := parseVecArr(ops[1])
	if err != nil {
		return err
	}
	if t.size == 3 || (t.size == 2 && !t.q) {
		return fmt.Errorf("%s does not support the .%s arrangement", mnem, t)
	}
	want := t.size
	if e.widen {
		want++
	}
	if class != want {
		return fmt.Errorf("%s destination %q does not match the .%s arrangement", mnem, ops[0], t)
	}
	a.Emit(VecAcross(rd, rn, e.opcode, t.size, t.q, e.u))
	return nil
}

// asmMovi handles the modified-immediate moves: `movi Vd.<T>, #imm8` with
// the byte-position shifts the element size has, and the 64-bit bytemask
// form `movi Vd.2d, #imm64` / `movi Dd, #imm64` (each byte 0x00 or 0xff).
func asmMovi(a *Assembler, ops []string) error {
	if len(ops) < 2 || len(ops) > 3 {
		return fmt.Errorf("movi expects Vd.<T>|Dd, #imm{, lsl #shift}")
	}
	first := strings.TrimSpace(ops[0])
	if !strings.Contains(first, ".") {
		// Scalar form: the bytemask into a D register.
		class, rd, err := parseScalarReg(first)
		if err != nil || class != 3 {
			return fmt.Errorf("movi scalar destination must be a D register, got %q", ops[0])
		}
		return emitMoviBytemask(a, rd, ops, false)
	}
	rd, t, err := parseVecArr(ops[0])
	if err != nil {
		return err
	}
	if err := checkArr("movi", t, arrBHSD); err != nil {
		return err
	}
	if t.size == 3 {
		return emitMoviBytemask(a, rd, ops, true)
	}
	imm, err := parseImm(ops[1])
	if err != nil {
		return err
	}
	if imm < 0 || imm > 255 {
		return fmt.Errorf("movi immediate must be 0..255, got %d", imm)
	}
	shift, err := parseShiftField(ops, 2, "lsl")
	if err != nil {
		return err
	}
	var cmode uint32
	switch t.size {
	case 0:
		if shift != 0 {
			return fmt.Errorf("movi .%s takes no shift", t)
		}
		cmode = 0xE
	case 1:
		if shift != 0 && shift != 8 {
			return fmt.Errorf("movi .%s shift must be 0 or 8, got %d", t, shift)
		}
		cmode = 0x8 | shift/8<<1
	case 2:
		if shift%8 != 0 || shift > 24 {
			return fmt.Errorf("movi .%s shift must be 0, 8, 16 or 24, got %d", t, shift)
		}
		cmode = shift / 8 << 1
	}
	a.Emit(VecMovi(rd, uint32(imm), cmode, t.q, false))
	return nil
}

func emitMoviBytemask(a *Assembler, rd uint32, ops []string, q bool) error {
	if len(ops) != 2 {
		return fmt.Errorf("movi bytemask form takes no shift")
	}
	s := strings.TrimPrefix(strings.TrimSpace(ops[1]), "#")
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return fmt.Errorf("bad movi immediate %q", ops[1])
	}
	var imm8 uint32
	for i := 0; i < 8; i++ {
		switch b := v >> (8 * i) & 0xFF; b {
		case 0xFF:
			imm8 |= 1 << i
		case 0:
		default:
			return fmt.Errorf("movi 64-bit immediate %q is not a bytemask (every byte 0x00 or 0xff)", ops[1])
		}
	}
	a.Emit(VecMovi(rd, imm8, 0xE, q, true))
	return nil
}

// asmLdSt1 handles ld1/st1 of a register list — one to four consecutive
// registers, every arrangement including .1d — with no writeback, post-index
// by immediate, or post-index by register. The immediate must equal the
// transfer size: the encoding has no immediate field, Rm=31 just means
// "advance by the size".
func asmLdSt1(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 2 || len(ops) > 3 {
		return fmt.Errorf("%s expects a register list, [Xn] and an optional post-index", mnem)
	}
	regs, t, err := parseVecList(mnem, ops[0])
	if err != nil {
		return err
	}
	rn, err := parseBracketedBase(ops[1])
	if err != nil {
		return err
	}
	nregs := uint32(len(regs))
	load := mnem == "ld1"
	if len(ops) == 2 {
		a.Emit(VecLdSt1(regs[0], rn, 0, nregs, t.size, t.q, load, false))
		return nil
	}
	step := int64(nregs) * 8
	if t.q {
		step *= 2
	}
	rm, err := parsePostIndex(mnem, ops[2], step)
	if err != nil {
		return err
	}
	a.Emit(VecLdSt1(regs[0], rn, rm, nregs, t.size, t.q, load, true))
	return nil
}

// asmLd1r handles `ld1r {Vt.<T>}, [Xn]` — load one element and replicate —
// with the same post-index forms as ld1 (the immediate is the ELEMENT size).
func asmLd1r(a *Assembler, ops []string) error {
	if len(ops) < 2 || len(ops) > 3 {
		return fmt.Errorf("ld1r expects {Vt.<T>}, [Xn] and an optional post-index")
	}
	regs, t, err := parseVecList("ld1r", ops[0])
	if err != nil {
		return err
	}
	if len(regs) != 1 {
		return fmt.Errorf("ld1r takes a single-register list")
	}
	rn, err := parseBracketedBase(ops[1])
	if err != nil {
		return err
	}
	if len(ops) == 2 {
		a.Emit(VecLd1R(regs[0], rn, 0, t.size, t.q, false))
		return nil
	}
	rm, err := parsePostIndex("ld1r", ops[2], 1<<t.size)
	if err != nil {
		return err
	}
	a.Emit(VecLd1R(regs[0], rn, rm, t.size, t.q, true))
	return nil
}

// parseVecList parses `{Vt.<T>}` … `{Vt.<T>, …+3}` — up to four registers,
// consecutive mod 32, sharing an arrangement. The braces are part of the
// syntax, not decoration, so they are required rather than tolerated:
// accepting a bare `vN.16b` would let a typo assemble as something else.
func parseVecList(mnem, op string) ([]uint32, vecArr, error) {
	var t vecArr
	list := strings.TrimSpace(op)
	if !strings.HasPrefix(list, "{") || !strings.HasSuffix(list, "}") {
		return nil, t, fmt.Errorf("%s expects a register list {Vt.<T>, ...}, got %q", mnem, op)
	}
	parts := splitOperands(strings.TrimSpace(list[1 : len(list)-1]))
	if len(parts) < 1 || len(parts) > 4 {
		return nil, t, fmt.Errorf("%s register list must name 1..4 registers", mnem)
	}
	regs := make([]uint32, len(parts))
	for i, p := range parts {
		r, ti, err := parseVecArr(p)
		if err != nil {
			return nil, t, err
		}
		if i == 0 {
			t = ti
		} else {
			if ti != t {
				return nil, t, fmt.Errorf("%s list registers must share an arrangement: %q vs %q", mnem, parts[0], p)
			}
			if r != (regs[i-1]+1)%32 {
				return nil, t, fmt.Errorf("%s list registers must be consecutive: %q after %q", mnem, p, parts[i-1])
			}
		}
		regs[i] = r
	}
	return regs, t, nil
}

// parsePostIndex parses the post-index operand of the structure loads: an
// immediate — which must equal `want`, since the encoding has no immediate
// field, only "advance by the transfer size" — or an X register (number 31
// is reserved to mean the immediate form, so sp/xzr cannot be the index).
func parsePostIndex(mnem, op string, want int64) (uint32, error) {
	op = strings.TrimSpace(op)
	if strings.HasPrefix(op, "#") {
		imm, err := parseImm(op)
		if err != nil {
			return 0, err
		}
		if imm != want {
			return 0, fmt.Errorf("%s post-index immediate must be #%d (the transfer size), got #%d", mnem, want, imm)
		}
		return 31, nil
	}
	if is32(op) || op == "sp" || op == "xzr" {
		return 0, fmt.Errorf("%s post-index register must be x0..x30, got %q", mnem, op)
	}
	return parseReg(op)
}

// parseVecLane parses `vN.<b|h|s|d>[i]` — one lane of a vector register.
func parseVecLane(s string) (reg, size, index uint32, err error) {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '[')
	dot := strings.IndexByte(s, '.')
	if !strings.HasPrefix(s, "v") || dot < 0 || open < dot || !strings.HasSuffix(s, "]") {
		return 0, 0, 0, fmt.Errorf("expected vN.<Ts>[index], got %q", s)
	}
	switch s[dot+1 : open] {
	case "b":
		size = 0
	case "h":
		size = 1
	case "s":
		size = 2
	case "d":
		size = 3
	default:
		return 0, 0, 0, fmt.Errorf("bad lane element size in %q", s)
	}
	n, e := strconv.Atoi(s[1:dot])
	if e != nil || n < 0 || n > 31 {
		return 0, 0, 0, fmt.Errorf("bad vector register %q", s)
	}
	i, e := strconv.Atoi(s[open+1 : len(s)-1])
	if e != nil || i < 0 || i > int(15>>size) {
		return 0, 0, 0, fmt.Errorf("lane index in %q out of range 0..%d", s, 15>>size)
	}
	return uint32(n), size, uint32(i), nil
}

// parseScalarReg parses a bN/hN/sN/dN scalar SIMD register, returning its
// element-size class (0=B, 1=H, 2=S, 3=D) and number.
func parseScalarReg(s string) (class, reg uint32, err error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if i := strings.IndexByte("bhsd", s[0]); i >= 0 {
			n, e := strconv.Atoi(s[1:])
			if e == nil && n >= 0 && n <= 31 {
				return uint32(i), uint32(n), nil
			}
		}
	}
	return 0, 0, fmt.Errorf("bad scalar SIMD register %q", s)
}

// parseBracketedBase parses `[xN]` — the base-register-only addressing form
// the structure loads take. An offset would be a DIFFERENT encoding, so
// anything beyond a bare register is rejected rather than ignored.
func parseBracketedBase(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return 0, fmt.Errorf("expected [xN], got %q", s)
	}
	return parseReg(strings.TrimSpace(s[1 : len(s)-1]))
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
	cond, err := condOperand(ops[3], false)
	if err != nil {
		return err
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
	cond, err := condOperand(ops[1], true)
	if err != nil {
		return err
	}
	a.Emit(clearSF(CSET(rd, cond), is32(ops[0])))
	return nil
}

// asm4Reg handles the four-register multiply-accumulates
// `madd/msub Rd, Rn, Rm, Ra` (the destination width selects W vs X).
func asm4Reg(a *Assembler, mnem string, ops []string, enc func(rd, rn, rm, ra uint32) uint32) error {
	if len(ops) != 4 {
		return fmt.Errorf("%s expects Rd, Rn, Rm, Ra", mnem)
	}
	var r [4]uint32
	for i, op := range ops {
		v, err := parseReg(op)
		if err != nil {
			return err
		}
		r[i] = v
	}
	a.Emit(clearSF(enc(r[0], r[1], r[2], r[3]), is32(ops[0])))
	return nil
}

// asmMulLong handles the widening multiplies: `smaddl/umaddl/smsubl/
// umsubl Xd, Wn, Wm, Xa` and the Ra=XZR aliases `smull/umull Xd, Wn,
// Wm`. The widths are part of the instruction (no sf bit), so a wrong
// register class is refused rather than reinterpreted.
func asmMulLong(a *Assembler, mnem string, ops []string) error {
	alias := mnem == "smull" || mnem == "umull"
	want := 4
	if alias {
		want = 3
	}
	if len(ops) != want {
		return fmt.Errorf("%s expects %d operands", mnem, want)
	}
	if is32(ops[0]) || !is32(ops[1]) || !is32(ops[2]) {
		return fmt.Errorf("%s operands must be Xd, Wn, Wm", mnem)
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
	ra := uint32(31)
	if !alias {
		if is32(ops[3]) {
			return fmt.Errorf("%s accumulator must be an x register, got %q", mnem, ops[3])
		}
		if ra, err = parseReg(ops[3]); err != nil {
			return err
		}
	}
	switch mnem {
	case "smull", "smaddl":
		a.Emit(SMADDL(rd, rn, rm, ra))
	case "umull", "umaddl":
		a.Emit(UMADDL(rd, rn, rm, ra))
	case "smsubl":
		a.Emit(SMSUBL(rd, rn, rm, ra))
	case "umsubl":
		a.Emit(UMSUBL(rd, rn, rm, ra))
	}
	return nil
}

// asmTst handles `tst Rn, Rm{, <shift> #amt}` and `tst Rn, #bitmask` —
// the ANDS aliases with Rd=XZR.
func asmTst(a *Assembler, ops []string) error {
	if len(ops) < 2 || len(ops) > 3 {
		return fmt.Errorf("tst expects Rn, Rm|#imm{, shift}")
	}
	rn, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	w := is32(ops[0])
	if strings.HasPrefix(ops[1], "#") {
		if len(ops) != 2 {
			return fmt.Errorf("tst immediate form takes no shift")
		}
		imm, err := parseImm(ops[1])
		if err != nil {
			return err
		}
		insn, ok := ANDSimm(31, rn, uint64(imm), !w)
		if !ok {
			return fmt.Errorf("tst: %s is not an encodable bitmask immediate", ops[1])
		}
		a.Emit(insn)
		return nil
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	var st, amt uint32
	if len(ops) > 2 {
		if st, amt, err = parseRegShift(ops[2]); err != nil {
			return err
		}
	}
	a.Emit(clearSF(ANDSregShift(31, rn, rm, st, amt), w))
	return nil
}

// asmNeg handles `neg/negs Rd, Rm{, shift}` — sub(s) Rd, zr, Rm, shift.
func asmNeg(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 2 || len(ops) > 3 {
		return fmt.Errorf("%s expects Rd, Rm{, shift}", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	var st, amt uint32
	if len(ops) > 2 {
		if st, amt, err = parseRegShift(ops[2]); err != nil {
			return err
		}
	}
	insn := SUBregShift(rd, 31, rm, st, amt)
	if mnem == "negs" {
		insn |= 1 << 29
	}
	a.Emit(clearSF(insn, is32(ops[0])))
	return nil
}

// asmMvn handles `mvn Rd, Rm{, <shift> #amt}` — the ORN alias with
// Rn=XZR.
func asmMvn(a *Assembler, ops []string) error {
	if len(ops) < 2 || len(ops) > 3 {
		return fmt.Errorf("mvn expects Rd, Rm{, shift}")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	var st, amt uint32
	if len(ops) > 2 {
		if st, amt, err = parseRegShift(ops[2]); err != nil {
			return err
		}
	}
	a.Emit(clearSF(ORNregShift(rd, 31, rm, st, amt), is32(ops[0])))
	return nil
}

// asmExtr handles `extr Rd, Rn, Rm, #lsb`. lsb is bounded by the
// register width; the encoders mask, so an unchecked value would wrap
// into a different (valid) extract.
func asmExtr(a *Assembler, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("extr expects Rd, Rn, Rm, #lsb")
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
	lsb, err := parseImm(ops[3])
	if err != nil {
		return err
	}
	return emitExtr(a, rd, rn, rm, lsb, is32(ops[0]))
}

func emitExtr(a *Assembler, rd, rn, rm uint32, lsb int64, w bool) error {
	size := int64(64)
	if w {
		size = 32
	}
	if lsb < 0 || lsb >= size {
		return fmt.Errorf("extr/ror shift %d out of range 0..%d", lsb, size-1)
	}
	if w {
		a.Emit(EXTRW(rd, rn, rm, uint32(lsb)))
	} else {
		a.Emit(EXTR(rd, rn, rm, uint32(lsb)))
	}
	return nil
}

// asmRor handles the standalone rotates: `ror Rd, Rn, #n` (the
// EXTR Rd, Rn, Rn, #n alias) and `ror Rd, Rn, Rm` (RORV).
func asmRor(a *Assembler, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("ror expects 3 operands")
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
		n, err := parseImm(ops[2])
		if err != nil {
			return err
		}
		return emitExtr(a, rd, rn, rn, n, is32(ops[0]))
	}
	rm, err := parseReg(ops[2])
	if err != nil {
		return err
	}
	a.Emit(clearSF(RORV(rd, rn, rm), is32(ops[0])))
	return nil
}

// asmBitfieldInsert handles the BFM/UBFM/SBFM insert aliases
// `bfi/bfxil/ubfiz/sbfiz Rd, Rn, #lsb, #width`, range-checked like
// asmBitfieldExtract (the raw immr/imms fields would wrap silently).
func asmBitfieldInsert(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("%s expects Rd, Rn, #lsb, #width", mnem)
	}
	w32 := is32(ops[0])
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
	size := int64(64)
	if w32 {
		size = 32
	}
	if lsb < 0 || width < 1 || lsb+width > size {
		return fmt.Errorf("%s: field [%d,+%d) out of range for a %d-bit register", mnem, lsb, width, size)
	}
	// bfi/ubfiz/sbfiz place a field AT lsb: immr = (-lsb) mod size,
	// imms = width-1. bfxil extracts FROM lsb: immr = lsb,
	// imms = lsb+width-1.
	immr := uint32((size - lsb) % size)
	imms := uint32(width - 1)
	if mnem == "bfxil" {
		immr = uint32(lsb)
		imms = uint32(lsb + width - 1)
	}
	switch {
	case mnem == "ubfiz" && w32:
		a.Emit(ubfmW(rd, rn, immr, imms))
	case mnem == "ubfiz":
		a.Emit(ubfmX(rd, rn, immr, imms))
	case mnem == "sbfiz" && w32:
		a.Emit(sbfmW(rd, rn, immr, imms))
	case mnem == "sbfiz":
		a.Emit(sbfmX(rd, rn, immr, imms))
	case w32: // bfi / bfxil
		a.Emit(bfmW(rd, rn, immr, imms))
	default:
		a.Emit(bfmX(rd, rn, immr, imms))
	}
	return nil
}

// asmCondCmp handles `ccmp/ccmn Rn, Rm|#imm5, #nzcv, <cond>`. The
// immediate operand is UNSIGNED 0..31 (it is a 5-bit field, not an
// add/sub imm12), and nzcv is the 4-bit flag pattern used when cond
// fails.
func asmCondCmp(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("%s expects Rn, Rm|#imm5, #nzcv, cond", mnem)
	}
	rn, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	nzcv, err := parseImm(ops[2])
	if err != nil {
		return err
	}
	if nzcv < 0 || nzcv > 15 {
		return fmt.Errorf("%s nzcv %d out of range 0..15", mnem, nzcv)
	}
	cond, err := condOperand(ops[3], false)
	if err != nil {
		return err
	}
	w := is32(ops[0])
	if strings.HasPrefix(ops[1], "#") {
		imm, err := parseImm(ops[1])
		if err != nil {
			return err
		}
		if imm < 0 || imm > 31 {
			return fmt.Errorf("%s immediate %d out of range 0..31", mnem, imm)
		}
		if mnem == "ccmp" {
			a.Emit(clearSF(CCMPimm(rn, uint32(imm), uint32(nzcv), cond), w))
		} else {
			a.Emit(clearSF(CCMNimm(rn, uint32(imm), uint32(nzcv), cond), w))
		}
		return nil
	}
	rm, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	if mnem == "ccmp" {
		a.Emit(clearSF(CCMPreg(rn, rm, uint32(nzcv), cond), w))
	} else {
		a.Emit(clearSF(CCMNreg(rn, rm, uint32(nzcv), cond), w))
	}
	return nil
}

var condSelEnc = map[string]func(rd, rn, rm, cond uint32) uint32{
	"csinc": CSINC, "csinv": CSINV, "csneg": CSNEG,
}

// asmCondSel handles `csinc/csinv/csneg Rd, Rn, Rm, <cond>`.
func asmCondSel(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("%s expects Rd, Rn, Rm, cond", mnem)
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
	cond, err := condOperand(ops[3], false)
	if err != nil {
		return err
	}
	a.Emit(clearSF(condSelEnc[mnem](rd, rn, rm, cond), is32(ops[0])))
	return nil
}

// asmCondAlias handles `cinc/cinv/cneg Rd, Rn, <cond>` — the
// csinc/csinv/csneg aliases with Rn=Rm and the condition INVERTED
// (like cset), so the operation applies when cond holds.
func asmCondAlias(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Rd, Rn, cond", mnem)
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	cond, err := condOperand(ops[2], true)
	if err != nil {
		return err
	}
	enc := condSelEnc["cs"+mnem[1:]]
	a.Emit(clearSF(enc(rd, rn, rn, cond^1), is32(ops[0])))
	return nil
}

// asmCsetm handles `csetm Rd, <cond>` — Rd = cond ? -1 : 0, the
// CSINV Rd, XZR, XZR, invert(cond) alias.
func asmCsetm(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("csetm expects Rd, cond")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	cond, err := condOperand(ops[1], true)
	if err != nil {
		return err
	}
	a.Emit(clearSF(CSINV(rd, 31, 31, cond^1), is32(ops[0])))
	return nil
}

// asmRev handles `rev Rd, Rn`. The X and W encodings differ in the opc
// field, not just sf, so this cannot go through asm2Reg + clearSF.
func asmRev(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("rev expects 2 operands")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	if is32(ops[0]) {
		a.Emit(REVW(rd, rn))
	} else {
		a.Emit(REV64(rd, rn))
	}
	return nil
}

// asmRev32 handles `rev32 Xd, Xn`. X only: the 32-bit-wide operation is
// spelled `rev Wd, Wn`.
func asmRev32(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("rev32 expects 2 operands")
	}
	if is32(ops[0]) || is32(ops[1]) {
		return fmt.Errorf("rev32 takes only x registers (use rev for a w register)")
	}
	rd, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rn, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	a.Emit(REV32(rd, rn))
	return nil
}

// sysRegs maps the system-register names the backend uses to their
// op0:op1:CRn:CRm:op2 encoding, plus whether msr may target them from
// EL0. An unlisted name is an error rather than a guessed encoding.
var sysRegs = map[string]struct {
	enc      [5]uint32
	writable bool
}{
	"cntfrq_el0": {enc: [5]uint32{3, 3, 14, 0, 0}},
	"cntvct_el0": {enc: [5]uint32{3, 3, 14, 0, 2}},
	"dczid_el0":  {enc: [5]uint32{3, 3, 0, 0, 7}},
	"tpidr_el0":  {enc: [5]uint32{3, 3, 13, 0, 2}, writable: true},
	"nzcv":       {enc: [5]uint32{3, 3, 4, 2, 0}, writable: true},
	"fpcr":       {enc: [5]uint32{3, 3, 4, 4, 0}, writable: true},
	"fpsr":       {enc: [5]uint32{3, 3, 4, 4, 1}, writable: true},
}

// asmMrs handles `mrs Xt, <sysreg>`.
func asmMrs(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("mrs expects Xt, sysreg")
	}
	if is32(ops[0]) {
		return fmt.Errorf("mrs destination must be an x register, got %q", ops[0])
	}
	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(ops[1]))
	f, ok := sysRegs[name]
	if !ok {
		return fmt.Errorf("unsupported system register %q", ops[1])
	}
	a.Emit(MRS(rt, f.enc[0], f.enc[1], f.enc[2], f.enc[3], f.enc[4]))
	return nil
}

// asmMsrWrite handles `msr <sysreg>, Xt` (the register form). A
// read-only register (dczid_el0, the counter-timers) is refused: the
// encoding would exist but the write traps or is UNDEFINED at EL0.
func asmMsrWrite(a *Assembler, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("msr expects sysreg, Xt")
	}
	if is32(ops[1]) {
		return fmt.Errorf("msr source must be an x register, got %q", ops[1])
	}
	rt, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(ops[0]))
	f, ok := sysRegs[name]
	if !ok {
		return fmt.Errorf("unsupported system register %q", ops[0])
	}
	if !f.writable {
		return fmt.Errorf("system register %q is not writable", ops[0])
	}
	a.Emit(MSRreg(rt, f.enc[0], f.enc[1], f.enc[2], f.enc[3], f.enc[4]))
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
	w32 := is32(ops[0])
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
	if w32 {
		// The 32-bit encoding's immr/imms are 5-bit: the extracted field must
		// lie inside the register.
		if lsb < 0 || width < 1 || lsb+width > 32 {
			return fmt.Errorf("%s: field [%d,+%d) out of range for a 32-bit register", mnem, lsb, width)
		}
		if mnem == "ubfx" {
			a.Emit(UBFXW(rd, rn, uint32(lsb), uint32(width)))
		} else {
			a.Emit(SBFXW(rd, rn, uint32(lsb), uint32(width)))
		}
		return nil
	}
	if lsb < 0 || width < 1 || lsb+width > 64 {
		return fmt.Errorf("%s: field [%d,+%d) out of range for a 64-bit register", mnem, lsb, width)
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
	if !is32(ops[1]) {
		return fmt.Errorf("%s source must be a w register, got %q", mnem, ops[1])
	}
	// SBFM's sf and N bits must match, so the 32-bit sxtb/sxth clear both;
	// clearing only sf is an UNALLOCATED encoding.
	const sfN = 1<<31 | 1<<22
	w := is32(ops[0])
	switch mnem {
	case "sxtb":
		insn := SXTB(rd, rn)
		if w {
			insn &^= sfN
		}
		a.Emit(insn)
	case "sxth":
		insn := SXTH(rd, rn)
		if w {
			insn &^= sfN
		}
		a.Emit(insn)
	case "sxtw":
		if w {
			return fmt.Errorf("sxtw destination must be an x register, got %q", ops[0])
		}
		a.Emit(SXTW(rd, rn))
	case "uxtb", "uxth":
		// A 32-bit result zero-extends to 64 bits anyway, so the x-form
		// alias does not exist; encoding it as the w form would be silent.
		if !w {
			return fmt.Errorf("%s destination must be a w register, got %q", mnem, ops[0])
		}
		if mnem == "uxtb" {
			a.Emit(UXTB(rd, rn))
		} else {
			a.Emit(UXTH(rd, rn))
		}
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
// register/extended-register offset `[Xn, Xm{, lsl|sxtx {#s}}]` /
// `[Xn, Wm, uxtw|sxtw {#s}]` (all sizes).
func asmLoadStore(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 2 && len(ops) != 3 {
		return fmt.Errorf("%s expects a register and a memory operand", mnem)
	}
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

	m, err := parseMem(ops[1])
	if err != nil {
		return err
	}

	// Register-offset: `<op> Rt, [Xn, Xm{, lsl #s}]` or the
	// extended-register `[Xn, Wm, uxtw|sxtw {#s}]` / `[Xn, Xm, sxtx {#s}]`,
	// for every access width (LoadStoreRegExt is size-general). The
	// amount, when present, must be 0 or log2(access size). For a byte
	// access both are 0 — but an explicit `#0` still sets the S bit,
	// which is how GNU as distinguishes `[x1, w2, uxtw]` from
	// `[x1, w2, uxtw #0]`.
	if m.hasIndex {
		var scaled bool
		switch {
		case !m.indexHasAmt:
			scaled = false
		case m.indexAmt == size:
			scaled = true
		case m.indexAmt == 0:
			scaled = false
		default:
			return fmt.Errorf("%s register-offset shift must be #0 or #%d", mnem, size)
		}
		a.Emit(LoadStoreRegExt(rt, m.base, m.index, size, m.indexOpt, sz.load, scaled))
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
	// The scaled field is 12 bits, so the largest offset it reaches is
	// 4095*size. One step past that wraps to zero rather than overflowing
	// out of the instruction: `ldr x1, [x2, #32768]` would encode as
	// `ldr x1, [x2]`, a valid instruction addressing the wrong memory. gas
	// refuses it; so must this.
	if m.off>>size > 4095 {
		return fmt.Errorf("%s scaled offset %d out of range (0..%d in steps of %d)", mnem, m.off, 4095<<size, 1<<size)
	}
	a.Emit(LoadStoreUnsigned(rt, m.base, uint32(m.off), size, sz.load))
	return nil
}

// asmLoadStoreFP encodes `ldr/str Dt|St, <mem>` for a scalar FP
// register in the unsigned-offset, post-index and pre-index modes the
// transcendental helpers use, plus the unscaled fallback.
func asmLoadStoreFP(a *Assembler, mnem string, rt uint32, single bool, ops []string) error {
	load := mnem == "ldr"
	scale := int64(8)
	unsigned, postIdx, preIdx, unscaled := StrFP64Unsigned, StrFP64PostIdx, StrFP64PreIdx, SturFP64
	switch {
	case load && single:
		unsigned, postIdx, preIdx, unscaled = LdrFP32Unsigned, LdrFP32PostIdx, LdrFP32PreIdx, LdurFP32
	case load:
		unsigned, postIdx, preIdx, unscaled = LdrFP64Unsigned, LdrFP64PostIdx, LdrFP64PreIdx, LdurFP64
	case single:
		unsigned, postIdx, preIdx, unscaled = StrFP32Unsigned, StrFP32PostIdx, StrFP32PreIdx, SturFP32
	}
	if single {
		scale = 4
	}

	// Post-index: `<op> Vt, [Xn], #imm9` (3 operands).
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
		a.Emit(postIdx(rt, m.base, int32(off)))
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
		a.Emit(preIdx(rt, m.base, int32(m.off)))
		return nil
	}
	if m.off < 0 || m.off%scale != 0 {
		// Unscaled (LDUR/STUR) territory: a negative or non-size-aligned
		// displacement, which the scaled unsigned form cannot encode. GNU as
		// rewrites `str d0, [x12, #-8]` to `stur` silently, so accepting the
		// `str`/`ldr` spelling here is matching the reference assembler rather
		// than being lenient.
		if m.off < -256 || m.off > 255 {
			return fmt.Errorf("%s FP offset %d is neither a non-negative multiple of %d nor in the unscaled range [-256,255]", mnem, m.off, scale)
		}
		a.Emit(unscaled(rt, m.base, int32(m.off)))
		return nil
	}
	if m.off/scale > 4095 {
		return fmt.Errorf("%s FP scaled offset %d out of range (0..%d in steps of %d)", mnem, m.off, 4095*scale, scale)
	}
	a.Emit(unsigned(rt, m.base, uint32(m.off/scale)))
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
	if m.hasIndex {
		return fmt.Errorf("%s register-offset addressing not supported yet", mnem)
	}
	to64 := !is32(ops[0])
	var scale int64
	var size uint32
	switch mnem {
	case "ldrsb":
		scale, size = 1, 0
	case "ldrsh":
		scale, size = 2, 1
	case "ldrsw":
		if !to64 {
			return fmt.Errorf("ldrsw destination must be an x register, got %q", ops[0])
		}
		scale, size = 4, 2
	}
	if m.off >= 0 && m.off%scale == 0 && m.off/scale <= 4095 {
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
	// Negative or unaligned: the unscaled LDURS* encodings, as GNU as
	// routes them.
	if m.off < -256 || m.off > 255 {
		return fmt.Errorf("%s offset %d is neither a non-negative multiple of %d nor in the unscaled range [-256,255]", mnem, m.off, scale)
	}
	a.Emit(LoadSignedUnscaled(rt, m.base, int32(m.off), size, to64))
	return nil
}

// asmUnscaled handles the LDUR/STUR family: `<op> Rt, [Xn{, #off}]`
// with a signed 9-bit unscaled byte offset.
func asmUnscaled(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects a register and a memory operand", mnem)
	}
	// FP register form: `ldur/stur Dt|St, [Xn, #imm9]`. SIMD&FP load/store
	// has its own opcode space, exactly as in asmLoadStore above.
	if mnem == "ldur" || mnem == "stur" {
		if vt, single, verr := parseVReg(ops[0]); verr == nil {
			m, merr := parseMem(ops[1])
			if merr != nil {
				return merr
			}
			if m.pre || m.hasIndex {
				return fmt.Errorf("%s takes a plain [Xn, #imm9] operand", mnem)
			}
			if m.off < -256 || m.off > 255 {
				return fmt.Errorf("%s offset %d out of signed 9-bit range", mnem, m.off)
			}
			switch {
			case mnem == "ldur" && single:
				a.Emit(LdurFP32(vt, m.base, int32(m.off)))
			case mnem == "ldur":
				a.Emit(LdurFP64(vt, m.base, int32(m.off)))
			case single:
				a.Emit(SturFP32(vt, m.base, int32(m.off)))
			default:
				a.Emit(SturFP64(vt, m.base, int32(m.off)))
			}
			return nil
		}
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
	case "ldurh":
		a.Emit(LoadStoreUnscaled(rt, m.base, off, 1, true))
	case "sturh":
		a.Emit(LoadStoreUnscaled(rt, m.base, off, 1, false))
	}
	return nil
}

// asmLoadSignedUnscaled handles ldursb/ldursh/ldursw — the sign-
// extending loads with a signed 9-bit unscaled offset `[Xn{, #imm9}]`.
func asmLoadSignedUnscaled(a *Assembler, mnem string, ops []string) error {
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
	if m.pre || m.hasIndex {
		return fmt.Errorf("%s takes a plain [Xn, #imm9] operand", mnem)
	}
	if m.off < -256 || m.off > 255 {
		return fmt.Errorf("%s offset %d out of signed 9-bit range", mnem, m.off)
	}
	to64 := !is32(ops[0])
	var size uint32
	switch mnem {
	case "ldursb":
		size = 0
	case "ldursh":
		size = 1
	case "ldursw":
		if !to64 {
			return fmt.Errorf("ldursw destination must be an x register, got %q", ops[0])
		}
		size = 2
	}
	a.Emit(LoadSignedUnscaled(rt, m.base, int32(m.off), size, to64))
	return nil
}

// asmPair handles stp/ldp — for X, W, and D register pairs — in all
// three addressing modes: signed offset (`[Xn, #imm]`), pre-index
// (`[Xn, #imm]!`), and post-index (`[Xn], #imm`). The register class
// picks the encoding and the offset scale (W: 4, X/D: 8).
func asmPair(a *Assembler, mnem string, ops []string) error {
	if len(ops) < 3 {
		return fmt.Errorf("%s expects two registers and a memory operand", mnem)
	}
	var rt, rt2 uint32
	var err error
	wPair, dPair := is32(ops[0]), isFReg(ops[0])
	if dPair {
		var s1, s2 bool
		if rt, s1, err = parseVReg(ops[0]); err != nil {
			return err
		}
		if rt2, s2, err = parseVReg(ops[1]); err != nil {
			return err
		}
		if s1 || s2 {
			return fmt.Errorf("%s of s-register pairs not supported yet", mnem)
		}
	} else {
		if is32(ops[1]) != wPair {
			return fmt.Errorf("%s registers must share a width: %q, %q", mnem, ops[0], ops[1])
		}
		if rt, err = parseReg(ops[0]); err != nil {
			return err
		}
		if rt2, err = parseReg(ops[1]); err != nil {
			return err
		}
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
	// The pair encoders scale the offset into a signed 7-bit field and mask,
	// so an out-of-range offset would encode as a different, valid instruction.
	scale := int64(8)
	if wPair {
		scale = 4
	}
	if off%scale != 0 || off < -64*scale || off > 63*scale {
		return fmt.Errorf("%s offset %d out of range: must be a multiple of %d in [%d, %d]", mnem, off, scale, -64*scale, 63*scale)
	}
	switch {
	case wPair:
		a.Emit(PairLoadStoreW(rt, rt2, m.base, int32(off), load, mode))
	case dPair:
		a.Emit(PairLoadStoreD(rt, rt2, m.base, int32(off), load, mode))
	default:
		a.Emit(PairLoadStore(rt, rt2, m.base, int32(off), load, mode))
	}
	return nil
}

// exclSize derives the access size of an exclusive/acquire-release
// mnemonic: a `b`/`h` suffix fixes it (and requires a W data register);
// otherwise the data register's width selects word vs doubleword.
func exclSize(mnem, rtOp string) (uint32, error) {
	switch {
	case strings.HasSuffix(mnem, "b"):
		if !is32(rtOp) {
			return 0, fmt.Errorf("%s data register must be a w register, got %q", mnem, rtOp)
		}
		return 0, nil
	case strings.HasSuffix(mnem, "h"):
		if !is32(rtOp) {
			return 0, fmt.Errorf("%s data register must be a w register, got %q", mnem, rtOp)
		}
		return 1, nil
	case is32(rtOp):
		return 2, nil
	default:
		return 3, nil
	}
}

// parseExclBase parses the `[Xn]` operand of the exclusive/ordered
// accesses, which take no offset (a literal `#0` is tolerated, matching
// GNU as).
func parseExclBase(mnem, op string) (uint32, error) {
	m, err := parseMem(op)
	if err != nil {
		return 0, err
	}
	if m.pre || m.hasIndex || m.off != 0 {
		return 0, fmt.Errorf("%s takes a plain [Xn] operand", mnem)
	}
	return m.base, nil
}

// asmLoadExclusive handles `ldxr/ldaxr Rt, [Xn]` and the b/h variants.
func asmLoadExclusive(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects Rt, [Xn]", mnem)
	}
	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	size, err := exclSize(mnem, ops[0])
	if err != nil {
		return err
	}
	rn, err := parseExclBase(mnem, ops[1])
	if err != nil {
		return err
	}
	a.Emit(LDXR(rt, rn, size, strings.HasPrefix(mnem, "lda")))
	return nil
}

// asmStoreExclusive handles `stxr/stlxr Ws, Rt, [Xn]` and the b/h
// variants. The status register Ws is always 32-bit.
func asmStoreExclusive(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects Ws, Rt, [Xn]", mnem)
	}
	if !is32(ops[0]) {
		return fmt.Errorf("%s status register must be a w register, got %q", mnem, ops[0])
	}
	rs, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	rt, err := parseReg(ops[1])
	if err != nil {
		return err
	}
	size, err := exclSize(mnem, ops[1])
	if err != nil {
		return err
	}
	rn, err := parseExclBase(mnem, ops[2])
	if err != nil {
		return err
	}
	a.Emit(STXR(rs, rt, rn, size, strings.HasPrefix(mnem, "stl")))
	return nil
}

// asmAcqRel handles the non-exclusive ordered accesses `ldar/stlr Rt,
// [Xn]` and their b/h variants; enc is LDAR or STLR.
func asmAcqRel(a *Assembler, mnem string, ops []string, enc func(rt, rn, size uint32) uint32) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects Rt, [Xn]", mnem)
	}
	rt, err := parseReg(ops[0])
	if err != nil {
		return err
	}
	size, err := exclSize(mnem, ops[0])
	if err != nil {
		return err
	}
	rn, err := parseExclBase(mnem, ops[1])
	if err != nil {
		return err
	}
	a.Emit(enc(rt, rn, size))
	return nil
}

// barrierOpts maps a dmb/dsb option name to its 4-bit CRm encoding.
// Only the option names the runtime uses are listed; an unlisted name
// would encode a different (weaker) barrier.
var barrierOpts = map[string]uint32{
	"sy": 0xF, "st": 0xE, "ld": 0xD,
	"ish": 0xB, "ishst": 0xA, "ishld": 0x9,
}

// asmBarrier handles `dmb/dsb <option>`. The option is required — GNU
// as rejects the bare mnemonic.
func asmBarrier(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 1 {
		return fmt.Errorf("%s expects a barrier option", mnem)
	}
	opt, ok := barrierOpts[ops[0]]
	if !ok {
		return fmt.Errorf("unsupported barrier option %q", ops[0])
	}
	if mnem == "dmb" {
		a.Emit(DMB(opt))
	} else {
		a.Emit(DSB(opt))
	}
	return nil
}

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

// fpSingle turns a double-precision (ftype=01) scalar FP encoding into
// its single-precision (ftype=00) twin by clearing bit 22, when single
// is set.
func fpSingle(insn uint32, single bool) uint32 {
	if single {
		return insn &^ (1 << 22)
	}
	return insn
}

// parseFPRegs parses n same-precision FP registers, returning their
// numbers and whether they are single-precision. A precision mismatch
// is a different instruction, so it is refused.
func parseFPRegs(mnem string, ops []string, n int) (regs []uint32, single bool, err error) {
	regs = make([]uint32, n)
	for i := 0; i < n; i++ {
		r, s, err := parseVReg(ops[i])
		if err != nil {
			return nil, false, err
		}
		if i == 0 {
			single = s
		} else if s != single {
			return nil, false, fmt.Errorf("%s operands must share a precision: %q vs %q", mnem, ops[0], ops[i])
		}
		regs[i] = r
	}
	return regs, single, nil
}

// asmFloat3 handles the three-register scalar FP ops in both
// precisions; the registers' width (d vs s) selects double vs single.
func asmFloat3(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 3 {
		return fmt.Errorf("%s expects 3 fp registers", mnem)
	}
	r, single, err := parseFPRegs(mnem, ops, 3)
	if err != nil {
		return err
	}
	enc := map[string]func(a, b, c uint32) uint32{
		"fadd": FADD, "fsub": FSUB, "fmul": FMUL, "fdiv": FDIV,
		"fnmul": FNMUL, "fmin": FMIN, "fmax": FMAX, "fminnm": FMINNM, "fmaxnm": FMAXNM,
	}
	a.Emit(fpSingle(enc[mnem](r[0], r[1], r[2]), single))
	return nil
}

// asmFMulAdd handles the fused `fmadd/fmsub/fnmadd/fnmsub Dd, Dn, Dm,
// Da` (and S) forms.
func asmFMulAdd(a *Assembler, mnem string, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("%s expects 4 fp registers", mnem)
	}
	r, single, err := parseFPRegs(mnem, ops, 4)
	if err != nil {
		return err
	}
	enc := map[string]func(a, b, c, d uint32) uint32{
		"fmadd": FMADD, "fmsub": FMSUB, "fnmadd": FNMADD, "fnmsub": FNMSUB,
	}
	a.Emit(fpSingle(enc[mnem](r[0], r[1], r[2], r[3]), single))
	return nil
}

// asmFcsel handles `fcsel Dd, Dn, Dm, <cond>` (and S).
func asmFcsel(a *Assembler, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("fcsel expects Vd, Vn, Vm, cond")
	}
	r, single, err := parseFPRegs("fcsel", ops, 3)
	if err != nil {
		return err
	}
	cond, err := condOperand(ops[3], false)
	if err != nil {
		return err
	}
	a.Emit(fpSingle(FCSEL(r[0], r[1], r[2], cond), single))
	return nil
}

// asmFccmp handles `fccmp Dn, Dm, #nzcv, <cond>` (and S).
func asmFccmp(a *Assembler, ops []string) error {
	if len(ops) != 4 {
		return fmt.Errorf("fccmp expects Vn, Vm, #nzcv, cond")
	}
	r, single, err := parseFPRegs("fccmp", ops, 2)
	if err != nil {
		return err
	}
	nzcv, err := parseImm(ops[2])
	if err != nil {
		return err
	}
	if nzcv < 0 || nzcv > 15 {
		return fmt.Errorf("fccmp nzcv %d out of range 0..15", nzcv)
	}
	cond, err := condOperand(ops[3], false)
	if err != nil {
		return err
	}
	a.Emit(fpSingle(FCCMP(r[0], r[1], uint32(nzcv), cond), single))
	return nil
}

// asmFUnary handles a unary scalar FP op `<op> Dd, Dn` / `Sd, Sn`
// (fabs/fsqrt/frint*); enc is the double-precision encoder and the
// single form is its ftype=00 twin.
func asmFUnary(a *Assembler, mnem string, ops []string, enc func(rd, rn uint32) uint32) error {
	if len(ops) != 2 {
		return fmt.Errorf("%s expects 2 fp registers", mnem)
	}
	r, single, err := parseFPRegs(mnem, ops, 2)
	if err != nil {
		return err
	}
	a.Emit(fpSingle(enc(r[0], r[1]), single))
	return nil
}

// asmFNeg handles fneg Dd,Dn / Sd,Sn.
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

// asmFcmp handles fcmp/fcmpe Dn,Dm / Sn,Sm. signaling selects fcmpe,
// which raises Invalid Operation on a quiet NaN too (opc bit 4).
func asmFcmp(a *Assembler, ops []string, signaling bool) error {
	name := "fcmp"
	var e uint32
	if signaling {
		name, e = "fcmpe", 0x10
	}
	if len(ops) != 2 {
		return fmt.Errorf("%s expects 2 fp registers", name)
	}
	rn, single, err := parseVReg(ops[0])
	if err != nil {
		return err
	}
	// `fcmp Dn, #0.0` — the compare-against-zero form (opc bit 3 set,
	// Rm=0). Only the literal zero exists as an immediate; anything else
	// is a parse error.
	if strings.HasPrefix(ops[1], "#") {
		if ops[1] != "#0.0" && ops[1] != "#0" {
			return fmt.Errorf("%s takes only the #0.0 immediate", name)
		}
		a.Emit(fpSingle(FCMP(rn, 0)|0x08|e, single))
		return nil
	}
	rm, rmSingle, err := parseVReg(ops[1])
	if err != nil {
		return err
	}
	if rmSingle != single {
		return fmt.Errorf("%s operands must share a precision: %q vs %q", name, ops[0], ops[1])
	}
	a.Emit(fpSingle(FCMP(rn, rm)|e, single))
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

// splitOperands splits the operand list on commas, but keeps commas that
// sit inside a memory operand's brackets or a register list's braces
// together (so "[x1, #8]" and "{v0.16b, v1.16b}" each stay one operand).
func splitOperands(rest string) []string {
	if rest == "" {
		return nil
	}
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '[', '{':
			depth++
		case ']', '}':
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

	// Register-offset form ("[Xn, Rm{, <extend|lsl> {#amt}}]").
	indexOpt    uint32 // extend option: UXTW=2, LSL/UXTX=3, SXTW=6, SXTX=7
	indexAmt    uint32
	hasIndex    bool
	index       uint32
	indexIs32   bool // Wm offset register (only valid with uxtw/sxtw)
	indexHasAmt bool // explicit #amt (matters for byte accesses: it sets S)
}

// parseMem parses a bracketed memory operand: [Xn], [Xn, #imm],
// [Xn, #imm]! (pre-index), or the register-offset
// [Xn, Xm{, lsl|sxtx {#amt}}] / [Xn, Wm, uxtw|sxtw {#amt}].
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
	m.indexIs32 = isWReg(inner[1])
	m.indexOpt = 3
	if len(inner) == 3 {
		f := strings.Fields(inner[2])
		if len(f) == 0 || len(f) > 2 {
			return m, fmt.Errorf("bad index extend %q", inner[2])
		}
		switch f[0] {
		case "lsl":
			if len(f) != 2 {
				return m, fmt.Errorf("index lsl needs an amount")
			}
		case "uxtw":
			m.indexOpt = 2
		case "sxtw":
			m.indexOpt = 6
		case "sxtx":
			m.indexOpt = 7
		default:
			return m, fmt.Errorf("unsupported index extend %q", f[0])
		}
		if len(f) == 2 {
			n, err := parseImm(f[1])
			if err != nil {
				return m, err
			}
			if n < 0 {
				return m, fmt.Errorf("negative index shift %q", inner[2])
			}
			m.indexAmt = uint32(n)
			m.indexHasAmt = true
		}
	}
	// The option must match the offset register's width: uxtw/sxtw widen a
	// W register, lsl/sxtx take an X register. A mismatch is not a valid
	// encoding (GNU as rejects it too).
	if m.indexIs32 != (m.indexOpt == 2 || m.indexOpt == 6) {
		return m, fmt.Errorf("index register %q does not match extend option", inner[1])
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

// parseReg parses x0..x30, plus sp/xzr/wzr, the lr/fp aliases, and the
// w-registers (which share register numbers with x for the forms
// covered so far).
func parseReg(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "sp", "xzr", "wzr":
		return 31, nil
	case "lr":
		return 30, nil
	case "fp":
		return 29, nil
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
// parseIntTerm parses one integer literal, accepting the upper unsigned
// 64-bit half (e.g. a bitmask immediate written #0xfffffff7fffffff7) as its
// two's-complement value.
func parseIntTerm(s string) (int64, error) {
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return v, nil
	}
	u, err := strconv.ParseUint(s, 0, 64)
	return int64(u), err
}

func evalIntExpr(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty immediate")
	}
	if v, err := parseIntTerm(s); err == nil {
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
		v, err := parseIntTerm(term)
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
	"al": CondAL, "nv": CondNV,
}

// condOperand decodes a condition operand. inverts marks the aliases whose
// encoder uses the INVERSE of the written condition — cset is CSINC with the
// condition flipped, and csetm / cinc / cinv / cneg follow. GNU as refuses AL
// and NV on exactly those, because inverting "always" or "never" names no
// useful instruction, and it says so rather than encoding something.
func condOperand(tok string, inverts bool) (uint32, error) {
	c, ok := condCodes[tok]
	if !ok {
		return 0, fmt.Errorf("bad condition %q", tok)
	}
	if inverts && (tok == "al" || tok == "nv") {
		return 0, fmt.Errorf("condition %q is not allowed here: this form encodes the inverse of the written condition, so %q would name no instruction (GNU as refuses it too)", tok, tok)
	}
	return c, nil
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
