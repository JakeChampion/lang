package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// End-to-end closure dispatch on the real-asm path: assemble + run natively and
// diff the exit code against ssa.EvalInTable. apply(fn, x) dispatches the {fn,
// env} cell through the function-address table; main sums two closures.
// apply(mkClosure(inc), 10) + apply(mkClosure(dbl), 10) = 11 + 20 = 31.
func TestAsmRunCallIndirect(t *testing.T) {
	funcs, table := indirectFuncs()
	main := ssa.NewFunc("main")
	me := main.NewBlock()
	cInc := makeClosureOp(main, me, "inc")
	cDbl := makeClosureOp(main, me, "dbl")
	r0 := callOp(main, me, "apply", cInc, constOp(main, me, 10))
	r1 := callOp(main, me, "apply", cDbl, constOp(main, me, 10))
	main.SetRet(me, main.AddOp(me, ssa.OpAdd, r0, r1))
	funcs["main"] = main

	for _, n := range []int{1, 2, 8} {
		runModuleTableMatchesEval(t, funcs, table, "main", n, nil) // 31
	}
}

// A caller that picks the closure at runtime and dispatches it — the chosen
// cell pointer (and the resolved target address) must survive the arg shuffle
// and every spill-forcing register count. main(sel, x) = (sel != 0 ? dbl : inc)(x).
func TestAsmRunCallIndirectComputedTarget(t *testing.T) {
	build := func() (map[string]*ssa.Func, []string) {
		funcs, table := indirectFuncs()
		main := ssa.NewFunc("main")
		sel := main.AddParam()
		x := main.AddParam()
		me := main.NewBlock()
		cInc := makeClosureOp(main, me, "inc")
		cDbl := makeClosureOp(main, me, "dbl")
		cond := main.AddOp(me, ssa.OpNe, sel, constOp(main, me, 0))
		chosen := main.AddOp(me, ssa.OpSelect, cond, cDbl, cInc)
		main.SetRet(me, callIndirectOp(main, me, chosen, x))
		funcs["main"] = main
		return funcs, table
	}
	for _, args := range [][]int64{{0, 7}, {1, 7}, {0, 100}, {1, 50}} {
		for _, n := range []int{1, 2, 8} {
			funcs, table := build()
			runModuleTableMatchesEval(t, funcs, table, "main", n, args)
		}
	}
}
