package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftFLoad — an f64-width OpFLoad → OpLoadF. The f32 width takes a
// different route; TestLiftFloatMemoryWidthPicksTheAccess covers both.
func TestLiftFLoad(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0x2000}, // addr
			{Kind: ir.OpFLoad, Width: 64},
			{Kind: ir.OpDrop},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if op := out.Blocks[0].Ops[1]; op.Kind != OpLoadF {
		t.Errorf("Kind = %v, want OpLoadF", op.Kind)
	}
}

// TestLiftFStore — an f64-width OpFStore → OpStoreF, with no result.
func TestLiftFStore(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0x2000}, // addr
			{Kind: ir.OpConstF64, F64: 3.14},   // value
			{Kind: ir.OpFStore, Width: 64},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Find the OpStoreF op.
	found := false
	for _, op := range out.Blocks[0].Ops {
		if op.Kind == OpStoreF {
			found = true
			if op.Result.IsValid() {
				t.Errorf("OpStoreF should have no Result")
			}
		}
	}
	if !found {
		t.Errorf("expected OpStoreF in:\n%s", out)
	}
}

// TestLiftFStoreStackUnderflow — too few operands.
func TestLiftFStoreStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpFStore},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestFLdStOpKindStrings — printer pinning.
func TestFLdStOpKindStrings(t *testing.T) {
	if got := OpLoadF.String(); got != "load_f" {
		t.Errorf("OpLoadF.String() = %q, want %q", got, "load_f")
	}
	if got := OpStoreF.String(); got != "store_f" {
		t.Errorf("OpStoreF.String() = %q, want %q", got, "store_f")
	}
}

// TestFLdStIsImpure — both kinds are side-effect-y.
func TestFLdStIsImpure(t *testing.T) {
	if IsPure(OpLoadF) {
		t.Error("OpLoadF should be impure")
	}
	if IsPure(OpStoreF) {
		t.Error("OpStoreF should be impure")
	}
}
