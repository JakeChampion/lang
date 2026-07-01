package ssa

import "testing"

// OpStore32 writes 4 bytes; OpLoad32U reads them back zero-extended. Two i32
// fields packed 4 bytes apart round-trip independently (the case an 8-byte
// access would overrun / alias).
func TestEvalLoad32RoundTrip(t *testing.T) {
	f := NewFunc("m")
	e := f.NewBlock()
	p := f.AddOp(e, OpAlloc, constIn(f, e, 8))
	// field A at +0 = 0x11223344, field B at +4 = 0x55667788.
	storeNOp(f, e, p, constIn(f, e, 0x11223344), 0, OpStore32)
	storeNOp(f, e, p, constIn(f, e, 0x55667788), 4, OpStore32)
	a := loadNOp(f, e, p, 0, OpLoad32U)
	b := loadNOp(f, e, p, 4, OpLoad32U)
	// (a & 0xff) + (b & 0xff) = 0x44 + 0x88 = 0xCC
	al := f.AddOp(e, OpAnd, a, constIn(f, e, 0xff))
	bl := f.AddOp(e, OpAnd, b, constIn(f, e, 0xff))
	f.SetRet(e, f.AddOp(e, OpAdd, al, bl))

	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 0xCC {
		t.Errorf("load32 round-trip = %#x, want 0xCC", got)
	}
}

// A 4-byte store leaves the adjacent 4 bytes untouched (no 8-byte overrun).
func TestEvalStore32NoOverrun(t *testing.T) {
	f := NewFunc("m")
	e := f.NewBlock()
	p := f.AddOp(e, OpAlloc, constIn(f, e, 8))
	storeNOp(f, e, p, constIn(f, e, 0x7fffffff), 4, OpStore32) // field B
	storeNOp(f, e, p, constIn(f, e, 0xA), 0, OpStore32)        // field A must not disturb B
	f.SetRet(e, loadNOp(f, e, p, 4, OpLoad32U))

	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 0x7fffffff {
		t.Errorf("adjacent field = %#x, want 0x7fffffff", got)
	}
}
