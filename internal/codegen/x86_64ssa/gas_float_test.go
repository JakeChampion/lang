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
