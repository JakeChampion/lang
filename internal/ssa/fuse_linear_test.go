package ssa

import "testing"

// TestFuseLinearBlocksAppendsOps — A → B linear, A has 1 op,
// B has 1 op. After fuse: A has 2 ops + B's terminator.
func TestFuseLinearBlocksAppendsOps(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	a := f.NewBlock()
	b := f.NewBlock()
	x := f.NewBlock()
	f.AddOp(a, OpAdd, p, p) // op #1
	f.SetBr(a, b)
	f.AddOp(b, OpMul, p, p) // op #2
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	FuseLinearBlocks(f)

	if len(a.Ops) != 2 {
		t.Fatalf("A.Ops = %d, want 2 (merged from B)", len(a.Ops))
	}
	if a.Ops[0].Kind != OpAdd || a.Ops[1].Kind != OpMul {
		t.Errorf("op order = [%v %v], want [OpAdd OpMul]",
			a.Ops[0].Kind, a.Ops[1].Kind)
	}
	if a.Term.Target != x {
		t.Errorf("A.Term.Target = %v, want X", a.Term.Target)
	}
	// B becomes orphan.
	if len(b.Ops) != 0 {
		t.Errorf("B.Ops = %v, want empty", b.Ops)
	}
}

// TestFuseLinearBlocksUpdatesSuccessorPreds — when B is fused
// into A, B's successors' Preds must update to reference A.
func TestFuseLinearBlocksUpdatesSuccessorPreds(t *testing.T) {
	f := NewFunc("f")
	a := f.NewBlock()
	b := f.NewBlock()
	x := f.NewBlock()
	f.SetBr(a, b)
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	FuseLinearBlocks(f)

	if len(x.Preds) != 1 || x.Preds[0] != a {
		t.Errorf("X.Preds = %v, want [A]", x.Preds)
	}
}

// TestFuseLinearBlocksSkipsMultiPred — if B has multiple preds,
// fusion would orphan the other preds; skip.
func TestFuseLinearBlocksSkipsMultiPred(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(a, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	FuseLinearBlocks(f)

	// merge has 2 preds [thenB, elseB] — should NOT fuse into
	// either branch.
	if len(merge.Preds) != 2 {
		t.Errorf("merge.Preds = %d, want 2 (unchanged)", len(merge.Preds))
	}
}

// TestFuseLinearBlocksSkipsPhiBlock — if B has phi ops, fusing
// is incorrect (phi.Args[0] references B's pred, not A's).
// Leave it to TrivialPhis first.
func TestFuseLinearBlocksSkipsPhiBlock(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	a := f.NewBlock()
	b := f.NewBlock()
	x := f.NewBlock()
	f.SetBr(a, b)
	// Inject a phi at B even though B has only 1 pred — to
	// simulate the case where TrivialPhis hasn't run yet.
	f.AddPhi(b, p)
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	FuseLinearBlocks(f)

	if a.Term.Target != b {
		t.Errorf("A.Term.Target = %v, want B (skipped due to phi)", a.Term.Target)
	}
}

// TestFuseLinearBlocksSkipsEntryTarget — don't fuse a block
// whose target is the function's entry block (would orphan
// the entry pointer). Manually flip f.Entry to set up the
// shape — under normal construction the iteration order
// would already have fused entry's outgoing edge first.
func TestFuseLinearBlocksSkipsEntryTarget(t *testing.T) {
	f := NewFunc("f")
	pred := f.NewBlock()
	entry := f.NewBlock()
	f.Entry = entry
	f.SetBr(pred, entry)

	FuseLinearBlocks(f)

	if pred.Term.Target != entry {
		t.Errorf("pred should still target entry (entry can't be fused away)")
	}
}

// TestFuseLinearBlocksNil — defensive.
func TestFuseLinearBlocksNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil panic: %v", r)
		}
	}()
	FuseLinearBlocks(nil)
}

// TestFuseLinearBlocksInOptimize — end-to-end. A 3-block chain
// fuses into 1 after Optimize.
func TestFuseLinearBlocksInOptimize(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	a := f.NewBlock()
	b := f.NewBlock()
	c := f.NewBlock()
	f.AddOp(a, OpAdd, p, p)
	f.SetBr(a, b)
	f.AddOp(b, OpMul, p, p)
	f.SetBr(b, c)
	f.AddOp(c, OpSub, p, p)
	f.SetRet(c, Value{})

	Optimize(f)

	if len(f.Blocks) != 1 {
		t.Errorf("Blocks = %d, want 1 (fused); kinds = %v",
			len(f.Blocks), blockOpKinds(f.Blocks))
	}
}

func blockOpKinds(bs []*Block) []OpKind {
	var all []OpKind
	for _, b := range bs {
		for _, op := range b.Ops {
			all = append(all, op.Kind)
		}
	}
	return all
}
