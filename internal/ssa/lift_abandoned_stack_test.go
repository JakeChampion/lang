package ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// A scope arm that TERMINATES — OpReturn, or an OpBr past the scope — never
// reaches the pop a falling-through arm performs, so whatever it pushed used to
// stay on the operand stack and shadow operands pushed BEFORE the scope.
//
// `?` inside an array or tuple literal is exactly that shape: the container's
// element address is pushed, the try desugar's early `return` abandons a value
// on top of it, and the following store reads the abandoned value as its
// address. The result lifted to SSA with a use its def does not dominate, which
// arm64-ssa turned into a SIGSEGV (#5903).
//
// The `store` must consume the value pushed before the if, not the one the dead
// arm left behind.
func TestLiftDeadArmDoesNotShadowEarlierOperand(t *testing.T) {
	in := &ir.Func{
		Name:       "f",
		ReturnType: ast.NumberType{Width: 32},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 100}, // the address, pushed before the if
			{Kind: ir.OpConstI32, I32: 1},   // cond
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 7}, // abandoned by the return below
			{Kind: ir.OpReturn},
			{Kind: ir.OpEnd},
			{Kind: ir.OpConstI32, I32: 5}, // the value
			{Kind: ir.OpStore},            // [addr, value]
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	// Verify is the load-bearing assertion: the bug's signature is exactly a
	// use its def does not dominate.
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// And name the operand, so a future change that keeps the SSA valid but
	// picks the wrong address still fails.
	var store *Op
	for _, b := range out.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpStore32 {
				store = op
			}
		}
	}
	if store == nil {
		t.Fatal("no OpStore in the lifted function")
	}
	addrDef := defOf(out, store.Args[0])
	if addrDef == nil || addrDef.Imm != 100 {
		t.Errorf("store address comes from %#v, want the const 100 pushed before the if", addrDef)
	}
}

// The same for a dead body under an OpBlock, whose close takes a different path
// in the lifter (endBlockScope rather than endIfScope).
//
// Verify is NOT asserted here: an OpBlock whose body unconditionally returns
// leaves the post-block code genuinely unreachable, which endBlockScope creates
// on purpose for PruneUnreachable to drop, and Verify's use-before-def rule
// rejects any use in an unreachable block. So this asserts the OPERAND choice —
// the only thing dropAbandonedStack is responsible for.
func TestLiftDeadBlockArmDoesNotShadowEarlierOperand(t *testing.T) {
	in := &ir.Func{
		Name:       "f",
		ReturnType: ast.NumberType{Width: 32},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 100},
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpReturn},
			{Kind: ir.OpEnd},
			{Kind: ir.OpConstI32, I32: 5},
			{Kind: ir.OpStore},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	var store *Op
	for _, b := range out.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpStore32 {
				store = op
			}
		}
	}
	if store == nil {
		t.Fatal("no store in the lifted function")
	}
	if d := defOf(out, store.Args[0]); d == nil || d.Imm != 100 {
		t.Errorf("store address comes from %#v, want the const 100 pushed before the block", d)
	}
}

// The counterpart that keeps the fix honest: a value pushed inside a void scope
// that FALLS THROUGH still flows out to the enclosing code. Truncating the
// stack unconditionally at every scope close would discard a real operand —
// which is what a first cut of this fix did, and TestLiftBlockLinear caught it.
func TestLiftLiveScopeKeepsItsPushedValue(t *testing.T) {
	in := &ir.Func{
		Name:       "f",
		ReturnType: ast.NumberType{Width: 32},
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturn}, // consumes the 42 from inside the block
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v (a live fall-through's value must survive OpEnd)", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !strings.Contains(out.String(), "42") {
		t.Errorf("the const 42 pushed inside the block was dropped:\n%s", out.String())
	}
}

// defOf finds the op defining v, or nil.
func defOf(f *Func, v Value) *Op {
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result == v {
				return op
			}
		}
	}
	return nil
}
