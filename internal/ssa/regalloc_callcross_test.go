package ssa

import "testing"

// callCrossing builds a function whose parameter is live across `calls` calls
// and then read `uses` times, and returns it with the parameter.
func callCrossing(calls, uses int) (*Func, Value) {
	f := NewFunc("f")
	p := f.AddParam()
	b := f.NewBlock()
	for i := 0; i < calls; i++ {
		f.AddOp(b, OpCall)
		b.Ops[len(b.Ops)-1].Str = "g"
	}
	acc := p
	for i := 1; i < uses; i++ {
		acc = f.AddOp(b, OpAdd, acc, p)
	}
	f.SetRet(b, acc)
	return f, p
}

// A value live across many more calls than it has uses is cheaper in a
// spill slot than in a caller-saved register, which every call would save
// and restore; one read more often than that stays in a register, and a free
// callee-saved register takes either.
func TestLinearScanSpillsACallCrossingValueOverSavingIt(t *testing.T) {
	callerSavedOnly := Target{NumRegs: 4, CalleeSaved: []bool{false, false, false, false}}
	f, p := callCrossing(20, 1)
	if a := LinearScan(f, callerSavedOnly); a.Slot[p.ID] != 0 || len(a.Slot) != 1 {
		t.Errorf("a parameter crossing twenty calls with one use has a register (%v); want the one spill slot", a.Reg)
	}
	f, p = callCrossing(1, 6)
	if a := LinearScan(f, callerSavedOnly); len(a.Slot) != 0 {
		t.Errorf("a parameter read six times across one call was spilled (%v); want a register", a.Slot)
	}
	oneCalleeSaved := Target{NumRegs: 4, CalleeSaved: []bool{false, false, false, true}}
	f, p = callCrossing(20, 1)
	if a := LinearScan(f, oneCalleeSaved); a.Reg[p.ID] != 3 {
		t.Errorf("with a callee-saved register free the parameter is homed at %v; want register 3", a.Reg)
	}
	noHints := Target{NumRegs: 4}
	f, p = callCrossing(20, 1)
	if a := LinearScan(f, noHints); len(a.Slot) != 0 {
		t.Errorf("a target with no callee-saved hint spilled %v; want the allocation unchanged", a.Slot)
	}
}
