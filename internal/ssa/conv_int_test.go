package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestFoldTrunc — trunc(0xFFFFFFFF_00000005) → int32 5
// (sign-aware: int64(int32(0xFFFFFFFF_00000005)) = 5).
func TestFoldTrunc(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	highMask := uint64(0xFFFFFFFF00000005)
	entry.Ops[0].Imm = int64(highMask)
	r := f.AddOp(entry, OpTrunc, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != 5 {
		t.Errorf("Trunc = {%v 0x%x}, want {OpConstInt 5}", got.Kind, got.Imm)
	}
}

// TestFoldExtendS — extend_s(-2 as i32) → -2 as i64.
func TestFoldExtendS(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = int64(uint32(0xFFFFFFFE)) // -2 as i32, stored as uint32
	r := f.AddOp(entry, OpExtendS, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != -2 {
		t.Errorf("ExtendS = {%v %d}, want {OpConstInt -2}", got.Kind, got.Imm)
	}
}

// TestFoldExtendU — extend_u(-2 as i32 = 0xFFFFFFFE) → 0xFFFFFFFE.
func TestFoldExtendU(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -2
	r := f.AddOp(entry, OpExtendU, c)
	f.SetRet(entry, r)

	Fold(f)
	wantImm := int64(0xFFFFFFFE)
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != wantImm {
		t.Errorf("ExtendU = {%v 0x%x}, want {OpConstInt 0x%x}", got.Kind, got.Imm, wantImm)
	}
}

// TestFoldExtend8S — extend8_s(0xFF) → -1.
func TestFoldExtend8S(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0xFF
	r := f.AddOp(entry, OpExtend8S, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != -1 {
		t.Errorf("Extend8S = {%v %d}, want {OpConstInt -1}", got.Kind, got.Imm)
	}
}

// TestFoldExtend16S — extend16_s(0xFFFF) → -1.
func TestFoldExtend16S(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0xFFFF
	r := f.AddOp(entry, OpExtend16S, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != -1 {
		t.Errorf("Extend16S = {%v %d}, want {OpConstInt -1}", got.Kind, got.Imm)
	}
}

// TestLiftConvI32SToI64 — ir.OpExtendI32S lifts to ssa.OpExtendS.
func TestLiftConvI32SToI64(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpExtendI32S},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Ops[0].Kind != OpExtendS {
		t.Errorf("Kind = %v, want OpExtendS", out.Blocks[0].Ops[0].Kind)
	}
}

// TestLiftConvAllShapes — every IR conversion op maps to its
// SSA equivalent.
func TestLiftConvAllShapes(t *testing.T) {
	cases := []struct {
		from ir.OpKind
		want OpKind
	}{
		{ir.OpExtendI32S, OpExtendS},
		{ir.OpExtendI32U, OpExtendU},
		{ir.OpWrapI64, OpTrunc},
		{ir.OpSignExtend8, OpExtend8S},
		{ir.OpSignExtend16, OpExtend16S},
	}
	for _, c := range cases {
		t.Run(c.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name:   "f",
				Params: []ast.Param{{Name: "a"}},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: c.from},
					{Kind: ir.OpReturn},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			if out.Blocks[0].Ops[0].Kind != c.want {
				t.Errorf("Kind = %v, want %v", out.Blocks[0].Ops[0].Kind, c.want)
			}
		})
	}
}

// TestConvOpKindStrings — printer pinning.
func TestConvOpKindStrings(t *testing.T) {
	cases := []struct {
		k    OpKind
		want string
	}{
		{OpTrunc, "trunc"},
		{OpExtendS, "extend_s"},
		{OpExtendU, "extend_u"},
		{OpExtend8S, "extend8_s"},
		{OpExtend16S, "extend16_s"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}
