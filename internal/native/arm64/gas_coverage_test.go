package arm64_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// assemblePinned assembles each case and compares against the expected
// word. Every expectation in the tables below is what
// aarch64-linux-gnu-as emits for the same line, read back with objdump —
// never hand-derived, since a wrong field placement usually still
// assembles as some other valid instruction.
func assemblePinned(t *testing.T, cases []struct {
	asm  string
	want uint32
}) {
	t.Helper()
	for _, c := range cases {
		got, err := arm64.Assemble(c.asm)
		if err != nil {
			t.Errorf("Assemble(%q): %v", c.asm, err)
			continue
		}
		want := arm64.Put(nil, c.want)
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got % x, want % x (GNU as)", c.asm, got, want)
		}
	}
}

// assembleRejects checks that every line is refused. Each is a
// near-miss operand shape that must NOT silently assemble as a
// different instruction.
func assembleRejects(t *testing.T, lines []string) {
	t.Helper()
	for _, asm := range lines {
		if _, err := arm64.Assemble(asm); err == nil {
			t.Errorf("Assemble(%q): expected an error, got none", asm)
		}
	}
}

// TestCarryMulInsns pins the carry-chain arithmetic and the multiply
// family. Expectations from aarch64-linux-gnu-as; high register numbers
// (x23/x9/x28) exercise every 5-bit field.
func TestCarryMulInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tadc x0, x1, x2\n", 0x9a020020},
		{"\tadc x23, x9, x28\n", 0x9a1c0137},
		{"\tadc w3, w4, w5\n", 0x1a050083},
		{"\tadc w28, w9, w23\n", 0x1a17013c},
		{"\tsbc x23, x9, x28\n", 0xda1c0137},
		{"\tsbc w28, w9, w23\n", 0x5a17013c},
		{"\tadcs x23, x9, x28\n", 0xba1c0137},
		{"\tadcs w28, w9, w23\n", 0x3a17013c},
		{"\tsbcs x23, x9, x28\n", 0xfa1c0137},
		{"\tsbcs w28, w9, w23\n", 0x7a17013c},
		{"\tngc x23, x9\n", 0xda0903f7},
		{"\tngc w28, w9\n", 0x5a0903fc},
		{"\tngcs x23, x9\n", 0xfa0903f7},
		{"\tngcs w28, w9\n", 0x7a0903fc},
		{"\tumulh x0, x1, x2\n", 0x9bc27c20},
		{"\tumulh x23, x9, x28\n", 0x9bdc7d37},
		{"\tsmulh x23, x9, x28\n", 0x9b5c7d37},
		{"\tmadd x23, x9, x28, x11\n", 0x9b1c2d37},
		{"\tmadd w23, w9, w28, w11\n", 0x1b1c2d37},
		{"\tsmull x23, w9, w28\n", 0x9b3c7d37},
		{"\tumull x23, w9, w28\n", 0x9bbc7d37},
		{"\tsmaddl x23, w9, w28, x11\n", 0x9b3c2d37},
		{"\tumaddl x23, w9, w28, x11\n", 0x9bbc2d37},
		{"\tsmsubl x23, w9, w28, x11\n", 0x9b3cad37},
		{"\tumsubl x23, w9, w28, x11\n", 0x9bbcad37},
	})
}

// TestCarryMulReject: the carry ops have no immediate form, and the
// high/widening multiplies have fixed register widths (no sf bit) — a
// wrong width must be refused, not reinterpreted.
func TestCarryMulReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tadc x0, x1, #1\n",
		"\tsbcs w0, w1, #2\n",
		"\tadc x0, x1, x2, lsl #1\n",
		"\tngc x0, x1, x2\n",
		"\tumulh w0, w1, w2\n",
		"\tsmulh w0, w1, w2\n",
		"\tumulh x0, x1, w2\n",
		"\tsmull x0, x1, x2\n",
		"\tumull w0, w1, w2\n",
		"\tsmaddl x23, w9, w28, w11\n",
		"\tumaddl w23, w9, w28, x11\n",
	})
}

// TestLogicalNegatedInsns pins tst/ands and the negated logical ops
// (bic/bics/orn/eon) plus the mvn and negs aliases. Expectations from
// aarch64-linux-gnu-as.
func TestLogicalNegatedInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\ttst x23, x9\n", 0xea0902ff},
		{"\ttst w28, w9\n", 0x6a09039f},
		{"\ttst x1, x2, lsl #3\n", 0xea020c3f},
		{"\ttst w1, w2, lsr #7\n", 0x6a421c3f},
		{"\ttst x0, #0xff\n", 0xf2401c1f},
		{"\ttst w9, #0xf\n", 0x72000d3f},
		{"\tands x23, x9, x28\n", 0xea1c0137},
		{"\tands w28, w9, w23\n", 0x6a17013c},
		{"\tands x1, x2, x3, lsl #4\n", 0xea031041},
		{"\tands w1, w2, w3, asr #2\n", 0x6a830841},
		{"\tands x0, x1, #0xff00\n", 0xf2781c20},
		{"\tands w9, w10, #0x7\n", 0x72000949},
		{"\tbic x23, x9, x28\n", 0x8a3c0137},
		{"\tbic w28, w9, w23\n", 0x0a37013c},
		{"\tbic x1, x2, x3, lsl #8\n", 0x8a232041},
		{"\tbic w1, w2, w3, ror #3\n", 0x0ae30c41},
		{"\tbics x23, x9, x28\n", 0xea3c0137},
		{"\tbics w28, w9, w23\n", 0x6a37013c},
		{"\tbics x1, x2, x3, lsr #2\n", 0xea630841},
		{"\torn x23, x9, x28\n", 0xaa3c0137},
		{"\torn w28, w9, w23\n", 0x2a37013c},
		{"\torn x1, x2, x3, asr #5\n", 0xaaa31441},
		{"\teon x23, x9, x28\n", 0xca3c0137},
		{"\teon w28, w9, w23\n", 0x4a37013c},
		{"\teon x1, x2, x3, lsl #16\n", 0xca234041},
		{"\tmvn x23, x9\n", 0xaa2903f7},
		{"\tmvn w28, w9\n", 0x2a2903fc},
		{"\tmvn x1, x2, lsl #4\n", 0xaa2213e1},
		{"\tmvn w1, w2, asr #3\n", 0x2aa20fe1},
		{"\tnegs x23, x9\n", 0xeb0903f7},
		{"\tnegs w28, w9\n", 0x6b0903fc},
	})
}

// TestLogicalNegatedReject: bic/bics/orn/eon have no immediate encoding
// (the bitmask class has no invert bit); GAS aliases `bic Rd, Rn, #v`
// to `and Rd, Rn, #~v` but this assembler refuses the alias so what was
// encoded is always what was written.
func TestLogicalNegatedReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tbic x0, x1, #0xff\n",
		"\tbics x0, x1, #0xff\n",
		"\torn x0, x1, #0xff\n",
		"\teon w0, w1, #0xf\n",
		"\ttst x0, #0\n", // 0 is not an encodable bitmask
		"\ttst x0\n",
		"\tmvn x0\n",
		"\tnegs x0, x1, x2\n",
	})
}

// TestExtrRorInsns pins extr and both ror forms (the extr alias and
// RORV). Expectations from aarch64-linux-gnu-as.
func TestExtrRorInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\textr x0, x1, x2, #12\n", 0x93c23020},
		{"\textr x23, x9, x28, #63\n", 0x93dcfd37},
		{"\textr w28, w9, w23, #17\n", 0x1397453c},
		{"\tror x23, x9, #63\n", 0x93c9fd37},
		{"\tror w28, w9, #17\n", 0x1389453c},
		{"\tror x23, x9, x28\n", 0x9adc2d37},
		{"\tror w28, w9, w23\n", 0x1ad72d3c},
	})
}

func TestExtrRorReject(t *testing.T) {
	assembleRejects(t, []string{
		"\textr x0, x1, x2, #64\n",
		"\textr w0, w1, w2, #32\n",
		"\textr x0, x1, x2, #-1\n",
		"\tror x0, x1, #64\n",
		"\tror w0, w1, #32\n",
		"\tror x0, x1\n",
	})
}

// TestBitfieldInsertInsns pins the BFM-family insert aliases.
// Expectations from aarch64-linux-gnu-as — the immr/imms alias
// arithmetic ((-lsb) mod size vs lsb) is exactly the kind of thing that
// wraps into a different valid bitfield when wrong.
func TestBitfieldInsertInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tbfi x0, x1, #4, #8\n", 0xb37c1c20},
		{"\tbfi x23, x9, #40, #16\n", 0xb3583d37},
		{"\tbfi w28, w9, #3, #5\n", 0x331d113c},
		{"\tbfxil x0, x1, #4, #8\n", 0xb3442c20},
		{"\tbfxil x23, x9, #40, #16\n", 0xb368dd37},
		{"\tbfxil w28, w9, #3, #5\n", 0x33031d3c},
		{"\tubfiz x23, x9, #40, #16\n", 0xd3583d37},
		{"\tubfiz w28, w9, #3, #5\n", 0x531d113c},
		{"\tsbfiz x23, x9, #40, #16\n", 0x93583d37},
		{"\tsbfiz w28, w9, #3, #5\n", 0x131d113c},
	})
}

func TestBitfieldInsertReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tbfi x0, x1, #60, #8\n",   // lsb+width > 64
		"\tbfi w0, w1, #30, #5\n",   // lsb+width > 32
		"\tbfxil x0, x1, #64, #1\n", // lsb out of range
		"\tubfiz x0, x1, #0, #0\n",  // width < 1
		"\tsbfiz w0, w1, #-1, #4\n",
		"\tbfi x0, x1, #4\n", // missing width
	})
}

// TestCondCmpSelInsns pins ccmp/ccmn (register and imm5 forms) and the
// conditional-select family with its inverted-condition aliases.
// Expectations from aarch64-linux-gnu-as.
func TestCondCmpSelInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tccmp x0, x1, #0, eq\n", 0xfa410000},
		{"\tccmp x23, x9, #15, lt\n", 0xfa49b2ef},
		{"\tccmp w28, w9, #8, hi\n", 0x7a498388},
		{"\tccmp x23, #9, #15, lt\n", 0xfa49baef},
		{"\tccmp w28, #31, #8, hi\n", 0x7a5f8b88},
		{"\tccmn x23, x9, #15, lt\n", 0xba49b2ef},
		{"\tccmn w28, w9, #8, hi\n", 0x3a498388},
		{"\tccmn x23, #9, #15, lt\n", 0xba49baef},
		{"\tccmn w28, #31, #8, hi\n", 0x3a5f8b88},
		{"\tcsinc x23, x9, x28, lt\n", 0x9a9cb537},
		{"\tcsinc w28, w9, w23, hi\n", 0x1a97853c},
		{"\tcsinv x23, x9, x28, lt\n", 0xda9cb137},
		{"\tcsinv w28, w9, w23, hi\n", 0x5a97813c},
		{"\tcsneg x23, x9, x28, lt\n", 0xda9cb537},
		{"\tcsneg w28, w9, w23, hi\n", 0x5a97853c},
		{"\tcinc x23, x9, lt\n", 0x9a89a537},
		{"\tcinc w28, w9, hi\n", 0x1a89953c},
		{"\tcinv x23, x9, lt\n", 0xda89a137},
		{"\tcinv w28, w9, hi\n", 0x5a89913c},
		{"\tcneg x23, x9, lt\n", 0xda89a537},
		{"\tcneg w28, w9, hi\n", 0x5a89953c},
		{"\tcsetm x23, lt\n", 0xda9fa3f7},
		{"\tcsetm w28, hi\n", 0x5a9f93fc},
	})
}

func TestCondCmpSelReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tccmp x0, x1, #16, eq\n", // nzcv is 4 bits
		"\tccmp x0, #32, #0, eq\n", // imm5 is 0..31
		"\tccmp x0, #-1, #0, eq\n", // imm5 is unsigned
		"\tccmn x0, x1, #0, xx\n",  // bad condition
		"\tcsinc x0, x1, x2\n",     // missing condition
		"\tcinc x0, x1\n",          // missing condition
		"\tcsetm x0\n",             // missing condition
		"\tcsetm x0, al\n",         // AL cannot be inverted
		"\tccmp x0, x1, #0, al\n",  // (also no al entry)
	})
}

// TestRevClsInsns pins rev (whose X and W forms differ in opc, not just
// sf), rev32 (X only), and cls. Expectations from aarch64-linux-gnu-as.
func TestRevClsInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\trev x23, x9\n", 0xdac00d37},
		{"\trev w28, w9\n", 0x5ac0093c},
		{"\trev32 x23, x9\n", 0xdac00937},
		{"\tcls x23, x9\n", 0xdac01537},
		{"\tcls w28, w9\n", 0x5ac0153c},
	})
}

func TestRevClsReject(t *testing.T) {
	assembleRejects(t, []string{
		"\trev32 w0, w1\n", // no W form: that operation is `rev Wd, Wn`
		"\trev x0\n",
		"\tcls x0, x1, x2\n",
	})
}

// TestExtRegAddressing pins the extended-register addressing forms —
// the documented codegen workaround gap (internal/codegen/arm64 emits a
// separate sxtw/add today because the assembler lacked these).
// Expectations from aarch64-linux-gnu-as. Note the byte access: an
// explicit `#0` amount sets the S bit where the bare extend leaves it
// clear, and GNU as distinguishes the two spellings the same way.
func TestExtRegAddressing(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tldr x0, [x1, w2, uxtw]\n", 0xf8624820},
		{"\tldr x23, [x9, w28, uxtw #3]\n", 0xf87c5937},
		{"\tldr x23, [x9, w28, sxtw #3]\n", 0xf87cd937},
		{"\tldr x23, [x9, x28, sxtx]\n", 0xf87ce937},
		{"\tldr x23, [x9, x28, sxtx #3]\n", 0xf87cf937},
		{"\tldr x23, [x9, x28, lsl #3]\n", 0xf87c7937},
		{"\tldr w23, [x9, w28, uxtw #2]\n", 0xb87c5937},
		{"\tldr w23, [x9, w28, sxtw]\n", 0xb87cc937},
		{"\tstr x23, [x9, w28, uxtw #3]\n", 0xf83c5937},
		{"\tstr x23, [x9, x28, sxtx]\n", 0xf83ce937},
		{"\tstr w23, [x9, w28, sxtw #2]\n", 0xb83cd937},
		{"\tldrb w23, [x9, w28, uxtw]\n", 0x387c4937},
		{"\tldrb w23, [x9, w28, sxtw]\n", 0x387cc937},
		{"\tldrb w23, [x9, x28, sxtx]\n", 0x387ce937},
		{"\tstrb w23, [x9, w28, uxtw]\n", 0x383c4937},
		{"\tldrb w0, [x1, w2, uxtw #0]\n", 0x38625820},
		{"\tldrh w23, [x9, w28, uxtw #1]\n", 0x787c5937},
		{"\tldrh w23, [x9, w28, sxtw]\n", 0x787cc937},
		{"\tstrh w23, [x9, x28, sxtx #1]\n", 0x783cf937},
		{"\tstrh w23, [x9, x28, lsl #1]\n", 0x783c7937},
	})
}

// TestExtRegAddressingReject: the amount must be 0 or log2(access
// size), the extend must match the offset register's width, and a W
// offset register without a widening extend is not a valid encoding
// (GNU as rejects each of these the same way).
func TestExtRegAddressingReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tldr x0, [x1, w2, uxtw #2]\n",  // amount must be 0 or 3
		"\tldr x0, [x1, x2, uxtw]\n",     // uxtw needs a W offset register
		"\tldr x0, [x1, w2, sxtx]\n",     // sxtx needs an X offset register
		"\tldr x0, [x1, w2]\n",           // W offset without an extend
		"\tldr x0, [x1, w2, lsl #3]\n",   // lsl needs an X offset register
		"\tldr x0, [x1, x2, lsl]\n",      // lsl needs an amount
		"\tldrb w0, [x1, w2, uxtw #1]\n", // byte access: amount must be 0
		"\tldrh w0, [x1, w2, uxtw #2]\n", // half access: amount must be 0 or 1
		"\tldr x0, [x1, x2, ror #3]\n",   // no such extend option
	})
}

// TestPairWDInsns pins ldp/stp for W-register pairs (offset scale 4)
// and D-register pairs (scale 8) in all three addressing modes.
// Expectations from aarch64-linux-gnu-as.
func TestPairWDInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tldp w0, w1, [x2]\n", 0x29400440},
		{"\tldp w0, w1, [x2, #4]\n", 0x29408440},
		{"\tldp w23, w9, [x28, #8]\n", 0x29412797},
		{"\tstp w23, w9, [x28, #-8]\n", 0x293f2797},
		{"\tldp w23, w9, [x28], #16\n", 0x28c22797},
		{"\tstp w23, w9, [x28, #-16]!\n", 0x29be2797},
		{"\tldp d0, d1, [x2]\n", 0x6d400440},
		{"\tldp d23, d9, [x28, #16]\n", 0x6d412797},
		{"\tstp d23, d9, [x28, #-16]\n", 0x6d3f2797},
		{"\tldp d23, d9, [x28], #32\n", 0x6cc22797},
		{"\tstp d23, d9, [x28, #-32]!\n", 0x6dbe2797},
		{"\tstp d8, d9, [sp, #-16]!\n", 0x6dbf27e8},
	})
}

func TestPairWDReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tldp w0, w1, [x2, #2]\n",    // W pairs scale by 4
		"\tldp w0, w1, [x2, #256]\n",  // out of the scaled imm7 range
		"\tstp w1, w2, [x3, #-260]\n", // likewise, negative side
		"\tldp d0, d1, [x2, #4]\n",    // D pairs scale by 8
		"\tldp w0, x1, [x2]\n",        // mixed widths
		"\tldp s0, s1, [x2]\n",        // S pairs not supported (loud gap)
	})
}

// TestFP32AndUnscaledLoads pins the S-register loads/stores (all the
// modes the D form supports, including the silent ldur/stur rewrite of
// negative offsets that GNU as performs), ldurh/sturh, and the
// sign-extending unscaled loads. Expectations from aarch64-linux-gnu-as.
func TestFP32AndUnscaledLoads(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tldr s0, [x1]\n", 0xbd400020},
		{"\tldr s23, [x9, #4]\n", 0xbd400537},
		{"\tstr s23, [x9, #8]\n", 0xbd000937},
		{"\tldr s23, [x9], #4\n", 0xbc404537},
		{"\tstr s23, [x9], #-4\n", 0xbc1fc537},
		{"\tldr s23, [x9, #4]!\n", 0xbc404d37},
		{"\tstr s23, [x9, #-4]!\n", 0xbc1fcd37},
		{"\tldr s23, [x9, #-4]\n", 0xbc5fc137}, // ldur rewrite
		{"\tstr s23, [x9, #-8]\n", 0xbc1f8137}, // stur rewrite
		{"\tldur s23, [x9, #-4]\n", 0xbc5fc137},
		{"\tstur s23, [x9, #-4]\n", 0xbc1fc137},
		{"\tldur s0, [x1, #255]\n", 0xbc4ff020},
		{"\tstur s0, [x1, #-256]\n", 0xbc100020},
		{"\tldurh w23, [x9, #-2]\n", 0x785fe137},
		{"\tsturh w23, [x9, #-2]\n", 0x781fe137},
		{"\tldurh w0, [x1, #255]\n", 0x784ff020},
		{"\tsturh w0, [x1, #-256]\n", 0x78100020},
		{"\tldursb x23, [x9, #-1]\n", 0x389ff137},
		{"\tldursb w23, [x9, #-1]\n", 0x38dff137},
		{"\tldursb x0, [x1]\n", 0x38800020},
		{"\tldursh x23, [x9, #-2]\n", 0x789fe137},
		{"\tldursh w23, [x9, #-2]\n", 0x78dfe137},
		{"\tldursw x23, [x9, #-4]\n", 0xb89fc137},
		{"\tldursw x0, [x1]\n", 0xb8800020},
	})
}

func TestFP32AndUnscaledReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tldursw w0, [x1]\n",        // destination must be X
		"\tldurh w0, [x1, #256]\n",   // out of imm9 range
		"\tldursb x0, [x1, #-257]\n", // likewise
		"\tldur s0, [x1, x2]\n",      // no register-offset unscaled form
		"\tldursh x0, [x1, #2]!\n",   // no writeback form
	})
}

// TestExclusivesAndBarriers pins the ARMv8.0 exclusive/acquire-release
// accesses (no LSE atomics — the baseline is plain ARMv8-A) and the
// barriers. Expectations from aarch64-linux-gnu-as.
func TestExclusivesAndBarriers(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tldxr x23, [x9]\n", 0xc85f7d37},
		{"\tldxr w23, [x9]\n", 0x885f7d37},
		{"\tldaxr x23, [x9]\n", 0xc85ffd37},
		{"\tldaxr w23, [x9]\n", 0x885ffd37},
		{"\tstxr w11, x23, [x9]\n", 0xc80b7d37},
		{"\tstxr w11, w23, [x9]\n", 0x880b7d37},
		{"\tstlxr w11, x23, [x9]\n", 0xc80bfd37},
		{"\tstlxr w11, w23, [x9]\n", 0x880bfd37},
		{"\tldar x23, [x9]\n", 0xc8dffd37},
		{"\tldar w23, [x9]\n", 0x88dffd37},
		{"\tstlr x23, [x9]\n", 0xc89ffd37},
		{"\tstlr w23, [x9]\n", 0x889ffd37},
		{"\tldxrb w23, [x9]\n", 0x085f7d37},
		{"\tldaxrb w23, [x9]\n", 0x085ffd37},
		{"\tstxrb w11, w23, [x9]\n", 0x080b7d37},
		{"\tstlxrb w11, w23, [x9]\n", 0x080bfd37},
		{"\tldarb w23, [x9]\n", 0x08dffd37},
		{"\tstlrb w23, [x9]\n", 0x089ffd37},
		{"\tldxrh w23, [x9]\n", 0x485f7d37},
		{"\tldaxrh w23, [x9]\n", 0x485ffd37},
		{"\tstxrh w11, w23, [x9]\n", 0x480b7d37},
		{"\tstlxrh w11, w23, [x9]\n", 0x480bfd37},
		{"\tldarh w23, [x9]\n", 0x48dffd37},
		{"\tstlrh w23, [x9]\n", 0x489ffd37},
		{"\tdmb sy\n", 0xd5033fbf},
		{"\tdmb ish\n", 0xd5033bbf},
		{"\tdmb ishld\n", 0xd50339bf},
		{"\tdmb ishst\n", 0xd5033abf},
		{"\tdmb ld\n", 0xd5033dbf},
		{"\tdmb st\n", 0xd5033ebf},
		{"\tdsb sy\n", 0xd5033f9f},
		{"\tdsb ish\n", 0xd5033b9f},
		{"\tdsb ishld\n", 0xd503399f},
		{"\tdsb ishst\n", 0xd5033a9f},
		{"\tdsb ld\n", 0xd5033d9f},
		{"\tdsb st\n", 0xd5033e9f},
		{"\tisb\n", 0xd5033fdf},
		{"\tisb sy\n", 0xd5033fdf},
	})
}

func TestExclusivesAndBarriersReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tstxr x11, x23, [x9]\n", // status register must be W
		"\tldxr x0, [x1, #8]\n",   // no offset form
		"\tldaxr x0, [x1], #8\n",  // no writeback form
		"\tldxrb x0, [x1]\n",      // byte data register must be W
		"\tldaxrh x0, [x1]\n",     // half data register must be W
		"\tstlr w0, [x1, #4]\n",   // no offset form
		"\tdmb\n",                 // GNU as requires the option
		"\tdsb\n",                 //
		"\tdmb foo\n",             // unknown option
		"\tisb ish\n",             // isb takes only sy
		"\tldxr x0, [x1, x2]\n",   // no register-offset form
	})
}

// TestFPScalarInsns pins the fused multiply-adds, fnmul, min/max,
// fcsel, fccmp, fcmpe, and the single-precision forms of the unary ops
// (previously a loud D-only gap). Expectations from
// aarch64-linux-gnu-as.
func TestFPScalarInsns(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tfmadd d0, d1, d2, d3\n", 0x1f420c20},
		{"\tfmadd d23, d9, d28, d11\n", 0x1f5c2d37},
		{"\tfmadd s23, s9, s28, s11\n", 0x1f1c2d37},
		{"\tfmsub d23, d9, d28, d11\n", 0x1f5cad37},
		{"\tfmsub s23, s9, s28, s11\n", 0x1f1cad37},
		{"\tfnmadd d23, d9, d28, d11\n", 0x1f7c2d37},
		{"\tfnmadd s23, s9, s28, s11\n", 0x1f3c2d37},
		{"\tfnmsub d23, d9, d28, d11\n", 0x1f7cad37},
		{"\tfnmsub s23, s9, s28, s11\n", 0x1f3cad37},
		{"\tfnmul d23, d9, d28\n", 0x1e7c8937},
		{"\tfnmul s23, s9, s28\n", 0x1e3c8937},
		{"\tfmin d23, d9, d28\n", 0x1e7c5937},
		{"\tfmin s23, s9, s28\n", 0x1e3c5937},
		{"\tfmax d23, d9, d28\n", 0x1e7c4937},
		{"\tfmax s23, s9, s28\n", 0x1e3c4937},
		{"\tfminnm d23, d9, d28\n", 0x1e7c7937},
		{"\tfminnm s23, s9, s28\n", 0x1e3c7937},
		{"\tfmaxnm d23, d9, d28\n", 0x1e7c6937},
		{"\tfmaxnm s23, s9, s28\n", 0x1e3c6937},
		{"\tfcsel d23, d9, d28, lt\n", 0x1e7cbd37},
		{"\tfcsel s23, s9, s28, hi\n", 0x1e3c8d37},
		{"\tfccmp d23, d9, #15, lt\n", 0x1e69b6ef},
		{"\tfccmp s23, s9, #8, hi\n", 0x1e2986e8},
		{"\tfcmpe d23, d9\n", 0x1e6922f0},
		{"\tfcmpe s23, s9\n", 0x1e2922f0},
		{"\tfcmpe d23, #0.0\n", 0x1e6022f8},
		{"\tfcmpe s23, #0.0\n", 0x1e2022f8},
		{"\tfabs s23, s9\n", 0x1e20c137},
		{"\tfsqrt s23, s9\n", 0x1e21c137},
		{"\tfrintm s23, s9\n", 0x1e254137},
		{"\tfrintp s23, s9\n", 0x1e24c137},
		{"\tfrintz s23, s9\n", 0x1e25c137},
		{"\tfrinta s23, s9\n", 0x1e264137},
		{"\tfrintn s23, s9\n", 0x1e244137},
	})
}

func TestFPScalarReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tfmadd d0, s1, d2, d3\n",  // mixed precision
		"\tfmin d0, d1, s2\n",       // likewise
		"\tfcsel d0, d1, s2, eq\n",  // likewise
		"\tfccmp d0, d1, #16, eq\n", // nzcv is 4 bits
		"\tfcmpe d0, #1.0\n",        // only #0.0 exists
		"\tfcmp d0, s1\n",           // mixed precision
		"\tfnmul d0, d1\n",          // missing operand
	})
}

// TestFpAliasAndSysregs pins the `fp` register alias (x29 — GNU as
// accepts it) and the extended system-register table with the msr
// write form.
func TestFpAliasAndSysregs(t *testing.T) {
	assemblePinned(t, []struct {
		asm  string
		want uint32
	}{
		{"\tmov x0, fp\n", 0xaa1d03e0},
		{"\tadd fp, sp, #32\n", 0x910083fd},
		{"\tstr x0, [fp, #-8]\n", 0xf81f83a0},
		{"\tmrs x23, tpidr_el0\n", 0xd53bd057},
		{"\tmsr tpidr_el0, x23\n", 0xd51bd057},
		{"\tmrs x23, nzcv\n", 0xd53b4217},
		{"\tmsr nzcv, x23\n", 0xd51b4217},
		{"\tmrs x23, fpcr\n", 0xd53b4417},
		{"\tmsr fpcr, x23\n", 0xd51b4417},
		{"\tmrs x23, fpsr\n", 0xd53b4437},
		{"\tmsr fpsr, x23\n", 0xd51b4437},
		{"\tmrs x23, dczid_el0\n", 0xd53b00f7},
	})
}

// TestMsrReject keeps the write direction honest: dczid_el0 and the
// counter-timer registers are read-only from EL0, so an msr to them is
// refused rather than encoded (the encoding would exist and trap at
// runtime).
func TestMsrReject(t *testing.T) {
	assembleRejects(t, []string{
		"\tmsr dczid_el0, x0\n",
		"\tmsr cntvct_el0, x0\n",
		"\tmsr cntfrq_el0, x0\n",
		"\tmsr nzcv, w0\n", // source must be X
		"\tmsr bogus_reg, x0\n",
		"\tmsr nzcv\n",
	})
}

// TestAdr pins `adr Xd, label` through the program path (the fixup
// resolves against symbol addresses, like adrp). The words are what
// aarch64-linux-gnu-as emits for the same layout; adr is PC-relative,
// so they are independent of the text base address.
func TestAdr(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tadr x0, lbl\n" +
		"\tadr x23, lbl\n" +
		"lbl:\n" +
		"\tadr x9, lbl\n"
	text, _, err := arm64.AssembleProgram(src, 0x400078)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	for _, w := range []uint32{0x10000040, 0x10000037, 0x10000009} {
		want = arm64.Put(want, w)
	}
	if !bytes.Equal(text, want) {
		t.Fatalf("got % x, want % x (GNU as)", text, want)
	}
}

func TestAdrReject(t *testing.T) {
	for _, src := range []string{
		"\t.text\n\tadr w0, lbl\nlbl:\n\tret\n", // W destination: no such form
		"\t.text\n\tadr x0, nowhere\n\tret\n",   // undefined symbol
		"\t.text\n\tadr x0\n",                   // missing operand
	} {
		if _, _, err := arm64.AssembleProgram(src, 0x400078); err == nil {
			t.Errorf("AssembleProgram(%q): expected an error, got none", src)
		}
	}
}
