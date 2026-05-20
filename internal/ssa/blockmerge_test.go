package ssa

import "testing"

// TestMergeTrivialBlocksLinearChain — A → B → X where B is
// empty + just br X. After merge: A → X, B unreachable.
func TestMergeTrivialBlocksLinearChain(t *testing.T) {
	f := NewFunc("f")
	a := f.NewBlock()
	b := f.NewBlock()
	x := f.NewBlock()
	f.SetBr(a, b)
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	MergeTrivialBlocks(f)

	if a.Term.Target != x {
		t.Errorf("A.Term.Target = %v, want X", a.Term.Target)
	}
	// X.Preds should now reference A, not B.
	if len(x.Preds) != 1 || x.Preds[0] != a {
		t.Errorf("X.Preds = %v, want [A]", x.Preds)
	}
	// B becomes unreachable. PruneUnreachable would drop it.
	if len(b.Preds) != 0 {
		t.Errorf("B.Preds = %v, want empty", b.Preds)
	}
}

// TestMergeTrivialBlocksSkipsWithOps — block with ops isn't a
// pure forwarder, leave alone.
func TestMergeTrivialBlocksSkipsWithOps(t *testing.T) {
	f := NewFunc("f")
	a := f.NewBlock()
	b := f.NewBlock()
	x := f.NewBlock()
	f.SetBr(a, b)
	f.AddOp(b, OpConstInt)
	b.Ops[0].Imm = 7
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	MergeTrivialBlocks(f)

	if a.Term.Target != b {
		t.Errorf("A should still target B (B has ops); got %v", a.Term.Target)
	}
}

// TestMergeTrivialBlocksSkipsEntry — never merge away the
// entry block.
func TestMergeTrivialBlocksSkipsEntry(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	x := f.NewBlock()
	f.SetBr(entry, x)
	f.SetRet(x, Value{})

	MergeTrivialBlocks(f)

	if f.Entry != entry {
		t.Errorf("Entry changed; should stay %v", entry)
	}
}

// TestMergeTrivialBlocksSkipsMultiPred — if B has multiple
// preds, don't merge (we'd need to handle phi-fold).
func TestMergeTrivialBlocksSkipsMultiPred(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	b := f.NewBlock() // 2-pred trivial block
	x := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, elseB)
	f.SetBr(thenB, b)
	f.SetBr(elseB, b)
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	MergeTrivialBlocks(f)

	// B should still be reached from both branches.
	if len(b.Preds) != 2 {
		t.Errorf("B.Preds = %d, want 2 (unchanged)", len(b.Preds))
	}
}

// TestMergeTrivialBlocksSkipsDuplicatePred — if A is already
// a pred of X (separate from going through B), merging would
// create duplicate preds. Skip.
func TestMergeTrivialBlocksSkipsDuplicatePred(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	a := f.NewBlock()
	b := f.NewBlock()
	x := f.NewBlock()
	// A → B (true) AND A → X (false) via brif
	f.SetBrIf(a, cond, b, x)
	f.SetBr(b, x)
	f.SetRet(x, Value{})

	MergeTrivialBlocks(f)

	// A is already a pred of X via the false branch — don't
	// rewrite A's true target.
	if a.Term.True != b {
		t.Errorf("A.True should still be B; got %v", a.Term.True)
	}
}

// TestMergeTrivialBlocksThroughBrIf — merge applies when A's
// brif targets B (in either True or False slot).
func TestMergeTrivialBlocksThroughBrIf(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	a := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	// thenB is a trivial forwarder
	target := f.NewBlock()
	f.SetBrIf(a, cond, thenB, elseB)
	f.SetBr(thenB, target)
	f.SetRet(elseB, Value{})
	f.SetRet(target, Value{})

	MergeTrivialBlocks(f)

	if a.Term.True != target {
		t.Errorf("A.True = %v, want target", a.Term.True)
	}
	if a.Term.False != elseB {
		t.Errorf("A.False = %v, want elseB (unchanged)", a.Term.False)
	}
}

// TestMergeTrivialBlocksNil — defensive.
func TestMergeTrivialBlocksNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-input panicked: %v", r)
		}
	}()
	MergeTrivialBlocks(nil)
}

// TestMergeTrivialBlocksInOptimize — end-to-end through the
// Optimize pipeline. The PruneUnreachable pass downstream
// should reclaim the now-orphan B.
func TestMergeTrivialBlocksInOptimize(t *testing.T) {
	f := NewFunc("f")
	a := f.NewBlock()
	b1 := f.NewBlock()
	b2 := f.NewBlock()
	b3 := f.NewBlock()
	x := f.NewBlock()
	// Long chain: A → b1 → b2 → b3 → X
	f.SetBr(a, b1)
	f.SetBr(b1, b2)
	f.SetBr(b2, b3)
	f.SetBr(b3, x)
	f.SetRet(x, Value{})

	Optimize(f)

	// After several Optimize iterations, only A → X should remain.
	if len(f.Blocks) != 2 {
		t.Errorf("Blocks = %d, want 2 (A + X); got block IDs %v",
			len(f.Blocks), blockIDs(f.Blocks))
	}
}

func blockIDs(bs []*Block) []int32 {
	ids := make([]int32, len(bs))
	for i, b := range bs {
		ids[i] = b.ID
	}
	return ids
}
