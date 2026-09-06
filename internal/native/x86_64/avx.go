package x86_64

import "fmt"

// AVX2 support: exactly the five VEX-encoded forms the vectorised
// memchr/rmemchr/count_byte kernels need over a 256-bit ymm register —
// vmovdqu (load), vpbroadcastb, vpcmpeqb, vpmovmskb and vzeroupper — rather
// than the whole AVX2 vocabulary. AVX2 is inside the declared x86-64
// baseline (Haswell-class 2013, same baseline that makes popcnt a hard
// requirement rather than a fast path), so these need no CPU feature
// detection.
//
// emitVEX3 picks the shorter 2-byte VEX prefix (0xC5) over the 3-byte form
// (0xC4) whenever the instruction allows it, matching what GNU as emits —
// see its doc comment for which fields decide that.

// emitVEX3 emits the VEX prefix for the given fields. rexR/rexX/rexB are
// the REX.R/X/B bits this instruction would carry WITHOUT VEX — i.e. true
// when the corresponding register field needs the extension bit — and are
// stored inverted in the prefix, per the encoding. vvvv is the second
// source register (0 when unused, which is its own correct encoding: an
// all-zero vvvv field's one's complement is 1111, the documented "not
// used" value).
//
// The 2-byte form (0xC5) is used whenever it can represent the
// instruction — mmmmm names the 0F map and W is clear, and neither X nor B
// is needed (that form has no bits for them) — because that is what GNU as
// emits, and TestAssembleAgainstGNUAs pins every encoding choice byte for
// byte. The two forms decode to the same instruction; this is not a
// behavioural choice, only which bytes reach the oracle.
func (a *Assembler) emitVEX3(rexR, rexX, rexB bool, mmmmm byte, w bool, vvvv int, l bool, pp byte) {
	vvvvInv := byte((^vvvv) & 0xF)
	if mmmmm == 0x01 && !w && !rexX && !rexB {
		b2 := pp & 0x03
		if l {
			b2 |= 0x04
		}
		b2 |= vvvvInv << 3
		if !rexR {
			b2 |= 0x80
		}
		a.emit(0xC5, b2)
		return
	}
	b2 := mmmmm & 0x1F
	if !rexR {
		b2 |= 0x80
	}
	if !rexX {
		b2 |= 0x40
	}
	if !rexB {
		b2 |= 0x20
	}
	b3 := pp & 0x03
	if l {
		b3 |= 0x04
	}
	b3 |= vvvvInv << 3
	if w {
		b3 |= 0x80
	}
	a.emit(0xC4, b2, b3)
}

// vexRXB derives the REX.R/X/B bits emitVEX3 wants from a ModRM.reg field
// and an rm operand (register or memory), mirroring emitRexRM/memRex but
// returning booleans instead of a packed legacy REX byte.
func vexRXB(reg int, rm Operand) (r, x, b bool) {
	r = reg >= 8
	if rm.kind == opMem {
		x = rm.index >= 8
		b = rm.base >= 8
		return
	}
	b = rm.reg >= 8
	return
}

// vMovdqu encodes `vmovdqu ymm, ymm/m256` (VEX.256.F3.0F.WIG 6F /r): the
// unaligned 32-byte load the AVX2 kernels use in place of SSE2's movdqu.
func (a *Assembler) vMovdqu(ops []Operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size != 256 ||
		!(ops[1].kind == opMem || (ops[1].kind == opReg && ops[1].size == 256)) {
		return fmt.Errorf("vmovdqu expects ymm, ymm/m256")
	}
	dst, src := ops[0], ops[1]
	r, x, b := vexRXB(dst.reg, src)
	a.emitVEX3(r, x, b, 0x01, false, 0, true, 0x02)
	a.emit(0x6F)
	a.emitModRM(dst.reg, src)
	return nil
}

// vpbroadcastb encodes `vpbroadcastb ymm, xmm` (VEX.256.66.0F38.WIG 78
// /r): broadcast the low byte of an xmm register across all 32 lanes of a
// ymm — the AVX2 needle splat, replacing SSE2's
// movd/punpcklbw/punpcklwd/pshufd chain.
func (a *Assembler) vpbroadcastb(ops []Operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size != 256 ||
		ops[1].kind != opReg || ops[1].size != 128 {
		return fmt.Errorf("vpbroadcastb expects ymm, xmm")
	}
	dst, src := ops[0], ops[1]
	r, x, b := vexRXB(dst.reg, src)
	a.emitVEX3(r, x, b, 0x02, false, 0, true, 0x01)
	a.emit(0x78)
	a.emitModRM(dst.reg, src)
	return nil
}

// vpcmpeqb encodes the non-destructive 3-operand
// `vpcmpeqb ymm(dst), ymm(src1), ymm/m256(src2)` (VEX.256.66.0F.WIG 74
// /r): dst = (src1 == src2) per byte lane, gathered 1 bit per lane by
// vpmovmskb. src1 is the VEX.vvvv operand.
func (a *Assembler) vpcmpeqb(ops []Operand) error {
	if len(ops) != 3 || ops[0].kind != opReg || ops[0].size != 256 ||
		ops[1].kind != opReg || ops[1].size != 256 {
		return fmt.Errorf("vpcmpeqb expects ymm, ymm, ymm/m256")
	}
	dst, src1, src2 := ops[0], ops[1], ops[2]
	r, x, b := vexRXB(dst.reg, src2)
	a.emitVEX3(r, x, b, 0x01, false, src1.reg, true, 0x01)
	a.emit(0x74)
	a.emitModRM(dst.reg, src2)
	return nil
}

// vpmovmskb encodes `vpmovmskb r32, ymm` (VEX.256.66.0F.WIG D7 /r): gather
// the top bit of each of the 32 lanes into a GPR — the AVX2 widening of
// pmovmskb's 16-bit mask to 32 bits, one bit per byte of the block. The
// instruction has no 64-bit-result form (VEX.W is WIG here, not a real
// operand-size switch), so only r32 is accepted.
func (a *Assembler) vpmovmskb(ops []Operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size != 32 ||
		ops[1].kind != opReg || ops[1].size != 256 {
		return fmt.Errorf("vpmovmskb expects r32, ymm")
	}
	dst, src := ops[0], ops[1]
	r, x, b := vexRXB(dst.reg, src)
	a.emitVEX3(r, x, b, 0x01, false, 0, true, 0x01)
	a.emit(0xD7)
	a.emit(modrmReg(dst.reg, src.reg))
	return nil
}

// vzeroupper encodes VEX.128.0F.WIG 77, no ModRM: zeroes the upper 128
// bits of ymm0..15. Emitted before any fall-through from this kernel's
// AVX2 body into its legacy-SSE (non-VEX) 16-byte tail loop or scalar
// return, which is what avoids the save/restore penalty a CPU pays the
// first legacy-SSE instruction it runs after leaving ymm state dirty.
func (a *Assembler) vzeroupper(ops []Operand) error {
	if len(ops) != 0 {
		return fmt.Errorf("vzeroupper takes no operands")
	}
	a.emit(0xC5, 0xF8, 0x77)
	return nil
}
