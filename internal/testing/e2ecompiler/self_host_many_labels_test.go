package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The label table of each assembler grows with its labels. A program with
// more labels than four for each first bucket crosses that growth, and each
// branch and call must still find its label.
func TestSelfHostManyLabelsAssemble(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "many.fern")
	const n = 6000
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "@noinline function step%d(x: i32): i32 {\n    if (x > %d) { return x - 1; }\n    return x + 1;\n}\n", i, i)
	}
	b.WriteString("function main(): i32 {\n    let t: i32 = 0;\n")
	for _, i := range []int{0, 1, n / 2, n - 2, n - 1} {
		fmt.Fprintf(&b, "    t = t + step%d(%d) - %d;\n", i, i, i)
	}
	fmt.Fprintf(&b, "    return t + step%d(%d) - %d;\n}\n", n-1, n, n)
	if err := os.WriteFile(src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, "many-"+tg.target)
		h.compileWith(t, tg, src, bin)
		// Five calls give x + 1, and the last gives x - 1.
		if _, code := h.runProduced(t, tg, bin); code != 4 {
			t.Errorf("%s: exit = %d, want 4", tg.target, code)
		}
	}
}
