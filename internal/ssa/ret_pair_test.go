package ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftReturnPair — OpReturnPair pops (tag, payload) and
// terminates with TermRetPair.
func TestLiftReturnPair(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpMakeSomeI32}, // pushes (tag=0, payload=42)
			{Kind: ir.OpReturnPair},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	term := out.Blocks[0].Term
	if term.Kind != TermRetPair {
		t.Errorf("Term.Kind = %v, want TermRetPair", term.Kind)
	}
	if !term.Value.IsValid() || !term.Value2.IsValid() {
		t.Errorf("Term values invalid: %+v", term)
	}
}

// TestLiftReturnPairStackUnderflow — OpReturnPair with < 2
// stack values fails.
func TestLiftReturnPairStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpReturnPair}, // only 1 value
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for missing payload")
	}
}

// TestReturnPairPrints — golden printer form for ret_pair.
func TestReturnPairPrints(t *testing.T) {
	f := NewFunc("f")
	tag := f.AddParam()
	payload := f.AddParam()
	entry := f.NewBlock()
	f.SetRetPair(entry, tag, payload)

	got := f.String()
	want := "ret_pair v1, v2"
	if !strings.Contains(got, want) {
		t.Errorf("Func.String() missing %q in:\n%s", want, got)
	}
}

// TestReturnPairCloneRoundTrip — Clone preserves Value2.
func TestReturnPairCloneRoundTrip(t *testing.T) {
	f := NewFunc("f")
	tag := f.AddParam()
	payload := f.AddParam()
	entry := f.NewBlock()
	f.SetRetPair(entry, tag, payload)

	c := f.Clone()
	if c.Blocks[0].Term.Kind != TermRetPair {
		t.Errorf("clone Term.Kind = %v, want TermRetPair", c.Blocks[0].Term.Kind)
	}
	if c.Blocks[0].Term.Value2.ID != payload.ID {
		t.Errorf("clone Value2.ID = %d, want %d", c.Blocks[0].Term.Value2.ID, payload.ID)
	}
}

// TestReturnPairUsesIndexed — BuildUses records both Value
// and Value2 as terminator uses.
func TestReturnPairUsesIndexed(t *testing.T) {
	f := NewFunc("f")
	tag := f.AddParam()
	payload := f.AddParam()
	entry := f.NewBlock()
	f.SetRetPair(entry, tag, payload)

	u := BuildUses(f)
	if u.Count(tag) != 1 {
		t.Errorf("tag use count = %d, want 1", u.Count(tag))
	}
	if u.Count(payload) != 1 {
		t.Errorf("payload use count = %d, want 1", u.Count(payload))
	}
}

// TestReturnPairVerifyDom — both Values must dominate the
// terminator slot.
func TestReturnPairVerifyDom(t *testing.T) {
	// Construct a CFG where the second Value is defined in a
	// sibling branch, not dominating the terminator block.
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	tag := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 0
	f.SetRet(thenB, Value{})
	bad := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 99
	f.SetRetPair(elseB, tag, bad) // tag defined in thenB, not visible here!

	if err := Verify(f); err == nil {
		t.Fatal("expected dominance error for cross-arm Value use")
	}
}
