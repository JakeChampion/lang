package arm64ssa_test

import (
	"math"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Float-to-int conversion saturates: NaN → 0, out of range → the destination's
// min/max (docs/FLOAT-SEMANTICS.md).
//
// AArch64's fcvtz{s,u} already saturate — but to the width of the DESTINATION
// REGISTER. The renderer converted into `x` and then narrowed with maskFix,
// which saturates to the 64-bit range and sign-extends bit 31 of that: that is
// wraparound, not saturation. `(91.23f32 * 1e9) as i32` came out as 1035689984
// where the interpreter and both native backends give INT32_MAX. Caught by
// sweeping the fernsmith printable corpus through `-target arm64-ssa` past the
// seed range CI runs.
//
// ssa.Eval is the oracle, as everywhere in this package; it saturates too.
func TestArmRunFloatToIntSaturates(t *testing.T) {
	build := func(k ssa.OpKind, v float64, width int8) *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		r := f.AddOp(e, k, constFloat(f, e, v))
		e.Ops[len(e.Ops)-1].Width = width
		f.SetRet(e, r)
		return f
	}
	cases := []struct {
		name  string
		kind  ssa.OpKind
		v     float64
		width int8
	}{
		{"i32_overflow", ssa.OpFToIS, 91.23e9, 0},
		{"i32_underflow", ssa.OpFToIS, -91.23e9, 0},
		{"i32_pos_inf", ssa.OpFToIS, math.Inf(1), 0},
		{"i32_neg_inf", ssa.OpFToIS, math.Inf(-1), 0},
		{"i32_nan", ssa.OpFToIS, math.NaN(), 0},
		{"i32_in_range", ssa.OpFToIS, 42.9, 0},
		{"i64_overflow", ssa.OpFToIS, 1e30, 64},
		{"i64_nan", ssa.OpFToIS, math.NaN(), 64},
		{"u32_overflow", ssa.OpFToIU, 91.23e9, 0},
		{"u32_negative", ssa.OpFToIU, -1.0, 0},
		{"u32_nan", ssa.OpFToIU, math.NaN(), 0},
		{"u32_in_range", ssa.OpFToIU, 42.9, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, n := range []int{1, 2, 8} {
				runMatchesEval(t, build(c.kind, c.v, c.width), n)
			}
		})
	}
}
