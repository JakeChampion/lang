package ssa

import "testing"

// The proof's five conditions each get the case that fails without
// them, plus the shape they all admit. The bar is zeroslot_test.go's:
// a condition without a test that exercises it is a condition nobody
// can refactor safely.

// rc1Call adds a call to an RcResultOwned producer — born rc=1, live
// header — with provenance, which is what a lifted allocator call
// looks like.
func rc1Call(f *Func, b *Block) Value {
	v := f.AddOp(b, OpCall)
	o := b.Ops[len(b.Ops)-1]
	o.Str = "__fern_alloc_rc1"
	o.Addr = true
	o.SrcOp = int32(len(b.Ops)) // any non-zero index: provenance present
	return v
}

// guardOn adds the `__fern_rc_is_unique` call on v.
func guardOn(f *Func, b *Block, v Value) Value {
	g := f.AddOp(b, OpCall, v)
	o := b.Ops[len(b.Ops)-1]
	o.Str = "__fern_rc_is_unique"
	o.SrcOp = int32(len(b.Ops))
	return g
}

func helperCall(f *Func, b *Block, name string, v Value) Value {
	r := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = name
	return r
}

// dropDiamond finishes the canonical guard shape: brif g → free-arm /
// dec-arm → join → ret. Both arms use v, strictly after the guard.
func dropDiamond(f *Func, b *Block, g, v Value) {
	bT, bF, bJ := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(b, g, bT, bF)
	helperCall(f, bT, "__fern_box_free", v)
	f.SetBr(bT, bJ)
	helperCall(f, bF, "__fern_rc_dec", v)
	f.SetBr(bF, bJ)
	f.SetRet(bJ, Value{})
}

func soleVerdict(t *testing.T, f *Func) GuardSite {
	t.Helper()
	sites := SoleOwnedGuards(f, UnitsOf(f, nil), nil)
	if len(sites) != 1 {
		t.Fatalf("want exactly one guard site, got %d", len(sites))
	}
	return sites[0]
}

func TestSoleOwnedGuardIsProvenOnTheCanonicalDropShape(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := rc1Call(f, b)
	g := guardOn(f, b, v)
	dropDiamond(f, b, g, v)

	got := soleVerdict(t, f)
	if !got.Proven {
		t.Errorf("the canonical shape was refused: %s — rc=1 birth, no use before "+
			"the guard, both arm releases after it", got.Reason)
	}
}

func TestSoleOwnedGuardRefusesARetainBeforeTheGuard(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := rc1Call(f, b)
	helperCall(f, b, "__fern_rc_inc", v)
	g := guardOn(f, b, v)
	dropDiamond(f, b, g, v)

	got := soleVerdict(t, f)
	if got.Proven || got.Reason != "use-before-guard" {
		t.Errorf("a retained value was proven sole-owned (reason %q) — the count "+
			"is 2 at the guard and forcing it to 1 frees a shared box", got.Reason)
	}
}

func TestSoleOwnedGuardRefusesAStoreBeforeTheGuard(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := rc1Call(f, b)
	cell := f.AddOp(b, OpAlloc)
	f.AddOpNoResult(b, OpStore, cell, v)
	g := guardOn(f, b, v)
	dropDiamond(f, b, g, v)

	got := soleVerdict(t, f)
	if got.Proven || got.Reason != "use-before-guard" {
		t.Errorf("a stored value was proven sole-owned (reason %q) — memory now "+
			"reaches it and the proof cannot see who reads it back", got.Reason)
	}
}

func TestSoleOwnedGuardRefusesAGuardInsideALoop(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := rc1Call(f, b)
	loop := f.NewBlock()
	f.SetBr(b, loop)
	g := guardOn(f, loop, v)
	done := f.NewBlock()
	f.SetBrIf(loop, g, loop, done)
	helperCall(f, done, "__fern_rc_dec", v)
	f.SetRet(done, Value{})

	got := soleVerdict(t, f)
	if got.Proven || got.Reason != "in-loop" {
		t.Errorf("a guard in a cycle was proven (reason %q) — a use after it in "+
			"the cycle runs before its NEXT evaluation, and the birth does not", got.Reason)
	}
}

func TestSoleOwnedGuardRefusesAConsumedParameter(t *testing.T) {
	f := &Func{Name: "f"}
	p := f.NewValue()
	f.Params = []Value{p}
	f.ParamAddrs = []bool{true}
	b := f.NewBlock()
	f.Entry = b
	g := guardOn(f, b, p)
	dropDiamond(f, b, g, p)

	// Even under a signature that says Consumed, a handed-over unit is
	// not a count of 1 — the caller may have been one of several
	// owners. With nil sigs the origin reads borrowed; either way the
	// refusal is by origin class.
	got := soleVerdict(t, f)
	if got.Proven || got.Reason != "origin:borrowed" {
		t.Errorf("a parameter was proven sole-owned (reason %q) — \"the caller "+
			"handed me a unit\" and \"the count is 1\" are different claims", got.Reason)
	}
}

func TestSoleOwnedGuardRefusesARawAllocation(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpAlloc)
	g := guardOn(f, b, v)
	b.Ops[len(b.Ops)-1].SrcOp = 1
	dropDiamond(f, b, g, v)

	got := soleVerdict(t, f)
	if got.Proven || got.Reason != "fresh-not-rc1-producer" {
		t.Errorf("a raw allocation was proven (reason %q) — OpAlloc on this path "+
			"is the bare bump allocator, and [ptr-8] is a neighbour's bytes", got.Reason)
	}
}

func TestSoleOwnedGuardRefusesAValueThatFeedsAPhi(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := rc1Call(f, b)
	w := rc1Call(f, b)
	g := guardOn(f, b, v)
	bT, bF, bJ := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(b, g, bT, bF)
	f.SetBr(bT, bJ)
	f.SetBr(bF, bJ)
	merged := f.AddOp(bJ, OpPhi, v, w)
	helperCall(f, bJ, "__fern_rc_dec", merged)
	helperCall(f, bJ, "__fern_rc_dec", w)
	f.SetRet(bJ, Value{})

	got := SoleOwnedGuards(f, UnitsOf(f, nil), nil)[0]
	if got.Proven || got.Reason != "feeds-phi" {
		t.Errorf("a phi-feeding value was proven (reason %q) — after the join the "+
			"object answers to the phi's name and this proof is not per-path", got.Reason)
	}
}

// A solver-proven ReturnOwned callee is not an rc=1 birth — the proof
// is "the caller holds a unit", which says nothing about the count or
// about null — but a site refused ONLY for that is the widening
// headroom, so it is counted apart from the raw producers.
func TestSoleOwnedGuardCountsAnOwnedCalleeProducerApart(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := f.AddOp(b, OpCall)
	o := b.Ops[len(b.Ops)-1]
	o.Str = "mk_node"
	o.Addr = true
	o.SrcOp = 1
	g := guardOn(f, b, v)
	dropDiamond(f, b, g, v)

	sigs := map[string]Signature{"mk_node": {ReturnOwned: true}}
	sites := SoleOwnedGuards(f, UnitsOf(f, sigs), sigs)
	if len(sites) != 1 {
		t.Fatalf("want one site, got %d", len(sites))
	}
	got := sites[0]
	if got.Proven || got.Reason != "owned-callee-otherwise-proven" {
		t.Errorf("owned-callee producer read (proven=%v, %q), want the distinct "+
			"refusal class — a widening argument needs to know what it would buy", got.Proven, got.Reason)
	}
}

func TestSoleOwnedGuardRefusesAnUnmappedSite(t *testing.T) {
	f := &Func{Name: "f"}
	b := f.NewBlock()
	f.Entry = b
	v := rc1Call(f, b)
	g := f.AddOp(b, OpCall, v)
	b.Ops[len(b.Ops)-1].Str = "__fern_rc_is_unique"
	// no SrcOp: an answer with nowhere to apply it
	dropDiamond(f, b, g, v)

	got := soleVerdict(t, f)
	if got.Proven || got.Reason != "unmapped" {
		t.Errorf("an unmapped site was proven (reason %q)", got.Reason)
	}
}
