package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func enumSentinel(f *ssa.Func, b *ssa.Block, tag int64) ssa.Value {
	v := f.AddOp(b, ssa.OpEnumSentinel)
	b.Ops[len(b.Ops)-1].Imm = tag
	return v
}

// Enum sentinels validated RunModule == EvalIn: same-tag equality, different-tag
// inequality, and the stored tag byte.
func TestModuleEnumSentinel(t *testing.T) {
	sameTag := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpEq, enumSentinel(f, e, 3), enumSentinel(f, e, 3)))
		return f
	}
	diffTag := func() *ssa.Func {
		f := ssa.NewFunc("d")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpEq, enumSentinel(f, e, 3), enumSentinel(f, e, 7)))
		return f
	}
	tagStored := func() *ssa.Func {
		f := ssa.NewFunc("t")
		e := f.NewBlock()
		s := enumSentinel(f, e, 5)
		f.SetRet(e, loadNOp(f, e, s, 0, ssa.OpLoad8U))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"s": sameTag()}, "s", [][]int64{{}})
	moduleMatchesEval(t, map[string]*ssa.Func{"d": diffTag()}, "d", [][]int64{{}})
	moduleMatchesEval(t, map[string]*ssa.Func{"t": tagStored()}, "t", [][]int64{{}})
}
