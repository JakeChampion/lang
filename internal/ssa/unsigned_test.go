package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestFoldDivU — unsigned division on the int64-bit pattern
// 0xFFFFFFFF_FFFFFFFE (which is -2 signed) by 2 gives a huge
// positive number — not -1 like signed div would.
func TestFoldDivU(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -2 // 0xFFFFFFFFFFFFFFFE as uint64
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	r := f.AddOp(entry, OpDivU, a, b)
	entry.Ops[2].Width = 64 // i64 op: fold with u64 semantics
	f.SetRet(entry, r)

	Fold(f)
	wantImm := int64(uint64(0xFFFFFFFFFFFFFFFE) / 2)
	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != wantImm {
		t.Errorf("DivU = {%v %d}, want {OpConstInt %d}", got.Kind, got.Imm, wantImm)
	}
}

// TestFoldLtU — unsigned -1 (= max uint64) is NOT less than 1.
func TestFoldLtU(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -1 // huge as unsigned
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 1
	r := f.AddOp(entry, OpLtU, a, b)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[2]; got.Kind != OpConstBool || got.Imm != 0 {
		t.Errorf("LtU = {%v %d}, want {OpConstBool 0}", got.Kind, got.Imm)
	}
}

// TestFoldShrU — logical right shift fills with zero, unlike
// arithmetic OpShr which sign-extends.
func TestFoldShrU(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -1 // all-ones bit pattern
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 4
	r := f.AddOp(entry, OpShrU, a, b)
	entry.Ops[2].Width = 64 // i64 op: fold with u64 semantics
	f.SetRet(entry, r)

	Fold(f)
	wantImm := int64(uint64(0xFFFFFFFFFFFFFFFF) >> 4)
	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != wantImm {
		t.Errorf("ShrU = {%v %x}, want {OpConstInt %x}", got.Kind, got.Imm, wantImm)
	}
}

// TestLiftDivUDispatch — ir.OpDivS with Unsigned=true lifts to
// ssa.OpDivU.
func TestLiftDivUDispatch(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpDivS, Unsigned: true},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Ops[0].Kind != OpDivU {
		t.Errorf("Kind = %v, want OpDivU", out.Blocks[0].Ops[0].Kind)
	}
}

// TestLiftLtUDispatch — same for comparisons.
func TestLiftLtUDispatch(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpLtS, Unsigned: true},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Ops[0].Kind != OpLtU {
		t.Errorf("Kind = %v, want OpLtU", out.Blocks[0].Ops[0].Kind)
	}
}

// TestUnsignedOpKindStrings — pin the printer output.
func TestUnsignedOpKindStrings(t *testing.T) {
	cases := []struct {
		k    OpKind
		want string
	}{
		{OpDivU, "div_u"},
		{OpRemU, "rem_u"},
		{OpShrU, "shr_u"},
		{OpLtU, "lt_u"},
		{OpLeU, "le_u"},
		{OpGtU, "gt_u"},
		{OpGeU, "ge_u"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}
