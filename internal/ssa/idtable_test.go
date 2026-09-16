package ssa

import "testing"

// A table covers every value the function holds, answers the zero value for an
// invalid one, and neither reads nor writes past its end.
func TestIDTableCoversTheFunctionAndBoundsTheRest(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 7
	sum := f.AddOp(entry, OpAdd, x, c)
	f.SetRet(entry, sum)

	tab := newIDTable[int32](f)
	for _, v := range []Value{x, c, sum} {
		if got := tab.get(v); got != 0 {
			t.Errorf("a fresh table answers %d for %v, want 0", got, v)
		}
		tab.set(v, v.ID+100)
	}
	for _, v := range []Value{x, c, sum} {
		if got, want := tab.get(v), v.ID+100; got != want {
			t.Errorf("tab.get(%v) = %d, want %d", v, got, want)
		}
	}

	if got := tab.get(Value{}); got != 0 {
		t.Errorf("tab.get(invalid) = %d, want 0", got)
	}
	tab.set(Value{}, 5) // must not panic or write anywhere

	// A value minted after the table was built is outside it: absent, not a
	// panic, which is how the map behaved.
	later := f.AddOp(entry, OpConstInt)
	if got := tab.get(later); got != 0 {
		t.Errorf("tab.get(a value minted later) = %d, want 0", got)
	}
	tab.set(later, 9)
	if got := tab.get(later); got != 0 {
		t.Errorf("setting a value outside the table stored %d, want it dropped", got)
	}
	if got := newIDTable[int32](nil); got != nil {
		t.Errorf("newIDTable(nil) = %v, want nil", got)
	}
}

// The use index answers for every value it recorded and stays safe for one
// minted after it was built.
func TestUsesIndexBoundsValuesMintedAfterTheBuild(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	twice := f.AddOp(entry, OpAdd, sum, sum)
	f.SetRet(entry, twice)

	u := BuildUses(f)
	if got, want := u.Count(a), 1; got != want {
		t.Errorf("Count(a) = %d, want %d", got, want)
	}
	if got, want := u.Count(sum), 2; got != want {
		t.Errorf("Count(sum) = %d, want %d", got, want)
	}
	if got, want := u.Count(twice), 1; got != want { // the ret terminator
		t.Errorf("Count(twice) = %d, want %d", got, want)
	}
	if u.HasUses(Value{}) {
		t.Error("HasUses(invalid) = true, want false")
	}
	later := f.AddOp(entry, OpConstInt)
	if u.HasUses(later) || u.Of(later) != nil {
		t.Error("a value minted after the build reads as used")
	}
	var nilUses *Uses
	if nilUses.Count(a) != 0 || nilUses.Of(a) != nil {
		t.Error("a nil index answers something")
	}
}
