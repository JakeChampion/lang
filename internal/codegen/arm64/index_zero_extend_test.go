package arm64

import (
	"strings"
	"testing"
)

// Index zero-extension (#4377 slice 1b): an array / slice / string index
// is an i32, and an i32 value only carries meaning in its low 32 bits —
// arithmetic overflow, or a folded constant like -1, can leave the high
// 32 bits dirty. The index helper bounds-checks the low 32 bits
// (`cmp w0, w2`) but must not scale the address off the full 64-bit
// register (`add x0, x1, x0, lsl #N`): a dirty high half would slip an
// out-of-range effective index past the check into a wild
// `base + idx*stride`. The scaled add takes the `w0, uxtw` extend form,
// which uses only the zero-extended low 32 bits.
//
// This regressed when ir.Fold was enabled on the native path (see the
// x86-64 sibling test and TestSelfHostTupleElemTag).

// arm64FnBody returns the emitted lines of function `name` (between its
// label and its `.size` directive), for shape assertions.
func arm64FnBody(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+name+":\n")
	if start < 0 {
		t.Fatalf("function %q not found in asm", name)
	}
	rest := asm[start+1:]
	end := strings.Index(rest, ".size "+name)
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func TestArrIndexScalesFromLow32(t *testing.T) {
	asm := compile(t, `function f(a: i32[], i: i32): i32 { return a[i]; }
function main(): i32 { var xs: i32[] = [10, 20, 30]; return f(xs, 1); }`, Options{})
	body := arm64FnBody(t, asm, "f")

	// The scaled index add must use the `uxtw` extend (low 32 bits,
	// zero-extended), matching the 32-bit bounds check above it.
	if !strings.Contains(body, "uxtw") {
		t.Errorf("array index add did not use the `w0, uxtw` extend form:\n%s", body)
	}
	// The vulnerable full-64-bit `lsl`-shifted add on x0 must be gone.
	if strings.Contains(body, "add x0, x1, x0, lsl") {
		t.Errorf("array index still scales the full 64-bit register:\n%s", body)
	}
}

func TestStrIndexScalesFromLow32(t *testing.T) {
	// String byte-indexing shares the same index prologue; its address
	// math has no bounds check, so a dirty high half is just as
	// dangerous. It must also add via the `w0, uxtw` extend.
	asm := compile(t, `function f(s: string, i: i32): i32 { return s[i]; }
function main(): i32 { return f("abc", 1); }`, Options{})
	body := arm64FnBody(t, asm, "f")
	if !strings.Contains(body, "uxtw") {
		t.Errorf("string index add did not use the `w0, uxtw` extend form:\n%s", body)
	}
}
