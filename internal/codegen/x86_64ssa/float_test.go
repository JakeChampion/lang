package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func constFloat(f *ssa.Func, b *ssa.Block, v float64) ssa.Value {
	x := f.AddOp(b, ssa.OpConstFloat)
	b.Ops[len(b.Ops)-1].F64 = v
	return x
}

// Float arithmetic + convert, validated RunModule == EvalIn: floats live as
// f64 bits in the registers, so Run and Eval must agree bit-for-bit.
func TestModuleFloatArith(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("fa")
		e := f.NewBlock()
		s := f.AddOp(e, ssa.OpFAdd, constFloat(f, e, 1.5), constFloat(f, e, 2.5))
		p := f.AddOp(e, ssa.OpFMul, s, constFloat(f, e, 2.0))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, p))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"fa": build()}, "fa", [][]int64{{}})
}

func TestModuleFloatCompare(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("fc")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpFGe, constFloat(f, e, 3.0), constFloat(f, e, 3.0)))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"fc": build()}, "fc", [][]int64{{}})
}

// int->float->int and float memory, forced to spill at nAlloc=1 (the f64 bits
// must round-trip through a spill slot intact).
func TestModuleFloatConvAndMemory(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("fm")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8))
		fv := f.AddOp(e, ssa.OpIToFS, constOp(f, e, 5))
		sum := f.AddOp(e, ssa.OpFAdd, fv, constFloat(f, e, 0.75))
		stf := f.AddOpNoResult(e, ssa.OpStoreF, p, sum)
		stf.Imm = 0
		ld := f.AddOp(e, ssa.OpLoadF, p)
		e.Ops[len(e.Ops)-1].Imm = 0
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, ld))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"fm": build()}, "fm", [][]int64{{}})
}
