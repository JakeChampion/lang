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

// A fresh allocation is a unit the caller owns, and must not be
// mistaken for a borrow.
func TestSolveReturnsLeavesAFreshAllocationOwned(t *testing.T) {
	f := retFn("make", 0, func(f *Func, b *Block, ps []Value) Value {
		return f.AddOp(b, OpAlloc)
	})
	sol := SolveOwnership(map[string]*Func{"make": f})
	if sigOf(t, sol, "make").ReturnBorrowed {
		t.Error("a returned allocation is owned")
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
	// But not when the base is fresh.
	g := retFn("fresh_field", 0, func(f *Func, b *Block, ps []Value) Value {
		a := f.AddOp(b, OpAlloc)
		k := f.AddOp(b, OpConstInt)
		b.Ops[len(b.Ops)-1].Imm = 8
		return f.AddOp(b, OpAdd, a, k)
	})
	sol = SolveOwnership(map[string]*Func{"fresh_field": g})
	if sigOf(t, sol, "fresh_field").ReturnBorrowed {
		t.Error("an interior pointer into a FRESH object is owned")
	}
}

// One owned edge is enough to sink the proof: a function that returns
// its parameter on one path and a fresh object on another owns its
// return.
func TestSolveReturnsRequiresEveryPathToBeABorrow(t *testing.T) {
	f := NewFunc("maybe")
	f.ReturnAddr = true
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	entry, borrow, own := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	entry.Term = Terminator{Kind: TermBrIf, Cond: p, True: borrow, False: own}
	f.SetRet(borrow, p)
	f.SetRet(own, f.AddOp(own, OpAlloc))

	sol := SolveOwnership(map[string]*Func{"maybe": f})
	if sigOf(t, sol, "maybe").ReturnBorrowed {
		t.Error("one allocating path makes the whole return owned")
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
	if sigOf(t, sol, "lit").ReturnBorrowed {
		t.Error("a function returning only a literal has no borrow to hand back")
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
// Which half carries the unit is a different question, so it stays
// owned rather than being guessed at.
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
	if sigOf(t, sol, "pair").ReturnBorrowed {
		t.Error("a pair return is not classified")
	}
}

// A function whose return is not an address has no unit to hand back,
// so calling its return borrowed would be a statement about nothing.
func TestSolveReturnsSkipsNonAddressReturns(t *testing.T) {
	f := retFn("scalar", 1, func(f *Func, b *Block, ps []Value) Value { return ps[0] })
	f.ReturnAddr = false
	sol := SolveOwnership(map[string]*Func{"scalar": f})
	if sigOf(t, sol, "scalar").ReturnBorrowed {
		t.Error("a scalar return carries no unit")
	}
}
