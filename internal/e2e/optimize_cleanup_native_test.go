// #4377 slice 1b: OptimizeCleanup (copyprop + constprop + Fold + strength)
// runs on every backend. These shapes pin that its results stay correct on the
// native targets; the folding itself is pinned on the self-host's emitted text
// by TestSelfHostConstExprFoldShape (internal/e2eselfhost), and the
// Fold-miscompile canary is TestSelfHostTupleElemTag.
package e2e

import (
	"testing"
)

var optimizeCleanupCases = []struct {
	name     string
	src      string
	expected int
}{
	// Arithmetic fold (the OpSub shape that exposed the index-truncation bug —
	// a subtraction feeding an array index must stay upper-bits-clean).
	{"sub-index-fold",
		`function main(): i32 { let xs: i32[] = [10, 20, 30, 40, 50]; let i: i32 = 5 - 3; return xs[i]; }`, 30},
	// Const-if pruning + fold together.
	{"const-if-and-fold",
		`function main(): i32 { let x: i32 = 100 / 4; if (false) { return 0; } return x + 1; }`, 26},
	// Copy propagation: y is a pure copy of x, both fold away.
	{"copyprop",
		`function main(): i32 { let x: i32 = 7; let y: i32 = x; return y * 6; }`, 42},
	// Strength reduction shape: *2 → shift, then folded with a constant.
	{"strength",
		`function main(): i32 { let x: i32 = 21; return x * 2; }`, 42},
	// A loop whose bound folds — makes sure the fixpoint doesn't miscompile a
	// live loop induction variable (must stay dynamic, not folded to a const).
	{"loop-not-overfolded",
		`function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 5 + 5) { s = s + i; i = i + 1; } return s; }`, 45},
}

// TestX86_64OptimizeCleanupCorrect runs each shape through the x86-64 native
// backend with the cleanup fixpoint active and asserts the exit code.
func TestX86_64OptimizeCleanupCorrect(t *testing.T) {
	for _, tc := range optimizeCleanupCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestArm64OptimizeCleanupCorrect runs the same shapes through arm64 (the
// default target).
func TestArm64OptimizeCleanupCorrect(t *testing.T) {
	for _, tc := range optimizeCleanupCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
