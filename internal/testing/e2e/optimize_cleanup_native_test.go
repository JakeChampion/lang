package e2e

import (
	"testing"
)

var optimizeCleanupCases = []struct {
	name     string
	src      string
	expected int
}{
	{"sub-index-fold",
		`function main(): i32 { let xs: i32[] = [10, 20, 30, 40, 50]; let i: i32 = 5 - 3; return xs[i]; }`, 30},
	{"const-if-and-fold",
		`function main(): i32 { let x: i32 = 100 / 4; if (false) { return 0; } return x + 1; }`, 26},
	{"copyprop",
		`function main(): i32 { let x: i32 = 7; let y: i32 = x; return y * 6; }`, 42},
	{"strength",
		`function main(): i32 { let x: i32 = 21; return x * 2; }`, 42},
	{"loop-not-overfolded",
		`function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 5 + 5) { s = s + i; i = i + 1; } return s; }`, 45},
}

func TestX86_64OptimizeCleanupCorrect(t *testing.T) {
	for _, tc := range optimizeCleanupCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestArm64OptimizeCleanupCorrect(t *testing.T) {
	for _, tc := range optimizeCleanupCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
