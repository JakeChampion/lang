package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// i32-width results are sign-extended back into the full register (mirroring
// the model's maskW), so an i32 op whose 64-bit computation would leave nonzero
// high bits matches Eval once a later unsigned op observes those bits.
//
// mul(0x10000, 0x10000) = 0x1_0000_0000 — at i32 width the low 32 bits are 0,
// so shrU by 32 must yield 0. Without the width fix the 64-bit product keeps
// bit 32 and shrU-32 would yield 1, so this pins the fix.
func TestAsmRunI32WidthShrU(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("w")
		x := f.AddParam()
		y := f.AddParam()
		s := f.AddParam()
		e := f.NewBlock()
		m := f.AddOp(e, ssa.OpMul, x, y)
		setLastWidth(e, 32) // i32 multiply: high bits masked off
		r := f.AddOp(e, ssa.OpShrU, m, s)
		setLastWidth(e, 32)
		f.SetRet(e, r)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{0x10000, 0x10000, 32})
	}
}

// An i32 negation whose sign bit is set: -(1) at i32 width is 0xFFFFFFFF, and
// an unsigned shift observes the sign-extended high bits exactly as the model.
// (-x) >>u 1: model uint64(int32(-x)) >> 1.
func TestAsmRunI32WidthNegShrU(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("w")
		x := f.AddParam()
		s := f.AddParam()
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpNeg, x)
		setLastWidth(e, 32)
		r := f.AddOp(e, ssa.OpShrU, n, s)
		setLastWidth(e, 32)
		f.SetRet(e, r)
		return f
	}
	for _, na := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), na, []int64{1, 4})  // (-1)>>u4
		runMatchesEvalArgs(t, build(), na, []int64{5, 2})  // (-5)>>u2
		runMatchesEvalArgs(t, build(), na, []int64{3, 28}) // (-3)>>u28
	}
}
