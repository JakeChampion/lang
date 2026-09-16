package arm64ssa

import (
	"fmt"
	"strings"
	"testing"

	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
)

// calleeSavedIn counts a register at either width and never inside a longer
// word: a label carrying a register name and a helper symbol are not uses.
func TestCalleeSavedInTokenisesRegisterNames(t *testing.T) {
	asm := "fn_x20_helper:\n\tldr w19, [sp, #8]\n\tb .Lx21_done\n\tadd x22, x22, #1\n\tbl __fern_x20\n\tmov x0, 4x21z\n"
	want := []int{armIndex(t, "x19"), armIndex(t, "x22")}
	if got := calleeSavedIn(asm, len(armX)); !equalInts(got, want) {
		t.Fatalf("calleeSavedIn = %v, want %v", got, want)
	}
	// Registers outside the program's file are not allocatable, so a mention of
	// one cannot be this function's.
	if got := calleeSavedIn(asm, armIndex(t, "x22")); !equalInts(got, want[:1]) {
		t.Fatalf("calleeSavedIn with a %d-register file = %v, want %v", armIndex(t, "x22"), got, want[:1])
	}
	if got := calleeSavedIn("", len(armX)); len(got) != 0 {
		t.Fatalf("calleeSavedIn(empty) = %v, want none", got)
	}
	// The marker the body carries is not a register mention.
	if got := calleeSavedIn(teardownMarker+"\n", len(armX)); len(got) != 0 {
		t.Fatalf("calleeSavedIn(marker) = %v, want none", got)
	}
}

// A parameter homed in a callee-saved register is a use even when no block
// mentions it, so the parameter moves have to reach the scan.
func TestParamMoveHomesReachTheScan(t *testing.T) {
	home := armIndex(t, "x21")
	fr := frameLayout{}.withSaves(false, 0)
	lines := paramMoveLines([]x86.Loc{{IsReg: true, Reg: home}}, fr, len(armX)-1)
	if len(lines) == 0 {
		t.Fatal("a parameter homed away from its argument register emitted no move")
	}
	got := calleeSavedIn(strings.Join(lines, "\n"), len(armX))
	if !equalInts(got, []int{home}) {
		t.Fatalf("calleeSavedIn(%q) = %v, want [%d]", strings.Join(lines, "\n"), got, home)
	}
}

// Rendering the parameter moves against a provisional frame and against the
// final one differs in immediates only, which is what lets the scan run on the
// provisional render.
func TestParamMoveRegistersDoNotMoveWithTheFrame(t *testing.T) {
	locs := []x86.Loc{
		{IsReg: true, Reg: armIndex(t, "x21")},
		{IsReg: false, Slot: 3},
		{IsReg: true, Reg: armIndex(t, "x19")},
	}
	for k := 0; k < 4; k++ { // a stack-passed parameter, whose offset is the frame top
		locs = append(locs, x86.Loc{IsReg: true, Reg: armIndex(t, "x20")})
	}
	base := frameLayout{outArgs: 2}
	base.slotBase = base.outArgs
	base.callSaveBase = base.slotBase + 4
	base.csBase = base.callSaveBase + 2
	provisional := paramMoveLines(locs, base.withSaves(true, 0), len(armX)-1)
	final := paramMoveLines(locs, base.withSaves(true, 5), len(armX)-1)
	if len(provisional) != len(final) {
		t.Fatalf("%d provisional lines against %d final ones", len(provisional), len(final))
	}
	if got, want := calleeSavedIn(strings.Join(final, "\n"), len(armX)), calleeSavedIn(strings.Join(provisional, "\n"), len(armX)); !equalInts(got, want) {
		t.Fatalf("final render names %v, provisional names %v", got, want)
	}
}

// writeBody hands the blocks to the writer a line at a time and replaces every
// marker with the teardown. Blank lines survive.
func TestWriteBodyReplacesMarkersWithTeardown(t *testing.T) {
	body := "\tmov x0, x1\n" + teardownMarker + "\n\tret\n\n\tmov x2, #1\n" + teardownMarker + "\n\tret\n"
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...); b.WriteByte('\n') }
	writeBody(w, body, []string{"ldr x30, [sp, #16]", "add sp, sp, #32"})
	want := "\tmov x0, x1\n\tldr x30, [sp, #16]\n\tadd sp, sp, #32\n\tret\n\n\tmov x2, #1\n\tldr x30, [sp, #16]\n\tadd sp, sp, #32\n\tret\n"
	if got := b.String(); got != want {
		t.Fatalf("writeBody =\n%q\nwant\n%q", got, want)
	}
}

func armIndex(t *testing.T, name string) int {
	t.Helper()
	r, ok := armRegByName[name]
	if !ok {
		t.Fatalf("no register named %q", name)
	}
	return r
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
