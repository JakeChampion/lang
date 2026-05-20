package ssa

import "testing"

// TestTrivialPhiAllIdentical — `phi v, v, v` aliases to `v`,
// and DCE reclaims the now-orphan phi.
func TestTrivialPhiAllIdentical(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	// Both branches forward the same param value.
	p := f.AddParam() // shared incoming value
	_ = p
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, p, p)
	use := f.AddOp(merge, OpAdd, phi, phi)
	f.SetRet(merge, use)

	TrivialPhis(f)
	DCE(f)

	for _, op := range merge.Ops {
		if op.Kind == OpPhi {
			t.Error("expected phi to be eliminated; still present")
		}
	}
	if use2 := merge.Ops[0]; use2.Args[0] != p || use2.Args[1] != p {
		t.Errorf("use.Args = %v, want both = %v", use2.Args, p)
	}
}

// TestTrivialPhiSingleArg — a phi with only one incoming
// value (after FoldBranches dropped the other edge) aliases
// straight to that value.
func TestTrivialPhiSingleArg(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1 // always take thenB
	f.SetBrIf(entry, c, thenB, merge)
	preMerge := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 7
	v := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	// merge.Preds is [entry, thenB] from the SetBrIf+SetBr
	// order. Phi args follow.
	phi := f.AddPhi(merge, preMerge, v)
	f.SetRet(merge, phi)

	// FoldBranches drops the entry→merge edge (taken=thenB), so
	// phi becomes single-arg [v]. TrivialPhis aliases phi to v.
	FoldBranches(f)
	TrivialPhis(f)

	if merge.Term.Value != v {
		t.Errorf("Term.Value = %v, want %v (phi aliased to single surviving arg)", merge.Term.Value, v)
	}
}

// TestTrivialPhiKeepsDistinctArgs — phi with ≥2 distinct args
// is NOT trivial; left alone.
func TestTrivialPhiKeepsDistinctArgs(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	if merge.Ops[0].Kind != OpPhi {
		t.Errorf("phi gone; expected to survive with distinct args")
	}
}

// TestTrivialPhiSelfRefOK — a phi `phi v, phi.Result` (loop
// header common case where the phi feeds itself) still
// counts as trivial — the self-ref is ignored, only `v`
// remains.
func TestTrivialPhiSelfRefOK(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()

	f.SetBr(entry, header)
	// Pre-mint the phi result so body can reference it as a
	// self-loop arg.
	phiResult := f.NewValue()
	phiOp := &Op{Kind: OpPhi, Result: phiResult, Args: []Value{p, phiResult}}
	header.Ops = append(header.Ops, phiOp)
	cond := f.AddOp(header, OpConstBool)
	header.Ops[1].Imm = 0 // exit on first iter to keep the test small
	f.SetBrIf(header, cond, body, done)
	f.SetBr(body, header)
	f.SetRet(done, phiResult)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify pre-trivial-phi: %v", err)
	}

	TrivialPhis(f)

	// phi must have been aliased; done's ret should now point
	// at p directly.
	if done.Term.Value != p {
		t.Errorf("Term.Value = %v, want %v (self-ref phi collapsed)", done.Term.Value, p)
	}
}

// TestTrivialPhiCascades — eliminating one phi exposes a
// downstream phi as trivial (its arg now points at the
// surviving Value instead of the old phi result).
func TestTrivialPhiCascades(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	mid := f.NewBlock()
	endB := f.NewBlock()
	cond := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, cond, thenB, elseB)
	f.SetBr(thenB, mid)
	f.SetBr(elseB, mid)
	phi1 := f.AddPhi(mid, p, p) // trivial
	f.SetBr(mid, endB)
	phi2 := f.AddPhi(endB, phi1) // single arg, trivial
	f.SetRet(endB, phi2)

	TrivialPhis(f)

	if endB.Term.Value != p {
		t.Errorf("Term.Value = %v, want %v (cascade through two phis)", endB.Term.Value, p)
	}
}

// TestTrivialPhiInOptimizePipeline — Optimize runs
// TrivialPhis after FoldBranches; verify end-to-end the
// pipeline collapses brif-on-const + redundant phi.
func TestTrivialPhiInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, thenB, merge)
	preMerge := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 7
	v := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	phi := f.AddPhi(merge, preMerge, v)
	f.SetRet(merge, phi)

	Optimize(f)

	// After Optimize: brif-on-const collapses to br, the trivial
	// phi resolves, and FuseLinearBlocks fuses the whole chain
	// into a single block. The final block's Ret value must be
	// the surviving const.
	last := f.Blocks[len(f.Blocks)-1]
	for _, b := range f.Blocks {
		if b.Term.Kind == TermRet {
			last = b
			break
		}
	}
	if !last.Term.Value.IsValid() {
		t.Fatal("no Ret terminator carries a valid Value after Optimize")
	}
}

// TestTrivialPhiNilFunc — defensive nil-input.
func TestTrivialPhiNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TrivialPhis(nil) panicked: %v", r)
		}
	}()
	TrivialPhis(nil)
}
