package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftMakeClosure — OpMakeClosure with 2 captures and a
// target function name.
func TestLiftMakeClosure(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 11}, // cap 0
			{Kind: ir.OpConstI32, I32: 22}, // cap 1
			{Kind: ir.OpMakeClosure, Str: "$cb_hoisted", I32: 2},
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
	// Find the make_closure op.
	var mk *Op
	for _, op := range out.Blocks[0].Ops {
		if op.Kind == OpMakeClosure {
			mk = op
			break
		}
	}
	if mk == nil {
		t.Fatalf("expected an OpMakeClosure in:\n%s", out)
	}
	if mk.Str != "$cb_hoisted" {
		t.Errorf("Str = %q, want %q", mk.Str, "$cb_hoisted")
	}
	if len(mk.Args) != 2 {
		t.Errorf("Args = %d, want 2 (captures)", len(mk.Args))
	}
}

// TestLiftMakeClosureZeroCaptures — closure with no captures.
func TestLiftMakeClosureZeroCaptures(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpMakeClosure, Str: "$cb", I32: 0},
			{Kind: ir.OpDrop},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if op := out.Blocks[0].Ops[0]; op.Kind != OpMakeClosure || len(op.Args) != 0 {
		t.Errorf("Op = {%v args=%d}, want {OpMakeClosure 0}", op.Kind, len(op.Args))
	}
}

// TestLiftMakeEnv — OpMakeEnv shares the same N-capture →
// pointer shape but emits OpMakeEnv (no Str).
func TestLiftMakeEnv(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpMakeEnv, I32: 1},
			{Kind: ir.OpDrop},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	var mk *Op
	for _, op := range out.Blocks[0].Ops {
		if op.Kind == OpMakeEnv {
			mk = op
			break
		}
	}
	if mk == nil {
		t.Fatalf("expected an OpMakeEnv")
	}
	if mk.Str != "" {
		t.Errorf("Str = %q, want empty (MakeEnv has no callee name)", mk.Str)
	}
	if len(mk.Args) != 1 {
		t.Errorf("Args = %d, want 1", len(mk.Args))
	}
}

// TestLiftMakeClosureStackUnderflow — too few captures.
func TestLiftMakeClosureStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpMakeClosure, Str: "$cb", I32: 3},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestMakeClosureIsImpure — DCE/CSE skip these.
func TestMakeClosureIsImpure(t *testing.T) {
	if IsPure(OpMakeClosure) {
		t.Error("OpMakeClosure should be impure")
	}
	if IsPure(OpMakeEnv) {
		t.Error("OpMakeEnv should be impure")
	}
}

// TestMakeClosureOpKindStrings — pin printer output.
func TestMakeClosureOpKindStrings(t *testing.T) {
	if got := OpMakeClosure.String(); got != "make_closure" {
		t.Errorf("OpMakeClosure.String() = %q, want %q", got, "make_closure")
	}
	if got := OpMakeEnv.String(); got != "make_env" {
		t.Errorf("OpMakeEnv.String() = %q, want %q", got, "make_env")
	}
}
