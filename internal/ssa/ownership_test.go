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

// A parameter the body releases is locally evidenced as consumed; one it
// only reads is not.
func TestParamModesSeesAReleaseOfAParameter(t *testing.T) {
	f := &Func{Name: "f"}
	p0 := f.AddParam() // released
	p1 := f.AddParam() // only read
	f.ParamAddrs = []bool{true, true}
	b := f.NewBlock()
	f.Entry = b
	dec := f.AddOpNoResult(b, OpCall, p0)
	dec.Str = "__fern_rc_dec"
	f.AddOp(b, OpLoad, p1)
	b.Term = Terminator{Kind: TermRet}

	modes := ParamModes(f)
	if len(modes) != 2 {
		t.Fatalf("want two params, got %d", len(modes))
	}
	if !modes[0].Released {
		t.Error("a parameter the body releases must show the release")
	}
	if modes[1].Released {
		t.Error("a parameter the body only reads must not show a release")
	}
}

// The release can be of a pass-through alias rather than the parameter
// value itself — retain, then release the retain's result. That is the
// same object, and missing it would report the parameter as borrowed.
func TestParamModesFollowsThePassThroughAlias(t *testing.T) {
	f := &Func{Name: "f"}
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	b := f.NewBlock()
	f.Entry = b
	inc := f.AddOp(b, OpCall, p)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	dec := f.AddOpNoResult(b, OpCall, inc) // releases the INC's result
	dec.Str = "__fern_rc_dec"
	b.Term = Terminator{Kind: TermRet}

	modes := ParamModes(f)
	if !modes[0].Retained || !modes[0].Released {
		t.Errorf("a release of the retain's result is a release of the parameter, got %+v", modes[0])
	}
}

// A parameter released through a TYPED helper rather than the generic
// dec is still released. `__fern_arr_dec` alone outnumbers
// `__fern_rc_dec` over the corpus, so a pass that knew only the generic
// helpers reported the majority of releases as none.
func TestParamModesSeesAReleaseThroughATypedHelper(t *testing.T) {
	for _, helper := range []string{
		"__fern_arr_dec",
		"__fern_str_dec",
		"__fern_box_free",
		"__drop_struct_Point",
		"__map_drop_values",
	} {
		f := &Func{Name: "f"}
		p := f.AddParam()
		f.ParamAddrs = []bool{true}
		b := f.NewBlock()
		f.Entry = b
		dec := f.AddOpNoResult(b, OpCall, p)
		dec.Str = helper
		b.Term = Terminator{Kind: TermRet}

		if modes := ParamModes(f); !modes[0].Released {
			t.Errorf("%s: releases its operand, got %+v", helper, modes[0])
		}
	}
}

// The mirror: a call whose name merely resembles a release must not be
// read as one. `__drop_losers` is a real function in std/async.fern and
// releases none of its arguments.
func TestParamModesIgnoresCalleesThatOnlyLookLikeReleases(t *testing.T) {
	for _, callee := range []string{"__drop_losers", "mk_free", "hex_decode", "__fern_str_copy"} {
		f := &Func{Name: "f"}
		p := f.AddParam()
		f.ParamAddrs = []bool{true}
		b := f.NewBlock()
		f.Entry = b
		call := f.AddOpNoResult(b, OpCall, p)
		call.Str = callee
		b.Term = Terminator{Kind: TermRet}

		if modes := ParamModes(f); modes[0].Released {
			t.Errorf("%s: releases nothing, got %+v", callee, modes[0])
		}
	}
}

// A copy-on-write move gives up the caller's unit on the receiver, so it
// counts as a release — but its result is a DIFFERENT object whenever
// the receiver was shared, so it is not a pass-through alias. A use of
// the result says nothing about the operand's liveness.
func TestRCSitesTreatsAMoveAsAReleaseButNotAnAlias(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	fresh := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = "__fern_arr_cow_inplace"
	f.AddOp(b, OpLoad, fresh) // reads the RESULT, not the receiver
	b.Term = Terminator{Kind: TermRet}

	sites := RCSites(f)
	if len(sites) != 1 {
		t.Fatalf("want one rc site, got %d", len(sites))
	}
	if sites[0].LiveAfter {
		t.Error("a use of the move's result is not a use of the receiver")
	}
}

// A parameter released through a loop-carried phi is consumed.
//
// `dup(xs) = Cons(h, dup(t))` is TRMC'd into a loop, so the parameter
// becomes the first incoming of a phi whose other incoming is the tail
// loaded from the previous box, and the drop releases the PHI. The
// alias closure does not cross phis — a phi's incomings really are
// different objects here — but whichever edge the parameter arrives by,
// the phi's value on that edge IS the parameter.
//
// Reading it Borrowed tells every caller it still owns an argument the
// callee consumed, which is the direction that costs a leak:
// `nested_call_temp` pairs all 18 of its allocations at runtime and the
// certifier reported two of them leaked.
func TestSolveOwnershipSeesAReleaseThroughALoopCarriedPhi(t *testing.T) {
	f := &Func{Name: "dup"}
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	f.SetBr(entry, header)

	tail := f.AddOp(body, OpLoad, p) // placeholder operand, replaced below
	cur := f.AddPhi(header, p, tail)
	f.SetBrIf(header, cur, body, exit)

	body.Ops[0].Args[0] = cur // the tail is loaded out of THIS iteration's box
	dec := f.AddOpNoResult(body, OpCall, cur)
	dec.Str = "__fern_rc_dec"
	f.SetBr(body, header)
	exit.Term = Terminator{Kind: TermRet}

	sigs := SolveOwnership(map[string]*Func{"dup": f}).Sigs
	if sigs["dup"].Params[0] != Consumed {
		t.Errorf("parameter released through a loop phi reads %v, want consumed", sigs["dup"].Params[0])
	}
}

// The same shape with the release removed: nothing consumes the
// parameter, so following the phi must not invent a verdict.
func TestSolveOwnershipDoesNotInventAReleaseThroughAPhi(t *testing.T) {
	f := &Func{Name: "walk"}
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	f.SetBr(entry, header)

	tail := f.AddOp(body, OpLoad, p)
	cur := f.AddPhi(header, p, tail)
	f.SetBrIf(header, cur, body, exit)
	body.Ops[0].Args[0] = cur
	f.SetBr(body, header)
	exit.Term = Terminator{Kind: TermRet}

	sigs := SolveOwnership(map[string]*Func{"walk": f}).Sigs
	if sigs["walk"].Params[0] == Consumed {
		t.Error("a parameter nothing releases was called consumed")
	}
}

// A retain through the phi blocks the conclusion the same way a retain
// on the value itself does.
func TestSolveOwnershipLetsARetainThroughAPhiBlockConsumed(t *testing.T) {
	f := &Func{Name: "hold"}
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	f.SetBr(entry, header)

	tail := f.AddOp(body, OpLoad, p)
	cur := f.AddPhi(header, p, tail)
	f.SetBrIf(header, cur, body, exit)
	body.Ops[0].Args[0] = cur
	inc := f.AddOpNoResult(body, OpCall, cur)
	inc.Str = "__fern_rc_inc"
	dec := f.AddOpNoResult(body, OpCall, cur)
	dec.Str = "__fern_rc_dec"
	f.SetBr(body, header)
	exit.Term = Terminator{Kind: TermRet}

	sigs := SolveOwnership(map[string]*Func{"hold": f}).Sigs
	if sigs["hold"].Params[0] == Consumed {
		t.Error("a balanced retain/release through the phi is a borrow, not a consume")
	}
}
