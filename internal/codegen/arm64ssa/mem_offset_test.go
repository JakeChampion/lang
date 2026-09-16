package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// store8 / load8 are the byte-wide siblings of storeOp / loadOp.
func store8(f *ssa.Func, b *ssa.Block, base, val ssa.Value, offset int64) {
	op := f.AddOpNoResult(b, ssa.OpStore8, base, val)
	op.Imm = offset
}

// A load or store whose immediate offset the instruction cannot carry (past
// 4095 units of the access size, or past the unscaled -256..255 when it is
// not a multiple of the size) is addressed through a computed base instead
// of being emitted as text the assembler rejects. Each offset here is one
// the forms cannot encode: a byte at 4999 and 70000, a word at 4004 (not a
// multiple of 8 and past the unscaled range), a word at 40000 and 1 MiB, and
// a word 300 bytes below the base.
func TestArmRunMemoryOffsetsOutsideTheImmediateRange(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	block := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 2<<20))
	base := f.AddOp(e, ssa.OpAdd, block, constOp(f, e, 512))
	sum := constOp(f, e, 0)
	for i, off := range []int64{4999, 70000, 4004, 40000, 1 << 20, -300} {
		v := constOp(f, e, int64(i)+3)
		if off == 4999 || off == 70000 {
			store8(f, e, base, v, off)
			sum = f.AddOp(e, ssa.OpAdd, sum, load8(f, e, base, off))
			continue
		}
		storeOp(f, e, base, v, off)
		sum = f.AddOp(e, ssa.OpAdd, sum, loadOp(f, e, base, off))
	}
	f.SetRet(e, sum)
	runMatchesEval(t, f, 8) // 3+4+5+6+7+8 = 33
}
