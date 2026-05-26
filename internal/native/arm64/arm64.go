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

// Put appends insn to buf as 4 little-endian bytes.
func Put(buf []byte, insn uint32) []byte {
	return append(buf, byte(insn), byte(insn>>8), byte(insn>>16), byte(insn>>24))
}
