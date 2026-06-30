package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func storeNOp(f *ssa.Func, b *ssa.Block, base, val ssa.Value, offset int64, kind ssa.OpKind) {
	op := f.AddOpNoResult(b, kind, base, val)
	op.Imm = offset
}

func loadNOp(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64, kind ssa.OpKind) ssa.Value {
	v := f.AddOp(b, kind, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// Byte signedness through the allocator + model, diffed against EvalIn.
func TestModuleSubwordByteSign(t *testing.T) {
	for _, kind := range []ssa.OpKind{ssa.OpLoad8U, ssa.OpLoad8S} {
		build := func() *ssa.Func {
			f := ssa.NewFunc("b")
			e := f.NewBlock()
			p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8))
			storeNOp(f, e, p, constOp(f, e, 200), 0, ssa.OpStore8)
			f.SetRet(e, loadNOp(f, e, p, 0, kind))
			return f
		}
		moduleMatchesEval(t, map[string]*ssa.Func{"b": build()}, "b", [][]int64{{}})
	}
}

// 3-byte array sum (=60) and a store8-preserves-high-bytes check, validated
// RunModule == EvalIn across register counts.
func TestModuleSubwordByteArray(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("arr")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 3))
		storeNOp(f, e, p, constOp(f, e, 10), 0, ssa.OpStore8)
		storeNOp(f, e, p, constOp(f, e, 20), 1, ssa.OpStore8)
		storeNOp(f, e, p, constOp(f, e, 30), 2, ssa.OpStore8)
		a := loadNOp(f, e, p, 0, ssa.OpLoad8U)
		b := loadNOp(f, e, p, 1, ssa.OpLoad8U)
		c := loadNOp(f, e, p, 2, ssa.OpLoad8U)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, a, b), c))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"arr": build()}, "arr", [][]int64{{}})
}

func TestModuleSubwordHalfword(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("h")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8))
		storeNOp(f, e, p, constOp(f, e, 0xFFFF), 0, ssa.OpStore16)
		f.SetRet(e, loadNOp(f, e, p, 0, ssa.OpLoad16S)) // -> -1
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"h": build()}, "h", [][]int64{{}})
}
