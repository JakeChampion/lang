package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftMatchTag — OpMatchTag pops ptr, pushes the tag at [ptr+0]
// (lifted as OpLoad).
func TestLiftMatchTag(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0x1000}, // ptr (arbitrary)
			{Kind: ir.OpMatchTag},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Expect: const_int 0x1000, then load.
	if len(out.Blocks[0].Ops) < 2 {
		t.Fatalf("Ops len = %d, want ≥ 2", len(out.Blocks[0].Ops))
	}
	if out.Blocks[0].Ops[1].Kind != OpLoad {
		t.Errorf("Op[1].Kind = %v, want OpLoad", out.Blocks[0].Ops[1].Kind)
	}
}

// TestLiftMatchTagStackUnderflow — no ptr on stack.
func TestLiftMatchTagStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpMatchTag},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestLiftCallClosureDirect — `closure_helper(arg, env)`.
// Lifts to OpCall with Str=callee, Args=[arg, env].
func TestLiftCallClosureDirect(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},   // arg
			{Kind: ir.OpConstI32, I32: 1000}, // env_ptr
			{Kind: ir.OpCallClosureDirect, Str: "$cb_hoisted", I32: 2},
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
	// Expect the third op to be the OpCall.
	call := out.Blocks[0].Ops[2]
	if call.Kind != OpCall || call.Str != "$cb_hoisted" {
		t.Errorf("call = {%v %q}, want {OpCall $cb_hoisted}", call.Kind, call.Str)
	}
	if len(call.Args) != 2 {
		t.Errorf("Args = %d, want 2", len(call.Args))
	}
}

// TestLiftCallClosureDirectStackUnderflow — too few args.
func TestLiftCallClosureDirectStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallClosureDirect, Str: "$cb", I32: 3},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}
