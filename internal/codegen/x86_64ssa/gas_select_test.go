package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// End-to-end OpSelect on the real-asm path (branch-free mask sequence): assemble
// + run natively and diff the exit code against ssa.EvalIn on both branches, with
// a and b kept live across the select so spills exercise the scratch operands.
// sel(cond, a, b) = (cond != 0 ? a : b) + (a - b).
func TestAsmRunSelect(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("sel")
		cond := f.AddParam()
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		picked := f.AddOp(e, ssa.OpSelect, cond, a, b)
		diff := f.AddOp(e, ssa.OpSub, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, picked, diff))
		return f
	}
	for _, args := range [][]int64{{1, 40, 2}, {0, 40, 2}, {7, 5, 9}, {0, 5, 9}} {
		for _, n := range []int{1, 2, 8} {
			runModuleTableMatchesEval(t, map[string]*ssa.Func{"sel": build()}, nil, "sel", n, args)
		}
	}
}
