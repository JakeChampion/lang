package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftAlloc — OpAlloc with a size arg → ssa.OpAlloc.
func TestLiftAlloc(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 16}, // size
			{Kind: ir.OpAlloc},
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
	if op := out.Blocks[0].Ops[1]; op.Kind != OpAlloc {
		t.Errorf("Kind = %v, want OpAlloc", op.Kind)
	}
}

// TestLiftAllocStackUnderflow — no size operand.
func TestLiftAllocStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpAlloc},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestLiftEnumSentinel — OpEnumSentinel with tag in I32 →
// ssa.OpEnumSentinel with Imm = tag.
func TestLiftEnumSentinel(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpEnumSentinel, I32: 3},
			{Kind: ir.OpDrop},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	op := out.Blocks[0].Ops[0]
	if op.Kind != OpEnumSentinel {
		t.Errorf("Kind = %v, want OpEnumSentinel", op.Kind)
	}
	if op.Imm != 3 {
		t.Errorf("Imm = %d, want 3", op.Imm)
	}
}

// TestAllocIsImpure — DCE keeps unused allocations.
func TestAllocIsImpure(t *testing.T) {
	if IsPure(OpAlloc) {
		t.Error("OpAlloc should be impure")
	}
}

// TestEnumSentinelIsPure — same tag → same address, so CSE
// can dedupe and DCE can drop unused.
func TestEnumSentinelIsPure(t *testing.T) {
	if !IsPure(OpEnumSentinel) {
		t.Error("OpEnumSentinel should be pure")
	}
}

// TestAllocSentinelOpKindStrings — printer pinning.
func TestAllocSentinelOpKindStrings(t *testing.T) {
	if got := OpAlloc.String(); got != "alloc" {
		t.Errorf("OpAlloc.String() = %q, want %q", got, "alloc")
	}
	if got := OpEnumSentinel.String(); got != "enum_sentinel" {
		t.Errorf("OpEnumSentinel.String() = %q, want %q", got, "enum_sentinel")
	}
}

// TestEnumSentinelPrints — golden form: `v1 = enum_sentinel 3`.
func TestEnumSentinelPrints(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	v := f.AddOp(entry, OpEnumSentinel)
	entry.Ops[0].Imm = 7
	f.SetRet(entry, v)

	got := f.String()
	want := "v1 = enum_sentinel 7"
	if !containsSubstring(got, want) {
		t.Errorf("Func.String() missing %q in:\n%s", want, got)
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
