package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftSubLoadVariants — each sub-i32 load lifts to its SSA
// kind.
func TestLiftSubLoadVariants(t *testing.T) {
	cases := []struct {
		from ir.OpKind
		want OpKind
	}{
		{ir.OpLoadI8S, OpLoad8S},
		{ir.OpLoadByte, OpLoad8U},
		{ir.OpLoadI16S, OpLoad16S},
		{ir.OpLoadI16U, OpLoad16U},
	}
	for _, c := range cases {
		t.Run(c.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name:   "f",
				Params: []ast.Param{{Name: "addr"}},
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

// TestLiftSubStoreVariants — store8/16.
func TestLiftSubStoreVariants(t *testing.T) {
	cases := []struct {
		from ir.OpKind
		want OpKind
	}{
		{ir.OpStoreI8, OpStore8},
		{ir.OpStoreI16, OpStore16},
	}
	for _, c := range cases {
		t.Run(c.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name:   "f",
				Params: []ast.Param{{Name: "addr"}, {Name: "val"}},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpLoadLocal, I32: 1},
					{Kind: c.from},
					{Kind: ir.OpReturnVoid},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			if op := out.Blocks[0].Ops[0]; op.Kind != c.want {
				t.Errorf("Kind = %v, want %v", op.Kind, c.want)
			}
			if out.Blocks[0].Ops[0].Result.IsValid() {
				t.Errorf("store should have no Result")
			}
		})
	}
}

// TestSubLdStOpKindStrings — pin printer strings.
func TestSubLdStOpKindStrings(t *testing.T) {
	cases := []struct {
		k    OpKind
		want string
	}{
		{OpLoad8S, "load8_s"},
		{OpLoad8U, "load8_u"},
		{OpLoad16S, "load16_s"},
		{OpLoad16U, "load16_u"},
		{OpStore8, "store8"},
		{OpStore16, "store16"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}

// TestSubLdStIsImpure — DCE keeps unused sub-load/store ops.
func TestSubLdStIsImpure(t *testing.T) {
	impure := []OpKind{OpLoad8S, OpLoad8U, OpLoad16S, OpLoad16U, OpStore8, OpStore16}
	for _, k := range impure {
		if IsPure(k) {
			t.Errorf("IsPure(%v) = true, want false", k)
		}
	}
}
