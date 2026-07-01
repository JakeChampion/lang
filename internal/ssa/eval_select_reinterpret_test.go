package ssa

import (
	"math"
	"testing"
)

// setLastWidth sets Width on the most-recently-added op in b.
func setLastWidth(b *Block, w int8) {
	b.Ops[len(b.Ops)-1].Width = w
}

// OpSelect picks then/else on a nonzero/zero condition.
func TestEvalSelect(t *testing.T) {
	sel := func(cond int64) int64 {
		f := NewFunc("s")
		e := f.NewBlock()
		r := f.AddOp(e, OpSelect, constIn(f, e, cond), constIn(f, e, 111), constIn(f, e, 222))
		f.SetRet(e, r)
		got, err := Eval(f)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		return got
	}
	if got := sel(1); got != 111 {
		t.Errorf("select(true) = %d, want 111", got)
	}
	if got := sel(0); got != 222 {
		t.Errorf("select(false) = %d, want 222", got)
	}
	if got := sel(-5); got != 111 {
		t.Errorf("select(nonzero) = %d, want 111", got)
	}
}

// OpReinterpretF64ToI64 exposes a float's raw f64 bit pattern as an i64.
func TestEvalReinterpretF64ToI64(t *testing.T) {
	f := NewFunc("r")
	e := f.NewBlock()
	c := fConstOp(f, e, 1.5)
	bits := f.AddOp(e, OpReinterpretF64ToI64, c)
	setLastWidth(e, 64)
	f.SetRet(e, bits)
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if want := int64(math.Float64bits(1.5)); got != want {
		t.Errorf("reinterpret f64->i64 = %#x, want %#x", got, want)
	}
}

// OpReinterpretI64ToF64 then back to i64 round-trips a bit pattern.
func TestEvalReinterpretI64RoundTrip(t *testing.T) {
	f := NewFunc("r")
	e := f.NewBlock()
	raw := constIn(f, e, int64(math.Float64bits(-3.25)))
	setLastWidth(e, 64)
	asF := f.AddOp(e, OpReinterpretI64ToF64, raw)
	setLastWidth(e, 64)
	back := f.AddOp(e, OpReinterpretF64ToI64, asF)
	setLastWidth(e, 64)
	f.SetRet(e, back)
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if want := int64(math.Float64bits(-3.25)); got != want {
		t.Errorf("i64->f64->i64 = %#x, want %#x", got, want)
	}
}

// OpReinterpretF32ToI32 exposes an f32's 32-bit pattern.
func TestEvalReinterpretF32ToI32(t *testing.T) {
	f := NewFunc("r")
	e := f.NewBlock()
	c := fConstOp(f, e, 2.5)
	setLastWidth(e, 32) // store at f32 precision
	bits := f.AddOp(e, OpReinterpretF32ToI32, c)
	setLastWidth(e, 32)
	f.SetRet(e, bits)
	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if want := int64(int32(math.Float32bits(2.5))); got != want {
		t.Errorf("reinterpret f32->i32 = %#x, want %#x", got, want)
	}
}
