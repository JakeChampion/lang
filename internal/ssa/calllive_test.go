package ssa

import "testing"

// callOpOf returns the single call op in f, failing when there is not exactly
// one.
func callOpOf(t *testing.T, f *Func) *Op {
	t.Helper()
	var found *Op
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if isCallOp(op.Kind) {
				if found != nil {
					t.Fatalf("want one call op, found several")
				}
				found = op
			}
		}
	}
	if found == nil {
		t.Fatalf("no call op in %s", f.Name)
	}
	return found
}

// Live intervals are hole-free, so a value defined before a branch and used in
// only one arm still spans the other arm's calls. Nothing reads it there, so
// saving and reloading its register around those calls is pure cost.
func TestCallLiveExcludesAValueTheCallsArmNeverReads(t *testing.T) {
	f := NewFunc("main")
	entry, useArm, callArm := f.NewBlock(), f.NewBlock(), f.NewBlock()
	v := f.AddOp(entry, OpConstInt)
	entry.Ops[len(entry.Ops)-1].Imm = 7
	cond := f.AddOp(entry, OpConstBool)
	entry.Ops[len(entry.Ops)-1].Imm = 1
	f.SetBrIf(entry, cond, useArm, callArm)
	f.SetRet(useArm, v)
	r := f.AddOp(callArm, OpCall)
	callArm.Ops[len(callArm.Ops)-1].Str = "id"
	f.SetRet(callArm, r)

	a := LinearScan(f, Target{NumRegs: 4})
	call := callOpOf(t, f)
	live, ok := a.LiveAcrossOp(call)
	if !ok {
		t.Fatalf("no call-live set recorded for the call")
	}
	if live[v.ID] {
		t.Errorf("v%d is live across a call in the arm that never reads it: %v", v.ID, live)
	}
	if live[r.ID] {
		t.Errorf("the call's own result v%d counted as live across it: %v", r.ID, live)
	}
	// The interval answer does include it, so the walk is what removed it.
	if !a.LiveAcross(a.OpPos[call])[v.ID] {
		t.Errorf("v%d is already excluded by its interval; this case no longer shows the imprecision", v.ID)
	}
}

// The other half of the contract: a value the code after the call reads must be
// in the set, or the caller would not preserve it and the callee could clobber
// it.
func TestCallLiveKeepsAValueReadAfterTheCall(t *testing.T) {
	f := NewFunc("main")
	e := f.NewBlock()
	v := f.AddOp(e, OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 7
	r := f.AddOp(e, OpCall)
	e.Ops[len(e.Ops)-1].Str = "id"
	f.SetRet(e, f.AddOp(e, OpAdd, v, r))

	a := LinearScan(f, Target{NumRegs: 4})
	live, ok := a.LiveAcrossOp(callOpOf(t, f))
	if !ok {
		t.Fatalf("no call-live set recorded for the call")
	}
	if !live[v.ID] {
		t.Errorf("v%d is read after the call but is not live across it: %v", v.ID, live)
	}
}

// A terminator reads its operand after every op in the block, so a value used
// only by the terminator is live across a call earlier in the block.
func TestCallLiveKeepsAValueOnlyTheTerminatorReads(t *testing.T) {
	f := NewFunc("main")
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	cond := f.AddOp(e, OpConstBool)
	e.Ops[len(e.Ops)-1].Imm = 1
	f.AddOp(e, OpCall)
	e.Ops[len(e.Ops)-1].Str = "id"
	f.SetBrIf(e, cond, then, els)
	f.SetRet(then, cond)
	f.SetRet(els, cond)

	a := LinearScan(f, Target{NumRegs: 4})
	live, _ := a.LiveAcrossOp(callOpOf(t, f))
	if !live[cond.ID] {
		t.Errorf("v%d is the branch condition after the call but is not live across it: %v", cond.ID, live)
	}
}

// Precision may only shrink the set: the interval-based answer covers every
// point at which a value is really live, so anything the per-point walk keeps
// must be there too. Under-saving would be a miscompile, so this is the
// direction that matters.
func TestCallLiveNeverExceedsTheIntervalAnswer(t *testing.T) {
	f, _ := buildLoopFunc()
	// Turn the loop body's first op into a call so the loop-carried values have
	// something to be live across.
	body := f.Blocks[1]
	body.Ops[0].Kind = OpCall
	body.Ops[0].Str = "id"
	body.Ops[0].Args = nil

	a := LinearScan(f, Target{NumRegs: 3})
	for op, live := range a.CallLive {
		wide := a.LiveAcross(a.OpPos[op])
		for id := range live {
			if !wide[id] {
				t.Errorf("v%d is live across the call by the per-point walk but not by its interval", id)
			}
		}
	}
}

// Dynamic dispatch is a call: OpCallDyn goes through a vtable slot and OpBoxDyn
// calls the allocator. A value live across either must be preserved, so both
// need a call-live set — without one the emitter falls back to saving the whole
// caller-saved file at every dynamic call.
func TestCallLiveCoversDynamicDispatch(t *testing.T) {
	for _, kind := range []OpKind{OpCallDyn, OpBoxDyn} {
		f := NewFunc("main")
		e := f.NewBlock()
		v := f.AddOp(e, OpConstInt)
		e.Ops[len(e.Ops)-1].Imm = 7
		r := f.AddOp(e, kind, v)
		f.SetRet(e, f.AddOp(e, OpAdd, v, r))

		a := LinearScan(f, Target{NumRegs: 4})
		live, ok := a.LiveAcrossOp(e.Ops[1])
		if !ok {
			t.Errorf("%v has no call-live set", kind)
			continue
		}
		if !live[v.ID] {
			t.Errorf("%v: v%d is read afterwards but is not live across it: %v", kind, v.ID, live)
		}
	}
}
