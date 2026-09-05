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

// TestLiftCallIndirectVoidPushesNothing — `f(v)` where `f: (i32) => void`.
// The verifier pushes what the call site's signature says (nothing for a
// void result); the lift used to push one value regardless, so every stack
// height after the call was one too high (#8539).
//
// The whole-corpus TestLiftAgreesWithTheVerifiersStackModel is what caught
// that, and it stays the real gate — but it takes three minutes and reports
// a percentage, so this pins the one shape directly and in milliseconds.
func TestLiftCallIndirectVoidPushesNothing(t *testing.T) {
	lift := func(t *testing.T, result ast.Type) *Op {
		t.Helper()
		fn := &ir.Func{
			Name:       "caller",
			Params:     []ast.Param{{Name: "v", Type: ast.NumberType{}}, {Name: "fp", Type: &ast.FuncType{Params: []ast.Type{ast.NumberType{}}, Result: result}}},
			ReturnType: ast.VoidType{},
			Ops: []ir.Op{
				{Kind: ir.OpLoadLocal, I32: 0}, // v
				{Kind: ir.OpLoadLocal, I32: 1}, // fp
				{Kind: ir.OpCallIndirect, I32: 1, Ext: &ir.OpExt{
					Sig: &ast.FuncType{Params: []ast.Type{ast.NumberType{}}, Result: result},
				}},
			},
		}
		if _, isVoid := result.(ast.VoidType); !isVoid {
			fn.Ops = append(fn.Ops, ir.Op{Kind: ir.OpDrop})
		}
		fn.Ops = append(fn.Ops, ir.Op{Kind: ir.OpReturnVoid})
		out, err := LiftFromIRWith(fn, ir.NewCallShapes(&ir.Program{PtrW: 8, Funcs: []*ir.Func{fn}}))
		if err != nil {
			t.Fatalf("LiftFromIRWith(result=%v): %v", result, err)
		}
		if err := Verify(out); err != nil {
			t.Fatalf("Verify(result=%v): %v", result, err)
		}
		for _, op := range out.Blocks[0].Ops {
			if op.Kind == OpCallIndirect {
				return op
			}
		}
		t.Fatalf("no OpCallIndirect in the lifted function (result=%v)", result)
		return nil
	}

	if got := lift(t, ast.VoidType{}); got.Result.ID != 0 {
		t.Errorf("void indirect call defines %v; it must define nothing, or every later stack height is one too high", got.Result)
	}
	if got := lift(t, ast.NumberType{}); got.Result.ID == 0 {
		t.Error("i32 indirect call defines nothing; the value it returns is what the caller consumes")
	}
}
