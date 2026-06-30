package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func loadOp(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64) ssa.Value {
	v := f.AddOp(b, ssa.OpLoad, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

func storeOp(f *ssa.Func, b *ssa.Block, base, val ssa.Value, offset int64) {
	op := f.AddOpNoResult(b, ssa.OpStore, base, val)
	op.Imm = offset
}

// alloc 16, store 42/100 at 0/8, load both, return sum -> 142. Validated
// against EvalIn (a one-function module) across register counts.
func TestModuleMemoryRoundtrip(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("mem")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 16))
		storeOp(f, e, p, constOp(f, e, 42), 0)
		storeOp(f, e, p, constOp(f, e, 100), 8)
		a := loadOp(f, e, p, 0)
		b := loadOp(f, e, p, 8)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, a, b))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		funcs := map[string]*ssa.Func{"mem": build()}
		moduleMatchesEval(t, funcs, "mem", [][]int64{{}})
		_ = n
	}
}

// Heap shared across calls: main allocs a cell, setCell stores into it, main
// reads it back -> 77.
func TestModuleMemorySharedAcrossCalls(t *testing.T) {
	build := func() map[string]*ssa.Func {
		setCell := ssa.NewFunc("setCell")
		ptr := setCell.AddParam()
		val := setCell.AddParam()
		se := setCell.NewBlock()
		storeOp(setCell, se, ptr, val, 0)
		setCell.SetRet(se, ssa.Value{})

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		p := main.AddOp(me, ssa.OpAlloc, constOp(main, me, 8))
		_ = callOp(main, me, "setCell", p, constOp(main, me, 77))
		main.SetRet(me, loadOp(main, me, p, 0))
		return map[string]*ssa.Func{"setCell": setCell, "main": main}
	}
	moduleMatchesEval(t, build(), "main", [][]int64{{}})
}
