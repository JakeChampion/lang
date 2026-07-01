package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// neg builds 0 - v so tests can form negative operands without relying on
// negative immediates in the _start shim.
func negOp(f *ssa.Func, b *ssa.Block, v ssa.Value) ssa.Value {
	return f.AddOp(b, ssa.OpSub, constOp(f, b, 0), v)
}

// Signed/unsigned div and rem run natively via the idiv/div fixed-register
// (rdx:rax) sequence, diffed against Eval. Includes a negative dividend to
// exercise cqo + idiv sign handling.
func TestAsmRunDivRem(t *testing.T) {
	// f(a,b) = a <k> b
	bin := func(k ssa.OpKind) *ssa.Func {
		f := ssa.NewFunc("d")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, k, a, b))
		return f
	}
	// f(a,b) = (-a) <k> b — negative dividend built inside the function.
	binNeg := func(k ssa.OpKind) *ssa.Func {
		f := ssa.NewFunc("d")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, k, negOp(f, e, a), b))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, bin(ssa.OpDiv), n, []int64{20, 3})    // 6
		runMatchesEvalArgs(t, bin(ssa.OpRem), n, []int64{20, 3})    // 2
		runMatchesEvalArgs(t, binNeg(ssa.OpDiv), n, []int64{20, 3}) // -6
		runMatchesEvalArgs(t, binNeg(ssa.OpRem), n, []int64{20, 3}) // -2
		runMatchesEvalArgs(t, bin(ssa.OpDivU), n, []int64{200, 7})  // 28
		runMatchesEvalArgs(t, bin(ssa.OpRemU), n, []int64{200, 7})  // 4
	}
}

// The div operands must survive the idiv's rax/rdx clobber: (a/b)+a+b.
func TestAsmRunDivOperandsLive(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("d")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		q := f.AddOp(e, ssa.OpDiv, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, q, a), b))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{20, 3}) // 6+20+3 = 29
	}
}

// Variable shifts run via the cl fixed-register sequence, diffed against Eval.
// sar (OpShr) vs shr (OpShrU) is distinguished by a negative left operand.
func TestAsmRunShifts(t *testing.T) {
	// f(a,b) = a <k> b
	bin := func(k ssa.OpKind) *ssa.Func {
		f := ssa.NewFunc("s")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, k, a, b))
		return f
	}
	// f(a,b) = (-a) <k> b
	binNeg := func(k ssa.OpKind) *ssa.Func {
		f := ssa.NewFunc("s")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, k, negOp(f, e, a), b))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, bin(ssa.OpShl), n, []int64{3, 4})      // 48
		runMatchesEvalArgs(t, bin(ssa.OpShr), n, []int64{200, 2})    // 50
		runMatchesEvalArgs(t, binNeg(ssa.OpShr), n, []int64{16, 2})  // -16 >> 2 = -4 (sar)
		runMatchesEvalArgs(t, binNeg(ssa.OpShrU), n, []int64{16, 2}) // (uint64 -16) >> 2 (shr)
	}
}

// A shift count in a param (arg register) that also aliases rcx across the op:
// keep the count live after the shift so rcx must be preserved.
func TestAsmRunShiftCountLive(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		sh := f.AddOp(e, ssa.OpShl, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, sh, b)) // b live past the shift
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{5, 3}) // (5<<3)+3 = 43
	}
}
