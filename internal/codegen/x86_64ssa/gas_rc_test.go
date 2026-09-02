package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// __fern_rc_is_unique on the real-asm path: a freshly rc-headed heap cell
// (rc == 1) is unique; a null or low-address non-pointer scalar is not. Exercises
// the rc-header the SSA bump allocator now lays down (rc=1 at data-8) plus the
// gated runtime-helper emission. See docs/SSA-RC-RUNTIME.md.
func TestAsmRunRcIsUnique(t *testing.T) {
	// isUniqueOf builds main() = __fern_rc_is_unique(<arg>) where arg is produced
	// by mkArg, and returns the native exit code.
	isUniqueOf := func(mkArg func(f *ssa.Func, b *ssa.Block) ssa.Value) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", mkArg(f, e)))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}

	// A freshly allocated heap block (env cell) carries rc == 1 -> unique.
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return makeEnvOp(f, b, constOp(f, b, 7))
	}); got != 1 {
		t.Errorf("is_unique(fresh cell) = %d, want 1", got)
	}

	// A null pointer is not a heap value -> 0.
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return constOp(f, b, 0)
	}); got != 0 {
		t.Errorf("is_unique(null) = %d, want 0", got)
	}

	// A low-address non-pointer scalar (below the 0x10000 guard) -> 0.
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return constOp(f, b, 42)
	}); got != 0 {
		t.Errorf("is_unique(42) = %d, want 0", got)
	}
}

// __fern_rc_inc / __fern_rc_dec on the real-asm path, observed through
// __fern_rc_is_unique: bumping the rc past 1 makes a cell non-unique, and
// dropping it back to 1 restores uniqueness. The void inc/dec calls survive DCE
// (OpCall is impure) and run in order before the is_unique read. RC-2 of
// docs/SSA-RC-RUNTIME.md.
func TestAsmRunRcIncDec(t *testing.T) {
	// isUniqueAfter allocs a cell, applies each mutation call in `ops` to it in
	// order, then returns is_unique(cell) as the exit code.
	isUniqueAfter := func(ops ...string) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		c := makeEnvOp(f, e, constOp(f, e, 7)) // fresh rc=1 cell
		for _, op := range ops {
			callOp(f, e, op, c) // void, impure — kept + ordered
		}
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", c))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}

	// rc: 1 -> inc -> 2  => not unique.
	if got := isUniqueAfter("__fern_rc_inc"); got != 0 {
		t.Errorf("is_unique after inc = %d, want 0 (rc=2)", got)
	}
	// rc: 1 -> inc -> 2 -> dec -> 1  => unique again.
	if got := isUniqueAfter("__fern_rc_inc", "__fern_rc_dec"); got != 1 {
		t.Errorf("is_unique after inc,dec = %d, want 1 (rc=1)", got)
	}
	// rc: 1 -> inc -> inc -> dec -> 2  => still not unique.
	if got := isUniqueAfter("__fern_rc_inc", "__fern_rc_inc", "__fern_rc_dec"); got != 0 {
		t.Errorf("is_unique after inc,inc,dec = %d, want 0 (rc=2)", got)
	}
}

// The rc/drop helpers hand back the pointer they were given: ir.OpRcInc and
// ir.OpRcDec are documented `(ptr) -> ptr`, and the drop calls are emitted with
// ir.ResAddr, so the code around them reads the result out of rax and keeps
// using it as the object.
//
// On x86-64 the argument (rdi) and the result (rax) are different registers, so
// that is a property each body has to establish — unlike arm64, where both are
// x0 and leaving it alone is enough. These bodies use eax as the scratch for the
// rc word, which is exactly the value that must not be returned.
//
// Reading the cell back THROUGH each helper's result is what pins it: a helper
// returning the rc header (0x80000000, or a small count) instead of the pointer
// faults on the load rather than quietly answering something plausible. The
// tests above call these helpers for their side effect only, which is why the
// contract went unchecked.
func TestAsmRunRcHelpersReturnTheirPointer(t *testing.T) {
	through := func(helper string, extra ...int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		args := []ssa.Value{makeEnvOp(f, e, constOp(f, e, 9))} // rc=1 cell, 9 at [c+0]
		for _, a := range extra {
			args = append(args, constOp(f, e, a))
		}
		f.SetRet(e, loadMem(f, e, callOp(f, e, helper, args...), 0, ssa.OpLoad8U))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	for _, tc := range []struct {
		helper string
		extra  []int64 // trailing args: arr_dec takes a stride, box_free a size
	}{
		{helper: "__fern_rc_inc"},
		{helper: "__fern_rc_dec"},
		{helper: "__fern_str_dec"},
		{helper: "__fern_closure_drop"},
		{helper: "__fern_arr_dec", extra: []int64{8}},
		{helper: "__fern_box_free", extra: []int64{8}},
	} {
		if got := through(tc.helper, tc.extra...); got != 9 {
			t.Errorf("%s: reading [result+0] gave %d, want 9 — the helper must return its argument", tc.helper, got)
		}
	}
}
