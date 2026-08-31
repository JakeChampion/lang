package ssa

import "testing"

// A static enum sentinel is an address and carries no unit. This is the
// class the oracle named first: `values flagged in
// __method_i64_checked_abs: alloc x212, enum_sentinel x106`.
func TestUnitsOfPlacesAStaticSentinelAsCarryingNothing(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	s := f.AddOp(b, OpEnumSentinel)
	b.Term = Terminator{Kind: TermRet}

	u := UnitsOf(f, nil)
	if got := u.Origin(s); got != UnitNone {
		t.Errorf("enum_sentinel origin = %v, want none — its rc word's high bit "+
			"makes every helper a no-op, so it can never be leaked", got)
	}
}

// A string literal is the same shape: a .rodata pointer with an
// immortal rc header.
func TestUnitsOfPlacesAStringLiteralAsCarryingNothing(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	s := f.AddOp(b, OpConstString)
	b.Term = Terminator{Kind: TermRet}

	if got := UnitsOf(f, nil).Origin(s); got != UnitNone {
		t.Errorf("const_string origin = %v, want none", got)
	}
}

// An allocation is the one definition that unambiguously puts a unit in
// the function's hands.
func TestUnitsOfPlacesAnAllocationAsFresh(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRet}

	if got := UnitsOf(f, nil).Origin(v); got != UnitFresh {
		t.Errorf("alloc origin = %v, want fresh", got)
	}
}

// A pointer read out of memory borrows the container's unit. The old
// probe read this as a fresh unit because `Op.Addr` is set on every
// 8-byte load.
func TestUnitsOfPlacesALoadAsBorrowed(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	base := f.AddOp(b, OpAlloc)
	v := f.AddOp(b, OpLoad, base)
	b.Term = Terminator{Kind: TermRet}

	if got := UnitsOf(f, nil).Origin(v); got != UnitBorrowed {
		t.Errorf("load origin = %v, want borrowed — the container owns the unit", got)
	}
}

// `base + fieldOffset` is the one object under an offset. The rc header
// sits below the pointer the program passes around, so what a function
// returns or stores is routinely `alloc + N`, and resolving it to the
// allocation is what lets a transfer of the derived pointer discharge
// the allocation's unit.
func TestUnitsOfResolvesAnInteriorAddressToItsBase(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	base := f.AddOp(b, OpAlloc)
	off := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpAdd, base, off)
	b.Term = Terminator{Kind: TermRet}

	u := UnitsOf(f, nil)
	if u.Root(v).ID != base.ID {
		t.Errorf("root of the interior address is v%d, want the allocation v%d",
			u.Root(v).ID, base.ID)
	}
	if got := u.Origin(v); got != UnitFresh {
		t.Errorf("interior address origin = %v, want fresh — it is the allocation offset", got)
	}
}

// Pointer arithmetic standing on nothing recognisable holds no unit of
// its own.
func TestUnitsOfPlacesAnUnanchoredOffsetAsBorrowed(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	a := f.AddOp(b, OpConstInt)
	c := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpAdd, a, c)
	b.Term = Terminator{Kind: TermRet}

	if got := UnitsOf(f, nil).Origin(v); got != UnitBorrowed {
		t.Errorf("unanchored offset origin = %v, want borrowed", got)
	}
}

// A retain hands back the pointer it was given, so the result is the
// operand under another name — not a second object. Getting this wrong
// is what makes a later release land on nothing.
func TestUnitsOfFollowsThePassThroughRename(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	inc := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	b.Term = Terminator{Kind: TermRet}

	u := UnitsOf(f, nil)
	if u.Root(inc).ID != v.ID {
		t.Errorf("root of the inc's result is v%d, want v%d", u.Root(inc).ID, v.ID)
	}
	if got := u.Origin(inc); got != UnitFresh {
		t.Errorf("the inc's result reads %v; it is the allocation under another name", got)
	}
}

// A move's result may be a DIFFERENT object — the copy-on-write helpers
// replace a shared receiver — so it is not a rename.
func TestUnitsOfDoesNotRenameThroughAMove(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	mv := f.AddOp(b, OpCall, v)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "__fern_arr_cow_inplace", true
	b.Term = Terminator{Kind: TermRet}

	u := UnitsOf(f, nil)
	if u.Root(mv).ID == v.ID {
		t.Error("a copy-on-write move's result was treated as its operand renamed")
	}
}

// A parameter the solved signature says is consumed arrives holding a
// unit; one it says is borrowed does not.
func TestUnitsOfSeparatesConsumedFromBorrowedParameters(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	p0 := f.AddParam()
	p1 := f.AddParam()
	f.ParamAddrs = []bool{true, true}
	b.Term = Terminator{Kind: TermRet}

	sigs := map[string]Signature{"f": {
		Params:  []ParamOwnership{Consumed, Borrowed},
		Pointer: []bool{true, true},
	}}
	u := UnitsOf(f, sigs)
	if got := u.Origin(p0); got != UnitTransferred {
		t.Errorf("consumed parameter origin = %v, want transferred", got)
	}
	if got := u.Origin(p1); got != UnitBorrowed {
		t.Errorf("borrowed parameter origin = %v, want borrowed", got)
	}
}

// A call whose result nothing classifies is unplaced and counted, not
// guessed at. `rcsigs.go` models argument effects and says outright
// that the result axis is not modelled.
func TestUnitsOfCountsAnUnclassifiedCallResult(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	c := f.AddOp(b, OpCall)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "some_defined_callee", true
	b.Term = Terminator{Kind: TermRet}

	u := UnitsOf(f, nil)
	if got := u.Origin(c); got != UnitUnknown {
		t.Errorf("unclassified call result origin = %v, want unknown", got)
	}
	if u.Unplaced() != 1 {
		t.Errorf("unplaced = %d, want 1 — the gap has to be countable", u.Unplaced())
	}
}

// Phase B's proof that a callee hands back a borrow of its arguments is
// the one thing that takes a call result out of "unknown".
func TestUnitsOfReadsABorrowReturningCallee(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	c := f.AddOp(b, OpCall)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "accessor", true
	b.Term = Terminator{Kind: TermRet}

	sigs := map[string]Signature{"accessor": {ReturnBorrowed: true, ReturnBorrowedFrom: []int{0}}}
	if got := UnitsOf(f, sigs).Origin(c); got != UnitBorrowed {
		t.Errorf("borrow-returning callee's result = %v, want borrowed", got)
	}
}

// A static closure cell is a `.rodata` constant, not a heap block, so
// it carries no unit — the same answer as an enum sentinel or a
// vtable. `ir.OpConstFunc` lifts to OpMakeClosure for dispatch
// uniformity, which is why the kind alone cannot decide this.
func TestUnitsOfPlacesAStaticClosureCellAsCarryingNothing(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpMakeClosure)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.StaticCell = "target", true
	b.Term = Terminator{Kind: TermRet}

	if got := UnitsOf(f, nil).Origin(v); got != UnitNone {
		t.Errorf("static closure cell origin = %v, want none — a `lea` against "+
			"`.rodata` can never be released", got)
	}
}

// And the heap form still allocates. A zero-capture closure that has
// NOT been rewritten is a 32-byte rc=1 block, so the two are
// indistinguishable by kind, Str and capture count alike.
func TestUnitsOfPlacesAHeapClosureAsFresh(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpMakeClosure)
	b.Ops[len(b.Ops)-1].Str = "target"
	b.Term = Terminator{Kind: TermRet}

	if got := UnitsOf(f, nil).Origin(v); got != UnitFresh {
		t.Errorf("heap closure origin = %v, want fresh", got)
	}
}
