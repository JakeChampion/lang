package ssa

import "testing"

// TestStrengthSubSelfFoldsToZero — `x - x` synthesises
// const_int 0 in place.
func TestStrengthSubSelfFoldsToZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpSub, x, x)
	f.SetRet(entry, r)

	StrengthReduce(f)

	if entry.Ops[0].Kind != OpConstInt {
		t.Errorf("Kind = %v, want OpConstInt", entry.Ops[0].Kind)
	}
	if entry.Ops[0].Imm != 0 {
		t.Errorf("Imm = %d, want 0", entry.Ops[0].Imm)
	}
	if entry.Ops[0].Result != r {
		t.Errorf("Result changed; downstream uses would break")
	}
}

// TestStrengthMulByZeroRight — `x * 0` → const_int 0.
func TestStrengthMulByZeroRight(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpMul, x, zero)
	f.SetRet(entry, r)

	StrengthReduce(f)

	if entry.Ops[1].Kind != OpConstInt || entry.Ops[1].Imm != 0 {
		t.Errorf("Op = {%v %d}, want {OpConstInt 0}", entry.Ops[1].Kind, entry.Ops[1].Imm)
	}
}

// TestStrengthMulByZeroLeft — `0 * x` → const_int 0.
func TestStrengthMulByZeroLeft(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpMul, zero, x)
	f.SetRet(entry, r)

	StrengthReduce(f)

	if entry.Ops[1].Kind != OpConstInt || entry.Ops[1].Imm != 0 {
		t.Errorf("Op = {%v %d}, want {OpConstInt 0}", entry.Ops[1].Kind, entry.Ops[1].Imm)
	}
}

// TestStrengthLeavesGeneric — Add, Div, regular Sub with
// distinct operands all left alone.
func TestStrengthLeavesGeneric(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	f.AddOp(entry, OpAdd, a, b)
	f.AddOp(entry, OpSub, a, b)
	f.AddOp(entry, OpDiv, a, b)
	f.SetRet(entry, Value{})

	StrengthReduce(f)

	wantKinds := []OpKind{OpAdd, OpSub, OpDiv}
	for i, k := range wantKinds {
		if entry.Ops[i].Kind != k {
			t.Errorf("Ops[%d].Kind = %v, want %v (unchanged)", i, entry.Ops[i].Kind, k)
		}
	}
}

// TestStrengthDivSelfLeftAlone — `x / x` is NOT yet
// strength-reduced (we can't prove x != 0). Verify the safe
// behaviour.
func TestStrengthDivSelfLeftAlone(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpDiv, x, x)
	f.SetRet(entry, r)

	StrengthReduce(f)

	if entry.Ops[0].Kind != OpDiv {
		t.Errorf("Kind = %v, want OpDiv (Phase 1 doesn't fold x/x)", entry.Ops[0].Kind)
	}
}

// TestStrengthComposesWithOptimize — end-to-end via Optimize.
// `(x - x) + (a * 0)` collapses to const_int 0.
func TestStrengthComposesWithOptimize(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	a := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	subSelf := f.AddOp(entry, OpSub, x, x)
	mulZero := f.AddOp(entry, OpMul, a, zero)
	sum := f.AddOp(entry, OpAdd, subSelf, mulZero)
	f.SetRet(entry, sum)

	Optimize(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1 (one folded const 0); kinds %v",
			len(entry.Ops), opKinds(entry.Ops))
	}
	if entry.Ops[0].Kind != OpConstInt || entry.Ops[0].Imm != 0 {
		t.Errorf("survivor = {%v %d}, want {OpConstInt 0}",
			entry.Ops[0].Kind, entry.Ops[0].Imm)
	}
}

// TestStrengthCmpSelfTrue — `x == x`, `x <= x`, `x >= x` all
// fold to const_bool true regardless of x's runtime value
// (integers can't be NaN; safe).
func TestStrengthCmpSelfTrue(t *testing.T) {
	cases := []OpKind{OpEq, OpLe, OpLeU, OpGe, OpGeU}
	for _, k := range cases {
		f := NewFunc("f")
		x := f.AddParam()
		entry := f.NewBlock()
		r := f.AddOp(entry, k, x, x)
		f.SetRet(entry, r)

		StrengthReduce(f)

		if entry.Ops[0].Kind != OpConstBool {
			t.Errorf("%v: Kind = %v, want OpConstBool", k, entry.Ops[0].Kind)
		}
		if entry.Ops[0].Imm != 1 {
			t.Errorf("%v: Imm = %d, want 1 (true)", k, entry.Ops[0].Imm)
		}
		if entry.Ops[0].Result != r {
			t.Errorf("%v: Result changed; downstream uses would break", k)
		}
	}
}

// TestStrengthCmpSelfFalse — `x != x`, `x < x`, `x > x` all
// fold to const_bool false.
func TestStrengthCmpSelfFalse(t *testing.T) {
	cases := []OpKind{OpNe, OpLt, OpLtU, OpGt, OpGtU}
	for _, k := range cases {
		f := NewFunc("f")
		x := f.AddParam()
		entry := f.NewBlock()
		r := f.AddOp(entry, k, x, x)
		f.SetRet(entry, r)

		StrengthReduce(f)

		if entry.Ops[0].Kind != OpConstBool {
			t.Errorf("%v: Kind = %v, want OpConstBool", k, entry.Ops[0].Kind)
		}
		if entry.Ops[0].Imm != 0 {
			t.Errorf("%v: Imm = %d, want 0 (false)", k, entry.Ops[0].Imm)
		}
		if entry.Ops[0].Result != r {
			t.Errorf("%v: Result changed; downstream uses would break", k)
		}
	}
}

// TestStrengthCmpSelfDistinctOperandsUntouched — `a == b`
// (different params) must NOT be folded.
func TestStrengthCmpSelfDistinctOperandsUntouched(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	for _, k := range []OpKind{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe} {
		f.AddOp(entry, k, a, b)
	}
	f.SetRet(entry, Value{})

	StrengthReduce(f)

	wantKinds := []OpKind{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}
	for i, k := range wantKinds {
		if entry.Ops[i].Kind != k {
			t.Errorf("Ops[%d].Kind = %v, want %v (distinct operands, unchanged)",
				i, entry.Ops[i].Kind, k)
		}
	}
}

// TestStrengthFCmpSelfUntouched — float self-compare is NOT
// folded: NaN compares unequal to itself, so `x == x` is not
// always true for floats. Verify safe behaviour.
func TestStrengthFCmpSelfUntouched(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	for _, k := range []OpKind{OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe} {
		f.AddOp(entry, k, x, x)
	}
	f.SetRet(entry, Value{})

	StrengthReduce(f)

	wantKinds := []OpKind{OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe}
	for i, k := range wantKinds {
		if entry.Ops[i].Kind != k {
			t.Errorf("Ops[%d].Kind = %v, want %v (float self-compare unsafe to fold)",
				i, entry.Ops[i].Kind, k)
		}
	}
}

// TestStrengthOrAllSetRight — `x | -1` synthesises const_int
// -1 (all bits set absorbs the operand).
func TestStrengthOrAllSetRight(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	allSet := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -1
	r := f.AddOp(entry, OpOr, x, allSet)
	f.SetRet(entry, r)
	_ = r

	StrengthReduce(f)

	if entry.Ops[1].Kind != OpConstInt || entry.Ops[1].Imm != -1 {
		t.Errorf("Op = {%v %d}, want {OpConstInt -1}",
			entry.Ops[1].Kind, entry.Ops[1].Imm)
	}
}

// TestStrengthOrAllSetLeft — `-1 | x` synthesises const_int -1.
func TestStrengthOrAllSetLeft(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	allSet := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -1
	r := f.AddOp(entry, OpOr, allSet, x)
	f.SetRet(entry, r)
	_ = r

	StrengthReduce(f)

	if entry.Ops[1].Kind != OpConstInt || entry.Ops[1].Imm != -1 {
		t.Errorf("Op = {%v %d}, want {OpConstInt -1}",
			entry.Ops[1].Kind, entry.Ops[1].Imm)
	}
}

// TestStrengthRemByOne — `x % 1` synthesises const_int 0.
// Holds for both signed (OpRem) and unsigned (OpRemU).
func TestStrengthRemByOne(t *testing.T) {
	for _, k := range []OpKind{OpRem, OpRemU} {
		t.Run(k.String(), func(t *testing.T) {
			f := NewFunc("f")
			x := f.AddParam()
			entry := f.NewBlock()
			one := f.AddOp(entry, OpConstInt)
			entry.Ops[0].Imm = 1
			r := f.AddOp(entry, k, x, one)
			f.SetRet(entry, r)
			_ = r

			StrengthReduce(f)

			if entry.Ops[1].Kind != OpConstInt || entry.Ops[1].Imm != 0 {
				t.Errorf("Op = {%v %d}, want {OpConstInt 0}",
					entry.Ops[1].Kind, entry.Ops[1].Imm)
			}
		})
	}
}

// TestStrengthRemByOneOnLeftUntouched — `1 % x` is NOT an
// identity (depends on x), so it must NOT be folded.
func TestStrengthRemByOneOnLeftUntouched(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	f.AddOp(entry, OpRem, one, x)
	f.SetRet(entry, Value{})

	StrengthReduce(f)

	if entry.Ops[1].Kind != OpRem {
		t.Errorf("Kind = %v, want OpRem (LHS=1 isn't identity)",
			entry.Ops[1].Kind)
	}
}

// TestStrengthZeroSubToNeg — `0 - x` rewrites in place from
// OpSub to OpNeg. Result Value preserved; the const_int 0
// arg is dropped, leaving Args = [x].
func TestStrengthZeroSubToNeg(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpSub, zero, x)
	f.SetRet(entry, r)

	StrengthReduce(f)

	sub := entry.Ops[1]
	if sub.Kind != OpNeg {
		t.Errorf("Kind = %v, want OpNeg", sub.Kind)
	}
	if len(sub.Args) != 1 || sub.Args[0] != x {
		t.Errorf("Args = %v, want [%v]", sub.Args, x)
	}
	if sub.Result != r {
		t.Error("Result changed; downstream uses would break")
	}
}

// TestStrengthZeroSubLHSNotConst — `0 - x` where the LHS isn't
// literally const_int 0 is left alone (e.g. a param with value
// 0 at runtime — we can't fold without value-tracking).
func TestStrengthZeroSubLHSNotConst(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	f.AddOp(entry, OpSub, a, b)
	f.SetRet(entry, Value{})

	StrengthReduce(f)

	if entry.Ops[0].Kind != OpSub {
		t.Errorf("Kind = %v, want OpSub (lhs param, untouched)",
			entry.Ops[0].Kind)
	}
}

// TestStrengthNilFunc — defensive.
func TestStrengthNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StrengthReduce(nil) panicked: %v", r)
		}
	}()
	StrengthReduce(nil)
}
