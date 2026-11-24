package e2ecompiler

import (
	"strings"
	"testing"
)

// A line with fewer operands than its mnemonic takes is refused with one
// arity fault. The assembler does not emit nothing for it, and does not
// abort on an index out of range.
func TestSelfHostArm64AsmRefusesShortOperandLists(t *testing.T) {
	short := []string{
		"swpal x0, x1", "ldaddal x0, x1", "casal x0, x1",
		"ngc x0", "adc x0, x1",
		"mul x0, x1", "msub x0, x1, x2", "udiv x0, x1", "sdiv x0, x1",
		"umulh x0, x1", "madd x0, x1, x2", "smull x0, w1",
		"and x0, x1", "tst x0", "mvn x0", "bic x0, x1",
		"ubfx x0, x1, #1", "bfi x0, x1, #1",
		"extr x0, x1, x2", "ror x0, x1",
		"lsl x0, x1", "lsr x0, x1", "asr x0, x1",
		"cset x0", "cinc x0, x1", "csel x0, x1, x2", "ccmp x0, x1, #0",
		"clz x0", "rbit x0", "sxtw x0", "neg x0",
		"ldxr x0", "stxr w0, x1", "ldar x0",
		"ldr x0", "str x0", "ldrb w0", "strb w0", "ldrh w0",
		"ldur x0", "ldurb w0", "stp x0, x1", "ldp x0, x1",
		"add x0, x1", "sub x0, x1", "cmp x0",
		"movz x0", "movk x0", "mov x0",
		"crc32x w0, w1",
		"fadd d0, d1", "fmadd d0, d1, d2", "fcsel d0, d1, d2", "fcmp d0",
		"fsqrt d0", "fcvt s0", "fcvtzs x0", "scvtf d0", "fmov d0",
		"movi v0.16b",
		"cbz x0", "tbz x0, #3", "b", "bl", "br", "blr", "svc", "dmb",
	}
	valid := []string{
		"cmp x0, x1", "cmn w0, #1", "add x0, x1, x2",
		"tst x0, x1", "mvn x0, x1", "bic x0, x1, x2",
		"ngc x0, x1", "adc x0, x1, x2",
		"cset x0, eq", "cinc x0, x1, ne", "csel x0, x1, x2, lt", "ccmp x0, x1, #0, eq",
		"mul x0, x1, x2", "msub x0, x1, x2, x3", "udiv x0, x1, x2",
		"umulh x0, x1, x2", "smull x0, w1, w2", "madd x0, x1, x2, x3",
		"lsl x0, x1, #3", "ror x0, x1, #3", "extr x0, x1, x2, #3",
		"and x0, x1, x2", "ubfx x0, x1, #1, #3", "bfi x0, x1, #1, #3",
		"clz x0, x1", "sxtw x0, w1", "neg x0, x1",
		"movz x0, #1", "mov x0, x1",
		"ldr x0, [x1]", "str x0, [x1, #8]", "ldrb w0, [x1]",
		"ldur x0, [x1, #-8]", "ldurb w0, [x1, #-1]", "ldp x0, x1, [sp]",
		"ldxr x0, [x1]", "stxr w0, x1, [x2]", "ldar x0, [x1]",
		"swpal x0, x1, [x2]", "ldaddal x1, x0, [x2]", "crc32x w0, w1, x2",
		"fadd d0, d1, d2", "fmadd d0, d1, d2, d3", "fcsel d0, d1, d2, eq",
		"fcmp d0, #0.0", "fsqrt d0, d1", "fcvt s0, d1", "fcvtzs x0, d1",
		"scvtf d0, x1", "fmov d0, x1", "movi v0.16b, #0",
		"isb", "dmb ish", "mrs x0, tpidr_el0", "adrp x0, f",
		"cbz x0, f", "tbz x0, #3, f", "b f", "bl f", "br x0", "svc #0", "ret",
	}

	dir := t.TempDir()
	gcc, runner := x86_64Tooling(t)
	copySelfHostDriver(t, dir, "drivers/asm_refusals_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_refusals_run.fern", "asm_refusals_run")
	run := func(name, body string) string {
		text := "    " + arm64Arch + "\n    .text\nf:\n" + body
		out, err := runX86_64Bin(runner, driver, "arm64", mustWrite(t, dir, name, text)).Output()
		if err != nil {
			t.Fatalf("%q: run: %v", body, err)
		}
		return string(out)
	}

	for _, line := range short {
		mnem, _, _ := strings.Cut(line, " ")
		if got, want := run("short.s", "    "+line+"\n"), mnem+":arity\n"; got != want {
			t.Errorf("%q: refusals %q, want %q", line, got, want)
		}
	}
	if got := run("valid.s", "    "+strings.Join(valid, "\n    ")+"\n"); got != "" {
		t.Errorf("the full forms: refusals %q, want none", got)
	}
}
