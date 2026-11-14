package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func arm64FormCases() []string {
	var out []string
	add := func(fs string, args ...any) { out = append(out, fmt.Sprintf(fs, args...)) }

	widths := []struct{ d, n, m, zr string }{
		{"x1", "x2", "x3", "xzr"},
		{"w1", "w2", "w3", "wzr"},
	}

	for _, w := range widths {
		for _, m := range []string{"add", "adds", "sub", "subs"} {
			add("%s %s, %s, %s", m, w.d, w.n, w.m)
			for _, sh := range []string{"lsl", "lsr", "asr"} {
				for _, amt := range []int{1, 4, 31} {
					add("%s %s, %s, %s, %s #%d", m, w.d, w.n, w.m, sh, amt)
				}
			}
			for _, imm := range []int{0, 1, 255, 4095, 4096, 8192, 16773120} {
				add("%s %s, %s, #%d", m, w.d, w.n, imm)
			}
		}
	}
	add("cmp x2, x3")
	add("cmn x2, x3")
	add("cmp w2, w3")
	for _, m := range []string{"add", "sub", "adds", "subs"} {
		for _, imm := range []string{"-1", "-16", "-4095", "-4096", "-16773120"} {
			add("%s x1, x2, #%s", m, imm)
			add("%s w1, w2, #%s", m, imm)
		}
		add("%s x1, x2, #1, lsl #12", m)
		add("%s x1, x2, #0, lsl #12", m)
		add("%s w1, w2, #4095, lsl #12", m)
		add("%s x1, x2, #7, lsl #0", m)
		add("%s x1, x2, #-1, lsl #12", m)
	}
	add("cmp x1, #-16")
	add("cmn x1, #-16")
	add("cmp w1, #-4096")
	add("cmn w1, #-1")
	add("add sp, sp, #-16")
	add("sub sp, sp, #-16")
	add("add sp, sp, #-4096")
	add("cmp x2, #4095")
	add("cmp x2, #4096")
	add("cmn w2, #1")
	add("add sp, sp, #16")
	add("sub sp, sp, #16")
	add("add x1, sp, #32")
	add("mov x1, sp")
	add("mov sp, x1")

	for _, w := range widths {
		for _, m := range []string{"and", "orr", "eor"} {
			add("%s %s, %s, %s", m, w.d, w.n, w.m)
			for _, amt := range []int{1, 8} {
				add("%s %s, %s, %s, lsl #%d", m, w.d, w.n, w.m, amt)
			}
		}
	}
	for _, imm := range []string{"0x1", "0x3", "0xff", "0xffff", "0xfffe",
		"0xf0f0f0f0", "0x1010101", "0x8000000f", "0xfffffffe", "0x3fffc000"} {
		add("and w1, w2, #%s", imm)
		add("orr w1, w2, #%s", imm)
		add("eor w1, w2, #%s", imm)
	}
	for _, imm := range []string{"0x1", "0xff", "0xffff", "0xffffffff",
		"0xf0f0f0f0f0f0f0f0", "0x5555555555555555", "0xfffffffffffffffe",
		"0x0000ffff0000ffff", "0xff00ff00ff00ff00"} {
		add("and x1, x2, #%s", imm)
		add("orr x1, x2, #%s", imm)
		add("eor x1, x2, #%s", imm)
	}
	for _, imm := range []string{"0xff", "0xfffe", "0xf0f0f0f0"} {
		add("ands w1, w2, #%s", imm)
		add("tst w1, #%s", imm)
	}
	for _, imm := range []string{"0xff", "0x5555555555555555", "0xfffffffffffffffe"} {
		add("ands x1, x2, #%s", imm)
		add("tst x1, #%s", imm)
	}
	add("ands x1, x2, x3")
	add("ands w1, w2, w3")
	add("tst x1, x2")

	for _, sh := range []int{0, 16, 32, 48} {
		add("movz x1, #%d, lsl #%d", 0x1234, sh)
		add("movk x1, #%d, lsl #%d", 0x1234, sh)
		add("movn x1, #%d, lsl #%d", 0x1234, sh)
	}
	for _, sh := range []int{0, 16} {
		add("movz w1, #%d, lsl #%d", 0x1234, sh)
		add("movk w1, #%d, lsl #%d", 0x1234, sh)
	}
	for _, imm := range []string{"0", "1", "65535", "65536", "-1", "-2"} {
		add("mov x1, #%s", imm)
	}
	for _, imm := range []string{"0", "1", "65535", "65536", "-1"} {
		add("mov w1, #%s", imm)
	}
	add("mov x1, x2")
	add("mov w1, w2")

	for _, w := range widths {
		hi := 63
		if w.d[0] == 'w' {
			hi = 31
		}
		for _, m := range []string{"lsl", "lsr", "asr", "ror"} {
			for _, amt := range []int{0, 1, 7, hi} {
				add("%s %s, %s, #%d", m, w.d, w.n, amt)
			}
			add("%s %s, %s, %s", m, w.d, w.n, w.m)
		}
		for _, f := range [][2]int{{0, 1}, {0, 8}, {4, 4}, {8, 8}} {
			add("ubfx %s, %s, #%d, #%d", w.d, w.n, f[0], f[1])
			add("sbfx %s, %s, #%d, #%d", w.d, w.n, f[0], f[1])
		}
	}
	add("extr x1, x2, x3, #1")
	add("extr x1, x2, x3, #63")
	add("extr w1, w2, w3, #1")

	for _, m := range []string{"sxtb", "sxth", "uxtb", "uxth"} {
		add("%s w1, w2", m)
	}
	for _, m := range []string{"sxtb", "sxth", "sxtw"} {
		add("%s x1, w2", m)
	}

	for _, w := range widths {
		add("mul %s, %s, %s", w.d, w.n, w.m)
		add("msub %s, %s, %s, %s", w.d, w.n, w.m, w.d)
		add("sdiv %s, %s, %s", w.d, w.n, w.m)
		add("udiv %s, %s, %s", w.d, w.n, w.m)
		add("neg %s, %s", w.d, w.n)
		add("clz %s, %s", w.d, w.n)
		add("cls %s, %s", w.d, w.n)
		add("rbit %s, %s", w.d, w.n)
		add("rev %s, %s", w.d, w.n)
		add("rev16 %s, %s", w.d, w.n)
	}
	add("rev32 x1, x2")

	for _, c := range []string{"eq", "ne", "cs", "hs", "cc", "lo", "mi", "pl",
		"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le"} {
		add("csel x1, x2, x3, %s", c)
		add("csel w1, w2, w3, %s", c)
		add("cset x1, %s", c)
		add("cset w1, %s", c)
	}
	for _, q := range []struct{ a, b string }{{"q0", "q2"}, {"q17", "q31"}} {
		add("ldr %s, [x6]", q.a)
		add("ldr %s, [x6, #32]", q.a)
		add("ldr %s, [x6, #-16]", q.a)
		add("ldr %s, [x6, #8]", q.a)
		add("str %s, [x7, #65520]", q.b)
		add("ldr %s, [x6], #16", q.a)
		add("str %s, [x7, #-32]!", q.b)
		add("ldur %s, [x6, #-48]", q.a)
		add("stur %s, [x7, #3]", q.b)
		add("ldp %s, %s, [x6], #32", q.a, q.b)
		add("ldp %s, %s, [x6, #-32]!", q.a, q.b)
		add("ldr %s, [x6, #-16]!", q.a)
		add("stp %s, %s, [x7, #-1024]!", q.a, q.b)
		add("ldp %s, %s, [sp, #1008]", q.a, q.b)
	}
	// The forms of the ChaCha20 kernel, __fern_chacha20_xor_into.
	for _, f := range []string{
		"rev32 v13.8h, v13.8h", "tbl v12.16b, {v12.16b}, v22.16b", "ushr v4.4s, v23.4s, #20", "sli v4.4s, v23.4s, #12",
		"ushr v7.4s, v26.4s, #25", "sli v7.4s, v26.4s, #7", "add v0.4s, v0.4s, v4.4s", "eor v23.16b, v4.16b, v8.16b",
		"zip1 v23.4s, v0.4s, v1.4s", "zip2 v24.4s, v0.4s, v1.4s", "zip1 v27.2d, v23.2d, v25.2d", "zip2 v30.2d, v24.2d, v26.2d",
		"dup v13.4s, v19.s[1]", "dup v0.4s, v16.s[0]", "dup v21.4s, w9", "ins v23.d[1], x10", "ins v16.s[3], w9",
		"orr v12.16b, v20.16b, v20.16b", "movi v23.16b, #0", "movz x9, #1, lsl #32", "movk x10, #3, lsl #32",
		"movk x9, #1541, lsl #48", "ldr q31, [x1, #240]", "str q31, [x1, #240]", "str q27, [x29, #464]",
		"stp d8, d9, [x29, #104]", "ldp d14, d15, [x29, #152]", "ld1 {v17.16b}, [x9]", "subs w9, w9, #1",
	} {
		add("%s", f)
	}
	// The forms of the Poly1305 kernel, __fern_poly1305_blocks.
	for _, f := range []string{
		"mul x4, x12, x9", "umulh x15, x13, x9", "adds x4, x4, x6", "adcs x13, x13, x5", "adc x7, x7, xzr", "adc x14, x14, x7",
		"adds x4, x12, #5", "add x11, x10, x10, lsr #2", "and x8, x14, #0xfffffffffffffffc", "and x14, x14, #3",
		"movz x4, #0xfffc", "movk x4, #0x0fff, lsl #48", "csel x12, x4, x12, ne", "ldp x12, x13, [x0, #8]", "stp xzr, xzr, [x29, #224]",
		"ldrb w6, [x1, x4]", "strb w6, [x5, x4]",
	} {
		add("%s", f)
	}
	// The forms of the fused kernels, __fern_chacha20_poly1305_seal_into and __fern_chacha20_poly1305_open_into.
	for _, f := range []string{
		"ldp x4, x5, [x0]", "add x0, x0, #16", "ldr x7, [x1, x6]", "ldr x8, [x5, x6]", "str x7, [x1, x6]", "add x7, x6, #8",
		"cmp x7, x2", "subs x2, x2, #192", "subs x2, x2, #256", "sub x3, x3, x0", "add x0, x0, x3",
		"stp xzr, xzr, [x29, #64]", "ldp x4, x5, [x29, #240]", "stp x4, x5, [x29, #48]", "str q23, [x29, #464]",
		"sxtw x6, w6", "mov w3, #8", "subs w3, w3, #1", "eor x4, x4, x12", "orr x4, x4, x5", "cset w4, ne", "strb w4, [x3]",
		"stp x12, x13, [x3]",
	} {
		add("%s", f)
	}
	// The forms of __fern_scan_set, the scan with a set that is not a constant.
	for _, f := range []string{
		"csel x10, x10, x3, le", "add x8, x2, #8", "ldrb w6, [x5, #3]", "ldrb w7, [x8, x6]", "ldrb w7, [x7, #8]",
		"sub x9, x2, #88", "mov x11, sp", "mov sp, x11", "sub sp, sp, #352", "stp q0, q0, [x9, #320]",
		"add x6, x9, x10", "strb w7, [x6, #96]", "ldr q22, [x10]", "ldr q1, [x9, #336]", "movi v5.16b, #64",
		"movi v5.16b, #128", "cmtst v1.16b, v1.16b, v1.16b", "and v1.16b, v1.16b, v5.16b", "orr v21.16b, v21.16b, v1.16b",
	} {
		add("%s", f)
	}
	return out
}

func TestSelfHostArm64FormsMatchGas(t *testing.T) {
	cases := arm64FormCases()
	if len(cases) < 300 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of families, forms and widths", len(cases))
	}
	compareArm64Cases(t, cases)
}

func compareArm64Cases(t *testing.T, cases []string) {
	t.Helper()
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	for _, c := range cases {
		src := ".text\n_start:\n    " + c + "\n"
		text, rejected := gas.program(t, src)
		if rejected != "" {
			t.Errorf("%-40q GNU as rejects it, so it cannot be the oracle: %s", c, rejected)
			continue
		}
		want := arm64Words(text)
		if refused := refusalsFor(t, bin, runner, src); len(refused) > 0 {
			t.Errorf("%-40q the self-host assembler REFUSES it (%s); GNU as emits %08x",
				c, strings.Join(refused, ", "), want)
			continue
		}
		got := assembleSelfHost(t, bin, runner, src)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%-40q self-host %08x, GNU as %08x", c, got, want)
		}
	}
}

func TestSelfHostArm64RefusesUnencodableForms(t *testing.T) {
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	lines := []string{
		"mov x1, #4294967295",
		"ldr w1, [x2, :lo12:sym]",
		"str x1, [x2, :lo12:sym]",
	}
	for _, ln := range lines {
		if _, rejected := gas.program(t, ".text\n"+ln+"\n"); rejected != "" {
			t.Errorf("%-40s GNU as refuses it too, so this row does not "+
				"describe a self-host gap — check it: %s", ln, rejected)
		}
	}
	checkRefusedSelfHost(t, bin, runner, lines)
}

func TestSelfHostArm64RefusesUnencodableImmediates(t *testing.T) {
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	lines := []string{
		"add x1, x2, #4097",
		"sub x1, x2, #8191",
		"sub sp, sp, #16777215",
		"cmp x2, #5000",
		"cmn w2, #4097",
		"adds x1, x2, #16777216",
		"add x1, x2, #-4097",
		"cmn x1, #-16777215",
		"add x1, x2, #4096, lsl #12",
		"add x1, x2, #-4096, lsl #12",
		"add x1, x2, #5, lsr #12",
		"add x1, x2, #5, lsl #13",
		"and w1, w2, #0x1000000ff",
		"orr w1, w2, #0x300000001",
	}
	for _, ln := range lines {
		if _, rejected := gas.program(t, ".text\n"+ln+"\n"); rejected == "" {
			t.Errorf("%-40s GNU as ACCEPTS it, so it is encodable after all "+
				"and the self-host should encode it rather than refuse", ln)
		}
	}
	checkRefusedSelfHost(t, bin, runner, lines)
}

func TestSelfHostArm64RefusesMisplacedQRegisters(t *testing.T) {
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	lines := []string{
		"add x0, q1, x2",
		"ldrb q0, [x1]",
		"ldpsw q0, q1, [x2]",
		"ldp q0, d1, [x2]",
		"ldr q32, [x1]",
	}
	for _, ln := range lines {
		if _, rejected := gas.program(t, ".text\n"+ln+"\n"); rejected == "" {
			t.Errorf("%-40s GNU as ACCEPTS it, so this row does not describe a misplaced register", ln)
		}
	}
	checkRefusedSelfHost(t, bin, runner, lines)
}
