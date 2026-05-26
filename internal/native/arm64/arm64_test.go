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
		{"udiv x0, x1, x2", arm64.UDIV(0, 1, 2), 0x9ac20820},
		{"msub x0,x1,x2,x3", arm64.MSUB(0, 1, 2, 3), 0x9b028c20},
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
		{"br x0", arm64.BR(0), 0xd61f0000},
		{"blr x1", arm64.BLR(1), 0xd63f0020},
		{"svc #0", arm64.SVC(0), 0xd4000001},
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
