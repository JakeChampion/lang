package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// An indirect call's result needs the same 64-bit annotation a direct call
// gets from AnnotateCallWidths, and for the same reason: a backend
// sign-extends a 32-bit-wide call result back into the register, which
// truncates an i64 and destroys a float (every float lives in a general
// register as its f64 BIT PATTERN, so masking to 32 bits keeps the low
// mantissa half and discards the sign and exponent — it reads back as a
// denormal ≈ 0).
//
// There is no callee name at an indirect call to look up, so the width comes
// from the signature the IR op carries. That is what makes this correct for
// BOTH shapes that reach it: a nested function, which its enclosing function
// calls through a closure cell rather than by name, and a function value
// arriving in a parameter, where no closure is in scope to trace at all.
//
// Found by the differential oracle: an `f32`-returning nested function
// returned 0.0 on arm64-ssa while interp, x86-64, arm64 and wasm all returned
// the value.
func TestLiftCallIndirectResultWidth(t *testing.T) {
	cases := []struct {
		name   string
		result ast.Type
		want   int8
	}{
		{"i32 result stays narrow", ast.NumberType{Width: 32}, 0},
		{"i64 result is widened", ast.NumberType{Width: 64}, 64},
		{"f64 result is widened", ast.FloatType{Width: 64}, 64},
		{"f32 result is widened despite width 32", ast.FloatType{Width: 32}, 64},
		{"string result stays narrow", ast.StringType{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &ir.Func{
				Name: "f",
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 0}, // callee cell
					{Kind: ir.OpCallIndirect, I32: 0, Ext: &ir.OpExt{
						Sig: &ast.FuncType{Result: c.result},
					}},
					{Kind: ir.OpReturn},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			var call *Op
			for _, b := range out.Blocks {
				for _, op := range b.Ops {
					if op.Kind == OpCallIndirect {
						call = op
					}
				}
			}
			if call == nil {
				t.Fatal("no OpCallIndirect in the lifted function")
			}
			if call.Width != c.want {
				t.Errorf("call_indirect Width = %d, want %d", call.Width, c.want)
			}
		})
	}
}

// A signature-less indirect call is left at its default width rather than
// guessed at. Every ir.OpCallIndirect emit site sets Sig today, so this pins
// the fallback for any that stops doing so.
func TestLiftCallIndirectWithoutSigKeepsDefaultWidth(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpCallIndirect, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	for _, b := range out.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpCallIndirect && op.Width != 0 {
				t.Errorf("call_indirect Width = %d, want 0 (no signature to read)", op.Width)
			}
		}
	}
}
