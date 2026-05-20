package ssa

import "testing"

// TestOptimizeConstChain — `(1 + 2) * (3 - 1)` should collapse
// to a single const_int 6 plus the ret.
func TestOptimizeConstChain(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	three := f.AddOp(entry, OpConstInt)
	entry.Ops[2].Imm = 3
	one2 := f.AddOp(entry, OpConstInt)
	entry.Ops[3].Imm = 1
	lhs := f.AddOp(entry, OpAdd, one, two)
	rhs := f.AddOp(entry, OpSub, three, one2)
	prod := f.AddOp(entry, OpMul, lhs, rhs)
	f.SetRet(entry, prod)

	iters := Optimize(f)
	if iters < 1 {
		t.Errorf("iters = %d, want >= 1", iters)
	}

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1 (one folded const); got %v", len(entry.Ops), opKinds(entry.Ops))
	}
	if got := entry.Ops[0]; got.Kind != OpConstInt || got.Imm != 6 {
		t.Errorf("survivor = {%v %d}, want {OpConstInt 6}", got.Kind, got.Imm)
	}
}

// TestOptimizeAlgebraicIdentityChain — `((x + 0) * 1) + 0`
// reduces to plain `x`. Tests Fold + Simplify + DCE together.
func TestOptimizeAlgebraicIdentityChain(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 1
	a := f.AddOp(entry, OpAdd, x, zero) // x + 0 → x
	b := f.AddOp(entry, OpMul, a, one)  // x * 1 → x
	c := f.AddOp(entry, OpAdd, b, zero) // x + 0 → x
	f.SetRet(entry, c)

	Optimize(f)

	// Result is now `ret x` directly; all identity ops collapsed.
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (identity chain reduced to x)", entry.Term.Value, x)
	}
}

// TestOptimizeCSEThenDCE — duplicate (a+b) ops collapse to
// a single canonical, leaving exactly one (a+b) and the
// downstream final add.
func TestOptimizeCSEThenDCE(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	x := f.AddOp(entry, OpAdd, a, b)
	y := f.AddOp(entry, OpAdd, a, b)
	sum := f.AddOp(entry, OpAdd, x, y)
	f.SetRet(entry, sum)

	Optimize(f)

	if len(entry.Ops) != 2 {
		t.Fatalf("Ops = %d, want 2 (one a+b, one final add); kinds %v",
			len(entry.Ops), opKinds(entry.Ops))
	}
}

// TestOptimizeBranchAndCleanup — Fold collapses comparison;
// FoldBranches drops the un-taken edge; DCE sweeps the now-
// orphan const and cmp ops.
func TestOptimizeBranchAndCleanup(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 5
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 5
	cmp := f.AddOp(entry, OpEq, a, b) // → const_bool 1
	f.SetBrIf(entry, cmp, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	Optimize(f)

	if entry.Term.Kind != TermBr || entry.Term.Target != thenB {
		t.Errorf("Term = %+v, want Br→thenB", entry.Term)
	}
	if len(elseB.Preds) != 0 {
		t.Errorf("elseB.Preds = %v, want empty", elseB.Preds)
	}
	// All const/cmp ops in entry should be dead after the brif
	// got rewritten — entry should be empty or only contain
	// truly-still-used ops.
	for _, op := range entry.Ops {
		if op.Result == cmp {
			t.Errorf("cmp survived; expected DCE to reclaim it")
		}
	}
}

// TestOptimizeIdempotent — running Optimize a second time
// produces no changes (converged on the first call). The
// returned iter count for the second call should be 1.
func TestOptimizeIdempotent(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	first := Optimize(f)
	dumpAfterFirst := f.String()
	second := Optimize(f)
	if second != 1 {
		t.Errorf("second Optimize iters = %d, want 1 (already converged)", second)
	}
	if f.String() != dumpAfterFirst {
		t.Error("Optimize is not idempotent — second call changed output")
	}
	if first < 1 {
		t.Errorf("first Optimize iters = %d, want ≥ 1", first)
	}
}

// TestOptimizeVerifyAfter — Optimize must leave the function
// structurally valid (Preds, phi args, single-assignment all
// intact).
func TestOptimizeVerifyAfter(t *testing.T) {
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

	Optimize(f)
	if err := Verify(f); err != nil {
		t.Fatalf("Verify after Optimize: %v", err)
	}
}

// TestOptimizeNilFunc — defensive nil guard.
func TestOptimizeNilFunc(t *testing.T) {
	if got := Optimize(nil); got != 0 {
		t.Errorf("Optimize(nil) = %d, want 0", got)
	}
}

// TestOptimizeIterBudgetBounded — pathological-ish input
// shouldn't exhaust the iter budget on real inputs, but we
// pin that the budget exists.
func TestOptimizeIterBudgetBounded(t *testing.T) {
	if maxOptimizeIters < 1 {
		t.Errorf("maxOptimizeIters = %d, want ≥ 1", maxOptimizeIters)
	}
}
