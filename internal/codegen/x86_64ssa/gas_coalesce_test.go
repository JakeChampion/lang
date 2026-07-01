package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A chain of arithmetic where each result feeds the next stresses result-into-
// home coalescing: a result frequently lands in an operand's own register (the
// operand dies at that op), and some results collide with the second operand
// (forcing the scratch fallback). Diffed against ssa.Eval over spill-forcing
// register counts, so a wrong coalesce (clobbering a still-live operand) changes
// the result. f(a,b) = (((a+b) * a) - b) + a ; f(3,5) = ((8*3)-5)+3 = 22.
func TestAsmRunCoalesceChain(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		t1 := f.AddOp(e, ssa.OpAdd, a, b)  // a+b
		t2 := f.AddOp(e, ssa.OpMul, t1, a) // *a  (a still live)
		t3 := f.AddOp(e, ssa.OpSub, t2, b) // -b  (b still live)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, t3, a))
		return f
	}
	for _, n := range []int{1, 2, 3, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{3, 5}) // 22
	}
}

// The comparison result coalescing (SetCmp into a register home) must not clobber
// an operand still needed. cmp(a,b) then use both a and b again.
// g(a,b) = (a < b ? 1 : 0) + a * b ; g(3,5) = 1 + 15 = 16.
func TestAsmRunCoalesceCmp(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("g")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		lt := f.AddOp(e, ssa.OpLt, a, b)    // a<b (a,b still live)
		prod := f.AddOp(e, ssa.OpMul, a, b) // a*b
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, lt, prod))
		return f
	}
	for _, n := range []int{1, 2, 3, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{3, 5}) // 16
	}
}
