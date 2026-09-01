package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// termOf returns the terminator of the MBlock whose last instruction is a
// SetCmp, which is the branch a backend can fuse.
func cmpBlockTerm(t *testing.T, p *Program) Term {
	t.Helper()
	for _, b := range p.Blocks {
		if n := len(b.Insts); n > 0 && b.Insts[n-1].Op == SetCmp {
			return b.Term
		}
	}
	t.Fatalf("no block ends in a comparison")
	return Term{}
}

// A comparison the branch is the only reader of needs no 0/1 in a register on a
// flags machine, and CondFuse is how the emitter says so.
func TestCondFuseSetWhenTheBranchIsTheComparisonsOnlyReader(t *testing.T) {
	f := ssa.NewFunc("main")
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	a := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 3
	b := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 4
	f.SetBrIf(e, f.AddOp(e, ssa.OpLt, a, b), then, els)
	f.SetRet(then, a)
	f.SetRet(els, b)

	p, err := Emit(f, 4)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	term := cmpBlockTerm(t, p)
	if !term.CondFuse {
		t.Errorf("CondFuse not set on a branch that is the comparison's only reader")
	}
}

// A second reader means the 0/1 has to exist, so the annotation must stay off.
func TestCondFuseClearWhenTheComparisonIsReadTwice(t *testing.T) {
	f := ssa.NewFunc("main")
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	a := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 3
	b := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 4
	lt := f.AddOp(e, ssa.OpLt, a, b)
	f.SetBrIf(e, lt, then, els)
	f.SetRet(then, lt) // the second reader
	f.SetRet(els, b)

	p, err := Emit(f, 4)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if term := cmpBlockTerm(t, p); term.CondFuse {
		t.Errorf("CondFuse set on a comparison a second site reads")
	}
}

// CondFuse is an annotation, not a semantic change: the SetCmp is still emitted
// and still defines CondReg, so Run — which ignores the flag — must be
// unaffected.
func TestCondFuseLeavesTheModelSemanticsAlone(t *testing.T) {
	f := ssa.NewFunc("main")
	e, then, els := f.NewBlock(), f.NewBlock(), f.NewBlock()
	a := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 3
	b := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = 4
	f.SetBrIf(e, f.AddOp(e, ssa.OpLt, a, b), then, els)
	f.SetRet(then, a)
	f.SetRet(els, b)

	want, err := ssa.Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	p, err := Emit(f, 4)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got, err := Run(p, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != want {
		t.Errorf("Run = %d, want Eval = %d", got, want)
	}
}
