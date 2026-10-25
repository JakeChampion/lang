package e2ecompiler

import (
	"strings"
	"testing"
)

type pinnedAsm struct {
	asm  string
	want uint32
}

func checkPinnedSelfHost(t *testing.T, bin string, runner []string, cases []pinnedAsm) {
	t.Helper()
	var b strings.Builder
	b.WriteString(".text\n_start:\n")
	for _, c := range cases {
		b.WriteString("    " + c.asm + "\n")
	}
	got := assembleSelfHost(t, bin, runner, b.String())
	if len(got) != len(cases) {
		t.Fatalf("word count differs: self-host %d, %d cases", len(got), len(cases))
	}
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("%q: self-host %08x, GNU as %08x", c.asm, got[i], c.want)
		}
	}
}

func checkRefusedSelfHost(t *testing.T, bin string, runner []string, lines []string) {
	t.Helper()
	for _, ln := range lines {
		if got := refusalsFor(t, bin, runner, ".text\n_start:\n    "+ln+"\n    ret\n"); len(got) == 0 {
			t.Errorf("%q: expected a refusal, the assembler accepted it", ln)
		}
	}
}

func TestSelfHostArm64CarryMulGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"adc x0, x1, x2", 0x9a020020},
		{"adc x23, x9, x28", 0x9a1c0137},
		{"adc w3, w4, w5", 0x1a050083},
		{"sbc x23, x9, x28", 0xda1c0137},
		{"sbc w28, w9, w23", 0x5a17013c},
		{"adcs x23, x9, x28", 0xba1c0137},
		{"adcs w28, w9, w23", 0x3a17013c},
		{"sbcs x23, x9, x28", 0xfa1c0137},
		{"sbcs w28, w9, w23", 0x7a17013c},
		{"ngc x23, x9", 0xda0903f7},
		{"ngc w28, w9", 0x5a0903fc},
		{"ngcs x23, x9", 0xfa0903f7},
		{"ngcs w28, w9", 0x7a0903fc},
		{"umulh x0, x1, x2", 0x9bc27c20},
		{"umulh x23, x9, x28", 0x9bdc7d37},
		{"smulh x0, x1, x2", 0x9b427c20},
		{"smulh x23, x9, x28", 0x9b5c7d37},
		{"madd x0, x1, x2, x3", 0x9b020c20},
		{"madd x23, x9, x28, x11", 0x9b1c2d37},
		{"madd w23, w9, w28, w11", 0x1b1c2d37},
		{"mul w1, w2, w3", 0x1b037c41},
		{"smull x23, w9, w28", 0x9b3c7d37},
		{"umull x23, w9, w28", 0x9bbc7d37},
		{"smaddl x23, w9, w28, x11", 0x9b3c2d37},
		{"umaddl x23, w9, w28, x11", 0x9bbc2d37},
		{"smsubl x23, w9, w28, x11", 0x9b3cad37},
		{"umsubl x23, w9, w28, x11", 0x9bbcad37},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"adc x0, x1, #1",
		"sbcs w0, w1, #2",
		"adc x0, x1, x2, lsl #1",
		"ngc x0, x1, x2",
		"umulh w0, w1, w2",
		"smulh w0, w1, w2",
		"umulh x0, x1, w2",
		"smull x0, x1, x2",
		"umull w0, w1, w2",
		"smaddl x23, w9, w28, w11",
		"umaddl w23, w9, w28, x11",
	})
}

func TestSelfHostArm64LogicalNegatedGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"tst x23, x9", 0xea0902ff},
		{"tst w28, w9", 0x6a09039f},
		{"tst x1, x2, lsl #3", 0xea020c3f},
		{"tst w1, w2, lsr #7", 0x6a421c3f},
		{"tst x0, #0xff", 0xf2401c1f},
		{"tst w9, #0xf", 0x72000d3f},
		{"ands x23, x9, x28", 0xea1c0137},
		{"ands w28, w9, w23", 0x6a17013c},
		{"ands x1, x2, x3, lsl #4", 0xea031041},
		{"ands w1, w2, w3, asr #2", 0x6a830841},
		{"ands x0, x1, #0xff00", 0xf2781c20},
		{"ands w9, w10, #0x7", 0x72000949},
		{"bic x23, x9, x28", 0x8a3c0137},
		{"bic w28, w9, w23", 0x0a37013c},
		{"bic x1, x2, x3, lsl #8", 0x8a232041},
		{"bic w1, w2, w3, ror #3", 0x0ae30c41},
		{"bics x23, x9, x28", 0xea3c0137},
		{"bics w28, w9, w23", 0x6a37013c},
		{"bics x1, x2, x3, lsr #2", 0xea630841},
		{"orn x23, x9, x28", 0xaa3c0137},
		{"orn w28, w9, w23", 0x2a37013c},
		{"orn x1, x2, x3, asr #5", 0xaaa31441},
		{"eon x23, x9, x28", 0xca3c0137},
		{"eon w28, w9, w23", 0x4a37013c},
		{"eon x1, x2, x3, lsl #16", 0xca234041},
		{"mvn x23, x9", 0xaa2903f7},
		{"mvn w28, w9", 0x2a2903fc},
		{"mvn x1, x2, lsl #4", 0xaa2213e1},
		{"mvn w1, w2, asr #3", 0x2aa20fe1},
		{"negs x23, x9", 0xeb0903f7},
		{"negs w28, w9", 0x6b0903fc},
		{"orr x0, x1, x2, lsl #8", 0xaa022020},
		{"orr w3, w1, w1, lsl #8", 0x2a012023},
		{"and x1, x2, x3, lsr #4", 0x8a431041},
		{"eor w1, w2, w3, ror #7", 0x4ac31c41},
		{"cmn x0, x1", 0xab01001f},
		{"cmn w1, w2", 0x2b02003f},
		{"cmn x23, x9", 0xab0902ff},
		{"cmn w9, #255", 0x3103fd3f},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"bic x0, x1, #0xff",
		"bics x0, x1, #0xff",
		"orn x0, x1, #0xff",
		"eon w0, w1, #0xf",
		"tst x0, #0",
		"tst x0",
		"mvn x0",
		"negs x0, x1, x2",
		"orr x0, x1, x2, banana #1",
	})
}

func TestSelfHostArm64ExtrBitfieldGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"extr x0, x1, x2, #12", 0x93c23020},
		{"extr x23, x9, x28, #63", 0x93dcfd37},
		{"extr w28, w9, w23, #17", 0x1397453c},
		{"ror x23, x9, #63", 0x93c9fd37},
		{"ror w28, w9, #17", 0x1389453c},
		{"ror x23, x9, x28", 0x9adc2d37},
		{"ror w28, w9, w23", 0x1ad72d3c},
		{"bfi x0, x1, #4, #8", 0xb37c1c20},
		{"bfi x23, x9, #40, #16", 0xb3583d37},
		{"bfi w28, w9, #3, #5", 0x331d113c},
		{"bfxil x0, x1, #4, #8", 0xb3442c20},
		{"bfxil x23, x9, #40, #16", 0xb368dd37},
		{"bfxil w28, w9, #3, #5", 0x33031d3c},
		{"ubfiz x23, x9, #40, #16", 0xd3583d37},
		{"ubfiz w28, w9, #3, #5", 0x531d113c},
		{"sbfiz x23, x9, #40, #16", 0x93583d37},
		{"sbfiz w28, w9, #3, #5", 0x131d113c},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"extr x0, x1, x2, #64",
		"extr w0, w1, w2, #32",
		"extr x0, x1, x2, #-1",
		"ror x0, x1, #64",
		"ror w0, w1, #32",
		"ror x0, x1",
		"bfi x0, x1, #60, #8",
		"bfi w0, w1, #30, #5",
		"bfxil x0, x1, #64, #1",
		"ubfiz x0, x1, #0, #0",
		"sbfiz w0, w1, #-1, #4",
		"bfi x0, x1, #4",
	})
}

func TestSelfHostArm64ExtractExtendGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"nop", 0xd503201f},
		{"br x5", 0xd61f00a0},
		{"br x28", 0xd61f0380},
		{"sbfx x23, x9, #40, #16", 0x9368dd37},
		{"sbfx w28, w9, #3, #5", 0x13031d3c},
		{"sbfx x1, x2, #3, #8", 0x93432841},
		{"ubfx x23, x9, #40, #16", 0xd368dd37},
		{"ubfx w28, w9, #3, #5", 0x53031d3c},
		{"ubfx w1, w2, #3, #8", 0x53032841},
		{"sxtb x23, w9", 0x93401d37},
		{"sxtb w28, w9", 0x13001d3c},
		{"sxth x23, w9", 0x93403d37},
		{"sxth w28, w9", 0x13003d3c},
		{"uxtb w28, w9", 0x53001d3c},
		{"uxth w28, w9", 0x53003d3c},
		{"uxtb x28, w9", 0x53001d3c},
		{"ldrsb x23, [x9, #4095]", 0x39bffd37},
		{"ldrsb w28, [x9]", 0x39c0013c},
		{"ldrsh x23, [x9, #8190]", 0x79bffd37},
		{"ldrsh w28, [x9, #2]", 0x79c0053c},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"ubfx x0, x1, #60, #8",
		"ubfx w0, w1, #30, #5",
		"sbfx x0, x1, #64, #1",
		"sbfx w0, w1, #0, #0",
		"ubfx x0, x1, #4",
		"ldrsb x0, [x1, x2]",
		"ldrsh w0, [x1, w2, uxtw #1]",
	})
}

func TestSelfHostArm64VectorGeneralGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"cnt v0.8b, v0.8b", 0x0e205800},
		{"cnt v3.16b, v9.16b", 0x4e205923},
		{"cmeq v0.16b, v1.16b, v2.16b", 0x6e228c20},
		{"cmlt v0.16b, v1.16b, #0", 0x4e20a820},

		{"add v23.4s, v9.4s, v28.4s", 0x4ebc8537},
		{"add v0.2d, v1.2d, v2.2d", 0x4ee28420},
		{"sub v23.8h, v9.8h, v28.8h", 0x6e7c8537},
		{"mul v23.4s, v9.4s, v28.4s", 0x4ebc9d37},
		{"cmeq v23.2d, v9.2d, v28.2d", 0x6efc8d37},
		{"cmtst v23.8b, v9.8b, v28.8b", 0x0e3c8d37},
		{"cmgt v23.4h, v9.4h, v28.4h", 0x0e7c3537},
		{"cmge v23.16b, v9.16b, v28.16b", 0x4e3c3d37},
		{"cmhi v23.4s, v9.4s, v28.4s", 0x6ebc3537},
		{"cmhs v23.2s, v9.2s, v28.2s", 0x2ebc3d37},
		{"smax v23.8h, v9.8h, v28.8h", 0x4e7c6537},
		{"smin v23.4s, v9.4s, v28.4s", 0x4ebc6d37},
		{"umax v23.16b, v9.16b, v28.16b", 0x6e3c6537},
		{"umin v23.4h, v9.4h, v28.4h", 0x2e7c6d37},
		{"sshl v23.2d, v9.2d, v28.2d", 0x4efc4537},
		{"ushl v23.8b, v9.8b, v28.8b", 0x2e3c4537},

		{"and v23.8b, v9.8b, v28.8b", 0x0e3c1d37},
		{"bic v23.16b, v9.16b, v28.16b", 0x4e7c1d37},
		{"orr v23.8b, v9.8b, v28.8b", 0x0ebc1d37},
		{"orr v3.16b, v0.16b, v2.16b", 0x4ea21c03},
		{"orn v23.16b, v9.16b, v28.16b", 0x4efc1d37},
		{"eor v23.16b, v9.16b, v28.16b", 0x6e3c1d37},

		{"cmeq v23.4s, v9.4s, #0", 0x4ea09937},
		{"cmgt v23.8h, v9.8h, #0", 0x4e608937},
		{"cmge v23.16b, v9.16b, #0", 0x6e208937},
		{"cmle v23.2d, v9.2d, #0", 0x6ee09937},
		{"cmlt v23.4h, v9.4h, #0", 0x0e60a937},

		{"neg v23.4s, v9.4s", 0x6ea0b937},
		{"abs v23.2d, v9.2d", 0x4ee0b937},
		{"not v23.8b, v9.8b", 0x2e205937},
		{"mvn v23.16b, v9.16b", 0x6e205937},
		{"rev16 v23.16b, v9.16b", 0x4e201937},
		{"rev32 v23.8h, v9.8h", 0x6e600937},
		{"rev64 v23.4s, v9.4s", 0x4ea00937},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"mul v0.2d, v1.2d, v2.2d",
		"smax v0.2d, v1.2d, v2.2d",
		"and v0.4s, v1.4s, v2.4s",
		"orn v0.8h, v1.8h, v2.8h",
		"cnt v0.4s, v1.4s",
		"rev32 v0.4s, v1.4s",
		"add v0.1d, v1.1d, v2.1d",
		"neg v0.1d, v1.1d",
		"add v0.4s, v1.8h, v2.4s",
		"cmeq v0.16b, v1.8b, #0",
		"cmlt v0.4s, v1.4s, #1",
		"cmle v0.8b, v1.8b, #4",
		"add v0.4s, v1.4s",
		"neg v0.4s, v1.4s, v2.4s",
	})
}

func TestSelfHostArm64VectorFPShiftGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"fadd v23.4s, v9.4s, v28.4s", 0x4e3cd537},
		{"fadd v23.2d, v9.2d, v28.2d", 0x4e7cd537},
		{"fsub v23.2s, v9.2s, v28.2s", 0x0ebcd537},
		{"fmul v23.4s, v9.4s, v28.4s", 0x6e3cdd37},
		{"fdiv v23.2d, v9.2d, v28.2d", 0x6e7cfd37},
		{"fmax v23.4s, v9.4s, v28.4s", 0x4e3cf537},
		{"fmin v23.2d, v9.2d, v28.2d", 0x4efcf537},
		{"fcmeq v23.4s, v9.4s, v28.4s", 0x4e3ce537},
		{"fcmge v23.2d, v9.2d, v28.2d", 0x6e7ce537},
		{"fcmgt v23.4s, v9.4s, v28.4s", 0x6ebce537},

		{"fcmeq v23.4s, v9.4s, #0.0", 0x4ea0d937},
		{"fcmgt v23.2d, v9.2d, #0.0", 0x4ee0c937},
		{"fcmge v23.2s, v9.2s, #0.0", 0x2ea0c937},
		{"fcmle v23.4s, v9.4s, #0.0", 0x6ea0d937},
		{"fcmlt v23.2d, v9.2d, #0.0", 0x4ee0e937},

		{"fneg v23.4s, v9.4s", 0x6ea0f937},
		{"fabs v23.2d, v9.2d", 0x4ee0f937},
		{"fsqrt v23.2s, v9.2s", 0x2ea1f937},
		{"scvtf v23.4s, v9.4s", 0x4e21d937},
		{"ucvtf v23.2d, v9.2d", 0x6e61d937},
		{"fcvtzs v23.4s, v9.4s", 0x4ea1b937},
		{"fcvtzu v23.2d, v9.2d", 0x6ee1b937},

		{"shl v23.4s, v9.4s, #7", 0x4f275537},
		{"shl v23.16b, v9.16b, #3", 0x4f0b5537},
		{"sli v23.2d, v9.2d, #40", 0x6f685537},
		{"sshr v23.8h, v9.8h, #5", 0x4f1b0537},
		{"ushr v23.4s, v9.4s, #17", 0x6f2f0537},
		{"sri v23.16b, v9.16b, #6", 0x6f0a4537},

		{"zip1 v23.4s, v9.4s, v28.4s", 0x4e9c3937},
		{"zip2 v23.8h, v9.8h, v28.8h", 0x4e5c7937},
		{"uzp1 v23.16b, v9.16b, v28.16b", 0x4e1c1937},
		{"uzp2 v23.2d, v9.2d, v28.2d", 0x4edc5937},
		{"trn1 v23.4h, v9.4h, v28.4h", 0x0e5c2937},
		{"trn2 v23.2s, v9.2s, v28.2s", 0x0e9c6937},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"fadd v0.16b, v1.16b, v2.16b",
		"fmul v0.8h, v1.8h, v2.8h",
		"fneg v0.1d, v1.1d",
		"scvtf v0.4h, v1.4h",
		"fcmeq v0.4s, v1.4s, #1.0",
		"fcmlt v0.2d, v1.2d, v2.2d",
		"shl v0.4s, v1.4s, #32",
		"shl v0.16b, v1.16b, #8",
		"sshr v0.8h, v1.8h, #17",
		"sshr v0.8h, v1.8h, #0",
		"ushr v0.2d, v1.2d, #65",
		"sri v0.16b, v1.16b, #0",
		"zip1 v0.4s, v1.2d, v2.4s",
		"trn2 v0.1d, v1.1d, v2.1d",
		"fadd v0.4s, v1.4s",
		"fneg v0.4s, v1.4s, v2.4s",
	})
}

func TestSelfHostArm64VectorWidenAcrossGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"xtn v23.8b, v9.8h", 0x0e212937},
		{"xtn2 v23.16b, v9.8h", 0x4e212937},
		{"xtn v23.4h, v9.4s", 0x0e612937},
		{"xtn2 v23.8h, v9.4s", 0x4e612937},
		{"xtn v23.2s, v9.2d", 0x0ea12937},

		{"shrn v23.8b, v9.8h, #3", 0x0f0d8537},
		{"shrn2 v23.16b, v9.8h, #3", 0x4f0d8537},
		{"shrn v23.4h, v9.4s, #9", 0x0f178537},

		{"sshll v23.8h, v9.8b, #3", 0x0f0ba537},
		{"sshll2 v23.8h, v9.16b, #3", 0x4f0ba537},
		{"ushll v23.4s, v9.4h, #7", 0x2f17a537},
		{"ushll2 v23.2d, v9.4s, #17", 0x6f31a537},
		{"sxtl v23.8h, v9.8b", 0x0f08a537},
		{"sxtl2 v23.4s, v9.8h", 0x4f10a537},
		{"uxtl v23.2d, v9.2s", 0x2f20a537},
		{"uxtl2 v23.8h, v9.16b", 0x6f08a537},

		{"ext v23.16b, v9.16b, v28.16b, #5", 0x6e1c2937},
		{"ext v23.8b, v9.8b, v28.8b, #3", 0x2e1c1937},
		{"tbl v23.16b, {v9.16b}, v28.16b", 0x4e1c0137},
		{"tbx v23.16b, {v9.16b}, v28.16b", 0x4e1c1137},
		{"tbl v23.16b, {v9.16b-v12.16b}, v28.16b", 0x4e1c6137},
		{"tbx v23.8b, {v30.16b-v31.16b}, v28.8b", 0x0e1c33d7},
		{"tbl v0.16b, {v31.16b-v1.16b}, v2.16b", 0x4e0243e0},

		{"smaxv b23, v9.16b", 0x4e30a937},
		{"sminv h23, v9.8h", 0x4e71a937},
		{"umaxv s23, v9.4s", 0x6eb0a937},
		{"uminv b23, v9.8b", 0x2e31a937},
		{"saddlv h23, v9.16b", 0x4e303937},
		{"uaddlv s23, v9.8h", 0x6e703937},

		{"uaddlp v1.8h, v0.16b", 0x6e202801},
		{"uadalp v2.4s, v1.8h", 0x6e606822},
		{"uaddlp v9.4s, v23.8h", 0x6e602ae9},
		{"uadalp v23.2d, v9.4s", 0x6ea06937},
		{"saddlp v3.4h, v4.8b", 0x0e202883},
		{"sadalp v5.1d, v6.2s", 0x0ea068c5},
		{"saddlp v7.2s, v8.4h", 0x0e602907},

		{"pmull v0.8h, v1.8b, v2.8b", 0x0e22e020},
		{"pmull2 v0.8h, v1.16b, v2.16b", 0x4e22e020},
		{"pmull v0.1q, v1.1d, v2.1d", 0x0ee2e020},
		{"pmull2 v0.1q, v1.2d, v2.2d", 0x4ee2e020},
		{"pmull v31.8h, v30.8b, v29.8b", 0x0e3de3df},
		{"pmull2 v31.1q, v30.2d, v29.2d", 0x4efde3df},

		{"aese v0.16b, v1.16b", 0x4e284820},
		{"aesmc v0.16b, v1.16b", 0x4e286820},
		{"aese v31.16b, v30.16b", 0x4e284bdf},
		{"aesmc v31.16b, v29.16b", 0x4e286bbf},
		{"aese v17.16b, v5.16b", 0x4e2848b1},

		{"sha256h q0, q1, v2.4s", 0x5e024020},
		{"sha256h2 q0, q1, v2.4s", 0x5e025020},
		{"sha256su0 v0.4s, v1.4s", 0x5e282820},
		{"sha256su1 v0.4s, v1.4s, v2.4s", 0x5e026020},
		{"sha256h q31, q30, v29.4s", 0x5e1d43df},
		{"sha256h2 q1, q3, v2.4s", 0x5e025061},
		{"sha256su0 v17.4s, v5.4s", 0x5e2828b1},
		{"sha256su1 v7.4s, v30.4s, v18.4s", 0x5e1263c7},

		{"sha1c q0, s1, v2.4s", 0x5e020020},
		{"sha1p q0, s1, v2.4s", 0x5e021020},
		{"sha1m q0, s1, v2.4s", 0x5e022020},
		{"sha1su0 v0.4s, v1.4s, v2.4s", 0x5e023020},
		{"sha1h s0, s1", 0x5e280820},
		{"sha1su1 v0.4s, v1.4s", 0x5e281820},
		{"sha1c q31, s30, v29.4s", 0x5e1d03df},
		{"sha1m q3, s17, v5.4s", 0x5e052223},
		{"sha1h s20, s9", 0x5e280934},
		{"sha1su1 v7.4s, v30.4s", 0x5e281bc7},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"xtn v0.8b, v1.4s",
		"shrn v0.4h, v1.8h, #2",
		"sshll v0.4s, v1.8b, #1",
		"xtn v0.16b, v1.8h",
		"xtn2 v0.8b, v1.8h",
		"sshll2 v0.8h, v1.8b, #1",
		"uxtl v0.8h, v1.16b",
		"xtn v0.2s, v1.1d",
		"shrn v0.8b, v1.8h, #9",
		"shrn v0.8b, v1.8h, #0",
		"sshll v0.8h, v1.8b, #8",
		"ext v0.4s, v1.4s, v2.4s, #1",
		"ext v0.8b, v1.8b, v2.8b, #8",
		"ext v0.16b, v1.16b, v2.16b, #16",
		"tbl v0.16b, v1.16b, v2.16b",
		"tbl v0.16b, {v1.8b}, v2.16b",
		"tbl v0.8b, {v1.16b}, v2.16b",
		"tbx v0.16b, {v1.16b-v5.16b}, v2.16b",
		"tbx v0.16b, {v1.16b-v1.16b}, v2.16b",
		"tbl v0.16b, {v1.16b-v2.8b}, v2.16b",
		"smaxv h23, v9.16b",
		"saddlv b23, v9.16b",
		"umaxv s23, v9.2s",
		"addv d23, v9.2d",
		"sminv v0.8h, v9.8h",
		"uaddlp v1.4s, v0.16b",
		"uadalp v2.2s, v1.8h",
		"saddlp v0.2d, v1.2d",
		"pmull v0.8h, v1.16b, v2.16b",
		"pmull2 v0.8h, v1.8b, v2.8b",
		"pmull v0.1q, v1.2d, v2.2d",
		"pmull2 v0.1q, v1.1d, v2.1d",
		"pmull v0.4s, v1.4h, v2.4h",
		"pmull v0.16b, v1.8b, v2.8b",
		"pmull2 v0.1q, v1.2d, v2.1d",
		"add v0.1q, v1.1q, v2.1q",
		"ld1 {v0.1q}, [x0]",
		"aese v0.4s, v1.4s",
		"aesmc v0.16b, v1.8b",
		"aese v0.16b, v1.16b, v2.16b",
		"aesmc v0.16b",
		"sha256h v0.4s, v1.4s, v2.4s",
		"sha256h2 q0, q1, v2.16b",
		"sha256h q0, q1",
		"sha256su0 v0.16b, v1.16b",
		"sha256su0 v0.4s, v1.4s, v2.4s",
		"sha256su1 v0.4s, v1.4s",
		"sha256su1 q0, v1.4s, v2.4s",
		"sha1c q0, q1, v2.4s",
		"sha1p v0.4s, s1, v2.4s",
		"sha1m q0, s1, v2.16b",
		"sha1c q0, s1",
		"sha1h q0, s1",
		"sha1h s0, s1, s2",
		"sha1su0 v0.4s, v1.4s",
		"sha1su1 v0.4s, v1.4s, v2.4s",
		"sha1su1 s0, v1.4s",
	})
}

func TestSelfHostArm64VectorLaneGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"umov w23, v9.b[5]", 0x0e0b3d37},
		{"umov w23, v9.h[3]", 0x0e0e3d37},
		{"umov w23, v9.s[1]", 0x0e0c3d37},
		{"umov x23, v9.d[1]", 0x4e183d37},
		{"smov w23, v9.b[5]", 0x0e0b2d37},
		{"smov w23, v9.h[3]", 0x0e0e2d37},
		{"smov x23, v9.b[2]", 0x4e052d37},
		{"smov x23, v9.s[1]", 0x4e0c2d37},

		{"ins v23.b[5], w9", 0x4e0b1d37},
		{"ins v23.h[3], w9", 0x4e0e1d37},
		{"ins v23.s[1], w9", 0x4e0c1d37},
		{"ins v23.d[1], x9", 0x4e181d37},
		{"ins v23.b[5], v9.b[2]", 0x6e0b1537},
		{"ins v23.d[1], v9.d[0]", 0x6e180537},

		{"dup v23.16b, w9", 0x4e010d37},
		{"dup v23.4s, w9", 0x4e040d37},
		{"dup v23.2d, x9", 0x4e080d37},
		{"dup v23.8h, v9.h[3]", 0x4e0e0537},

		{"movi v23.16b, #7", 0x4f00e4f7},
		{"movi v23.4s, #7", 0x4f0004f7},
		{"movi v23.4s, #7, lsl #8", 0x4f0024f7},
		{"movi v23.8h, #7, lsl #8", 0x4f00a4f7},
		{"movi v23.2d, #0", 0x6f00e417},

		{"ld1r {v23.16b}, [x9]", 0x4d40c137},
		{"ld1r {v23.4s}, [x9]", 0x4d40c937},
		{"ld1 {v23.16b}, [x9]", 0x4c407137},
		{"ld1 {v23.4s}, [x9]", 0x4c407937},
		{"st1 {v23.16b}, [x9]", 0x4c007137},
		{"st1 {v23.2d}, [x9]", 0x4c007d37},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"umov x23, v9.b[5]",
		"umov w23, v9.d[1]",
		"smov x23, v9.d[0]",
		"smov w23, v9.s[1]",
		"ins v23.b[5], x9",
		"ins v23.d[1], w9",
		"ins v23.b[5], v9.h[2]",
		"umov w23, v9.b[16]",
		"ins v23.s[4], w9",
		"dup v23.8h, v9.h[8]",
		"dup v23.16b, x9",
		"dup v23.2d, w9",
		"dup v23.4s, v9.h[1]",
		"movi v23.16b, #256",
		"movi v23.16b, #7, lsl #8",
		"movi v23.8h, #7, lsl #16",
		"movi v23.4s, #7, lsl #32",
		"movi v23.2d, #7, lsl #8",
		"ld1 {v23.16b}, [x9, #16]",
		"ld1 {v23.16b}, [x9, x10]",
		"st1 v23.16b, [x9]",
		"ld1r {v23.16b}, [x9, #1]",
	})
}

func TestSelfHostArm64CondCmpSelGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"ccmp x0, x1, #0, eq", 0xfa410000},
		{"ccmp x23, x9, #15, lt", 0xfa49b2ef},
		{"ccmp w28, w9, #8, hi", 0x7a498388},
		{"ccmp x23, #9, #15, lt", 0xfa49baef},
		{"ccmp w28, #31, #8, hi", 0x7a5f8b88},
		{"ccmn x23, x9, #15, lt", 0xba49b2ef},
		{"ccmn w28, w9, #8, hi", 0x3a498388},
		{"ccmn x23, #9, #15, lt", 0xba49baef},
		{"ccmn w28, #31, #8, hi", 0x3a5f8b88},
		{"csinc x23, x9, x28, lt", 0x9a9cb537},
		{"csinc w28, w9, w23, hi", 0x1a97853c},
		{"csinv x23, x9, x28, lt", 0xda9cb137},
		{"csinv w28, w9, w23, hi", 0x5a97813c},
		{"csneg x23, x9, x28, lt", 0xda9cb537},
		{"csneg w28, w9, w23, hi", 0x5a97853c},
		{"cinc x23, x9, lt", 0x9a89a537},
		{"cinc w28, w9, hi", 0x1a89953c},
		{"cinv x23, x9, lt", 0xda89a137},
		{"cinv w28, w9, hi", 0x5a89913c},
		{"cneg x23, x9, lt", 0xda89a537},
		{"cneg w28, w9, hi", 0x5a89953c},
		{"csetm x23, lt", 0xda9fa3f7},
		{"csetm w28, hi", 0x5a9f93fc},
		{"csel w1, w2, w3, lt", 0x1a83b041},
		{"csel x1, x2, x3, lt", 0x9a83b041},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"ccmp x0, x1, #16, eq",
		"ccmp x0, #32, #0, eq",
		"ccmp x0, #-1, #0, eq",
		"ccmn x0, x1, #0, xx",
		"csinc x0, x1, x2",
		"cinc x0, x1",
		"csetm x0",
		"cset x0, al",
		"cset x0, nv",
		"csetm x0, al",
		"csetm x0, nv",
		"cinc x0, x1, al",
		"cinv x0, x1, nv",
		"cneg x0, x1, al",
	})
}

func TestSelfHostArm64RevClsGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"rev x0, x1", 0xdac00c20},
		{"rev x23, x9", 0xdac00d37},
		{"rev w0, w1", 0x5ac00820},
		{"rev w28, w9", 0x5ac0093c},
		{"rev32 x0, x1", 0xdac00820},
		{"rev32 x23, x9", 0xdac00937},
		{"cls x0, x1", 0xdac01420},
		{"cls x23, x9", 0xdac01537},
		{"cls w28, w9", 0x5ac0153c},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"rev32 w0, w1",
		"rev x0",
		"cls x0, x1, x2",
	})
}

func TestSelfHostArm64ExtRegAddressingGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"ldr x0, [x1, w2, uxtw]", 0xf8624820},
		{"ldr x23, [x9, w28, uxtw #3]", 0xf87c5937},
		{"ldr x23, [x9, w28, sxtw #3]", 0xf87cd937},
		{"ldr x23, [x9, x28, sxtx]", 0xf87ce937},
		{"ldr x23, [x9, x28, sxtx #3]", 0xf87cf937},
		{"ldr x23, [x9, x28, lsl #3]", 0xf87c7937},
		{"ldr w23, [x9, w28, uxtw #2]", 0xb87c5937},
		{"ldr w23, [x9, w28, sxtw]", 0xb87cc937},
		{"ldr w0, [x1, x2]", 0xb8626820},
		{"str w5, [x6, x7, lsl #2]", 0xb82778c5},
		{"str x23, [x9, w28, uxtw #3]", 0xf83c5937},
		{"str x23, [x9, x28, sxtx]", 0xf83ce937},
		{"str w23, [x9, w28, sxtw #2]", 0xb83cd937},
		{"ldrb w23, [x9, w28, uxtw]", 0x387c4937},
		{"ldrb w23, [x9, w28, sxtw]", 0x387cc937},
		{"ldrb w23, [x9, x28, sxtx]", 0x387ce937},
		{"strb w23, [x9, w28, uxtw]", 0x383c4937},
		{"ldrb w0, [x1, w2, uxtw #0]", 0x38625820},
		{"ldrh w23, [x9, w28, uxtw #1]", 0x787c5937},
		{"ldrh w23, [x9, w28, sxtw]", 0x787cc937},
		{"strh w23, [x9, x28, sxtx #1]", 0x783cf937},
		{"strh w23, [x9, x28, lsl #1]", 0x783c7937},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"ldr x0, [x1, w2, uxtw #2]",
		"ldr x0, [x1, x2, uxtw]",
		"ldr x0, [x1, w2, sxtx]",
		"ldr x0, [x1, w2]",
		"ldr x0, [x1, w2, lsl #3]",
		"ldr x0, [x1, x2, lsl]",
		"ldrb w0, [x1, w2, uxtw #1]",
		"ldrh w0, [x1, w2, uxtw #2]",
		"ldr x0, [x1, x2, ror #3]",
	})
}

func TestSelfHostArm64PairWDGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"ldp w0, w1, [x2]", 0x29400440},
		{"ldp w0, w1, [x2, #4]", 0x29408440},
		{"ldp w23, w9, [x28, #8]", 0x29412797},
		{"stp w23, w9, [x28, #-8]", 0x293f2797},
		{"ldp w23, w9, [x28], #16", 0x28c22797},
		{"stp w23, w9, [x28, #-16]!", 0x29be2797},
		{"ldp d0, d1, [x2]", 0x6d400440},
		{"ldp d23, d9, [x28, #16]", 0x6d412797},
		{"stp d23, d9, [x28, #-16]", 0x6d3f2797},
		{"ldp d23, d9, [x28], #32", 0x6cc22797},
		{"stp d23, d9, [x28, #-32]!", 0x6dbe2797},
		{"stp d8, d9, [sp, #-16]!", 0x6dbf27e8},
		{"stp x29, x30, [sp, #-16]!", 0xa9bf7bfd},
		{"ldp x0, x1, [sp], #16", 0xa8c107e0},
		{"ldp x0, x1, [sp, #16]", 0xa94107e0},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"ldp w0, w1, [x2, #2]",
		"ldp w0, w1, [x2, #256]",
		"stp w1, w2, [x3, #-260]",
		"ldp d0, d1, [x2, #4]",
		"ldp w0, x1, [x2]",
		"ldp s0, s1, [x2]",
	})
}

func TestSelfHostArm64FP32UnscaledGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"ldr s0, [x1]", 0xbd400020},
		{"ldr s23, [x9, #4]", 0xbd400537},
		{"str s23, [x9, #8]", 0xbd000937},
		{"ldr s23, [x9], #4", 0xbc404537},
		{"str s23, [x9], #-4", 0xbc1fc537},
		{"ldr s23, [x9, #4]!", 0xbc404d37},
		{"str s23, [x9, #-4]!", 0xbc1fcd37},
		{"ldr s23, [x9, #-4]", 0xbc5fc137},
		{"str s23, [x9, #-8]", 0xbc1f8137},
		{"ldur s23, [x9, #-4]", 0xbc5fc137},
		{"stur s23, [x9, #-4]", 0xbc1fc137},
		{"ldur s0, [x1, #255]", 0xbc4ff020},
		{"stur s0, [x1, #-256]", 0xbc100020},
		{"ldr d1, [x2, #8]!", 0xfc408c41},
		{"str d2, [x3], #-8", 0xfc1f8462},
		{"ldurh w23, [x9, #-2]", 0x785fe137},
		{"sturh w23, [x9, #-2]", 0x781fe137},
		{"ldurh w0, [x1, #255]", 0x784ff020},
		{"sturh w0, [x1, #-256]", 0x78100020},
		{"ldurb w0, [x1, #1]", 0x38401020},
		{"sturb w2, [x3, #-1]", 0x381ff062},
		{"ldursb x23, [x9, #-1]", 0x389ff137},
		{"ldursb w23, [x9, #-1]", 0x38dff137},
		{"ldursb x0, [x1]", 0x38800020},
		{"ldursh x23, [x9, #-2]", 0x789fe137},
		{"ldursh w23, [x9, #-2]", 0x78dfe137},
		{"ldursw x23, [x9, #-4]", 0xb89fc137},
		{"ldursw x0, [x1]", 0xb8800020},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"ldursw w0, [x1]",
		"ldurh w0, [x1, #256]",
		"ldursb x0, [x1, #-257]",
		"ldur s0, [x1, x2]",
		"ldursh x0, [x1, #2]!",
	})
}

func TestSelfHostArm64ExclusivesBarriersGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"ldxr x0, [x1]", 0xc85f7c20},
		{"ldxr x23, [x9]", 0xc85f7d37},
		{"ldxr w23, [x9]", 0x885f7d37},
		{"ldaxr x23, [x9]", 0xc85ffd37},
		{"ldaxr w23, [x9]", 0x885ffd37},
		{"stxr w2, x0, [x1]", 0xc8027c20},
		{"stxr w11, x23, [x9]", 0xc80b7d37},
		{"stxr w11, w23, [x9]", 0x880b7d37},
		{"stlxr w11, x23, [x9]", 0xc80bfd37},
		{"stlxr w11, w23, [x9]", 0x880bfd37},
		{"ldaddal x13, x13, [x15]", 0xf8ed01ed},
		{"ldaddal x0, x1, [x2]", 0xf8e00041},
		{"ldaddal w1, w2, [x3]", 0xb8e10062},
		{"casal x13, x14, [x15]", 0xc8edfdee},
		{"casal xzr, x9, [x10]", 0xc8fffd49},
		{"casal w1, w2, [x3]", 0x88e1fc62},
		{"ldar x0, [x1]", 0xc8dffc20},
		{"ldar x23, [x9]", 0xc8dffd37},
		{"ldar w23, [x9]", 0x88dffd37},
		{"stlr x23, [x9]", 0xc89ffd37},
		{"stlr w0, [x1]", 0x889ffc20},
		{"ldxrb w23, [x9]", 0x085f7d37},
		{"ldaxrb w23, [x9]", 0x085ffd37},
		{"stxrb w11, w23, [x9]", 0x080b7d37},
		{"stlxrb w11, w23, [x9]", 0x080bfd37},
		{"ldarb w23, [x9]", 0x08dffd37},
		{"stlrb w23, [x9]", 0x089ffd37},
		{"ldxrh w23, [x9]", 0x485f7d37},
		{"ldaxrh w23, [x9]", 0x485ffd37},
		{"stxrh w11, w23, [x9]", 0x480b7d37},
		{"stlxrh w11, w23, [x9]", 0x480bfd37},
		{"ldarh w23, [x9]", 0x48dffd37},
		{"stlrh w23, [x9]", 0x489ffd37},
		{"dmb sy", 0xd5033fbf},
		{"dmb ish", 0xd5033bbf},
		{"dmb ishld", 0xd50339bf},
		{"dmb ishst", 0xd5033abf},
		{"dmb ld", 0xd5033dbf},
		{"dmb st", 0xd5033ebf},
		{"dsb sy", 0xd5033f9f},
		{"dsb ish", 0xd5033b9f},
		{"dsb ishld", 0xd503399f},
		{"dsb ishst", 0xd5033a9f},
		{"dsb ld", 0xd5033d9f},
		{"dsb st", 0xd5033e9f},
		{"isb", 0xd5033fdf},
		{"isb sy", 0xd5033fdf},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"stxr x11, x23, [x9]",
		"ldxr x0, [x1, #8]",
		"ldaxr x0, [x1], #8",
		"ldxrb x0, [x1]",
		"ldaxrh x0, [x1]",
		"stlr w0, [x1, #4]",
		"dmb",
		"dsb",
		"dmb foo",
		"isb ish",
		"ldxr x0, [x1, x2]",
	})
}

func TestSelfHostArm64FPScalarGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"fmadd d0, d1, d2, d3", 0x1f420c20},
		{"fmadd d23, d9, d28, d11", 0x1f5c2d37},
		{"fmadd s23, s9, s28, s11", 0x1f1c2d37},
		{"fmsub d23, d9, d28, d11", 0x1f5cad37},
		{"fmsub s23, s9, s28, s11", 0x1f1cad37},
		{"fnmadd d23, d9, d28, d11", 0x1f7c2d37},
		{"fnmadd s23, s9, s28, s11", 0x1f3c2d37},
		{"fnmsub d23, d9, d28, d11", 0x1f7cad37},
		{"fnmsub s23, s9, s28, s11", 0x1f3cad37},
		{"fnmul d23, d9, d28", 0x1e7c8937},
		{"fnmul s23, s9, s28", 0x1e3c8937},
		{"fmin d23, d9, d28", 0x1e7c5937},
		{"fmin s23, s9, s28", 0x1e3c5937},
		{"fmax d23, d9, d28", 0x1e7c4937},
		{"fmax s23, s9, s28", 0x1e3c4937},
		{"fminnm d23, d9, d28", 0x1e7c7937},
		{"fminnm s23, s9, s28", 0x1e3c7937},
		{"fmaxnm d23, d9, d28", 0x1e7c6937},
		{"fmaxnm s23, s9, s28", 0x1e3c6937},
		{"fadd s1, s2, s3", 0x1e232841},
		{"fcsel d23, d9, d28, lt", 0x1e7cbd37},
		{"fcsel s23, s9, s28, hi", 0x1e3c8d37},
		{"fccmp d23, d9, #15, lt", 0x1e69b6ef},
		{"fccmp s23, s9, #8, hi", 0x1e2986e8},
		{"fcmpe d23, d9", 0x1e6922f0},
		{"fcmpe s23, s9", 0x1e2922f0},
		{"fcmpe d23, #0.0", 0x1e6022f8},
		{"fcmpe s23, #0.0", 0x1e2022f8},
		{"fcmp d0, #0.0", 0x1e602008},
		{"fcmp s1, #0.0", 0x1e202028},
		{"fabs s23, s9", 0x1e20c137},
		{"fsqrt s23, s9", 0x1e21c137},
		{"frintm s23, s9", 0x1e254137},
		{"frintp s23, s9", 0x1e24c137},
		{"frintz s23, s9", 0x1e25c137},
		{"frinta s23, s9", 0x1e264137},
		{"frintn s23, s9", 0x1e244137},
		{"fneg s2, s3", 0x1e214062},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"fmadd d0, s1, d2, d3",
		"fmin d0, d1, s2",
		"fcsel d0, d1, s2, eq",
		"fccmp d0, d1, #16, eq",
		"fcmpe d0, #1.0",
		"fcmp d0, s1",
		"fnmul d0, d1",
	})
}

func TestSelfHostArm64SysregFpGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"mov x0, fp", 0xaa1d03e0},
		{"add fp, sp, #32", 0x910083fd},
		{"str x0, [fp, #-8]", 0xf81f83a0},
		{"mrs x23, tpidr_el0", 0xd53bd057},
		{"msr tpidr_el0, x23", 0xd51bd057},
		{"mrs x23, nzcv", 0xd53b4217},
		{"msr nzcv, x23", 0xd51b4217},
		{"mrs x23, fpcr", 0xd53b4417},
		{"msr fpcr, x23", 0xd51b4417},
		{"mrs x23, fpsr", 0xd53b4437},
		{"msr fpsr, x23", 0xd51b4437},
		{"mrs x23, dczid_el0", 0xd53b00f7},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"msr dczid_el0, x0",
		"msr cntvct_el0, x0",
		"msr cntfrq_el0, x0",
		"msr nzcv, w0",
		"msr bogus_reg, x0",
		"msr nzcv",
		"mrs w0, nzcv",
	})
}

func TestSelfHostArm64AdrGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	const snippet = `.text
_start:
    adr x0, lbl
    adr x23, lbl
lbl:
    adr x9, lbl
`
	got := assembleSelfHost(t, bin, runner, snippet)
	want := []uint32{0x10000040, 0x10000037, 0x10000009}
	if len(got) != len(want) {
		t.Fatalf("word count differs: self-host %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word %d: self-host %08x, GNU as %08x", i, got[i], want[i])
		}
	}
	checkRefusedSelfHost(t, bin, runner, []string{
		"adr w0, _start",
		"adr x0, nowhere",
		"adr x0",
	})
}

func TestSelfHostArm64CrcGas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkPinnedSelfHost(t, bin, runner, []pinnedAsm{
		{"crc32x w0, w0, x2", 0x9ac24c00},
		{"crc32x w23, w9, x28", 0x9adc4d37},
		{"crc32b w0, w0, w2", 0x1ac24000},
		{"crc32b w23, w9, w28", 0x1adc4137},
		{"crc32h w3, w4, w5", 0x1ac54483},
		{"crc32w w30, wzr, w1", 0x1ac14bfe},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"crc32x w0, w1, w2",
		"crc32x x0, x1, x2",
		"crc32b w0, w1, x2",
		"crc32w x0, w1, w2",
		"crc32h w0, w1",
		"crc32b w0, w1, #1",
	})
}
