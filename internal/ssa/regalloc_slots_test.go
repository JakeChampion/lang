package ssa

import "testing"

// A value's last use and the definition of the result it feeds are disjoint in
// time — the machine reads every source before it writes the destination — and
// the point space says so by giving each op a use slot and a def slot. Without
// the split the two land on the same point, the intervals overlap, and no
// result can ever reuse an operand's register: the ordinary two-address case,
// which is most induction variables and most accumulator updates.
func TestResultStartsAfterItsOperandsLastUse(t *testing.T) {
	f, vals := buildLoopFunc()
	a := LinearScan(f, Target{NumRegs: 8})
	i, inext := a.Intervals[vals["i"].ID], a.Intervals[vals["inext"].ID]
	if i.End >= inext.Start {
		t.Errorf("i=%v ends at or after inext=%v begins; i's last use is the op defining inext", i, inext)
	}
}

// Both phi edges of a loop-carried value coalesce once its interval stops
// overlapping its update's: the header phi, the entry initialiser and the
// update all take one register, so neither edge move survives. The back edge is
// the half that only the slot split reaches — the entry edge coalesced on the
// hint alone.
func TestLoopCarriedPhiCoalescesWithItsUpdate(t *testing.T) {
	f, vals := buildLoopFunc()
	a := LinearScan(f, Target{NumRegs: 8})
	if msg := VerifyAllocation(a); msg != "" {
		t.Fatalf("allocation not sound: %s", msg)
	}
	phi, ok := a.Reg[vals["i"].ID]
	if !ok {
		t.Fatalf("the loop-carried phi took a spill slot with 8 registers free")
	}
	for _, name := range []string{"init", "inext"} {
		if r, ok := a.Reg[vals[name].ID]; !ok || r != phi {
			t.Errorf("%s in r%d (reg=%v), phi i in r%d: the edge move survives", name, r, ok, phi)
		}
	}
}
