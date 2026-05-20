package ssa

import "testing"

// TestUsesParamReadByOp — a Param consumed by an Op shows up
// as a single Op-arg use site, with Index pointing at the
// correct slot.
func TestUsesParamReadByOp(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpAdd, Result: f.NewValue(), Args: []Value{p, p}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	u := BuildUses(f)
	sites := u.Of(p)
	if len(sites) != 2 {
		t.Fatalf("uses of p = %d, want 2", len(sites))
	}
	for i, s := range sites {
		if s.Op != op {
			t.Errorf("sites[%d].Op = %v, want %v", i, s.Op, op)
		}
		if s.Block != entry {
			t.Errorf("sites[%d].Block = %v, want entry", i, s.Block)
		}
		if s.Index != i {
			t.Errorf("sites[%d].Index = %d, want %d", i, s.Index, i)
		}
	}
}

// TestUsesTerminatorReadsCountAsUses — values read by BrIf or
// Ret terminators surface in the use list. The Op field is
// nil to discriminate from Op-arg uses.
func TestUsesTerminatorReadsCountAsUses(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetRet(thenB, one)
	f.SetRet(elseB, Value{})

	u := BuildUses(f)

	// c is used by entry's BrIf terminator only.
	cSites := u.Of(c)
	if len(cSites) != 1 {
		t.Fatalf("uses of c = %d, want 1", len(cSites))
	}
	if cSites[0].Op != nil {
		t.Errorf("BrIf.Cond use should have nil Op, got %v", cSites[0].Op)
	}
	if cSites[0].Block != entry {
		t.Errorf("BrIf.Cond use Block = %v, want entry", cSites[0].Block)
	}

	// one is used by thenB's Ret terminator only.
	oneSites := u.Of(one)
	if len(oneSites) != 1 {
		t.Fatalf("uses of one = %d, want 1", len(oneSites))
	}
	if oneSites[0].Op != nil || oneSites[0].Block != thenB {
		t.Errorf("Ret.Value use mismatch: %+v", oneSites[0])
	}
}

// TestUsesNoneForUnusedValue — a Value with no readers has
// Count 0 and Of returns nil (caller-rangeable).
func TestUsesNoneForUnusedValue(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	usedAdd := f.AddOp(entry, OpAdd, a, b)
	deadMul := f.AddOp(entry, OpMul, a, b) // result unused
	f.SetRet(entry, usedAdd)
	_ = deadMul

	u := BuildUses(f)
	if got := u.Count(deadMul); got != 0 {
		t.Errorf("Count(deadMul) = %d, want 0", got)
	}
	if got := u.Of(deadMul); got != nil {
		t.Errorf("Of(deadMul) = %v, want nil", got)
	}
	if !u.HasUses(usedAdd) {
		t.Error("HasUses(usedAdd) = false, want true")
	}
}

// TestUsesNilFunc — defensive: nil input gives an empty
// index, nil queries return zero values.
func TestUsesNilFunc(t *testing.T) {
	u := BuildUses(nil)
	if u == nil {
		t.Fatal("BuildUses(nil) returned nil, want empty Uses")
	}
	if got := u.Of(Value{ID: 7}); got != nil {
		t.Errorf("Of on empty index = %v, want nil", got)
	}
	if got := u.Count(Value{ID: 7}); got != 0 {
		t.Errorf("Count on empty index = %d, want 0", got)
	}
	if u.HasUses(Value{ID: 7}) {
		t.Error("HasUses on empty index = true, want false")
	}

	var nilU *Uses
	if nilU.Count(Value{ID: 1}) != 0 {
		t.Error("(*Uses)(nil).Count should be 0, not panic")
	}
}

// TestUsesZeroSentinel — the zero Value sentinel is never
// indexed (it doesn't name any real def).
func TestUsesZeroSentinel(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	// Build an Op with one valid + one zero arg; the zero
	// shouldn't register as a use.
	op := &Op{Kind: OpAdd, Result: f.NewValue(), Args: []Value{a, {}}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, Value{})

	u := BuildUses(f)
	if got := u.Count(Value{}); got != 0 {
		t.Errorf("Count(zero) = %d, want 0", got)
	}
	if got := u.Count(a); got != 1 {
		t.Errorf("Count(a) = %d, want 1", got)
	}
}

// TestUsesMultipleSitesAcrossBlocks — same Value consumed by
// Ops in different blocks shows up under each call site.
func TestUsesMultipleSitesAcrossBlocks(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	next := f.NewBlock()
	_ = f.AddOp(entry, OpAdd, p, p)
	f.SetBr(entry, next)
	_ = f.AddOp(next, OpMul, p, p)
	f.SetRet(next, Value{})

	u := BuildUses(f)
	sites := u.Of(p)
	// 2 args × 2 ops = 4 sites.
	if len(sites) != 4 {
		t.Fatalf("Of(p) len = %d, want 4", len(sites))
	}
	// Sanity: at least one in entry, at least one in next.
	var sawEntry, sawNext bool
	for _, s := range sites {
		if s.Block == entry {
			sawEntry = true
		}
		if s.Block == next {
			sawNext = true
		}
	}
	if !sawEntry || !sawNext {
		t.Errorf("expected uses in both entry and next; sawEntry=%v sawNext=%v", sawEntry, sawNext)
	}
}

// TestUsesPhiArgs — phi op args count as uses for the
// incoming values. Each phi arg position becomes one UseSite
// with Op = the phi.
func TestUsesPhiArgs(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	u := BuildUses(f)
	if got := u.Count(one); got != 1 {
		t.Errorf("Count(one) = %d, want 1 (phi arg 0)", got)
	}
	if got := u.Count(two); got != 1 {
		t.Errorf("Count(two) = %d, want 1 (phi arg 1)", got)
	}
	if got := u.Count(phi); got != 1 {
		t.Errorf("Count(phi result) = %d, want 1 (ret value)", got)
	}
	// The phi result use should be a terminator use (nil Op).
	if sites := u.Of(phi); len(sites) != 1 || sites[0].Op != nil {
		t.Errorf("phi-result use site = %+v, want terminator-style", sites)
	}
}
