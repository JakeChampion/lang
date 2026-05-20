package ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftCallDirectPair — pair-returning direct call. Pushes
// (tag, payload) onto the stack via OpCallPair.
func TestLiftCallDirectPair(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpCallDirectPair, Str: "maybe_parse", I32: 1},
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
	call := out.Blocks[0].Ops[0]
	if call.Kind != OpCallPair {
		t.Fatalf("Kind = %v, want OpCallPair", call.Kind)
	}
	if !call.Result.IsValid() || !call.Result2.IsValid() {
		t.Errorf("expected both Result + Result2 valid; got %+v", call)
	}
	if call.Str != "maybe_parse" {
		t.Errorf("Str = %q, want %q", call.Str, "maybe_parse")
	}
	// Terminator should consume Result + Result2.
	term := out.Blocks[0].Term
	if term.Kind != TermRetPair {
		t.Fatalf("Term.Kind = %v, want TermRetPair", term.Kind)
	}
	if term.Value != call.Result || term.Value2 != call.Result2 {
		t.Errorf("Term consumes %v/%v, want %v/%v",
			term.Value, term.Value2, call.Result, call.Result2)
	}
}

// TestLiftCallDirectPairZeroArgs — pair call with no args.
func TestLiftCallDirectPairZeroArgs(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallDirectPair, Str: "make_default", I32: 0},
			{Kind: ir.OpDrop},
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
}

// TestLiftCallDirectPairStackUnderflow — too few args.
func TestLiftCallDirectPairStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallDirectPair, Str: "foo", I32: 3},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestCallPairPrints — golden form for `v1, v2 = call_pair ...`.
func TestCallPairPrints(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	tag, payload := f.AddCallPair(entry, a)
	entry.Ops[0].Str = "go"
	f.SetRetPair(entry, tag, payload)

	got := f.String()
	want := "v2, v3 = call_pair"
	if !strings.Contains(got, want) {
		t.Errorf("Func.String() missing %q in:\n%s", want, got)
	}
}

// TestCallPairCloneRoundTrip — Clone preserves Result + Result2.
func TestCallPairCloneRoundTrip(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	tag, payload := f.AddCallPair(entry, a)
	entry.Ops[0].Str = "go"
	f.SetRetPair(entry, tag, payload)

	c := f.Clone()
	op := c.Blocks[0].Ops[0]
	if op.Kind != OpCallPair {
		t.Errorf("clone Kind = %v, want OpCallPair", op.Kind)
	}
	if op.Result.ID != tag.ID || op.Result2.ID != payload.ID {
		t.Errorf("clone results = %v/%v, want %v/%v",
			op.Result, op.Result2, tag, payload)
	}
}

// TestCallPairVerifyRejectsAlias — Result2 = Result is a
// double-definition.
func TestCallPairVerifyRejectsAlias(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	v := f.NewValue()
	// Hand-craft an Op where Result == Result2.
	op := &Op{Kind: OpCallPair, Result: v, Result2: v, Str: "x"}
	entry.Ops = append(entry.Ops, op)
	f.SetRetPair(entry, v, v)

	if err := Verify(f); err == nil {
		t.Fatal("expected double-definition error")
	}
}

// TestCallPairIsImpure — DCE/CSE skip pair calls.
func TestCallPairIsImpure(t *testing.T) {
	if IsPure(OpCallPair) {
		t.Error("OpCallPair should be impure")
	}
}
