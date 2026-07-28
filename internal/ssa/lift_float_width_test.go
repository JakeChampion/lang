package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The lift carries an f32 op's width onto the SSA op. It is the only source of
// that information downstream: the constant folders round to f32 when they see
// Width 32, and the backends emit their fcvt round trip on the same signal. The
// lift used to propagate only Width 64 — on the theory that "floats carry width
// in their kind" — which silently left every f32 computation on the SSA path
// running at f64 precision.
//
// Integer ops keep the opposite convention: 32 is the default and stays 0, the
// value maskFix reads. Asserting both directions here keeps a future "just
// propagate every width" change from breaking the integer side.
func TestLiftCarriesFloatWidth(t *testing.T) {
	cases := []struct {
		name    string
		irKind  ir.OpKind
		ssaKind OpKind
		width   int
		want    int8
	}{
		{"f32 mul keeps 32", ir.OpFMul, OpFMul, 32, 32},
		{"f32 add keeps 32", ir.OpFAdd, OpFAdd, 32, 32},
		{"f32 sub keeps 32", ir.OpFSub, OpFSub, 32, 32},
		{"f32 div keeps 32", ir.OpFDiv, OpFDiv, 32, 32},
		{"f64 mul keeps 64", ir.OpFMul, OpFMul, 64, 64},
		{"i32 mul stays 0", ir.OpMul, OpMul, 32, 0},
		{"i64 mul keeps 64", ir.OpMul, OpMul, 64, 64},
		// A float COMPARISON yields a bool, so there is nothing to round and
		// the width is deliberately not propagated.
		{"f32 compare stays 0", ir.OpFLt, OpFLt, 32, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &ir.Func{
				Name: "main",
				Ops: []ir.Op{
					{Kind: ir.OpConstF64, F64: 1.5},
					{Kind: ir.OpConstF64, F64: 2.5},
					{Kind: c.irKind, Width: c.width},
					{Kind: ir.OpReturn},
				},
			}
			f, err := LiftFromIR(fn)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			var found *Op
			for _, b := range f.Blocks {
				for _, op := range b.Ops {
					if op.Kind == c.ssaKind {
						found = op
					}
				}
			}
			if found == nil {
				t.Fatalf("no %v op in lifted function", c.ssaKind)
			}
			if found.Width != c.want {
				t.Errorf("%v Width = %d, want %d", c.ssaKind, found.Width, c.want)
			}
		})
	}
}
