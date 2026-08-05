package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func makeClosureOp(f *ssa.Func, b *ssa.Block, target string, caps ...ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpMakeClosure, caps...)
	b.Ops[len(b.Ops)-1].Str = target
	return v
}

func makeEnvOp(f *ssa.Func, b *ssa.Block, caps ...ssa.Value) ssa.Value {
	return f.AddOp(b, ssa.OpMakeEnv, caps...)
}

// OpMakeClosure builds a {fn_idx, env_ptr, drop_idx, env_ptr} cell over a
// capture env block. f(a,b) reads back fn_idx and both captures, combined so any
// field mismatch shows: fn_idx*1000 + cap0*10 + cap1. Validated RunModuleTable
// == EvalInTable (same heap layout) over spill-forcing register counts.
func TestModuleMakeClosure(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	c := makeClosureOp(f, e, "inc", a, b)
	fnIdx := loadNOp(f, e, c, 0, ssa.OpLoad)
	env := loadNOp(f, e, c, 8, ssa.OpLoad)
	cap0 := loadNOp(f, e, env, 0, ssa.OpLoad)
	cap1 := loadNOp(f, e, env, 8, ssa.OpLoad)
	t1 := f.AddOp(e, ssa.OpMul, fnIdx, constOp(f, e, 1000))
	t2 := f.AddOp(e, ssa.OpMul, cap0, constOp(f, e, 10))
	sum := f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, t1, t2), cap1)
	f.SetRet(e, sum)

	funcs := map[string]*ssa.Func{"f": f}
	table := []string{"inc"} // inc's function-value index is 1
	moduleMatchesEvalTable(t, funcs, table, "f", [][]int64{{5, 7}, {0, 0}, {9, 1}})
}

// OpMakeEnv builds just the env block. f(a,b) reads back both captures.
func TestModuleMakeEnv(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	env := makeEnvOp(f, e, a, b)
	cap0 := loadNOp(f, e, env, 0, ssa.OpLoad)
	cap1 := loadNOp(f, e, env, 8, ssa.OpLoad)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpMul, cap0, constOp(f, e, 10)), cap1))

	moduleMatchesEvalTable(t, map[string]*ssa.Func{"f": f}, nil, "f", [][]int64{{5, 7}, {3, 4}})
}
