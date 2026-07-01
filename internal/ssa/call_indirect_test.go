package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftConstFunc — a bare function value (OpConstFunc) lifts to a
// zero-capture OpMakeClosure carrying the target name, so it derefs as a
// {fn_idx, env_ptr=0} cell through OpCallIndirect just like a real closure.
func TestLiftConstFunc(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstFunc, Str: "target"},
			{Kind: ir.OpDrop},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	op := out.Blocks[0].Ops[0]
	if op.Kind != OpMakeClosure || op.Str != "target" {
		t.Errorf("Op = {%v %q}, want {OpMakeClosure \"target\"}", op.Kind, op.Str)
	}
	if len(op.Args) != 0 {
		t.Errorf("zero-capture closure should have no capture args, got %d", len(op.Args))
	}
}

// TestLiftCallIndirect — `f_ptr(a, b)`. IR stack: [a, b,
// callee_idx]. Lift pops callee, then args; emits SSA
// OpCallIndirect with Args = [callee, a, b].
func TestLiftCallIndirect(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}, {Name: "fp"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0}, // a
			{Kind: ir.OpLoadLocal, I32: 1}, // b
			{Kind: ir.OpLoadLocal, I32: 2}, // fp
			{Kind: ir.OpCallIndirect, I32: 2},
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
	call := out.Blocks[0].Ops[0]
	if call.Kind != OpCallIndirect {
		t.Errorf("Kind = %v, want OpCallIndirect", call.Kind)
	}
	// Expected Args = [callee=fp(v3), arg0=a(v1), arg1=b(v2)].
	want := []Value{out.Params[2], out.Params[0], out.Params[1]}
	if len(call.Args) != 3 {
		t.Fatalf("Args len = %d, want 3", len(call.Args))
	}
	for i, w := range want {
		if call.Args[i] != w {
			t.Errorf("Args[%d] = %v, want %v", i, call.Args[i], w)
		}
	}
}

// TestLiftCallIndirectZeroArgs — `fp()` zero-arg case.
func TestLiftCallIndirectZeroArgs(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "fp"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpCallIndirect, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	call := out.Blocks[0].Ops[0]
	if call.Kind != OpCallIndirect {
		t.Errorf("Kind = %v, want OpCallIndirect", call.Kind)
	}
	if len(call.Args) != 1 {
		t.Errorf("Args = %d, want 1 (just the callee)", len(call.Args))
	}
}

// TestLiftCallIndirectStackUnderflow — too few args fails clean.
func TestLiftCallIndirectStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallIndirect, I32: 2},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestCallIndirectIsImpure — DCE keeps unused OpCallIndirect
// results.
func TestCallIndirectIsImpure(t *testing.T) {
	if IsPure(OpCallIndirect) {
		t.Error("OpCallIndirect should be impure")
	}
}

// TestCallIndirectOpKindString — printer pinning.
func TestCallIndirectOpKindString(t *testing.T) {
	if got := OpCallIndirect.String(); got != "call_indirect" {
		t.Errorf("OpCallIndirect.String() = %q, want %q", got, "call_indirect")
	}
}
