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

// Rotate right shares the shifts' count-in-cl sequence, so it inherits their
// width rule: at i32 the operand and the count must both be the 32-bit form.
// 0x80000005 rotated right by 28 is 0x58 under `ror eax` and 0x50 under
// `ror rax` — which would rotate through the cleared high half — and the exit
// code is the low byte, so the two are distinguishable. Diffed against Eval.
func TestAsmRunRotr(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("r")
		a := f.AddParam()
		n := f.AddParam()
		e := f.NewBlock()
		hi := f.AddOp(e, ssa.OpShl, a, constOp(f, e, 31))
		x := f.AddOp(e, ssa.OpOr, hi, a) // 0x80000005 from a = 5
		f.SetRet(e, f.AddOp(e, ssa.OpRotr, x, n))
		return f
	}
	// The count kept live past the rotate: rcx is preserved around the sequence.
	countLive := func() *ssa.Func {
		f := ssa.NewFunc("r")
		a := f.AddParam()
		n := f.AddParam()
		e := f.NewBlock()
		r := f.AddOp(e, ssa.OpRotr, a, n)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, r, n))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{5, 28})     // 0x58 = 88
		runMatchesEvalArgs(t, countLive(), n, []int64{96, 2}) // (96 ror 2) + 2 = 26
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
