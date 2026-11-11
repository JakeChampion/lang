package e2ecompiler

import (
	"strings"
	"testing"
)

// A conditional branch reaches 1 MiB, and a test-bit branch 32 KiB. The
// assembler gives a branch with a farther target the inverted branch over an
// unconditional one, which is the sequence the GNU source spells out.
func TestSelfHostArm64AsmFarConditionalBranch(t *testing.T) {
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)

	gap := strings.Repeat("    nop\n", 270000)
	const head = ".text\n.globl _start\n_start:\nLback:\n"
	short := head + gap +
		"    b.eq Lback\n    cbz x1, Lback\n    cbnz w2, Lback\n    tbz x3, #5, Lback\n    tbnz w4, #31, Lback\n" +
		"    b.ne Lnear\n" +
		"    b.lt Lfwd\n    cbz w5, Lfwd\n    cbnz x6, Lfwd\n    tbz w7, #0, Lfwd\n    tbnz x8, #63, Lfwd\n" +
		"Lnear:\n" + gap + "Lfwd:\n    ret\n"
	long := head + gap +
		"    b.ne 1f\n    b Lback\n1:\n    cbnz x1, 1f\n    b Lback\n1:\n    cbz w2, 1f\n    b Lback\n1:\n" +
		"    tbnz x3, #5, 1f\n    b Lback\n1:\n    tbz w4, #31, 1f\n    b Lback\n1:\n" +
		"    b.ne Lnear\n" +
		"    b.ge 1f\n    b Lfwd\n1:\n    cbnz w5, 1f\n    b Lfwd\n1:\n    cbz x6, 1f\n    b Lfwd\n1:\n" +
		"    tbnz w7, #0, 1f\n    b Lfwd\n1:\n    tbz x8, #63, 1f\n    b Lfwd\n1:\n" +
		"Lnear:\n" + gap + "Lfwd:\n    ret\n"

	got := assembleSelfHost(t, buildAsmBenchDriver(t, gcc), runner, short)
	want := arm64Words(gas.text(t, long))
	if len(got) != len(want) {
		t.Fatalf("word count differs: self-host %d, GNU as %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("word %d: self-host %08x, GNU as %08x", i, got[i], want[i])
		}
	}
}
