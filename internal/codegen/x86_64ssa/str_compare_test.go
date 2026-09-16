package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// runStrCompare assembles main() = helper(a, b) and returns its exit code
// (the low byte of the result: 255 stands for -1).
func runStrCompare(t *testing.T, helper, a, b string) int {
	t.Helper()
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, callOp(f, e, helper, constStr(f, e, a), constStr(f, e, b)))
	return assembleRun(t, f, 8)
}

// The two string comparisons over every length to 72 — past the 32-byte,
// 16-byte and 8-byte steps of the shared first-difference scan — with the
// strings equal, differing at each position, and one a prefix of the other,
// so a step that compares too few bytes, reports the wrong lane, or reads the
// difference from the wrong side shows up.
func TestAsmRunStringComparisonsAcrossLengths(t *testing.T) {
	for _, n := range scanLengths() {
		a := strings.Repeat("a", n)
		if got := runStrCompare(t, "__str_eq", a, a); got != 1 {
			t.Errorf("len %d: equal strings compare as %d, want 1", n, got)
		}
		if got := runStrCompare(t, "__str_ord", a, a); got != 0 {
			t.Errorf("len %d: equal strings order as %d, want 0", n, got)
		}
		for pos := 0; pos < n; pos++ {
			b := a[:pos] + "b" + a[pos+1:]
			if got := runStrCompare(t, "__str_eq", a, b); got != 0 {
				t.Errorf("len %d, differing at %d: compare as %d, want 0", n, pos, got)
			}
			if got := runStrCompare(t, "__str_ord", a, b); got != 255 {
				t.Errorf("len %d, differing at %d: a orders against b as %d, want -1", n, pos, got)
			}
			if got := runStrCompare(t, "__str_ord", b, a); got != 1 {
				t.Errorf("len %d, differing at %d: b orders against a as %d, want 1", n, pos, got)
			}
			prefix := a[:pos]
			if got := runStrCompare(t, "__str_eq", a, prefix); got != 0 {
				t.Errorf("len %d, prefix of %d: compare as %d, want 0", n, pos, got)
			}
			if got := runStrCompare(t, "__str_ord", a, prefix); got != n-pos {
				t.Errorf("len %d, prefix of %d: a orders against its prefix as %d, want %d", n, pos, got, n-pos)
			}
		}
	}
}

// Each of __ssa_mismatch's three vector loops requires exactly the bytes it
// then consumes: a guard below the stride would read past the shorter
// string, which does not reliably fault, so a functional sweep cannot be the
// proof. The constants are read off the emitted text instead.
func TestMismatchGuardsMatchStrides(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, callOp(f, e, "__str_eq", constStr(f, e, "banana"), constStr(f, e, "bandana")))
	asm, err := EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, loop := range []struct{ start, end, load, cursor string }{
		{".Lssa_mm_avx:", ".Lssa_mm_hit32:", "vmovdqu", "add rcx, "},
		{".Lssa_mm_vec:", ".Lssa_mm_hit16:", "movdqu", "add rcx, "},
		{".Lssa_mm_word:", ".Lssa_mm_hit8:", "mov r8, [rdi + rcx]", "add rcx, "},
	} {
		start, end := strings.Index(asm, loop.start), strings.Index(asm, loop.end)
		if start < 0 || end < start {
			t.Fatalf("no body between %s and %s in the emitted module", loop.start, loop.end)
		}
		body := asm[start:end]
		if !strings.Contains(body, loop.load) {
			t.Fatalf("the %s loop has no %s load, so this test checked nothing", loop.start, loop.load)
		}
		guard := operandAfter(t, body, "cmp rax, ")
		stride := operandAfter(t, body, loop.cursor)
		if guard != stride {
			t.Errorf("__ssa_mismatch's %s loop requires %s bytes before a block but advances %s: "+
				"requiring fewer than it consumes reads past the end of the shorter string\n%s",
				loop.start, guard, stride, body)
		}
	}
}
