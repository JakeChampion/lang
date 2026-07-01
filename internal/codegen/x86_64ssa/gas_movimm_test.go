package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A negative i32 constant must round-trip: `mov r64, imm32` sign-extends the
// immediate, so dropping the movsxd fixup on an in-range MovImm still matches the
// model's i32 sign-extension. Uses the value where the high bits are observed (an
// unsigned right shift) so a wrong (zero-extended) high half would change the
// result. (-16) as i32 = 0xFFFFFFF0; (>>u 1) = 0x7FFFFFF8; &0xFF = 0xF8 = 248.
func TestAsmRunNegConstSignExtend(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		neg := constOp(f, e, -16)
		sh := f.AddOp(e, ssa.OpShrU, neg, constOp(f, e, 1))
		f.SetRet(e, f.AddOp(e, ssa.OpAnd, sh, constOp(f, e, 0xff)))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}
