package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// buildHeaderLimitLoop is the shape every counted scan has: the limit is
// recomputed in the header, so it is defined immediately before the comparison
// that reads it and dies there — which is exactly when the allocator hands the
// comparison's result the limit's own register.
//
//	i = 0; while (i < limit+step) { i += step } ; return i
func buildHeaderLimitLoop() *ssa.Func {
	f := ssa.NewFunc("main")
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	limit := f.AddParam()
	step := f.AddParam()
	init := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[len(entry.Ops)-1].Imm = 0
	f.SetBr(entry, header)

	inext := f.NewValue()
	i := f.AddPhi(header, init, inext)
	lim := f.AddOp(header, ssa.OpAdd, limit, step)
	f.SetBrIf(header, f.AddOp(header, ssa.OpLt, i, lim), body, exit)

	add := f.AddOpNoResult(body, ssa.OpAdd, i, step)
	add.Result = inext
	f.SetBr(body, header)

	f.SetRet(exit, i)
	return f
}

// A comparison whose result's register home is its RIGHT operand cannot compute
// in place — the copy in would clobber the operand the cmp still has to read —
// so left as it is, it stages through the scratch. That costs far more than the
// two moves: the SetCmp is no longer the last instruction defining the branch's
// condition register, condFusable refuses it, and the block materialises a 0/1
// it then tests. Five instructions where cmp/jcc is two, every iteration.
//
// Swapping the operands under the flipped predicate is the same comparison and
// keeps the fusion. Guards string_scan's loop test (#9640).
func TestLoopTestKeepsItsFusedCompare(t *testing.T) {
	for _, n := range []int{3, 4, 6, 8} {
		asm, err := EmitAsm(buildHeaderLimitLoop(), n)
		if err != nil {
			t.Fatalf("EmitAsm(%d regs): %v", n, err)
		}
		if strings.Contains(asm, "\tset") {
			t.Errorf("%d regs: the loop test materialises a 0/1 instead of branching on the compare:\n%s", n, asm)
		}
	}
}

// The flip is a rewrite of the comparison, so it has to mean the same thing at
// every predicate and register count. Diffed against ssa.Eval, which reads the
// unflipped op — a predicate flipped without its operands, or operands swapped
// without the predicate, inverts the loop test and changes the trip count.
func TestSwappedComparisonsAgreeWithEval(t *testing.T) {
	kinds := []ssa.OpKind{ssa.OpLt, ssa.OpLe, ssa.OpGt, ssa.OpGe, ssa.OpLtU, ssa.OpLeU, ssa.OpGtU, ssa.OpGeU}
	for _, k := range kinds {
		build := func() *ssa.Func {
			f := ssa.NewFunc("f")
			a := f.AddParam()
			b := f.AddParam()
			e := f.NewBlock()
			// sum = b+a keeps both operands live past the comparison, and the
			// comparison's result is read twice, so neither is free to be
			// folded away before the emitter sees the collision.
			sum := f.AddOp(e, ssa.OpAdd, b, a)
			cmp := f.AddOp(e, k, a, sum)
			f.SetRet(e, f.AddOp(e, ssa.OpAdd, cmp, f.AddOp(e, ssa.OpMul, cmp, sum)))
			return f
		}
		for _, n := range []int{2, 3, 8} {
			for _, args := range [][]int64{{3, 5}, {5, 3}, {4, 4}} {
				runMatchesEvalArgs(t, build(), n, args)
			}
		}
	}
}

// The commutative half of the same rewrite: an add whose result's home is its
// right operand reads the other way round rather than staging through the
// scratch, and the value is unchanged either way.
func TestSwappedCommutativeOpsAgreeWithEval(t *testing.T) {
	for _, k := range []ssa.OpKind{ssa.OpAdd, ssa.OpMul, ssa.OpAnd, ssa.OpOr, ssa.OpXor} {
		build := func() *ssa.Func {
			f := ssa.NewFunc("f")
			a := f.AddParam()
			b := f.AddParam()
			e := f.NewBlock()
			t1 := f.AddOp(e, k, a, b)
			t2 := f.AddOp(e, k, b, t1) // result frequently homes on t1
			f.SetRet(e, f.AddOp(e, ssa.OpAdd, t2, a))
			return f
		}
		for _, n := range []int{2, 3, 8} {
			runMatchesEvalArgs(t, build(), n, []int64{3, 5})
		}
	}
}
