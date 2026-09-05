package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// pushGrowElemModule builds a full two-element pointer array (cap = len = 2)
// holding two fresh rc=1 cells, bumps its rc to `arrRc`, appends through
// `helper`, and returns is_unique(elem0) + 2*(buf == arr). A grow past
// capacity always copies, so buf != arr and the second term is 0; the first
// term is 0 iff the copy retained the element it now shares with the old
// buffer.
func pushGrowElemModule(helper string, arrRc int) map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	arr := addrCallOp(f, e, "__alloc_u8", constOp(f, e, 16)) // 16-byte header, 16 data bytes
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -4)), constOp(f, e, 2))
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -12)), constOp(f, e, 2))
	e0 := rcCell(f, e, 8)
	e1 := rcCell(f, e, 8)
	f.AddOpNoResult(e, ssa.OpStore, arr, e0)
	f.AddOpNoResult(e, ssa.OpStore, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, 8)), e1)
	for i := 1; i < arrRc; i++ {
		callOp(f, e, "__fern_rc_inc", arr)
	}
	buf := addrCallOp(f, e, helper, arr, constOp(f, e, 2), constOp(f, e, 8))
	unique := callOp(f, e, "__fern_rc_is_unique", e0)
	same := f.AddOp(e, ssa.OpEq, buf, arr)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, unique, f.AddOp(e, ssa.OpMul, same, constOp(f, e, 2))))
	return map[string]*ssa.Func{"main": f}
}

// The element-retaining grow helpers own the references their copy shares with
// the old buffer; the move forms do so only when the old buffer outlives the
// grow (rc != 1), because a sole owner's buffer is about to be released without
// an element walk and its references pass to the copy.
func TestArmRunArrPushGrowElemRetains(t *testing.T) {
	cases := []struct {
		helper string
		arrRc  int
		want   int
	}{
		{"__fern_arr_push_grow_ptr", 1, 0},
		{"__fern_arr_push_grow_ptr", 2, 0},
		{"__fern_arr_push_grow_str", 2, 0},
		{"__fern_arr_push_grow_move_ptr", 1, 1}, // sole owner: the copy inherits, elem0 stays unique
		{"__fern_arr_push_grow_move_ptr", 2, 0},
		{"__fern_arr_push_grow_move_str", 2, 0},
	}
	for _, c := range cases {
		if got := assembleRunArmModule(t, pushGrowElemModule(c.helper, c.arrRc), "main", 8); got != c.want {
			t.Errorf("%s at rc=%d: is_unique(elem0) + 2*(buf == arr) = %d, want %d", c.helper, c.arrRc, got, c.want)
		}
	}
}
