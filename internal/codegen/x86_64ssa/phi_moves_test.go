package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func rLoc(r int) Loc { return Loc{IsReg: true, Reg: r} }
func sLoc(n int) Loc { return Loc{Slot: n} }

// An acyclic set is emitted as one copy per move, in an order where nothing
// reads a destination that has already been overwritten — and never through a
// temp slot. Routing these through temps is what made every loop back edge cost
// a store and a load per value.
func TestParallelMovesAcyclicNeedsNoTemp(t *testing.T) {
	// r1 <- r2, r2 <- r3: r2 must be READ before it is written.
	got := sequentialMoves([]move{
		{dst: rLoc(1), src: rLoc(2)},
		{dst: rLoc(2), src: rLoc(3)},
	}, 100)
	if len(got) != 2 {
		t.Fatalf("got %d moves, want 2 (one per copy): %+v", len(got), got)
	}
	for _, m := range got {
		if !m.dst.IsReg || !m.src.IsReg {
			t.Errorf("acyclic copy touched a slot: %+v", got)
		}
	}
	// r2 is read by the first move and written by the second, so the read has to
	// come first: r1<-r2, then r2<-r3. The reverse order would leave r1 holding
	// r3's value.
	if !(got[0].dst.eq(rLoc(1)) && got[0].src.eq(rLoc(2))) {
		t.Errorf("overwrote r2 before r1 had read it: %+v", got)
	}
}

// A two-move cycle cannot be ordered, so exactly one value is parked in a temp
// — one park for the cycle, not one per move.
func TestParallelMovesCycleParksOnce(t *testing.T) {
	got := sequentialMoves([]move{
		{dst: rLoc(1), src: rLoc(2)},
		{dst: rLoc(2), src: rLoc(1)},
	}, 100)
	parks := 0
	for _, m := range got {
		if m.dst.eq(sLoc(100)) {
			parks++
		}
	}
	if parks != 1 {
		t.Errorf("cycle used %d temp parks, want exactly 1: %+v", parks, got)
	}
	if len(got) != 3 {
		t.Errorf("2-cycle took %d moves, want 3 (park + two copies): %+v", len(got), got)
	}
	// Whatever the order, the last write to each register must carry the other's
	// original value: r1 ends up with r2's, r2 with r1's.
	if final(got, rLoc(1)).eq(rLoc(1)) || final(got, rLoc(2)).eq(rLoc(2)) {
		t.Errorf("cycle collapsed to a self-copy, losing a value: %+v", got)
	}
}

// A cycle with a tail: r3 <- r1 hangs off the r1/r2 swap and must be emitted
// before the swap overwrites r1.
func TestParallelMovesCycleWithTail(t *testing.T) {
	got := sequentialMoves([]move{
		{dst: rLoc(1), src: rLoc(2)},
		{dst: rLoc(2), src: rLoc(1)},
		{dst: rLoc(3), src: rLoc(1)},
	}, 100)
	var seenR1Write bool
	for _, m := range got {
		if m.dst.eq(rLoc(3)) && seenR1Write && m.src.eq(rLoc(1)) {
			t.Errorf("r3 read r1 after r1 was overwritten: %+v", got)
		}
		if m.dst.eq(rLoc(1)) {
			seenR1Write = true
		}
	}
}

// final reports the source of the last move written to dst.
func final(got []move, dst Loc) Loc {
	out := dst
	for _, m := range got {
		if m.dst.eq(dst) {
			out = m.src
		}
	}
	return out
}

// The ordering has to be wired into emission, not just be available: a loop
// whose values all live in registers must resolve its back edge with register
// copies and touch no stack slot at all. Testing sequentialMoves alone would
// still pass if emitParallelMoves went back to routing every move through a
// temp, which is the regression this guards.
func TestLoopBackEdgeTouchesNoSlots(t *testing.T) {
	f := ssa.NewFunc("sum")
	n := f.AddParam()
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()

	i0 := constOp(f, entry, 0)
	s0 := constOp(f, entry, 0)
	f.SetBr(entry, header)

	iNext, sNext := f.NewValue(), f.NewValue()
	i := f.AddPhi(header, i0, iNext)
	sum := f.AddPhi(header, s0, sNext)
	f.SetBrIf(header, f.AddOp(header, ssa.OpLt, i, n), body, exit)

	add := f.AddOpNoResult(body, ssa.OpAdd, sum, i)
	add.Result = sNext
	inc := f.AddOpNoResult(body, ssa.OpAdd, i, constOp(f, body, 1))
	inc.Result = iNext
	f.SetBr(body, header)

	f.SetRet(exit, sum)

	p, err := Emit(f, 8)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	var stores, loads int
	for _, b := range p.Blocks {
		for _, in := range b.Insts {
			switch in.Op {
			case StoreSlot:
				stores++
			case LoadSlot:
				loads++
			}
		}
	}
	if stores != 0 || loads != 0 {
		t.Errorf("register-resident loop still spills to slots: %d stores, %d loads", stores, loads)
	}
}
