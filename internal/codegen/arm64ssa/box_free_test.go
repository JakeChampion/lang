package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// boxFreeModule builds a box the way the IR does, writes through it, releases
// it, and reads the value back THROUGH the pointer the release handed back: the
// IR's OpDrop relies on __fern_box_free returning the data pointer it was given.
// 41 + 8.
func boxFreeModule() map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	data := rcCell(f, b, 8)
	storeOp(f, b, data, constOp(f, b, 41), 0)
	back := addrCallOp(f, b, "__fern_box_free", data, constOp(f, b, 8))
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, loadOp(f, b, back, 0), constOp(f, b, 8)))
	return map[string]*ssa.Func{"main": f}
}

func TestBoxFreeReturnsItsArgument(t *testing.T) {
	for _, n := range []int{2, 4, arm64ssa.DefaultNumAlloc} {
		if got := assembleRunArmModule(t, boxFreeModule(), "main", n); got != 49 {
			t.Errorf("nAlloc=%d box_free identity = %d, want 49", n, got)
		}
	}
}

// freelistReuseModule releases a box and allocates the same class again: the
// second allocation must come back at the first one's base, which is what
// bounds a churning program's heap.
func freelistReuseModule() map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	first := rcCell(f, b, 8)
	addrCallOp(f, b, "__fern_box_free", first, constOp(f, b, 8))
	second := rcCell(f, b, 8)
	f.SetRet(b, f.AddOp(b, ssa.OpSub, second, first))
	return map[string]*ssa.Func{"main": f}
}

func TestFreedBlockIsReused(t *testing.T) {
	if got := assembleRunArmModule(t, freelistReuseModule(), "main", arm64ssa.DefaultNumAlloc); got != 0 {
		t.Errorf("second allocation landed %d bytes from the freed block, want the same base", got)
	}
}
