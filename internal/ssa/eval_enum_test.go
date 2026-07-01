package ssa

import "testing"

func enumSentinel(f *Func, b *Block, tag int64) Value {
	v := f.AddOp(b, OpEnumSentinel)
	b.Ops[len(b.Ops)-1].Imm = tag
	return v
}

// Two sentinels with the same tag share a pointer; different tags do not; and
// the tag is stored at the pointer.
func TestEvalEnumSentinel(t *testing.T) {
	// same tag -> equal pointers -> 1
	sameTag := func() int64 {
		f := NewFunc("s")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpEq, enumSentinel(f, e, 3), enumSentinel(f, e, 3)))
		got, _ := Eval(f)
		return got
	}
	if got := sameTag(); got != 1 {
		t.Errorf("same-tag sentinels equal = %d, want 1", got)
	}

	// different tags -> distinct pointers -> 0
	diffTag := func() int64 {
		f := NewFunc("d")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpEq, enumSentinel(f, e, 3), enumSentinel(f, e, 7)))
		got, _ := Eval(f)
		return got
	}
	if got := diffTag(); got != 0 {
		t.Errorf("different-tag sentinels equal = %d, want 0", got)
	}

	// the tag is stored at the sentinel pointer
	tagStored := func() int64 {
		f := NewFunc("t")
		e := f.NewBlock()
		s := enumSentinel(f, e, 5)
		f.SetRet(e, loadNOp(f, e, s, 0, OpLoad8U))
		got, _ := Eval(f)
		return got
	}
	if got := tagStored(); got != 5 {
		t.Errorf("sentinel tag byte = %d, want 5", got)
	}
}
