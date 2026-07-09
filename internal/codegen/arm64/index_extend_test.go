package arm64

import (
	"strings"
	"testing"
)

// TestArrayIndexZeroExtendsIndex pins the i32-index zero-extend in
// emitInlineIdxHelper (#4377), the arm64 sibling of the x86-64 guard. The
// index helpers bounds-check the index using the 32-bit `w0` view but compute
// the element address with the full 64-bit `x0` in a shifted `add`. If the
// index carries stale garbage in bits 32..63 (a materialised-constant index
// can — a runtime 32-bit ALU op would have zeroed them), the bounds check
// passes yet the scaled address is wild. `mov w0, w0` zeroes x0's upper 32
// bits so the address matches the checked 32-bit index.
func TestArrayIndexZeroExtendsIndex(t *testing.T) {
	asm := compile(t, `
function main(): i32 {
    var a: i32[] = [10, 20, 30];
    var i: i32 = 1;
    return a[i];
}`, Options{})
	if !strings.Contains(asm, "mov w0, w0") {
		t.Errorf("array index must zero-extend the i32 index (mov w0, w0) before the shifted add; asm:\n%s", asm)
	}
}
