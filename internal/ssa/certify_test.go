package ssa

import "testing"

func dec(f *Func, b *Block, v Value) {
	o := f.AddOpNoResult(b, OpCall, v)
	o.Str = "__fern_rc_dec"
}

// The shape the walk exists to catch: a unit acquired and never given
// back.
func TestCertifyReportsAnAllocationNeverReleased(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	f.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRet}

	rep := Certify(f, nil)
	if len(rep.Leaks) != 1 {
		t.Fatalf("want one leak, got %d: %+v", len(rep.Leaks), rep.Leaks)
	}
	if rep.Leaks[0].Origin != UnitFresh {
		t.Errorf("leak origin = %v, want fresh", rep.Leaks[0].Origin)
	}
}

func TestCertifyIsSilentWhenTheAllocationIsReleased(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	dec(f, b, v)
	b.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a released allocation was reported: %+v", rep.Leaks)
	}
}

// The rename class. The release names the inc's RESULT, which is the
// allocation under another name; a walk keyed on values rather than
// rename roots reports this as a leak.
func TestCertifyFollowsTheReleaseThroughAPassThroughRename(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	inc := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	dec(f, b, inc)
	b.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("the release was not attributed to the allocation: %+v", rep.Leaks)
	}
}

// A returned unit leaves through the return and is the caller's.
func TestCertifyDoesNotReportAReturnedUnit(t *testing.T) {
	f := &Func{Name: "f", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRet, Value: v}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a returned allocation was reported as leaked: %+v", rep.Leaks)
	}
}

// The class the oracle named. A payloadless enum variant is a static
// sentinel; nothing allocated it and nothing can free it.
func TestCertifyDoesNotReportAStaticSentinel(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	f.AddOp(b, OpEnumSentinel)
	b.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("an enum sentinel was reported as leaked: %+v", rep.Leaks)
	}
}

// A borrowed parameter is not this function's to release. Seeding every
// pointer parameter as holding a unit is what made the probe report one
// leak per borrowed parameter per return.
func TestCertifyDoesNotReportABorrowedParameter(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	f.AddParam()
	f.ParamAddrs = []bool{true}
	b.Term = Terminator{Kind: TermRet}

	sigs := map[string]Signature{"f": {Params: []ParamOwnership{Borrowed}, Pointer: []bool{true}}}
	if rep := Certify(f, sigs); len(rep.Leaks) != 0 {
		t.Errorf("a borrowed parameter was reported as leaked: %+v", rep.Leaks)
	}
}

// A consumed parameter IS this function's, and dropping it on the floor
// is a leak.
func TestCertifyReportsAConsumedParameterNeverReleased(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	f.AddParam()
	f.ParamAddrs = []bool{true}
	b.Term = Terminator{Kind: TermRet}

	sigs := map[string]Signature{"f": {Params: []ParamOwnership{Consumed}, Pointer: []bool{true}}}
	rep := Certify(f, sigs)
	if len(rep.Leaks) != 1 || rep.Leaks[0].Origin != UnitTransferred {
		t.Errorf("want one transferred-origin leak, got %+v", rep.Leaks)
	}
}

// Ownership handed into a container is not held by the local any more.
func TestCertifyTreatsAStoreAsATransfer(t *testing.T) {
	f := &Func{Name: "f", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	box := f.AddOp(b, OpAlloc)
	v := f.AddOp(b, OpAlloc)
	f.AddOpNoResult(b, OpStore, box, v)
	b.Term = Terminator{Kind: TermRet, Value: box}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a value stored into a returned container was reported: %+v", rep.Leaks)
	}
}

// A parameter position the solved signature says is consumed takes the
// unit with it.
func TestCertifyTreatsAConsumingCallArgumentAsATransfer(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	o := f.AddOpNoResult(b, OpCall, v)
	o.Str = "takes_it"
	b.Term = Terminator{Kind: TermRet}

	sigs := map[string]Signature{"takes_it": {Params: []ParamOwnership{Consumed}, Pointer: []bool{true}}}
	if rep := Certify(f, sigs); len(rep.Leaks) != 0 {
		t.Errorf("a unit handed to a consuming position was reported: %+v", rep.Leaks)
	}
}

// Fail-soft: when the arms disagree about whether the unit is still
// held, the walk says nothing rather than guessing. Reporting here is
// how the probe turned every conditional release into a leak.
func TestCertifyIsSilentWhenTheArmsDisagree(t *testing.T) {
	f := &Func{Name: "f"}
	entry := f.NewBlock()
	then := f.NewBlock()
	els := f.NewBlock()
	join := f.NewBlock()
	f.Entry = entry

	v := f.AddOp(entry, OpAlloc)
	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBrIf, Cond: c, True: then, False: els}
	then.Preds = []*Block{entry}
	els.Preds = []*Block{entry}
	dec(f, then, v)
	then.Term = Terminator{Kind: TermBr, Target: join}
	els.Term = Terminator{Kind: TermBr, Target: join}
	join.Preds = []*Block{then, els}
	join.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a conditionally-released unit was reported: %+v", rep.Leaks)
	}
}

// A unit released on every arm is not a disagreement, and the walk
// still has to be able to see it is gone.
func TestCertifyIsSilentWhenBothArmsRelease(t *testing.T) {
	f := &Func{Name: "f"}
	entry := f.NewBlock()
	then := f.NewBlock()
	els := f.NewBlock()
	join := f.NewBlock()
	f.Entry = entry

	v := f.AddOp(entry, OpAlloc)
	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBrIf, Cond: c, True: then, False: els}
	then.Preds = []*Block{entry}
	els.Preds = []*Block{entry}
	dec(f, then, v)
	dec(f, els, v)
	then.Term = Terminator{Kind: TermBr, Target: join}
	els.Term = Terminator{Kind: TermBr, Target: join}
	join.Preds = []*Block{then, els}
	join.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a unit released on both arms was reported: %+v", rep.Leaks)
	}
}

// An indirect call could have done anything with the pointer it was
// handed, so nothing downstream of it may be reported — and the fact
// that it happened is counted.
func TestCertifyPoisonsAnIndirectCallsArguments(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	callee := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpAlloc)
	f.AddOpNoResult(b, OpCallIndirect, callee, v)
	b.Term = Terminator{Kind: TermRet}

	rep := Certify(f, nil)
	if len(rep.Leaks) != 0 {
		t.Errorf("a value an indirect call touched was reported: %+v", rep.Leaks)
	}
	if rep.Poisoned == 0 {
		t.Error("the opaque call was not counted — an uncounted gap is the failure mode")
	}
}

// A pair return discharges both halves. The Option/Result ABI is over
// half the corpus, so treating it as unmodelled would leave the walk
// with nothing to say about most functions.
func TestCertifyDischargesBothHalvesOfAPairReturn(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	tag := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRetPair, Value: tag, Value2: v}

	rep := Certify(f, nil)
	if !rep.Modelled {
		t.Errorf("a pair return was skipped: %+v", rep)
	}
	if len(rep.Leaks) != 0 {
		t.Errorf("the returned payload was reported as leaked: %+v", rep.Leaks)
	}
}

// A unit that is neither returned half of a pair is still reported.
func TestCertifyReportsALeakBesideAPairReturn(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	tag := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpAlloc)
	f.AddOp(b, OpAlloc) // never released, never returned
	b.Term = Terminator{Kind: TermRetPair, Value: tag, Value2: v}

	if rep := Certify(f, nil); len(rep.Leaks) != 1 {
		t.Errorf("want one leak beside the returned pair, got %+v", rep.Leaks)
	}
}

// A unit acquired in a loop body and released there is balanced across
// the back edge.
func TestCertifyIsSilentOnABalancedLoopBody(t *testing.T) {
	f := &Func{Name: "f"}
	entry := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()
	f.Entry = entry

	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBr, Target: body}
	body.Preds = []*Block{entry, body}
	v := f.AddOp(body, OpAlloc)
	dec(f, body, v)
	body.Term = Terminator{Kind: TermBrIf, Cond: c, True: body, False: exit}
	exit.Preds = []*Block{body}
	exit.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a balanced loop body was reported: %+v", rep.Leaks)
	}
}

// A unit threaded through a loop is disposed of under the PHI's name,
// never under the name it was allocated with. A walk keyed on the
// allocation sees no release and reports a leak — the shape that made
// `int____int_to_string_u64` report its `__alloc_u8` buffer on every
// fixture that used it.
func TestCertifyFollowsAUnitIntoAPhi(t *testing.T) {
	f := &Func{Name: "f"}
	entry := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()
	f.Entry = entry

	v := f.AddOp(entry, OpAlloc)
	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBr, Target: body}

	body.Preds = []*Block{entry, body}
	next := f.AddOp(body, OpAlloc)
	cur := f.AddPhi(body, v, next)
	body.Term = Terminator{Kind: TermBrIf, Cond: c, True: body, False: exit}

	exit.Preds = []*Block{body}
	dec(f, exit, cur)
	exit.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a unit released through the phi it was threaded into was reported: %+v",
			rep.Leaks)
	}
}

// The transfer is attributed to the EDGE, not to the phi in general: a
// value that never reaches the join keeps its unit, so a second
// allocation on a path with no phi is still reported.
func TestCertifyStillReportsAUnitThatNeverReachesThePhi(t *testing.T) {
	f := &Func{Name: "f"}
	entry := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()
	f.Entry = entry

	v := f.AddOp(entry, OpAlloc)
	stray := f.AddOp(entry, OpAlloc) // feeds nothing, released nowhere
	_ = stray
	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBr, Target: body}

	body.Preds = []*Block{entry, body}
	next := f.AddOp(body, OpAlloc)
	cur := f.AddPhi(body, v, next)
	body.Term = Terminator{Kind: TermBrIf, Cond: c, True: body, False: exit}

	exit.Preds = []*Block{body}
	dec(f, exit, cur)
	exit.Term = Terminator{Kind: TermRet}

	rep := Certify(f, nil)
	if len(rep.Leaks) != 1 {
		t.Fatalf("want the one allocation that feeds no phi reported, got %+v", rep.Leaks)
	}
	if rep.Leaks[0].Value.ID != stray.ID {
		t.Errorf("reported v%d, want the stray allocation v%d", rep.Leaks[0].Value.ID, stray.ID)
	}
}

// The result axis places a call that allocates, so a fresh buffer from
// a runtime helper is tracked the same as an OpAlloc.
func TestCertifyReportsAnOwnedCallResultNeverReleased(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	n := f.AddOp(b, OpConstInt)
	f.AddOp(b, OpCall, n)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "__alloc_u8", true
	b.Term = Terminator{Kind: TermRet}

	rep := Certify(f, nil)
	if len(rep.Leaks) != 1 || rep.Leaks[0].Origin != UnitFresh {
		t.Errorf("want one fresh-origin leak for the unreleased __alloc_u8 buffer, got %+v",
			rep.Leaks)
	}
}

// An immortal-headered result is fresh, pointer-shaped, and carries no
// unit: every rc helper short-circuits on the sentinel bit, so nothing
// can release it and it must never be reported.
func TestCertifyDoesNotReportAnImmortalCallResult(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	n := f.AddOp(b, OpConstInt)
	f.AddOp(b, OpCall, n)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "__fern_alloc_box", true
	b.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a static-sentinel box was reported as leaked: %+v", rep.Leaks)
	}
}

// The last class the certifier reported against the runtime oracle: a
// static closure cell read as a fresh allocation. 102 of the 109
// findings over the census-clean fixtures were this.
func TestCertifyDoesNotReportAStaticClosureCell(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	f.AddOp(b, OpMakeClosure)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.StaticCell = "target", true
	b.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 0 {
		t.Errorf("a static closure cell was reported as leaked: %+v", rep.Leaks)
	}
}

// The heap form is still reported, so the fix is a distinction rather
// than a blanket exemption for the op kind.
func TestCertifyStillReportsAHeapClosure(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	f.AddOp(b, OpMakeClosure)
	b.Ops[len(b.Ops)-1].Str = "target"
	b.Term = Terminator{Kind: TermRet}

	if rep := Certify(f, nil); len(rep.Leaks) != 1 {
		t.Errorf("want the unreleased heap closure reported, got %+v", rep.Leaks)
	}
}

// The dataflow runs to a fixpoint with no round cap. It used to stop at
// 64 sweeps, which silently truncated the answer on the functions least
// able to afford it — over the self-host compiler three need more than
// that, one needs 206, and the cap was hiding 41 findings.
//
// A chain of blocks needs a sweep per link to propagate, so the shape is
// cheap to build and would have hit the old cap exactly.
func TestCertifyRunsToAFixpointWithNoRoundCap(t *testing.T) {
	const links = 120
	f := &Func{Name: "f"}
	blocks := make([]*Block, links+1)
	for i := range blocks {
		blocks[i] = f.NewBlock()
	}
	f.Entry = blocks[0]
	// A unit allocated in the first block and released in the last: the
	// state has to travel the whole chain before the walk settles.
	v := f.AddOp(blocks[0], OpAlloc)
	for i := 0; i < links; i++ {
		blocks[i].Term = Terminator{Kind: TermBr, Target: blocks[i+1]}
		blocks[i+1].Preds = []*Block{blocks[i]}
	}
	dec(f, blocks[links], v)
	blocks[links].Term = Terminator{Kind: TermRet}

	rep := Certify(f, nil)
	if len(rep.Leaks) != 0 {
		t.Errorf("a unit released at the end of a %d-block chain was reported: %+v",
			links, rep.Leaks)
	}
	if rep.Passes == 0 {
		t.Error("the pass count is not being reported")
	}
}
