package ssa

import (
	"strings"
	"testing"
)

// TestNotFoldTrue — `not (const_bool 1)` → `const_bool 0`.
func TestNotFoldTrue(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	r := f.AddOp(entry, OpNot, c)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[1]; got.Kind != OpConstBool || got.Imm != 0 {
		t.Errorf("not(true) = {%v %d}, want {OpConstBool 0}", got.Kind, got.Imm)
	}
}

// TestNotFoldFalse — `not (const_bool 0)` → `const_bool 1`.
func TestNotFoldFalse(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpNot, c)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[1]; got.Kind != OpConstBool || got.Imm != 1 {
		t.Errorf("not(false) = {%v %d}, want {OpConstBool 1}", got.Kind, got.Imm)
	}
}

// TestNotLeavesNonConst — `not p` (Param) stays as OpNot.
func TestNotLeavesNonConst(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpNot, p)
	f.SetRet(entry, r)

	Fold(f)
	if entry.Ops[0].Kind != OpNot {
		t.Errorf("Kind = %v, want OpNot (non-const arg)", entry.Ops[0].Kind)
	}
}

// TestNotDoubleNegationSimplifies — `not (not x)` aliases
// directly to x.
func TestNotDoubleNegationSimplifies(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	inner := f.AddOp(entry, OpNot, x)
	outer := f.AddOp(entry, OpNot, inner)
	f.SetRet(entry, outer)

	Simplify(f)

	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (not(not(x)) → x)", entry.Term.Value, x)
	}
}

// TestNotInOptimizePipeline — end-to-end. A chain `not (not
// (eq c c))` where c is a const folds via constfold + simplify
// to const_bool 1.
func TestNotInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 5
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 5
	cmp := f.AddOp(entry, OpEq, a, b)  // → const_bool 1
	inner := f.AddOp(entry, OpNot, cmp) // → const_bool 0
	outer := f.AddOp(entry, OpNot, inner) // → const_bool 1
	f.SetRet(entry, outer)

	Optimize(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1 (one folded const); kinds %v",
			len(entry.Ops), opKinds(entry.Ops))
	}
	if got := entry.Ops[0]; got.Kind != OpConstBool || got.Imm != 1 {
		t.Errorf("survivor = {%v %d}, want {OpConstBool 1}", got.Kind, got.Imm)
	}
}

// TestNotPrints — Func.String renders `not v1` form.
func TestNotPrints(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpNot, p)
	f.SetRet(entry, r)

	got := f.String()
	wantSnippet := "v2 = not v1"
	if !strings.Contains(got, wantSnippet) {
		t.Errorf("Func.String() missing %q in:\n%s", wantSnippet, got)
	}
}
