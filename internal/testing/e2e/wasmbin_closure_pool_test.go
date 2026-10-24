package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func closurePoolProg(n int) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n    let fs: ((i32) => i32)[] = [")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "((a%d: i32) => a%d + %d)", i, i, i)
	}
	b.WriteString("];\n    let s: i32 = 0;\n    let i: i32 = 0;\n")
	fmt.Fprintf(&b, "    while (i < %d) { s = s + fs[i](1i32); i = i + 1; }\n", n)
	b.WriteString("    return s & 63i32;\n}\n")
	return b.String()
}

const closurePoolCells = 24

func TestWasmbinClosurePoolClearsFreelist(t *testing.T) {
	src := closurePoolProg(closurePoolCells)
	want := runInterpExit(t, src)
	if got := compileAndRunWasmbinMain(t, src); got != want {
		t.Errorf("wasmbin got %d, interp got %d — %d distinct function values "+
			"overflow the closure-cell pool into the freelist heads table\nsrc:\n%s",
			got, want, closurePoolCells, src)
	}
}

func TestWasmbinClosurePoolClearsFreelistX86_64(t *testing.T) {
	src := closurePoolProg(closurePoolCells)
	want := runInterpExit(t, src)
	if _, got := compileAndRunX86_64(t, src); got != want {
		t.Errorf("x86-64 got %d, interp got %d", got, want)
	}
}
