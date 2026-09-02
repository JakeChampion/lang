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

import "math/bits"

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

// ADDregShift / SUBregShift encode the shifted-register forms
// `add/sub Xd, Xn, Xm, <shift> #amount`. shiftType is 0=LSL, 1=LSR,
// 2=ASR (bits 23:22); amount is the 6-bit shift count (bits 15:10). The
// no-shift encoders above are the shiftType=0, amount=0 special case.
// The backend emits `add Xd, Xn, Xm, lsl #N` to scale an array index by
// the element size when computing an element address — dropping the
// shift (as the plain ADDreg path did) corrupts every element past [0].
func ADDregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0x8B000000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func SUBregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xCB000000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// ADDextReg / SUBextReg encode the extended-register forms
// `add/sub Xd, Xn, Wm, <extend> {#amount}`. option is the 3-bit extend
// selector (bits 15:13): UXTW=0b010 / SXTW=0b110 are the ones the
// backend emits to widen a 32-bit array index to a 64-bit address;
// amount is the 0..4 left-shift (bits 12:10). The form is marked by
// bit 21.
func ADDextReg(rd, rn, rm, option, amount uint32) uint32 {
	return 0x8B200000 | ((rm & regMask) << 16) | ((option & 7) << 13) | ((amount & 7) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func SUBextReg(rd, rn, rm, option, amount uint32) uint32 {
	return 0xCB200000 | ((rm & regMask) << 16) | ((option & 7) << 13) | ((amount & 7) << 10) | ((rn & regMask) << 5) | (rd & regMask)
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

// ANDregShift / ORRregShift / EORregShift encode the shifted-register
// logical forms `<op> Rd, Rn, Rm, <shift> #amount`. shiftType is bits
// 23:22 (0=LSL, 1=LSR, 2=ASR, 3=ROR); amount is the 6-bit count (bits
// 15:10). The no-shift encoders above are the shiftType=0, amount=0
// case. The backend emits e.g. `orr w3, w1, w1, lsl #8` to splice bytes
// when building a wider integer.
func ANDregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0x8A000000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func ORRregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xAA000000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func EORregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xCA000000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// REV16 reverses the byte order within each 16-bit halfword of Rn into
// Rd — the byte-swap the backend emits for 16-bit network/endian
// conversions. This is the 64-bit base (0xDAC00400); a 32-bit `rev16
// Wd, Wn` clears the sf bit via clearSF.
func REV16(rd, rn uint32) uint32 {
	return 0xDAC00400 | ((rn & regMask) << 5) | (rd & regMask)
}

// encodeBitmask computes the (N, immr, imms) logical-immediate fields
// for imm at the given register size (32 or 64). ok is false when imm
// is not a valid bitmask immediate — 0, all-ones, or not a rotated run
// of ones replicated across a power-of-two element. Mirrors LLVM's
// processLogicalImmediate; validated against aarch64-linux-gnu-as.
func encodeBitmask(imm uint64, regSize int) (n, immr, imms uint32, ok bool) {
	if imm == 0 || imm == ^uint64(0) ||
		(regSize != 64 && (imm>>uint(regSize) != 0 || imm == (^uint64(0)>>(64-uint(regSize))))) {
		return 0, 0, 0, false
	}
	size := regSize
	for {
		size /= 2
		mask := (uint64(1) << uint(size)) - 1
		if (imm & mask) != ((imm >> uint(size)) & mask) {
			size *= 2
			break
		}
		if size <= 2 {
			break
		}
	}
	mask := (^uint64(0)) >> (64 - uint(size))
	imm &= mask
	isShifted := func(v uint64) bool {
		if v == 0 {
			return false
		}
		m := (v - 1) | v
		return ((m + 1) & m) == 0
	}
	var i, cto uint32
	if isShifted(imm) {
		ii := bits.TrailingZeros64(imm)
		i = uint32(ii)
		cto = uint32(bits.TrailingZeros64(^(imm >> uint(ii))))
	} else {
		imm |= ^mask
		if !isShifted(^imm) {
			return 0, 0, 0, false
		}
		clo := bits.LeadingZeros64(^imm)
		i = uint32(64 - clo)
		cto = uint32(clo) + uint32(bits.TrailingZeros64(^imm)) - uint32(64-size)
	}
	immr = (uint32(size) - i) & (uint32(size) - 1)
	nimms := (^uint32(size - 1)) << 1
	nimms |= cto - 1
	n = ((nimms >> 6) & 1) ^ 1
	imms = nimms & 0x3f
	return n, immr, imms, true
}

// logicalImm builds a logical-immediate instruction from its 64-bit
// base opcode, clearing the sf bit for the 32-bit form. ok is false
// when imm isn't an encodable bitmask.
//
// The 32-bit form takes the low half of imm, so a negative written at
// 64-bit width (`and w1, w2, #-16` arrives as 0xFFFFFFFFFFFFFFF0) names
// the same mask as its unsigned spelling. GNU as accepts a high half
// that is either all-zero or all-one and drops it; a high half that is
// neither names a value the destination cannot hold and is rejected.
func logicalImm(base64, rd, rn uint32, imm uint64, sf bool) (uint32, bool) {
	regSize := 32
	if sf {
		regSize = 64
	} else {
		if hi := imm >> 32; hi != 0 && hi != 0xffffffff {
			return 0, false
		}
		imm &= 0xffffffff
	}
	n, immr, imms, ok := encodeBitmask(imm, regSize)
	if !ok {
		return 0, false
	}
	insn := base64 | (n << 22) | (immr << 16) | (imms << 10) | ((rn & regMask) << 5) | (rd & regMask)
	if !sf {
		insn &^= 1 << 31
	}
	return insn, true
}

// ANDimm / ORRimm / EORimm encode the logical-immediate ops
// `<op> Rd, Rn, #imm`. The second return is false when imm is not an
// encodable AArch64 bitmask immediate.
func ANDimm(rd, rn uint32, imm uint64, sf bool) (uint32, bool) {
	return logicalImm(0x92000000, rd, rn, imm, sf)
}
func ORRimm(rd, rn uint32, imm uint64, sf bool) (uint32, bool) {
	return logicalImm(0xB2000000, rd, rn, imm, sf)
}
func EORimm(rd, rn uint32, imm uint64, sf bool) (uint32, bool) {
	return logicalImm(0xD2000000, rd, rn, imm, sf)
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

// LoadStoreUnsigned encodes a load/store with an unsigned scaled
// offset for any size (0=byte, 1=half, 2=word, 3=doubleword). The byte
// offset is scaled by the access size to the 12-bit immediate.
// base = (size<<30) | 0x39000000 | (load?0x00400000).
func LoadStoreUnsigned(rt, rn, byteOffset, size uint32, load bool) uint32 {
	base := (size << 30) | 0x39000000
	if load {
		base |= 0x00400000
	}
	return base | (((byteOffset >> size) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// LoadStoreReg encodes a register-offset load/store `[Xn, Xm{, lsl #s}]`
// for any size, with option fixed at LSL/UXTX (3); scaled sets S (Xm
// scaled by the access size).
func LoadStoreReg(rt, rn, rm, size uint32, load, scaled bool) uint32 {
	return LoadStoreRegExt(rt, rn, rm, size, 3, load, scaled)
}

// LoadStoreRegExt is LoadStoreReg with the extend option explicit:
// UXTW=2, LSL/UXTX=3, SXTW=6, SXTX=7 (bits 15:13). The W-indexed options
// widen a 32-bit offset register; scaled sets S, shifting the index by
// log2(access size).
func LoadStoreRegExt(rt, rn, rm, size, option uint32, load, scaled bool) uint32 {
	base := (size << 30) | 0x38200800
	if load {
		base |= 0x00400000
	}
	if scaled {
		base |= 1 << 12
	}
	return base | ((rm & regMask) << 16) | ((option & 7) << 13) | ((rn & regMask) << 5) | (rt & regMask)
}

// The size-specific helpers below wrap the general encoders.
func LDRimm(rt, rn, byteOffset uint32) uint32  { return LoadStoreUnsigned(rt, rn, byteOffset, 3, true) }
func STRimm(rt, rn, byteOffset uint32) uint32  { return LoadStoreUnsigned(rt, rn, byteOffset, 3, false) }
func LDRBimm(rt, rn, byteOffset uint32) uint32 { return LoadStoreUnsigned(rt, rn, byteOffset, 0, true) }
func STRBimm(rt, rn, byteOffset uint32) uint32 {
	return LoadStoreUnsigned(rt, rn, byteOffset, 0, false)
}

func LDRHimm(rt, rn, byteOffset uint32) uint32 { return LoadStoreUnsigned(rt, rn, byteOffset, 1, true) }
func STRHimm(rt, rn, byteOffset uint32) uint32 {
	return LoadStoreUnsigned(rt, rn, byteOffset, 1, false)
}

// ---- Sign-extending loads (unsigned scaled offset) ----
//
// Each loads a narrow value and sign-extends it into the destination.
// to64 selects the 64-bit destination (opc=10) vs the 32-bit one
// (opc=11). byteOffset is scaled by the access size, like the plain
// loads.

// LDRSB: `ldrsb Rt, [Xn, #byteOffset]` (signed byte, scale 1).
func LDRSB(rt, rn, byteOffset uint32, to64 bool) uint32 {
	base := uint32(0x39C00000) // to W (opc=11)
	if to64 {
		base = 0x39800000 // to X (opc=10)
	}
	return base | ((byteOffset & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDRSH: `ldrsh Rt, [Xn, #byteOffset]` (signed halfword, scale 2).
func LDRSH(rt, rn, byteOffset uint32, to64 bool) uint32 {
	base := uint32(0x79C00000)
	if to64 {
		base = 0x79800000
	}
	return base | (((byteOffset / 2) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDRSW: `ldrsw Xt, [Xn, #byteOffset]` (signed word -> 64-bit, scale 4).
func LDRSW(rt, rn, byteOffset uint32) uint32 {
	return 0xB9800000 | (((byteOffset / 4) & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// ---- Unscaled loads / stores (LDUR/STUR family) ----
//
// These carry a SIGNED 9-bit byte offset (-256..255) with no scaling,
// so they reach negative and non-size-aligned displacements that the
// scaled LDRimm/STRimm forms can't. off must fit the 9-bit field.

// LoadStoreUnscaled encodes a load/store with a signed 9-bit unscaled
// offset for any size (0=byte … 3=doubleword).
// base = (size<<30) | 0x38000000 | (load?0x00400000).
func LoadStoreUnscaled(rt, rn uint32, off int32, size uint32, load bool) uint32 {
	base := (size << 30) | 0x38000000
	if load {
		base |= 0x00400000
	}
	return base | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

func LDUR(rt, rn uint32, off int32) uint32  { return LoadStoreUnscaled(rt, rn, off, 3, true) }
func STUR(rt, rn uint32, off int32) uint32  { return LoadStoreUnscaled(rt, rn, off, 3, false) }
func LDURB(rt, rn uint32, off int32) uint32 { return LoadStoreUnscaled(rt, rn, off, 0, true) }
func STURB(rt, rn uint32, off int32) uint32 { return LoadStoreUnscaled(rt, rn, off, 0, false) }

// ---- Load/store pair, pre/post-indexed ----
//
// The frame prologue/epilogue idiom. byteOffset is a signed multiple
// of 8; it is scaled to the 7-bit immediate. STPpre writes the new
// base back BEFORE the access (`[Xn, #off]!`); LDPpost updates it
// AFTER (`[Xn], #off`).

// Pair addressing modes for PairLoadStore (the 24:23 index field).
const (
	PairPost   uint32 = 1 // [Xn], #imm
	PairOffset uint32 = 2 // [Xn, #imm]  (no writeback)
	PairPre    uint32 = 3 // [Xn, #imm]!
)

// PairLoadStore encodes a 64-bit stp/ldp in any addressing mode.
// byteOffset is a signed multiple of 8 (scaled to the 7-bit immediate).
// Encoding: base 0xA8000000 | (load?0x00400000) | mode<<23 | imm7<<15 |
// Rt2<<10 | Rn<<5 | Rt.
func PairLoadStore(rt, rt2, rn uint32, byteOffset int32, load bool, mode uint32) uint32 {
	base := uint32(0xA8000000)
	if load {
		base |= 0x00400000
	}
	base |= mode << 23
	imm7 := uint32(byteOffset/8) & 0x7f
	return base | (imm7 << 15) | ((rt2 & regMask) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

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

// 32-bit (W) bitfield-move bases (sf=0, N=0): immr/imms in 0..31.
func ubfmW(rd, rn, immr, imms uint32) uint32 {
	return 0x53000000 | ((immr & 0x1f) << 16) | ((imms & 0x1f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func sbfmW(rd, rn, immr, imms uint32) uint32 {
	return 0x13000000 | ((immr & 0x1f) << 16) | ((imms & 0x1f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// LSLimmW / LSRimmW / ASRimmW — 32-bit immediate shifts `<op> Wd, Wn,
// #shift` (shift in 1..31).
func LSLimmW(rd, rn, shift uint32) uint32 {
	shift &= 0x1f
	return ubfmW(rd, rn, (32-shift)&0x1f, 31-shift)
}
func LSRimmW(rd, rn, shift uint32) uint32 { shift &= 0x1f; return ubfmW(rd, rn, shift, 31) }
func ASRimmW(rd, rn, shift uint32) uint32 { shift &= 0x1f; return sbfmW(rd, rn, shift, 31) }

// SXTB / SXTH / SXTW encode `sxt<b|h|w> Xd, Wn` — sign-extend the low
// 8 / 16 / 32 bits of Wn into the 64-bit Xd (SBFM aliases with immr=0).
func SXTB(rd, rn uint32) uint32 { return sbfmX(rd, rn, 0, 7) }
func SXTH(rd, rn uint32) uint32 { return sbfmX(rd, rn, 0, 15) }
func SXTW(rd, rn uint32) uint32 { return sbfmX(rd, rn, 0, 31) }

// UXTB / UXTH: zero-extend byte / halfword — aliases of the 32-bit UBFM
// (`ubfm wd, wn, #0, #7|#15`; sf=0 opc=10 → base 0x53000000, immr=0).
// Writing a W destination zeroes the upper 32 bits, so the full X
// register holds the zero-extended value.
func UXTB(rd, rn uint32) uint32 {
	return 0x53001C00 | ((rn & regMask) << 5) | (rd & regMask)
}
func UXTH(rd, rn uint32) uint32 {
	return 0x53003C00 | ((rn & regMask) << 5) | (rd & regMask)
}

// UBFX / SBFX encode `<op> Xd, Xn, #lsb, #width` — extract a width-bit
// field starting at bit lsb, zero- (UBFX) or sign- (SBFX) extended.
// UBFM/SBFM aliases with imms = lsb + width - 1.
func UBFX(rd, rn, lsb, width uint32) uint32 { return ubfmX(rd, rn, lsb, lsb+width-1) }
func SBFX(rd, rn, lsb, width uint32) uint32 { return sbfmX(rd, rn, lsb, lsb+width-1) }

// UBFXW / SBFXW are the W-register (32-bit) forms of the same aliases —
// `ubfx Wd, Wn, #lsb, #width`. The string runtime's small-string decode emits
// these (`ubfx w0, w0, #1, #3` pulls the inline length out of a tagged string
// word), so the assembler needs them to build any program touching strings via
// a runtime helper.
func UBFXW(rd, rn, lsb, width uint32) uint32 { return ubfmW(rd, rn, lsb, lsb+width-1) }
func SBFXW(rd, rn, lsb, width uint32) uint32 { return sbfmW(rd, rn, lsb, lsb+width-1) }

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

// CMNimm encodes `cmn Xn, #imm12{, lsl #12}` — the ADDS XZR, Xn, #imm
// alias (compare-negative against an immediate).
func CMNimm(rn uint32, imm12 uint16, shift12 bool) uint32 {
	var sh uint32
	if shift12 {
		sh = 1
	}
	return 0xB1000000 | (sh << 22) | ((uint32(imm12) & 0xfff) << 10) | ((rn & regMask) << 5) | 31
}

// NEG encodes `neg Xd, Xm` — negate (SUB Xd, XZR, Xm).
// Encoding: base 0xCB000000 | Rm<<16 | 31<<5 | Rd.
func NEG(rd, rm uint32) uint32 {
	return 0xCB000000 | ((rm & regMask) << 16) | (31 << 5) | (rd & regMask)
}

// CLZ encodes `clz Xd, Xn` — count leading zeros (Data-processing, 1
// source). Used by the allocator's large-block power-of-two size class
// (next-pow2 bit position = 64 - clz(size-1)).
// Encoding: base 0xDAC01000 | Rn<<5 | Rd.
func CLZ(rd, rn uint32) uint32 {
	return 0xDAC01000 | ((rn & regMask) << 5) | (rd & regMask)
}

// RBIT encodes `rbit Xd, Xn` — reverse the bit order of the whole
// register (Data-processing, 1 source; the sibling of CLZ and REV16).
// `rbit` then `clz` is the canonical count-trailing-zeros idiom, and it
// inherits clz's definition at zero: reversing zero leaves zero, whose
// clz is the operand width — exactly what OpCtz defines.
// Encoding: base 0xDAC00000 | Rn<<5 | Rd; clearing sf gives the W form.
func RBIT(rd, rn uint32) uint32 {
	return 0xDAC00000 | ((rn & regMask) << 5) | (rd & regMask)
}

// Advanced SIMD encoders. Each is one encoding CLASS, parameterized by the
// fields the class actually has (Q, U, size, opcode, …); the per-mnemonic
// opcode/size tables and the arrangement validation live in gas.go. The
// classes' shared shape is why "add another vector op" is a table row, not a
// new function — and why a wrong size/Q pair must be rejected up front:
// within a class most bit patterns are SOME valid instruction.

// Vec3Same encodes the three-registers-same-arrangement class: the integer
// ALU ops (add/sub/mul, compares, min/max, sshl/ushl), the bitwise ops
// (where `size` selects the operation, not a width), and — with `size` read
// as (0|sz)/(2|sz) — the lane-wise FP ops.
// Encoding: 0 Q U 01110 size 1 Rm opcode 1 Rn Rd =
// 0x0E200400 | Q<<30 | U<<29 | size<<22 | Rm<<16 | opcode<<11 | Rn<<5 | Rd.
func Vec3Same(rd, rn, rm, opcode, size uint32, q, u bool) uint32 {
	return 0x0E200400 | qbit(q) | ubit(u) | ((size & 3) << 22) | ((rm & regMask) << 16) |
		((opcode & 0x1F) << 11) | ((rn & regMask) << 5) | (rd & regMask)
}

// Vec2Misc encodes the two-register miscellaneous class: the unary ops
// (neg/abs/not/cnt/rev*), the compare-against-zero forms (#0 is part of the
// opcode, not a field), the narrowing xtn, and — with `size` read as
// (0|sz)/(2|sz) — the lane-wise FP unaries and int<->float converts.
// Encoding: 0 Q U 01110 size 10000 opcode 10 Rn Rd =
// 0x0E200800 | Q<<30 | U<<29 | size<<22 | opcode<<12 | Rn<<5 | Rd.
func Vec2Misc(rd, rn, opcode, size uint32, q, u bool) uint32 {
	return 0x0E200800 | qbit(q) | ubit(u) | ((size & 3) << 22) |
		((opcode & 0x1F) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecAcross encodes the across-lanes class (addv, s/u min/max-v, the
// widening s/uaddlv): a horizontal reduction into a scalar register.
// Encoding: 0 Q U 01110 size 11000 opcode 10 Rn Rd =
// 0x0E300800 | Q<<30 | U<<29 | size<<22 | opcode<<12 | Rn<<5 | Rd.
func VecAcross(rd, rn, opcode, size uint32, q, u bool) uint32 {
	return 0x0E300800 | qbit(q) | ubit(u) | ((size & 3) << 22) |
		((opcode & 0x1F) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecShiftImm encodes the shift-by-immediate class: shl/sshr/ushr/sli/sri,
// the narrowing shrn and the widening sshll/ushll. The element size and the
// shift amount share the immh:immb field (`immhb`) — immh's top set bit IS
// the element size, which is why the class has no size field — so the caller
// derives it: esize+shift for left shifts, 2*esize-shift for right shifts.
// Encoding: 0 Q U 011110 immh immb opcode 1 Rn Rd =
// 0x0F000400 | Q<<30 | U<<29 | immhb<<16 | opcode<<11 | Rn<<5 | Rd.
func VecShiftImm(rd, rn, opcode, immhb uint32, q, u bool) uint32 {
	return 0x0F000400 | qbit(q) | ubit(u) | ((immhb & 0x7F) << 16) |
		((opcode & 0x1F) << 11) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecPermute encodes the permute class: zip1/zip2/uzp1/uzp2/trn1/trn2
// (opc 011/111/001/101/010/110).
// Encoding: 0 Q 001110 size 0 Rm 0 opc 10 Rn Rd =
// 0x0E000800 | Q<<30 | size<<22 | Rm<<16 | opc<<12 | Rn<<5 | Rd.
func VecPermute(rd, rn, rm, opc, size uint32, q bool) uint32 {
	return 0x0E000800 | qbit(q) | ((size & 3) << 22) | ((rm & regMask) << 16) |
		((opc & 7) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecExt encodes `ext Vd.<T>, Vn.<T>, Vm.<T>, #index` — byte-wise extract
// from a register pair, the NEON sliding window (palignr's counterpart).
// Byte arrangements only; index < 8 (8b) / 16 (16b).
// Encoding: 0 Q 101110 000 Rm 0 imm4 0 Rn Rd =
// 0x2E000000 | Q<<30 | Rm<<16 | index<<11 | Rn<<5 | Rd.
func VecExt(rd, rn, rm, index uint32, q bool) uint32 {
	return 0x2E000000 | qbit(q) | ((rm & regMask) << 16) | ((index & 0xF) << 11) |
		((rn & regMask) << 5) | (rd & regMask)
}

// VecTbl1 encodes `tbl Vd.<T>, {Vn.16b}, Vm.<T>` — the single-table-register
// byte shuffle (pshufb's counterpart). The table is always .16b.
// Encoding: 0 Q 001110 000 Rm 0 len=00 op=0 00 Rn Rd =
// 0x0E000000 | Q<<30 | Rm<<16 | Rn<<5 | Rd.
func VecTbl1(rd, rn, rm uint32, q bool) uint32 {
	return 0x0E000000 | qbit(q) | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// The copy class (dup/ins/umov/smov) addresses a lane through imm5: the
// element size is imm5's lowest set bit and the lane index the bits above
// it, i.e. imm5 = (index<<1|1) << size.

// VecDupElem encodes `dup Vd.<T>, Vn.<Ts>[index]` — broadcast one lane.
// Encoding: 0 Q 001110000 imm5 000001 Rn Rd =
// 0x0E000400 | Q<<30 | imm5<<16 | Rn<<5 | Rd.
func VecDupElem(rd, rn, imm5 uint32, q bool) uint32 {
	return 0x0E000400 | qbit(q) | ((imm5 & 0x1F) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecDupGPR encodes `dup Vd.<T>, Rn` — broadcast a GPR to every lane (the
// needle splat; one instruction where SSE2 needs four).
// Encoding: 0 Q 001110000 imm5 000011 Rn Rd =
// 0x0E000C00 | Q<<30 | imm5<<16 | Rn<<5 | Rd.
func VecDupGPR(rd, rn, imm5 uint32, q bool) uint32 {
	return 0x0E000C00 | qbit(q) | ((imm5 & 0x1F) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecInsGPR encodes `ins Vd.<Ts>[index], Rn` — insert a GPR into one lane.
// Encoding: 01001110000 imm5 000111 Rn Rd =
// 0x4E001C00 | imm5<<16 | Rn<<5 | Rd.
func VecInsGPR(rd, rn, imm5 uint32) uint32 {
	return 0x4E001C00 | ((imm5 & 0x1F) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecInsElem encodes `ins Vd.<Ts>[i], Vn.<Ts>[j]` — lane-to-lane move.
// imm4 is the source index shifted by the element size (j<<size).
// Encoding: 01101110000 imm5 0 imm4 1 Rn Rd =
// 0x6E000400 | imm5<<16 | imm4<<11 | Rn<<5 | Rd.
func VecInsElem(rd, rn, imm5, imm4 uint32) uint32 {
	return 0x6E000400 | ((imm5 & 0x1F) << 16) | ((imm4 & 0xF) << 11) |
		((rn & regMask) << 5) | (rd & regMask)
}

// VecUmov encodes `umov Wd, Vn.<Ts>[index]` (b/h/s lanes) and
// `umov Xd, Vn.d[index]` (q set) — zero-extending lane extract.
// Encoding: 0 Q 001110000 imm5 001111 Rn Rd =
// 0x0E003C00 | Q<<30 | imm5<<16 | Rn<<5 | Rd.
func VecUmov(rd, rn, imm5 uint32, q bool) uint32 {
	return 0x0E003C00 | qbit(q) | ((imm5 & 0x1F) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecSmov encodes `smov Wd|Xd, Vn.<Ts>[index]` — sign-extending lane
// extract; q is the destination width (X when set).
// Encoding: 0 Q 001110000 imm5 001011 Rn Rd =
// 0x0E002C00 | Q<<30 | imm5<<16 | Rn<<5 | Rd.
func VecSmov(rd, rn, imm5 uint32, q bool) uint32 {
	return 0x0E002C00 | qbit(q) | ((imm5 & 0x1F) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// VecMovi encodes the modified-immediate class: `movi Vd.<T>, #imm8` with an
// optional byte-position shift (cmode selects the element width and shift
// slot), and — with op set — the 64-bit bytemask form `movi Vd.2d, #imm64` /
// `movi Dd, #imm64`, where each imm8 bit expands to an all-ones/zero byte.
// Encoding: 0 Q op 0111100000 abc cmode 01 defgh Rd =
// 0x0F000400 | Q<<30 | op<<29 | imm8[7:5]<<16 | cmode<<12 | imm8[4:0]<<5 | Rd.
func VecMovi(rd, imm8, cmode uint32, q, op bool) uint32 {
	return 0x0F000400 | qbit(q) | ubit(op) | ((imm8 >> 5 & 7) << 16) |
		((cmode & 0xF) << 12) | ((imm8 & 0x1F) << 5) | (rd & regMask)
}

// VecLdSt1 encodes ld1/st1 of `nregs` consecutive registers, one structure
// per element: no writeback (wb false), post-index by register (wb, rm<31),
// or post-index by the transfer size (wb, rm=31). The register count selects
// the opcode field (1:0111, 2:1010, 3:0110, 4:0010). NEON has no alignment
// requirement here, so the single-register load is the direct counterpart of
// movdqu rather than of movdqa.
// Encoding: 0 Q 001100 wb L 0 Rm opcode size Rn Rt =
// 0x0C000000 | Q<<30 | wb<<23 | L<<22 | Rm<<16 | opcode<<12 | size<<10 | Rn<<5 | Rt.
func VecLdSt1(rt, rn, rm, nregs, size uint32, q, load, wb bool) uint32 {
	opcode := [5]uint32{0, 7, 0xA, 6, 2}[nregs]
	insn := 0x0C000000 | qbit(q) | ((rm & regMask) << 16) | (opcode << 12) |
		((size & 3) << 10) | ((rn & regMask) << 5) | (rt & regMask)
	if load {
		insn |= 1 << 22
	}
	if wb {
		insn |= 1 << 23
	}
	return insn
}

// VecLd1R encodes `ld1r {Vt.<T>}, [Xn]` — load one element and replicate it
// to every lane — with the same writeback options as VecLdSt1 (rm=31 is
// post-index by the element size).
// Encoding: 0 Q 001101 wb 10 Rm 1100 size Rn Rt =
// 0x0D40C000 | Q<<30 | wb<<23 | Rm<<16 | size<<10 | Rn<<5 | Rt.
func VecLd1R(rt, rn, rm, size uint32, q, wb bool) uint32 {
	insn := 0x0D40C000 | qbit(q) | ((rm & regMask) << 16) |
		((size & 3) << 10) | ((rn & regMask) << 5) | (rt & regMask)
	if wb {
		insn |= 1 << 23
	}
	return insn
}

func ubit(u bool) uint32 {
	if u {
		return 1 << 29
	}
	return 0
}

func qbit(q bool) uint32 {
	if q {
		return 1 << 30
	}
	return 0
}

// UDIV encodes `udiv Xd, Xn, Xm` — unsigned divide (Xn / Xm; division
// by zero yields 0, per the architecture).
// Encoding: base 0x9AC00800 | Rm<<16 | Rn<<5 | Rd.
func UDIV(rd, rn, rm uint32) uint32 {
	return 0x9AC00800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// SDIV encodes `sdiv Xd, Xn, Xm` — signed divide.
// Encoding: base 0x9AC00C00 | Rm<<16 | Rn<<5 | Rd.
func SDIV(rd, rn, rm uint32) uint32 {
	return 0x9AC00C00 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// MSUB encodes `msub Xd, Xn, Xm, Xa` — Xd = Xa - Xn*Xm (the building
// block for the modulo idiom: `udiv q,a,b; msub r,q,b,a`).
// Encoding: base 0x9B008000 | Rm<<16 | Ra<<10 | Rn<<5 | Rd.
func MSUB(rd, rn, rm, ra uint32) uint32 {
	return 0x9B008000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// MRS encodes `mrs Xt, <sysreg>` — read a system register. The register
// is named by the tuple op0:op1:CRn:CRm:op2; op0 is 2 or 3 and only its
// low bit is encoded (bit 19), the rest of bits 31..20 being fixed.
// Encoding: base 0xD5300000 | (op0&1)<<19 | op1<<16 | CRn<<12 | CRm<<8 | op2<<5 | Rt.
func MRS(rt, op0, op1, crn, crm, op2 uint32) uint32 {
	return 0xD5300000 |
		((op0 & 1) << 19) | ((op1 & 7) << 16) |
		((crn & 15) << 12) | ((crm & 15) << 8) | ((op2 & 7) << 5) |
		(rt & regMask)
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

// FABS / FSQRT and the FRINT rounding family (Dd, Dn, double-precision)
// back the cheap f64 math intrinsics — abs, sqrt, floor (round toward
// -inf), ceil (+inf), trunc (toward zero), round (nearest, ties away).
// Each is a single FP instruction; no libm.
// FP load/store of a 64-bit D register (used by the transcendental
// runtime helpers — coefficient loads from .rodata and the d8
// spill/restore in __fern_pow_f64). Three addressing modes:
//
//	unsigned offset : LDR Dt, [Xn, #imm]   imm scaled by 8
//	post-index      : LDR Dt, [Xn], #imm9  (signed, unscaled)
//	pre-index       : STR Dt, [Xn, #imm9]! (signed, unscaled)
func LdrFP64Unsigned(rt, rn, imm12 uint32) uint32 {
	return 0xFD400000 | ((imm12 & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}
func StrFP64Unsigned(rt, rn, imm12 uint32) uint32 {
	return 0xFD000000 | ((imm12 & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}
func LdrFP64PostIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xFC400400 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func StrFP64PostIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xFC000400 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func LdrFP64PreIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xFC400C00 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func StrFP64PreIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xFC000C00 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// LdurFP64 / SturFP64 are the fourth addressing mode: an UNSCALED signed
// 9-bit offset with no writeback (bits 11:10 = 00, where post-index is 01 and
// pre-index is 11). They reach the negative and non-8-aligned displacements
// the scaled unsigned form cannot express, which is why `str d0, [x12, #-8]`
// needs them — GNU as silently rewrites that spelling to `stur`.
//
// Without these the assembler rejected any negative FP offset outright, which
// made it unusable as the oracle for the self-host assembler's own stur/ldur
// support (#6075). Verified against GNU as: stur d0,[x12,#-8] = 0xfc1f8180,
// ldur d0,[x12,#-8] = 0xfc5f8180, stur d1,[x2,#-16] = 0xfc1f0041,
// ldur d3,[x4,#-32] = 0xfc5e0083.
func LdurFP64(rt, rn uint32, imm9 int32) uint32 {
	return 0xFC400000 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func SturFP64(rt, rn uint32, imm9 int32) uint32 {
	return 0xFC000000 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

func FABS(rd, rn uint32) uint32   { return 0x1E60C000 | ((rn & regMask) << 5) | (rd & regMask) }
func FSQRT(rd, rn uint32) uint32  { return 0x1E61C000 | ((rn & regMask) << 5) | (rd & regMask) }
func FRINTM(rd, rn uint32) uint32 { return 0x1E654000 | ((rn & regMask) << 5) | (rd & regMask) }
func FRINTP(rd, rn uint32) uint32 { return 0x1E64C000 | ((rn & regMask) << 5) | (rd & regMask) }
func FRINTZ(rd, rn uint32) uint32 { return 0x1E65C000 | ((rn & regMask) << 5) | (rd & regMask) }
func FRINTA(rd, rn uint32) uint32 { return 0x1E664000 | ((rn & regMask) << 5) | (rd & regMask) }

// FRINTN — round to nearest, ties to EVEN, as distinct from FRINTA's ties
// away from zero. It is the arm64 spelling of x86-64's `roundsd …, 0`, so
// the transcendental kernels' argument reduction picks the same integer on
// both backends and their results agree bit for bit.
func FRINTN(rd, rn uint32) uint32 { return 0x1E644000 | ((rn & regMask) << 5) | (rd & regMask) }

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

// ---- Floating-point (single-precision) + remaining conversions ----
//
// Single-precision (S-register) forms are ftype=00 — the double forms
// with bit 22 cleared. Plus fcvt precision converts and the unsigned
// int<->float conversions.

// FADDS/FSUBS/FMULS/FDIVS Sd, Sn, Sm — single-precision arithmetic.
func FADDS(rd, rn, rm uint32) uint32 {
	return 0x1E202800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FSUBS(rd, rn, rm uint32) uint32 {
	return 0x1E203800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMULS(rd, rn, rm uint32) uint32 {
	return 0x1E200800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FDIVS(rd, rn, rm uint32) uint32 {
	return 0x1E201800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// FNEGS Sd, Sn / FCMPS Sn, Sm / FMOVS Sd, Sn — single-precision.
func FNEGS(rd, rn uint32) uint32 { return 0x1E214000 | ((rn & regMask) << 5) | (rd & regMask) }
func FCMPS(rn, rm uint32) uint32 { return 0x1E202000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) }
func FMOVS(rd, rn uint32) uint32 { return 0x1E204000 | ((rn & regMask) << 5) | (rd & regMask) }

// FMOVSfromGPR Sd, Wn — copy the raw 32 bits of Wn into Sd; FMOVStoGPR
// Wd, Sn — the reverse. These are the single-precision (ftype=00,
// sf=0) twins of FMOVfromGPR / FMOVtoGPR.
func FMOVSfromGPR(rd, rn uint32) uint32 {
	return 0x1E270000 | ((rn & regMask) << 5) | (rd & regMask)
}
func FMOVStoGPR(rd, rn uint32) uint32 {
	return 0x1E260000 | ((rn & regMask) << 5) | (rd & regMask)
}

// FCVTStoD Dd, Sn — widen single to double; FCVTDtoS Sd, Dn — narrow.
func FCVTStoD(rd, rn uint32) uint32 { return 0x1E22C000 | ((rn & regMask) << 5) | (rd & regMask) }
func FCVTDtoS(rd, rn uint32) uint32 { return 0x1E624000 | ((rn & regMask) << 5) | (rd & regMask) }

// SCVTFS Sd, Xn — signed int -> single; FCVTZSS Xd, Sn — single ->
// signed int (truncate).
func SCVTFS(rd, rn uint32) uint32  { return 0x9E220000 | ((rn & regMask) << 5) | (rd & regMask) }
func FCVTZSS(rd, rn uint32) uint32 { return 0x9E380000 | ((rn & regMask) << 5) | (rd & regMask) }

// FCVTZUS Xd, Sn — single → unsigned int (truncate). The
// single-precision (type=00) twin of FCVTZU's double form; the
// caller clears bit 31 (clearSF) for a `w` destination.
func FCVTZUS(rd, rn uint32) uint32 { return 0x9E390000 | ((rn & regMask) << 5) | (rd & regMask) }

// UCVTF Dd, Xn — unsigned int -> double; FCVTZU Xd, Dn — double ->
// unsigned int (truncate).
func UCVTF(rd, rn uint32) uint32  { return 0x9E630000 | ((rn & regMask) << 5) | (rd & regMask) }
func FCVTZU(rd, rn uint32) uint32 { return 0x9E790000 | ((rn & regMask) << 5) | (rd & regMask) }

// UCVTFS Sd, Xn — unsigned int -> single. The single-precision
// (type=00) twin of UCVTF's double form; the caller clears bit 31
// (clearSF) for a `w` (32-bit) source.
func UCVTFS(rd, rn uint32) uint32 { return 0x9E230000 | ((rn & regMask) << 5) | (rd & regMask) }

// ADRP encodes `adrp Xd, <target>` — form the PC-relative page address
// of a symbol: Xd = (PC & ~0xfff) + (pageDelta << 12). pageDelta is the
// signed 21-bit difference, in 4 KiB pages, between the target's page
// and the instruction's page. Pair with an `add Xd, Xd, #:lo12:sym`
// (ADDimm with the low 12 bits) to materialise the full address.
//
// Encoding: op=1 immlo(2) 10000 immhi(19) Rd
// → base 0x90000000 | immlo<<29 | immhi<<5 | Rd.
func ADRP(rd uint32, pageDelta int32) uint32 {
	imm := uint32(pageDelta)
	immlo := imm & 0x3
	immhi := (imm >> 2) & 0x7ffff
	return 0x90000000 | (immlo << 29) | (immhi << 5) | (rd & regMask)
}

// ---- Register-offset loads/stores ----
//
// `ldr Xt, [Xn, Xm{, lsl #3}]` — address = Xn + (Xm << (3 if scaled)).
// option is always LSL/UXTX (3); scaled sets the S bit (index scaled by
// the 8-byte access size). The no-shift form passes scaled=false.

func LDRreg(rt, rn, rm uint32, scaled bool) uint32 {
	return LoadStoreReg(rt, rn, rm, 3, true, scaled)
}

func STRreg(rt, rn, rm uint32, scaled bool) uint32 {
	return LoadStoreReg(rt, rn, rm, 3, false, scaled)
}

// ---- Single-register pre/post-indexed loads/stores ----
//
// The writeback forms used for stack pushes/pops: `str Xt, [Xn, #o]!`
// (pre-index) and `ldr Xt, [Xn], #o` (post-index). Signed 9-bit
// unscaled offset, like LDUR/STUR, but with the index/writeback bits.

// IdxLoadStore encodes a pre/post-indexed load or store across sizes.
// size: 0=byte, 1=half, 2=word(w), 3=doubleword(x). load selects ldr
// vs str; pre selects [Xn, #off]! (pre-index) vs [Xn], #off (post). off
// is the signed 9-bit unscaled byte displacement.
//
// base = (size<<30) | 0x38000400 | (load?0x00400000) | (pre?0x800).
func IdxLoadStore(rt, rn uint32, off int32, size uint32, load, pre bool) uint32 {
	base := (size << 30) | 0x38000400
	if load {
		base |= 0x00400000
	}
	if pre {
		base |= 0x800
	}
	return base | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// SVC encodes `svc #imm16` — supervisor call (syscall) trap.
//
// Encoding: 11010100 000 imm16 00001 → base 0xD4000001 | imm16<<5.
func SVC(imm16 uint16) uint32 {
	return 0xD4000001 | (uint32(imm16) << 5)
}

// BRK encodes `brk #imm16` — a breakpoint, which raises a synchronous
// exception rather than trapping to a syscall handler. This is the
// hostless "stop": SIGTRAP under a kernel, an exception vector without
// one (#6510).
//
// Encoding: 11010100 001 imm16 00000 → base 0xD4200000 | imm16<<5.
func BRK(imm16 uint16) uint32 {
	return 0xD4200000 | (uint32(imm16) << 5)
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
	CondNV uint32 = 15 // never — encodes 0b1111 and behaves as always, but is
	// a distinct encoding gas accepts and round-trips, so it must not be
	// folded onto AL.
)

// ---- Add/subtract with carry ----
//
// `<op> Rd, Rn, Rm` — Rd = Rn ± Rm ± C. The 64-bit bases; clearSF gives
// the W forms. NGC/NGCS are the Rn=XZR aliases.
//
// Encoding: sf op S 11010000 Rm 000000 Rn Rd
// → ADC 0x9A000000, ADCS 0xBA000000, SBC 0xDA000000, SBCS 0xFA000000.
func ADC(rd, rn, rm uint32) uint32 {
	return 0x9A000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func ADCS(rd, rn, rm uint32) uint32 {
	return 0xBA000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func SBC(rd, rn, rm uint32) uint32 {
	return 0xDA000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func SBCS(rd, rn, rm uint32) uint32 {
	return 0xFA000000 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func NGC(rd, rm uint32) uint32  { return SBC(rd, 31, rm) }
func NGCS(rd, rm uint32) uint32 { return SBCS(rd, 31, rm) }

// UMULH / SMULH encode `<op> Xd, Xn, Xm` — the high 64 bits of the
// 128-bit product. 64-bit only: the instruction has no sf bit to clear
// (a W form does not exist).
//
// Encoding: 10011011 U 10 Rm 011111 Rn Rd
// → UMULH base 0x9BC07C00, SMULH base 0x9B407C00.
func UMULH(rd, rn, rm uint32) uint32 {
	return 0x9BC07C00 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func SMULH(rd, rn, rm uint32) uint32 {
	return 0x9B407C00 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// MADD encodes `madd Xd, Xn, Xm, Xa` — Xd = Xa + Xn*Xm (MUL above is
// the Ra=XZR special case).
// Encoding: base 0x9B000000 | Rm<<16 | Ra<<10 | Rn<<5 | Rd.
func MADD(rd, rn, rm, ra uint32) uint32 {
	return 0x9B000000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// SMADDL / UMADDL / SMSUBL / UMSUBL encode the widening multiplies
// `<op> Xd, Wn, Wm, Xa` — Xd = Xa ± sign_or_zero_extend(Wn * Wm). The
// operand widths are fixed (no sf bit): destination and accumulator are
// X, sources are W. The smull/umull mnemonics are the Ra=XZR aliases,
// which asmMulLong forms directly.
//
// Encoding: 10011011 U 01 Rm o0 Ra Rn Rd
// → SMADDL 0x9B200000, UMADDL 0x9BA00000, SMSUBL 0x9B208000, UMSUBL 0x9BA08000.
func SMADDL(rd, rn, rm, ra uint32) uint32 {
	return 0x9B200000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func UMADDL(rd, rn, rm, ra uint32) uint32 {
	return 0x9BA00000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func SMSUBL(rd, rn, rm, ra uint32) uint32 {
	return 0x9B208000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func UMSUBL(rd, rn, rm, ra uint32) uint32 {
	return 0x9BA08000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// ANDSregShift encodes the flag-setting `ands Rd, Rn, Rm{, <shift> #amt}`;
// the plain register form is the shiftType=0, amount=0 case. TST is the
// Rd=XZR alias.
// Encoding: base 0xEA000000 (opc=11 of the logical shifted-register class).
func ANDSregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xEA000000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// ANDSimm encodes `ands Rd, Rn, #imm` (logical immediate, opc=11). ok is
// false when imm is not an encodable bitmask.
func ANDSimm(rd, rn uint32, imm uint64, sf bool) (uint32, bool) {
	return logicalImm(0xF2000000, rd, rn, imm, sf)
}

// BICregShift / BICSregShift / ORNregShift / EONregShift encode the
// negated logical shifted-register ops `<op> Rd, Rn, Rm{, <shift> #amt}`
// (bit 21 = invert Rm). The plain register forms are shiftType=0,
// amount=0. No immediate encoding exists for these — the bitmask class
// has no invert bit. MVN is ORN's Rn=XZR alias.
func BICregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0x8A200000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func BICSregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xEA200000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func ORNregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xAA200000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func EONregShift(rd, rn, rm, shiftType, amount uint32) uint32 {
	return 0xCA200000 | ((shiftType & 3) << 22) | ((rm & regMask) << 16) | ((amount & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// EXTR / EXTRW encode `extr Rd, Rn, Rm, #lsb` — extract a register-width
// field from the Rn:Rm concatenation starting at bit lsb of Rm. The
// standalone `ror Rd, Rn, #n` is the Rn=Rm alias.
//
// Encoding: sf 00 100111 N 0 Rm imms Rn Rd, N=sf
// → 64-bit base 0x93C00000 (lsb 0..63), 32-bit base 0x13800000 (lsb 0..31).
func EXTR(rd, rn, rm, lsb uint32) uint32 {
	return 0x93C00000 | ((rm & regMask) << 16) | ((lsb & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func EXTRW(rd, rn, rm, lsb uint32) uint32 {
	return 0x13800000 | ((rm & regMask) << 16) | ((lsb & 0x1f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// RORV encodes `ror Xd, Xn, Xm` — rotate right by the low bits of Xm
// (data-processing 2-source, the sibling of LSLV/LSRV/ASRV, op2=11).
// Encoding: base 0x9AC02C00 | Rm<<16 | Rn<<5 | Rd.
func RORV(rd, rn, rm uint32) uint32 {
	return 0x9AC02C00 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// bfmX / bfmW are the bitfield-move bases with opc=01 (insert: bits
// outside the field keep Rd's old value), completing the UBFM/SBFM
// family above. The bfi/bfxil/ubfiz/sbfiz aliases compute immr/imms in
// the parser.
// Encoding: sf 01 100110 N immr imms Rn Rd → X base 0xB3400000 (N=1),
// W base 0x33000000 (N=0).
func bfmX(rd, rn, immr, imms uint32) uint32 {
	return 0xB3400000 | ((immr & 0x3f) << 16) | ((imms & 0x3f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func bfmW(rd, rn, immr, imms uint32) uint32 {
	return 0x33000000 | ((immr & 0x1f) << 16) | ((imms & 0x1f) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// CCMPreg / CCMPimm / CCMNreg / CCMNimm encode the conditional compares
// `ccmp/ccmn Rn, Rm|#imm5, #nzcv, <cond>`: if cond holds, compare
// (setting flags exactly as cmp/cmn would); else set the flags to nzcv.
// The immediate form's imm5 is UNSIGNED 0..31.
//
// Encoding: sf op 111010010 Rm|imm5 cond 0 ir 0 Rn 0 nzcv, ir=1 for the
// immediate form → CCMP reg 0xFA400000 / imm 0xFA400800, CCMN reg
// 0xBA400000 / imm 0xBA400800.
func CCMPreg(rn, rm, nzcv, cond uint32) uint32 {
	return 0xFA400000 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (nzcv & 0xf)
}
func CCMPimm(rn, imm5, nzcv, cond uint32) uint32 {
	return 0xFA400800 | ((imm5 & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (nzcv & 0xf)
}
func CCMNreg(rn, rm, nzcv, cond uint32) uint32 {
	return 0xBA400000 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (nzcv & 0xf)
}
func CCMNimm(rn, imm5, nzcv, cond uint32) uint32 {
	return 0xBA400800 | ((imm5 & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (nzcv & 0xf)
}

// CSINC / CSINV / CSNEG encode `<op> Xd, Xn, Xm, <cond>` — Xd = cond ?
// Xn : (Xm+1 | ~Xm | -Xm). CSET above is CSINC's Rn=Rm=XZR alias; the
// cinc/cinv/cneg/csetm aliases (inverted cond) live in the parser.
// Encoding: CSINC 0x9A800400, CSINV 0xDA800000, CSNEG 0xDA800400,
// each | Rm<<16 | cond<<12 | Rn<<5 | Rd.
func CSINC(rd, rn, rm, cond uint32) uint32 {
	return 0x9A800400 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}
func CSINV(rd, rn, rm, cond uint32) uint32 {
	return 0xDA800000 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}
func CSNEG(rd, rn, rm, cond uint32) uint32 {
	return 0xDA800400 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}

// REV64 / REVW reverse the byte order of the whole register. The X and W
// forms differ in the opc field (11 vs 10), not just sf, so clearSF
// cannot derive one from the other. REV32 (X only — the W-width
// operation IS `rev Wd, Wn`) reverses bytes within each 32-bit word.
// Encoding: REV64 0xDAC00C00, REVW 0x5AC00800, REV32 0xDAC00800.
func REV64(rd, rn uint32) uint32 { return 0xDAC00C00 | ((rn & regMask) << 5) | (rd & regMask) }
func REVW(rd, rn uint32) uint32  { return 0x5AC00800 | ((rn & regMask) << 5) | (rd & regMask) }
func REV32(rd, rn uint32) uint32 { return 0xDAC00800 | ((rn & regMask) << 5) | (rd & regMask) }

// CLS encodes `cls Xd, Xn` — count leading sign bits (CLZ's sibling,
// op=101). clearSF gives the W form.
// Encoding: base 0xDAC01400 | Rn<<5 | Rd.
func CLS(rd, rn uint32) uint32 {
	return 0xDAC01400 | ((rn & regMask) << 5) | (rd & regMask)
}

// ADR encodes `adr Xd, <target>` — Xd = PC + byteDelta. Unlike ADRP the
// signed 21-bit immediate is in BYTES, spanning ±1 MB.
// Encoding: op=0 immlo(2) 10000 immhi(19) Rd
// → base 0x10000000 | immlo<<29 | immhi<<5 | Rd.
func ADR(rd uint32, byteDelta int32) uint32 {
	imm := uint32(byteDelta)
	return 0x10000000 | ((imm & 3) << 29) | (((imm >> 2) & 0x7ffff) << 5) | (rd & regMask)
}

// PairLoadStoreW / PairLoadStoreD are PairLoadStore for W registers
// (32-bit GPR pair, offset scaled by 4) and D registers (64-bit FP pair,
// scaled by 8). Same three addressing modes.
// Encoding: W base 0x28000000, D base 0x6C000000, each | (load?
// 0x00400000) | mode<<23 | imm7<<15 | Rt2<<10 | Rn<<5 | Rt.
func PairLoadStoreW(rt, rt2, rn uint32, byteOffset int32, load bool, mode uint32) uint32 {
	base := uint32(0x28000000)
	if load {
		base |= 0x00400000
	}
	base |= mode << 23
	imm7 := uint32(byteOffset/4) & 0x7f
	return base | (imm7 << 15) | ((rt2 & regMask) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}
func PairLoadStoreD(rt, rt2, rn uint32, byteOffset int32, load bool, mode uint32) uint32 {
	base := uint32(0x6C000000)
	if load {
		base |= 0x00400000
	}
	base |= mode << 23
	imm7 := uint32(byteOffset/8) & 0x7f
	return base | (imm7 << 15) | ((rt2 & regMask) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}

// FP load/store of a 32-bit S register — the single-precision twins of
// the D-register forms above, in the same four addressing modes. The
// unsigned offset is scaled by 4.
func LdrFP32Unsigned(rt, rn, imm12 uint32) uint32 {
	return 0xBD400000 | ((imm12 & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}
func StrFP32Unsigned(rt, rn, imm12 uint32) uint32 {
	return 0xBD000000 | ((imm12 & 0xfff) << 10) | ((rn & regMask) << 5) | (rt & regMask)
}
func LdrFP32PostIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xBC400400 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func StrFP32PostIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xBC000400 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func LdrFP32PreIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xBC400C00 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func StrFP32PreIdx(rt, rn uint32, imm9 int32) uint32 {
	return 0xBC000C00 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func LdurFP32(rt, rn uint32, imm9 int32) uint32 {
	return 0xBC400000 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}
func SturFP32(rt, rn uint32, imm9 int32) uint32 {
	return 0xBC000000 | ((uint32(imm9) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// LoadSignedUnscaled encodes ldursb/ldursh/ldursw — the sign-extending
// loads with a signed 9-bit unscaled offset. size 0=byte, 1=half,
// 2=word; to64 selects the X destination (opc=10) vs W (opc=11). ldursw
// is size 2, X only.
// base = (size<<30) | 0x38800000 | (to64 ? 0 : 0x00400000).
func LoadSignedUnscaled(rt, rn uint32, off int32, size uint32, to64 bool) uint32 {
	base := (size << 30) | 0x38800000
	if !to64 {
		base |= 0x00400000
	}
	return base | ((uint32(off) & 0x1ff) << 12) | ((rn & regMask) << 5) | (rt & regMask)
}

// ---- Exclusive / acquire-release accesses (ARMv8.0, no LSE) ----
//
// size: 0=byte, 1=half, 2=word, 3=doubleword. The status register of
// the store-exclusives (Rs) is always a W register.

// LDXR encodes `ldxr Rt, [Xn]` (acquire=false) / `ldaxr` (acquire=true).
// base = (size<<30) | 0x085F7C00 | (acquire ? 0x8000).
func LDXR(rt, rn, size uint32, acquire bool) uint32 {
	base := (size << 30) | 0x085F7C00
	if acquire {
		base |= 0x8000
	}
	return base | ((rn & regMask) << 5) | (rt & regMask)
}

// STXR encodes `stxr Ws, Rt, [Xn]` (release=false) / `stlxr`
// (release=true): store if the exclusive monitor holds, Ws=0 on success.
// base = (size<<30) | 0x08007C00 | (release ? 0x8000).
func STXR(rs, rt, rn, size uint32, release bool) uint32 {
	base := (size << 30) | 0x08007C00
	if release {
		base |= 0x8000
	}
	return base | ((rs & regMask) << 16) | ((rn & regMask) << 5) | (rt & regMask)
}

// LDAR / STLR encode the non-exclusive load-acquire / store-release.
// bases (size<<30) | 0x08DFFC00 and (size<<30) | 0x089FFC00.
func LDAR(rt, rn, size uint32) uint32 {
	return (size << 30) | 0x08DFFC00 | ((rn & regMask) << 5) | (rt & regMask)
}
func STLR(rt, rn, size uint32) uint32 {
	return (size << 30) | 0x089FFC00 | ((rn & regMask) << 5) | (rt & regMask)
}

// DMB / DSB encode the data barriers with a 4-bit option (sy=0xF etc. —
// see barrierOpts in the parser). ISB takes only the sy option, encoded
// as the fixed word 0xD5033FDF.
func DMB(option uint32) uint32 { return 0xD50330BF | ((option & 0xf) << 8) }
func DSB(option uint32) uint32 { return 0xD503309F | ((option & 0xf) << 8) }
func ISB() uint32              { return 0xD5033FDF }

// ---- Floating-point fused / three-source and remaining scalar ops ----
//
// All are the ftype=01 (double) encodings; clearing bit 22 (see
// fpSingle in the parser) gives the single-precision form.

// FMADD/FMSUB/FNMADD/FNMSUB Dd, Dn, Dm, Da — fused multiply-add family:
// Da ± Dn*Dm, negated for the FN* forms.
// Encoding: 0x1F400000 / 0x1F408000 / 0x1F600000 / 0x1F608000,
// each | Rm<<16 | Ra<<10 | Rn<<5 | Rd.
func FMADD(rd, rn, rm, ra uint32) uint32 {
	return 0x1F400000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMSUB(rd, rn, rm, ra uint32) uint32 {
	return 0x1F408000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func FNMADD(rd, rn, rm, ra uint32) uint32 {
	return 0x1F600000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}
func FNMSUB(rd, rn, rm, ra uint32) uint32 {
	return 0x1F608000 | ((rm & regMask) << 16) | ((ra & regMask) << 10) | ((rn & regMask) << 5) | (rd & regMask)
}

// FNMUL Dd, Dn, Dm — negated multiply; FMIN/FMAX and the FMINNM/FMAXNM
// NaN-propagating-minNum variants, Dd, Dn, Dm.
func FNMUL(rd, rn, rm uint32) uint32 {
	return 0x1E608800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMIN(rd, rn, rm uint32) uint32 {
	return 0x1E605800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMAX(rd, rn, rm uint32) uint32 {
	return 0x1E604800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMINNM(rd, rn, rm uint32) uint32 {
	return 0x1E607800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}
func FMAXNM(rd, rn, rm uint32) uint32 {
	return 0x1E606800 | ((rm & regMask) << 16) | ((rn & regMask) << 5) | (rd & regMask)
}

// FCSEL Dd, Dn, Dm, <cond> — Dd = cond ? Dn : Dm.
// Encoding: base 0x1E600C00 | Rm<<16 | cond<<12 | Rn<<5 | Rd.
func FCSEL(rd, rn, rm, cond uint32) uint32 {
	return 0x1E600C00 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (rd & regMask)
}

// FCCMP Dn, Dm, #nzcv, <cond> — conditional FP compare: if cond holds,
// compare; else set the flags to nzcv.
// Encoding: base 0x1E600400 | Rm<<16 | cond<<12 | Rn<<5 | nzcv.
func FCCMP(rn, rm, nzcv, cond uint32) uint32 {
	return 0x1E600400 | ((rm & regMask) << 16) | ((cond & 0xf) << 12) | ((rn & regMask) << 5) | (nzcv & 0xf)
}

// MSRreg encodes `msr <sysreg>, Xt` — write a system register: the MRS
// encoding with the direction bit (bit 21) clear.
// Encoding: base 0xD5100000 | (op0&1)<<19 | op1<<16 | CRn<<12 | CRm<<8 | op2<<5 | Rt.
func MSRreg(rt, op0, op1, crn, crm, op2 uint32) uint32 {
	return 0xD5100000 |
		((op0 & 1) << 19) | ((op1 & 7) << 16) |
		((crn & 15) << 12) | ((crm & 15) << 8) | ((op2 & 7) << 5) |
		(rt & regMask)
}

// Put appends insn to buf as 4 little-endian bytes.
func Put(buf []byte, insn uint32) []byte {
	return append(buf, byte(insn), byte(insn>>8), byte(insn>>16), byte(insn>>24))
}
