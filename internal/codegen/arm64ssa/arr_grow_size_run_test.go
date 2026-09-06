package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The grow guard fires on the REQUEST, before anything is allocated or copied:
// a header claiming 2^30 byte elements at full capacity sends
// __fern_arr_push_grow down the copy path, where the doubled request is
// 2^31 + 18 bytes (#8587). Nothing behind the header exists, so this touches
// no memory beyond the 16-byte block.
func TestArmRunArrPushGrowRefusesOverflowedRequest(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	arr := addrCallOp(f, e, "__alloc_u8", constOp(f, e, 16))                                                // 16-byte header, 16 data bytes
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -4)), constOp(f, e, 1<<30))  // len
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -12)), constOp(f, e, 1<<30)) // cap
	buf := addrCallOp(f, e, "__fern_arr_push_grow", arr, constOp(f, e, 1<<30), constOp(f, e, 1))
	f.SetRet(e, f.AddOp(e, ssa.OpEq, buf, arr))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 134 {
		t.Errorf("grow of a 2^30-byte array exited %d, want 134 (allocation size refused)", got)
	}
}
