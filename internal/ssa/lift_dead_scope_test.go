package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftCountsScopesOpenedOnADeadPath — an `if` that sits after a
// `return` in the same scope is never lifted, but its `else` and `end`
// still arrive: they must be matched against THAT if, not against the
// live scope enclosing it. Inlined drop glue after a `return_pair` is the
// shape the corpus produces; the lifter refused it with "OpElse doesn't
// match an if scope".
func TestLiftCountsScopesOpenedOnADeadPath(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpReturn},
			{Kind: ir.OpConstI32, I32: 0}, // dead from here to the block's end
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 5},
			{Kind: ir.OpDrop},
			{Kind: ir.OpElse},
			{Kind: ir.OpConstI32, I32: 6},
			{Kind: ir.OpDrop},
			{Kind: ir.OpEnd},
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd},
			{Kind: ir.OpEnd},
			{Kind: ir.OpEnd}, // the live block's end
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	got, err := Eval(out)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 1 {
		t.Errorf("f() = %d, want 1 (the return ahead of the dead scopes)", got)
	}
}
