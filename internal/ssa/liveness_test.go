package ssa

import (
	"reflect"
	"testing"
)

// ids is a helper to write expected live sets inline.
func ids(v ...int32) []int32 { return v }

// TestLivenessStraightLine — a single block defines and consumes everything
// locally, so nothing is live in or out.
func TestLivenessStraightLine(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	b := f.AddOp(entry, OpConstInt)
	s := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, s)

	l := ComputeLiveness(f)
	if got := l.LiveInSorted(entry); len(got) != 0 {
		t.Errorf("LiveIn(entry) = %v, want empty", got)
	}
	if got := l.LiveOutSorted(entry); len(got) != 0 {
		t.Errorf("LiveOut(entry) = %v, want empty", got)
	}
}

// TestLivenessAcrossBlocks — `a` is defined in entry but only used in exit, so
// it must stay live through the middle block.
func TestLivenessAcrossBlocks(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	mid := f.NewBlock()
	exit := f.NewBlock()

	a := f.AddOp(entry, OpConstInt)
	f.SetBr(entry, mid)
	b := f.AddOp(mid, OpConstInt)
	f.SetBr(mid, exit)
	s := f.AddOp(exit, OpAdd, a, b)
	f.SetRet(exit, s)

	l := ComputeLiveness(f)
	check := func(name string, got, want []int32) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("LiveOut(entry)", l.LiveOutSorted(entry), ids(a.ID))
	check("LiveIn(mid)", l.LiveInSorted(mid), ids(a.ID))
	check("LiveOut(mid)", l.LiveOutSorted(mid), ids(a.ID, b.ID))
	check("LiveIn(exit)", l.LiveInSorted(exit), ids(a.ID, b.ID))
	check("LiveOut(exit)", l.LiveOutSorted(exit), nil)
}

// TestLivenessDiamondWithPhi — entry branches on a param; the two arms each
// use `x` and feed a phi at the merge. Key checks: `x` is live out of entry
// (used in both arms); the phi *results* are not live-in of the merge; the phi
// *args* are live-out of their defining arm only.
func TestLivenessDiamondWithPhi(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()

	x := f.AddOp(entry, OpConstInt)
	f.SetBrIf(entry, cond, thenB, elseB)

	y := f.AddOp(thenB, OpAdd, x, x)
	f.SetBr(thenB, merge)
	z := f.AddOp(elseB, OpSub, x, x)
	f.SetBr(elseB, merge)

	// merge.Preds is [thenB, elseB] in branch order, so phi args parallel that.
	p := f.AddPhi(merge, y, z)
	f.SetRet(merge, p)

	l := ComputeLiveness(f)
	check := func(name string, got, want []int32) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	// The param `cond` is live-in of entry (it comes from the caller and is
	// read by the brif).
	check("LiveIn(entry)", l.LiveInSorted(entry), ids(cond.ID))
	// `x` is the only value live out of entry (both arms use it; cond is
	// consumed by the terminator).
	check("LiveOut(entry)", l.LiveOutSorted(entry), ids(x.ID))
	check("LiveIn(thenB)", l.LiveInSorted(thenB), ids(x.ID))
	check("LiveIn(elseB)", l.LiveInSorted(elseB), ids(x.ID))
	// Phi arg `y` is live out of thenB (its edge into the phi) but `z` is not.
	check("LiveOut(thenB)", l.LiveOutSorted(thenB), ids(y.ID))
	check("LiveOut(elseB)", l.LiveOutSorted(elseB), ids(z.ID))
	// The phi result `p` is defined in merge, so nothing is live-in of merge.
	check("LiveIn(merge)", l.LiveInSorted(merge), nil)
}

// TestLivenessLoopWithPhi — the canonical loop: a header phi merges the entry
// init with the body's update over a back-edge. The induction value must be
// live around the whole loop, and the body's update live across the back-edge.
func TestLivenessLoopWithPhi(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()

	init := f.AddOp(entry, OpConstInt) // i = 0
	f.SetBr(entry, header)

	// header.Preds becomes [entry, body]; phi(init from entry, inext from body).
	// AddPhi needs both args, but `inext` is defined later — Values are just
	// IDs, so forward-reference it by minting it first.
	inext := f.NewValue()
	i := f.AddPhi(header, init, inext)
	limit := f.AddOp(header, OpConstInt)
	cond := f.AddOp(header, OpLt, i, limit)
	f.SetBrIf(header, cond, body, exit)

	// body: inext = i + 1 ; back-edge to header. Reuse the pre-minted `inext`.
	one := f.AddOp(body, OpConstInt)
	addOp := f.AddOpNoResult(body, OpAdd, i, one)
	addOp.Result = inext
	f.SetBr(body, header)

	f.SetRet(exit, i)

	l := ComputeLiveness(f)
	contains := func(set []int32, id int32) bool {
		for _, v := range set {
			if v == id {
				return true
			}
		}
		return false
	}
	// The induction value `i` is live out of the header (used in body and exit).
	if !contains(l.LiveOutSorted(header), i.ID) {
		t.Errorf("LiveOut(header) = %v, want to contain i=v%d", l.LiveOutSorted(header), i.ID)
	}
	// The body's update flows back over the edge to the header phi.
	if !contains(l.LiveOutSorted(body), inext.ID) {
		t.Errorf("LiveOut(body) = %v, want to contain inext=v%d", l.LiveOutSorted(body), inext.ID)
	}
	// `i` is live into the body (used by the add).
	if !contains(l.LiveInSorted(body), i.ID) {
		t.Errorf("LiveIn(body) = %v, want to contain i=v%d", l.LiveInSorted(body), i.ID)
	}
	// `i` is live into exit (returned).
	if !contains(l.LiveInSorted(exit), i.ID) {
		t.Errorf("LiveIn(exit) = %v, want to contain i=v%d", l.LiveInSorted(exit), i.ID)
	}
	// The phi result `i` is NOT live-in of its own header (it is defined there).
	if contains(l.LiveInSorted(header), i.ID) {
		t.Errorf("LiveIn(header) = %v, should not contain the phi result i=v%d", l.LiveInSorted(header), i.ID)
	}
}
