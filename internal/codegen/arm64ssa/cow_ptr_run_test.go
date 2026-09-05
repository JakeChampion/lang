package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// __fern_arr_cow_inplace_ptr on the arm64 SSA path: copying a SHARED pointer
// array retains every element, so an element both arrays reach is no longer
// unique. The helper was a tail-branch alias of the scalar copy, whose raw
// memcpy left the elements at unchanged count; a consuming match one level
// down then read the shared child as unique and rewrote it in place, and a
// persistent vector's snapshot changed under a `.with`.
//
// Exit code: is_unique(elem0) + 2*(buf == arr). Both terms must be 0 — the
// element is now held twice and the shared array was copied, not returned.
func TestArmRunArrCowInplacePtrRetainsElements(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	arr := callOp(f, e, "__alloc_u8", constOp(f, e, 16)) // 16-byte header, 16 data bytes
	// Two pointer elements: len = cap = 2 at the header's len / cap words.
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -4)), constOp(f, e, 2))
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -12)), constOp(f, e, 2))
	e0 := rcCell(f, e, 8) // fresh rc=1 cells
	e1 := rcCell(f, e, 8)
	f.AddOpNoResult(e, ssa.OpStore, arr, e0)
	f.AddOpNoResult(e, ssa.OpStore, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, 8)), e1)
	callOp(f, e, "__fern_rc_inc", arr) // shared: rc 2
	buf := callOp(f, e, "__fern_arr_cow_inplace_ptr", arr, constOp(f, e, 8))
	unique := callOp(f, e, "__fern_rc_is_unique", e0)
	same := f.AddOp(e, ssa.OpEq, buf, arr)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, unique, f.AddOp(e, ssa.OpMul, same, constOp(f, e, 2))))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 0 {
		t.Errorf("is_unique(elem0) + 2*(buf == arr) = %d, want 0: the copy must retain its elements and be a fresh buffer", got)
	}
}
