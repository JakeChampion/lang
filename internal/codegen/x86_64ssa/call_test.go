package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func callOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpCall, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return v
}

// moduleMatchesEval asserts RunModule(EmitModule(funcs)) == EvalIn(funcs) for
// the named entry across several register-file sizes (small ones force spills).
func moduleMatchesEval(t *testing.T, funcs map[string]*ssa.Func, entry string, argSets [][]int64) {
	t.Helper()
	for _, nAlloc := range []int{1, 2, 8} {
		mod, err := EmitModule(funcs, nAlloc)
		if err != nil {
			t.Fatalf("nAlloc=%d EmitModule: %v", nAlloc, err)
		}
		for _, args := range argSets {
			want, err := ssa.EvalIn(funcs, funcs[entry], args...)
			if err != nil {
				t.Fatalf("EvalIn(%s, %v): %v", entry, args, err)
			}
			got, err := RunModule(mod, entry, args)
			if err != nil {
				t.Fatalf("nAlloc=%d RunModule(%s, %v): %v", nAlloc, entry, args, err)
			}
			if got != want {
				t.Errorf("nAlloc=%d %s(%v): RunModule=%d, EvalIn=%d", nAlloc, entry, args, got, want)
			}
		}
	}
}

// add(a,b)=a+b ; main()=add(3,4)+add(10,20). Cross-function direct calls.
func TestModuleDirectCalls(t *testing.T) {
	add := ssa.NewFunc("add")
	a := add.AddParam()
	b := add.AddParam()
	ae := add.NewBlock()
	add.SetRet(ae, add.AddOp(ae, ssa.OpAdd, a, b))

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	t1 := callOp(main, me, "add", constOp(main, me, 3), constOp(main, me, 4))
	t2 := callOp(main, me, "add", constOp(main, me, 10), constOp(main, me, 20))
	main.SetRet(me, main.AddOp(me, ssa.OpAdd, t1, t2))

	funcs := map[string]*ssa.Func{"add": add, "main": main}
	moduleMatchesEval(t, funcs, "main", [][]int64{{}})
}

// factorial(n) = n<=1 ? 1 : n*factorial(n-1). Recursion through the allocator +
// model, validated against EvalIn over several inputs and register counts.
func TestModuleRecursion(t *testing.T) {
	f := ssa.NewFunc("factorial")
	n := f.AddParam()
	entry := f.NewBlock()
	base := f.NewBlock()
	rec := f.NewBlock()
	cond := f.AddOp(entry, ssa.OpLe, n, constOp(f, entry, 1))
	f.SetBrIf(entry, cond, base, rec)
	f.SetRet(base, constOp(f, base, 1))
	nm1 := f.AddOp(rec, ssa.OpSub, n, constOp(f, rec, 1))
	fr := callOp(f, rec, "factorial", nm1)
	f.SetRet(rec, f.AddOp(rec, ssa.OpMul, n, fr))

	funcs := map[string]*ssa.Func{"factorial": f}
	moduleMatchesEval(t, funcs, "factorial", [][]int64{{0}, {1}, {3}, {5}, {7}})
}
