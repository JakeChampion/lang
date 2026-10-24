package e2e

import (
	"testing"
)

var boundsElisionCases = []struct {
	name     string
	src      string
	expected int
}{
	{"i32-sum",
		`function main(): i32 { let xs: i32[] = [10, 20, 30, 40]; let s: i32 = 0; for x in xs { s = s + x; } return s; }`, 100},
	{"u8-sum",
		`function main(): i32 { let xs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8]; let s: i32 = 0; for b in xs { s = s + (b as i32); } return s; }`, 15},
	{"i64-sum",
		`function main(): i32 { let xs: i64[] = [100, 200, 300]; let s: i64 = 0; for x in xs { s = s + x; } if (s == 600) { return 42; } return 1; }`, 42},
	{"struct-field-sum",
		`struct P { x: i32 } function main(): i32 { let ps: P[] = [P { x: 5 }, P { x: 7 }, P { x: 9 } ]; let s: i32 = 0; for p in ps { s = s + p.x; } return s; }`, 21},
	{"empty",
		`function main(): i32 { let xs: i32[] = []; let s: i32 = 7; for x in xs { s = s + x; } return s; }`, 7},
	{"nested",
		`function main(): i32 { let xs: i32[] = [1, 2, 3]; let t: i32 = 0; for a in xs { for b in xs { t = t + a * b; } } return t; }`, 36},
	{"string-bytes",
		`function main(): i32 { let s: string = "abcdefghijkl"; let t: i32 = 0; let i: i32 = 0; while (i < s.len()) { t = t + (s[i] as i32) - 96; i = i + 1; } return t; }`, 78},
	{"inline-string-bytes",
		`function main(): i32 { let s: string = "abc"; let t: i32 = 0; let i: i32 = 0; while (i < s.len()) { t = t + (s[i] as i32) - 96; i = i + 1; } return t; }`, 6},
}

func TestX86_64BoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestArm64BoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestWASMBoundsElisionCorrect(t *testing.T) {
	for _, tc := range boundsElisionCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.expected {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
