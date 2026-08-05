package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// The static closure-cell pool must not alias the wasmbin allocator's
// freelist heads table (#6142).
//
// Every unique function value a program takes gets an 8-byte
// { fn_idx, env_ptr=0 } cell in a static pool. That pool used to start at
// address 96 and was budgeted all the way to 1024 — the same window the
// segregated freelist's heads table owns from 256 up. So from the 21st cell
// on, the two wrote over each other in both directions: the cells' data
// segment left function indices sitting in the heads table (the next
// same-class allocation popped one as a free block and returned a pointer
// into reserved low memory, which the program then wrote through, taking
// the bump cursor with it), and conversely a free stored a heap pointer into
// a slot a later `call_indirect` read back as a function index.
//
// It surfaced as a trap deep inside the allocator following a corrupt head,
// which is why the count of DISTINCT lambdas is the thing that matters here
// and the shape of the program is not. Twenty-four is comfortably past the
// old boundary; interp is the oracle.
func closurePoolProg(n int) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n    var fs: ((i32) => i32)[] = [")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "((a%d: i32) => a%d + %d)", i, i, i)
	}
	b.WriteString("];\n    var s: i32 = 0;\n    var i: i32 = 0;\n")
	fmt.Fprintf(&b, "    while (i < %d) { s = s + fs[i](1i32); i = i + 1; }\n", n)
	b.WriteString("    return s & 63i32;\n}\n")
	return b.String()
}

// closurePoolCells is a cell count past the old 20-cell boundary — the whole
// point of the case, so it is named rather than left as a literal.
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

// The same program on the backends that were already correct, so a failure
// above is attributable to wasmbin's layout rather than to the program.
func TestWasmbinClosurePoolClearsFreelistX86_64(t *testing.T) {
	src := closurePoolProg(closurePoolCells)
	want := runInterpExit(t, src)
	if _, got := compileAndRunX86_64(t, src); got != want {
		t.Errorf("x86-64 got %d, interp got %d", got, want)
	}
}
