package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The three bit-count IR ops lift, and carry the OPERAND width through. The
// result is an i32 count at either width, so the width is only observable in
// the answer — clz(1) is 31 at 32 and 63 at 64 — which is exactly what a
// backend that reads the wrong one gets wrong.
func TestLiftBitCountOps(t *testing.T) {
	cases := []struct {
		kind  ir.OpKind
		width int
		imm   int32
		want  int64
		op    OpKind
	}{
		{ir.OpPopcount, 32, 255, 8, OpPopcount},
		{ir.OpClz, 32, 1, 31, OpClz},
		{ir.OpClz, 64, 1, 63, OpClz},
		{ir.OpCtz, 32, 8, 3, OpCtz},
		{ir.OpCtz, 64, 1024, 10, OpCtz},
		// Zero is defined as the operand width, not left undefined.
		{ir.OpClz, 32, 0, 32, OpClz},
		{ir.OpCtz, 64, 0, 64, OpCtz},
	}
	for _, c := range cases {
		in := &ir.Func{
			Name: "f",
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: c.imm},
				{Kind: c.kind, Width: c.width},
				{Kind: ir.OpReturn},
			},
		}
		out, err := LiftFromIR(in)
		if err != nil {
			t.Fatalf("%v/%d: LiftFromIR: %v", c.kind, c.width, err)
		}
		if err := Verify(out); err != nil {
			t.Fatalf("%v/%d: Verify: %v", c.kind, c.width, err)
		}
		op := out.Blocks[0].Ops[1]
		if op.Kind != c.op {
			t.Errorf("%v/%d: lifted to %v, want %v", c.kind, c.width, op.Kind, c.op)
		}
		if got := int(op.Width); (c.width == 64) != (got == 64) {
			t.Errorf("%v/%d: lifted Width = %d, want the operand's", c.kind, c.width, got)
		}
		got, err := Eval(out)
		if err != nil {
			t.Fatalf("%v/%d: Eval: %v", c.kind, c.width, err)
		}
		if got != c.want {
			t.Errorf("%v/%d of %d = %d, want %d", c.kind, c.width, c.imm, got, c.want)
		}
	}
}
