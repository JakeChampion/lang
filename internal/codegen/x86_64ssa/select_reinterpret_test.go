package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func setLastWidth(b *ssa.Block, w int8) {
	b.Ops[len(b.Ops)-1].Width = w
}

// OpSelect validated RunModule == EvalIn on both branches and under a
// spill-forcing register count (params kept live across the select).
func TestModuleSelect(t *testing.T) {
	// f(cond, a, b) = cond != 0 ? a : b, but keep a and b live across an add so
	// the allocator must hold three values plus the cond.
	build := func() *ssa.Func {
		f := ssa.NewFunc("sel")
		cond := f.AddParam()
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		picked := f.AddOp(e, ssa.OpSelect, cond, a, b)
		// picked + (a - b) — forces a, b to survive past the select.
		diff := f.AddOp(e, ssa.OpSub, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, picked, diff))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"sel": build()}, "sel",
		[][]int64{{1, 40, 2}, {0, 40, 2}, {7, 5, 9}, {0, 5, 9}})
}

// Reinterprets validated RunModule == EvalIn. The 64-bit ones expose a float's
// raw bits; the 32-bit ones round-trip an i32 pattern through an f32.
func TestModuleReinterpret(t *testing.T) {
	// f64->i64: take 1.5's bits, mask to the low byte so the exit code is stable.
	f64ToI64 := func() *ssa.Func {
		f := ssa.NewFunc("r")
		e := f.NewBlock()
		c := constFloat(f, e, 1.5)
		bits := f.AddOp(e, ssa.OpReinterpretF64ToI64, c)
		setLastWidth(e, 64)
		// low byte of the bit pattern
		f.SetRet(e, f.AddOp(e, ssa.OpAnd, bits, constOp(f, e, 0xff)))
		return f
	}
	// i64->f64->i64 round-trip of a raw bit pattern.
	i64Round := func() *ssa.Func {
		f := ssa.NewFunc("r")
		e := f.NewBlock()
		raw := constOp(f, e, 0x4008000000000000) // f64 bits of 3.0
		setLastWidth(e, 64)
		asF := f.AddOp(e, ssa.OpReinterpretI64ToF64, raw)
		setLastWidth(e, 64)
		back := f.AddOp(e, ssa.OpReinterpretF64ToI64, asF)
		setLastWidth(e, 64)
		f.SetRet(e, f.AddOp(e, ssa.OpAnd, back, constOp(f, e, 0xff)))
		return f
	}
	// f32->i32 of 2.5.
	f32ToI32 := func() *ssa.Func {
		f := ssa.NewFunc("r")
		e := f.NewBlock()
		c := constFloat(f, e, 2.5)
		setLastWidth(e, 32)
		bits := f.AddOp(e, ssa.OpReinterpretF32ToI32, c)
		setLastWidth(e, 32)
		f.SetRet(e, f.AddOp(e, ssa.OpAnd, bits, constOp(f, e, 0xff)))
		return f
	}
	// i32->f32->i32 round-trip.
	i32Round := func() *ssa.Func {
		f := ssa.NewFunc("r")
		e := f.NewBlock()
		raw := constOp(f, e, 0x40200000) // f32 bits of 2.5
		asF := f.AddOp(e, ssa.OpReinterpretI32ToF32, raw)
		setLastWidth(e, 32)
		back := f.AddOp(e, ssa.OpReinterpretF32ToI32, asF)
		setLastWidth(e, 32)
		f.SetRet(e, f.AddOp(e, ssa.OpAnd, back, constOp(f, e, 0xff)))
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"r": f64ToI64()}, "r", [][]int64{{}})
	moduleMatchesEval(t, map[string]*ssa.Func{"r": i64Round()}, "r", [][]int64{{}})
	moduleMatchesEval(t, map[string]*ssa.Func{"r": f32ToI32()}, "r", [][]int64{{}})
	moduleMatchesEval(t, map[string]*ssa.Func{"r": i32Round()}, "r", [][]int64{{}})
}
