package ssa

import "testing"

// TestCmpFlipNotEq — `not(a == b)` rewrites to `a != b`.
func TestCmpFlipNotEq(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	cmp := f.AddOp(entry, OpEq, a, b)
	r := f.AddOp(entry, OpNot, cmp)
	f.SetRet(entry, r)

	CmpFlip(f)

	flipped := entry.Ops[1]
	if flipped.Kind != OpNe {
		t.Errorf("Kind = %v, want OpNe", flipped.Kind)
	}
	if len(flipped.Args) != 2 || flipped.Args[0] != a || flipped.Args[1] != b {
		t.Errorf("Args = %v, want [a, b]", flipped.Args)
	}
	if flipped.Result != r {
		t.Errorf("Result changed; downstream uses would break")
	}
}

// TestCmpFlipAllPredicates — every comparison kind flips
// correctly.
func TestCmpFlipAllPredicates(t *testing.T) {
	cases := []struct {
		from OpKind
		want OpKind
	}{
		{OpEq, OpNe},
		{OpNe, OpEq},
		{OpLt, OpGe},
		{OpLe, OpGt},
		{OpGt, OpLe},
		{OpGe, OpLt},
	}
	for _, c := range cases {
		t.Run(c.from.String(), func(t *testing.T) {
			f := NewFunc("f")
			a := f.AddParam()
			b := f.AddParam()
			entry := f.NewBlock()
			cmp := f.AddOp(entry, c.from, a, b)
			r := f.AddOp(entry, OpNot, cmp)
			f.SetRet(entry, r)

			CmpFlip(f)

			if got := entry.Ops[1].Kind; got != c.want {
				t.Errorf("not(%v) → %v, want %v", c.from, got, c.want)
			}
		})
	}
}

// TestCmpFlipLeavesUnrelated — OpNot whose arg isn't a
// comparison is untouched.
func TestCmpFlipLeavesUnrelated(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	// Not of a Param (which isn't a comparison) — stays.
	r := f.AddOp(entry, OpNot, p)
	f.SetRet(entry, r)

	CmpFlip(f)
	if entry.Ops[0].Kind != OpNot {
		t.Errorf("Kind = %v, want OpNot (arg isn't a cmp)", entry.Ops[0].Kind)
	}
}

// TestCmpFlipOriginalCmpStays — the original comparison Op
// is left in place (other consumers may still need it). DCE
// reclaims it only when use count drops to zero.
func TestCmpFlipOriginalCmpStays(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	cmp := f.AddOp(entry, OpEq, a, b)
	r := f.AddOp(entry, OpNot, cmp)
	// Another use of cmp to prevent DCE from removing it.
	_ = f.AddOp(entry, OpAdd, cmp, cmp)
	f.SetRet(entry, r)

	CmpFlip(f)

	if entry.Ops[0].Kind != OpEq {
		t.Errorf("original cmp.Kind = %v, want OpEq (still here)", entry.Ops[0].Kind)
	}
	if entry.Ops[1].Kind != OpNe {
		t.Errorf("not-rewrite kind = %v, want OpNe", entry.Ops[1].Kind)
	}
}

// TestCmpFlipInOptimizePipeline — end-to-end. After
// Optimize, the not + cmp chain collapses to a single
// inverted cmp; original cmp dies via DCE.
func TestCmpFlipInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	cmp := f.AddOp(entry, OpLt, a, b)
	r := f.AddOp(entry, OpNot, cmp)
	f.SetRet(entry, r)

	Optimize(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1; got kinds %v", len(entry.Ops), opKinds(entry.Ops))
	}
	if entry.Ops[0].Kind != OpGe {
		t.Errorf("survivor.Kind = %v, want OpGe", entry.Ops[0].Kind)
	}
}

// TestCmpFlipNilFunc — defensive.
func TestCmpFlipNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CmpFlip(nil) panicked: %v", r)
		}
	}()
	CmpFlip(nil)
}
