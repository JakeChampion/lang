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
