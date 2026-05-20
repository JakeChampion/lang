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

// TestCmpFlipUnsignedPredicates — unsigned variants flip too.
func TestCmpFlipUnsignedPredicates(t *testing.T) {
	cases := []struct {
		from OpKind
		want OpKind
	}{
		{OpLtU, OpGeU},
		{OpLeU, OpGtU},
		{OpGtU, OpLeU},
		{OpGeU, OpLtU},
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

// TestCmpFlipFloatEqNe — FEq / FNe are exact complements on
// every input (including NaN), so flipping is safe. FLt/FLe/
// FGt/FGe are NOT flipped (see TestCmpFlipFloatOrderedUntouched).
func TestCmpFlipFloatEqNe(t *testing.T) {
	cases := []struct {
		from OpKind
		want OpKind
	}{
		{OpFEq, OpFNe},
		{OpFNe, OpFEq},
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

// TestCmpFlipFloatOrderedUntouched — ordered float comparisons
// (FLt, FLe, FGt, FGe) are NOT flipped. `not(FLt NaN NaN)` is
// true but `FGe NaN NaN` is false — IEEE-754 says ordered
// comparisons on NaN return false, so they're not inverse.
func TestCmpFlipFloatOrderedUntouched(t *testing.T) {
	for _, k := range []OpKind{OpFLt, OpFLe, OpFGt, OpFGe} {
		t.Run(k.String(), func(t *testing.T) {
			f := NewFunc("f")
			a := f.AddParam()
			b := f.AddParam()
			entry := f.NewBlock()
			f.AddOp(entry, k, a, b)
			r := f.AddOp(entry, OpNot, entry.Ops[0].Result)
			f.SetRet(entry, r)

			CmpFlip(f)

			if entry.Ops[1].Kind != OpNot {
				t.Errorf("not(%v) was flipped to %v; ordered float compares "+
					"can't be inverted via not (NaN semantics)",
					k, entry.Ops[1].Kind)
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

// TestCmpFlipSelectNotUnwraps — `select(not(c), a, b)` is
// rewritten to `select(c, b, a)`. The OpNot is left in place
// for DCE.
func TestCmpFlipSelectNotUnwraps(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	n := f.AddOp(entry, OpNot, c)
	sel := f.AddOp(entry, OpSelect, n, a, b)
	f.SetRet(entry, sel)

	CmpFlip(f)

	selOp := entry.Ops[1]
	if len(selOp.Args) != 3 {
		t.Fatalf("Args = %v, want 3", selOp.Args)
	}
	if selOp.Args[0] != c {
		t.Errorf("Args[0] = %v, want %v (cond unwrapped)", selOp.Args[0], c)
	}
	if selOp.Args[1] != b {
		t.Errorf("Args[1] = %v, want %v (was b)", selOp.Args[1], b)
	}
	if selOp.Args[2] != a {
		t.Errorf("Args[2] = %v, want %v (was a)", selOp.Args[2], a)
	}
}

// TestCmpFlipSelectNonNotUntouched — `select(c, a, b)` where
// c isn't a not stays as-is.
func TestCmpFlipSelectNonNotUntouched(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sel := f.AddOp(entry, OpSelect, c, a, b)
	f.SetRet(entry, sel)

	CmpFlip(f)

	selOp := entry.Ops[0]
	if selOp.Args[0] != c || selOp.Args[1] != a || selOp.Args[2] != b {
		t.Errorf("Args = %v, want [c, a, b] (unchanged)", selOp.Args)
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
