package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A string/array-free struct local whose last use is inside an `if` takes the
// control-flow precise drop rather than the function-exit sweep. The signal: a scalar-only struct's *sweep* drop is emitted INLINE
// (is_unique + box_free), so the generated `__drop_struct_*` helper appears ONLY
// when the precise control-flow placement fires. See safeForControlFlowDrop /
// typeIsStringArrayFree. (A string-containing struct is excluded — verified
// corpus-wide by the free-on == free-off differential gate, since its sweep
// drop also routes through the helper and so isn't distinguishable by op
// presence here.)
func usesCall(ip *ir.Program, fn, substr string) bool {
	f := funcByName(ip, fn)
	for _, op := range f.Ops {
		if op.Kind == ir.OpCallDirect && strings.Contains(op.Str, substr) {
			return true
		}
	}
	return false
}

func TestPreciseDropControlFlowStruct(t *testing.T) {
	// Scalar-only struct, last use inside an if -> precise drop fires (helper
	// present). Without the placement the scalar struct would be swept inline.
	ip := lowerForTest(t, `struct P { x: i32, y: i32 }
function add(p: P): i32 { return p.x + p.y; }
function f(n: i32): i32 { var p: P = P { x: 1, y: 2 }; var c: i32 = 0; if (n > 0) { c = add(p); } return c + n; }
function main(): i32 { return 0; }`)
	if !usesCall(ip, "f", "__drop_struct_") {
		t.Errorf("scalar struct last-used in an if: expected a precise __drop_struct_ placement, found none")
	}
}
