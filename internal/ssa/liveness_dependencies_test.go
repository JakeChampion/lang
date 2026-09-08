package ssa

import "testing"

func TestLivenessDependenciesExtendTerminatorAndPhiUses(t *testing.T) {
	f := NewFunc("dependent")
	cond := f.AddParam()
	entry, yes, no, join := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(entry, cond, yes, no)
	owner := f.AddOp(yes, OpConstInt)
	borrow := f.AddOp(yes, OpAdd, owner, owner)
	other := f.AddOp(no, OpConstInt)
	f.SetBr(yes, join)
	f.SetBr(no, join)
	result := f.AddPhi(join, borrow, other)
	f.SetRet(join, result)
	l := ComputeLivenessWithDependencies(f, map[int32][]Value{borrow.ID: {owner}})
	if !l.LiveOut[yes][owner.ID] || l.LiveOut[no][owner.ID] || l.LiveIn[join][owner.ID] {
		t.Fatal("phi dependencies must stay on their corresponding predecessor edge")
	}
	// Change the CFG to carry the same dependent value to a later return.
	g := NewFunc("return_dependent")
	first, last := g.NewBlock(), g.NewBlock()
	parent := g.AddOp(first, OpConstInt)
	child := g.AddOp(first, OpAdd, parent, parent)
	g.SetBr(first, last)
	g.SetRet(last, child)
	ordinary := ComputeLiveness(g)
	extended := ComputeLivenessWithDependencies(g, map[int32][]Value{child.ID: {parent}})
	if ordinary.LiveOut[first][parent.ID] || !extended.LiveOut[first][parent.ID] || !extended.LiveIn[last][parent.ID] {
		t.Fatal("return lost its additional lifetime dependency")
	}
	if !ordinary.LiveOut[first][child.ID] || !extended.LiveOut[first][child.ID] {
		t.Fatal("additional dependencies erased the original value use")
	}
}
