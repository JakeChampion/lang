package ssa

import "testing"

// TestSCCPFoldsConstChain — `2 + 3` folds the OpAdd in place
// to const_int 5. Identical outcome to Fold for a simple
// case; pins the "SCCP at least matches Fold" baseline.
func TestSCCPFoldsConstChain(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 2
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	r := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, r)

	rewritten := SCCP(f)
	if rewritten == 0 {
		t.Fatal("SCCP didn't rewrite any ops")
	}
	add := entry.Ops[2]
	if add.Kind != OpConstInt || add.Imm != 5 {
		t.Errorf("add = {%v %d}, want {OpConstInt 5}", add.Kind, add.Imm)
	}
}

// TestSCCPFoldsBranchOnConst — `brif (const true), T, F` is
// rewritten to `br T`; the F successor loses its pred entry.
func TestSCCPFoldsBranchOnConst(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	SCCP(f)

	if entry.Term.Kind != TermBr {
		t.Fatalf("Term.Kind = %v, want TermBr", entry.Term.Kind)
	}
	if entry.Term.Target != thenB {
		t.Errorf("Target = %v, want thenB", entry.Term.Target)
	}
	if len(elseB.Preds) != 0 {
		t.Errorf("elseB.Preds = %v, want empty", elseB.Preds)
	}
}

// TestSCCPProvesPhiConstantAcrossDeadEdge — the decisive
// demonstration of SCCP's edge over Fold + FoldBranches run
// separately:
//
//	if (true) { x = 7 } else { x = 9 }
//	use x  → SCCP proves x is 7
//
// Fold can't prove this without FoldBranches first dropping
// the dead edge, AND FoldBranches can't drop it without
// knowing the cond is const, AND TrivialPhis can't collapse
// the phi without one of those running first.
// SCCP solves the whole thing in one pass.
func TestSCCPProvesPhiConstantAcrossDeadEdge(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1 // always-true cond
	f.SetBrIf(entry, c, thenB, elseB)
	seven := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 7
	f.SetBr(thenB, merge)
	nine := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 9
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, seven, nine)
	use := f.AddOp(merge, OpAdd, phi, phi)
	f.SetRet(merge, use)

	SCCP(f)

	// The phi should now be const_int 7 (only thenB→merge edge
	// is reachable). And `add phi, phi` becomes `add 7, 7 = 14`.
	phiOp := merge.Ops[0]
	if phiOp.Kind != OpConstInt {
		t.Errorf("phi rewritten to %v, want OpConstInt", phiOp.Kind)
	}
	if phiOp.Kind == OpConstInt && phiOp.Imm != 7 {
		t.Errorf("phi.Imm = %d, want 7", phiOp.Imm)
	}
	addOp := merge.Ops[1]
	if addOp.Kind != OpConstInt || addOp.Imm != 14 {
		t.Errorf("add = {%v %d}, want {OpConstInt 14}", addOp.Kind, addOp.Imm)
	}
}

// TestSCCPLeavesUnknownAlone — operations on Params (Bottom)
// stay as-is; SCCP can't prove anything constant.
func TestSCCPLeavesUnknownAlone(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, r)

	SCCP(f)

	if entry.Ops[0].Kind != OpAdd {
		t.Errorf("Kind = %v, want OpAdd (params are unknown)",
			entry.Ops[0].Kind)
	}
}

// TestSCCPPhiWithDistinctConstsStaysPhi — both arms of a
// brif reachable + different consts → phi stays at Bottom.
func TestSCCPPhiWithDistinctConstsStaysPhi(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam() // unknown cond
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	SCCP(f)

	if merge.Ops[0].Kind != OpPhi {
		t.Errorf("phi rewritten to %v, want OpPhi (distinct consts on reachable paths)",
			merge.Ops[0].Kind)
	}
}

// TestSCCPCallIsBottom — an OpCall return value is always
// Bottom (we can't prove anything about it).
func TestSCCPCallIsBottom(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	r := f.AddOp(entry, OpCall)
	entry.Ops[0].Str = "foo"
	f.SetRet(entry, r)

	SCCP(f)

	if entry.Ops[0].Kind != OpCall {
		t.Errorf("Kind = %v, want OpCall (impure, stays Bottom)",
			entry.Ops[0].Kind)
	}
}

// TestSCCPLoopHeaderPhiStaysBottom — `i = phi(0, i+1)` in a
// loop header keeps i at Bottom (its value depends on the
// loop iteration).
func TestSCCPLoopHeaderPhiStaysBottom(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()

	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	f.SetBr(entry, header)

	// Pre-mint the phi result so we can refer to it from body.
	phiRes := f.NewValue()
	one := f.NewValue() // will be initialised below
	phiOp := &Op{Kind: OpPhi, Result: phiRes, Args: []Value{zero, one}}
	header.Ops = append(header.Ops, phiOp)
	f.SetBrIf(header, cond, body, done)

	oneDef := f.AddOp(body, OpConstInt)
	body.Ops[0].Imm = 1
	incOp := f.AddOp(body, OpAdd, phiRes, oneDef)
	phiOp.Args[1] = incOp // close the loop
	one = incOp
	_ = one
	f.SetBr(body, header)

	f.SetRet(done, phiRes)

	SCCP(f)

	// The phi shouldn't have been rewritten to a const — its
	// value depends on the iteration count.
	if header.Ops[0].Kind != OpPhi {
		t.Errorf("loop header phi rewritten to %v, want OpPhi",
			header.Ops[0].Kind)
	}
}

// TestSCCPNilFunc — defensive.
func TestSCCPNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SCCP(nil) panicked: %v", r)
		}
	}()
	SCCP(nil)
}

// TestSCCPKeepsAProvenConstantsWidth — a phi SCCP proves constant is
// rewritten to the constant AT ITS WIDTH. The x86-64 and arm64 emitters
// materialise a const by its Width, sign-extending the low 32 bits of a
// value with none, so an i64 that lost its width came out of the loop
// below as -1 instead of i64.MAX: the inlined `bit_length` loop then ran
// 64 times on every input.
func TestSCCPKeepsAProvenConstantsWidth(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, thenB, elseB)
	wide := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 9223372036854775807
	thenB.Ops[0].Width = 64
	f.SetBr(thenB, merge)
	zero := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Width = 64
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, wide, zero)
	one := f.AddOp(merge, OpConstInt)
	merge.Ops[1].Imm = 1
	merge.Ops[1].Width = 64
	sub := f.AddOp(merge, OpSub, phi, one)
	merge.Ops[2].Width = 64
	f.SetRet(merge, sub)

	SCCP(f)

	phiOp := merge.Ops[0]
	if phiOp.Kind != OpConstInt || phiOp.Imm != 9223372036854775807 {
		t.Fatalf("phi = {%v %d}, want {OpConstInt i64.MAX}", phiOp.Kind, phiOp.Imm)
	}
	if phiOp.Width != 64 {
		t.Errorf("the proven phi has Width %d, want 64: a wide value without its width is sign-extended from its low 32 bits", phiOp.Width)
	}
	subOp := merge.Ops[2]
	if subOp.Kind != OpConstInt || subOp.Imm != 9223372036854775806 {
		t.Fatalf("sub = {%v %d}, want {OpConstInt i64.MAX-1}", subOp.Kind, subOp.Imm)
	}
	if subOp.Width != 64 {
		t.Errorf("the folded i64 sub has Width %d, want 64", subOp.Width)
	}
}

// A block whose FIRST phi folds to a const while a later one does not.
// SCCP rewrites ops in place, so the folded phi keeps its slot among the
// block's leading phis and stops being a phi there — which leaves the
// surviving phi sitting after a non-phi Op and makes Verify refuse the whole
// function (#9638). It refused three functions of the self-host compiler, so
// the driver could not be built with -backend ssa at all.
//
// Phis lead a block by construction (see AddPhi), and every consumer relies on
// it, so the rewrite has to restore the order rather than the verifier learn to
// tolerate it.
func TestSCCPFoldingOnePhiKeepsPhisLeading(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, elseB)

	// then: 7 and 1. else: 7 and 2. So the first phi is constant (7 both
	// ways) and the second is not — the ordering that breaks.
	sevenT := f.AddOp(thenB, OpConstInt)
	thenB.Ops[len(thenB.Ops)-1].Imm = 7
	oneT := f.AddOp(thenB, OpConstInt)
	thenB.Ops[len(thenB.Ops)-1].Imm = 1
	f.SetBr(thenB, merge)

	sevenE := f.AddOp(elseB, OpConstInt)
	elseB.Ops[len(elseB.Ops)-1].Imm = 7
	twoE := f.AddOp(elseB, OpConstInt)
	elseB.Ops[len(elseB.Ops)-1].Imm = 2
	f.SetBr(elseB, merge)

	foldable := f.AddPhi(merge, sevenT, sevenE)
	survivor := f.AddPhi(merge, oneT, twoE)
	sum := f.AddOp(merge, OpAdd, foldable, survivor)
	f.SetRet(merge, sum)

	SCCP(f)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify after SCCP: %v", err)
	}
	// And the order itself, so this still fails if Verify ever softens.
	sawNonPhi := false
	for i, op := range merge.Ops {
		if op.Kind == OpPhi {
			if sawNonPhi {
				t.Errorf("merge.Ops[%d] is a phi after a non-phi Op", i)
			}
			continue
		}
		sawNonPhi = true
	}
	// The foldable one really did fold — otherwise this test would pass
	// for the wrong reason, never having created the situation.
	folded := false
	for _, op := range merge.Ops {
		if op.Result == foldable && op.Kind == OpConstInt && op.Imm == 7 {
			folded = true
		}
	}
	if !folded {
		t.Error("the constant phi was not folded, so the ordering hazard was never created")
	}
}
