package ssa

import (
	"strings"
	"testing"
)

// TestStatsSimple — counts blocks, ops, params for the
// canonical `f(a, b) = a + b` shape.
func TestStatsSimple(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	s := f.Stats()
	if s.Blocks != 1 {
		t.Errorf("Blocks = %d, want 1", s.Blocks)
	}
	if s.Ops != 1 {
		t.Errorf("Ops = %d, want 1", s.Ops)
	}
	if s.Params != 2 {
		t.Errorf("Params = %d, want 2", s.Params)
	}
	if s.MaxBlockOps != 1 {
		t.Errorf("MaxBlockOps = %d, want 1", s.MaxBlockOps)
	}
	if s.Terminators[TermRet] != 1 {
		t.Errorf("TermRet count = %d, want 1", s.Terminators[TermRet])
	}
}

// TestStatsPhiAndConst — phi/const counters bucketed
// correctly.
func TestStatsPhiAndConst(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	s := f.Stats()
	if s.Phis != 1 {
		t.Errorf("Phis = %d, want 1", s.Phis)
	}
	if s.Consts != 2 {
		t.Errorf("Consts = %d, want 2 (two const_int)", s.Consts)
	}
	if s.Blocks != 4 {
		t.Errorf("Blocks = %d, want 4", s.Blocks)
	}
	if s.Terminators[TermBrIf] != 1 {
		t.Errorf("BrIf count = %d, want 1", s.Terminators[TermBrIf])
	}
	if s.Terminators[TermBr] != 2 {
		t.Errorf("Br count = %d, want 2", s.Terminators[TermBr])
	}
	if s.Terminators[TermRet] != 1 {
		t.Errorf("Ret count = %d, want 1", s.Terminators[TermRet])
	}
}

// TestStatsOpKindsHistogram — counts ops bucketed by OpKind.
// Useful for tracking which kinds an optimization pass
// reduces.
func TestStatsOpKindsHistogram(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	f.AddOp(entry, OpAdd, a, b)
	f.AddOp(entry, OpAdd, a, b)
	f.AddOp(entry, OpMul, a, b)
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[3].Imm = 7
	f.SetRet(entry, c)

	s := f.Stats()
	if s.OpKinds[OpAdd] != 2 {
		t.Errorf("OpKinds[OpAdd] = %d, want 2", s.OpKinds[OpAdd])
	}
	if s.OpKinds[OpMul] != 1 {
		t.Errorf("OpKinds[OpMul] = %d, want 1", s.OpKinds[OpMul])
	}
	if s.OpKinds[OpConstInt] != 1 {
		t.Errorf("OpKinds[OpConstInt] = %d, want 1", s.OpKinds[OpConstInt])
	}
	if s.OpKinds[OpSub] != 0 {
		t.Errorf("OpKinds[OpSub] = %d, want 0 (no such op)", s.OpKinds[OpSub])
	}
}

// TestStatsBeforeAndAfterOptimize — Optimize should reduce
// the op count for a constant-arithmetic chain.
func TestStatsBeforeAndAfterOptimize(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 2
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[2].Imm = 5
	sum := f.AddOp(entry, OpAdd, a, b)
	r := f.AddOp(entry, OpMul, sum, c)
	f.SetRet(entry, r)

	before := f.Stats()
	if before.Ops != 5 {
		t.Fatalf("before.Ops = %d, want 5", before.Ops)
	}

	Optimize(f)
	after := f.Stats()
	if after.Ops != 1 {
		t.Errorf("after.Ops = %d, want 1 (one folded const)", after.Ops)
	}
	if after.Consts != 1 {
		t.Errorf("after.Consts = %d, want 1", after.Consts)
	}
}

// TestStatsString — Stats.String renders the expected
// single-line summary form.
func TestStatsString(t *testing.T) {
	s := Stats{Blocks: 3, Reachable: 2, Ops: 7, Phis: 1, Consts: 2, Params: 1, MaxBlockOps: 4}
	got := s.String()
	for _, want := range []string{
		"blocks=3", "reachable=2", "ops=7", "phis=1", "consts=2", "params=1", "max_block_ops=4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Stats.String() = %q missing %q", got, want)
		}
	}
}

// TestStatsReachableUnderInOrphan — a Block disconnected from
// Entry (so unreachable) bumps Blocks but not Reachable.
// PruneUnreachable would drop it next, but Stats reports the
// pre-prune snapshot.
func TestStatsReachableUnderInOrphan(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	orphan := f.NewBlock() // never reached from entry
	f.SetRet(entry, Value{})
	f.SetRet(orphan, Value{})

	s := f.Stats()
	if s.Blocks != 2 {
		t.Errorf("Blocks = %d, want 2", s.Blocks)
	}
	if s.Reachable != 1 {
		t.Errorf("Reachable = %d, want 1 (orphan excluded)", s.Reachable)
	}
}

// TestStatsReachableEqualsBlocksOnHealthyFunc — for a normal
// well-formed function with no orphans, Reachable == Blocks.
func TestStatsReachableEqualsBlocksOnHealthyFunc(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	mid := f.NewBlock()
	f.SetBr(entry, mid)
	f.SetRet(mid, a)

	s := f.Stats()
	if s.Reachable != s.Blocks {
		t.Errorf("Reachable=%d, Blocks=%d; want equal on healthy func",
			s.Reachable, s.Blocks)
	}
	if s.Reachable != 2 {
		t.Errorf("Reachable = %d, want 2", s.Reachable)
	}
}

// TestStatsNilFunc — defensive.
func TestStatsNilFunc(t *testing.T) {
	var f *Func
	s := f.Stats()
	if s.Blocks != 0 || s.Ops != 0 {
		t.Errorf("nil Stats = %+v, want zero", s)
	}
}

// TestStatsMaxBlockOps — verifies the longest-block tracker.
func TestStatsMaxBlockOps(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	small := f.NewBlock()
	big := f.NewBlock()
	f.AddOp(small, OpAdd, a, a)
	f.SetBr(small, big)
	for i := 0; i < 7; i++ {
		f.AddOp(big, OpAdd, a, a)
	}
	f.SetRet(big, Value{})

	s := f.Stats()
	if s.MaxBlockOps != 7 {
		t.Errorf("MaxBlockOps = %d, want 7", s.MaxBlockOps)
	}
}
