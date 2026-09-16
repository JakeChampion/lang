package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Division and remainder by a power of two run through the shift lowering
// the shared emitter produces, and agree with the interpreter on every sign.
func TestPowerOfTwoDivisionMatchesEval(t *testing.T) {
	for _, kind := range []ssa.OpKind{ssa.OpDiv, ssa.OpRem, ssa.OpDivU, ssa.OpRemU} {
		for _, a := range []int64{-9, -8, -1, 0, 7, 8, -2147483648, 2147483647} {
			f := ssa.NewFunc("f")
			p := f.AddParam()
			e := f.NewBlock()
			r := f.AddOp(e, kind, p, constOp(f, e, 8))
			e.Ops[len(e.Ops)-1].Width = 32
			f.SetRet(e, r)
			runMatchesEval(t, f, 8, a)
		}
	}
}

// Division and remainder by any other i32 constant run through the shared
// emitter's reciprocal lowering, and agree with the interpreter on every
// sign, including the unsigned dividends past 2^31 and the 33-bit magics.
func TestReciprocalDivisionMatchesEval(t *testing.T) {
	for _, kind := range []ssa.OpKind{ssa.OpDiv, ssa.OpRem, ssa.OpDivU, ssa.OpRemU} {
		for _, n := range []int64{3, 7, 10, 100, 641, -7, -100, 2147483647} {
			for _, a := range []int64{-9, -7, -1, 0, 1, 7, 99, 100, 101, -100, -2147483648, 2147483647} {
				f := ssa.NewFunc("f")
				p := f.AddParam()
				e := f.NewBlock()
				r := f.AddOp(e, kind, p, constOp(f, e, n))
				e.Ops[len(e.Ops)-1].Width = 32
				f.SetRet(e, r)
				runMatchesEval(t, f, 8, a)
			}
		}
	}
}
