package arm64

import (
	"strings"
	"testing"
)

// TestAllocU8ReadsItsSizeAt32Bits pins the width `__alloc_u8` reads its
// argument at. `n` is an i32, so only w0 carries it and bits 32..63 are
// whatever the caller left there; every other consumer in the helper
// (the zero test, the cap and len stores, the zero-fill counter) uses
// the w view, and `__fern_alloc` takes a 64-bit size in x0. Asking it for
// `x19 + 16` bytes hands that garbage to the allocator, which reports the
// arena exhausted on a one-byte request. Reached from `"0".repeat(1)`
// inside coreutils printf on arm64. Every other allocation site in this
// emitter computes its size in w0 (`add w0, w0, #8`, `madd w0, ...`), the
// x86-64 twin does the same (`lea edi, [rbx + 16]`), and the array-index
// helper carries the same guard for the same reason (#4377).
func TestAllocU8ReadsItsSizeAt32Bits(t *testing.T) {
	asm := compile(t, `
function main(): i32 {
    var n: i32 = 3;
    var b: u8[] = __alloc_u8(n);
    return b.len();
}`, Options{})
	body, ok := runtimeHelperBody(asm, "__alloc_u8")
	if !ok {
		t.Fatal("__alloc_u8 was not emitted, so this test proves nothing")
	}
	if strings.Contains(body, "add x0, x19") {
		t.Errorf("__alloc_u8 sizes its allocation from the 64-bit x19; the argument is an i32:\n%s", body)
	}
	if !strings.Contains(body, "add w0, w19, #16") {
		t.Errorf("__alloc_u8 must add its header at 32-bit width so the size reaching __fern_alloc is zero-extended:\n%s", body)
	}
}

// runtimeHelperBody returns the emitted lines of the named runtime
// helper, from its label to the `ret` that ends it.
func runtimeHelperBody(asm, name string) (string, bool) {
	lines := strings.Split(asm, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == name+":" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "ret" {
			return strings.Join(lines[start:i+1], "\n"), true
		}
	}
	return "", false
}
