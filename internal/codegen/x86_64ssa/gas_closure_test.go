package x86_64ssa

import (
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func sortedNames(funcs map[string]*ssa.Func) []string {
	names := make([]string, 0, len(funcs))
	for n := range funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// runModuleTableMatchesEval asserts the real binary's exit code equals
// ssa.EvalInTable(entry, args) mod 256, with the model resolving fn_idx through
// `table` (which must equal the module's sorted function order, matching the
// real-asm fn_idx assignment).
func runModuleTableMatchesEval(t *testing.T, funcs map[string]*ssa.Func, table []string, entry string, numAlloc int, args []int64) {
	t.Helper()
	want, err := ssa.EvalInTable(funcs, table, funcs[entry], args...)
	if err != nil {
		t.Fatalf("EvalInTable: %v", err)
	}
	got := assembleRunModule(t, funcs, entry, numAlloc, args)
	if got != int(uint8(want)) {
		t.Errorf("real run(%s, %v) exit=%d, want Eval&0xFF=%d (Eval=%d)", entry, args, got, int(uint8(want)), want)
	}
}

// OpMakeEnv builds an env block of captures on the heap; read both back.
// f(a,b) = env[0]*10 + env[1].
func TestAsmRunMakeEnv(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	env := makeEnvOp(f, e, a, b)
	c0 := loadMem(f, e, env, 0, ssa.OpLoad)
	c1 := loadMem(f, e, env, 8, ssa.OpLoad)
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpMul, c0, constOp(f, e, 10)), c1))

	funcs := map[string]*ssa.Func{"f": f}
	for _, n := range []int{1, 2, 8} {
		runModuleTableMatchesEval(t, funcs, nil, "f", n, []int64{5, 7}) // 57
	}
}

// OpMakeClosure builds a {fn_idx, env_ptr, drop_idx, env_ptr} cell over a
// capture env block. f(a,b) reads back fn_idx and both captures:
// fn_idx*1000 + cap0*10 + cap1. With funcs sorted as ["f","target"] and indices
// 1-based (0 is the null reference), target's fn_idx is 2.
func TestAsmRunMakeClosure(t *testing.T) {
	target := ssa.NewFunc("target")
	te := target.NewBlock()
	target.SetRet(te, constOp(target, te, 0))

	f := ssa.NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	c := makeClosureOp(f, e, "target", a, b)
	fnIdx := loadMem(f, e, c, 0, ssa.OpLoad)
	env := loadMem(f, e, c, 8, ssa.OpLoad)
	cap0 := loadMem(f, e, env, 0, ssa.OpLoad)
	cap1 := loadMem(f, e, env, 8, ssa.OpLoad)
	t1 := f.AddOp(e, ssa.OpMul, fnIdx, constOp(f, e, 1000))
	t2 := f.AddOp(e, ssa.OpMul, cap0, constOp(f, e, 10))
	f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, t1, t2), cap1))

	funcs := map[string]*ssa.Func{"f": f, "target": target}
	table := sortedNames(funcs) // ["f","target"] -> target idx 2
	for _, n := range []int{1, 2, 8} {
		runModuleTableMatchesEval(t, funcs, table, "f", n, []int64{5, 7}) // 2057
	}
}

// A zero-capture closure: the cell alone, with null env and drop slots; read
// fn_idx back.
func TestAsmRunMakeClosureNoCaptures(t *testing.T) {
	target := ssa.NewFunc("cb")
	te := target.NewBlock()
	target.SetRet(te, constOp(target, te, 0))

	f := ssa.NewFunc("f")
	e := f.NewBlock()
	c := makeClosureOp(f, e, "cb")
	f.SetRet(e, loadMem(f, e, c, 0, ssa.OpLoad)) // fn_idx

	funcs := map[string]*ssa.Func{"cb": target, "f": f}
	table := sortedNames(funcs) // ["cb","f"] -> cb idx 1
	for _, n := range []int{1, 2, 8} {
		runModuleTableMatchesEval(t, funcs, table, "f", n, nil) // 1
	}
}
