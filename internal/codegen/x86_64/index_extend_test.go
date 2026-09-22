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
		// The scaled address: the helper's `lea`, or the load P13 folded
		// it into.
		if !strings.HasSuffix(strings.TrimSpace(l), "[rax + rcx*4]") {
			continue
		}
		leas++
		// The last write of the index register under any spelling — a
		// mov, a pop, a sign extension, an address, an ALU op — must be
		// a 32-bit one.
		last := ""
		for k := i - 1; k >= 0; k-- {
			t := strings.TrimSpace(lines[k])
			if writesIndexReg(t) {
				last = t
				break
			}
		}
		if !strings.HasPrefix(last, "mov ecx, ") {
			t.Errorf("the index register's last write before the scaled lea is %q; want a 32-bit write of ecx so the upper half is zero; asm:\n%s", last, asm)
		}
	}
	if leas == 0 {
		t.Fatalf("no scaled index address found; asm:\n%s", asm)
	}
}

// writesIndexReg reports whether an instruction's destination is rcx under
// any of its names: the first operand is `rcx`, `ecx`, `cx` or `cl` and the
// instruction is not a compare, which reads its first operand, or the
// instruction is a pop into rcx.
func writesIndexReg(insn string) bool {
	if insn == "pop rcx" {
		return true
	}
	sp := strings.IndexByte(insn, ' ')
	if sp < 0 {
		return false
	}
	switch insn[:sp] {
	case "cmp", "test", "bt":
		return false
	}
	dst := strings.TrimSuffix(strings.TrimSpace(insn[sp:]), ",")
	if c := strings.IndexByte(dst, ','); c >= 0 {
		dst = dst[:c]
	}
	switch dst {
	case "rcx", "ecx", "cx", "cl":
		return true
	}
	return false
}
