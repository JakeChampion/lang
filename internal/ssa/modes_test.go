package ssa

import "testing"

// A callee that keeps its parameter: the retain is what an owned
// variant would elide.
func storingCallee(name string) *Func {
	f := &Func{Name: name}
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	b := f.NewBlock()
	f.AddOp(b, OpCall, p)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	b.Term = Terminator{Kind: TermRet}
	return f
}

// A callee that only reads its parameter.
func readingCallee(name string) *Func {
	f := &Func{Name: name}
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	b := f.NewBlock()
	f.AddOp(b, OpLoad, p)
	b.Term = Terminator{Kind: TermRet}
	return f
}

// A callee that releases its parameter, which the solver calls consumed.
func consumingCallee(name string) *Func {
	f := &Func{Name: name}
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	b := f.NewBlock()
	dec(f, b, p)
	b.Term = Terminator{Kind: TermRet}
	return f
}

func sitesOf(t *testing.T, funcs map[string]*Func, caller string) []CallModeSite {
	t.Helper()
	sol := SolveOwnership(funcs)
	var out []CallModeSite
	for _, s := range CallModeSites(funcs, sol) {
		if s.Caller == caller {
			out = append(out, s)
		}
	}
	return out
}

func wantOneClass(t *testing.T, sites []CallModeSite, class string) CallModeSite {
	t.Helper()
	if len(sites) != 1 {
		t.Fatalf("want one site, got %d: %+v", len(sites), sites)
	}
	if got := sites[0].Class(); got != class {
		t.Fatalf("class = %s, want %s: %+v", got, class, sites[0])
	}
	return sites[0]
}

// The shape #7792 was filed about: a fresh value handed to a borrowed
// position, released straight after, and the callee retains it. An
// owned variant would turn that retain and this release into a transfer.
func TestCallModeSitesFindsTheRemovablePair(t *testing.T) {
	f := &Func{Name: "caller"}
	b := f.NewBlock()
	v := f.AddOp(b, OpAlloc)
	callVoid(f, b, "store_it", v)
	dec(f, b, v)
	b.Term = Terminator{Kind: TermRet}

	s := wantOneClass(t, sitesOf(t, map[string]*Func{"caller": f, "store_it": storingCallee("store_it")}, "caller"),
		ClassOwnedVariantPair)
	if s.Mode != Borrowed || s.Origin != UnitFresh || !s.Dying || !s.CalleeRetains {
		t.Errorf("site = %+v", s)
	}
}

// The same call against a callee that only reads: the release after the
// call is the value's only release, and an owned variant would move it
// into the callee rather than delete it.
func TestCallModeSitesSeesADeferredReleaseAsNoWin(t *testing.T) {
	f := &Func{Name: "caller"}
	b := f.NewBlock()
	v := f.AddOp(b, OpAlloc)
	callVoid(f, b, "read_it", v)
	dec(f, b, v)
	b.Term = Terminator{Kind: TermRet}

	wantOneClass(t, sitesOf(t, map[string]*Func{"caller": f, "read_it": readingCallee("read_it")}, "caller"),
		ClassOwnedVariantDeferred)
}

// The other direction: the caller retains before a consuming call
// because it still needs the value afterwards. A borrowed variant would
// delete that retain and the callee's release.
func TestCallModeSitesFindsTheRetainOwedToAConsumedPosition(t *testing.T) {
	f := &Func{Name: "caller"}
	b := f.NewBlock()
	v := f.AddOp(b, OpAlloc)
	r := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	callVoid(f, b, "take_it", r)
	f.AddOp(b, OpLoad, v)
	dec(f, b, v)
	b.Term = Terminator{Kind: TermRet}

	s := wantOneClass(t, sitesOf(t, map[string]*Func{"caller": f, "take_it": consumingCallee("take_it")}, "caller"),
		ClassBorrowedVariantPair)
	if s.Mode != Consumed || s.Dying || !s.CallerRetains {
		t.Errorf("site = %+v", s)
	}
}

// A loop-threaded value reaches a consuming call on every iteration and
// the phi walk reads it as live at each one. Without a retain feeding the
// call the caller paid nothing for it, and the site is not a pair.
func TestCallModeSitesNeedsAWitnessedRetainForABorrowedVariant(t *testing.T) {
	f := &Func{Name: "caller"}
	entry, header, body, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	seed := f.AddOp(entry, OpAlloc)
	f.SetBr(entry, header)
	next := f.AddOp(body, OpCall, seed) // placeholder operand, replaced below
	body.Ops[0].Str = "take_it"
	cur := f.AddPhi(header, seed, next)
	f.SetBrIf(header, cur, body, exit)
	body.Ops[0].Args[0] = cur
	f.SetBr(body, header)
	exit.Term = Terminator{Kind: TermRet}

	s := wantOneClass(t, sitesOf(t, map[string]*Func{"caller": f, "take_it": consumingCallee("take_it")}, "caller"),
		ClassOptimal)
	if s.Dying || s.CallerRetains {
		t.Errorf("site = %+v", s)
	}
}

// A dying value into a consumed position is a transfer, and a live value
// into a borrowed position is a borrow: both are what the solved mode
// was chosen for.
func TestCallModeSitesLeavesTheSolvedModeAlone(t *testing.T) {
	f := &Func{Name: "caller"}
	b := f.NewBlock()
	v := f.AddOp(b, OpAlloc)
	callVoid(f, b, "take_it", v)
	w := f.AddOp(b, OpAlloc)
	callVoid(f, b, "read_it", w)
	f.AddOp(b, OpLoad, w)
	dec(f, b, w)
	b.Term = Terminator{Kind: TermRet}

	sites := sitesOf(t, map[string]*Func{
		"caller": f, "take_it": consumingCallee("take_it"), "read_it": readingCallee("read_it"),
	}, "caller")
	if len(sites) != 2 {
		t.Fatalf("want two sites, got %d: %+v", len(sites), sites)
	}
	for _, s := range sites {
		if s.Class() != ClassOptimal {
			t.Errorf("%s at %s reads %s, want optimal", s.Callee, s.Caller, s.Class())
		}
	}
}

// A later push into a container carries a release effect in the runtime
// table too, but it hands the unit to the container: the value is still
// wanted after the call, so the site is a borrow the caller needs.
func TestCallModeSitesTreatsAStoreIntoAContainerAsAUse(t *testing.T) {
	f := &Func{Name: "caller"}
	b := f.NewBlock()
	arr := f.AddOp(b, OpAlloc)
	v := f.AddOp(b, OpAlloc)
	callVoid(f, b, "read_it", v)
	callVoid(f, b, "__method_Array_push", arr, v)
	b.Term = Terminator{Kind: TermRet}

	wantOneClass(t, sitesOf(t, map[string]*Func{"caller": f, "read_it": readingCallee("read_it")}, "caller"),
		ClassOptimal)
}

// A scalar position and a callee outside the solved set are not sites:
// there is no mode to deviate from.
func TestCallModeSitesSkipsScalarsAndOpaqueCallees(t *testing.T) {
	scalar := &Func{Name: "scalar"}
	scalar.AddParam()
	scalar.ParamAddrs = []bool{false}
	sb := scalar.NewBlock()
	sb.Term = Terminator{Kind: TermRet}

	f := &Func{Name: "caller"}
	b := f.NewBlock()
	v := f.AddOp(b, OpAlloc)
	callVoid(f, b, "scalar", v)
	callVoid(f, b, "nobody_knows", v)
	dec(f, b, v)
	b.Term = Terminator{Kind: TermRet}

	if sites := sitesOf(t, map[string]*Func{"caller": f, "scalar": scalar}, "caller"); len(sites) != 0 {
		t.Errorf("want no sites, got %+v", sites)
	}
}

// The retain can be on a pass-through alias of the parameter rather than
// the parameter itself, and that is still the callee keeping it.
func TestParamRetainedFollowsThePassThroughAlias(t *testing.T) {
	f := &Func{Name: "keeps"}
	p := f.AddParam()
	q := f.AddParam()
	f.ParamAddrs = []bool{true, true}
	b := f.NewBlock()
	r := f.AddOp(b, OpCall, p)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_dec"
	f.AddOp(b, OpCall, r)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_inc"
	f.AddOp(b, OpLoad, q)
	b.Term = Terminator{Kind: TermRet}

	got := ParamRetained(f, nil)
	if len(got) != 2 || !got[0] || got[1] {
		t.Errorf("ParamRetained = %v, want [true false]", got)
	}
}
