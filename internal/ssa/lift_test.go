package ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftConstReturn — `function f() { return 42; }` lowers
// to one const_int + ret.
func TestLiftConstReturn(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},
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
	if len(out.Blocks) != 1 || len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("got %v", out)
	}
	op := out.Blocks[0].Ops[0]
	if op.Kind != OpConstInt || op.Imm != 42 {
		t.Errorf("op = {%v %d}, want {OpConstInt 42}", op.Kind, op.Imm)
	}
	if out.Blocks[0].Term.Kind != TermRet {
		t.Errorf("Term.Kind = %v, want TermRet", out.Blocks[0].Term.Kind)
	}
}

// TestLiftAddChain — `1 + 2` lowers to two consts + an add.
func TestLiftAddChain(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpAdd},
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
	if len(out.Blocks[0].Ops) != 3 {
		t.Fatalf("Ops = %d, want 3", len(out.Blocks[0].Ops))
	}
	add := out.Blocks[0].Ops[2]
	if add.Kind != OpAdd {
		t.Errorf("add.Kind = %v, want OpAdd", add.Kind)
	}
}

// TestLiftThenOptimize — after lift, Optimize collapses
// `1 + 2` to const_int 3.
func TestLiftThenOptimize(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpAdd},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)

	if len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("Ops after Optimize = %d, want 1", len(out.Blocks[0].Ops))
	}
	if op := out.Blocks[0].Ops[0]; op.Kind != OpConstInt || op.Imm != 3 {
		t.Errorf("Op = {%v %d}, want {OpConstInt 3}", op.Kind, op.Imm)
	}
}

// TestLiftReturnVoid — function ending with OpReturnVoid
// produces `ret` (no value).
func TestLiftReturnVoid(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops:  []ir.Op{{Kind: ir.OpReturnVoid}},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Term.Kind != TermRet {
		t.Errorf("Term.Kind = %v, want TermRet", out.Blocks[0].Term.Kind)
	}
	if out.Blocks[0].Term.Value.IsValid() {
		t.Errorf("Term.Value should be zero sentinel, got %v", out.Blocks[0].Term.Value)
	}
}

// TestLiftImplicitVoid — function with no terminator falls
// through to implicit void ret.
func TestLiftImplicitVoid(t *testing.T) {
	in := &ir.Func{Name: "f"} // empty Ops

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Term.Kind != TermRet {
		t.Errorf("Term.Kind = %v, want TermRet", out.Blocks[0].Term.Kind)
	}
}

// TestLiftRejectsUnsupportedOp — OpDivS isn't in the Phase 1
// subset; lift surfaces a clear error.
func TestLiftRejectsUnsupportedOp(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpDivS}, // not yet supported
			{Kind: ir.OpReturn},
		},
	}

	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for unsupported op")
	}
	if !strings.Contains(err.Error(), "unsupported op") {
		t.Errorf("error %q doesn't mention unsupported op", err)
	}
}

// TestLiftRejectsNilInput — defensive.
func TestLiftRejectsNilInput(t *testing.T) {
	_, err := LiftFromIR(nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

// TestLiftRejectsStackUnderflow — OpAdd with no stack input
// fails with a clear message.
func TestLiftRejectsStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpAdd}, // nothing on the stack
		},
	}

	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected stack-underflow error")
	}
	if !strings.Contains(err.Error(), "operands") {
		t.Errorf("error %q doesn't mention operands", err)
	}
}
