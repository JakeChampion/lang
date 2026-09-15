package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// `set_window_size(fd, rows, cols)` is the write half of `window_size`, and
// this backend emits both — three scalar operands and no array, so the gap
// `termios_get` has here (docs/BACKEND-PARITY.md) does not apply.
//
// The test's own stdout is a pipe, so the first ioctl answers ENOTTY and the
// box comes back Err. That is the assertion that carries signal: an unlinked
// or misassembled helper leaves a fault or a raw -errno rather than a box
// whose tag word is 1, and a helper that skipped straight to TIOCSWINSZ
// would still have to refuse — so the case pins the refusal, not the
// request number.
func TestArmRunSetWindowSizeOnAPipeIsErr(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	box := addrCallOp(f, b, "set_window_size",
		constOp(f, b, 1), constOp(f, b, 40), constOp(f, b, 100))
	f.SetRet(b, loadOp(f, b, box, 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc); got != 1 {
		t.Errorf("set_window_size(1, 40, 100) with stdout on a pipe returned tag %d, want 1 (Err)", got)
	}
}
