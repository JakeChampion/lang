package ssa

import "testing"

// TestIsCommutative — pinned table for every OpKind that the
// peephole passes care about.
func TestIsCommutative(t *testing.T) {
	commutative := []OpKind{OpAdd, OpMul, OpAnd, OpOr, OpXor, OpEq, OpNe}
	for _, k := range commutative {
		if !IsCommutative(k) {
			t.Errorf("IsCommutative(%v) = false, want true", k)
		}
	}
	nonCommutative := []OpKind{OpSub, OpDiv, OpRem, OpShl, OpShr,
		OpLt, OpLe, OpGt, OpGe, OpNeg, OpNot, OpSelect,
		OpCall, OpLoad, OpStore, OpPhi,
		OpConstInt, OpConstBool, OpConstString}
	for _, k := range nonCommutative {
		if IsCommutative(k) {
			t.Errorf("IsCommutative(%v) = true, want false", k)
		}
	}
}

// TestIsPure — Call/Load/Store are impure; everything else
// (including Phi, which is structurally complex but
// side-effect-free) is pure.
func TestIsPure(t *testing.T) {
	impure := []OpKind{OpCall, OpLoad, OpStore}
	for _, k := range impure {
		if IsPure(k) {
			t.Errorf("IsPure(%v) = true, want false", k)
		}
	}
	pure := []OpKind{OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor,
		OpShl, OpShr, OpNeg, OpNot, OpSelect,
		OpEq, OpNe, OpLt, OpLe, OpGt, OpGe,
		OpConstInt, OpConstBool, OpConstString, OpPhi}
	for _, k := range pure {
		if !IsPure(k) {
			t.Errorf("IsPure(%v) = false, want true", k)
		}
	}
}

// TestIsConst — the four Const* kinds.
func TestIsConst(t *testing.T) {
	consts := []OpKind{OpConstInt, OpConstBool, OpConstString, OpConstFloat}
	for _, k := range consts {
		if !IsConst(k) {
			t.Errorf("IsConst(%v) = false, want true", k)
		}
	}
	for _, k := range []OpKind{OpAdd, OpEq, OpCall, OpPhi, OpInvalid} {
		if IsConst(k) {
			t.Errorf("IsConst(%v) = true, want false", k)
		}
	}
}

// TestIsComparison — every comparison kind across the
// signed/unsigned/float families. Negative cases keep generic
// arithmetic + control-flow ops out.
func TestIsComparison(t *testing.T) {
	cmps := []OpKind{
		OpEq, OpNe,
		OpLt, OpLtU, OpLe, OpLeU,
		OpGt, OpGtU, OpGe, OpGeU,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe,
	}
	for _, k := range cmps {
		if !IsComparison(k) {
			t.Errorf("IsComparison(%v) = false, want true", k)
		}
	}
	for _, k := range []OpKind{OpAdd, OpSub, OpNeg, OpNot, OpCall,
		OpConstBool, OpPhi, OpFAdd} {
		if IsComparison(k) {
			t.Errorf("IsComparison(%v) = true, want false", k)
		}
	}
}
