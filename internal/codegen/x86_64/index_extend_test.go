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
// A 32-bit write to `ecx` zeroes the upper 32 bits so the address matches
// the checked 32-bit index: the helper's own `mov ecx, ecx`, or after the
// peephole the index materialised straight into `ecx`. Whichever form
// survives, the last write of the index register before the scaled `lea`
// must be 32 bits wide. Regression guard for the emitter fix.
func TestArrayIndexZeroExtendsIndex(t *testing.T) {
	asm := compile(t, `
function main(): i32 {
    var a: i32[] = [10, 20, 30];
    var i: i32 = 1;
    return a[i];
}`)
	lines := strings.Split(asm, "\n")
	leas := 0
	for i, l := range lines {
		if strings.TrimSpace(l) != "lea rax, [rax + rcx*4]" {
			continue
		}
		leas++
		last := ""
		for k := i - 1; k >= 0; k-- {
			t := strings.TrimSpace(lines[k])
			if strings.HasPrefix(t, "mov rcx, ") || strings.HasPrefix(t, "mov ecx, ") || strings.HasPrefix(t, "pop rcx") {
				last = t
				break
			}
		}
		if !strings.HasPrefix(last, "mov ecx, ") {
			t.Errorf("the index register's last write before the scaled lea is %q; want a 32-bit write of ecx so the upper half is zero; asm:\n%s", last, asm)
		}
	}
	if leas == 0 {
		t.Fatalf("no scaled index lea found; asm:\n%s", asm)
	}
}
