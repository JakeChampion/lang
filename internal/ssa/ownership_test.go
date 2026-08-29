package ssa

import "testing"

// A release whose operand is used again later in the same block is not
// at the last use.
func TestRCSitesSeesALaterUseInTheSameBlock(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	dec := f.AddOpNoResult(b, OpCall, v)
	dec.Str = "__fern_rc_dec"
	f.AddOp(b, OpLoad, v) // a later read of the same value
	b.Term = Terminator{Kind: TermRet}

	sites := RCSites(f)
	if len(sites) != 1 {
		t.Fatalf("want one rc site, got %d", len(sites))
	}
	if !sites[0].LiveAfter {
		t.Error("a value read later in the same block must be live after the release")
	}
}

// The same shape with nothing after it: the release IS at the last use.
func TestRCSitesSeesNoLaterUse(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	dec := f.AddOpNoResult(b, OpCall, v)
	dec.Str = "__fern_rc_dec"
	b.Term = Terminator{Kind: TermRet}

	sites := RCSites(f)
	if len(sites) != 1 || sites[0].LiveAfter {
		t.Errorf("want one site not live after, got %+v", sites)
	}
}

// The case a textual scan gets wrong (#7544): the use is textually
// BEFORE the release, but the block is a loop, so the next iteration
// reaches it again. Liveness must follow the back edge, not the text.
func TestRCSitesFollowsTheBackEdgeNotTheText(t *testing.T) {
	f := &Func{Name: "f"}
	entry := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()
	f.Entry = entry

	v := f.AddOp(entry, OpAlloc)
	entry.Term = Terminator{Kind: TermBr, Target: body}

	// read v, THEN release it — textually the release is last
	f.AddOp(body, OpLoad, v)
	dec := f.AddOpNoResult(body, OpCall, v)
	dec.Str = "__fern_rc_dec"
	body.Term = Terminator{Kind: TermBrIf, Cond: v, True: body, False: exit}

	exit.Term = Terminator{Kind: TermRet}

	sites := RCSites(f)
	if len(sites) != 1 {
		t.Fatalf("want one rc site, got %d", len(sites))
	}
	if !sites[0].LiveAfter {
		t.Error("the read is reached again across the back edge, so the value is live " +
			"after the release — this is the case a textual last-occurrence test gets wrong (#7544)")
	}
}
