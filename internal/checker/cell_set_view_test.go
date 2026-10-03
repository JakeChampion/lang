package checker

import (
	"strings"
	"testing"
)

// Cell.set keeps its argument, so a `str` view is not lent to it the way it is
// to a borrowing parameter: it would outlive the bytes it views (#10702).
func TestCellSetRefusesAStrView(t *testing.T) {
	const pre = `function main(): i32 { let b: string = "abcdefgh"; let u: str = slice_unchecked(b, 0, 8); let c: Cell[string] = cell_new(b); `
	err := checkSource(t, pre+`c.set(u); return c.get().len(); }`)
	if err == nil || !strings.Contains(err.Error(), "expected string, got str") {
		t.Fatalf("Cell.set(str): got %v, want an E038 naming str", err)
	}
	if err := checkSource(t, pre+`c.set(b); return c.get().len(); }`); err != nil {
		t.Fatalf("Cell.set(string): %v", err)
	}
}
