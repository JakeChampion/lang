package x86_64

import (
	"strings"
	"testing"
)

// TestArrayIndexZeroExtendsIndex pins the i32-index zero-extend in
// emitInlineIdxHelper (#4377). The array/slice/string index helpers bounds-
// check the index using the 32-bit `ecx` view but compute the element address
// with the full 64-bit `rcx` in a scaled `lea`. If the index carries stale
// garbage in bits 32..63 (which a materialised-constant index can — a runtime
// 32-bit ALU op would have zeroed them, but a folded constant load does not),
// the bounds check passes yet the scaled address is wild → out-of-bounds read.
// The `mov ecx, ecx` zeroes the upper 32 bits so the address matches the
// checked 32-bit index. Regression guard for the emitter fix.
func TestArrayIndexZeroExtendsIndex(t *testing.T) {
	asm := compile(t, `
function main(): i32 {
    var a: i32[] = [10, 20, 30];
    var i: i32 = 1;
    return a[i];
}`)
	if !strings.Contains(asm, "mov ecx, ecx") {
		t.Errorf("array index must zero-extend the i32 index (mov ecx, ecx) before the scaled lea; asm:\n%s", asm)
	}
}
