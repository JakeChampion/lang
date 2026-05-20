package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftMakeSomeI32 — pops payload, pushes (tag=0, payload).
func TestLiftMakeSomeI32(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42}, // payload
			{Kind: ir.OpMakeSomeI32},
			// After: stack = [tag=0 (v2), payload=42 (v1)].
			// Drop both before void return.
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
	// Expect: const 42 (payload), const 0 (tag) — that's 2 const_int ops.
	consts := 0
	for _, op := range out.Blocks[0].Ops {
		if op.Kind == OpConstInt {
			consts++
		}
	}
	if consts != 2 {
		t.Errorf("const count = %d, want 2 (payload + tag)", consts)
	}
}

// TestLiftMakeNoneI32 — pushes (tag=1, payload=0). No pop.
func TestLiftMakeNoneI32(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpMakeNoneI32},
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
	// Expect: const 1 (tag) + const 0 (payload). After Optimize+
	// DCE both are dropped (unused).
}

// TestLiftMakeOkI32 — same shape as MakeSome (Ok=tag 0).
func TestLiftMakeOkI32(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpMakeOkI32},
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

// TestLiftMakeErrI32 — Err with tag=1.
func TestLiftMakeErrI32(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 99},
			{Kind: ir.OpMakeErrI32},
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
	// Find the tag const_int and assert Imm=1.
	hasTagOne := false
	for _, op := range out.Blocks[0].Ops {
		if op.Kind == OpConstInt && op.Imm == 1 {
			hasTagOne = true
			break
		}
	}
	if !hasTagOne {
		t.Errorf("expected const_int 1 (Err tag) in:\n%s", out)
	}
}

// TestLiftMakeSomeStackUnderflow — MakeSome with no payload.
func TestLiftMakeSomeStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpMakeSomeI32},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for missing payload")
	}
}
