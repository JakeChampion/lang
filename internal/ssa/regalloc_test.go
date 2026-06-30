package ssa

import (
	"reflect"
	"testing"
)

func intervalSet(ivs ...Interval) map[int32]Interval {
	m := map[int32]Interval{}
	for _, i := range ivs {
		m[i.Value] = i
	}
	return m
}

// Disjoint intervals reuse a single register; with two available, none spill.
func TestAllocDisjointNoSpill(t *testing.T) {
	iv := intervalSet(
		Interval{Value: 1, Start: 0, End: 1},
		Interval{Value: 2, Start: 2, End: 3},
		Interval{Value: 3, Start: 4, End: 5},
	)
	a := allocateLinear(iv, Target{NumRegs: 2})
	if a.NumSlots != 0 {
		t.Errorf("NumSlots = %d, want 0 (disjoint intervals fit in registers)", a.NumSlots)
	}
	if msg := VerifyAllocation(a); msg != "" {
		t.Errorf("allocation not sound: %s", msg)
	}
}

// Three mutually-overlapping intervals with only two registers must spill
// exactly one — the interval that ends last.
func TestAllocOverlapSpillsFurthestEnd(t *testing.T) {
	iv := intervalSet(
		Interval{Value: 1, Start: 0, End: 10},
		Interval{Value: 2, Start: 1, End: 11},
		Interval{Value: 3, Start: 2, End: 12}, // ends last → spilled
	)
	a := allocateLinear(iv, Target{NumRegs: 2})
	if msg := VerifyAllocation(a); msg != "" {
		t.Errorf("allocation not sound: %s", msg)
	}
	if a.NumSlots != 1 {
		t.Fatalf("NumSlots = %d, want 1", a.NumSlots)
	}
	if _, spilled := a.Slot[3]; !spilled {
		t.Errorf("expected v3 (furthest end) to be spilled; Slot=%v Reg=%v", a.Slot, a.Reg)
	}
}

// When a newly-started interval ends sooner than the furthest active one, the
// allocator steals the active register and spills that longer-lived value.
func TestAllocSpillStealsFromLongerLived(t *testing.T) {
	iv := intervalSet(
		Interval{Value: 1, Start: 0, End: 12}, // longest-lived → spilled when v3 arrives
		Interval{Value: 2, Start: 1, End: 11},
		Interval{Value: 3, Start: 2, End: 10},
	)
	a := allocateLinear(iv, Target{NumRegs: 2})
	if msg := VerifyAllocation(a); msg != "" {
		t.Errorf("allocation not sound: %s", msg)
	}
	if _, spilled := a.Slot[1]; !spilled {
		t.Errorf("expected v1 (longest-lived) to be spilled; Slot=%v Reg=%v", a.Slot, a.Reg)
	}
	if _, r2 := a.Reg[2]; !r2 {
		t.Error("v2 should hold a register")
	}
	if _, r3 := a.Reg[3]; !r3 {
		t.Error("v3 should hold a register")
	}
}

// Single register, three overlapping values: two spill, and the survivor plus
// the spills must still verify (no overlapping pair shares the one register).
func TestAllocSingleRegister(t *testing.T) {
	iv := intervalSet(
		Interval{Value: 1, Start: 0, End: 5},
		Interval{Value: 2, Start: 1, End: 6},
		Interval{Value: 3, Start: 2, End: 7},
	)
	a := allocateLinear(iv, Target{NumRegs: 1})
	if msg := VerifyAllocation(a); msg != "" {
		t.Errorf("allocation not sound: %s", msg)
	}
	if a.NumSlots != 2 {
		t.Errorf("NumSlots = %d, want 2", a.NumSlots)
	}
}

// buildLoopFunc is the canonical loop+phi used in the liveness tests, reused
// here to exercise LinearScan end-to-end over real liveness/intervals.
func buildLoopFunc() (*Func, map[string]Value) {
	f := NewFunc("loop")
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()

	init := f.AddOp(entry, OpConstInt)
	f.SetBr(entry, header)

	inext := f.NewValue()
	i := f.AddPhi(header, init, inext)
	limit := f.AddOp(header, OpConstInt)
	cond := f.AddOp(header, OpLt, i, limit)
	f.SetBrIf(header, cond, body, exit)

	one := f.AddOp(body, OpConstInt)
	addOp := f.AddOpNoResult(body, OpAdd, i, one)
	addOp.Result = inext
	f.SetBr(body, header)

	f.SetRet(exit, i)
	return f, map[string]Value{"init": init, "i": i, "inext": inext, "cond": cond}
}

// End-to-end: liveness → intervals → linear scan over a real loop, with the
// interference verifier as the oracle. Run with a register file roomy enough to
// avoid spills, and a tight one that forces them; both must be sound.
func TestLinearScanLoopSound(t *testing.T) {
	for _, nregs := range []int{1, 2, 8} {
		f, vals := buildLoopFunc()
		a := LinearScan(f, Target{NumRegs: nregs})
		if msg := VerifyAllocation(a); msg != "" {
			t.Errorf("NumRegs=%d: allocation not sound: %s", nregs, msg)
		}
		// The induction value's interval must span the loop: defined at the
		// header phi, used through the back-edge into the body and out to exit.
		iv := a.Intervals[vals["i"].ID]
		if iv.End <= iv.Start {
			t.Errorf("NumRegs=%d: induction value interval %v is degenerate", nregs, iv)
		}
	}
}

// The allocator is deterministic: identical inputs yield identical assignments.
func TestLinearScanDeterministic(t *testing.T) {
	f1, _ := buildLoopFunc()
	f2, _ := buildLoopFunc()
	a1 := LinearScan(f1, Target{NumRegs: 2})
	a2 := LinearScan(f2, Target{NumRegs: 2})
	if !reflect.DeepEqual(a1.Reg, a2.Reg) || !reflect.DeepEqual(a1.Slot, a2.Slot) {
		t.Errorf("non-deterministic allocation:\n  reg %v vs %v\n  slot %v vs %v",
			a1.Reg, a2.Reg, a1.Slot, a2.Slot)
	}
}
