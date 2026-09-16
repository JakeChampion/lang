package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

func mustBePairForm(t *testing.T, p *Program, name string) *Func {
	t.Helper()
	fn := findFunc(p, name)
	if fn == nil {
		t.Fatalf("%s not found", name)
	}
	if countKind(fn.Ops, OpReturnPair) == 0 || countKind(fn.Ops, OpReturn) != 0 {
		t.Fatalf("%s did not lower in pair form, so this test checks nothing:\n%s", name, p)
	}
	return fn
}

// A self tail call followed by a pair return is a tail call: the call and
// the return become the parameter stores and a back-edge, as they do for
// the one-value and void returns. The source lowering cannot produce this
// shape today (the pair-form fixpoint starts empty, so a function whose
// return is its own call is never admitted), so the body is built by hand.
func TestTCORewritesAPairFormSelfTailCall(t *testing.T) {
	fn := &Func{
		Name:   "f",
		Params: []ast.Param{{Name: "n", Type: ast.NumberType{}}},
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 1},
			{Kind: OpSub},
			{Kind: OpCallDirect, Str: "f", I32: 1},
			{Kind: OpReturnPair},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	TailCallOptimize(p)
	if fn.Ops[0].Kind != OpLoop {
		t.Fatalf("expected the OpLoop wrapper at op[0], got %s:\n%s", fn.Ops[0].Kind, p)
	}
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirect && op.Str == "f" {
			t.Errorf("the self tail call survived the rewrite:\n%s", p)
		}
	}
	mustContainOp(t, p, "f", OpBr)
}

// An explicit pair return at the end of the body needs no implicit one
// after it.
func TestNoImplicitReturnFollowsAnExplicitPairReturn(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): Option[i32] {
	return Some(n);
}`)
	fn := mustBePairForm(t, p, "f")
	if got := countKind(fn.Ops, OpReturnPair); got != 1 {
		t.Errorf("one explicit pair return should lower to one OpReturnPair, got %d:\n%s", got, p)
	}
	if last := fn.Ops[len(fn.Ops)-1].Kind; last != OpReturnPair {
		t.Errorf("the body should end at its explicit return, ends with %s:\n%s", last, p)
	}
}

// A then-arm ending in a pair return is not flattened: the rewrite's typed
// if carries one result, and a (tag, payload) pair has no block type.
func TestFlattenLeavesAPairReturnAlone(t *testing.T) {
	p := lowerSource(t, `function f(n: i32): Option[i32] {
	if (n == 0) { return None; }
	return Some(n);
}`)
	mustBePairForm(t, p, "f")
	before := countKind(findFunc(p, "f").Ops, OpReturnPair)
	FlattenBranches(p)
	fn := findFunc(p, "f")
	if got := countKind(fn.Ops, OpReturnPair); got != before {
		t.Errorf("flatten changed the pair returns from %d to %d:\n%s", before, got, p)
	}
	if countKind(fn.Ops, OpElse) != 0 {
		t.Errorf("flatten rewrote a pair-returning if:\n%s", p)
	}
}

// A pair return ends a loop header's straight-line run like any other
// return: a length read after it never runs, so hoisting it before the loop
// would introduce a read the loop never made.
func TestHoistLoopInvariantsStopsAtAPairReturn(t *testing.T) {
	fn := &Func{
		Name:   "f",
		Params: []ast.Param{{Name: "s", Type: ast.StringType{}}},
		Ops: []Op{
			{Kind: OpLoop},
			{Kind: OpConstI32, I32: 0},
			{Kind: OpConstI32, I32: 0},
			{Kind: OpReturnPair},
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpStrLen},
			{Kind: OpDrop},
			{Kind: OpEnd},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	HoistLoopInvariants(p)
	if fn.Ops[0].Kind != OpLoop {
		t.Errorf("a length read after the header's pair return was hoisted before the loop:\n%s", p)
	}
	if got := countKind(fn.Ops, OpStrLen); got != 1 {
		t.Errorf("the length should still be read once, in place; read %d times:\n%s", got, p)
	}
}
