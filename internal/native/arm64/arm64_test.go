package arm64_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// Reference encodings from llvm-mc:
//
//	$ printf 'movz x8,#93\nmovz x0,#42\nmovz x0,#0\nsvc #0\nret\n' \
//	    | llvm-mc -triple=aarch64 --show-encoding
//
// llvm-mc prints little-endian byte tuples; the uint32 values below
// are those bytes read back little-endian (e.g. [0xa8,0x0b,0x80,0xd2]
// → 0xd2800ba8).
func TestEncodings(t *testing.T) {
	cases := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"movz x8, #93", arm64.MOVZ(8, 93, 0), 0xd2800ba8},
		{"movz x0, #42", arm64.MOVZ(0, 42, 0), 0xd2800540},
		{"movz x0, #0", arm64.MOVZ(0, 0, 0), 0xd2800000},
		{"movz x1, #1, lsl #16", arm64.MOVZ(1, 1, 16), 0xd2a00021},
		{"movk x0, #1, lsl #16", arm64.MOVK(0, 1, 16), 0xf2a00020},
		{"movk x3, #0xabcd", arm64.MOVK(3, 0xabcd, 0), 0xf29579a3},
		{"movn x0, #0", arm64.MOVN(0, 0, 0), 0x92800000},
		{"movn x5, #7, lsl #32", arm64.MOVN(5, 7, 32), 0x92c000e5},
		{"add x0, x1, #1", arm64.ADDimm(0, 1, 1, false), 0x91000420},
		{"add x0, x1, #4095", arm64.ADDimm(0, 1, 4095, false), 0x913ffc20},
		{"add x2, x3, #1, lsl #12", arm64.ADDimm(2, 3, 1, true), 0x91400462},
		{"sub x0, x1, #1", arm64.SUBimm(0, 1, 1, false), 0xd1000420},
		{"add x0, x1, x2", arm64.ADDreg(0, 1, 2), 0x8b020020},
		{"sub x4, x5, x6", arm64.SUBreg(4, 5, 6), 0xcb0600a4},
		{"mov x0, x1", arm64.MOVreg(0, 1), 0xaa0103e0},
		{"and x0, x1, x2", arm64.ANDreg(0, 1, 2), 0x8a020020},
		{"orr x0, x1, x2", arm64.ORRreg(0, 1, 2), 0xaa020020},
		{"eor x0, x1, x2", arm64.EORreg(0, 1, 2), 0xca020020},
		{"lsl x0, x1, x2", arm64.LSLV(0, 1, 2), 0x9ac22020},
		{"lsr x0, x1, x2", arm64.LSRV(0, 1, 2), 0x9ac22420},
		{"asr x0, x1, x2", arm64.ASRV(0, 1, 2), 0x9ac22820},
		{"cmp x1, x2", arm64.CMPreg(1, 2), 0xeb02003f},
		{"cmp x1, #5", arm64.CMPimm(1, 5, false), 0xf100143f},
		{"mul x0, x1, x2", arm64.MUL(0, 1, 2), 0x9b027c20},
		{"ldr x0, [x1]", arm64.LDRimm(0, 1, 0), 0xf9400020},
		{"ldr x0, [x1, #8]", arm64.LDRimm(0, 1, 8), 0xf9400420},
		{"ldr x0, [x1, #16]", arm64.LDRimm(0, 1, 16), 0xf9400820},
		{"str x0, [x1, #8]", arm64.STRimm(0, 1, 8), 0xf9000420},
		{"ldrb w0, [x1]", arm64.LDRBimm(0, 1, 0), 0x39400020},
		{"ldrb w0, [x1, #1]", arm64.LDRBimm(0, 1, 1), 0x39400420},
		{"strb w0, [x1, #1]", arm64.STRBimm(0, 1, 1), 0x39000420},
		{"ldrh w0, [x1, #2]", arm64.LDRHimm(0, 1, 2), 0x79400420},
		{"strh w0, [x1, #2]", arm64.STRHimm(0, 1, 2), 0x79000420},
		{"stp x29,x30,[sp,#-16]!", arm64.STPpre(29, 30, 31, -16), 0xa9bf7bfd},
		{"ldp x29,x30,[sp],#16", arm64.LDPpost(29, 30, 31, 16), 0xa8c17bfd},
		{"lsl x0, x1, #4", arm64.LSLimm(0, 1, 4), 0xd37cec20},
		{"lsr x0, x1, #4", arm64.LSRimm(0, 1, 4), 0xd344fc20},
		{"asr x0, x1, #4", arm64.ASRimm(0, 1, 4), 0x9344fc20},
		{"lsl x2, x3, #1", arm64.LSLimm(2, 3, 1), 0xd37ff862},
		{"sxtb x0, w1", arm64.SXTB(0, 1), 0x93401c20},
		{"sxth x0, w1", arm64.SXTH(0, 1), 0x93403c20},
		{"sxtw x0, w1", arm64.SXTW(0, 1), 0x93407c20},
		{"csel x0,x1,x2,eq", arm64.CSEL(0, 1, 2, arm64.CondEQ), 0x9a820020},
		{"csel x3,x4,x5,lt", arm64.CSEL(3, 4, 5, arm64.CondLT), 0x9a85b083},
		{"cset x0, ne", arm64.CSET(0, arm64.CondNE), 0x9a9f07e0},
		{"cset x7, ge", arm64.CSET(7, arm64.CondGE), 0x9a9fb7e7},
		{"cmn x1, x2", arm64.CMN(1, 2), 0xab02003f},
		{"neg x0, x1", arm64.NEG(0, 1), 0xcb0103e0},
		{"clz x0, x1", arm64.CLZ(0, 1), 0xdac01020},
		{"clz x3, x2", arm64.CLZ(3, 2), 0xdac01043},
		{"udiv x0, x1, x2", arm64.UDIV(0, 1, 2), 0x9ac20820},
		{"sdiv x0, x1, x2", arm64.SDIV(0, 1, 2), 0x9ac20c20},
		{"msub x0,x1,x2,x3", arm64.MSUB(0, 1, 2, 3), 0x9b028c20},
		{"mrs x9, cntvct_el0", arm64.MRS(9, 3, 3, 14, 0, 2), 0xd53be049},
		{"mrs x10, cntfrq_el0", arm64.MRS(10, 3, 3, 14, 0, 0), 0xd53be00a},
		{"mrs x0, cntpct_el0", arm64.MRS(0, 3, 3, 14, 0, 1), 0xd53be020}, // op2 selects the physical counter
		{"mrs x30, cntvct_el0", arm64.MRS(30, 3, 3, 14, 0, 2), 0xd53be05e},
		{"ldur x0, [x1, #-8]", arm64.LDUR(0, 1, -8), 0xf85f8020},
		{"stur x0, [x1, #-8]", arm64.STUR(0, 1, -8), 0xf81f8020},
		{"ldur x2, [x3, #15]", arm64.LDUR(2, 3, 15), 0xf840f062},
		{"stur x4, [x5]", arm64.STUR(4, 5, 0), 0xf80000a4},
		{"ldurb w0, [x1, #-1]", arm64.LDURB(0, 1, -1), 0x385ff020},
		{"sturb w0, [x1, #-1]", arm64.STURB(0, 1, -1), 0x381ff020},
		{"fadd d0,d1,d2", arm64.FADD(0, 1, 2), 0x1e622820},
		{"fsub d0,d1,d2", arm64.FSUB(0, 1, 2), 0x1e623820},
		{"fmul d0,d1,d2", arm64.FMUL(0, 1, 2), 0x1e620820},
		{"fdiv d0,d1,d2", arm64.FDIV(0, 1, 2), 0x1e621820},
		{"fneg d0,d1", arm64.FNEG(0, 1), 0x1e614020},
		{"fcmp d1,d2", arm64.FCMP(1, 2), 0x1e622020},
		{"fmov d0,d1", arm64.FMOV(0, 1), 0x1e604020},
		{"fmov d0,x1", arm64.FMOVfromGPR(0, 1), 0x9e670020},
		{"fmov x0,d1", arm64.FMOVtoGPR(0, 1), 0x9e660020},
		{"scvtf d0,x1", arm64.SCVTF(0, 1), 0x9e620020},
		{"fcvtzs x0,d1", arm64.FCVTZS(0, 1), 0x9e780020},
		{"ldrsb x0, [x1]", arm64.LDRSB(0, 1, 0, true), 0x39800020},
		{"ldrsb w0, [x1, #1]", arm64.LDRSB(0, 1, 1, false), 0x39c00420},
		{"ldrsh x0, [x1, #2]", arm64.LDRSH(0, 1, 2, true), 0x79800420},
		{"ldrsh w0, [x1, #2]", arm64.LDRSH(0, 1, 2, false), 0x79c00420},
		{"ldrsw x0, [x1, #4]", arm64.LDRSW(0, 1, 4), 0xb9800420},
		{"fadd s0,s1,s2", arm64.FADDS(0, 1, 2), 0x1e222820},
		{"fsub s0,s1,s2", arm64.FSUBS(0, 1, 2), 0x1e223820},
		{"fmul s0,s1,s2", arm64.FMULS(0, 1, 2), 0x1e220820},
		{"fdiv s0,s1,s2", arm64.FDIVS(0, 1, 2), 0x1e221820},
		{"fneg s0,s1", arm64.FNEGS(0, 1), 0x1e214020},
		{"fcmp s1,s2", arm64.FCMPS(1, 2), 0x1e222020},
		{"fmov s0,s1", arm64.FMOVS(0, 1), 0x1e204020},
		{"fcvt d0,s1", arm64.FCVTStoD(0, 1), 0x1e22c020},
		{"fcvt s0,d1", arm64.FCVTDtoS(0, 1), 0x1e624020},
		{"scvtf s0,x1", arm64.SCVTFS(0, 1), 0x9e220020},
		{"fcvtzs x0,s1", arm64.FCVTZSS(0, 1), 0x9e380020},
		{"ucvtf d0,x1", arm64.UCVTF(0, 1), 0x9e630020},
		{"fcvtzu x0,d1", arm64.FCVTZU(0, 1), 0x9e790020},
		{"adrp x0, +1page", arm64.ADRP(0, 1), 0xb0000000},
		{"adrp x0, +0", arm64.ADRP(0, 0), 0x90000000},
		{"adrp x5, +3pages", arm64.ADRP(5, 3), 0xf0000005},
		{"str x0,[sp,#-16]!", arm64.IdxLoadStore(0, 31, -16, 3, false, true), 0xf81f0fe0},
		{"ldr x0,[sp],#16", arm64.IdxLoadStore(0, 31, 16, 3, true, false), 0xf84107e0},
		{"str x1,[x2,#8]!", arm64.IdxLoadStore(1, 2, 8, 3, false, true), 0xf8008c41},
		{"ldr x3,[x4],#-8", arm64.IdxLoadStore(3, 4, -8, 3, true, false), 0xf85f8483},
		{"ubfx x1,x1,#56,#4", arm64.UBFX(1, 1, 56, 4), 0xd378ec21},
		// 32-bit (W-register) forms: sf=0, N=0 where the 64-bit forms set
		// both. The string runtime's small-string decode emits
		// `ubfx w0, w0, #1, #3` to pull the inline length out of a tagged
		// string word, so these are needed to assemble any program that
		// reaches a string runtime helper.
		{"ubfx w0,w0,#1,#3", arm64.UBFXW(0, 0, 1, 3), 0x53010c00},
		{"ubfx w20,w0,#1,#3", arm64.UBFXW(20, 0, 1, 3), 0x53010c14},
		{"sbfx w3,w4,#8,#16", arm64.SBFXW(3, 4, 8, 16), 0x13085c83},
		{"ubfx x0,x2,#0,#8", arm64.UBFX(0, 2, 0, 8), 0xd3401c40},
		{"sbfx x3,x4,#8,#16", arm64.SBFX(3, 4, 8, 16), 0x93485c83},
		{"ldr x3,[x2,x1,lsl#3]", arm64.LDRreg(3, 2, 1, true), 0xf8617843},
		{"ldr x0,[x1,x2]", arm64.LDRreg(0, 1, 2, false), 0xf8626820},
		{"str x3,[x2,x1,lsl#3]", arm64.STRreg(3, 2, 1, true), 0xf8217843},
		{"cmn x0,#0", arm64.CMNimm(0, 0, false), 0xb100001f},
		{"cmn x1,#5", arm64.CMNimm(1, 5, false), 0xb100143f},
		{"lsl w1,w1,#31", arm64.LSLimmW(1, 1, 31), 0x53010021},
		{"lsr w0,w1,#4", arm64.LSRimmW(0, 1, 4), 0x53047c20},
		{"asr w0,w1,#4", arm64.ASRimmW(0, 1, 4), 0x13047c20},
		{"ldrb w4,[x1],#1", arm64.IdxLoadStore(4, 1, 1, 0, true, false), 0x38401424},
		{"strb w4,[x1],#1", arm64.IdxLoadStore(4, 1, 1, 0, false, false), 0x38001424},
		{"ldrb w0,[x2,#1]!", arm64.IdxLoadStore(0, 2, 1, 0, true, true), 0x38401c40},
		{"stp x19,x20,[sp,#16]", arm64.PairLoadStore(19, 20, 31, 16, false, arm64.PairOffset), 0xa90153f3},
		{"ldp x19,x20,[sp,#16]", arm64.PairLoadStore(19, 20, 31, 16, true, arm64.PairOffset), 0xa94153f3},
		{"br x0", arm64.BR(0), 0xd61f0000},
		{"blr x1", arm64.BLR(1), 0xd63f0020},
		{"svc #0", arm64.SVC(0), 0xd4000001},
		{"brk #0", arm64.BRK(0), 0xd4200000},
		{"brk #1", arm64.BRK(1), 0xd4200020},
		{"ret (x30)", arm64.RET(30), 0xd65f03c0},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %#08x, want %#08x", c.name, c.got, c.want)
		}
	}
}

// TestPutLittleEndian confirms Put lays the word down low-byte-first.
func TestPutLittleEndian(t *testing.T) {
	got := arm64.Put(nil, 0xd2800ba8)
	want := []byte{0xa8, 0x0b, 0x80, 0xd2}
	if len(got) != 4 {
		t.Fatalf("got %d bytes, want 4", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %#02x, want %#02x (full % x)", i, got[i], want[i], got)
		}
	}
}
