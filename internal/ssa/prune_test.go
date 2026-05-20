package ssa

import "testing"

// TestPruneUnreachableDropsDeadBlock — entry brifs to A and B
// on const_bool=1 (always take A). FoldBranches drops the
// entry→B edge. B is now unreachable; PruneUnreachable
// removes it.
func TestPruneUnreachableDropsDeadBlock(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	A := f.NewBlock()
	B := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, A, B)
	f.SetRet(A, Value{})
	f.SetRet(B, Value{})

	FoldBranches(f)
	removed := PruneUnreachable(f)

	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	for _, b := range f.Blocks {
		if b == B {
			t.Errorf("B still in f.Blocks: %v", f.Blocks)
		}
	}
}

// TestPruneCleansSuccessorPreds — dead block had an outgoing
// edge into a live merge block. After prune, merge.Preds no
// longer mentions the dead block.
func TestPruneCleansSuccessorPreds(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	A := f.NewBlock()
	dead := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1 // always take A
	preVal := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 7
	f.SetBrIf(entry, c, A, dead)
	f.SetBr(A, merge)
	dv := f.AddOp(dead, OpConstInt)
	dead.Ops[0].Imm = 9
	f.SetBr(dead, merge)
	// merge.Preds will be [A, dead] post-build
	// (SetBr(A, merge) appends A first, SetBr(dead, merge) then dead).
	phi := f.AddPhi(merge, preVal, dv)
	_ = phi
	f.SetRet(merge, phi)

	FoldBranches(f)
	PruneUnreachable(f)

	if len(merge.Preds) != 1 || merge.Preds[0] != A {
		t.Errorf("merge.Preds = %v, want [A]", merge.Preds)
	}
	// The phi should now have one arg matching merge.Preds.
	if len(merge.Ops[0].Args) != 1 {
		t.Errorf("phi.Args = %v, want length 1", merge.Ops[0].Args)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after prune: %v", err)
	}
}

// TestPruneKeepsReachableBlocks — control: reachable blocks
// must survive the pass.
func TestPruneKeepsReachableBlocks(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	removed := PruneUnreachable(f)
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (all reachable)", removed)
	}
	if len(f.Blocks) != 4 {
		t.Errorf("Blocks len = %d, want 4", len(f.Blocks))
	}
}

// TestPruneLoopReachable — loop header + body reachable via
// back-edge; nothing pruned.
func TestPruneLoopReachable(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	f.SetBrIf(header, c, body, done)
	f.SetBr(body, header) // back-edge
	f.SetRet(done, Value{})

	if got := PruneUnreachable(f); got != 0 {
		t.Errorf("removed = %d, want 0", got)
	}
}

// TestPruneInOptimizePipeline — end-to-end via Optimize. The
// dead branch is gone after the full pipeline run.
func TestPruneInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	A := f.NewBlock()
	dead := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, A, dead)
	f.SetRet(A, Value{})
	f.SetRet(dead, Value{})

	Optimize(f)

	for _, b := range f.Blocks {
		if b == dead {
			t.Errorf("dead block survived Optimize: %v", f.Blocks)
		}
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after Optimize: %v", err)
	}
}

// TestPruneNilFunc — defensive nil-input.
func TestPruneNilFunc(t *testing.T) {
	if got := PruneUnreachable(nil); got != 0 {
		t.Errorf("PruneUnreachable(nil) = %d, want 0", got)
	}
}
