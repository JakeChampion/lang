package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Float arithmetic via SSE (bits shuttled GPR<->xmm), result truncated to int
// so the exit code is observable. Diffed against ssa.Eval.
func TestAsmRunFloatArith(t *testing.T) {
	build := func(k ssa.OpKind, a, b float64) *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		r := f.AddOp(e, k, constFloat(f, e, a), constFloat(f, e, b))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, r))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(ssa.OpFAdd, 1.5, 2.5), n) // 4.0 -> 4
		runMatchesEval(t, build(ssa.OpFSub, 5.0, 1.5), n) // 3.5 -> 3
		runMatchesEval(t, build(ssa.OpFMul, 2.5, 2.0), n) // 5.0 -> 5
		runMatchesEval(t, build(ssa.OpFDiv, 9.0, 2.0), n) // 4.5 -> 4
	}
}

// Ordered float comparisons materialise 0/1 (finite operands).
func TestAsmRunFloatCompare(t *testing.T) {
	build := func(k ssa.OpKind, a, b float64) *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, k, constFloat(f, e, a), constFloat(f, e, b)))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(ssa.OpFLt, 1.5, 2.5), n) // 1
		runMatchesEval(t, build(ssa.OpFLt, 2.5, 1.5), n) // 0
		runMatchesEval(t, build(ssa.OpFEq, 3.0, 3.0), n) // 1
		runMatchesEval(t, build(ssa.OpFNe, 3.0, 3.0), n) // 0
		runMatchesEval(t, build(ssa.OpFGe, 2.0, 2.0), n) // 1
		runMatchesEval(t, build(ssa.OpFGt, 2.0, 2.0), n) // 0
		runMatchesEval(t, build(ssa.OpFLe, 2.0, 2.5), n) // 1
	}
}

// int->float->int round-trips, float negation, and demote.
func TestAsmRunFloatConv(t *testing.T) {
	// IToFS: int 7 -> 7.0 -> 7
	itof := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		asF := f.AddOp(e, ssa.OpIToFS, constOp(f, e, 7))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, asF))
		return f
	}
	// FNeg: -(3.0) -> -3
	neg := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpFNeg, constFloat(f, e, 3.0))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, n))
		return f
	}
	// FDemote then truncate: 6.5 (f32-exact) -> 6
	demote := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		d := f.AddOp(e, ssa.OpFDemote, constFloat(f, e, 6.5))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, d))
		return f
	}
	// FToIS truncates toward zero: -2.7 -> -2
	trunc := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpFNeg, constFloat(f, e, 2.7))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, n))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, itof(), n)
		runMatchesEval(t, neg(), n)
		runMatchesEval(t, demote(), n)
		runMatchesEval(t, trunc(), n)
	}
}

// f32-width arithmetic exercises the round-to-f32 path; the result truncates
// to the same int, so the check is that it assembles + matches Eval.
func TestAsmRunFloatF32Width(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		m := f.AddOp(e, ssa.OpFMul, constFloat(f, e, 2.5), constFloat(f, e, 3.0))
		setLastWidth(e, 32) // f32 multiply
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, m))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n) // 7.5 -> 7
	}
}

// Floats through memory: store an f64, load it back, truncate. Uses the LoadF /
// StoreF 8-byte memory path.
func TestAsmRunFloatMemory(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		p := allocOp(f, e, 8)
		val := f.AddOp(e, ssa.OpFAdd, constFloat(f, e, 1.25), constFloat(f, e, 2.25))
		f.AddOpNoResult(e, ssa.OpStoreF, p, val)
		e.Ops[len(e.Ops)-1].Imm = 0
		back := f.AddOp(e, ssa.OpLoadF, p)
		e.Ops[len(e.Ops)-1].Imm = 0
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, back))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n) // 3.5 -> 3
	}
}

// The four reinterprets, run for real and diffed against ssa.Eval. The
// 64-bit pair is an identity over the f64 pattern the register holds; the
// 32-bit pair narrows through f32 and back. The same shapes
// TestModuleReinterpret checks against the model.
func TestAsmRunFloatReinterpret(t *testing.T) {
	// f64->i64: 1.5's bits, masked to the low byte.
	f64ToI64 := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		bits := f.AddOp(e, ssa.OpReinterpretF64ToI64, constFloat(f, e, 1.5))
		setLastWidth(e, 64)
		f.SetRet(e, f.AddOp(e, ssa.OpAnd, bits, constOp(f, e, 0xff)))
		return f
	}
	// i64->f64->i64 round-trip of a raw bit pattern.
	i64Round := func() *ssa.Func {
		f := ssa.NewFunc("f")
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
	// f32->i32: 1.5f's bits are 0x3fc00000; shift the top byte down.
	f32ToI32 := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		bits := f.AddOp(e, ssa.OpReinterpretF32ToI32, constFloat(f, e, 1.5))
		setLastWidth(e, 32)
		f.SetRet(e, f.AddOp(e, ssa.OpShrU, bits, constOp(f, e, 24)))
		return f
	}
	// -0.0f's bits set bit 31, so the i32 result is negative and its
	// sign-extension is what the shift exposes.
	f32Negative := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		bits := f.AddOp(e, ssa.OpReinterpretF32ToI32, constFloat(f, e, -2.0))
		setLastWidth(e, 32)
		f.SetRet(e, f.AddOp(e, ssa.OpShr, bits, constOp(f, e, 28)))
		return f
	}
	// i32->f32->i32 round-trip of a raw pattern, then to an int: 0x40490fdb
	// is 3.1415927f, truncating to 3.
	i32Round := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		raw := constOp(f, e, 0x40490fdb)
		setLastWidth(e, 32)
		asF := f.AddOp(e, ssa.OpReinterpretI32ToF32, raw)
		setLastWidth(e, 32)
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, asF))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, f64ToI64(), n)
		runMatchesEval(t, i64Round(), n)
		runMatchesEval(t, f32ToI32(), n)
		runMatchesEval(t, f32Negative(), n)
		runMatchesEval(t, i32Round(), n)
	}
}
