package ssa

import (
	"math"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestFoldReinterpretF64ToI64 — pi as f64 reinterpreted to its
// IEEE-754 bit pattern.
func TestFoldReinterpretF64ToI64(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = math.Pi
	r := f.AddOp(entry, OpReinterpretF64ToI64, c)
	f.SetRet(entry, r)

	Fold(f)
	want := int64(math.Float64bits(math.Pi))
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != want {
		t.Errorf("F64→I64 = %x, want %x", got.Imm, want)
	}
}

// TestFoldReinterpretI64ToF64 — the bit pattern of pi
// reinterpreted back to f64.
func TestFoldReinterpretI64ToF64(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = int64(math.Float64bits(math.Pi))
	r := f.AddOp(entry, OpReinterpretI64ToF64, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != math.Pi {
		t.Errorf("I64→F64 = %g, want %g", got.F64, math.Pi)
	}
}

// TestFoldReinterpretF32ToI32 — pi as f32 → its bit pattern.
func TestFoldReinterpretF32ToI32(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = float64(float32(math.Pi))
	r := f.AddOp(entry, OpReinterpretF32ToI32, c)
	f.SetRet(entry, r)

	Fold(f)
	want := int64(int32(math.Float32bits(float32(math.Pi))))
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != want {
		t.Errorf("F32→I32 = %x, want %x", got.Imm, want)
	}
}

// TestFoldReinterpretI32ToF32 — bit pattern → f32 (stored as f64).
func TestFoldReinterpretI32ToF32(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = int64(math.Float32bits(float32(math.Pi)))
	r := f.AddOp(entry, OpReinterpretI32ToF32, c)
	f.SetRet(entry, r)

	Fold(f)
	want := float64(float32(math.Pi))
	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != want {
		t.Errorf("I32→F32 = %g, want %g", got.F64, want)
	}
}

// TestLiftReinterpretAllShapes — every IR variant lifts to its
// SSA kind.
func TestLiftReinterpretAllShapes(t *testing.T) {
	cases := []struct {
		from ir.OpKind
		want OpKind
	}{
		{ir.OpReinterpretI32F32, OpReinterpretF32ToI32},
		{ir.OpReinterpretF32I32, OpReinterpretI32ToF32},
		{ir.OpReinterpretI64F64, OpReinterpretF64ToI64},
		{ir.OpReinterpretF64I64, OpReinterpretI64ToF64},
	}
	for _, c := range cases {
		t.Run(c.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name: "f",
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 0}, // arbitrary input
					{Kind: c.from},
					{Kind: ir.OpDrop},
					{Kind: ir.OpReturnVoid},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			if out.Blocks[0].Ops[1].Kind != c.want {
				t.Errorf("Kind = %v, want %v", out.Blocks[0].Ops[1].Kind, c.want)
			}
		})
	}
}

// TestReinterpretOpKindStrings — pin printer output.
func TestReinterpretOpKindStrings(t *testing.T) {
	cases := []struct {
		k    OpKind
		want string
	}{
		{OpReinterpretF32ToI32, "reinterpret_f32_to_i32"},
		{OpReinterpretI32ToF32, "reinterpret_i32_to_f32"},
		{OpReinterpretF64ToI64, "reinterpret_f64_to_i64"},
		{OpReinterpretI64ToF64, "reinterpret_i64_to_f64"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}
