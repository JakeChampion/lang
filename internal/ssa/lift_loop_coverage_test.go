package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftLoopWithConditionalStore — mimics a pattern that
// failed in the wild: a loop body that conditionally stores
// to a local. Should not trip phi dominance.
//
//	var x = 0;
//	loop {
//	    if (cond) { x = 1; }
//	    br_if 1 0   // exit if some other cond
//	    br 0        // continue
//	}
//	return x;
func TestLiftLoopWithConditionalStore(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "cond1"}, {Name: "cond2"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			// x = 0
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpStoreLocal, I32: 2},
			// outer block for break
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			// loop
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			// if (cond1) { x = 1 }
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpStoreLocal, I32: 2},
			{Kind: ir.OpEnd}, // close if
			// brif cond2 to outer block (exit)
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpBrIf, I32: 1},
			// continue loop
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd}, // close loop
			{Kind: ir.OpEnd}, // close block
			// return x
			{Kind: ir.OpLoadLocal, I32: 2},
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
}

// TestLiftNestedLoopWithStore — nested loops where the outer
// store occurs after the inner loop's exit. The outer phi at
// the outer loop's header needs the inner-loop's exit Value
// as its back-edge arg.
func TestLiftNestedLoopWithStore(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpStoreLocal, I32: 1},
			// outer loop
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			// inner loop
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			// x = x + 1
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 1},
			// br_if 0 — inner loop back-edge
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpBrIf, I32: 0},
			{Kind: ir.OpEnd}, // close inner loop
			// x = x * 2 (after inner loop's fall-through)
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpMul},
			{Kind: ir.OpStoreLocal, I32: 1},
			// br 0 — outer loop back-edge
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd}, // close outer loop
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

// TestLiftLoopStoreThenBrIf — store, then brif-back, then more
// body, then br-back. Multiple back-edges to the same header.
func TestLiftLoopStoreThenBrIf(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			// x = x + 1
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 1},
			// brif c to loop header (back-edge 1)
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpBrIf, I32: 0},
			// x = x * 2
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpMul},
			{Kind: ir.OpStoreLocal, I32: 1},
			// br back to loop header (back-edge 2)
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd},
			{Kind: ir.OpLoadLocal, I32: 1},
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
}
