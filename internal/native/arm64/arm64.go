// Package arm64 is a Go-side ARM64 (AArch64) machine-code encoder:
// one function per instruction form, each returning the 4-byte
// fixed-width encoding as a uint32. It is the first brick of the
// native-binary path that aims to replace the GAS-text +
// aarch64-linux-gnu-gcc shell-out in cmd/fern (Phase 3 of
// docs/TOOLCHAIN-SELF-HOSTING.md).
//
// AArch64 instructions are all exactly 32 bits, little-endian.
// Reference: ARM Architecture Reference Manual for ARMv8-A, §C4.
// Each encoder here is pinned byte-for-byte against llvm-mc
// (`llvm-mc -triple=aarch64 --show-encoding`) in the tests.
//
// This is a deliberately tiny starting subset — enough to encode an
// `exit(N)` program end-to-end (MOVZ + SVC, plus RET) — so the ELF
// writer and the encode→link→run pipeline can be proven before the
// full ~67-mnemonic instruction surface lands.
package arm64

// regMask keeps a register number in the 5-bit field range (x0-x30;
// 31 is xzr/sp depending on context).
const regMask uint32 = 0x1f

// MOVZ encodes `movz Xd, #imm16, lsl #shift` (64-bit move-wide-zero):
// load imm16 into the given 16-bit slot of Xd, zeroing the rest.
// shift must be one of 0, 16, 32, 48.
//
// Encoding: sf=1 opc=10 100101 hw(2) imm16(16) Rd(5)
// → base 0xD2800000 | hw<<21 | imm16<<5 | Rd.
func MOVZ(rd uint32, imm16 uint16, shift uint32) uint32 {
	hw := (shift / 16) & 0x3
	return 0xD2800000 | (hw << 21) | (uint32(imm16) << 5) | (rd & regMask)
}

// MOVK encodes `movk Xd, #imm16, lsl #shift` (move-wide-keep): write
// imm16 into one 16-bit slot of Xd, leaving the other bits unchanged.
// Paired with MOVZ it builds an arbitrary 64-bit constant. shift must
// be one of 0, 16, 32, 48.
//
// Encoding: sf=1 opc=11 100101 hw(2) imm16(16) Rd(5)
// → base 0xF2800000 | hw<<21 | imm16<<5 | Rd.
func MOVK(rd uint32, imm16 uint16, shift uint32) uint32 {
	hw := (shift / 16) & 0x3
	return 0xF2800000 | (hw << 21) | (uint32(imm16) << 5) | (rd & regMask)
}

// MOVN encodes `movn Xd, #imm16, lsl #shift` (move-wide-not): load the
// bitwise complement of (imm16 << shift) into Xd. The building block
// for small negative constants. shift ∈ {0, 16, 32, 48}.
//
// Encoding: sf=1 opc=00 → base 0x92800000 | hw<<21 | imm16<<5 | Rd.
func MOVN(rd uint32, imm16 uint16, shift uint32) uint32 {
	hw := (shift / 16) & 0x3
	return 0x92800000 | (hw << 21) | (uint32(imm16) << 5) | (rd & regMask)
}

// ADDimm encodes `add Xd, Xn, #imm12{, lsl #12}` (64-bit). When
// shift12 is set the 12-bit immediate is shifted left by 12, covering
// the 0..0xfff000 range in 0x1000 steps. imm12 occupies the low 12 bits.
//
// Encoding: sf=1 op=0 S=0 100010 sh(1) imm12(12) Rn(5) Rd(5)
// → base 0x91000000 | sh<<22 | imm12<<10 | Rn<<5 | Rd.
func ADDimm(rd, rn uint32, imm12 uint16, shift12 bool) uint32 {
	var sh uint32
	if shift12 {
		sh = 1
	}
	return 0x91000000 | (sh << 22) | ((uint32(imm12) & 0xfff) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// SUBimm encodes `sub Xd, Xn, #imm12{, lsl #12}` (64-bit) — ADDimm
// with the subtract opcode.
//
// Encoding: base 0xD1000000 | sh<<22 | imm12<<10 | Rn<<5 | Rd.
func SUBimm(rd, rn uint32, imm12 uint16, shift12 bool) uint32 {
	var sh uint32
	if shift12 {
		sh = 1
	}
	return 0xD1000000 | (sh << 22) | ((uint32(imm12) & 0xfff) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// ADDreg encodes `add Xd, Xn, Xm` (64-bit, shifted-register form with
// a zero shift — the plain three-register add).
//
// Encoding: sf=1 op=0 S=0 01011 shift=00 0 Rm(5) imm6=0 Rn(5) Rd(5)
// → base 0x8B000000 | Rm<<16 | Rn<<5 | Rd.
func ADDreg(rd, rn, rm uint32) uint32 {
	return 0x8B000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// SUBreg encodes `sub Xd, Xn, Xm` (64-bit, shifted-register, no shift).
//
// Encoding: base 0xCB000000 | Rm<<16 | Rn<<5 | Rd.
func SUBreg(rd, rn, rm uint32) uint32 {
	return 0xCB000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// MOVreg encodes `mov Xd, Xm` — register-to-register move, which
// AArch64 expresses as `orr Xd, XZR, Xm`.
//
// Encoding: ORR shifted-register 64-bit with Rn = XZR(31)
// → base 0xAA000000 | Rm<<16 | 31<<5 | Rd.
func MOVreg(rd, rm uint32) uint32 {
	return 0xAA000000 | ((rm & regMask) << 16) | (31 << 5) | (rd & regMask)
}

// ANDreg / ORRreg / EORreg encode the 64-bit logical shifted-register
// ops `<op> Xd, Xn, Xm` with a zero shift. (MOVreg above is the
// Rn=XZR special case of ORRreg.)
//
// Encoding: sf=1 opc 01010 shift=00 0 Rm imm6=0 Rn Rd. opc selects
// the op: AND=00→0x8A, ORR=01→0xAA, EOR=10→0xCA.
func ANDreg(rd, rn, rm uint32) uint32 {
	return 0x8A000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

func ORRreg(rd, rn, rm uint32) uint32 {
	return 0xAA000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

func EORreg(rd, rn, rm uint32) uint32 {
	return 0xCA000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// LSLV / LSRV / ASRV encode the 64-bit variable (register) shifts
// `<op> Xd, Xn, Xm`: shift Xn by the low 6 bits of Xm. These are the
// data-processing-2-source forms (the assembler spells them lsl/lsr/asr
// when the third operand is a register).
//
// Encoding: sf=1 0 0 11010110 Rm 0010 op2 Rn Rd, op2 = LSL=00 / LSR=01
// / ASR=10 → base 0x9AC02000 | (op2<<10) | Rm<<16 | Rn<<5 | Rd.
func LSLV(rd, rn, rm uint32) uint32 {
	return 0x9AC02000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

func LSRV(rd, rn, rm uint32) uint32 {
	return 0x9AC02400 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

func ASRV(rd, rn, rm uint32) uint32 {
	return 0x9AC02800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// CMPreg encodes `cmp Xn, Xm` — the SUBS XZR, Xn, Xm alias: subtract
// and set the condition flags, discarding the result.
//
// Encoding: SUBS shifted-register 64-bit with Rd=XZR(31)
// → base 0xEB000000 | Rm<<16 | Rn<<5 | 31.
func CMPreg(rn, rm uint32) uint32 {
	return 0xEB000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | 31
}

// CMPimm encodes `cmp Xn, #imm12{, lsl #12}` — the SUBS XZR, Xn, #imm
// alias.
//
// Encoding: SUBS immediate 64-bit with Rd=XZR(31)
// → base 0xF1000000 | sh<<22 | imm12<<10 | Rn<<5 | 31.
func CMPimm(rn uint32, imm12 uint16, shift12 bool) uint32 {
	var sh uint32
	if shift12 {
		sh = 1
	}
	return 0xF1000000 | (sh << 22) | ((uint32(imm12) & 0xfff) << 10) | ((rn & regMask) << 5) | 31
}

// MUL encodes `mul Xd, Xn, Xm` — the MADD Xd, Xn, Xm, XZR alias
// (multiply with no accumulate).
//
// Encoding: MADD 64-bit with Ra=XZR(31)
// → base 0x9B000000 | Rm<<16 | 31<<10 | Rn<<5 | Rd.
func MUL(rd, rn, rm uint32) uint32 {
	return 0x9B000000 | ((rm & regMask) << 16) | (31 << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// ---- Loads / stores (unsigned scaled offset) ----
//
// `byteOffset` is the offset in bytes; each encoder scales it by the
// access size to the 12-bit immediate the instruction carries, so the
// caller passes natural byte offsets (0, 8, 16, … for 64-bit; 0, 1, 2,
// … for byte). The offset must be a multiple of the access size and
// fit the scaled 12-bit field.

// LDRimm: `ldr Xt, [Xn, #byteOffset]` (64-bit, scale 8).
// Encoding: base 0xF9400000 | (byteOffset/8)<<10 | Rn<<5 | Rt.
func LDRimm(rt, rn, byteOffset uint32) uint32 {
	return 0xF9400000 | (((byteOffset / 8) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// STRimm: `str Xt, [Xn, #byteOffset]` (64-bit, scale 8).
func STRimm(rt, rn, byteOffset uint32) uint32 {
	return 0xF9000000 | (((byteOffset / 8) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDRBimm: `ldrb Wt, [Xn, #byteOffset]` (byte, zero-extend, scale 1).
func LDRBimm(rt, rn, byteOffset uint32) uint32 {
	return 0x39400000 | ((byteOffset & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// STRBimm: `strb Wt, [Xn, #byteOffset]` (byte, scale 1).
func STRBimm(rt, rn, byteOffset uint32) uint32 {
	return 0x39000000 | ((byteOffset & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDRHimm: `ldrh Wt, [Xn, #byteOffset]` (halfword, zero-extend, scale 2).
func LDRHimm(rt, rn, byteOffset uint32) uint32 {
	return 0x79400000 | (((byteOffset / 2) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// STRHimm: `strh Wt, [Xn, #byteOffset]` (halfword, scale 2).
func STRHimm(rt, rn, byteOffset uint32) uint32 {
	return 0x79000000 | (((byteOffset / 2) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// ---- Unscaled loads / stores (LDUR/STUR family) ----
//
// These carry a SIGNED 9-bit byte offset (-256..255) with no scaling,
// so they reach negative and non-size-aligned displacements that the
// scaled LDRimm/STRimm forms can't. off must fit the 9-bit field.

// LDUR: `ldur Xt, [Xn, #off]` (64-bit unscaled).
// Encoding: base 0xF8400000 | imm9<<12 | Rn<<5 | Rt.
func LDUR(rt, rn uint32, off int32) uint32 {
	return 0xF8400000 | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// STUR: `stur Xt, [Xn, #off]` (64-bit unscaled).
func STUR(rt, rn uint32, off int32) uint32 {
	return 0xF8000000 | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDURB: `ldurb Wt, [Xn, #off]` (byte, unscaled, zero-extend).
func LDURB(rt, rn uint32, off int32) uint32 {
	return 0x38400000 | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// STURB: `sturb Wt, [Xn, #off]` (byte, unscaled).
func STURB(rt, rn uint32, off int32) uint32 {
	return 0x38000000 | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// ---- Load/store pair, pre/post-indexed ----
//
// The frame prologue/epilogue idiom. byteOffset is a signed multiple
// of 8; it is scaled to the 7-bit immediate. STPpre writes the new
// base back BEFORE the access (`[Xn, #off]!`); LDPpost updates it
// AFTER (`[Xn], #off`).

// STPpre: `stp Xt, Xt2, [Xn, #byteOffset]!` (64-bit, pre-index).
// Encoding: base 0xA9800000 | imm7<<15 | Rt2<<10 | Rn<<5 | Rt.
func STPpre(rt, rt2, rn uint32, byteOffset int32) uint32 {
	imm7 := uint32(byteOffset/8) & 0x7f
	return 0xA9800000 | (imm7 << 15) | ((rt2 & regMask) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDPpost: `ldp Xt, Xt2, [Xn], #byteOffset` (64-bit, post-index).
// Encoding: base 0xA8C00000 | imm7<<15 | Rt2<<10 | Rn<<5 | Rt.
func LDPpost(rt, rt2, rn uint32, byteOffset int32) uint32 {
	imm7 := uint32(byteOffset/8) & 0x7f
	return 0xA8C00000 | (imm7 << 15) | ((rt2 & regMask) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// ubfmX / sbfmX are the 64-bit bitfield-move base encoders (N=1). The
// immediate shifts and sign-extends below are all aliases of these.
//
// Encoding: sf=1 opc 100110 N immr(6) imms(6) Rn Rd; opc=10/N=1 → UBFM
// (base 0xD3400000), opc=00/N=1 → SBFM (base 0x93400000).
func ubfmX(rd, rn, immr, imms uint32) uint32 {
	return 0xD3400000 | ((immr & 0x3f) << 16) | ((imms & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

func sbfmX(rd, rn, immr, imms uint32) uint32 {
	return 0x93400000 | ((immr & 0x3f) << 16) | ((imms & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// LSLimm / LSRimm / ASRimm encode the 64-bit immediate shifts
// `<op> Xd, Xn, #shift` (shift in 1..63), the UBFM/SBFM aliases:
//
//	lsl #s = ubfm Xd, Xn, #(-s mod 64), #(63-s)
//	lsr #s = ubfm Xd, Xn, #s, #63
//	asr #s = sbfm Xd, Xn, #s, #63
func LSLimm(rd, rn, shift uint32) uint32 {
	shift &= 0x3f
	return ubfmX(rd, rn, (64-shift)&0x3f, 63-shift)
}

func LSRimm(rd, rn, shift uint32) uint32 {
	shift &= 0x3f
	return ubfmX(rd, rn, shift, 63)
}

func ASRimm(rd, rn, shift uint32) uint32 {
	shift &= 0x3f
	return sbfmX(rd, rn, shift, 63)
}

// SXTB / SXTH / SXTW encode `sxt<b|h|w> Xd, Wn` — sign-extend the low
// 8 / 16 / 32 bits of Wn into the 64-bit Xd (SBFM aliases with immr=0).
func SXTB(rd, rn uint32) uint32 { return sbfmX(rd, rn, 0, 7) }
func SXTH(rd, rn uint32) uint32 { return sbfmX(rd, rn, 0, 15) }
func SXTW(rd, rn uint32) uint32 { return sbfmX(rd, rn, 0, 31) }

// CSEL encodes `csel Xd, Xn, Xm, <cond>` — Xd = cond ? Xn : Xm.
// Encoding: base 0x9A800000 | Rm<<16 | cond<<12 | Rn<<5 | Rd.
func CSEL(rd, rn, rm, cond uint32) uint32 {
	return 0x9A800000 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}

// CSET encodes `cset Xd, <cond>` — Xd = cond ? 1 : 0. It's the
// CSINC Xd, XZR, XZR, invert(cond) alias (the condition is inverted
// because CSINC increments the "else" path).
// Encoding: base 0x9A800400 | 31<<16 | (cond^1)<<12 | 31<<5 | Rd.
func CSET(rd, cond uint32) uint32 {
	return 0x9A800400 | (31 << 16) | (((cond ^ 1) & 0xf) << 12) | (31 << 5) | (rd & regMask)
}

// CMN encodes `cmn Xn, Xm` — compare-negative (ADDS XZR, Xn, Xm):
// set flags from Xn + Xm, discard the sum.
// Encoding: base 0xAB000000 | Rm<<16 | Rn<<5 | 31.
func CMN(rn, rm uint32) uint32 {
	return 0xAB000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | 31
}

// NEG encodes `neg Xd, Xm` — negate (SUB Xd, XZR, Xm).
// Encoding: base 0xCB000000 | Rm<<16 | 31<<5 | Rd.
func NEG(rd, rm uint32) uint32 {
	return 0xCB000000 | ((rm & regMask) << 16) | (31 << 5) | (rd & regMask)
}

// UDIV encodes `udiv Xd, Xn, Xm` — unsigned divide (Xn / Xm; division
// by zero yields 0, per the architecture).
// Encoding: base 0x9AC00800 | Rm<<16 | Rn<<5 | Rd.
func UDIV(rd, rn, rm uint32) uint32 {
	return 0x9AC00800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// MSUB encodes `msub Xd, Xn, Xm, Xa` — Xd = Xa - Xn*Xm (the building
// block for the modulo idiom: `udiv q,a,b; msub r,q,b,a`).
// Encoding: base 0x9B008000 | Rm<<16 | Ra<<10 | Rn<<5 | Rd.
func MSUB(rd, rn, rm, ra uint32) uint32 {
	return 0x9B008000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// ---- Floating-point (double-precision) ----
//
// FP registers d0..d31 share the 5-bit register field with the general
// registers. These are the ftype=01 (double) data-processing forms plus
// the int<->float conversions the backend needs for f64.

// FADD/FSUB/FMUL/FDIV Dd, Dn, Dm — double-precision arithmetic.
func FADD(rd, rn, rm uint32) uint32 {
	return 0x1E602800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FSUB(rd, rn, rm uint32) uint32 {
	return 0x1E603800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMUL(rd, rn, rm uint32) uint32 {
	return 0x1E600800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FDIV(rd, rn, rm uint32) uint32 {
	return 0x1E601800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// FNEG Dd, Dn — negate.
func FNEG(rd, rn uint32) uint32 {
	return 0x1E614000 | ((rn & regMask) << 5) | (rd & regMask)
}

// FCMP Dn, Dm — compare and set the FP condition flags.
func FCMP(rn, rm uint32) uint32 {
	return 0x1E602000 | ((rm & regMask) << 16) | ((rn & regMask) << 5)
}

// FMOV Dd, Dn — register move (double).
func FMOV(rd, rn uint32) uint32 {
	return 0x1E604000 | ((rn & regMask) << 5) | (rd & regMask)
}

// FMOVfromGPR Dd, Xn — copy the raw 64 bits of Xn into Dd.
func FMOVfromGPR(rd, rn uint32) uint32 {
	return 0x9E670000 | ((rn & regMask) << 5) | (rd & regMask)
}

// FMOVtoGPR Xd, Dn — copy the raw 64 bits of Dn into Xd.
func FMOVtoGPR(rd, rn uint32) uint32 {
	return 0x9E660000 | ((rn & regMask) << 5) | (rd & regMask)
}

// SCVTF Dd, Xn — convert signed 64-bit integer to double.
func SCVTF(rd, rn uint32) uint32 {
	return 0x9E620000 | ((rn & regMask) << 5) | (rd & regMask)
}

// FCVTZS Xd, Dn — convert double to signed 64-bit integer (truncate
// toward zero).
func FCVTZS(rd, rn uint32) uint32 {
	return 0x9E780000 | ((rn & regMask) << 5) | (rd & regMask)
}

// SVC encodes `svc #imm16` — supervisor call (syscall) trap.
//
// Encoding: 11010100 000 imm16 00001 → base 0xD4000001 | imm16<<5.
func SVC(imm16 uint16) uint32 {
	return 0xD4000001 | (uint32(imm16) << 5)
}

// RET encodes `ret Xn` — return to the address in Xn (x30 by the
// usual convention).
//
// Encoding: 1101011 0010 11111 0000 00 Rn 00000 → base 0xD65F0000 | Rn<<5.
func RET(rn uint32) uint32 {
	return 0xD65F0000 | ((rn & regMask) << 5)
}

// BR encodes `br Xn` — unconditional indirect branch to the address
// in Xn (no link).
//
// Encoding: 1101011 0000 11111 0000 00 Rn 00000 → base 0xD61F0000 | Rn<<5.
func BR(rn uint32) uint32 {
	return 0xD61F0000 | ((rn & regMask) << 5)
}

// BLR encodes `blr Xn` — indirect call: branch to Xn, link in x30.
//
// Encoding: base 0xD63F0000 | Rn<<5.
func BLR(rn uint32) uint32 {
	return 0xD63F0000 | ((rn & regMask) << 5)
}

// Condition codes for B.cond (the low 4 bits of the conditional-branch
// encoding). The assembler's Bcond takes one of these.
const (
	CondEQ uint32 = 0  // equal (Z==1)
	CondNE uint32 = 1  // not equal
	CondHS uint32 = 2  // unsigned >= (also CS)
	CondLO uint32 = 3  // unsigned <  (also CC)
	CondMI uint32 = 4  // negative
	CondPL uint32 = 5  // non-negative
	CondVS uint32 = 6  // overflow set
	CondVC uint32 = 7  // overflow clear
	CondHI uint32 = 8  // unsigned >
	CondLS uint32 = 9  // unsigned <=
	CondGE uint32 = 10 // signed >=
	CondLT uint32 = 11 // signed <
	CondGT uint32 = 12 // signed >
	CondLE uint32 = 13 // signed <=
	CondAL uint32 = 14 // always
)

// Put appends insn to buf as 4 little-endian bytes.
func Put(buf []byte, insn uint32) []byte {
	return append(buf, byte(insn), byte(insn>>8), byte(insn>>16), byte(insn>>24))
}
