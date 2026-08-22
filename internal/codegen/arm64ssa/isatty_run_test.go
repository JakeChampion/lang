package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// `isatty(fd)` is the primitive `std/cli`'s colour gate consults, so the SSA
// backend has to emit its runtime helper like every other syscall-shaped
// builtin. Without it a program that colourises links against an undefined
// `fn_isatty` — which is what a missing entry in the helper table looks like.
//
// The test's own stdout is a pipe, so the answer is 0. That is the assertion
// that carries signal: an unlinked or misassembled helper leaves a raw
// -errno (or a fault) rather than the normalised 0.
func TestArmRunIsattyOnAPipeIsFalse(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, callOp(f, e, "isatty", constOp(f, e, 1)))
	if got := assembleRunArm(t, f, 2); got != 0 {
		t.Errorf("isatty(1) with stdout on a pipe = %d, want 0 (not a terminal)", got)
	}
}
