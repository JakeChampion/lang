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

// The handle forms of the same two, `r.window_size()` and
// `r.set_window_size(rows, cols)` (#9363): two instructions each, the fd out
// of the box and a branch into the helper above. The termios pair has no
// handle form here for the reason it has no free form —
// docs/BACKEND-PARITY.md.
//
// The receiver is a handle box over fd 1, this process's pipe, so both answer
// Err. A stub reading the fd from the wrong offset would pass a pointer as a
// descriptor and answer EBADF — still an Err, so the case cannot tell those
// apart, and what it does prove is that the symbol exists, assembles and
// returns a well-formed box rather than faulting.
func TestArmRunHandleWindowSizeOnAPipeIsErr(t *testing.T) {
	for _, m := range []struct {
		name string
		args int
	}{
		{"__method_Reader_window_size", 0},
		{"__method_Reader_set_window_size", 2},
	} {
		f := ssa.NewFunc("main")
		b := f.NewBlock()
		handle := rcCell(f, b, 16)
		storeOp(f, b, handle, constOp(f, b, 1), 8) // fd at [handle+8]
		var box ssa.Value
		if m.args == 0 {
			box = addrCallOp(f, b, m.name, handle)
		} else {
			box = addrCallOp(f, b, m.name, handle, constOp(f, b, 40), constOp(f, b, 100))
		}
		f.SetRet(b, loadOp(f, b, box, 0))
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc); got != 1 {
			t.Errorf("%s over fd 1 returned tag %d, want 1 (Err)", m.name, got)
		}
	}
}
