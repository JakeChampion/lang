package x86_64

import (
	"fmt"

	"github.com/jakechampion/lang/internal/native/x86tbl"
)

// SSE scalar floating-point support. The Fern x86-64 code generator keeps
// f64 values in general-purpose registers as bit patterns and shuttles
// them into xmm registers with movq for each arithmetic/compare op, so the
// surface here is: GPR<->XMM transfers (movq/movd), scalar arithmetic
// (add/sub/mul/div/sqrt sd/ss), ordered compares (ucomis/comis),
// conversions (cvtsi2s*, cvtts*2si, cvts*2s*), aligned moves (movap*),
// scalar loads/stores (movsd/movss), and roundsd.

// sseOps are the two-operand "dst, src" forms encoded as
// [prefix] [REX] 0F <op> /r, with ModRM.reg = dst and rm = src. The
// operand size is implied by the mandatory prefix, so REX.W is never set
// (REX.R/B still extend xmm8..15). A zero prefix means "no mandatory
// prefix" (the scalar/packed-single compare forms).
// sseOps is the two-byte-opcode SSE vocabulary, from the shared table the
// self-host assembler is generated from (#7903). Keeping one list is what
// stops a form existing on one side and not the other.
var sseOps = func() map[string]struct{ prefix, op byte } {
	m := make(map[string]struct{ prefix, op byte }, 128)
	for k, v := range x86tbl.SSEOpMap() {
		m[k] = struct{ prefix, op byte }{v.Prefix, v.Op}
	}
	return m
}()

// sse38Ops are the three-byte-opcode forms 66 0F 38 <op> /r with an xmm
// destination (SSE4.1's packed min/max/multiply and ptest — in the declared
// Haswell baseline).
var sse38Ops = map[string]byte{
	"ptest": 0x17, "pmulld": 0x40,
	"pminsb": 0x38, "pminsd": 0x39, "pminuw": 0x3A, "pminud": 0x3B,
	"pmaxsb": 0x3C, "pmaxsd": 0x3D, "pmaxuw": 0x3E, "pmaxud": 0x3F,
}

func (a *Assembler) sse38Op(op byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size != 128 {
		return fmt.Errorf("SSE 0F38 op expects xmm, xmm/mem")
	}
	dst, src := ops[0], ops[1]
	a.emit(0x66)
	a.emitRexRM(false, dst.reg, src)
	a.emit(0x0F, 0x38, op)
	a.emitModRM(dst.reg, src)
	return nil
}

// vecShiftImmOps maps a shift-by-immediate mnemonic to its 0F 71/72/73
// group opcode and /digit; the shifted register is ModRM.rm.
var vecShiftImmOps = map[string]struct {
	op  byte
	ext int
}{
	"psrlw": {0x71, 2}, "psraw": {0x71, 4}, "psllw": {0x71, 6},
	"psrld": {0x72, 2}, "psrad": {0x72, 4}, "pslld": {0x72, 6},
	"psrlq": {0x73, 2}, "psllq": {0x73, 6},
	"psrldq": {0x73, 3}, "pslldq": {0x73, 7},
}

func (a *Assembler) vecShiftImm(mnem string, ops []operand) error {
	if ops[0].kind != opReg || ops[0].size != 128 {
		return fmt.Errorf("%s expects xmm, imm8", mnem)
	}
	e := vecShiftImmOps[mnem]
	a.emit(0x66)
	if rex := rexFor(false, 0, ops[0].reg, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, e.op)
	a.emit(modrmReg(e.ext, ops[0].reg))
	a.emit(byte(ops[1].imm))
	return nil
}

// movdqStore encodes the STORE direction of movdqu/movdqa — `movdqu
// [mem], xmm`, opcode 0x7F rather than the 0x6F the load uses.
//
// The sseOps table above only covers `xmm <- xmm/mem`, because every scalar
// float op is that shape. A vector kernel needs the other direction too, and
// getting it wrong is silent: 0x6F with the operands written backwards
// assembles cleanly and reads from the wrong address.
func (a *Assembler) movdqStore(prefix byte, ops []operand) error {
	if len(ops) != 2 || ops[1].kind != opReg || ops[1].size != 128 {
		return fmt.Errorf("movdqu/movdqa store expects mem, xmm")
	}
	dst, src := ops[0], ops[1]
	a.emit(prefix)
	a.emitRexRM(false, src.reg, dst)
	a.emit(0x0F, 0x7F)
	a.emitModRM(src.reg, dst)
	return nil
}

// xmmToGpr encodes the mask-extraction forms pmovmskb (66 0F D7 /r),
// movmskps (0F 50) and movmskpd (66 0F 50): gather sign bits into a GPR.
//
// These are the bridge out of the vector domain — the instructions that turn
// a compare mask into something `bsf` can scan — so the destination is a
// GENERAL-PURPOSE register while ModRM.reg still names it. That inversion is
// why they cannot be sseOps entries: the table assumes reg is an xmm.
func (a *Assembler) xmmToGpr(prefix, op byte, ops []operand, name string) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size == 128 || ops[0].size < 32 ||
		ops[1].kind != opReg || ops[1].size != 128 {
		return fmt.Errorf("%s expects r32/r64, xmm", name)
	}
	dst, src := ops[0], ops[1]
	if prefix != 0 {
		a.emit(prefix)
	}
	if rex := rexFor(false, dst.reg, src.reg, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, op)
	a.emit(modrmReg(dst.reg, src.reg))
	return nil
}

// sseImm8 encodes the two-byte-opcode shuffle forms `<op> xmm, xmm/mem,
// imm8` ([prefix] 0F <op> /r ib): pshufd (66 0F 70), shufps (0F C6),
// shufpd (66 0F C6). `pshufd x, x, 0` broadcasts lane 0 to all four, which
// is the last step of the byte splat.
func (a *Assembler) sseImm8(prefix, op byte, ops []operand, name string) error {
	if len(ops) != 3 || ops[0].kind != opReg || ops[0].size != 128 || ops[2].kind != opImm {
		return fmt.Errorf("%s expects xmm, xmm/mem, imm8", name)
	}
	dst, src, imm := ops[0], ops[1], ops[2]
	if prefix != 0 {
		a.emit(prefix)
	}
	a.emitRexRM(false, dst.reg, src)
	a.emit(0x0F, op)
	a.emitModRM(dst.reg, src)
	a.emit(byte(imm.imm))
	return nil
}

// sseOp encodes a symmetric two-operand SSE instruction (dst is an xmm
// register; src is xmm or memory).
func (a *Assembler) sseOp(prefix, op byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size != 128 {
		return fmt.Errorf("SSE op expects xmm, xmm/mem")
	}
	dst, src := ops[0], ops[1]
	if prefix != 0 {
		a.emit(prefix)
	}
	a.emitRexRM(false, dst.reg, src)
	a.emit(0x0F, op)
	a.emitModRM(dst.reg, src)
	return nil
}

// cvtsi2s encodes cvtsi2sd/cvtsi2ss: xmm <- r/m32|64. REX.W follows the
// integer source width.
func (a *Assembler) cvtsi2s(prefix byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size != 128 {
		return fmt.Errorf("cvtsi2sd/ss expects xmm, r/m")
	}
	dst, src := ops[0], ops[1]
	srcSize := src.size
	if src.kind == opMem {
		// The integer source width is invisible in a memory operand, and it
		// selects REX.W: an unsized qword load would silently convert only
		// 32 bits.
		srcSize = src.memSize
	}
	if srcSize != 32 && srcSize != 64 {
		return fmt.Errorf("cvtsi2sd/ss source must be a 32- or 64-bit GPR or dword/qword ptr memory")
	}
	a.emit(prefix)
	a.emitRexRM(srcSize == 64, dst.reg, src)
	a.emit(0x0F, 0x2A)
	a.emitModRM(dst.reg, src)
	return nil
}

// cvtt2si encodes cvttsd2si/cvttss2si (truncating): r32|64 <- xmm/mem.
// REX.W follows the integer destination width.
func (a *Assembler) cvtt2si(prefix, op byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || (ops[0].size != 32 && ops[0].size != 64) {
		return fmt.Errorf("cvttsd2si/ss expects an r32/r64 destination")
	}
	dst, src := ops[0], ops[1]
	a.emit(prefix)
	a.emitRexRM(dst.size == 64, dst.reg, src)
	a.emit(0x0F, op)
	a.emitModRM(dst.reg, src)
	return nil
}

// movqd encodes movq (64-bit) / movd (32-bit) transfers between an xmm
// register and a GPR or memory. isMovd selects the 32-bit form (no REX.W).
func (a *Assembler) movqd(ops []operand, isMovd bool) error {
	if len(ops) != 2 {
		return fmt.Errorf("movq/movd expects two operands")
	}
	dst, src := ops[0], ops[1]
	dstX := dst.kind == opReg && dst.size == 128
	srcX := src.kind == opReg && src.size == 128
	for _, o := range []operand{dst, src} {
		if o.kind == opReg && o.size != 128 && o.size != 32 && o.size != 64 {
			return fmt.Errorf("movq/movd GPR operand must be 32- or 64-bit")
		}
	}
	w := !isMovd
	switch {
	case dstX && !srcX: // xmm <- r/m : 66 [W] 0F 6E /r, reg=xmm(dst), rm=src
		a.emit(0x66)
		a.emitRexRM(w, dst.reg, src)
		a.emit(0x0F, 0x6E)
		a.emitModRM(dst.reg, src)
		return nil
	case !dstX && srcX: // r/m <- xmm : 66 [W] 0F 7E /r, reg=xmm(src), rm=dst
		a.emit(0x66)
		a.emitRexRM(w, src.reg, dst)
		a.emit(0x0F, 0x7E)
		a.emitModRM(src.reg, dst)
		return nil
	case dstX && srcX: // xmm <- xmm : F3 0F 7E /r, reg=dst, rm=src
		a.emit(0xF3)
		if rex := rexFor(false, dst.reg, src.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x7E)
		a.emit(modrmReg(dst.reg, src.reg))
		return nil
	}
	return fmt.Errorf("unsupported movq/movd form")
}

// movsdss encodes the 0F 10/0F 11 move family — scalar movsd (F2) / movss
// (F3) and unaligned-packed movups (no prefix) / movupd (66) — between xmm
// registers or xmm<->memory (load form 0x10 when the destination is an xmm;
// store form 0x11 when the source is the xmm and the destination is memory).
func (a *Assembler) movsdss(prefix byte, ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("movsd/movss/movups/movupd expects two operands")
	}
	dst, src := ops[0], ops[1]
	dstX := dst.kind == opReg && dst.size == 128
	srcX := src.kind == opReg && src.size == 128
	if prefix != 0 {
		a.emit(prefix)
	}
	switch {
	case dstX && srcX:
		if rex := rexFor(false, dst.reg, src.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x10)
		a.emit(modrmReg(dst.reg, src.reg))
		return nil
	case dstX && src.kind == opMem: // load
		a.emitRexRM(false, dst.reg, src)
		a.emit(0x0F, 0x10)
		a.emitModRM(dst.reg, src)
		return nil
	case dst.kind == opMem && srcX: // store
		a.emitRexRM(false, src.reg, dst)
		a.emit(0x0F, 0x11)
		a.emitModRM(src.reg, dst)
		return nil
	}
	return fmt.Errorf("unsupported movsd/movss/movups/movupd form")
}

// sse3AImm8 encodes the 66 0F 3A <op> /r ib forms with an xmm destination:
// roundsd (0B), roundss (0A), pcmpistri (63), pcmpestri (61).
func (a *Assembler) sse3AImm8(op byte, ops []operand, name string) error {
	if len(ops) != 3 || ops[0].kind != opReg || ops[0].size != 128 || ops[2].kind != opImm {
		return fmt.Errorf("%s expects xmm, xmm/mem, imm8", name)
	}
	dst, src, imm := ops[0], ops[1], ops[2]
	a.emit(0x66)
	a.emitRexRM(false, dst.reg, src)
	a.emit(0x0F, 0x3A, op)
	a.emitModRM(dst.reg, src)
	a.emit(byte(imm.imm))
	return nil
}

// pextr encodes pextrb/w/d/q — extract one lane of an xmm into a GPR or
// memory. The direction is inverted from most 0F 3A forms: ModRM.rm is the
// DESTINATION and ModRM.reg the xmm source (66 0F 3A 14/15/16 /r ib, with
// REX.W selecting the q width on /16). For a register destination pextrw
// uses the short legacy form 66 0F C5 /r ib instead, whose operands run the
// usual way round (reg = GPR destination, rm = xmm) — that is what GNU as
// picks, and the 3A 15 form is reserved for memory destinations.
func (a *Assembler) pextr(mnem string, ops []operand) error {
	if len(ops) != 3 || ops[1].kind != opReg || ops[1].size != 128 || ops[2].kind != opImm {
		return fmt.Errorf("%s expects r/m, xmm, imm8", mnem)
	}
	dst, src, imm := ops[0], ops[1], ops[2]
	var op byte
	var w bool
	wantReg := 32
	switch mnem {
	case "pextrb":
		op = 0x14
	case "pextrw":
		op = 0x15
	case "pextrd":
		op = 0x16
	case "pextrq":
		op, w, wantReg = 0x16, true, 64
	}
	switch {
	case dst.kind == opReg && dst.size == wantReg:
		if mnem == "pextrw" {
			a.emit(0x66)
			if rex := rexFor(false, dst.reg, src.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x0F, 0xC5)
			a.emit(modrmReg(dst.reg, src.reg))
			a.emit(byte(imm.imm))
			return nil
		}
		a.emit(0x66)
		if rex := rexFor(w, src.reg, dst.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x3A, op)
		a.emit(modrmReg(src.reg, dst.reg))
		a.emit(byte(imm.imm))
		return nil
	case dst.kind == opMem:
		a.emit(0x66)
		if rex := memRex(w, src.reg, dst, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x3A, op)
		a.encodeMem(src.reg, dst)
		a.emit(byte(imm.imm))
		return nil
	}
	return fmt.Errorf("%s destination must be an r%d or memory", mnem, wantReg)
}

// pinsr encodes pinsrb/w/d/q — insert a GPR or memory value into one lane
// of an xmm. pinsrb/d/q are 66 0F 3A 20/22 /r ib (REX.W selects q);
// pinsrw is the legacy two-byte form 66 0F C4 /r ib. ModRM.reg is the xmm
// destination throughout.
func (a *Assembler) pinsr(mnem string, ops []operand) error {
	if len(ops) != 3 || ops[0].kind != opReg || ops[0].size != 128 || ops[2].kind != opImm {
		return fmt.Errorf("%s expects xmm, r/m, imm8", mnem)
	}
	dst, src, imm := ops[0], ops[1], ops[2]
	var opcode []byte
	var w bool
	wantReg := 32
	switch mnem {
	case "pinsrb":
		opcode = []byte{0x0F, 0x3A, 0x20}
	case "pinsrw":
		opcode = []byte{0x0F, 0xC4}
	case "pinsrd":
		opcode = []byte{0x0F, 0x3A, 0x22}
	case "pinsrq":
		opcode, w, wantReg = []byte{0x0F, 0x3A, 0x22}, true, 64
	}
	if src.kind == opReg && src.size != wantReg {
		return fmt.Errorf("%s source must be an r%d or memory", mnem, wantReg)
	}
	if src.kind != opReg && src.kind != opMem {
		return fmt.Errorf("%s source must be a register or memory", mnem)
	}
	a.emit(0x66)
	a.emitRexRM(w, dst.reg, src)
	a.emit(opcode...)
	a.emitModRM(dst.reg, src)
	a.emit(byte(imm.imm))
	return nil
}

// crc32 (F2 0F 38 F0/F1 /r): accumulate a CRC-32C over the source into a
// 32- or 64-bit GPR. The opcode keys on the SOURCE width — F0 for a byte,
// F1 otherwise, with 66 (before the mandatory F2) for a word source and
// REX.W for a 64-bit one.
func (a *Assembler) crc32(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || (ops[0].size != 32 && ops[0].size != 64) {
		return fmt.Errorf("crc32 expects an r32/r64 destination")
	}
	dst, src := ops[0], ops[1]
	srcSize := src.size
	if src.kind == opMem {
		srcSize = src.memSize
		if srcSize == 0 {
			return fmt.Errorf("crc32 on memory needs a byte/word/dword/qword ptr size")
		}
	} else if src.kind != opReg || src.size == 128 {
		return fmt.Errorf("crc32 source must be a GPR or memory")
	}
	// The 64-bit destination pairs only with byte and qword sources; the
	// 32-bit one with byte/word/dword.
	if srcSize == 64 && dst.size != 64 || dst.size == 64 && srcSize != 8 && srcSize != 64 {
		return fmt.Errorf("crc32 operand sizes do not match")
	}
	op := byte(0xF1)
	if srcSize == 8 {
		op = 0xF0
	}
	if srcSize == 16 {
		a.emit(0x66)
	}
	a.emit(0xF2)
	w := dst.size == 64
	if src.kind == opReg {
		if rex := rexFor(w, dst.reg, src.reg, needsRexByte(src)); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x38, op)
		a.emit(modrmReg(dst.reg, src.reg))
		return nil
	}
	if rex := memRex(w, dst.reg, src, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, 0x38, op)
	a.encodeMem(dst.reg, src)
	return nil
}

// emitRexRM emits the REX prefix for an instruction whose ModRM.reg is the
// given register and whose rm operand is `rm` (a register or memory).
func (a *Assembler) emitRexRM(w bool, reg int, rm operand) {
	var rex byte
	if rm.kind == opMem {
		rex = memRex(w, reg, rm, false)
	} else {
		rex = rexFor(w, reg, rm.reg, false)
	}
	if rex != 0 {
		a.emit(rex)
	}
}

// emitModRM emits ModRM (+SIB/disp) for a register or memory rm operand.
func (a *Assembler) emitModRM(reg int, rm operand) {
	if rm.kind == opMem {
		a.encodeMem(reg, rm)
	} else {
		a.emit(modrmReg(reg, rm.reg))
	}
}
