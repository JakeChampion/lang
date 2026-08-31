package ssa

import "testing"

// retFn builds a function with n pointer parameters that returns
// whatever `build` hands back.
func retFn(name string, n int, build func(f *Func, b *Block, ps []Value) Value) *Func {
	f := NewFunc(name)
	f.ReturnAddr = true
	ps := make([]Value, n)
	f.ParamAddrs = make([]bool, n)
	for i := range ps {
		ps[i] = f.AddParam()
		f.ParamAddrs[i] = true
	}
	b := f.NewBlock()
	f.Entry = b
	f.SetRet(b, build(f, b, ps))
	return f
}

func sigOf(t *testing.T, sol Solution, name string) Signature {
	t.Helper()
	s, ok := sol.Sigs[name]
	if !ok {
		t.Fatalf("%s: no signature", name)
	}
	return s
}

// The base case: a function that hands its parameter straight back
// returns a borrow, anchored on that parameter.
func TestSolveReturnsProvesAPassThroughIsABorrow(t *testing.T) {
	f := retFn("ident", 1, func(f *Func, b *Block, ps []Value) Value { return ps[0] })
	sol := SolveOwnership(map[string]*Func{"ident": f})
	sig := sigOf(t, sol, "ident")
	if !sig.ReturnBorrowed {
		t.Fatal("a function returning its parameter returns a borrow")
	}
	if len(sig.ReturnBorrowedFrom) != 1 || sig.ReturnBorrowedFrom[0] != 0 {
		t.Errorf("want the borrow anchored on parameter 0, got %v", sig.ReturnBorrowedFrom)
	}
}

// An interior pointer — `self + offset` — points into the borrowed
// object and is a borrow of it. This is the largest category the first
// draft could not prove.
func TestSolveReturnsFollowsPointerArithmetic(t *testing.T) {
	f := retFn("field", 1, func(f *Func, b *Block, ps []Value) Value {
		k := f.AddOp(b, OpConstInt)
		b.Ops[len(b.Ops)-1].Imm = 8
		return f.AddOp(b, OpAdd, ps[0], k)
	})
	sol := SolveOwnership(map[string]*Func{"field": f})
	sig := sigOf(t, sol, "field")
	if !sig.ReturnBorrowed || len(sig.ReturnBorrowedFrom) != 1 || sig.ReturnBorrowedFrom[0] != 0 {
		t.Errorf("an interior pointer is a borrow of its base, got %v %v",
			sig.ReturnBorrowed, sig.ReturnBorrowedFrom)
	}
	// An interior pointer into a FRESH object is owned rather than
	// borrowed — and since the split, that is a verdict the pass can
	// state rather than merely withhold.
	g := retFn("fresh_field", 0, func(f *Func, b *Block, ps []Value) Value {
		a := f.AddOp(b, OpAlloc)
		k := f.AddOp(b, OpConstInt)
		b.Ops[len(b.Ops)-1].Imm = 8
		return f.AddOp(b, OpAdd, a, k)
	})
	sol = SolveOwnership(map[string]*Func{"fresh_field": g})
	if sig := sigOf(t, sol, "fresh_field"); sig.ReturnBorrowed || !sig.ReturnOwned {
		t.Errorf("an interior pointer into a fresh object is owned, got borrowed=%v owned=%v",
			sig.ReturnBorrowed, sig.ReturnOwned)
	}
}

// A constant carries no unit, so it neither proves nor blocks — a
// function returning only constants has no borrow to report.
func TestSolveReturnsTreatsConstantsAsCarryingNothing(t *testing.T) {
	f := retFn("lit", 1, func(f *Func, b *Block, ps []Value) Value {
		v := f.AddOp(b, OpConstString)
		b.Ops[len(b.Ops)-1].Str = "hello"
		return v
	})
	sol := SolveOwnership(map[string]*Func{"lit": f})
	if sig := sigOf(t, sol, "lit"); sig.ReturnBorrowed || sig.ReturnOwned {
		t.Errorf("a function returning only a literal hands back no unit and no borrow, "+
			"got borrowed=%v owned=%v", sig.ReturnBorrowed, sig.ReturnOwned)
	}
}

// The recursive edge: `outer` returns what `inner` returned, and inner
// returns its parameter. The anchor has to travel back.
func TestSolveReturnsPropagatesThroughACall(t *testing.T) {
	inner := retFn("inner", 1, func(f *Func, b *Block, ps []Value) Value { return ps[0] })
	outer := retFn("outer", 1, func(f *Func, b *Block, ps []Value) Value {
		v := f.AddOp(b, OpCall, ps[0])
		b.Ops[len(b.Ops)-1].Str = "inner"
		return v
	})
	sol := SolveOwnership(map[string]*Func{"inner": inner, "outer": outer})
	sig := sigOf(t, sol, "outer")
	if !sig.ReturnBorrowed || len(sig.ReturnBorrowedFrom) != 1 || sig.ReturnBorrowedFrom[0] != 0 {
		t.Errorf("the anchor must travel through the call, got %v %v",
			sig.ReturnBorrowed, sig.ReturnBorrowedFrom)
	}
}

// The payoff, and the reason the anchor is recorded rather than a bare
// flag: a caller that passes its parameter to a borrow-returning
// function and then releases the RESULT has released the parameter.
// Without the anchor the two are unrelated values and the parameter
// reads as borrowed.
func TestSolveReturnsMakesAReleaseOfTheResultAReleaseOfTheArgument(t *testing.T) {
	inner := retFn("passthrough", 1, func(f *Func, b *Block, ps []Value) Value { return ps[0] })

	caller := NewFunc("caller")
	p := caller.AddParam()
	caller.ParamAddrs = []bool{true}
	b := caller.NewBlock()
	caller.Entry = b
	got := caller.AddOp(b, OpCall, p)
	b.Ops[len(b.Ops)-1].Str = "passthrough"
	dec := caller.AddOpNoResult(b, OpCall, got)
	dec.Str = "__fern_rc_dec"
	b.Term = Terminator{Kind: TermRet}

	sol := SolveOwnership(map[string]*Func{"passthrough": inner, "caller": caller})
	if sigOf(t, sol, "caller").Params[0] != Consumed {
		t.Error("releasing the result of a borrow-returning call releases the argument")
	}
}

// A pair return is the Option / Result ABI's (tag, payload) convention.
// Which half carries the unit is a different question, so it gets NO
// verdict rather than a guessed one — in either direction.
func TestSolveReturnsDoesNotClassifyAPairReturn(t *testing.T) {
	f := NewFunc("pair")
	f.ReturnAddr = true
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	b := f.NewBlock()
	f.Entry = b
	tag := f.AddOp(b, OpConstInt)
	f.SetRetPair(b, tag, p)

	sol := SolveOwnership(map[string]*Func{"pair": f})
	if sig := sigOf(t, sol, "pair"); sig.ReturnBorrowed || sig.ReturnOwned {
		t.Errorf("a pair return is not classified, got borrowed=%v owned=%v",
			sig.ReturnBorrowed, sig.ReturnOwned)
	}
}

// A function whose return is not an address has no unit to hand back,
// so calling its return borrowed would be a statement about nothing.
func TestSolveReturnsSkipsNonAddressReturns(t *testing.T) {
	f := retFn("scalar", 1, func(f *Func, b *Block, ps []Value) Value { return ps[0] })
	f.ReturnAddr = false
	sol := SolveOwnership(map[string]*Func{"scalar": f})
	if sig := sigOf(t, sol, "scalar"); sig.ReturnBorrowed || sig.ReturnOwned {
		t.Errorf("a scalar return carries no unit, got borrowed=%v owned=%v",
			sig.ReturnBorrowed, sig.ReturnOwned)
	}
}

// The positive half. A function that returns what it allocated hands
// the caller a unit, and before clsOwned was split the pass had no way
// to say so — "allocates" and "not understood" were one answer, and
// only the second is entitled to block a proof.
func TestSolveReturnsProvesAnAllocatingReturnIsOwned(t *testing.T) {
	f := &Func{Name: "mk", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRet, Value: v}

	sigs := SolveOwnership(map[string]*Func{"mk": f}).Sigs
	if !sigs["mk"].ReturnOwned {
		t.Error("a function returning its own allocation was not proved to return an owned unit")
	}
	if sigs["mk"].ReturnBorrowed {
		t.Error("the two verdicts are mutually exclusive and both are set")
	}
}

// A field read is the case the split must NOT turn into a positive
// answer: the container owns the reference, so claiming it owned would
// tell every caller to release something it never acquired.
func TestSolveReturnsLeavesALoadUnproven(t *testing.T) {
	f := &Func{Name: "get", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	v := f.AddOp(b, OpLoad, p)
	b.Term = Terminator{Kind: TermRet, Value: v}

	sigs := SolveOwnership(map[string]*Func{"get": f}).Sigs
	if sigs["get"].ReturnOwned || sigs["get"].ReturnBorrowed {
		t.Errorf("a field read was given a verdict: owned=%v borrowed=%v",
			sigs["get"].ReturnOwned, sigs["get"].ReturnBorrowed)
	}
}

// One arm borrows and the other allocates: neither conclusion holds on
// every path, so neither may be claimed.
func TestSolveReturnsRefusesAMixOfBorrowAndFresh(t *testing.T) {
	f := &Func{Name: "either", ReturnAddr: true}
	entry := f.NewBlock()
	lhs := f.NewBlock()
	rhs := f.NewBlock()
	f.Entry = entry
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBrIf, Cond: c, True: lhs, False: rhs}
	lhs.Preds = []*Block{entry}
	rhs.Preds = []*Block{entry}
	lhs.Term = Terminator{Kind: TermRet, Value: p}
	v := f.AddOp(rhs, OpAlloc)
	rhs.Term = Terminator{Kind: TermRet, Value: v}

	sigs := SolveOwnership(map[string]*Func{"either": f}).Sigs
	if sigs["either"].ReturnOwned || sigs["either"].ReturnBorrowed {
		t.Errorf("a function that borrows on one path and allocates on the other was "+
			"given a verdict: owned=%v borrowed=%v",
			sigs["either"].ReturnOwned, sigs["either"].ReturnBorrowed)
	}
}

// The Option shape: `Some(box)` allocates and `None` is a static
// sentinel. A release of a sentinel short-circuits on its rc word, so
// the caller may release the result whichever arm ran — the neutral
// value yields rather than blocking.
func TestSolveReturnsTreatsASentinelArmAsYielding(t *testing.T) {
	f := &Func{Name: "opt", ReturnAddr: true}
	entry := f.NewBlock()
	some := f.NewBlock()
	none := f.NewBlock()
	f.Entry = entry
	c := f.AddOp(entry, OpConstBool)
	entry.Term = Terminator{Kind: TermBrIf, Cond: c, True: some, False: none}
	some.Preds = []*Block{entry}
	none.Preds = []*Block{entry}
	v := f.AddOp(some, OpAlloc)
	some.Term = Terminator{Kind: TermRet, Value: v}
	sent := f.AddOp(none, OpEnumSentinel)
	none.Term = Terminator{Kind: TermRet, Value: sent}

	if !SolveOwnership(map[string]*Func{"opt": f}).Sigs["opt"].ReturnOwned {
		t.Error("Some(box) beside a None sentinel was not proved owned")
	}
}

// A runtime helper whose RESULT axis says immortal carries no unit, so
// returning one is neutral rather than a blocked proof. Before the
// result axis existed every call blocked.
func TestSolveReturnsReadsTheResultAxisForAHelper(t *testing.T) {
	f := &Func{Name: "boxed", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	n := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpCall, n)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "__fern_alloc_box", true
	b.Term = Terminator{Kind: TermRet, Value: v}

	sigs := SolveOwnership(map[string]*Func{"boxed": f}).Sigs
	if sigs["boxed"].ReturnOwned {
		t.Error("a static-sentinel box was proved owned — nothing can release it")
	}
	if sigs["boxed"].ReturnBorrowed {
		t.Error("a static-sentinel box was proved a borrow of a parameter")
	}
}

// And the owned half of the same table: a helper that allocates makes
// its caller's return owned too.
func TestSolveReturnsPropagatesAnOwnedHelperResult(t *testing.T) {
	f := &Func{Name: "buf", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	n := f.AddOp(b, OpConstInt)
	v := f.AddOp(b, OpCall, n)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Addr = "__alloc_u8", true
	b.Term = Terminator{Kind: TermRet, Value: v}

	if !SolveOwnership(map[string]*Func{"buf": f}).Sigs["buf"].ReturnOwned {
		t.Error("a return of a freshly allocated buffer was not proved owned")
	}
}

// The verdict propagates across the call graph: a forwarder that
// returns what an owned-returning callee gave it is owned as well.
func TestSolveReturnsPropagatesOwnedAcrossACall(t *testing.T) {
	mk := &Func{Name: "mk", ReturnAddr: true}
	b := mk.NewBlock()
	mk.Entry = b
	v := mk.AddOp(b, OpAlloc)
	b.Term = Terminator{Kind: TermRet, Value: v}

	fwd := &Func{Name: "fwd", ReturnAddr: true}
	fb := fwd.NewBlock()
	fwd.Entry = fb
	r := fwd.AddOp(fb, OpCall)
	op := fb.Ops[len(fb.Ops)-1]
	op.Str, op.Addr = "mk", true
	fb.Term = Terminator{Kind: TermRet, Value: r}

	sigs := SolveOwnership(map[string]*Func{"mk": mk, "fwd": fwd}).Sigs
	if !sigs["fwd"].ReturnOwned {
		t.Error("a forwarder of an owned-returning callee was not proved owned")
	}
}

// A fresh box the function STORES into memory and returns as well hands
// back a borrow of what the container now owns, not a unit. Nothing
// retained it, so the store is what the container's ownership rests on,
// and telling every caller it owns the result makes each of them
// account for a unit it never acquired.
//
// `__map_grow_keyed` in core/map.fern is the shape, and it is what made
// the certifier report `__map_set_keyed_impl` as leaking across 21
// fixtures the runtime proves clean.
func TestSolveReturnsWithholdsOwnedWhenTheFreshValueWasStored(t *testing.T) {
	mk := func(store bool) Signature {
		f := &Func{Name: "grow", ReturnAddr: true}
		b := f.NewBlock()
		f.Entry = b
		p := f.AddParam()
		f.ParamAddrs = []bool{true}
		v := f.AddOp(b, OpAlloc)
		if store {
			f.AddOpNoResult(b, OpStore, p, v)
		}
		b.Term = Terminator{Kind: TermRet, Value: v}
		return SolveOwnership(map[string]*Func{"grow": f}).Sigs["grow"]
	}
	if got := mk(false); !got.ReturnOwned {
		t.Error("without the store, a returned allocation is owned")
	}
	if got := mk(true); got.ReturnOwned {
		t.Error("a fresh box stored into a parameter is not the function's to hand out")
	}
	// Not a borrow either: the value is a borrow of the CONTAINER, and
	// ReturnBorrowedFrom can only name a parameter position.
	if got := mk(true); got.ReturnBorrowed {
		t.Error("the answer is unknown, not an anchor the pass cannot support")
	}
}

// The same fact through the raw pointer store the stdlib writes with.
// `__store_ptr` moves no COUNT — `rcsigs.go` calls it inert — but the
// pointer it writes is reachable from the container afterwards exactly
// as OpStore's is.
func TestSolveReturnsWithholdsOwnedForARawPointerStore(t *testing.T) {
	f := &Func{Name: "grow", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	v := f.AddOp(b, OpAlloc)
	st := f.AddOpNoResult(b, OpCall, p, v)
	st.Str = "__store_ptr"
	b.Term = Terminator{Kind: TermRet, Value: v}

	if SolveOwnership(map[string]*Func{"grow": f}).Sigs["grow"].ReturnOwned {
		t.Error("a fresh box written through __store_ptr is not returned owned")
	}
}

// Storing a RETAINED copy is the other direction: the container gets a
// unit of its own and the function keeps the one it allocated, so the
// return really is owned. This is why the escape set does not follow
// the rc-helper rename chain.
func TestSolveReturnsKeepsOwnedWhenTheStoreWasRetained(t *testing.T) {
	f := &Func{Name: "grow", ReturnAddr: true}
	b := f.NewBlock()
	f.Entry = b
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	v := f.AddOp(b, OpAlloc)
	inc := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	f.AddOpNoResult(b, OpStore, p, inc)
	b.Term = Terminator{Kind: TermRet, Value: v}

	if !SolveOwnership(map[string]*Func{"grow": f}).Sigs["grow"].ReturnOwned {
		t.Error("storing a retained copy leaves the allocation's own unit with the function")
	}
}
