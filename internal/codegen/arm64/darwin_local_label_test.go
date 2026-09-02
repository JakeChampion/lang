package arm64

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRetargetLocals is the unit-level pin on the rewrite rule (#8065).
//
// The interesting cases are the ones a blind string replacement gets wrong:
// a ".L" inside a quoted operand is string DATA, not a symbol, and `.asciz`
// reaches the printer through the same path as every instruction.
func TestRetargetLocals(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{".Lret_3:", "Lret_3:"},
		{"\tb .Lret_3", "\tb Lret_3"},
		{"\tcbz x0, .Lnull_7", "\tcbz x0, Lnull_7"},
		{"\tadrp x0, .LStr_4@PAGE", "\tadrp x0, LStr_4@PAGE"},
		{"\tb.ne .Lblk_2", "\tb.ne Lblk_2"},
		// Two on one line: a definition and a reference.
		{".La: b .Lb", "La: b Lb"},
		// String data must survive untouched — this is the case that makes
		// the scan quote-aware rather than a replacement.
		{"\t.asciz \".L\"", "\t.asciz \".L\""},
		{"\t.asciz \"see .Lfoo for details\"", "\t.asciz \"see .Lfoo for details\""},
		// An escaped quote inside the payload must not end the string early.
		{"\t.asciz \"a\\\" .Lb\"", "\t.asciz \"a\\\" .Lb\""},
		// Not a prefix: preceded by a symbol byte.
		{"\tbl foo.Lbar", "\tbl foo.Lbar"},
		// Untouched lines come back identical.
		{"\tmov x0, x1", "\tmov x0, x1"},
		{"\t.cfi_def_cfa w29, 16", "\t.cfi_def_cfa w29, 16"},
	} {
		if got := retargetLocals(c.in); got != c.want {
			t.Errorf("retargetLocals(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestDarwinUsesMachOLocalPrefix is the end-to-end half: the emitted Darwin
// listing must carry no ELF-style local label at all, while the Linux
// listing still does.
func TestDarwinUsesMachOLocalPrefix(t *testing.T) {
	asm := compile(t, cfiSrc, Options{Darwin: true})
	if n := countOutsideStrings(asm, ".L"); n != 0 {
		t.Errorf("Darwin listing still has %d ELF-style .L labels outside string data", n)
	}
	if !strings.Contains(asm, "L") {
		t.Fatal("Darwin listing has no labels at all; the fixture stopped exercising them")
	}
	// Anti-vacuity in the other direction: the ELF path must be unchanged,
	// or this test would pass by the emitter having stopped using .L.
	if n := countOutsideStrings(compile(t, cfiSrc, Options{}), ".L"); n == 0 {
		t.Fatal("the Linux listing lost its .L labels; ELF wants them")
	}
}

// TestDarwinStringLiteralKeepsDotL is the regression the quote-aware scan
// exists for: a Fern program whose STRING contains ".L" must reach the
// listing intact. A blind rewrite corrupts the program's data silently.
func TestDarwinStringLiteralKeepsDotL(t *testing.T) {
	const src = `function main(): i32 { print(".Lnot_a_label"); return 0; }`
	asm := compile(t, src, Options{Darwin: true})
	if !strings.Contains(asm, `.Lnot_a_label`) {
		t.Error("the string literal \".Lnot_a_label\" was rewritten in the Darwin listing — string data is not a symbol")
	}
}

// countOutsideStrings counts occurrences of sub that are not inside a
// double-quoted region, line by line.
func countOutsideStrings(asm, sub string) int {
	n := 0
	for _, line := range strings.Split(asm, "\n") {
		inStr := false
		for i := 0; i < len(line); i++ {
			if inStr {
				if line[i] == '\\' {
					i++
					continue
				}
				if line[i] == '"' {
					inStr = false
				}
				continue
			}
			if line[i] == '"' {
				inStr = true
				continue
			}
			if strings.HasPrefix(line[i:], sub) {
				n++
			}
		}
	}
	return n
}

// TestDarwinCFIAssemblesWithMachOLabels is the payoff, and the reason #8065
// was filed: with ELF's prefix the platform assembler rejects the epilogue
// rule outright. The listing is the emitted Darwin prologue shape; the
// emitter's own output goes through the same assembler in
// TestDarwinEmitsCFI.
func TestDarwinCFIAssemblesWithMachOLabels(t *testing.T) {
	mc, err := exec.LookPath("llvm-mc")
	if err != nil {
		t.Skip("llvm-mc not on PATH")
	}
	const body = `.text
.globl _f
_f:
	.cfi_startproc
	stp x29, x30, [sp, #-16]!
	.cfi_def_cfa_offset 16
	mov x29, sp
	.cfi_def_cfa w29, 16
%sblk_1:
	ldp x29, x30, [sp], #16
	.cfi_def_cfa wsp, 0
	ret
	.cfi_endproc
`
	dir := t.TempDir()
	run := func(prefix string) error {
		p := filepath.Join(dir, "t.s")
		if err := os.WriteFile(p, []byte(strings.Replace(body, "%s", prefix, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		return exec.Command(mc, "-triple=arm64-apple-darwin", "-filetype=obj",
			"-o", filepath.Join(dir, "t.o"), p).Run()
	}
	if err := run("L"); err != nil {
		t.Errorf("llvm-mc rejects the Mach-O local prefix, which is what this change emits: %v", err)
	}
	// The negative half. If this ever starts passing, the premise of #8065
	// is gone and the rewrite is no longer buying anything.
	if err := run(".L"); err == nil {
		t.Error("llvm-mc now ACCEPTS the ELF prefix with CFI; #8065's premise no longer holds and this rewrite needs rejustifying")
	}
}
