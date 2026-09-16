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
