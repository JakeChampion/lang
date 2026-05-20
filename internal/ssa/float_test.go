package ssa

import (
	"math"
	"strings"
	"testing"
)

// TestFloatFoldAdd — `1.5 + 2.5` folds to const_float 4.0.
func TestFloatFoldAdd(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = 1.5
	b := f.AddOp(entry, OpConstFloat)
	entry.Ops[1].F64 = 2.5
	r := f.AddOp(entry, OpFAdd, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstFloat || got.F64 != 4.0 {
		t.Errorf("1.5+2.5 = {%v %g}, want {OpConstFloat 4}", got.Kind, got.F64)
	}
}

// TestFloatFoldSubMulDiv — three arithmetic kinds at once.
func TestFloatFoldSubMulDiv(t *testing.T) {
	cases := []struct {
		kind OpKind
		l, r float64
		want float64
	}{
		{OpFSub, 5.0, 2.0, 3.0},
		{OpFMul, 3.0, 2.5, 7.5},
		{OpFDiv, 10.0, 4.0, 2.5},
	}
	for _, c := range cases {
		t.Run(c.kind.String(), func(t *testing.T) {
			f := NewFunc("f")
			entry := f.NewBlock()
			a := f.AddOp(entry, OpConstFloat)
			entry.Ops[0].F64 = c.l
			b := f.AddOp(entry, OpConstFloat)
			entry.Ops[1].F64 = c.r
			r := f.AddOp(entry, c.kind, a, b)
			f.SetRet(entry, r)

			Fold(f)

			if got := entry.Ops[2]; got.Kind != OpConstFloat || got.F64 != c.want {
				t.Errorf("%v(%g, %g) = {%v %g}, want %g", c.kind, c.l, c.r, got.Kind, got.F64, c.want)
			}
		})
	}
}

// TestFloatFoldDivByZero — IEEE-754 division by zero is
// well-defined (produces ±Inf or NaN), so Fold proceeds
// unlike OpDiv.
func TestFloatFoldDivByZero(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = 1.0
	b := f.AddOp(entry, OpConstFloat)
	entry.Ops[1].F64 = 0.0
	r := f.AddOp(entry, OpFDiv, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstFloat || !math.IsInf(got.F64, +1) {
		t.Errorf("1.0/0.0 = {%v %g}, want {OpConstFloat +Inf}", got.Kind, got.F64)
	}
}

// TestFloatFoldComparisons — every cmp folds to const_bool.
func TestFloatFoldComparisons(t *testing.T) {
	cases := []struct {
		kind OpKind
		l, r float64
		want int64
	}{
		{OpFEq, 1.0, 1.0, 1},
		{OpFNe, 1.0, 1.0, 0},
		{OpFLt, 1.0, 2.0, 1},
		{OpFLe, 2.0, 2.0, 1},
		{OpFGt, 3.0, 2.0, 1},
		{OpFGe, 2.0, 2.0, 1},
	}
	for _, c := range cases {
		t.Run(c.kind.String(), func(t *testing.T) {
			f := NewFunc("f")
			entry := f.NewBlock()
			a := f.AddOp(entry, OpConstFloat)
			entry.Ops[0].F64 = c.l
			b := f.AddOp(entry, OpConstFloat)
			entry.Ops[1].F64 = c.r
			r := f.AddOp(entry, c.kind, a, b)
			f.SetRet(entry, r)

			Fold(f)

			if got := entry.Ops[2]; got.Kind != OpConstBool || got.Imm != c.want {
				t.Errorf("%v(%g, %g) = {%v %d}, want %d", c.kind, c.l, c.r, got.Kind, got.Imm, c.want)
			}
		})
	}
}

// TestFloatFoldNeg — `-(const 3.14)` folds to const_float -3.14.
func TestFloatFoldNeg(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = 3.14
	r := f.AddOp(entry, OpFNeg, a)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != -3.14 {
		t.Errorf("neg(3.14) = {%v %g}, want {OpConstFloat -3.14}", got.Kind, got.F64)
	}
}

// TestFloatFoldLeavesNonConst — float op on Params is left
// alone.
func TestFloatFoldLeavesNonConst(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	y := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpFAdd, x, y)
	f.SetRet(entry, r)

	Fold(f)
	if entry.Ops[0].Kind != OpFAdd {
		t.Errorf("Kind = %v, want OpFAdd (non-const)", entry.Ops[0].Kind)
	}
}

// TestFloatCanonicalize — FAdd / FMul / FEq / FNe are
// commutative and get sorted.
func TestFloatCanonicalize(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam() // v1
	b := f.AddParam() // v2
	entry := f.NewBlock()
	op := &Op{Kind: OpFAdd, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)
	if op.Args[0] != a || op.Args[1] != b {
		t.Errorf("Args = %v, want [a, b] (commutative)", op.Args)
	}
}

// TestFloatFSubNotCommutative — FSub args preserved.
func TestFloatFSubNotCommutative(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpFSub, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)
	if op.Args[0] != b || op.Args[1] != a {
		t.Errorf("Args = %v, want [b, a] (FSub not commutative)", op.Args)
	}
}

// TestFloatPrints — golden form for const_float and fadd.
func TestFloatPrints(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = 1.5
	b := f.AddOp(entry, OpConstFloat)
	entry.Ops[1].F64 = 2.5
	r := f.AddOp(entry, OpFAdd, a, b)
	f.SetRet(entry, r)

	got := f.String()
	for _, want := range []string{
		"v1 = const_float 1.5",
		"v2 = const_float 2.5",
		"v3 = fadd v1, v2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestFloatInOptimize — `(1.0 + 2.0) * 3.0` end-to-end through
// the Optimize pipeline.
func TestFloatInOptimize(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = 1.0
	b := f.AddOp(entry, OpConstFloat)
	entry.Ops[1].F64 = 2.0
	c := f.AddOp(entry, OpConstFloat)
	entry.Ops[2].F64 = 3.0
	sum := f.AddOp(entry, OpFAdd, a, b)
	prod := f.AddOp(entry, OpFMul, sum, c)
	f.SetRet(entry, prod)

	Optimize(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1; kinds %v", len(entry.Ops), opKinds(entry.Ops))
	}
	if got := entry.Ops[0]; got.Kind != OpConstFloat || got.F64 != 9.0 {
		t.Errorf("result = {%v %g}, want {OpConstFloat 9}", got.Kind, got.F64)
	}
}
