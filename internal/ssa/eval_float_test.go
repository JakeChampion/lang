package ssa

import "testing"

func fConstOp(f *Func, b *Block, v float64) Value {
	x := f.AddOp(b, OpConstFloat)
	b.Ops[len(b.Ops)-1].F64 = v
	return x
}

// (1.5 + 2.5) * 2.0 = 8.0 -> FToIS -> 8.
func TestEvalFloatArith(t *testing.T) {
	f := NewFunc("fa")
	e := f.NewBlock()
	s := f.AddOp(e, OpFAdd, fConstOp(f, e, 1.5), fConstOp(f, e, 2.5))
	p := f.AddOp(e, OpFMul, s, fConstOp(f, e, 2.0))
	f.SetRet(e, f.AddOp(e, OpFToIS, p))
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 8 {
		t.Errorf("Eval((1.5+2.5)*2.0)->int = %d, want 8", got)
	}
}

// Float division and subtraction: (7.0 - 1.0) / 3.0 = 2.0 -> 2.
func TestEvalFloatDivSub(t *testing.T) {
	f := NewFunc("fd")
	e := f.NewBlock()
	d := f.AddOp(e, OpFSub, fConstOp(f, e, 7.0), fConstOp(f, e, 1.0))
	q := f.AddOp(e, OpFDiv, d, fConstOp(f, e, 3.0))
	f.SetRet(e, f.AddOp(e, OpFToIS, q))
	if got, _ := Eval(f); got != 2 {
		t.Errorf("(7-1)/3 -> int = %d, want 2", got)
	}
}

// Float comparison: (1.5 < 2.5) -> 1, (2.5 < 1.5) -> 0.
func TestEvalFloatCompare(t *testing.T) {
	mk := func(a, b float64) int64 {
		f := NewFunc("fc")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpFLt, fConstOp(f, e, a), fConstOp(f, e, b)))
		got, _ := Eval(f)
		return got
	}
	if got := mk(1.5, 2.5); got != 1 {
		t.Errorf("1.5 < 2.5 = %d, want 1", got)
	}
	if got := mk(2.5, 1.5); got != 0 {
		t.Errorf("2.5 < 1.5 = %d, want 0", got)
	}
}

// int -> float -> int round trip: FToIS(IToFS(5) + 0.9) truncates to 5.
func TestEvalFloatConvRoundTrip(t *testing.T) {
	f := NewFunc("cv")
	e := f.NewBlock()
	fv := f.AddOp(e, OpIToFS, constIn(f, e, 5))
	sum := f.AddOp(e, OpFAdd, fv, fConstOp(f, e, 0.9))
	f.SetRet(e, f.AddOp(e, OpFToIS, sum))
	if got, _ := Eval(f); got != 5 {
		t.Errorf("FToIS(5.0 + 0.9) = %d, want 5 (truncation)", got)
	}
}

// Float in memory: StoreF 3.75 then LoadF, truncate -> 3.
func TestEvalFloatMemory(t *testing.T) {
	f := NewFunc("fm")
	e := f.NewBlock()
	p := f.AddOp(e, OpAlloc, constIn(f, e, 8))
	stf := f.AddOpNoResult(e, OpStoreF, p, fConstOp(f, e, 3.75))
	stf.Imm = 0
	ld := f.AddOp(e, OpLoadF, p)
	e.Ops[len(e.Ops)-1].Imm = 0
	f.SetRet(e, f.AddOp(e, OpFToIS, ld))
	if got, _ := Eval(f); got != 3 {
		t.Errorf("LoadF(StoreF 3.75)->int = %d, want 3", got)
	}
}
