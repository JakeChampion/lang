package ssa

import "testing"

// TestFoldBranchesTrue — brif (const_bool 1) collapses to
// br on the True target. False target drops its pred.
func TestFoldBranchesTrue(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	FoldBranches(f)

	if entry.Term.Kind != TermBr {
		t.Fatalf("entry.Term.Kind = %v, want TermBr", entry.Term.Kind)
	}
	if entry.Term.Target != thenB {
		t.Errorf("entry.Term.Target = %v, want thenB", entry.Term.Target)
	}
	if len(elseB.Preds) != 0 {
		t.Errorf("elseB.Preds = %v, want empty after elim", elseB.Preds)
	}
	if len(thenB.Preds) != 1 || thenB.Preds[0] != entry {
		t.Errorf("thenB.Preds = %v, want [entry]", thenB.Preds)
	}
}

// TestFoldBranchesFalse — brif (const_bool 0) collapses to
// br on the False target.
func TestFoldBranchesFalse(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 0
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	FoldBranches(f)

	if entry.Term.Target != elseB {
		t.Errorf("Target = %v, want elseB", entry.Term.Target)
	}
	if len(thenB.Preds) != 0 {
		t.Errorf("thenB.Preds = %v, want empty", thenB.Preds)
	}
}

// TestFoldBranchesRedundantSameTarget — brif True==False
// collapses to br regardless of cond.
func TestFoldBranchesRedundantSameTarget(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	target := f.NewBlock()
	f.SetBrIf(entry, c, target, target)
	f.SetRet(target, Value{})

	FoldBranches(f)

	if entry.Term.Kind != TermBr || entry.Term.Target != target {
		t.Errorf("Term = %+v, want Br→target", entry.Term)
	}
	if len(target.Preds) != 1 || target.Preds[0] != entry {
		t.Errorf("target.Preds = %v, want [entry]", target.Preds)
	}
}

// TestFoldBranchesUntouchedOnNonConst — non-const cond is
// left alone. No silent rewrites.
func TestFoldBranchesUntouchedOnNonConst(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	FoldBranches(f)

	if entry.Term.Kind != TermBrIf {
		t.Errorf("Term.Kind = %v, want TermBrIf (no const)", entry.Term.Kind)
	}
}

// TestFoldBranchesDropsPhiArg — eliminating an inbound edge
// at a merge block drops the parallel phi arg slot. Shape:
// entry brifs DIRECTLY to merge (False) and to A (True);
// A brs to merge. After folding True-branch, entry→merge
// goes away and merge's phi loses its entry-side arg.
func TestFoldBranchesDropsPhiArg(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	A := f.NewBlock()
	merge := f.NewBlock()

	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1 // always take A
	// entry value that the merge-phi will reference from the False path.
	preVal := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 99
	f.SetBrIf(entry, c, A, merge)

	aVal := f.AddOp(A, OpConstInt)
	A.Ops[0].Imm = 1
	f.SetBr(A, merge)

	// SetBrIf called before SetBr, so merge.Preds order is
	// [entry, A]. Phi args must follow Preds order: from entry
	// use preVal, from A use aVal.
	phi := f.AddPhi(merge, preVal, aVal)
	_ = phi
	f.SetRet(merge, phi)

	// Sanity check pre-conditions: merge.Preds should have 2 entries
	// and the phi has 2 args matching them.
	if len(merge.Preds) != 2 {
		t.Fatalf("pre-fold: merge.Preds = %v, want 2", merge.Preds)
	}

	FoldBranches(f)

	// entry no longer flows to merge.
	if len(merge.Preds) != 1 || merge.Preds[0] != A {
		t.Errorf("merge.Preds = %v, want [A]", merge.Preds)
	}
	phiOp := merge.Ops[0]
	if phiOp.Kind != OpPhi {
		t.Fatalf("expected phi at merge.Ops[0]; got %v", phiOp.Kind)
	}
	if len(phiOp.Args) != 1 || phiOp.Args[0] != aVal {
		t.Errorf("phi.Args = %v, want [aVal]", phiOp.Args)
	}

	if err := Verify(f); err != nil {
		t.Errorf("Verify after FoldBranches: %v", err)
	}
}

// TestFoldBranchesComposesWithFold — full pipeline: Fold
// turns const-int comparison into const_bool, then
// FoldBranches eliminates the brif.
func TestFoldBranchesComposesWithFold(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 1
	cmp := f.AddOp(entry, OpEq, one, two) // becomes const_bool 1
	f.SetBrIf(entry, cmp, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	Fold(f)
	FoldBranches(f)

	if entry.Term.Kind != TermBr || entry.Term.Target != thenB {
		t.Errorf("Term = %+v, want Br→thenB after Fold+FoldBranches", entry.Term)
	}
}

// TestFoldBranchesUnwrapsNot — `brif (not v), T, F` rewrites
// to `brif v, F, T`. Saves the OpNot when DCE follows.
func TestFoldBranchesUnwrapsNot(t *testing.T) {
	f := NewFunc("f")
	v := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	n := f.AddOp(entry, OpNot, v)
	f.SetBrIf(entry, n, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	FoldBranches(f)

	if entry.Term.Kind != TermBrIf {
		t.Fatalf("Term.Kind = %v, want TermBrIf (cond non-const)", entry.Term.Kind)
	}
	if entry.Term.Cond != v {
		t.Errorf("Term.Cond = %v, want %v (unwrapped through not)", entry.Term.Cond, v)
	}
	if entry.Term.True != elseB {
		t.Errorf("Term.True = %v, want elseB (swap)", entry.Term.True)
	}
	if entry.Term.False != thenB {
		t.Errorf("Term.False = %v, want thenB (swap)", entry.Term.False)
	}
}

// TestFoldBranchesUnwrapsChainedNot — two stacked nots cancel
// each other; brif ends up on the inner value with the
// original True/False order restored.
func TestFoldBranchesUnwrapsChainedNot(t *testing.T) {
	f := NewFunc("f")
	v := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	n1 := f.AddOp(entry, OpNot, v)
	n2 := f.AddOp(entry, OpNot, n1)
	f.SetBrIf(entry, n2, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	FoldBranches(f)

	if entry.Term.Cond != v {
		t.Errorf("Term.Cond = %v, want %v (unwrapped two nots)", entry.Term.Cond, v)
	}
	if entry.Term.True != thenB {
		t.Errorf("Term.True = %v, want thenB (two swaps cancel)", entry.Term.True)
	}
	if entry.Term.False != elseB {
		t.Errorf("Term.False = %v, want elseB", entry.Term.False)
	}
}

// TestFoldBranchesUnwrapsNotIntoConst — `brif not(const_bool 1)` —
// the unwrap exposes the const, then const-folding fires too.
func TestFoldBranchesUnwrapsNotIntoConst(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	n := f.AddOp(entry, OpNot, c)
	f.SetBrIf(entry, n, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	FoldBranches(f)

	// not(const_bool true), T, F  →  brif const_bool true, F, T
	// → br F.
	if entry.Term.Kind != TermBr {
		t.Fatalf("Term.Kind = %v, want TermBr", entry.Term.Kind)
	}
	if entry.Term.Target != elseB {
		t.Errorf("Target = %v, want elseB (true cond after swap → F)",
			entry.Term.Target)
	}
}

// TestFoldBranchesNilFunc — defensive nil-input guard.
func TestFoldBranchesNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("FoldBranches(nil) panicked: %v", r)
		}
	}()
	FoldBranches(nil)
}
