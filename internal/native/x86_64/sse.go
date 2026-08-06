package x86_64

import "fmt"

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
var sseOps = map[string]struct{ prefix, op byte }{
	"addsd": {0xF2, 0x58}, "subsd": {0xF2, 0x5C}, "mulsd": {0xF2, 0x59}, "divsd": {0xF2, 0x5E}, "sqrtsd": {0xF2, 0x51},
	"addss": {0xF3, 0x58}, "subss": {0xF3, 0x5C}, "mulss": {0xF3, 0x59}, "divss": {0xF3, 0x5E}, "sqrtss": {0xF3, 0x51},
	"minsd": {0xF2, 0x5D}, "maxsd": {0xF2, 0x5F}, "minss": {0xF3, 0x5D}, "maxss": {0xF3, 0x5F},
	"ucomisd": {0x66, 0x2E}, "comisd": {0x66, 0x2F}, "ucomiss": {0x00, 0x2E}, "comiss": {0x00, 0x2F},
	"cvtss2sd": {0xF3, 0x5A}, "cvtsd2ss": {0xF2, 0x5A},
	"movapd": {0x66, 0x28}, "movaps": {0x00, 0x28},
	"xorpd": {0x66, 0x57}, "xorps": {0x00, 0x57}, "andpd": {0x66, 0x54}, "andps": {0x00, 0x54},

	// PACKED-BYTE ops, the vector-kernel surface (docs/ATLAS-PLATFORM-PLAN.md
	// §3). Everything above this line is SCALAR floating point — the f64
	// shuttling the code generator does — and that is why the assembler had no
	// vector instructions at all until now: nothing had ever asked for one.
	//
	// These four happen to fit the same [prefix] 0F <op> /r shape, so they are
	// table entries rather than encoders. `movdqu` is the unaligned 128-bit
	// load/store, `pcmpeqb` the byte-wise compare producing an all-ones mask
	// per equal lane, and the two `punpckl*` forms are the SSE2 way to
	// broadcast a byte across a register (interleave with itself twice, then
	// pshufd) — SSSE3's single-instruction `pshufb` is outside the declared
	// x86-64 baseline.
	"movdqu": {0xF3, 0x6F}, "movdqa": {0x66, 0x6F},
	"pcmpeqb": {0x66, 0x74}, "pcmpeqw": {0x66, 0x75}, "pcmpeqd": {0x66, 0x76},
	"punpcklbw": {0x66, 0x60}, "punpcklwd": {0x66, 0x61},
	"por": {0x66, 0xEB}, "pand": {0x66, 0xDB}, "pxor": {0x66, 0xEF},
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

// pmovmskb encodes `pmovmskb r32, xmm` (66 0F D7 /r): gather the top bit of
// each of the 16 bytes into the low 16 bits of a GPR.
//
// It is the bridge out of the vector domain — the instruction that turns a
// pcmpeqb mask into something `bsf` can scan — so its destination is a
// GENERAL-PURPOSE register while ModRM.reg still names it. That inversion is
// why it cannot be a sseOps entry: the table assumes reg is an xmm.
func (a *Assembler) pmovmskb(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size == 128 ||
		ops[1].kind != opReg || ops[1].size != 128 {
		return fmt.Errorf("pmovmskb expects r32/r64, xmm")
	}
	dst, src := ops[0], ops[1]
	a.emit(0x66)
	if rex := rexFor(false, dst.reg, src.reg, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, 0xD7)
	a.emit(modrmReg(dst.reg, src.reg))
	return nil
}

// pshufd encodes `pshufd xmm, xmm/mem, imm8` (66 0F 70 /r ib): permute the
// four 32-bit lanes by the immediate's four 2-bit selectors. `pshufd x, x, 0`
// broadcasts lane 0 to all four, which is the last step of the byte splat.
func (a *Assembler) pshufd(ops []operand) error {
	if len(ops) != 3 || ops[0].kind != opReg || ops[0].size != 128 || ops[2].kind != opImm {
		return fmt.Errorf("pshufd expects xmm, xmm/mem, imm8")
	}
	dst, src, imm := ops[0], ops[1], ops[2]
	a.emit(0x66)
	a.emitRexRM(false, dst.reg, src)
	a.emit(0x0F, 0x70)
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
	a.emit(prefix)
	a.emitRexRM(src.size == 64, dst.reg, src)
	a.emit(0x0F, 0x2A)
	a.emitModRM(dst.reg, src)
	return nil
}

// cvtt2si encodes cvttsd2si/cvttss2si (truncating): r32|64 <- xmm/mem.
// REX.W follows the integer destination width.
func (a *Assembler) cvtt2si(prefix, op byte, ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size == 128 {
		return fmt.Errorf("cvttsd2si/ss expects r, xmm/mem")
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

// movsdss encodes scalar movsd/movss between xmm registers or xmm<->memory
// (load form 0x10 when the destination is an xmm; store form 0x11 when the
// source is the xmm and the destination is memory).
func (a *Assembler) movsdss(prefix byte, ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("movsd/movss expects two operands")
	}
	dst, src := ops[0], ops[1]
	dstX := dst.kind == opReg && dst.size == 128
	srcX := src.kind == opReg && src.size == 128
	a.emit(prefix)
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
	return fmt.Errorf("unsupported movsd/movss form")
}

// roundsd encodes "roundsd xmm, xmm/mem, imm8": 66 0F 3A 0B /r ib.
func (a *Assembler) roundsd(ops []operand) error {
	if len(ops) != 3 || ops[0].kind != opReg || ops[0].size != 128 || ops[2].kind != opImm {
		return fmt.Errorf("roundsd expects xmm, xmm/mem, imm8")
	}
	dst, src, imm := ops[0], ops[1], ops[2]
	a.emit(0x66)
	a.emitRexRM(false, dst.reg, src)
	a.emit(0x0F, 0x3A, 0x0B)
	a.emitModRM(dst.reg, src)
	a.emit(byte(imm.imm))
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
