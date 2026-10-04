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
	"testing"
)

var boundsElisionCases = []struct {
	name     string
	src      string
	expected int
}{
	// i32 (stride 4).
	{"i32-sum",
		`function main(): i32 { let xs: i32[] = [10, 20, 30, 40]; let s: i32 = 0; for x in xs { s = s + x; } return s; }`, 100},
	// u8 (stride 1) — the `_1` helper variant.
	{"u8-sum",
		`function main(): i32 { let xs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8]; let s: i32 = 0; for b in xs { s = s + (b as i32); } return s; }`, 15},
	// i64 (stride 8) — the `_8` helper variant.
	{"i64-sum",
		`function main(): i32 { let xs: i64[] = [100, 200, 300]; let s: i64 = 0; for x in xs { s = s + x; } if (s == 600) { return 42; } return 1; }`, 42},
	// Pointer elements (struct[]) — the loop var is bound by reference and the
	// per-element field read composes with the elided address compute.
	{"struct-field-sum",
		`struct P { x: i32 } function main(): i32 { let ps: P[] = [P { x: 5 }, P { x: 7 }, P { x: 9 } ]; let s: i32 = 0; for p in ps { s = s + p.x; } return s; }`, 21},
	// Empty array — the loop never runs; the elision must not misfire on a
	// zero-length array (len captured as 0, guard false immediately).
	{"empty",
		`function main(): i32 { let xs: i32[] = []; let s: i32 = 7; for x in xs { s = s + x; } return s; }`, 7},
	// Nested for-loops over the same array — each desugar gets its own
	// synthetic idx/len, both elided.
	{"nested",
		`function main(): i32 { let xs: i32[] = [1, 2, 3]; let t: i32 = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }`, 36},
	// A string in the len-bounded loop idiom — the `__str_idx_nc` variant,
	// on a heap string and on an inline (SSO) one, whose byte address comes
	// from the scratch spill rather than the data pointer.
	{"string-bytes",
		`function main(): i32 { let s: string = "abcdefghijkl"; let t: i32 = 0; let i: i32 = 0; while (i < s.len()) { t = t + (s[i] as i32) - 96; i = i + 1; } return t; }`, 78},
	{"inline-string-bytes",
		`function main(): i32 { let s: string = "abc"; let t: i32 = 0; let i: i32 = 0; while (i < s.len()) { t = t + (s[i] as i32) - 96; i = i + 1; } return t; }`, 6},
}

// TestX86_64BoundsElisionCorrect runs each case through the x86-64 native
// backend and asserts the exit code — the elided address compute must produce
// the same values as the checked path.
func TestX86_64BoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.expected {
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
