package ssa

import (
	"math"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestFoldFPromote — lossless f32 → f64.
func TestFoldFPromote(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = 1.5
	r := f.AddOp(entry, OpFPromote, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != 1.5 {
		t.Errorf("FPromote = {%v %g}, want {OpConstFloat 1.5}", got.Kind, got.F64)
	}
}

// TestFoldFDemote — lossy round to f32 precision.
func TestFoldFDemote(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstFloat)
	// A f64 value not exactly representable in f32.
	entry.Ops[0].F64 = 1.0 + math.Pi
	r := f.AddOp(entry, OpFDemote, c)
	f.SetRet(entry, r)

	Fold(f)
	wantF := float64(float32(1.0 + math.Pi))
	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != wantF {
		t.Errorf("FDemote = %g, want %g (precision-rounded)", got.F64, wantF)
	}
}

// TestFoldIToFS — signed int → float.
func TestFoldIToFS(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -7
	r := f.AddOp(entry, OpIToFS, c)
	f.SetRet(entry, r)

	Fold(f)
	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != -7.0 {
		t.Errorf("IToFS(-7) = {%v %g}, want {OpConstFloat -7.0}", got.Kind, got.F64)
	}
}

// TestFoldIToFU — unsigned int (-1 as uint64 = huge) → float.
func TestFoldIToFU(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -1
	r := f.AddOp(entry, OpIToFU, c)
	f.SetRet(entry, r)

	Fold(f)
	want := float64(^uint64(0)) // max uint64 as float64
	if got := entry.Ops[1]; got.Kind != OpConstFloat || got.F64 != want {
		t.Errorf("IToFU(-1) = %g, want %g (max uint64 as float)", got.F64, want)
	}
}

// TestFoldFToIS — float → signed int (truncating).
func TestFoldFToIS(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = -3.7
	r := f.AddOp(entry, OpFToIS, c)
	f.SetRet(entry, r)

	Fold(f)
	// Go's int64(-3.7) = -3 (truncate toward zero).
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != -3 {
		t.Errorf("FToIS(-3.7) = {%v %d}, want {OpConstInt -3}", got.Kind, got.Imm)
	}
}

// TestLiftFPromote — ir.OpFPromoteF32 → ssa.OpFPromote.
func TestLiftFPromote(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpFPromoteF32},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Ops[0].Kind != OpFPromote {
		t.Errorf("Kind = %v, want OpFPromote", out.Blocks[0].Ops[0].Kind)
	}
}

// TestLiftFConvertSignedDispatch — OpFConvertI32 with Unsigned
// flag dispatches between OpIToFS and OpIToFU.
func TestLiftFConvertSignedDispatch(t *testing.T) {
	cases := []struct {
		unsigned bool
		want     OpKind
	}{
		{false, OpIToFS},
		{true, OpIToFU},
	}
	for _, c := range cases {
		t.Run(c.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name:   "f",
				Params: []ast.Param{{Name: "a"}},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpFConvertI32, Width: 64, Unsigned: c.unsigned},
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

// TestLiftITruncSignedDispatch — OpITruncF64 dispatches between
// OpFToIS / OpFToIU.
func TestLiftITruncSignedDispatch(t *testing.T) {
	cases := []struct {
		unsigned bool
		want     OpKind
	}{
		{false, OpFToIS},
		{true, OpFToIU},
	}
	for _, c := range cases {
		t.Run(c.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name:   "f",
				Params: []ast.Param{{Name: "a"}},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpITruncF64, Width: 64, Unsigned: c.unsigned},
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

// TestFloatConvOpKindStrings — pin the printer strings.
func TestFloatConvOpKindStrings(t *testing.T) {
	cases := []struct {
		k    OpKind
		want string
	}{
		{OpFPromote, "f_promote"},
		{OpFDemote, "f_demote"},
		{OpIToFS, "i_to_f_s"},
		{OpIToFU, "i_to_f_u"},
		{OpFToIS, "f_to_i_s"},
		{OpFToIU, "f_to_i_u"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}
