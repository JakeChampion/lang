package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Argument order must survive the parallel-move arg passing even when operands
// share registers with argument slots. main computes three distinct values kept
// live simultaneously and passes them positionally to f(a,b,c) = a*100+b*10+c;
// a wrong shuffle (swap/clobber) would reorder or corrupt them. Diffed against
// ssa.Eval over spill-forcing register counts. f(1,2,3)=123.
func TestAsmRunArgOrderParallelMove(t *testing.T) {
	f := ssa.NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	c := f.AddParam()
	fe := f.NewBlock()
	t1 := f.AddOp(fe, ssa.OpMul, a, constOp(f, fe, 100))
	t2 := f.AddOp(fe, ssa.OpMul, b, constOp(f, fe, 10))
	f.SetRet(fe, f.AddOp(fe, ssa.OpAdd, f.AddOp(fe, ssa.OpAdd, t1, t2), c))

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	// Derive three live values so the allocator places them in registers that
	// can collide with the arg-register targets.
	x := main.AddOp(me, ssa.OpAdd, constOp(main, me, 0), constOp(main, me, 1))
	y := main.AddOp(me, ssa.OpAdd, x, constOp(main, me, 1))
	z := main.AddOp(me, ssa.OpAdd, y, constOp(main, me, 1))
	main.SetRet(me, callOp(main, me, "f", x, y, z)) // f(1,2,3) = 123

	funcs := map[string]*ssa.Func{"f": f, "main": main}
	for _, n := range []int{1, 2, 3, 8} {
		runModuleMatchesEval(t, funcs, "main", n, nil)
	}
}
