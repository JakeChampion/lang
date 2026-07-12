// #4377 slice 1b: OptimizeCleanup (copyprop + constprop + Fold + strength)
// now runs on the native x86-64 / arm64 backends, not just wasm. The Fold
// emitter crash that used to block it is fixed (the array-index zero-extend),
// and the fixpoint's old up-to-8× whole-program convergence snapshot is gone
// (each sub-pass reports a changed bool), so it no longer balloons self-host
// build time.
//
// These pin the observable effect on native: constant expressions fold to a
// single immediate (the intermediate arithmetic vanishes), and results stay
// correct. The Fold-miscompile canary is TestSelfHostTupleElemTag
// (internal/e2eselfhost), which now passes with the pass enabled.
package e2e

import (
	"strings"
	"testing"
)

// TestX86_64OptimizeCleanupFolds pins that a pure-constant expression is
// folded at the IR level — the emitted main body carries the folded immediate
// and NOT the intermediate `imul`/`add` that computed it.
func TestX86_64OptimizeCleanupFolds(t *testing.T) {
	// 2 + 3 * 4 == 14, computable entirely at compile time.
	src := `function main(): i32 { var x: i32 = 2 + 3 * 4; return x; }`
	asm := mainBody(compileToX86Asm(t, src))
	if strings.Contains(asm, "imul") {
		t.Errorf("constant expr 2 + 3*4 was not folded — `imul` survives in main:\n%s", asm)
	}
	// The folded constant 14 must appear as a materialised immediate.
	if !strings.Contains(asm, "14") {
		t.Errorf("folded constant 14 missing from emitted main:\n%s", asm)
	}
}

var optimizeCleanupCases = []struct {
	name     string
	src      string
	expected int
}{
	// Arithmetic fold (the OpSub shape that exposed the index-truncation bug —
	// a subtraction feeding an array index must stay upper-bits-clean).
	{"sub-index-fold",
		`function main(): i32 { var xs: i32[] = [10, 20, 30, 40, 50]; var i: i32 = 5 - 3; return xs[i]; }`, 30},
	// Const-if pruning + fold together.
	{"const-if-and-fold",
		`function main(): i32 { var x: i32 = 100 / 4; if (false) { return 0; } return x + 1; }`, 26},
	// Copy propagation: y is a pure copy of x, both fold away.
	{"copyprop",
		`function main(): i32 { var x: i32 = 7; var y: i32 = x; return y * 6; }`, 42},
	// Strength reduction shape: *2 → shift, then folded with a constant.
	{"strength",
		`function main(): i32 { var x: i32 = 21; return x * 2; }`, 42},
	// A loop whose bound folds — makes sure the fixpoint doesn't miscompile a
	// live loop induction variable (must stay dynamic, not folded to a const).
	{"loop-not-overfolded",
		`function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 5 + 5) { s = s + i; i = i + 1; } return s; }`, 45},
}

// TestX86_64OptimizeCleanupCorrect runs each shape through the x86-64 native
// backend with the cleanup fixpoint active and asserts the exit code.
func TestX86_64OptimizeCleanupCorrect(t *testing.T) {
	for _, tc := range optimizeCleanupCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86Native(t, tc.src); code != tc.expected {
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
