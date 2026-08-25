package ssa

import "testing"

// CSE keys on Width as well as Kind and Args. Two ops of the same kind over the
// same operand compute DIFFERENT values at 32 and at 64 — clz(1) is 31 or 63 —
// so a key without Width merges them and silently rewrites the answer.
func TestCSEKeepsWidthsApart(t *testing.T) {
	f := NewFunc("f")
	e := f.NewBlock()
	one := f.AddOp(e, OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 1

	narrow := f.AddOp(e, OpClz, one) // Width 0 == i32
	wide := f.AddOp(e, OpClz, one)
	e.Ops[len(e.Ops)-1].Width = 64
	f.SetRet(e, f.AddOp(e, OpAdd, narrow, wide))

	before, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if before != 31+63 {
		t.Fatalf("Eval = %d, want %d (clz32(1) + clz64(1))", before, 31+63)
	}
	CSE(f)
	after, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval after CSE: %v", err)
	}
	if after != before {
		t.Errorf("CSE changed the answer: %d -> %d — the two clz ops differ in Width "+
			"and must not be merged", before, after)
	}
}
