package ssa

import (
	"math"
	"testing"
)

// Float-to-int conversion saturates (docs/FLOAT-SEMANTICS.md): NaN → 0, above
// the destination's max → that max, below its min → that min. Go's own
// conversion is undefined for those inputs and in practice wraps, so every
// compile-time evaluation of one of these ops has to clamp explicitly.
//
// Width follows the lift's convention: 64 for an i64/u64 destination, 0 for
// i32/u32 (the lift only stamps 64), so 0 is tested alongside 32.
func TestSatFloatToInt(t *testing.T) {
	nan := math.NaN()
	inf := math.Inf(1)

	signed := []struct {
		name  string
		v     float64
		width int8
		want  int64
	}{
		{"i32 in range", 42.9, 0, 42},
		{"i32 negative truncates toward zero", -42.9, 0, -42},
		{"i32 overflow saturates", 1e10, 0, math.MaxInt32},
		{"i32 underflow saturates", -1e10, 0, math.MinInt32},
		{"i32 +Inf saturates", inf, 0, math.MaxInt32},
		{"i32 -Inf saturates", -inf, 0, math.MinInt32},
		{"i32 NaN is zero", nan, 0, 0},
		{"i32 width 32 matches width 0", 1e10, 32, math.MaxInt32},
		{"i64 in range", 1e10, 64, 10000000000},
		{"i64 overflow saturates", 1e30, 64, math.MaxInt64},
		{"i64 underflow saturates", -1e30, 64, math.MinInt64},
		{"i64 NaN is zero", nan, 64, 0},
	}
	for _, c := range signed {
		t.Run("FToIS/"+c.name, func(t *testing.T) {
			if got := satFToIS(c.v, c.width); got != c.want {
				t.Errorf("satFToIS(%v, %d) = %d, want %d", c.v, c.width, got, c.want)
			}
		})
	}

	// u32 results are held sign-extended from bit 31, matching maskFix and the
	// unsigned-compare convention in evalBinaryInt — so UINT32_MAX is -1.
	unsigned := []struct {
		name  string
		v     float64
		width int8
		want  int64
	}{
		{"u32 in range", 42.9, 0, 42},
		{"u32 negative saturates to zero", -1.0, 0, 0},
		{"u32 overflow saturates to all-ones", 1e10, 0, -1},
		{"u32 NaN is zero", nan, 0, 0},
		{"u32 high but in range", 3000000000, 0, 3000000000 - (1 << 32)},
		{"u64 in range", 1e10, 64, 10000000000},
		{"u64 negative saturates to zero", -1.0, 64, 0},
		{"u64 overflow saturates to all-ones", 1e30, 64, -1},
		{"u64 NaN is zero", nan, 64, 0},
	}
	for _, c := range unsigned {
		t.Run("FToIU/"+c.name, func(t *testing.T) {
			if got := satFToIU(c.v, c.width); got != c.want {
				t.Errorf("satFToIU(%v, %d) = %d, want %d", c.v, c.width, got, c.want)
			}
		})
	}
}

// The same contract through the two compile-time evaluators that consume it:
// Fold (constant folding) and Eval (the oracle the backend tests diff against).
// Both used a bare Go conversion, so `(91.23e9) as i32` produced 1035689984
// where the interpreter and both native backends produce INT32_MAX.
func TestFoldAndEvalSaturateFloatToInt(t *testing.T) {
	build := func(v float64, width int8) (*Func, *Block) {
		f := NewFunc("f")
		e := f.NewBlock()
		x := f.AddOp(e, OpConstFloat)
		e.Ops[0].F64 = v
		r := f.AddOp(e, OpFToIS, x)
		e.Ops[1].Width = width
		f.SetRet(e, r)
		return f, e
	}

	t.Run("Fold", func(t *testing.T) {
		f, e := build(91.23e9, 0)
		Fold(f)
		if got := e.Ops[1]; got.Kind != OpConstInt || got.Imm != math.MaxInt32 {
			t.Errorf("fold = {%v %d}, want {OpConstInt %d}", got.Kind, got.Imm, int64(math.MaxInt32))
		}
	})

	t.Run("Eval", func(t *testing.T) {
		f, _ := build(91.23e9, 0)
		got, err := Eval(f)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		if got != math.MaxInt32 {
			t.Errorf("Eval = %d, want %d", got, int64(math.MaxInt32))
		}
	})

	t.Run("Eval_NaN", func(t *testing.T) {
		f, _ := build(math.NaN(), 0)
		got, err := Eval(f)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		if got != 0 {
			t.Errorf("Eval(NaN) = %d, want 0", got)
		}
	})
}
