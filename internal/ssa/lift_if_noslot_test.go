package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// ifExprIR builds `if (cond) { 1 } else { 2 }` as a value-producing OpIf whose
// result is returned, in a function with `slots` slots. Both arms fall through.
func ifExprIR(slots int) *ir.Func {
	ops := []ir.Op{{Kind: ir.OpConstI32, I32: 1}} // cond
	ops = append(ops,
		ir.Op{Kind: ir.OpIf, I32: ir.BlockTypeI32},
		ir.Op{Kind: ir.OpConstI32, I32: 1},
		ir.Op{Kind: ir.OpElse},
		ir.Op{Kind: ir.OpConstI32, I32: 2},
		ir.Op{Kind: ir.OpEnd},
		ir.Op{Kind: ir.OpReturn},
	)
	fn := &ir.Func{Name: "f", Ops: ops}
	for i := 0; i < slots; i++ {
		fn.Locals = append(fn.Locals, &ast.Var{Name: "v"})
	}
	return fn
}

// A value-producing `if` in a function with NO locals must lift: both arms fall
// through, so the then arm is a merge source.
//
// It used to be rejected as "needs both arms to fall through". The then arm's
// liveness was read off its slot snapshot being non-nil, and with zero slots
// `append([]Value(nil), l.slots...)` is nil — so every live then arm in a
// slotless function looked dead. Any `return if (c) { a } else { b }` written
// before the function's first `var` hit it; 60 of the 2048 fernsmith exit-byte
// seeds failed to compile through `-target arm64-linux -backend ssa` on it.
func TestLiftValueIfWithoutLocals(t *testing.T) {
	for _, slots := range []int{0, 1} {
		out, err := LiftFromIR(ifExprIR(slots))
		if err != nil {
			t.Fatalf("slots=%d: LiftFromIR: %v", slots, err)
		}
		if err := Verify(out); err != nil {
			t.Fatalf("slots=%d: Verify: %v", slots, err)
		}
		got, err := Eval(out)
		if err != nil {
			t.Fatalf("slots=%d: Eval: %v", slots, err)
		}
		if got != 1 {
			t.Errorf("slots=%d: Eval = %d, want 1 (the then arm)", slots, got)
		}
	}
}

// The dead-then-arm rejection the sentinel was meant to express still stands:
// a value-producing `if` whose then arm returns has no value to merge.
func TestLiftValueIfDeadThenArmStillRejected(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpReturn},
			{Kind: ir.OpElse},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturn},
		},
	}
	if _, err := LiftFromIR(in); err == nil {
		t.Fatal("LiftFromIR: want an error for a value-producing if whose then arm returns")
	}
}
