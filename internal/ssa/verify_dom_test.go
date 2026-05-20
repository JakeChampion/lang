package ssa

import (
	"strings"
	"testing"
)

// TestVerifyRejectsUseBeforeDefSameBlock — a binary op
// referencing a Value defined later in the same block must
// be rejected. Catches a common pre-SSA-lift mistake: emitting
// an arg position before the producer.
func TestVerifyRejectsUseBeforeDefSameBlock(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	// Pre-mint the Value IDs the way a buggy lifting pass might.
	later := f.NewValue()
	op := &Op{Kind: OpAdd, Result: f.NewValue(), Args: []Value{later, later}}
	entry.Ops = append(entry.Ops, op)
	// Now retroactively define `later` AFTER the use site.
	def := &Op{Kind: OpConstInt, Result: later, Imm: 5}
	entry.Ops = append(entry.Ops, def)
	f.SetRet(entry, op.Result)

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for in-block use-before-def")
	}
	if !strings.Contains(err.Error(), "before its def") {
		t.Errorf("error %q doesn't mention dominance violation", err)
	}
}

// TestVerifyRejectsUseFromUnreachedSibling — entry branches to
// thenB and elseB; thenB defines v_then; elseB tries to use it.
// thenB doesn't dominate elseB so the use is illegal.
func TestVerifyRejectsUseFromUnreachedSibling(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	leak := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetRet(thenB, leak)
	// elseB illegally references leak from its sibling.
	f.SetRet(elseB, leak)

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for cross-sibling use")
	}
	if !strings.Contains(err.Error(), "before its def") {
		t.Errorf("error %q doesn't mention dominance violation", err)
	}
}

// TestVerifyAcceptsUseInDominatedSuccessor — entry defines a
// value, then unconditionally branches to next, which uses it.
// Entry dominates next, so the use is well-formed.
func TestVerifyAcceptsUseInDominatedSuccessor(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	next := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 42
	f.SetBr(entry, next)
	f.SetRet(next, c)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify rejected legal cross-block use: %v", err)
	}
}

// TestVerifyAcceptsParamEverywhere — a Param is in scope on
// entry and dominates every block. Using one in a deep
// successor is legal.
func TestVerifyAcceptsParamEverywhere(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	next := f.NewBlock()
	last := f.NewBlock()
	f.SetBr(entry, next)
	f.SetBr(next, last)
	f.SetRet(last, p)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify rejected Param use in deep successor: %v", err)
	}
}

// TestVerifyAcceptsUseInDiamondMerge — both branches of a
// diamond define independent constants; the merge block's
// terminator returns the entry's pre-branch value. Entry
// dominates merge so the use is legal.
func TestVerifyAcceptsUseInDiamondMerge(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	pre := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, pre)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify rejected legal entry-dominates-merge use: %v", err)
	}
}

// TestVerifyAcceptsLoopBodyUsesHeaderDef — header defines a
// constant; body uses it. Header dominates body so the use is
// legal even with the back-edge.
func TestVerifyAcceptsLoopBodyUsesHeaderDef(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	hv := f.AddOp(header, OpConstInt)
	header.Ops[0].Imm = 7
	f.SetBrIf(header, c, body, done)
	bodyOp := f.AddOp(body, OpAdd, hv, hv)
	f.SetBr(body, header) // back-edge
	f.SetRet(done, hv)
	_ = bodyOp

	if err := Verify(f); err != nil {
		t.Fatalf("Verify rejected legal loop-body use of header def: %v", err)
	}
}

// TestVerifyTerminatorUseSameBlockOK — terminator references
// a Value defined in the same block. Terminator runs after
// every Op, so the use is well-formed.
func TestVerifyTerminatorUseSameBlockOK(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	v := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 99
	f.SetRet(entry, v)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify rejected legal same-block terminator use: %v", err)
	}
}
