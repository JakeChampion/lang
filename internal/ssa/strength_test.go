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

// TestStrengthNilFunc — defensive.
func TestStrengthNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StrengthReduce(nil) panicked: %v", r)
		}
	}()
	StrengthReduce(nil)
}
