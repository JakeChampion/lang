package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// noneBoxExtentModule closes a Reader handle over fd 0, which hands back a
// helper-built None box, allocates a probe block right behind it, then frees
// the None the way the IR drops an owned Option[IoError] (16 payload bytes) and
// takes the next block of that class off the freelist. Filling the popped block
// must leave the probe intact: the None box has to own the whole extent its
// class covers. Returns the probe's word, 77 when intact.
func noneBoxExtentModule() map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	handle := rcCell(f, b, 16)
	storeOp(f, b, handle, constOp(f, b, 0), 8) // fd at [handle+8]
	none := addrCallOp(f, b, "__method_Reader_close", handle)
	probe := addrCallOp(f, b, "__alloc", constOp(f, b, 8))
	storeOp(f, b, probe, constOp(f, b, 77), 0)
	addrCallOp(f, b, "__fern_box_free", none, constOp(f, b, 16))
	popped := addrCallOp(f, b, "__alloc", constOp(f, b, 24))
	for off := int64(0); off < 32; off += 8 {
		storeOp(f, b, popped, constOp(f, b, 0), off)
	}
	f.SetRet(b, loadOp(f, b, probe, 0))
	return map[string]*ssa.Func{"main": f}
}

func TestHelperNoneBoxOwnsItsClassExtent(t *testing.T) {
	if got := assembleRunArmModule(t, noneBoxExtentModule(), "main", arm64ssa.DefaultNumAlloc); got != 77 {
		t.Errorf("probe word after refilling the freed None box = %d, want 77 (the None box was shorter than its size class)", got)
	}
}
