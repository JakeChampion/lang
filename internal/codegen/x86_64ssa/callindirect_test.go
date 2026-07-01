package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// callIndirectOp adds an OpCallIndirect dispatching on callee (a function-index
// value) with the given args, and returns its result.
func callIndirectOp(f *ssa.Func, b *ssa.Block, callee ssa.Value, args ...ssa.Value) ssa.Value {
	all := append([]ssa.Value{callee}, args...)
	return f.AddOp(b, ssa.OpCallIndirect, all...)
}

// moduleMatchesEvalTable asserts RunModuleTable == EvalInTable for the named
// entry across register-file sizes, resolving OpCallIndirect through `table`.
func moduleMatchesEvalTable(t *testing.T, funcs map[string]*ssa.Func, table []string, entry string, argSets [][]int64) {
	t.Helper()
	for _, nAlloc := range []int{1, 2, 8} {
		mod, err := EmitModule(funcs, nAlloc)
		if err != nil {
			t.Fatalf("nAlloc=%d EmitModule: %v", nAlloc, err)
		}
		for _, args := range argSets {
			want, err := ssa.EvalInTable(funcs, table, funcs[entry], args...)
			if err != nil {
				t.Fatalf("EvalInTable(%s, %v): %v", entry, args, err)
			}
			got, err := RunModuleTable(mod, table, entry, args)
			if err != nil {
				t.Fatalf("nAlloc=%d RunModuleTable(%s, %v): %v", nAlloc, entry, args, err)
			}
			if got != want {
				t.Errorf("nAlloc=%d %s(%v): RunModuleTable=%d, EvalInTable=%d", nAlloc, entry, args, got, want)
			}
		}
	}
}

// inc(x)=x+1, dbl(x)=x*2, table=[inc,dbl]. apply(fn,x)=fn(x) via OpCallIndirect.
func indirectFuncs() (map[string]*ssa.Func, []string) {
	inc := ssa.NewFunc("inc")
	ix := inc.AddParam()
	ie := inc.NewBlock()
	inc.SetRet(ie, inc.AddOp(ie, ssa.OpAdd, ix, constOp(inc, ie, 1)))

	dbl := ssa.NewFunc("dbl")
	dx := dbl.AddParam()
	de := dbl.NewBlock()
	dbl.SetRet(de, dbl.AddOp(de, ssa.OpMul, dx, constOp(dbl, de, 2)))

	apply := ssa.NewFunc("apply")
	fn := apply.AddParam()
	x := apply.AddParam()
	ae := apply.NewBlock()
	apply.SetRet(ae, callIndirectOp(apply, ae, fn, x))

	funcs := map[string]*ssa.Func{"inc": inc, "dbl": dbl, "apply": apply}
	return funcs, []string{"inc", "dbl"}
}

// Indirect dispatch through a function-index value, validated RunModuleTable ==
// EvalInTable over both table entries and spill-forcing register counts.
func TestModuleCallIndirect(t *testing.T) {
	funcs, table := indirectFuncs()
	moduleMatchesEvalTable(t, funcs, table, "apply", [][]int64{{0, 10}, {1, 10}, {0, 41}, {1, 21}})
}

// A caller that picks the callee at runtime (index from a comparison) then
// dispatches indirectly — the index must survive to the call, incl. under
// spills. main(sel,x) = (sel!=0 ? dbl : inc)(x).
func TestModuleCallIndirectComputedIndex(t *testing.T) {
	funcs, table := indirectFuncs()

	main := ssa.NewFunc("main")
	sel := main.AddParam()
	x := main.AddParam()
	me := main.NewBlock()
	// idx = (sel != 0) ? 1 : 0  — OpNe already yields 0/1, matching the table.
	idx := main.AddOp(me, ssa.OpNe, sel, constOp(main, me, 0))
	main.SetRet(me, callIndirectOp(main, me, idx, x))
	funcs["main"] = main

	moduleMatchesEvalTable(t, funcs, table, "main", [][]int64{{0, 7}, {1, 7}, {0, 100}, {1, 100}})
}
