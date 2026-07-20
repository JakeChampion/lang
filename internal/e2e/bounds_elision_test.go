// #4380 lever 3: syntactic bounds-check elision for len-bounded loops.
// The `for x in arr` ForEach desugar produces a synthetic `iter[idx]` element
// read whose index provably stays in range (idx starts at 0, steps +1, the
// loop guard is `idx < iter.len()` captured once, iter/idx are compiler names
// user code can't touch, and Fern arrays never shrink in place). The desugar
// marks that access ast.Index.Unchecked, so the IR routes it to the `_nc`
// helper variant that drops the per-iteration len-load + compare + trap.
//
// These pin BOTH halves: the values stay correct across element strides on
// every backend, AND the emitted x86-64 for-loop no longer contains the
// bounds-check trap that a plain `while (i < n) { a[i] }` index loop keeps.
package e2e

import (
	"strings"
	"testing"
)

var boundsElisionCases = []struct {
	name     string
	src      string
	expected int
}{
	// i32 (stride 4).
	{"i32-sum",
		`function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; var s: i32 = 0; for x in xs { s = s + x; } return s; }`, 100},
	// u8 (stride 1) — the `_1` helper variant.
	{"u8-sum",
		`function main(): i32 { var xs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8]; var s: i32 = 0; for b in xs { s = s + (b as i32); } return s; }`, 15},
	// i64 (stride 8) — the `_8` helper variant.
	{"i64-sum",
		`function main(): i32 { var xs: i64[] = [100, 200, 300]; var s: i64 = 0; for x in xs { s = s + x; } if (s == 600) { return 42; } return 1; }`, 42},
	// Pointer elements (struct[]) — the loop var is bound by reference and the
	// per-element field read composes with the elided address compute.
	{"struct-field-sum",
		`struct P { x: i32 } function main(): i32 { var ps: P[] = [P { x: 5 }, P { x: 7 }, P { x: 9 } ]; var s: i32 = 0; for p in ps { s = s + p.x; } return s; }`, 21},
	// Empty array — the loop never runs; the elision must not misfire on a
	// zero-length array (len captured as 0, guard false immediately).
	{"empty",
		`function main(): i32 { var xs: i32[] = []; var s: i32 = 7; for x in xs { s = s + x; } return s; }`, 7},
	// Nested for-loops over the same array — each desugar gets its own
	// synthetic idx/len, both elided.
	{"nested",
		`function main(): i32 { var xs: i32[] = [1, 2, 3]; var t: i32 = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }`, 36},
}

// TestX86_64BoundsElisionCorrect runs each case through the x86-64 native
// backend and asserts the exit code — the elided address compute must produce
// the same values as the checked path.
func TestX86_64BoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86Native(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestArm64BoundsElisionCorrect runs the same cases through the arm64 backend
// (the `_nc` inline-helper path — arm64 is the default target).
func TestArm64BoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestWASMBoundsElisionCorrect runs the same cases through the wasm backend
// (the `_nc` runtime-helper variants).
func TestWASMBoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.expected {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestX86_64BoundsElisionEmitted pins the optimization itself: a `for x in xs`
// loop and the len-bounded index idiom `while (i < xs.len()) { xs[i] }`
// (elideLenBoundedChecks, #4380 lever 3) must NOT emit the bounds-check trap
// (`mov edi, 134` — the emitArrBoundsCheck exit code), while a loop whose
// guard routes the length through a variable — outside the pass's exact
// syntactic form — MUST keep it. This guards both directions: the desugar /
// `_nc` routing regressing to the checked helper, and the elision pass
// over-firing on shapes it cannot prove.
func TestX86_64BoundsElisionEmitted(t *testing.T) {
	forSrc := `function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; var s: i32 = 0; for x in xs { s = s + x; } return s; }`
	whileSrc := `function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }`
	keptSrc := `function main(): i32 { var xs: i32[] = [10, 20, 30, 40]; var n: i32 = xs.len(); var s: i32 = 0; var i: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`

	forAsm := compileToX86Asm(t, forSrc)
	if n := strings.Count(mainBody(forAsm), "mov edi, 134"); n != 0 {
		t.Errorf("for-loop kept %d bounds-check trap(s); want 0 (elided)\n%s", n, mainBody(forAsm))
	}
	whileAsm := compileToX86Asm(t, whileSrc)
	if n := strings.Count(mainBody(whileAsm), "mov edi, 134"); n != 0 {
		t.Errorf("len-guarded while-index loop kept %d bounds-check trap(s); want 0 (elideLenBoundedChecks)\n%s", n, mainBody(whileAsm))
	}
	keptAsm := compileToX86Asm(t, keptSrc)
	if n := strings.Count(mainBody(keptAsm), "mov edi, 134"); n == 0 {
		t.Errorf("variable-bounded while-index loop dropped its bounds-check trap; want it kept (guard is not syntactically i < xs.len())")
	}
}

// mainBody returns the text of `main:` up to its `.size` directive, so the
// bounds-trap count isn't polluted by other functions' array accesses.
func mainBody(asm string) string {
	i := strings.Index(asm, "\nmain:")
	if i < 0 {
		return asm
	}
	rest := asm[i:]
	if j := strings.Index(rest, ".size main"); j >= 0 {
		return rest[:j]
	}
	return rest
}
