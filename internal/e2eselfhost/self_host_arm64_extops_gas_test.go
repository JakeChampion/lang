package e2eselfhost

import (
	"strings"
	"testing"
)

// These tests byte-check the #7887 port of the #7886 instruction surface
// to the self-host arm64 assembler (examples/self_host/arm64_native.fern)
// against aarch64-linux-gnu-as, through the in-process bench driver — the
// same no-qemu, encodings-only pattern as
// TestSelfHostArm64AsmEncodingMatchesNative. Every `want` below is what
// GNU as emits for the same line (read back with objdump — never
// hand-derived, since a wrong field placement usually still assembles as
// some other valid instruction); each family checks low AND high register
// numbers so a dropped 5-bit field cannot pass, and each family's
// unencodable near-miss shapes must be REFUSED, not folded into a field
// that does not exist.

// pinnedAsm is one instruction line with its aarch64-linux-gnu-as word.
type pinnedAsm struct {
	asm  string
	want uint32
}

// checkPinnedSelfHost assembles the cases as one program and compares
// word-for-word, naming the diverging source line.
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

// checkRefusedSelfHost feeds each line separately and requires the
// assembler to record a refusal for it.
func checkRefusedSelfHost(t *testing.T, bin string, runner []string, lines []string) {
	t.Helper()
	for _, ln := range lines {
		if got := refusalsFor(t, bin, runner, ".text\n_start:\n    "+ln+"\n    ret\n"); len(got) == 0 {
			t.Errorf("%q: expected a refusal, the assembler accepted it", ln)
		}
	}
}

// TestSelfHostArm64CarryMulGas: the carry chain (adc/adcs/sbc/sbcs and
// the ngc/ngcs aliases) and the high/widening multiplies.
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
	// The carry ops have no immediate or shifted form, and the
	// high/widening multiplies have fixed register widths (no sf bit).
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

// TestSelfHostArm64LogicalNegatedGas: tst/ands (register, shifted and
// bitmask-immediate) and the Rm-inverting bic/bics/orn/eon with the
// mvn/negs aliases, plus the shifted forms of plain and/orr/eor (whose
// fourth operand used to be silently DROPPED).
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
	// bic/bics/orn/eon have no immediate encoding (the bitmask class has
	// no invert bit); GAS aliases `bic #v` to `and #~v`, refused here.
	checkRefusedSelfHost(t, bin, runner, []string{
		"bic x0, x1, #0xff",
		"bics x0, x1, #0xff",
		"orn x0, x1, #0xff",
		"eon w0, w1, #0xf",
		"tst x0, #0", // 0 is not an encodable bitmask
		"tst x0",
		"mvn x0",
		"negs x0, x1, x2",
		"orr x0, x1, x2, banana #1",
	})
}

// TestSelfHostArm64ExtrBitfieldGas: extr, both ror forms (the extr alias
// and RORV), and the BFM-family insert aliases, range-checked — the raw
// immr/imms fields would wrap silently.
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
		"bfi x0, x1, #60, #8",   // lsb+width > 64
		"bfi w0, w1, #30, #5",   // lsb+width > 32
		"bfxil x0, x1, #64, #1", // lsb out of range
		"ubfiz x0, x1, #0, #0",  // width < 1
		"sbfiz w0, w1, #-1, #4",
		"bfi x0, x1, #4", // missing width
	})
}

// TestSelfHostArm64ExtractExtendGas pins #8000 wave 1: the bitfield-EXTRACT
// pair, the sign/zero-extend aliases, the sign-extending byte/half loads, and
// the two operand-less-ish branches — every mnemonic the native arm64
// assembler encoded while the self-host one silently recorded it as unknown.
//
// The W rows are the ones with teeth. A 32-bit bitfield instruction is not its
// 64-bit sibling with sf cleared: N drops too, so `ubfx w1, w2, #3, #8` is
// 0x53032841 and not 0xd3432841. The self-host's ubfx encoder hardcoded the
// 64-bit base, so every W-form extract it assembled was a 64-bit UBFM.
//
// The ldrsb/ldrsh rows carry the inverse trap: opc names the DESTINATION
// width, so the X form has the SMALLER base word and reading the pair the
// obvious way round swaps them.
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
		// gas takes the X spelling of the unsigned pair and emits the W word
		// anyway; matching that is what keeps the two assemblers identical on
		// input a user can legally write.
		{"uxtb x28, w9", 0x53001d3c},
		{"ldrsb x23, [x9, #4095]", 0x39bffd37},
		{"ldrsb w28, [x9]", 0x39c0013c},
		{"ldrsh x23, [x9, #8190]", 0x79bffd37},
		{"ldrsh w28, [x9, #2]", 0x79c0053c},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		// The extract pair now goes through the same range check as the
		// insert aliases; without it these wrap into a different, valid
		// instruction rather than failing.
		"ubfx x0, x1, #60, #8",
		"ubfx w0, w1, #30, #5",
		"sbfx x0, x1, #64, #1",
		"sbfx w0, w1, #0, #0",
		"ubfx x0, x1, #4",
		// The sign-extending loads have only an immediate-offset encoder,
		// so an index register must be refused rather than dropped.
		"ldrsb x0, [x1, x2]",
		"ldrsh w0, [x1, w2, uxtw #1]",
	})
}

// TestSelfHostArm64CondCmpSelGas: ccmp/ccmn (register and imm5 forms) and
// the conditional-select family with its inverted-condition aliases.
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
		// csel carries its width in the sf bit; the W form used to encode
		// as X (the same class as cset's #6062 bug, one alias over).
		{"csel w1, w2, w3, lt", 0x1a83b041},
		{"csel x1, x2, x3, lt", 0x9a83b041},
	})
	checkRefusedSelfHost(t, bin, runner, []string{
		"ccmp x0, x1, #16, eq", // nzcv is 4 bits
		"ccmp x0, #32, #0, eq", // imm5 is 0..31
		"ccmp x0, #-1, #0, eq", // imm5 is unsigned
		"ccmn x0, x1, #0, xx",  // bad condition
		"csinc x0, x1, x2",     // missing condition
		"cinc x0, x1",          // missing condition
		"csetm x0",             // missing condition
		"csetm x0, al",         // AL cannot be inverted
		"ccmp x0, x1, #0, al",
	})
}

// TestSelfHostArm64RevClsGas: rev (whose X and W forms differ in opc, not
// just sf), rev32 (X only), and cls.
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
		"rev32 w0, w1", // no W form: that operation is `rev Wd, Wn`
		"rev x0",
		"cls x0, x1, x2",
	})
}

// TestSelfHostArm64ExtRegAddressingGas: the extended-register addressing
// forms `[Xn, Wm, uxtw|sxtw {#s}]` / `[Xn, Xm, sxtx|lsl {#s}]` on every
// plain load/store width. Note the byte access: an explicit `#0` amount
// sets the S bit where the bare extend leaves it clear — GNU as
// distinguishes the two spellings the same way.
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
	// The amount must be 0 or log2(access size), the extend must match the
	// offset register's width, and a W offset register without a widening
	// extend is not a valid encoding (GNU as rejects each the same way).
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

// TestSelfHostArm64PairWDGas: ldp/stp for W-register pairs (offset scale
// 4) and D-register pairs (scale 8) in all three addressing modes, plus
// the X modes the frame code has always used — now range-checked, since
// the imm7 mask would wrap an out-of-range offset into a different, valid
// instruction.
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
		"ldp w0, w1, [x2, #2]",    // W pairs scale by 4
		"ldp w0, w1, [x2, #256]",  // out of the scaled imm7 range
		"stp w1, w2, [x3, #-260]", // likewise, negative side
		"ldp d0, d1, [x2, #4]",    // D pairs scale by 8
		"ldp w0, x1, [x2]",        // mixed widths
		"ldp s0, s1, [x2]",        // S pairs not supported (loud gap)
	})
}

// TestSelfHostArm64FP32UnscaledGas: the S-register loads/stores (every
// mode the D form supports, including the silent ldur/stur rewrite of
// negative offsets GNU as performs), the D pre/post writeback modes,
// ldurb/sturb/ldurh/sturh, and the sign-extending unscaled loads.
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
		{"ldr s23, [x9, #-4]", 0xbc5fc137}, // ldur rewrite
		{"str s23, [x9, #-8]", 0xbc1f8137}, // stur rewrite
		{"ldur s23, [x9, #-4]", 0xbc5fc137},
		{"stur s23, [x9, #-4]", 0xbc1fc137},
		{"ldur s0, [x1, #255]", 0xbc4ff020},
		{"stur s0, [x1, #-256]", 0xbc100020},
		// The D writeback modes: `ldr d…!` used to drop the writeback and
		// encode a plain LDUR.
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
		"ldursw w0, [x1]",        // destination must be X
		"ldurh w0, [x1, #256]",   // out of imm9 range
		"ldursb x0, [x1, #-257]", // likewise
		"ldur s0, [x1, x2]",      // no register-offset unscaled form
		"ldursh x0, [x1, #2]!",   // no writeback form
	})
}

// TestSelfHostArm64ExclusivesBarriersGas: the ARMv8.0 exclusive /
// acquire-release accesses (no LSE — the baseline is plain ARMv8-A) and
// the barriers, whose dmb/dsb option is REQUIRED as GNU as requires it.
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
		"stxr x11, x23, [x9]", // status register must be W
		"ldxr x0, [x1, #8]",   // no offset form
		"ldaxr x0, [x1], #8",  // no writeback form
		"ldxrb x0, [x1]",      // byte data register must be W
		"ldaxrh x0, [x1]",     // half data register must be W
		"stlr w0, [x1, #4]",   // no offset form
		"dmb",                 // GNU as requires the option
		"dsb",
		"dmb foo",           // unknown option
		"isb ish",           // isb takes only sy
		"ldxr x0, [x1, x2]", // no register-offset form
	})
}

// TestSelfHostArm64FPScalarGas: the fused multiply-adds, fnmul, min/max,
// fcsel, fccmp, fcmpe (register and #0.0), and the single-precision forms
// of the whole scalar family — previously a loud D-only gap.
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
		"fmadd d0, s1, d2, d3",  // mixed precision
		"fmin d0, d1, s2",       // likewise
		"fcsel d0, d1, s2, eq",  // likewise
		"fccmp d0, d1, #16, eq", // nzcv is 4 bits
		"fcmpe d0, #1.0",        // only #0.0 exists
		"fcmp d0, s1",           // mixed precision
		"fnmul d0, d1",          // missing operand
	})
}

// TestSelfHostArm64SysregFpGas: the `fp` spelling of x29 (GNU as accepts
// it) and the extended system-register table with the msr write form.
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
	// dczid_el0 and the counter-timers are read-only from EL0: the msr
	// encoding would exist and trap at runtime, so it must refuse.
	checkRefusedSelfHost(t, bin, runner, []string{
		"msr dczid_el0, x0",
		"msr cntvct_el0, x0",
		"msr cntfrq_el0, x0",
		"msr nzcv, w0", // source must be X
		"msr bogus_reg, x0",
		"msr nzcv",
		"mrs w0, nzcv", // destination must be X
	})
}

// TestSelfHostArm64AdrGas pins `adr Xd, label` through the program+link
// path (a byte-relative kind-3 fixup beside adrp's page-relative one).
// The words are what aarch64-linux-gnu-as emits for the same layout; adr
// is PC-relative, so they are independent of the text base address.
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
		"adr w0, _start",  // W destination: no such form
		"adr x0, nowhere", // undefined symbol
		"adr x0",          // missing operand
	})
}
