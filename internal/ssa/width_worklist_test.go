package ssa

import "testing"

// Address-ness that has to travel several calls in each direction: an
// allocation returned by the innermost callee flows out through two more
// returns, and a value known to be an address only because the outermost
// caller stores through it flows back into the parameter of a function
// three calls away. The functions are named so that the settling order
// visits every caller before its callee, so each fact arrives after the
// function that needs it has already been settled once, and only the queue
// carries it there.
func TestResolveWidthsCarriesAddressesAcrossSeveralCalls(t *testing.T) {
	funcs := map[string]*Func{}
	mk := func(name string) (*Func, *Block) {
		f := NewFunc(name)
		funcs[name] = f
		return f, f.NewBlock()
	}
	call := func(f *Func, b *Block, callee string, args ...Value) *Op {
		f.AddOp(b, OpCall, args...)
		op := b.Ops[len(b.Ops)-1]
		op.Str = callee
		return op
	}

	// c (innermost) returns an allocation; b returns c's result plus an
	// offset; a returns b's result. Settled in name order, a and b are seen
	// before c has said anything about its return.
	cF, cB := mk("c")
	block := cF.AddOp(cB, OpAlloc, zeroConst(cF, cB))
	cF.SetRet(cB, block)
	bF, bB := mk("b")
	viaC := call(bF, bB, "c")
	bF.SetRet(bB, bF.AddOp(bB, OpAdd, viaC.Result, zeroConst(bF, bB)))
	aF, aB := mk("a")
	viaB := call(aF, aB, "b")
	aF.SetRet(aB, viaB.Result)

	// z stores through its parameter; y passes its own parameter to z; x
	// passes its parameter to y; w calls x with a value it only ever adds to.
	// Settled in name order, w, x and y come before z has marked anything.
	zF, zB := mk("z")
	zP := zF.AddParam()
	zF.AddOpNoResult(zB, OpStore, zP, zeroConst(zF, zB))
	zF.SetRet(zB, zeroConst(zF, zB))
	yF, yB := mk("y")
	yP := yF.AddParam()
	call(yF, yB, "z", yP)
	yF.SetRet(yB, zeroConst(yF, yB))
	xF, xB := mk("x")
	xP := xF.AddParam()
	call(xF, xB, "y", xP)
	xF.SetRet(xB, zeroConst(xF, xB))
	wF, wB := mk("w")
	wP := wF.AddParam()
	sum := wF.AddOp(wB, OpAdd, wP, zeroConst(wF, wB))
	call(wF, wB, "x", sum)
	wF.SetRet(wB, zeroConst(wF, wB))

	ResolveWidths(funcs)

	for name, f := range map[string]*Func{"a": aF, "b": bF, "c": cF} {
		if !f.ReturnAddr {
			t.Errorf("%s does not return an address after resolution", name)
		}
	}
	if viaB.Width != 64 || !bB.Ops[2].Addr {
		t.Errorf("the address out of c did not reach a's call (Width %d) and b's add (Addr %v)", viaB.Width, bB.Ops[2].Addr)
	}
	for name, f := range map[string]*Func{"x": xF, "y": yF, "z": zF} {
		if len(f.ParamAddrs) == 0 || !f.ParamAddrs[0] {
			t.Errorf("%s's parameter is not an address after resolution: %v", name, f.ParamAddrs)
		}
	}
	if !wB.Ops[1].Addr {
		t.Errorf("w's add, whose result reaches z's store three calls away, was not marked")
	}
}
