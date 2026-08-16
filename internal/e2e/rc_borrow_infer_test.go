package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Borrow inference differential gate (docs/OWNERSHIP-INFERENCE-PLAN.md). With
// owned-by-default ON, flipping borrow inference (a non-escaping parameter is
// kept BORROWED — the caller skips the retain inc, the callee skips the exit
// dec) must produce byte-identical OUTPUT whether it's on or off: the elided
// inc/dec pair is exactly balanced on a value that cannot escape the call frame,
// so it can only change rc traffic, never observable behaviour. Any divergence
// (over-release / leak that surfaces) fails here, on the whole fixture corpus,
// across all three backends.

func TestX86_64BorrowInferMatchesOwned(t *testing.T) {
	forEachRunnableFixture(t, "x86_64", func(t *testing.T, f *fixtureSpec) {
		prev := ast.BorrowInferEnabled
		defer func() { ast.BorrowInferEnabled = prev }()
		ast.BorrowInferEnabled = false
		outOff, exitOff := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = true
		outOn, exitOn := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("borrow inference diverged from owned model:\n owned =(exit %d) %q\n borrow=(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestArm64BorrowInferMatchesOwned(t *testing.T) {
	forEachRunnableFixture(t, "arm64", func(t *testing.T, f *fixtureSpec) {
		prev := ast.BorrowInferEnabled
		defer func() { ast.BorrowInferEnabled = prev }()
		ast.BorrowInferEnabled = false
		outOff, exitOff := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = true
		outOn, exitOn := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("borrow inference diverged from owned model:\n owned =(exit %d) %q\n borrow=(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestWASMBorrowInferMatchesOwned(t *testing.T) {
	forEachRunnableFixture(t, "wasm", func(t *testing.T, f *fixtureSpec) {
		prev := ast.RcFreeEnabled
		defer func() { ast.RcFreeEnabled = prev }()
		ast.RcFreeEnabled = true
		pb := ast.BorrowInferEnabled
		defer func() { ast.BorrowInferEnabled = pb }()
		ast.BorrowInferEnabled = false
		outOff, exitOff := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = true
		outOn, exitOn := runFixtureWasm(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("borrow inference diverged from owned model:\n owned =(exit %d) %q\n borrow=(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}
