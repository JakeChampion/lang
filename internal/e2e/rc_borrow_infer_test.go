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
		ast.BorrowInferEnabled = false
		outOff, exitOff := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = true
		outOn, exitOn := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("borrow inference diverged from owned model:\n owned =(exit %d) %q\n borrow=(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestArm64BorrowInferMatchesOwned(t *testing.T) {
	forEachRunnableFixture(t, "arm64", func(t *testing.T, f *fixtureSpec) {
		prev := ast.BorrowInferEnabled
		ast.BorrowInferEnabled = false
		outOff, exitOff := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = true
		outOn, exitOn := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("borrow inference diverged from owned model:\n owned =(exit %d) %q\n borrow=(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

// wasmBorrowInferKnownDivergent lists fixtures with a KNOWN owned-model
// (borrow-inference-off) RC divergence on wasm, tracked by an issue. They
// still run under the production (borrow-on) model via TestFernFixtures on
// all four backends — only the owned-vs-borrow differential is skipped here.
var wasmBorrowInferKnownDivergent = map[string]bool{
	// #6465: any `dyn Trait` LOCAL traps at scope exit under the owned
	// model — `memory fault at wasm address 0xffffffff`, a [ptr-4] header
	// read with ptr == 0. Not about argument passing: a lone local with no
	// call fails identically. wasm keeps `dyn` as the inline two-word
	// [data, vtable] pair where the natives box it, and the natives are
	// clean, which fits. Older than the case that found it.
	"dyn_trait_dispatch": true,
}

func TestWASMBorrowInferMatchesOwned(t *testing.T) {
	forEachRunnableFixture(t, "wasm", func(t *testing.T, f *fixtureSpec) {
		if wasmBorrowInferKnownDivergent[f.name] {
			t.Skip("known owned-model RC divergence on wasm — #2828")
		}
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = true
		pb := ast.BorrowInferEnabled
		ast.BorrowInferEnabled = false
		outOff, exitOff := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = true
		outOn, exitOn := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.BorrowInferEnabled = pb
		ast.RcFreeEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("borrow inference diverged from owned model:\n owned =(exit %d) %q\n borrow=(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}
