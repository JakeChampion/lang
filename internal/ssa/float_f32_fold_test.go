package ssa

import "testing"

// An f32 op folds at f32 precision, not f64. Every f32 operation rounds its
// result to f32 at runtime — the backends emit an fcvt round trip for exactly
// that — so a constant fold that keeps f64's extra mantissa bits produces a
// value the same expression could never compute, and often one f32 cannot even
// represent.
//
// `0.1f32 * 0.1f32` is the smallest case that shows it: in f64 the product of
// the two f32-rounded operands is 0.010000000707805156, which is not an f32;
// rounding to f32 gives 0.010000001080334187.
//
// Both fold sites need this. SCCP runs first in Optimize and folds most
// constants, but Fold is an exported pass in its own right and is invoked
// directly, so neither can rely on the other having got there.
func TestFoldF32RoundsToF32(t *testing.T) {
	const (
		a       = float64(float32(0.1))
		wantF32 = float64(float32(a * a))
	)
	if a*a == wantF32 {
		t.Fatal("test operands do not discriminate f32 from f64 rounding")
	}

	build := func(width int8) (*Func, *Block) {
		f := NewFunc("f")
		e := f.NewBlock()
		x := f.AddOp(e, OpConstFloat)
		e.Ops[0].F64 = a
		y := f.AddOp(e, OpConstFloat)
		e.Ops[1].F64 = a
		r := f.AddOp(e, OpFMul, x, y)
		e.Ops[2].Width = width
		f.SetRet(e, r)
		return f, e
	}

	t.Run("Fold", func(t *testing.T) {
		f, e := build(32)
		Fold(f)
		if got := e.Ops[2]; got.Kind != OpConstFloat || got.F64 != wantF32 {
			t.Errorf("f32 fold = {%v %v}, want {OpConstFloat %v}", got.Kind, got.F64, wantF32)
		}
	})

	t.Run("SCCP", func(t *testing.T) {
		f, e := build(32)
		SCCP(f)
		if got := e.Ops[2]; got.Kind != OpConstFloat || got.F64 != wantF32 {
			t.Errorf("f32 sccp = {%v %v}, want {OpConstFloat %v}", got.Kind, got.F64, wantF32)
		}
	})

	// The f64 case must keep full precision — the rounding is conditional on
	// the width, not unconditional.
	t.Run("Fold_f64_unrounded", func(t *testing.T) {
		f, e := build(64)
		Fold(f)
		if got := e.Ops[2]; got.Kind != OpConstFloat || got.F64 != a*a {
			t.Errorf("f64 fold = {%v %v}, want {OpConstFloat %v}", got.Kind, got.F64, a*a)
		}
	})

	t.Run("SCCP_f64_unrounded", func(t *testing.T) {
		f, e := build(64)
		SCCP(f)
		if got := e.Ops[2]; got.Kind != OpConstFloat || got.F64 != a*a {
			t.Errorf("f64 sccp = {%v %v}, want {OpConstFloat %v}", got.Kind, got.F64, a*a)
		}
	})
}
