package ssa

import (
	"strings"
	"testing"
)

// TestPhiDiamondMerge — the canonical phi shape. Entry
// branches to then/else; each defines a constant; merge
// joins with a phi that picks the right one. Verify accepts;
// printer renders pred-annotated arg list.
func TestPhiDiamondMerge(t *testing.T) {
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

	// Phi args follow merge.Preds order — thenB first, then elseB
	// (matches SetBrIf's Preds-append order).
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	want := "v4 = phi v2 [block 2], v3 [block 3]"
	if got := f.String(); !strings.Contains(got, want) {
		t.Errorf("Func.String() missing %q in:\n%s", want, got)
	}
}

// TestPhiAtTopOfBlockEnforced — a phi placed AFTER a non-phi
// op should be rejected. Builders must keep phis leading.
// We construct merge so the pre-phi op uses only function
// params (dominate everywhere), making the phi placement
// the FIRST violation Verify hits.
func TestPhiAtTopOfBlockEnforced(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	p := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)

	// Use a param (in scope at merge) for the pre-phi op so
	// its dominance check passes, then manually append a phi
	// after it — bypassing AddPhi's auto-splice.
	pre := f.AddOp(merge, OpAdd, p, p)
	badPhi := &Op{Kind: OpPhi, Result: f.NewValue(), Args: []Value{p, p}}
	merge.Ops = append(merge.Ops, badPhi)
	f.SetRet(merge, pre)

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for phi after non-phi op")
	}
	if !strings.Contains(err.Error(), "phis must lead") {
		t.Errorf("error %q doesn't mention phi ordering", err)
	}
}

// TestPhiArgCountMatchesPreds — phi with wrong number of args
// gets rejected. Catches a common error where preds got added
// after the phi was synthesised.
func TestPhiArgCountMatchesPreds(t *testing.T) {
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
	_ = f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	// Phi with only ONE arg — merge has TWO preds.
	short := f.AddPhi(merge, one)
	f.SetRet(merge, short)

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for short phi arg list")
	}
	if !strings.Contains(err.Error(), "args but block has") {
		t.Errorf("error %q doesn't mention arg/pred count mismatch", err)
	}
}

// TestPhiArgDominanceFromPredEnd — phi op's arg dominance is
// checked relative to the END of the corresponding pred, not
// the phi's own block. This is the SSA-correct rule.
//
// Setup: entry branches to thenB / elseB. ThenB defines v_t,
// elseB defines v_e, both flow into merge.phi. Each
// branch-end dominates its own pred-end, so this is legal.
func TestPhiArgDominanceFromPredEnd(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	vt := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	ve := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)

	phi := f.AddPhi(merge, vt, ve)
	f.SetRet(merge, phi)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestPhiUsesValueNotInPredScope — pred A doesn't define v_x;
// using v_x as the phi's A-side value is illegal.
func TestPhiUsesValueNotInPredScope(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	// thenB has no internal defs.
	f.SetBr(thenB, merge)
	// elseB defines a constant.
	v := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 7
	f.SetBr(elseB, merge)
	// Illegal phi: try to take v (defined only in elseB) as the
	// thenB-side incoming value. v's def doesn't dominate
	// thenB's terminator.
	phi := f.AddPhi(merge, v, v)
	f.SetRet(merge, phi)

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for phi referencing value not in pred scope")
	}
	if !strings.Contains(err.Error(), "phi") {
		t.Errorf("error %q doesn't mention phi", err)
	}
}

// TestPhiOpKindString — `phi` rendering pinned for dumps.
func TestPhiOpKindString(t *testing.T) {
	if got := OpPhi.String(); got != "phi" {
		t.Errorf("OpPhi.String() = %q, want %q", got, "phi")
	}
}

// TestAddPhiPrependsOverExistingPhi — adding a phi to a block
// that already has phis keeps all phis at the top, in
// insertion order.
func TestAddPhiPrependsOverExistingPhi(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	a := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	b := f.AddOp(thenB, OpConstInt)
	thenB.Ops[1].Imm = 2
	f.SetBr(thenB, merge)
	d := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 3
	e := f.AddOp(elseB, OpConstInt)
	elseB.Ops[1].Imm = 4
	f.SetBr(elseB, merge)

	phi1 := f.AddPhi(merge, a, d)
	phi2 := f.AddPhi(merge, b, e)
	sum := f.AddOp(merge, OpAdd, phi1, phi2)
	f.SetRet(merge, sum)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if merge.Ops[0].Kind != OpPhi || merge.Ops[1].Kind != OpPhi {
		t.Errorf("first two Ops should be phis; got %v / %v", merge.Ops[0].Kind, merge.Ops[1].Kind)
	}
	if merge.Ops[2].Kind != OpAdd {
		t.Errorf("Ops[2].Kind = %v, want OpAdd", merge.Ops[2].Kind)
	}
}
