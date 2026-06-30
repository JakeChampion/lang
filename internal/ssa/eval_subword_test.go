package ssa

import "testing"

func storeNOp(f *Func, b *Block, base, val Value, offset int64, kind OpKind) {
	op := f.AddOpNoResult(b, kind, base, val)
	op.Imm = offset
}

func loadNOp(f *Func, b *Block, base Value, offset int64, kind OpKind) Value {
	v := f.AddOp(b, kind, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// Store the byte 200, then load it back unsigned (200) and signed (-56, since
// int8(200) == -56).
func TestEvalSubwordByteSign(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind OpKind
		want int64
	}{
		{"u", OpLoad8U, 200},
		{"s", OpLoad8S, -56},
	} {
		f := NewFunc("b")
		e := f.NewBlock()
		p := f.AddOp(e, OpAlloc, constIn(f, e, 8))
		storeNOp(f, e, p, constIn(f, e, 200), 0, OpStore8)
		f.SetRet(e, loadNOp(f, e, p, 0, tc.kind))
		got, err := Eval(f)
		if err != nil {
			t.Fatalf("%s: Eval: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("load8%s(200) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Halfword: store 0x1234, load16U -> 4660; store 0xFFFF, load16S -> -1.
func TestEvalSubwordHalfword(t *testing.T) {
	mk := func(stored int64, kind OpKind) int64 {
		f := NewFunc("h")
		e := f.NewBlock()
		p := f.AddOp(e, OpAlloc, constIn(f, e, 8))
		storeNOp(f, e, p, constIn(f, e, stored), 0, OpStore16)
		f.SetRet(e, loadNOp(f, e, p, 0, kind))
		got, _ := Eval(f)
		return got
	}
	if got := mk(0x1234, OpLoad16U); got != 0x1234 {
		t.Errorf("load16U(0x1234) = %d, want 4660", got)
	}
	if got := mk(0xFFFF, OpLoad16S); got != -1 {
		t.Errorf("load16S(0xFFFF) = %d, want -1", got)
	}
}

// A 3-byte array: store 10/20/30 at consecutive byte offsets, sum the loads ->
// 60. Exercises sub-word addressing.
func TestEvalSubwordByteArray(t *testing.T) {
	f := NewFunc("arr")
	e := f.NewBlock()
	p := f.AddOp(e, OpAlloc, constIn(f, e, 3))
	storeNOp(f, e, p, constIn(f, e, 10), 0, OpStore8)
	storeNOp(f, e, p, constIn(f, e, 20), 1, OpStore8)
	storeNOp(f, e, p, constIn(f, e, 30), 2, OpStore8)
	a := loadNOp(f, e, p, 0, OpLoad8U)
	b := loadNOp(f, e, p, 1, OpLoad8U)
	c := loadNOp(f, e, p, 2, OpLoad8U)
	f.SetRet(e, f.AddOp(e, OpAdd, f.AddOp(e, OpAdd, a, b), c))
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 60 {
		t.Errorf("byte array sum = %d, want 60", got)
	}
}

// store8 writes only the low byte, leaving the higher bytes of a previously
// stored full word intact: store 0xAABBCCDD then store8 0x11 -> 0xAABBCC11.
func TestEvalSubwordStorePreservesHighBytes(t *testing.T) {
	f := NewFunc("p")
	e := f.NewBlock()
	p := f.AddOp(e, OpAlloc, constIn(f, e, 8))
	// i32-positive so the const isn't sign-extended into the high bytes.
	storeOp(f, e, p, constIn(f, e, 0x2ABBCCDD), 0) // full 8-byte store
	storeNOp(f, e, p, constIn(f, e, 0x11), 0, OpStore8)
	f.SetRet(e, loadOp(f, e, p, 0))
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 0x2ABBCC11 {
		t.Errorf("store8 over full word = %#x, want 0x2ABBCC11", got)
	}
}
