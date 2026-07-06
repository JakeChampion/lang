package x86_64

import (
	"strings"
	"testing"
)

// Index zero-extension (#4377 slice 1b): an array / slice / string index
// is an i32, and an i32 value only carries meaning in its low 32 bits —
// arithmetic overflow, or a folded constant like -1 loaded with
// `mov eax, imm` (zero-extending), can leave the high 32 bits dirty. The
// index helper bounds-checks the low 32 bits (`cmp ecx, edx`) but scales
// the address off the full 64-bit register (`lea rax, [rax + rcx*N]`), so
// a dirty high half would slip an out-of-range effective index past the
// check into a wild `base + idx*stride` dereference. The emitter must
// canonicalise the index to 32 bits (`mov ecx, ecx`) before scaling.
//
// This regressed when ir.Fold was enabled on the native path: it folds
// `0 - 1` to a single i32 const, which the native driver then loaded
// zero-extended and incremented into a value whose low 32 bits passed the
// bounds check while the full width scaled off the map — a self-host
// driver segfault (TestSelfHostTupleElemTag).

func TestArrIndexZeroExtendsBeforeScaling(t *testing.T) {
	asm := compile(t, `function f(a: i32[], i: i32): i32 { return a[i]; }
function main(): i32 { var xs: i32[] = [10, 20, 30]; return f(xs, 1); }`)
	body := fnBody(t, asm, "f")

	ze := strings.Index(body, "mov ecx, ecx")
	if ze < 0 {
		t.Fatalf("array index not zero-extended (no `mov ecx, ecx`):\n%s", body)
	}
	// The truncation must precede the scaled address computation; a
	// zero-extend that lands after the `lea` would not protect it.
	scale := strings.Index(body, "rcx*")
	if scale < 0 {
		t.Fatalf("expected a scaled index (`rcx*N`) in the index helper:\n%s", body)
	}
	if ze > scale {
		t.Errorf("index zero-extend must precede the scaled `lea`; got ze=%d scale=%d:\n%s", ze, scale, body)
	}
}

func TestStrIndexZeroExtends(t *testing.T) {
	// String byte-indexing shares the same index prologue; its address
	// math (`base + idx`) has no bounds check, so a dirty high half is
	// just as dangerous. The canonicalising `mov ecx, ecx` must be there.
	asm := compile(t, `function f(s: string, i: i32): i32 { return s[i]; }
function main(): i32 { return f("abc", 1); }`)
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "mov ecx, ecx") {
		t.Errorf("string index not zero-extended (no `mov ecx, ecx`):\n%s", body)
	}
}
