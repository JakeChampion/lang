package ssa

import "testing"

func constStr(f *Func, b *Block, s string) Value {
	v := f.AddOp(b, OpConstString)
	b.Ops[len(b.Ops)-1].Str = s
	return v
}

// A string literal materialises to a pointer whose bytes are the literal, and
// OpConstStringLen yields its byte length. Return len + byte[0] + byte[1] for
// "Hello" -> 5 + 'H'(72) + 'e'(101) = 178.
func TestEvalConstString(t *testing.T) {
	f := NewFunc("s")
	e := f.NewBlock()
	s := constStr(f, e, "Hello")
	l := f.AddOp(e, OpConstStringLen, s)
	b0 := loadNOp(f, e, s, 0, OpLoad8U)
	b1 := loadNOp(f, e, s, 1, OpLoad8U)
	sum := f.AddOp(e, OpAdd, f.AddOp(e, OpAdd, l, b0), b1)
	f.SetRet(e, sum)

	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 178 {
		t.Errorf("Eval(string) = %d, want 178 (len 5 + 'H' 72 + 'e' 101)", got)
	}
}

// Empty string: length 0, pointer valid (no bytes to read).
func TestEvalConstStringEmpty(t *testing.T) {
	f := NewFunc("e")
	e := f.NewBlock()
	s := constStr(f, e, "")
	f.SetRet(e, f.AddOp(e, OpConstStringLen, s))
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 0 {
		t.Errorf("len(\"\") = %d, want 0", got)
	}
}
