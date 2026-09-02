package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// boxFreeModule allocates a cell, writes through it, releases it, and reads the
// value back THROUGH the pointer the release handed back — so the identity the
// elision relies on is what the answer depends on. 41 + 8.
func boxFreeModule() map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	cell := f.AddOp(b, ssa.OpAlloc, constOp(f, b, 8))
	storeOp(f, b, cell, constOp(f, b, 41), 0)
	back := addrCallOp(f, b, "__fern_box_free", cell, constOp(f, b, 8))
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, loadOp(f, b, back, 0), constOp(f, b, 8)))
	return map[string]*ssa.Func{"main": f}
}

// A call into a body that is a bare `ret` is a `bl`, a `ret` and the argument
// setup around them, for nothing. Compiled code stops making it.
func TestNoOpHelperCallIsElided(t *testing.T) {
	asm := emitIdxAsm(t, boxFreeModule(), "main")
	if strings.Contains(asm, "bl "+"fn___fern_box_free") {
		t.Error("a do-nothing helper is still called from compiled code")
	}
}

// The body stays, though: __fern_closure_drop tail-branches to it, and that
// branch is not a call site this pass can see or should touch.
func TestNoOpHelperBodySurvivesForItsHelperCallers(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	cell := f.AddOp(b, ssa.OpAlloc, constOp(f, b, 8))
	addrCallOp(f, b, "__fern_closure_drop", cell)
	f.SetRet(b, constOp(f, b, 0))

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if !strings.Contains(asm, "\nfn___fern_box_free:") {
		t.Error("closure_drop's tail-call target was dropped with the call sites")
	}
	if !strings.Contains(asm, "b "+"fn___fern_box_free") {
		t.Error("closure_drop no longer reaches box_free")
	}
}

// And it still computes what the call computed: the pointer it was handed.
func TestNoOpHelperCallReturnsItsArgument(t *testing.T) {
	for _, n := range []int{2, 4, arm64ssa.DefaultNumAlloc} {
		if got := assembleRunArmModule(t, boxFreeModule(), "main", n); got != 49 {
			t.Errorf("nAlloc=%d box_free identity = %d, want 49", n, got)
		}
	}
}
